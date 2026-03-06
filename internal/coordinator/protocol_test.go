package coordinator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// postJSONWithCaller posts JSON with an explicit X-Agent-Name header.
func postJSONWithCaller(t *testing.T, url, callerName string, payload any) *http.Response {
	t.Helper()
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", callerName)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func TestAgentRegisterMissingHeader(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	data, _ := json.Marshal(map[string]any{"agent_type": "http"})
	resp, err := http.Post(base+"/spaces/test/agent/Alpha/register", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 when X-Agent-Name missing, got %d", resp.StatusCode)
	}
}

func TestAgentRegisterDefaultsAgentType(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postJSONWithCaller(t, base+"/spaces/test/agent/Beta/register", "Beta", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("register: got %d, body: %s", resp.StatusCode, body)
	}

	rec, ok := srv.GetRegistration("test", "Beta")
	if !ok {
		t.Fatal("registration not found")
	}
	if rec.Registration.AgentType != "unknown" {
		t.Errorf("expected agent_type=unknown, got %q", rec.Registration.AgentType)
	}
}

func TestAgentRegisterCreatesAgentIfMissing(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postJSONWithCaller(t, base+"/spaces/test/agent/Gamma/register", "Gamma", map[string]any{
		"agent_type": "http",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("register: got %d, body: %s", resp.StatusCode, body)
	}

	status, body := getBody(t, base+"/spaces/test/agent/Gamma")
	if status != http.StatusOK {
		t.Fatalf("GET agent: got %d, body: %s", status, body)
	}
	var agent AgentUpdate
	json.Unmarshal([]byte(body), &agent)
	if agent.Status != StatusIdle {
		t.Errorf("expected idle status, got %q", agent.Status)
	}
}

func TestAgentMessagesSinceFilter(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSONWithCaller(t, base+"/spaces/test/agent/Echo", "Echo", map[string]any{
		"status":  "active",
		"summary": "Echo: online",
	})

	before := time.Now().UTC()
	time.Sleep(5 * time.Millisecond)

	postJSONWithCaller(t, base+"/spaces/test/agent/Echo/message", "Boss", map[string]any{
		"message": "first message",
	})
	postJSONWithCaller(t, base+"/spaces/test/agent/Echo/message", "Boss", map[string]any{
		"message": "second message",
	})

	// All messages
	resp, _ := http.Get(base + "/spaces/test/agent/Echo/messages")
	defer resp.Body.Close()
	var all map[string]any
	json.NewDecoder(resp.Body).Decode(&all)
	allMsgs := all["messages"].([]any)
	if len(allMsgs) != 2 {
		t.Fatalf("expected 2 messages without filter, got %d", len(allMsgs))
	}

	// Filter since=before — should still get both messages
	sinceURL := base + "/spaces/test/agent/Echo/messages?since=" + before.Format(time.RFC3339Nano)
	resp2, _ := http.Get(sinceURL)
	defer resp2.Body.Close()
	var filtered map[string]any
	json.NewDecoder(resp2.Body).Decode(&filtered)
	filteredMsgs := filtered["messages"].([]any)
	if len(filteredMsgs) != 2 {
		t.Errorf("expected 2 messages with since=before, got %d", len(filteredMsgs))
	}

	// Use cursor from first response — next poll should return 0
	cursor := all["cursor"].(string)
	cursorURL := base + "/spaces/test/agent/Echo/messages?since=" + cursor
	resp3, _ := http.Get(cursorURL)
	defer resp3.Body.Close()
	var afterCursor map[string]any
	json.NewDecoder(resp3.Body).Decode(&afterCursor)
	afterMsgs := afterCursor["messages"].([]any)
	if len(afterMsgs) != 0 {
		t.Errorf("expected 0 messages after cursor, got %d", len(afterMsgs))
	}
}

func TestAgentMessagesCursorAdvancesCorrectly(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSONWithCaller(t, base+"/spaces/test/agent/Foxtrot", "Foxtrot", map[string]any{
		"status":  "active",
		"summary": "Foxtrot: online",
	})

	postJSONWithCaller(t, base+"/spaces/test/agent/Foxtrot/message", "Boss", map[string]any{
		"message": "batch1 msg1",
	})

	resp1, _ := http.Get(base + "/spaces/test/agent/Foxtrot/messages")
	defer resp1.Body.Close()
	var r1 map[string]any
	json.NewDecoder(resp1.Body).Decode(&r1)
	cursor1 := r1["cursor"].(string)
	if len(r1["messages"].([]any)) != 1 {
		t.Fatalf("expected 1 message in batch1")
	}

	time.Sleep(2 * time.Millisecond)
	postJSONWithCaller(t, base+"/spaces/test/agent/Foxtrot/message", "Boss", map[string]any{
		"message": "batch2 msg1",
	})
	postJSONWithCaller(t, base+"/spaces/test/agent/Foxtrot/message", "Boss", map[string]any{
		"message": "batch2 msg2",
	})

	// Poll with cursor from batch1 — should get only batch2
	resp2, _ := http.Get(base + "/spaces/test/agent/Foxtrot/messages?since=" + cursor1)
	defer resp2.Body.Close()
	var r2 map[string]any
	json.NewDecoder(resp2.Body).Decode(&r2)
	batch2Msgs := r2["messages"].([]any)
	if len(batch2Msgs) != 2 {
		t.Errorf("expected 2 messages in batch2 using cursor, got %d", len(batch2Msgs))
	}
}

func TestStalenessDetection(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postJSONWithCaller(t, base+"/spaces/test/agent/Zeta/register", "Zeta", map[string]any{
		"agent_type":             "http",
		"heartbeat_interval_sec": 1,
	})
	resp.Body.Close()

	rec, _ := srv.GetRegistration("test", "Zeta")
	if rec.Stale {
		t.Error("agent should not be stale immediately after registration")
	}

	// Wait 2× interval + margin
	time.Sleep(2500 * time.Millisecond)
	srv.checkHeartbeatStaleness()

	rec2, _ := srv.GetRegistration("test", "Zeta")
	if !rec2.Stale {
		t.Error("expected agent to be stale after missing heartbeat")
	}

	// Heartbeat clears stale
	hbResp := postJSONWithCaller(t, base+"/spaces/test/agent/Zeta/heartbeat", "Zeta", nil)
	hbResp.Body.Close()
	rec3, _ := srv.GetRegistration("test", "Zeta")
	if rec3.Stale {
		t.Error("expected stale cleared after heartbeat")
	}
}

func TestStalenessGracePeriod(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postJSONWithCaller(t, base+"/spaces/test/agent/Eta/register", "Eta", map[string]any{
		"agent_type":             "http",
		"heartbeat_interval_sec": 10,
	})
	resp.Body.Close()

	// Immediate check — within grace period, should not be stale
	srv.checkHeartbeatStaleness()

	rec, _ := srv.GetRegistration("test", "Eta")
	if rec.Stale {
		t.Error("agent should not be stale within grace period")
	}
}

func TestWebhookDelivery(t *testing.T) {
	received := make(chan map[string]any, 1)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer callbackSrv.Close()

	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	regResp := postJSONWithCaller(t, base+"/spaces/test/agent/Kappa/register", "Kappa", map[string]any{
		"agent_type":   "http",
		"callback_url": callbackSrv.URL,
	})
	regResp.Body.Close()

	stResp := postJSONWithCaller(t, base+"/spaces/test/agent/Kappa", "Kappa", map[string]any{
		"status":  "active",
		"summary": "Kappa: online",
	})
	stResp.Body.Close()

	msgResp := postJSONWithCaller(t, base+"/spaces/test/agent/Kappa/message", "Boss", map[string]any{
		"message": "urgent task",
	})
	msgResp.Body.Close()

	select {
	case payload := <-received:
		if payload["event"] != "message" {
			t.Errorf("expected event=message, got %v", payload["event"])
		}
		if payload["agent"] != "Kappa" {
			t.Errorf("expected agent=Kappa, got %v", payload["agent"])
		}
		if payload["message"] != "urgent task" {
			t.Errorf("expected message='urgent task', got %v", payload["message"])
		}
		if payload["sender"] != "Boss" {
			t.Errorf("expected sender=Boss, got %v", payload["sender"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook delivery timed out after 3s")
	}
}

func TestWebhookDeliveryMessageStillStoredOnFailure(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	regResp := postJSONWithCaller(t, base+"/spaces/test/agent/Lambda/register", "Lambda", map[string]any{
		"agent_type":   "http",
		"callback_url": "http://127.0.0.1:1", // refuses connections
	})
	regResp.Body.Close()

	stResp := postJSONWithCaller(t, base+"/spaces/test/agent/Lambda", "Lambda", map[string]any{
		"status":  "active",
		"summary": "Lambda: online",
	})
	stResp.Body.Close()

	msgResp := postJSONWithCaller(t, base+"/spaces/test/agent/Lambda/message", "Boss", map[string]any{
		"message": "stored despite webhook failure",
	})
	msgResp.Body.Close()

	time.Sleep(300 * time.Millisecond)

	resp, _ := http.Get(base + "/spaces/test/agent/Lambda/messages")
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	msgs := result["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("expected 1 stored message despite webhook failure, got %d", len(msgs))
	}
}

func TestAgentMessagesHaveCursorField(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSONWithCaller(t, base+"/spaces/test/agent/Nu", "Nu", map[string]any{
		"status":  "active",
		"summary": "Nu: online",
	})

	resp, _ := http.Get(base + "/spaces/test/agent/Nu/messages")
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	if _, ok := result["cursor"].(string); !ok {
		t.Error("expected cursor field in response even with no messages")
	}
	msgs := result["messages"].([]any)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}
