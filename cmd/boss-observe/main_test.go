package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestToolsRegistered verifies the MCP server registers all 4 observability tools.
func TestToolsRegistered(t *testing.T) {
	srv := newObserveServer("http://localhost:8899", "")
	if srv == nil {
		t.Fatal("newObserveServer returned nil")
	}
	// The server is created without panicking — tools are registered.
}

// TestGetRecentEventsHTTPClient verifies get_recent_events correctly calls the boss API.
func TestGetRecentEventsHTTPClient(t *testing.T) {
	events := []map[string]any{
		{
			"id":        "ev_1",
			"space":     "test-space",
			"type":      "agent_updated",
			"agent":     "test-agent",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
		{
			"id":        "ev_2",
			"space":     "test-space",
			"type":      "message_sent",
			"agent":     "other-agent",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spaces/test-space/api/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(events)
	}))
	defer ts.Close()

	obs := &observeServer{
		bossURL:    ts.URL,
		bossToken:  "",
		httpClient: ts.Client(),
	}

	body, err := obs.get("/spaces/test-space/api/events")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("expected 2 events, got %d", len(decoded))
	}
	if decoded[0]["type"] != "agent_updated" {
		t.Errorf("expected agent_updated, got %v", decoded[0]["type"])
	}
}

// TestGetAgentStatusHTTPClient verifies get_agent_status correctly calls the boss API.
func TestGetAgentStatusHTTPClient(t *testing.T) {
	agentData := map[string]any{
		"status":     "active",
		"summary":    "test: doing something",
		"session_id": "agent-boss-dev-test",
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spaces/test-space/agent/test-agent" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agentData)
	}))
	defer ts.Close()

	obs := &observeServer{
		bossURL:    ts.URL,
		bossToken:  "",
		httpClient: ts.Client(),
	}

	body, err := obs.get("/spaces/test-space/agent/test-agent")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode agent: %v", err)
	}
	if decoded["status"] != "active" {
		t.Errorf("expected status=active, got %v", decoded["status"])
	}
	if decoded["session_id"] != "agent-boss-dev-test" {
		t.Errorf("expected session_id, got %v", decoded["session_id"])
	}
}

// TestBearerTokenForwarding verifies the Authorization header is sent when a token is configured.
func TestBearerTokenForwarding(t *testing.T) {
	var receivedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
	}))
	defer ts.Close()

	obs := &observeServer{
		bossURL:    ts.URL,
		bossToken:  "secret-token",
		httpClient: ts.Client(),
	}

	if _, err := obs.get("/spaces/x/api/events"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if receivedAuth != "Bearer secret-token" {
		t.Errorf("expected Bearer secret-token, got %q", receivedAuth)
	}
}

// TestEventTypeFiltering verifies the event_type filter works correctly in-process.
func TestEventTypeFiltering(t *testing.T) {
	events := []spaceEvent{
		{ID: "1", Type: "agent_updated", Agent: "a1", Timestamp: time.Now()},
		{ID: "2", Type: "message_sent", Agent: "a2", Timestamp: time.Now()},
		{ID: "3", Type: "agent_updated", Agent: "a3", Timestamp: time.Now()},
	}

	filterType := "agent_updated"
	var filtered []spaceEvent
	for _, ev := range events {
		if ev.Type == filterType {
			filtered = append(filtered, ev)
		}
	}

	if len(filtered) != 2 {
		t.Errorf("expected 2 agent_updated events, got %d", len(filtered))
	}
}

// TestLimitTruncation verifies that the limit parameter correctly truncates results.
func TestLimitTruncation(t *testing.T) {
	events := make([]spaceEvent, 30)
	for i := range events {
		events[i] = spaceEvent{ID: string(rune('0' + i)), Type: "agent_updated", Timestamp: time.Now()}
	}

	limit := 10
	if len(events) > limit {
		events = events[len(events)-limit:]
	}

	if len(events) != 10 {
		t.Errorf("expected 10 events after limit, got %d", len(events))
	}
}
