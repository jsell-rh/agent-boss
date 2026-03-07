package coordinator

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAgentLifecycleFull exercises the complete agent lifecycle over HTTP:
// register → post status → send message → read via /messages?since= → heartbeat.
// This is an end-to-end contract test — it validates every response field and
// cursor handoff without relying on tmux or launching real agents.
func TestAgentLifecycleFull(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "LifecycleTest"
	agent := "Rover"

	// Step 1: Register the agent with heartbeat tracking.
	regResp := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/register", agent, map[string]any{
		"agent_type":             "http",
		"capabilities":           []string{"code", "test"},
		"heartbeat_interval_sec": 30,
	})
	if regResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(regResp.Body)
		t.Fatalf("register: got %d: %s", regResp.StatusCode, body)
	}
	regResp.Body.Close()

	var regBody map[string]any
	data, _ := io.ReadAll(regResp.Body)
	// Re-read via fresh GET since body is already drained above — use
	// postJSONWithCaller return directly next time. Decode from regResp.
	// Actually postJSONWithCaller already called .Do() — re-register to decode.
	regResp2 := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/register", agent, map[string]any{
		"agent_type":             "http",
		"capabilities":           []string{"code", "test"},
		"heartbeat_interval_sec": 30,
	})
	defer regResp2.Body.Close()
	data, _ = io.ReadAll(regResp2.Body)
	if err := json.Unmarshal(data, &regBody); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if regBody["status"] != "registered" {
		t.Errorf("register response status = %v, want registered", regBody["status"])
	}
	if regBody["agent_type"] != "http" {
		t.Errorf("register response agent_type = %v, want http", regBody["agent_type"])
	}

	// Step 2: Post a status update.
	statusResp := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent, agent, map[string]any{
		"status":  "active",
		"summary": agent + ": lifecycle test running",
		"branch":  "feat/integration-tests",
	})
	if statusResp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(statusResp.Body)
		t.Fatalf("post status: got %d: %s", statusResp.StatusCode, body)
	}
	statusResp.Body.Close()

	// Step 3: Send a message to the agent.
	before := time.Now().UTC()
	msgResp := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/message", "Boss", map[string]any{
		"message": "run the integration tests",
	})
	defer msgResp.Body.Close()
	if msgResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(msgResp.Body)
		t.Fatalf("send message: got %d: %s", msgResp.StatusCode, body)
	}
	var msgBody map[string]any
	if err := json.NewDecoder(msgResp.Body).Decode(&msgBody); err != nil {
		t.Fatalf("decode message response: %v", err)
	}
	if msgBody["status"] != "delivered" {
		t.Errorf("message status = %v, want delivered", msgBody["status"])
	}
	if _, ok := msgBody["messageId"]; !ok {
		t.Error("message response missing messageId field")
	}
	if msgBody["recipient"] != agent {
		t.Errorf("message recipient = %v, want %s", msgBody["recipient"], agent)
	}

	// Step 4: Read messages via /messages (no since filter) — should have 1 message.
	code, msgsRaw := getBody(t, base+"/spaces/"+space+"/agent/"+agent+"/messages")
	if code != http.StatusOK {
		t.Fatalf("GET /messages: got %d: %s", code, msgsRaw)
	}
	var msgsBody map[string]any
	if err := json.Unmarshal([]byte(msgsRaw), &msgsBody); err != nil {
		t.Fatalf("decode /messages response: %v", err)
	}
	msgs, ok := msgsBody["messages"].([]any)
	if !ok {
		t.Fatalf("messages field missing or not array")
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg0 := msgs[0].(map[string]any)
	if msg0["message"] != "run the integration tests" {
		t.Errorf("message text = %v, want %q", msg0["message"], "run the integration tests")
	}
	if msg0["sender"] != "Boss" {
		t.Errorf("message sender = %v, want Boss", msg0["sender"])
	}
	cursor, ok := msgsBody["cursor"].(string)
	if !ok || cursor == "" {
		t.Error("cursor field missing or empty")
	}

	// Step 5: Poll again with ?since=cursor — should return 0 new messages.
	code2, msgsRaw2 := getBody(t, base+"/spaces/"+space+"/agent/"+agent+"/messages?since="+cursor)
	if code2 != http.StatusOK {
		t.Fatalf("GET /messages?since=cursor: got %d", code2)
	}
	var msgsBody2 map[string]any
	if err := json.Unmarshal([]byte(msgsRaw2), &msgsBody2); err != nil {
		t.Fatalf("decode /messages?since response: %v", err)
	}
	msgs2 := msgsBody2["messages"].([]any)
	if len(msgs2) != 0 {
		t.Errorf("expected 0 messages after cursor, got %d", len(msgs2))
	}

	// Step 6: Send a second message after the cursor.
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/message", "Boss", map[string]any{
		"message": "check in please",
	})

	// Step 7: Poll with cursor — should return 1 new message.
	code3, msgsRaw3 := getBody(t, base+"/spaces/"+space+"/agent/"+agent+"/messages?since="+cursor)
	if code3 != http.StatusOK {
		t.Fatalf("GET /messages?since=cursor after 2nd message: got %d", code3)
	}
	var msgsBody3 map[string]any
	if err := json.Unmarshal([]byte(msgsRaw3), &msgsBody3); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	msgs3 := msgsBody3["messages"].([]any)
	if len(msgs3) != 1 {
		t.Errorf("expected 1 new message after cursor, got %d", len(msgs3))
	}

	// Step 8: Send heartbeat — agent is registered, should return ok.
	hbResp := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/heartbeat", agent, nil)
	defer hbResp.Body.Close()
	if hbResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(hbResp.Body)
		t.Fatalf("heartbeat: got %d: %s", hbResp.StatusCode, body)
	}
	var hbBody map[string]any
	if err := json.NewDecoder(hbResp.Body).Decode(&hbBody); err != nil {
		t.Fatalf("decode heartbeat response: %v", err)
	}
	if hbBody["status"] != "ok" {
		t.Errorf("heartbeat status = %v, want ok", hbBody["status"])
	}

	_ = before
}

// TestHeartbeatRequiresRegistration verifies that /heartbeat returns 400
// when the agent has not called /register first.
func TestHeartbeatRequiresRegistration(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "HeartbeatReg"
	agent := "Zephyr"

	// Create the agent via status post (no register call)
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent, agent, map[string]any{
		"status":  "active",
		"summary": agent + ": active",
	})

	// Heartbeat without registration must fail
	hbResp := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/heartbeat", agent, nil)
	defer hbResp.Body.Close()
	if hbResp.StatusCode != http.StatusBadRequest {
		t.Errorf("heartbeat without registration: got %d, want 400", hbResp.StatusCode)
	}
}

// TestMessagesUnknownSpaceReturns404 verifies that /messages for a non-existent
// space returns 404 rather than 200.
func TestMessagesUnknownSpaceReturns404(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, _ := getBody(t, base+"/spaces/does-not-exist/agent/nobody/messages")
	if code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown space, got %d", code)
	}
}

// TestMessagesKnownSpaceUnknownAgentReturns200Empty verifies that /messages
// for an unknown agent in a known space returns 200 with empty messages.
func TestMessagesKnownSpaceUnknownAgentReturns200Empty(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "MsgEmpty"

	// Create the space
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/Seed", "Seed", map[string]any{
		"status":  "idle",
		"summary": "Seed: idle",
	})

	code, body := getBody(t, base+"/spaces/"+space+"/agent/NoSuchAgent/messages")
	if code != http.StatusOK {
		t.Errorf("expected 200 for unknown agent in known space, got %d", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	msgs := resp["messages"].([]any)
	if len(msgs) != 0 {
		t.Errorf("expected empty messages, got %d", len(msgs))
	}
	if _, ok := resp["cursor"]; !ok {
		t.Error("cursor field missing for empty messages response")
	}
}

// TestStopNoRegisteredSession verifies that /stop returns 400 when the agent
// exists but has no registered tmux session.
func TestStopNoRegisteredSession(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "StopTest"
	agent := "Alpine"

	// Create agent (no tmux session registered)
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent, agent, map[string]any{
		"status":  "active",
		"summary": agent + ": active",
	})

	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/"+agent+"/stop", nil)
	req.Header.Set("X-Agent-Name", agent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST stop: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("stop without tmux session: got %d, want 400", resp.StatusCode)
	}
}

// TestRestartNoRegisteredSession verifies that /restart returns 400 when the
// agent has no registered tmux session.
func TestRestartNoRegisteredSession(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "RestartTest"
	agent := "Bravo"

	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent, agent, map[string]any{
		"status":  "active",
		"summary": agent + ": active",
	})

	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/"+agent+"/restart", nil)
	req.Header.Set("X-Agent-Name", agent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST restart: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("restart without tmux session: got %d, want 400", resp.StatusCode)
	}
}

// TestStopMethodNotAllowed verifies that GET /stop returns 405.
func TestStopMethodNotAllowed(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, _ := getBody(t, base+"/spaces/any/agent/any/stop")
	if code != http.StatusMethodNotAllowed {
		t.Errorf("GET /stop: got %d, want 405", code)
	}
}

// TestRestartMethodNotAllowed verifies that GET /restart returns 405.
func TestRestartMethodNotAllowed(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, _ := getBody(t, base+"/spaces/any/agent/any/restart")
	if code != http.StatusMethodNotAllowed {
		t.Errorf("GET /restart: got %d, want 405", code)
	}
}

// TestIntrospectResponseStructure verifies that /introspect returns a complete
// JSON response with all expected fields when the agent has no tmux session.
func TestIntrospectResponseStructure(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "IntrospectStruct"
	agent := "Charlie"

	// Register with capabilities
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/register", agent, map[string]any{
		"agent_type":             "cli",
		"capabilities":           []string{"review"},
		"heartbeat_interval_sec": 60,
	})
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent, agent, map[string]any{
		"status":  "active",
		"summary": agent + ": active",
	})

	code, body := getBody(t, base+"/spaces/"+space+"/agent/"+agent+"/introspect")
	if code != http.StatusOK {
		t.Fatalf("introspect: got %d: %s", code, body)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode introspect response: %v", err)
	}

	// Required fields
	for _, field := range []string{"agent", "space", "session_exists", "lines"} {
		if _, ok := resp[field]; !ok {
			t.Errorf("introspect response missing field %q", field)
		}
	}
	if resp["agent"] != agent {
		t.Errorf("introspect agent = %v, want %s", resp["agent"], agent)
	}
	if resp["space"] != space {
		t.Errorf("introspect space = %v, want %s", resp["space"], space)
	}
	// No tmux session registered → session_exists must be false
	if resp["session_exists"] != false {
		t.Errorf("session_exists = %v, want false (no session)", resp["session_exists"])
	}
	// lines must be non-nil (empty slice acceptable)
	if resp["lines"] == nil {
		t.Error("lines field must not be nil")
	}
}

// TestMessageCursorAdvancesAfterEachMessage verifies that the cursor returned
// by /messages is always strictly after the last message timestamp, so
// back-to-back polls with the cursor yield non-overlapping message sets.
func TestMessageCursorAdvancesAfterEachMessage(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "CursorAdv"
	agent := "Delta"

	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent, agent, map[string]any{
		"status":  "active",
		"summary": agent + ": active",
	})

	// Send 3 messages
	for i := 0; i < 3; i++ {
		postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/message", "Sender", map[string]any{
			"message": strings.Repeat("x", i+1),
		})
	}

	// Read all
	_, r1 := getBody(t, base+"/spaces/"+space+"/agent/"+agent+"/messages")
	var b1 map[string]any
	json.Unmarshal([]byte(r1), &b1)
	if len(b1["messages"].([]any)) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(b1["messages"].([]any)))
	}
	cursor1 := b1["cursor"].(string)

	// Send 2 more
	for i := 0; i < 2; i++ {
		postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/message", "Sender", map[string]any{
			"message": strings.Repeat("y", i+1),
		})
	}

	// Poll with cursor — should get exactly 2 new messages
	_, r2 := getBody(t, base+"/spaces/"+space+"/agent/"+agent+"/messages?since="+cursor1)
	var b2 map[string]any
	json.Unmarshal([]byte(r2), &b2)
	if len(b2["messages"].([]any)) != 2 {
		t.Errorf("expected 2 messages after cursor, got %d", len(b2["messages"].([]any)))
	}
	cursor2 := b2["cursor"].(string)
	if cursor2 <= cursor1 {
		t.Errorf("cursor did not advance: cursor1=%s cursor2=%s", cursor1, cursor2)
	}

	// Poll with cursor2 — should get 0 messages
	_, r3 := getBody(t, base+"/spaces/"+space+"/agent/"+agent+"/messages?since="+cursor2)
	var b3 map[string]any
	json.Unmarshal([]byte(r3), &b3)
	if len(b3["messages"].([]any)) != 0 {
		t.Errorf("expected 0 messages after cursor2, got %d", len(b3["messages"].([]any)))
	}

	// Verify the second batch has the right messages
	msg0 := b2["messages"].([]any)[0].(map[string]any)
	if msg0["message"] != "y" {
		t.Errorf("expected first message in batch2 to be 'y', got %v", msg0["message"])
	}

	_ = time.Now()
}

// TestRegistrationPersistsAcrossStatusUpdates verifies that agent registration
// data (agent_type, capabilities) persists after subsequent status POSTs.
func TestRegistrationPersistsAcrossStatusUpdates(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "RegPersist"
	agent := "Echo"

	// Register
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/register", agent, map[string]any{
		"agent_type":   "script",
		"capabilities": []string{"data-processing"},
	})

	// Post status update (should not clear registration)
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent, agent, map[string]any{
		"status":  "active",
		"summary": agent + ": processing data",
	})
	postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent, agent, map[string]any{
		"status":  "done",
		"summary": agent + ": done",
	})

	// Heartbeat should still work — registration persists
	hbResp := postJSONWithCaller(t, base+"/spaces/"+space+"/agent/"+agent+"/heartbeat", agent, nil)
	defer hbResp.Body.Close()
	if hbResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(hbResp.Body)
		t.Errorf("heartbeat after status updates: got %d: %s", hbResp.StatusCode, body)
	}
}
