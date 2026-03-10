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

func TestAmbientAvailable(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		json.NewEncoder(w).Encode(ambientSessionList{})
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
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		// Verify headers.
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth header")
		}
		if r.Header.Get("X-Ambient-Project") != "test-project" {
			t.Error("missing project header")
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["task"] != "do something" {
			t.Errorf("unexpected task: %v", body["task"])
		}
		if body["display_name"] != "test-agent" {
			t.Errorf("unexpected display_name: %v", body["display_name"])
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(ambientCreateResponse{ID: "sess-123"})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	id, err := b.CreateSession(context.Background(), SessionCreateOpts{
		Command: "do something",
		BackendOpts: AmbientCreateOpts{
			DisplayName: "test-agent",
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
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if dn, ok := body["display_name"].(string); ok {
			receivedDisplayName = dn
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(ambientCreateResponse{ID: "sess-456"})
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
		t.Fatalf("expected display_name 'my-session', got %q", receivedDisplayName)
	}
}

func TestAmbientKillSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions/sess-123", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/sessions/gone", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/sessions/exists", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientSession{ID: "exists", Status: "running"})
	})
	mux.HandleFunc("/sessions/missing", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientSessionList{
			Items: []ambientSession{
				{ID: "s1"},
				{ID: "s2"},
				{ID: "s3"},
			},
		})
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
		status   string
		runs     []ambientRun
		expected SessionStatus
	}{
		{"pending", "pending", nil, SessionStatusPending},
		{"completed", "completed", nil, SessionStatusCompleted},
		{"failed", "failed", nil, SessionStatusFailed},
		{"running with active run", "running", []ambientRun{{Status: "running"}}, SessionStatusRunning},
		{"running with completed run", "running", []ambientRun{{Status: "completed"}}, SessionStatusIdle},
		{"running with no runs", "running", nil, SessionStatusIdle},
		{"running with error run", "running", []ambientRun{{Status: "error"}}, SessionStatusIdle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/sessions/s1", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(ambientSession{ID: "s1", Status: tt.status})
			})
			mux.HandleFunc("/sessions/s1/runs", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(ambientRunList{Items: tt.runs})
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
	mux.HandleFunc("/sessions/missing", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/sessions/s1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientSession{ID: "s1", Status: "running"})
	})
	mux.HandleFunc("/sessions/s1/runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientRunList{Items: []ambientRun{{Status: "completed"}}})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if !b.IsIdle("s1") {
		t.Fatal("expected idle")
	}
}

func TestAmbientCaptureOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions/s1/output", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "transcript" {
			t.Error("expected format=transcript")
		}
		json.NewEncoder(w).Encode(ambientTranscriptOutput{
			Messages: []ambientTranscriptMessage{
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
	mux.HandleFunc("/sessions/s1/output", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientTranscriptOutput{
			Messages: []ambientTranscriptMessage{
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

func TestAmbientCheckApproval(t *testing.T) {
	b := NewAmbientSessionBackend(AmbientBackendConfig{})
	approval := b.CheckApproval("anything")
	if approval.NeedsApproval {
		t.Fatal("ambient should never need approval")
	}
}

func TestAmbientSendInput(t *testing.T) {
	var receivedContent string
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions/s1/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		receivedContent = body["content"]
		w.WriteHeader(http.StatusAccepted)
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	if err := b.SendInput("s1", "/boss.check agent1 space1"); err != nil {
		t.Fatal(err)
	}
	if receivedContent != "/boss.check agent1 space1" {
		t.Fatalf("unexpected content: %q", receivedContent)
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
	mux.HandleFunc("/sessions/s1/interrupt", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientSessionList{
			Items: []ambientSession{
				{ID: "s1", DisplayName: "agent-a", Status: "running"},
				{ID: "s2", DisplayName: "agent-b", Status: "completed"},
				{ID: "s3", DisplayName: "agent-c", Status: "pending"},
				{ID: "s4", DisplayName: "", Status: "running"},
			},
		})
	})
	b, ts := newTestAmbientBackend(t, mux)
	defer ts.Close()

	discovered, err := b.DiscoverSessions()
	if err != nil {
		t.Fatal(err)
	}

	// Only running and pending sessions with display_name should be discovered.
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

func TestAmbientWaitForRunning(t *testing.T) {
	callCount := int32(0)
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions/s1", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		status := "pending"
		if n >= 3 {
			status = "running"
		}
		json.NewEncoder(w).Encode(ambientSession{ID: "s1", Status: status})
	})
	mux.HandleFunc("/sessions/s1/runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientRunList{})
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
	mux.HandleFunc("/sessions/s1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ambientSession{ID: "s1", Status: "failed"})
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
		if r.Header.Get("X-Ambient-Project") != "my-project" {
			t.Error("missing or wrong project header")
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
