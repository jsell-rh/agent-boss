package coordinator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Compile-time interface compliance (exercised as test to be explicit).
func TestAmbientInterfaceCompliance(t *testing.T) {
	var _ SessionBackend = (*AmbientSessionBackend)(nil)
	var _ SessionLifecycle = (*AmbientSessionBackend)(nil)
	var _ SessionObserver = (*AmbientSessionBackend)(nil)
	var _ SessionActor = (*AmbientSessionBackend)(nil)
}

func TestAmbientName(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{})
	if b.Name() != "ambient" {
		t.Fatalf("expected name 'ambient', got %q", b.Name())
	}
}

func newTestAmbientBackend(t *testing.T, handler http.Handler) (*AmbientSessionBackend, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	b := NewAmbientSessionBackend(AmbientBackendConfig{
		APIURL:  ts.URL,
		Token:   "test-token",
		Project: "test-project",
	})
	return b, ts
}

// backendCR is a test helper that builds a backendSessionCR with common fields.
func backendCR(name, phase, displayName string, labels map[string]string) backendSessionCR {
	cr := backendSessionCR{}
	cr.Metadata.Name = name
	cr.Metadata.Labels = labels
	cr.Status.Phase = phase
	cr.Spec.DisplayName = displayName
	return cr
}

const testSessionsPath = "/api/projects/test-project/agentic-sessions"

func TestAmbientAvailable(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		json.NewEncoder(w).Encode(backendSessionList{})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	// First call should hit the server.
	if !b.Available() {
		t.Fatal("expected available")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 call, got %d", atomic.LoadInt32(&calls))
	}

	// Second call within 30s should use cache.
	if !b.Available() {
		t.Fatal("expected available (cached)")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 call (cached), got %d", atomic.LoadInt32(&calls))
	}

	// Expire cache and verify new call.
	b.availMu.Lock()
	b.availAt = time.Now().Add(-31 * time.Second)
	b.availMu.Unlock()
	if !b.Available() {
		t.Fatal("expected available (cache expired)")
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", atomic.LoadInt32(&calls))
	}
}

func TestAmbientAvailableUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // 502 = unavailable
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if b.Available() {
		t.Fatal("expected unavailable for 502")
	}
}

func TestAmbientAvailable4xxIsAvailable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // 401 = API reachable
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if !b.Available() {
		t.Fatal("expected available for 401 (API reachable)")
	}
}

func TestAmbientCreateSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		// Verify headers.
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth header")
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["initialPrompt"] != "do something" {
			t.Errorf("unexpected initialPrompt: %v", body["initialPrompt"])
		}
		if body["displayName"] != "test-agent" {
			t.Errorf("unexpected displayName: %v", body["displayName"])
		}
		if body["runnerType"] != "claude-agent-sdk" {
			t.Errorf("unexpected runnerType: %v", body["runnerType"])
		}
		if body["timeout"] == nil {
			t.Error("missing timeout")
		}
		// Verify labels are present.
		labels, ok := body["labels"].(map[string]interface{})
		if !ok {
			t.Error("missing labels")
		} else {
			if labels["managed-by"] != "agent-boss" {
				t.Errorf("unexpected managed-by label: %v", labels["managed-by"])
			}
			if labels["boss-agent"] != "test-agent" {
				t.Errorf("unexpected boss-agent label: %v", labels["boss-agent"])
			}
			if labels["boss-space"] != "test-space" {
				t.Errorf("unexpected boss-space label: %v", labels["boss-space"])
			}
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"name": "sess-123"})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	id, err := b.CreateSession(context.Background(), SessionCreateOpts{
		Command: "do something",
		BackendOpts: AmbientCreateOpts{
			DisplayName: "test-agent",
			SpaceName:   "test-space",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "sess-123" {
		t.Fatalf("expected sess-123, got %q", id)
	}
}

func TestAmbientCreateSessionFallbackDisplayName(t *testing.T) {
	var receivedDisplayName string
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if dn, ok := body["displayName"].(string); ok {
			receivedDisplayName = dn
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"name": "sess-456"})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	_, err := b.CreateSession(context.Background(), SessionCreateOpts{
		SessionID: "my-session",
		Command:   "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receivedDisplayName != "my-session" {
		t.Fatalf("expected displayName 'my-session', got %q", receivedDisplayName)
	}
}

func TestAmbientKillSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/sess-123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if err := b.KillSession(context.Background(), "sess-123"); err != nil {
		t.Fatal(err)
	}
}

func TestAmbientKillSession404IsSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/gone", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if err := b.KillSession(context.Background(), "gone"); err != nil {
		t.Fatalf("404 should be success, got: %v", err)
	}
}

func TestAmbientSessionExists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/exists", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(backendCR("exists", "Running", "", nil))
	})
	mux.HandleFunc(testSessionsPath+"/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if !b.SessionExists("exists") {
		t.Fatal("expected exists=true")
	}
	if b.SessionExists("missing") {
		t.Fatal("expected exists=false")
	}
}

func TestAmbientListSessions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(backendSessionList{Items: []backendSessionCR{
			backendCR("s1", "Running", "", nil),
			backendCR("s2", "Completed", "", nil),
			backendCR("s3", "Pending", "", nil),
		}})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	ids, err := b.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != "s1" || ids[2] != "s3" {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestAmbientGetStatus(t *testing.T) {
	tests := []struct {
		name     string
		phase    string
		expected SessionStatus
	}{
		{"pending", "Pending", SessionStatusPending},
		{"completed", "Completed", SessionStatusCompleted},
		{"failed", "Failed", SessionStatusFailed},
		{"running", "Running", SessionStatusRunning},
		{"lowercase running", "running", SessionStatusRunning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(testSessionsPath+"/s1", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(backendCR("s1", tt.phase, "", nil))
			})
			b, ts := newTestAmbientBackend(t, mux)
			defer ts.Close()

			status, err := b.GetStatus(context.Background(), "s1")
			if err != nil {
				t.Fatal(err)
			}
			if status != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, status)
			}
		})
	}
}

func TestAmbientGetStatus404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	status, err := b.GetStatus(context.Background(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	if status != SessionStatusMissing {
		t.Fatalf("expected missing, got %q", status)
	}
}

func TestAmbientIsIdle(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1", func(w http.ResponseWriter, r *http.Request) {
		// Running phase -> SessionStatusRunning (no longer idle without /runs)
		json.NewEncoder(w).Encode(backendCR("s1", "Running", "", nil))
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	// Without /runs endpoint, running sessions are not considered idle.
	if b.IsIdle("s1") {
		t.Fatal("expected not idle for running session")
	}
}

func TestAmbientCaptureOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1/export", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientExportResponse{
			LegacyMessages: []ambientExportMessage{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "world"},
				{Role: "user", Content: "third"},
			},
		})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	lines, err := b.CaptureOutput("s1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "[assistant] world" {
		t.Fatalf("unexpected line 0: %q", lines[0])
	}
	if lines[1] != "[user] third" {
		t.Fatalf("unexpected line 1: %q", lines[1])
	}
}

func TestAmbientCaptureOutputTruncation(t *testing.T) {
	longContent := strings.Repeat("x", 300)
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1/export", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientExportResponse{
			LegacyMessages: []ambientExportMessage{
				{Role: "user", Content: longContent},
			},
		})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	lines, err := b.CaptureOutput("s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	// "[user] " = 7 chars + 197 truncated + "..." = 207
	if len(lines[0]) > 210 {
		t.Fatalf("line too long, expected truncation: len=%d", len(lines[0]))
	}
	if !strings.HasSuffix(lines[0], "...") {
		t.Fatal("expected truncation suffix")
	}
}

func TestAmbientCaptureOutputAguiFallback(t *testing.T) {
	snapshot, _ := json.Marshal(map[string]interface{}{
		"type": "MESSAGES_SNAPSHOT",
		"messages": []ambientExportMessage{
			{Role: "user", Content: "prompt"},
			{Role: "assistant", Content: "response"},
		},
	})
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1/export", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientExportResponse{
			LegacyMessages: nil, // empty — forces aguiEvents fallback
			AguiEvents:     []json.RawMessage{snapshot},
		})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	lines, err := b.CaptureOutput("s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines from aguiEvents fallback, got %d", len(lines))
	}
	if lines[0] != "[user] prompt" {
		t.Fatalf("unexpected line 0: %q", lines[0])
	}
	if lines[1] != "[assistant] response" {
		t.Fatalf("unexpected line 1: %q", lines[1])
	}
}

func TestAmbientCaptureOutputAguiLastSnapshot(t *testing.T) {
	// When multiple MESSAGES_SNAPSHOT events exist, use the last one.
	snap1, _ := json.Marshal(map[string]interface{}{
		"type": "MESSAGES_SNAPSHOT",
		"messages": []ambientExportMessage{
			{Role: "user", Content: "old"},
		},
	})
	snap2, _ := json.Marshal(map[string]interface{}{
		"type": "MESSAGES_SNAPSHOT",
		"messages": []ambientExportMessage{
			{Role: "user", Content: "old"},
			{Role: "assistant", Content: "latest"},
		},
	})
	other, _ := json.Marshal(map[string]interface{}{
		"type": "TEXT_MESSAGE_START",
	})
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1/export", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientExportResponse{
			AguiEvents: []json.RawMessage{snap1, other, snap2},
		})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	lines, err := b.CaptureOutput("s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines from last snapshot, got %d", len(lines))
	}
	if lines[1] != "[assistant] latest" {
		t.Fatalf("expected last snapshot, got: %q", lines[1])
	}
}

func TestAmbientCheckApproval(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{})
	approval := b.CheckApproval("anything")
	if approval.NeedsApproval {
		t.Fatal("ambient should never need approval")
	}
}

func TestAmbientSendInput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1/agui/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		// Verify AG-UI envelope structure.
		msgs, ok := body["messages"].([]interface{})
		if !ok || len(msgs) != 1 {
			t.Fatalf("expected 1 message in envelope, got %v", body["messages"])
		}
		msg := msgs[0].(map[string]interface{})
		if msg["role"] != "user" {
			t.Errorf("expected role 'user', got %v", msg["role"])
		}
		if msg["content"] != "/boss.check agent1 space1" {
			t.Errorf("unexpected content: %v", msg["content"])
		}
		if msg["id"] == nil || msg["id"] == "" {
			t.Error("expected non-empty message id")
		}
		w.WriteHeader(http.StatusAccepted)
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if err := b.SendInput("s1", "/boss.check agent1 space1"); err != nil {
		t.Fatal(err)
	}
}

func TestAmbientApprove(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{})
	if err := b.Approve("anything"); err != nil {
		t.Fatalf("approve should be no-op: %v", err)
	}
}

func TestAmbientInterrupt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1/agui/interrupt", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if err := b.Interrupt(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
}

func TestAmbientDiscoverSessions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(backendSessionList{Items: []backendSessionCR{
			backendCR("s1", "Running", "agent-a", map[string]string{"boss-agent": "agent-a", "managed-by": "agent-boss"}),
			backendCR("s2", "Completed", "agent-b", map[string]string{"boss-agent": "agent-b"}),
			backendCR("s3", "Pending", "agent-c", map[string]string{"boss-agent": "agent-c"}),
			backendCR("s4", "Running", "", nil),
		}})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	discovered, err := b.DiscoverSessions()
	if err != nil {
		t.Fatal(err)
	}

	// Only running and pending sessions with a name should be discovered.
	if len(discovered) != 2 {
		t.Fatalf("expected 2 discovered, got %d: %v", len(discovered), discovered)
	}
	if discovered["agent-a"] != "s1" {
		t.Fatalf("expected agent-a -> s1, got %q", discovered["agent-a"])
	}
	if discovered["agent-c"] != "s3" {
		t.Fatalf("expected agent-c -> s3, got %q", discovered["agent-c"])
	}
}

func TestAmbientDiscoverSessionsFallbackDisplayName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		// Session without boss-agent label but with displayName.
		json.NewEncoder(w).Encode(backendSessionList{Items: []backendSessionCR{
			backendCR("s1", "Running", "agent-x", nil),
		}})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	discovered, err := b.DiscoverSessions()
	if err != nil {
		t.Fatal(err)
	}
	if discovered["agent-x"] != "s1" {
		t.Fatalf("expected agent-x -> s1, got %v", discovered)
	}
}

func TestAmbientWaitForRunning(t *testing.T) {
	callCount := int32(0)
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		phase := "Pending"
		if n >= 3 {
			phase = "Running"
		}
		json.NewEncoder(w).Encode(backendCR("s1", phase, "", nil))
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := b.waitForRunning(ctx, "s1", 10*time.Second)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if atomic.LoadInt32(&callCount) < 3 {
		t.Fatalf("expected at least 3 calls, got %d", atomic.LoadInt32(&callCount))
	}
}

func TestAmbientWaitForRunningFailed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath+"/s1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(backendCR("s1", "Failed", "", nil))
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := b.waitForRunning(ctx, "s1", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for failed session")
	}
	if !strings.Contains(err.Error(), "failed to start") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAmbientDoRequestHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer my-token" {
			t.Error("missing or wrong auth header")
		}
		// X-Ambient-Project header should NOT be set (project is in URL path now).
		if r.Header.Get("X-Ambient-Project") != "" {
			t.Error("X-Ambient-Project header should not be set")
		}
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	b := NewAmbientSessionBackend(AmbientBackendConfig{
		APIURL:  ts.URL,
		Token:   "my-token",
		Project: "my-project",
	})

	resp, err := b.doRequest(context.Background(), http.MethodGet, "/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestAmbientDefaultTimeout(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{})
	if b.timeout != 900 {
		t.Fatalf("expected default timeout 900, got %d", b.timeout)
	}
}

func TestAmbientCustomTimeout(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{Timeout: 3600})
	if b.timeout != 3600 {
		t.Fatalf("expected timeout 3600, got %d", b.timeout)
	}
}

func TestAmbientSessionsPath(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{Project: "my-proj"})
	expected := "/api/projects/my-proj/agentic-sessions"
	if b.sessionsPath() != expected {
		t.Fatalf("expected %q, got %q", expected, b.sessionsPath())
	}
}

func TestAmbientSessionPath(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{Project: "my-proj"})
	expected := "/api/projects/my-proj/agentic-sessions/sess-1"
	if b.sessionPath("sess-1") != expected {
		t.Fatalf("expected %q, got %q", expected, b.sessionPath("sess-1"))
	}
}

func TestAmbientCreateSessionRejectsInvalidLabelValue(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{
		APIURL:  "http://localhost",
		Project: "test",
	})

	// Space name with spaces is invalid for K8s labels.
	_, err := b.CreateSession(context.Background(), SessionCreateOpts{
		Command: "test",
		BackendOpts: AmbientCreateOpts{
			DisplayName: "agent1",
			SpaceName:   "My Space Name",
		},
	})
	if err == nil {
		t.Fatal("expected error for space name with spaces")
	}
	if !strings.Contains(err.Error(), "not a valid Kubernetes label") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent name with spaces is also invalid.
	_, err = b.CreateSession(context.Background(), SessionCreateOpts{
		Command: "test",
		BackendOpts: AmbientCreateOpts{
			DisplayName: "my agent",
			SpaceName:   "valid-space",
		},
	})
	if err == nil {
		t.Fatal("expected error for agent name with spaces")
	}
}

func TestValidLabelValue(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"", true},
		{"simple", true},
		{"with-hyphens", true},
		{"with_underscores", true},
		{"with.dots", true},
		{"MixedCase123", true},
		{"a", true},
		{"has spaces", false},
		{"-starts-with-hyphen", false},
		{"ends-with-hyphen-", false},
		{"has/slash", false},
		{"has:colon", false},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := validLabelValue(tt.value); got != tt.valid {
				t.Errorf("validLabelValue(%q) = %v, want %v", tt.value, got, tt.valid)
			}
		})
	}
}

// --- Group 5: Negative / Contract Tests ---

func TestAmbientCreateResponseMissingName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		// Return empty object — no "name" field.
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	id, err := b.CreateSession(context.Background(), SessionCreateOpts{
		Command: "test",
		BackendOpts: AmbientCreateOpts{
			DisplayName: "agent1",
			SpaceName:   "valid-space",
		},
	})
	// Should return empty string but no error from the JSON decode itself.
	// The caller (spawnAgentService) should detect the empty session ID.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "" {
		t.Fatalf("expected empty session ID from missing name field, got %q", id)
	}
}

func TestAmbientListResponseBareArray(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		// Return a bare array instead of {"items": [...]} — this is the wrong shape.
		json.NewEncoder(w).Encode([]backendSessionCR{
			backendCR("s1", "Running", "", nil),
		})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	ids, err := b.ListSessions()
	// A bare array cannot be decoded into backendSessionList (a struct).
	// ListSessions should return an error (not panic).
	if err == nil {
		t.Fatalf("expected error for bare array response, got %d sessions", len(ids))
	}
	if !strings.Contains(err.Error(), "decode session list") {
		t.Fatalf("expected decode error, got: %v", err)
	}
}

func TestGenerateMsgID(t *testing.T) {
	id := generateMsgID()
	if len(id) != 32 { // 16 bytes = 32 hex chars
		t.Fatalf("expected 32-char hex id, got %q (len=%d)", id, len(id))
	}
	// Ensure two calls produce different IDs.
	id2 := generateMsgID()
	if id == id2 {
		t.Fatal("expected unique IDs")
	}
}
