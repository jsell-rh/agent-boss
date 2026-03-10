package coordinator

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestInferAgentStatus verifies the status inference logic for various tmux observations.
func TestInferAgentStatus(t *testing.T) {
	tests := []struct {
		exists        bool
		idle          bool
		needsApproval bool
		want          string
	}{
		{exists: false, idle: false, needsApproval: false, want: "session_missing"},
		{exists: true, idle: false, needsApproval: true, want: "waiting_approval"},
		{exists: true, idle: true, needsApproval: false, want: "idle"},
		{exists: true, idle: false, needsApproval: false, want: "working"},
	}
	for _, tc := range tests {
		got := inferAgentStatus(tc.exists, tc.idle, tc.needsApproval)
		if got != tc.want {
			t.Errorf("inferAgentStatus(%v, %v, %v) = %q, want %q",
				tc.exists, tc.idle, tc.needsApproval, got, tc.want)
		}
	}
}

// TestCheckStaleness verifies that the staleness checker marks active agents
// that have not posted recently and clears staleness for done/idle agents.
func TestCheckStaleness(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "TestStaleness"

	// Register an active agent
	postJSON(t, base+"/spaces/"+space+"/agent/Alpha", map[string]interface{}{
		"status":  "active",
		"summary": "Alpha: working",
	})

	// Manually set UpdatedAt to far in the past to simulate staleness
	srv.mu.Lock()
	ks, _ := srv.spaces[space]
	canonical := resolveAgentName(ks, "Alpha")
	ks.agentStatus(canonical).UpdatedAt = time.Now().UTC().Add(-(StalenessThreshold + 5*time.Minute))
	srv.mu.Unlock()

	// Run the staleness check
	srv.checkStaleness()

	// Agent should be marked stale
	srv.mu.RLock()
	stale := ks.agentStatus(canonical).Stale
	srv.mu.RUnlock()

	if !stale {
		t.Error("expected alpha to be marked stale after missing StalenessThreshold")
	}

	// Post a fresh update — staleness should clear on next check
	postJSON(t, base+"/spaces/"+space+"/agent/Alpha", map[string]interface{}{
		"status":  "active",
		"summary": "Alpha: back",
	})

	srv.checkStaleness()

	srv.mu.RLock()
	stale = ks.agentStatus(canonical).Stale
	srv.mu.RUnlock()

	if stale {
		t.Error("expected alpha staleness to be cleared after fresh update")
	}
}

// TestStalenessNotMarkedForIdleDone verifies that idle/done agents are never marked stale.
func TestStalenessNotMarkedForIdleDone(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "TestStalenessIdleDone"

	for _, status := range []string{"idle", "done"} {
		name := strings.ToUpper(status[:1]) + status[1:]
		postJSON(t, base+"/spaces/"+space+"/agent/"+name, map[string]interface{}{
			"status":  status,
			"summary": status + ": done",
		})
	}

	// Set UpdatedAt to far past
	srv.mu.Lock()
	ks, _ := srv.spaces[space]
	for _, rec := range ks.Agents {
		a := rec.Status
		if a == nil { continue }
		a.UpdatedAt = time.Now().UTC().Add(-(StalenessThreshold + time.Hour))
	}
	srv.mu.Unlock()

	srv.checkStaleness()

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	for name, rec := range ks.Agents {
		a := rec.Status
		if a == nil { continue }
		if a.Stale {
			t.Errorf("agent %q with status %q should not be stale", name, a.Status)
		}
	}
}

// TestAgentIntrospect verifies the introspect endpoint returns agent info.
func TestAgentIntrospect(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "TestIntrospect"

	// Register an agent without a tmux session
	postJSON(t, base+"/spaces/"+space+"/agent/Rover", map[string]interface{}{
		"status":  "active",
		"summary": "Rover: active",
	})

	// Introspect the agent — no session registered, so session_exists should be false
	code, body := getBody(t, base+"/spaces/"+space+"/agent/Rover/introspect")
	if code != http.StatusOK {
		t.Fatalf("introspect returned %d: %s", code, body)
	}

	var resp introspectResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal introspect response: %v", err)
	}

	if resp.Agent != "Rover" {
		t.Errorf("expected agent=Rover, got %q", resp.Agent)
	}
	if resp.SessionExists {
		t.Error("expected session_exists=false for agent with no tmux session")
	}
	if resp.Lines == nil {
		t.Error("expected lines to be non-nil (empty slice)")
	}
	// Unregistered agent has no explicit non-tmux type, so tmux_available should be true.
	if !resp.TmuxAvailable {
		t.Error("expected tmux_available=true for agent with no registration (type unknown)")
	}
}

// TestAgentIntrospectNonTmux verifies that agents registered with a non-tmux
// agent_type have tmux_available=false in the introspect response.
func TestAgentIntrospectNonTmux(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "TestIntrospectNonTmux"

	// Register the agent via /register with agent_type=http
	regResp := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/Webhook/register", "Webhook", map[string]interface{}{
		"agent_type": "http",
	})
	regResp.Body.Close()

	// Post a status so the agent appears in the space
	postJSON(t, base+"/spaces/"+space+"/agent/Webhook", map[string]interface{}{
		"status":  "active",
		"summary": "Webhook: active",
	})

	code, body := getBody(t, base+"/spaces/"+space+"/agent/Webhook/introspect")
	if code != http.StatusOK {
		t.Fatalf("introspect returned %d: %s", code, body)
	}

	var resp introspectResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal introspect response: %v", err)
	}

	if resp.TmuxAvailable {
		t.Error("expected tmux_available=false for agent_type=http")
	}
	if resp.SessionExists {
		t.Error("expected session_exists=false for http agent")
	}
}

// TestLifecycleNonTmuxAgentReturns422 verifies that spawn, stop, and restart
// return HTTP 422 for agents registered with a non-tmux agent_type.
func TestLifecycleNonTmuxAgentReturns422(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "TestLifecycle422"

	// Register and post status for an http agent
	regResp := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/HttpBot/register", "HttpBot", map[string]interface{}{
		"agent_type": "http",
	})
	regResp.Body.Close()

	postJSON(t, base+"/spaces/"+space+"/agent/HttpBot", map[string]interface{}{
		"status":  "active",
		"summary": "HttpBot: active",
	})

	doPost := func(path string) int {
		req, err := http.NewRequest(http.MethodPost, base+path, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("X-Agent-Name", "HttpBot")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// /stop should return 422
	if code := doPost("/spaces/" + space + "/agent/HttpBot/stop"); code != http.StatusUnprocessableEntity {
		t.Errorf("/stop: expected 422, got %d", code)
	}
	// /restart should return 422
	if code := doPost("/spaces/" + space + "/agent/HttpBot/restart"); code != http.StatusUnprocessableEntity {
		t.Errorf("/restart: expected 422, got %d", code)
	}
	// /spawn should return 422
	if code := doPost("/spaces/" + space + "/agent/HttpBot/spawn"); code != http.StatusUnprocessableEntity {
		t.Errorf("/spawn: expected 422, got %d", code)
	}
}

// TestAgentIntrospectNotFound verifies 404 for unknown agents.
func TestAgentIntrospectNotFound(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "TestIntrospect404"

	// Create the space first
	postJSON(t, base+"/spaces/"+space+"/agent/Seed", map[string]interface{}{
		"status":  "idle",
		"summary": "Seed: idle",
	})

	code, _ := getBody(t, base+"/spaces/"+space+"/agent/NoSuchAgent/introspect")
	if code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown agent, got %d", code)
	}
}

// TestMessagePriority verifies that messages accept valid priority values and
// reject invalid ones. It also checks that the default priority is "info".
func TestMessagePriority(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "TestMsgPriority"

	// Register target agent
	postJSON(t, base+"/spaces/"+space+"/agent/Target", map[string]interface{}{
		"status":  "active",
		"summary": "Target: active",
	})

	postMsg := func(sender, msg, priority string) *http.Response {
		body := map[string]interface{}{"message": msg}
		if priority != "" {
			body["priority"] = priority
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost,
			base+"/spaces/"+space+"/agent/Target/message",
			strings.NewReader(string(data)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Name", sender)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST message: %v", err)
		}
		return resp
	}

	// Default priority (no priority field) should succeed
	resp := postMsg("boss", "hello", "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("empty priority: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Valid priorities
	for _, p := range []string{"info", "directive", "urgent"} {
		resp = postMsg("boss", "test "+p, p)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("priority %q: expected 200, got %d", p, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Invalid priority should be rejected
	resp = postMsg("boss", "bad priority", "critical")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid priority: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify default priority was stored as "info"
	srv.mu.RLock()
	ks := srv.spaces[space]
	canonical := resolveAgentName(ks, "Target")
	messages := ks.agentStatus(canonical).Messages
	srv.mu.RUnlock()

	if len(messages) == 0 {
		t.Fatal("expected messages to be stored")
	}
	if messages[0].Priority != PriorityInfo {
		t.Errorf("expected default priority %q, got %q", PriorityInfo, messages[0].Priority)
	}
}

// TestAgentSpawnMethodNotAllowed verifies spawn rejects GET.
func TestAgentSpawnMethodNotAllowed(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "TestSpawnMethod"

	code, _ := getBody(t, base+"/spaces/"+space+"/agent/Alpha/spawn")
	if code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET /spawn, got %d", code)
	}
}

// TestAgentStopNotFound verifies stop returns 404 for unknown space.
func TestAgentStopNotFound(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)

	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/NoSuchSpace/agent/Alpha/stop", nil)
	req.Header.Set("X-Agent-Name", "Alpha")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST stop: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown space, got %d", resp.StatusCode)
	}
}
