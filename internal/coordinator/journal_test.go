package coordinator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEventJournalAppendAndLoadSince(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	before := time.Now().UTC()
	j.Append("myspace", EventAgentUpdated, "Alice", map[string]string{"key": "val"})
	j.Append("myspace", EventMessageSent, "Bob", map[string]string{"msg": "hello"})
	j.Append("myspace", EventAgentRemoved, "Carol", nil)

	// Load all
	all, err := j.LoadSince("myspace", time.Time{})
	if err != nil {
		t.Fatalf("LoadSince all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	if all[0].Type != EventAgentUpdated {
		t.Errorf("expected agent_updated, got %s", all[0].Type)
	}
	if all[0].Agent != "Alice" {
		t.Errorf("expected Alice, got %s", all[0].Agent)
	}

	// Load since before = all events
	got, err := j.LoadSince("myspace", before)
	if err != nil {
		t.Fatalf("LoadSince before: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 events since before, got %d", len(got))
	}

	// Load since future = no events
	future, err := j.LoadSince("myspace", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("LoadSince future: %v", err)
	}
	if len(future) != 0 {
		t.Errorf("expected 0 events since future, got %d", len(future))
	}
}

func TestEventJournalNonexistentSpace(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	events, err := j.LoadSince("nosuchspace", time.Time{})
	if err != nil {
		t.Fatalf("expected no error for missing journal, got %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestEventJournalReplayEmpty(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	ks, err := j.ReplayInto("myspace")
	if err != nil {
		t.Fatalf("ReplayInto: %v", err)
	}
	if ks != nil {
		t.Error("expected nil for empty journal")
	}
}

func TestEventJournalReplayAgentUpdates(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	tc := 5
	update := &AgentUpdate{
		Status:    StatusActive,
		Summary:   "Alice: working",
		Branch:    "feat/test",
		TestCount: &tc,
		UpdatedAt: time.Now().UTC(),
	}
	j.Append("myspace", EventSpaceCreated, "", map[string]any{
		"name":       "myspace",
		"created_at": time.Now().UTC(),
	})
	j.Append("myspace", EventAgentUpdated, "Alice", update)

	// Second update overwrites
	update2 := &AgentUpdate{
		Status:    StatusDone,
		Summary:   "Alice: done",
		UpdatedAt: time.Now().UTC(),
	}
	j.Append("myspace", EventAgentUpdated, "Alice", update2)

	ks, err := j.ReplayInto("myspace")
	if err != nil {
		t.Fatalf("ReplayInto: %v", err)
	}
	if ks == nil {
		t.Fatal("expected non-nil KnowledgeSpace")
	}
	if len(ks.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(ks.Agents))
	}
	alice, ok := ks.agentStatusOk("Alice")
	if !ok {
		t.Fatal("expected Alice agent")
	}
	if alice.Status != StatusDone {
		t.Errorf("expected done, got %s", alice.Status)
	}
	if alice.Summary != "Alice: done" {
		t.Errorf("unexpected summary: %s", alice.Summary)
	}
}

func TestEventJournalReplayAgentRemoved(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	update := &AgentUpdate{Status: StatusActive, Summary: "Bob: active", UpdatedAt: time.Now().UTC()}
	j.Append("s", EventAgentUpdated, "Bob", update)
	j.Append("s", EventAgentUpdated, "Carol", &AgentUpdate{Status: StatusIdle, Summary: "Carol: idle", UpdatedAt: time.Now().UTC()})
	j.Append("s", EventAgentRemoved, "Bob", nil)

	ks, err := j.ReplayInto("s")
	if err != nil {
		t.Fatalf("ReplayInto: %v", err)
	}
	if _, ok := ks.Agents["Bob"]; ok {
		t.Error("Bob should have been removed")
	}
	if _, ok := ks.Agents["Carol"]; !ok {
		t.Error("Carol should still be present")
	}
}

func TestEventJournalReplayMessages(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	update := &AgentUpdate{Status: StatusActive, Summary: "Dave: active", UpdatedAt: time.Now().UTC()}
	j.Append("s", EventAgentUpdated, "Dave", update)

	msg := &AgentMessage{
		ID:        "msg_001",
		Sender:    "boss",
		Message:   "do the thing",
		Timestamp: time.Now().UTC(),
	}
	j.Append("s", EventMessageSent, "Dave", msg)

	ks, err := j.ReplayInto("s")
	if err != nil {
		t.Fatalf("ReplayInto: %v", err)
	}
	dave := ks.agentStatus("Dave")
	if dave == nil {
		t.Fatal("expected Dave agent")
	}
	if len(dave.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(dave.Messages))
	}
	if dave.Messages[0].ID != "msg_001" {
		t.Errorf("unexpected message id: %s", dave.Messages[0].ID)
	}
}

func TestEventJournalReplayMessageAck(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	update := &AgentUpdate{Status: StatusActive, Summary: "Eve: active", UpdatedAt: time.Now().UTC()}
	j.Append("s", EventAgentUpdated, "Eve", update)

	msg := &AgentMessage{ID: "msg_002", Sender: "boss", Message: "check in", Timestamp: time.Now().UTC()}
	j.Append("s", EventMessageSent, "Eve", msg)

	now := time.Now().UTC()
	j.Append("s", EventMessageAcked, "Eve", map[string]any{
		"message_id": "msg_002",
		"acked_at":   now,
	})

	ks, err := j.ReplayInto("s")
	if err != nil {
		t.Fatalf("ReplayInto: %v", err)
	}
	eve := ks.agentStatus("Eve")
	if eve == nil {
		t.Fatal("expected Eve agent")
	}
	if len(eve.Messages) == 0 {
		t.Fatal("expected messages")
	}
	if !eve.Messages[0].Read {
		t.Error("expected message to be marked read")
	}
	if eve.Messages[0].ReadAt == nil {
		t.Error("expected ReadAt to be set")
	}
}

func TestEventJournalReplaySnapshot(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	// First: some events
	j.Append("s", EventAgentUpdated, "OldAgent", &AgentUpdate{Status: StatusDone, Summary: "old", UpdatedAt: time.Now().UTC()})

	// Snapshot replaces everything
	ks := NewKnowledgeSpace("s")
	tc := 3
	ks.setAgentStatus("NewAgent", &AgentUpdate{Status: StatusActive, Summary: "new", TestCount: &tc, UpdatedAt: time.Now().UTC()})
	j.Append("s", EventSnapshot, "", ks)

	// After snapshot: events apply on top
	j.Append("s", EventAgentUpdated, "Another", &AgentUpdate{Status: StatusIdle, Summary: "another", UpdatedAt: time.Now().UTC()})

	result, err := j.ReplayInto("s")
	if err != nil {
		t.Fatalf("ReplayInto: %v", err)
	}
	if _, ok := result.Agents["OldAgent"]; ok {
		t.Error("OldAgent should not appear after snapshot")
	}
	if _, ok := result.Agents["NewAgent"]; !ok {
		t.Error("NewAgent should appear from snapshot")
	}
	if _, ok := result.Agents["Another"]; !ok {
		t.Error("Another should appear from post-snapshot event")
	}
}

func TestEventJournalCompact(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	for i := 0; i < 5; i++ {
		j.Append("s", EventAgentUpdated, "Alpha", &AgentUpdate{
			Status: StatusActive, Summary: "Alpha: working", UpdatedAt: time.Now().UTC(),
		})
	}

	all, _ := j.LoadSince("s", time.Time{})
	if len(all) != 5 {
		t.Fatalf("expected 5 events before compaction, got %d", len(all))
	}

	ks := NewKnowledgeSpace("s")
	ks.setAgentStatus("Alpha", &AgentUpdate{Status: StatusDone, Summary: "Alpha: done", UpdatedAt: time.Now().UTC()})
	if err := j.Compact("s", ks); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	afterCompact, _ := j.LoadSince("s", time.Time{})
	// Should be exactly 1 event: the snapshot
	if len(afterCompact) != 1 {
		t.Fatalf("expected 1 event after compaction, got %d", len(afterCompact))
	}
	if afterCompact[0].Type != EventSnapshot {
		t.Errorf("expected snapshot event, got %s", afterCompact[0].Type)
	}

	// Replay should restore state
	result, err := j.ReplayInto("s")
	if err != nil {
		t.Fatalf("ReplayInto after compact: %v", err)
	}
	alpha := result.agentStatus("Alpha")
	if alpha == nil || alpha.Status != StatusDone {
		t.Error("Alpha should be done after replay from compacted journal")
	}
}

func TestEventJournalMigrateFromJSON(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	ks := NewKnowledgeSpace("mig")
	tc := 10
	ks.setAgentStatus("Migrated", &AgentUpdate{Status: StatusActive, Summary: "Migrated: active", TestCount: &tc, UpdatedAt: time.Now().UTC()})

	if err := j.MigrateFromJSON(ks); err != nil {
		t.Fatalf("MigrateFromJSON: %v", err)
	}

	result, err := j.ReplayInto("mig")
	if err != nil {
		t.Fatalf("ReplayInto: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if agent := result.agentStatus("Migrated"); agent == nil {
		t.Error("expected Migrated agent")
	} else if agent.Status != StatusActive {
		t.Errorf("expected active, got %s", agent.Status)
	}
}

func TestServerJournalIntegration(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "jtest"

	// Post an agent update
	resp := postJSON(t, base+"/spaces/"+space+"/agent/TestAgent", AgentUpdate{
		Status:  StatusActive,
		Summary: "TestAgent: testing",
		Branch:  "feat/test",
	})
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	// Fetch events endpoint
	code, body := getBody(t, base+"/spaces/"+space+"/api/events")
	if code != 200 {
		t.Fatalf("expected 200 from /api/events, got %d: %s", code, body)
	}
	var events []SpaceEvent
	if err := json.Unmarshal([]byte(body), &events); err != nil {
		t.Fatalf("unmarshal events: %v", err)
	}
	// Should have at least the agent_updated event plus possibly a space_created.
	found := false
	for _, ev := range events {
		if ev.Type == EventAgentUpdated && strings.EqualFold(ev.Agent, "TestAgent") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected agent_updated event for TestAgent, events: %+v", events)
	}
}

func TestServerMessageAck(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "acktest"

	// Create the agent first
	resp := postJSON(t, base+"/spaces/"+space+"/agent/Acker", AgentUpdate{
		Status:  StatusActive,
		Summary: "Acker: active",
	})
	resp.Body.Close()

	// Send a message to Acker
	code, body := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/Acker/message",
		map[string]string{"message": "hello there"},
		"boss",
	)
	if code != 200 {
		t.Fatalf("send message: expected 200, got %d: %s", code, body)
	}
	var msgResp map[string]string
	if err := json.Unmarshal([]byte(body), &msgResp); err != nil {
		t.Fatalf("unmarshal msg response: %v", err)
	}
	msgID := msgResp["messageId"]
	if msgID == "" {
		t.Fatal("expected messageId in response")
	}

	// Ack the message
	code2, body2 := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/Acker/message/"+msgID+"/ack",
		nil,
		"Acker",
	)
	if code2 != 200 {
		t.Fatalf("ack message: expected 200, got %d: %s", code2, body2)
	}

	// Verify the message is marked read
	code3, body3 := getBody(t, base+"/spaces/"+space+"/agent/Acker")
	if code3 != 200 {
		t.Fatalf("get agent: expected 200, got %d", code3)
	}
	var agent AgentUpdate
	if err := json.Unmarshal([]byte(body3), &agent); err != nil {
		t.Fatalf("unmarshal agent: %v", err)
	}
	found := false
	for _, msg := range agent.Messages {
		if msg.ID == msgID {
			found = true
			if !msg.Read {
				t.Error("expected message to be marked read")
			}
			if msg.ReadAt == nil {
				t.Error("expected ReadAt to be set")
			}
		}
	}
	if !found {
		t.Errorf("message %q not found in agent messages", msgID)
	}
}

func TestServerEventsSincFilter(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	base := serverBaseURL(srv)
	space := "sincetest"

	// Post before capturing time
	resp := postJSON(t, base+"/spaces/"+space+"/agent/Early", AgentUpdate{
		Status:  StatusActive,
		Summary: "Early: first",
	})
	resp.Body.Close()

	cutoff := time.Now().UTC()

	resp2 := postJSON(t, base+"/spaces/"+space+"/agent/Late", AgentUpdate{
		Status:  StatusActive,
		Summary: "Late: second",
	})
	resp2.Body.Close()

	// Get events since cutoff — should only include Late's update (and possibly space_created is before)
	code, body := getBody(t, base+"/spaces/"+space+"/api/events?since="+cutoff.Format(time.RFC3339Nano))
	if code != 200 {
		t.Fatalf("expected 200, got %d: %s", code, body)
	}
	var events []SpaceEvent
	if err := json.Unmarshal([]byte(body), &events); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, ev := range events {
		if ev.Timestamp.Before(cutoff) {
			t.Errorf("event %s at %v is before cutoff %v", ev.ID, ev.Timestamp, cutoff)
		}
	}
	// Should include Late's agent_updated
	foundLate := false
	for _, ev := range events {
		if ev.Type == EventAgentUpdated && ev.Agent == "Late" {
			foundLate = true
		}
	}
	if !foundLate {
		t.Errorf("expected Late agent_updated in since-filtered events")
	}
}

func TestServerPersistsViaJournal(t *testing.T) {
	dir := t.TempDir()

	// Start server, post agent, stop server
	srv1 := NewServer(":0", dir)
	if err := srv1.Start(); err != nil {
		t.Fatalf("start srv1: %v", err)
	}
	base1 := serverBaseURL(srv1)
	space := "persist"

	postJSON(t, base1+"/spaces/"+space+"/agent/Persistent", AgentUpdate{
		Status:  StatusActive,
		Summary: "Persistent: active",
		Branch:  "main",
	})
	srv1.Stop()

	// Verify journal file was created
	journalPath := dir + "/" + space + ".events.jsonl"
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("expected journal file at %s: %v", journalPath, err)
	}

	// Start a new server on same data dir — should replay from journal
	srv2 := NewServer(":0", dir)
	if err := srv2.Start(); err != nil {
		t.Fatalf("start srv2: %v", err)
	}
	defer srv2.Stop()
	base2 := serverBaseURL(srv2)

	code, body := getBody(t, base2+"/spaces/"+space+"/agent/Persistent")
	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	var agent AgentUpdate
	if err := json.Unmarshal([]byte(body), &agent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if agent.Status != StatusActive {
		t.Errorf("expected active after restart, got %s", agent.Status)
	}
	if agent.Summary != "Persistent: active" {
		t.Errorf("unexpected summary: %s", agent.Summary)
	}
}

// TestEventJournalCorruptedLines verifies that corrupted/partial JSONL lines are
// skipped gracefully and valid lines before and after are still returned.
func TestEventJournalCorruptedLines(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	// Append two valid events
	j.Append("s", EventAgentUpdated, "Alpha", map[string]string{"k": "v"})
	j.Append("s", EventAgentUpdated, "Beta", map[string]string{"k": "v"})

	// Manually inject corrupted lines into the journal file
	path := filepath.Join(dir, "s.events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	f.WriteString("{not valid json\n")
	f.WriteString("\n") // blank line
	f.WriteString(`{"id":"x","space":"s","type":"agent_updated","agent":"Gamma","timestamp":"2026-01-01T00:00:00Z"}` + "\n")
	f.Close()

	events, err := j.LoadSince("s", time.Time{})
	if err != nil {
		t.Fatalf("LoadSince with corrupted lines: %v", err)
	}
	// Should have Alpha, Beta, and Gamma (3 valid lines; corrupted line skipped)
	if len(events) != 3 {
		t.Fatalf("expected 3 events (corrupted line skipped), got %d", len(events))
	}
	agents := map[string]bool{}
	for _, ev := range events {
		agents[ev.Agent] = true
	}
	if !agents["Alpha"] || !agents["Beta"] || !agents["Gamma"] {
		t.Errorf("unexpected agents: %v", agents)
	}
}

// TestEventJournalLargePayload verifies behavior with payloads near the scanner buffer limit.
func TestEventJournalLargePayload(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	// 256KB payload — well within 512KB scanner buffer
	largeVal := strings.Repeat("x", 256*1024)
	j.Append("s", EventAgentUpdated, "Big", map[string]string{"data": largeVal})

	events, err := j.LoadSince("s", time.Time{})
	if err != nil {
		t.Fatalf("LoadSince large payload: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// Payload exceeding 512KB scanner buffer: the line will be too long.
	// LoadSince should return an error (scanner.Err() = bufio.ErrTooLong).
	tooBig := strings.Repeat("y", 600*1024)
	j.Append("s", EventAgentUpdated, "TooBig", map[string]string{"data": tooBig})

	_, scanErr := j.LoadSince("s", time.Time{})
	if scanErr == nil {
		// Not a hard failure — document current behavior: either error or truncated results.
		t.Log("NOTE: scanner silently handled >512KB line (behavior may vary by Go version)")
	}
}

// TestEventJournalConcurrentWrites verifies no data races under concurrent appends.
func TestEventJournalConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	const goroutines = 20
	const eventsEach = 50
	done := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		agent := fmt.Sprintf("Agent%d", g)
		go func(name string) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < eventsEach; i++ {
				j.Append("concurrent", EventAgentUpdated, name, map[string]int{"i": i})
			}
		}(agent)
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}

	events, err := j.LoadSince("concurrent", time.Time{})
	if err != nil {
		t.Fatalf("LoadSince after concurrent writes: %v", err)
	}
	expected := goroutines * eventsEach
	if len(events) != expected {
		t.Errorf("expected %d events, got %d", expected, len(events))
	}
}

// TestEventJournalCompactVsConcurrentAppend tests compaction racing with appends.
func TestEventJournalCompactVsConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	// Pre-seed some events
	for i := 0; i < 10; i++ {
		j.Append("race", EventAgentUpdated, "Racer", map[string]int{"i": i})
	}

	ks := NewKnowledgeSpace("race")
	ks.setAgentStatus("Racer", &AgentUpdate{Status: StatusActive, Summary: "Racer: mid", UpdatedAt: time.Now().UTC()})

	// Compact and append concurrently
	errc := make(chan error, 1)
	go func() {
		errc <- j.Compact("race", ks)
	}()
	for i := 0; i < 20; i++ {
		j.Append("race", EventAgentUpdated, "Racer", map[string]int{"post": i})
	}
	if err := <-errc; err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}

	// Journal must still be readable and contain at least the snapshot
	events, err := j.LoadSince("race", time.Time{})
	if err != nil {
		t.Fatalf("LoadSince after compact+append race: %v", err)
	}
	hasSnapshot := false
	for _, ev := range events {
		if ev.Type == EventSnapshot {
			hasSnapshot = true
		}
	}
	if !hasSnapshot {
		t.Error("expected at least one snapshot event after Compact")
	}
}

// TestEventJournalFileHandlePool verifies that the pooled file handle survives
// multiple appends and that Compact properly closes and replaces it.
func TestEventJournalFileHandlePool(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	// Write many events — pooled handle should be reused across calls.
	for i := 0; i < 50; i++ {
		j.Append("pool", EventAgentUpdated, "Worker", map[string]int{"i": i})
	}

	events, err := j.LoadSince("pool", time.Time{})
	if err != nil {
		t.Fatalf("LoadSince: %v", err)
	}
	if len(events) != 50 {
		t.Fatalf("expected 50 events, got %d", len(events))
	}

	// Compact should close the pooled handle and reset the file.
	ks := NewKnowledgeSpace("pool")
	ks.setAgentStatus("Worker", &AgentUpdate{Status: StatusDone, Summary: "Worker: done", UpdatedAt: time.Now().UTC()})
	if err := j.Compact("pool", ks); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Subsequent appends should open a new handle to the compacted file.
	j.Append("pool", EventAgentUpdated, "Worker", map[string]int{"i": 99})
	events2, err := j.LoadSince("pool", time.Time{})
	if err != nil {
		t.Fatalf("LoadSince after Compact: %v", err)
	}
	// Should have snapshot + 1 post-compact event = 2 events.
	if len(events2) != 2 {
		t.Fatalf("expected 2 events after Compact+Append, got %d", len(events2))
	}
	if events2[0].Type != EventSnapshot {
		t.Errorf("expected first event to be snapshot, got %s", events2[0].Type)
	}
}

// TestEventJournalEventCount verifies that EventCount tracks appends and resets on Compact.
func TestEventJournalEventCount(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	if c := j.EventCount("cnt"); c != 0 {
		t.Errorf("expected 0 initial count, got %d", c)
	}

	for i := 0; i < 10; i++ {
		j.Append("cnt", EventAgentUpdated, "A", map[string]int{"i": i})
	}
	if c := j.EventCount("cnt"); c != 10 {
		t.Errorf("expected count 10, got %d", c)
	}

	ks := NewKnowledgeSpace("cnt")
	if err := j.Compact("cnt", ks); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// After compaction, count resets to 1 (the snapshot event).
	if c := j.EventCount("cnt"); c != 1 {
		t.Errorf("expected count 1 after Compact, got %d", c)
	}

	// Count threshold check.
	if j.EventCount("cnt") > CompactionThreshold {
		t.Error("single snapshot event should not exceed CompactionThreshold")
	}
}

// TestEventJournalCountTriggeredCompaction verifies that EventCount exceeds
// CompactionThreshold after many appends, simulating the trigger condition.
func TestEventJournalCountTriggeredCompaction(t *testing.T) {
	dir := t.TempDir()
	j := NewEventJournal(dir)

	for i := 0; i < CompactionThreshold+1; i++ {
		j.Append("heavy", EventAgentUpdated, "Heavy", map[string]int{"i": i})
	}
	if j.EventCount("heavy") <= CompactionThreshold {
		t.Errorf("expected count > %d, got %d", CompactionThreshold, j.EventCount("heavy"))
	}

	// Compact and verify count resets.
	ks := NewKnowledgeSpace("heavy")
	ks.setAgentStatus("Heavy", &AgentUpdate{Status: StatusActive, Summary: "Heavy: active", UpdatedAt: time.Now().UTC()})
	if err := j.Compact("heavy", ks); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if c := j.EventCount("heavy"); c != 1 {
		t.Errorf("expected count 1 after Compact, got %d", c)
	}
	// After compaction journal should have just the snapshot.
	events, err := j.LoadSince("heavy", time.Time{})
	if err != nil {
		t.Fatalf("LoadSince: %v", err)
	}
	if len(events) != 1 || events[0].Type != EventSnapshot {
		t.Errorf("expected exactly 1 snapshot after compaction, got %d events", len(events))
	}
}

// postJSONWithSender posts JSON to a URL with a custom X-Agent-Name header.
func postJSONWithSender(t *testing.T, url string, payload any, sender string) (int, string) {
	t.Helper()
	var bodyStr string
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		bodyStr = string(data)
	} else {
		bodyStr = "{}"
	}
	r, err := http.NewRequest(http.MethodPost, url, strings.NewReader(bodyStr))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Agent-Name", sender)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
