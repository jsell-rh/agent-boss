package coordinator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAppendAndLoadHistory(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	snaps := []StatusSnapshot{
		{AgentName: "bot1", Space: "testspace", Status: StatusActive, Timestamp: time.Now().UTC().Add(-2 * time.Minute)},
		{AgentName: "bot2", Space: "testspace", Status: StatusDone, Timestamp: time.Now().UTC().Add(-1 * time.Minute)},
		{AgentName: "bot1", Space: "testspace", Status: StatusIdle, Stale: true, Timestamp: time.Now().UTC()},
	}

	for _, s := range snaps {
		if err := srv.appendSnapshot(s); err != nil {
			t.Fatalf("appendSnapshot: %v", err)
		}
	}

	all, err := srv.loadHistory("testspace", "", time.Time{})
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(all))
	}

	// Filter by agent
	bot1snaps, err := srv.loadHistory("testspace", "bot1", time.Time{})
	if err != nil {
		t.Fatalf("loadHistory(bot1): %v", err)
	}
	if len(bot1snaps) != 2 {
		t.Fatalf("expected 2 bot1 snapshots, got %d", len(bot1snaps))
	}
	for _, s := range bot1snaps {
		if !strings.EqualFold(s.AgentName, "bot1") {
			t.Errorf("unexpected agent %q in bot1 filter", s.AgentName)
		}
	}

	// Filter by since
	cutoff := time.Now().UTC().Add(-90 * time.Second)
	recent, err := srv.loadHistory("testspace", "", cutoff)
	if err != nil {
		t.Fatalf("loadHistory(since): %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent snapshots, got %d", len(recent))
	}
}

func TestLoadHistoryEmpty(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	snaps, err := srv.loadHistory("nonexistent", "", time.Time{})
	if err != nil {
		t.Fatalf("loadHistory on nonexistent space: %v", err)
	}
	if len(snaps) != 0 {
		t.Fatalf("expected 0 snapshots, got %d", len(snaps))
	}
}

func TestSpaceHistoryEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "histspace"

	// Post two agents
	for _, agent := range []string{"alpha", "beta"} {
		resp := postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/%s", base, space, agent), map[string]any{
			"status":  "active",
			"summary": agent + ": working",
		})
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST agent %s: want 202, got %d", agent, resp.StatusCode)
		}
	}

	// GET /spaces/{space}/history — should have 2 snapshots
	status, body := getBody(t, fmt.Sprintf("%s/spaces/%s/history", base, space))
	if status != http.StatusOK {
		t.Fatalf("GET history: want 200, got %d, body: %s", status, body)
	}
	var snapshots []StatusSnapshot
	if err := json.Unmarshal([]byte(body), &snapshots); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}

	// GET /spaces/{space}/history?agent=alpha — should have 1 snapshot
	status, body = getBody(t, fmt.Sprintf("%s/spaces/%s/history?agent=alpha", base, space))
	if status != http.StatusOK {
		t.Fatalf("GET history?agent=alpha: want 200, got %d", status)
	}
	var filtered []StatusSnapshot
	if err := json.Unmarshal([]byte(body), &filtered); err != nil {
		t.Fatalf("unmarshal filtered history: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 alpha snapshot, got %d", len(filtered))
	}
	if !strings.EqualFold(filtered[0].AgentName, "alpha") {
		t.Errorf("expected agent Alpha, got %q", filtered[0].AgentName)
	}
}

func TestAgentHistoryEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "agenthistspace"

	// Post agent with two different statuses — change-detection should write 2 snapshots.
	for _, st := range []string{"active", "done"} {
		resp := postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/worker", base, space), map[string]any{
			"status":  st,
			"summary": fmt.Sprintf("worker: status %s", st),
		})
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST agent: want 202, got %d", resp.StatusCode)
		}
	}

	// GET /spaces/{space}/agent/worker/history
	status, body := getBody(t, fmt.Sprintf("%s/spaces/%s/agent/worker/history", base, space))
	if status != http.StatusOK {
		t.Fatalf("GET agent history: want 200, got %d, body: %s", status, body)
	}
	var snapshots []StatusSnapshot
	if err := json.Unmarshal([]byte(body), &snapshots); err != nil {
		t.Fatalf("unmarshal agent history: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots (one per status change), got %d", len(snapshots))
	}
	for _, s := range snapshots {
		if !strings.EqualFold(s.AgentName, "worker") {
			t.Errorf("expected agent Worker, got %q", s.AgentName)
		}
	}
}

func TestHistorySinceFilter(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "sincefilterspace"

	// Post agent to create snapshot
	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/agt", base, space), map[string]any{
		"status":  "active",
		"summary": "agt: starting",
	})

	// Use since=future to get 0 snapshots
	future := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	status, body := getBody(t, fmt.Sprintf("%s/spaces/%s/history?since=%s", base, space, future))
	if status != http.StatusOK {
		t.Fatalf("GET history?since=future: want 200, got %d", status)
	}
	var snapshots []StatusSnapshot
	if err := json.Unmarshal([]byte(body), &snapshots); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("expected 0 snapshots with future since, got %d", len(snapshots))
	}
}

func TestHistoryInvalidSince(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp, err := http.Get(fmt.Sprintf("%s/spaces/anyspace/history?since=notadate", base))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 400, got %d, body: %s", resp.StatusCode, body)
	}
}

func TestSnapshotFromAgent(t *testing.T) {
	agent := &AgentUpdate{
		Status:         StatusBlocked,
		InferredStatus: "waiting_approval",
		Stale:          true,
	}
	snap := snapshotFromAgent("myspace", "myagent", agent)
	if snap.Space != "myspace" {
		t.Errorf("space: want myspace, got %q", snap.Space)
	}
	if snap.AgentName != "myagent" {
		t.Errorf("agent: want myagent, got %q", snap.AgentName)
	}
	if snap.Status != StatusBlocked {
		t.Errorf("status: want blocked, got %q", snap.Status)
	}
	if snap.InferredStatus != "waiting_approval" {
		t.Errorf("inferred: want waiting_approval, got %q", snap.InferredStatus)
	}
	if !snap.Stale {
		t.Error("stale: want true")
	}
	if snap.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}
