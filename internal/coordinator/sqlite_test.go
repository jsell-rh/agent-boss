package coordinator

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"fmt"
)

func TestSQLiteStartupAndPersistence(t *testing.T) {
	t.Setenv("DB_TYPE", "sqlite")

	dir := t.TempDir()
	s := NewServer(":0", dir)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	base := "http://localhost" + s.Port()

	// Create agent.
	postJSON(t, base+"/spaces/persist-test/agent/Alpha", map[string]any{
		"status": "active", "summary": "Alpha: testing sqlite",
	})

	// Create a task (requires X-Agent-Name header).
	taskResp := postTaskJSON(t, base+"/spaces/persist-test/tasks", map[string]any{
		"title": "SQLite task", "created_by": "Alpha",
	}, "Alpha")
	if taskResp.StatusCode != http.StatusCreated && taskResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(taskResp.Body)
		t.Fatalf("task create status %d: %s", taskResp.StatusCode, body)
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Restart — SQLite-only (no JSON import).
	s2 := NewServer(":0", dir)
	t.Setenv("DB_TYPE", "sqlite")
	if err := s2.Start(); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	defer s2.Stop()

	base2 := "http://localhost" + s2.Port()

	// Agent should survive restart.
	_, body := getBody(t, base2+"/spaces/persist-test/agent/Alpha")
	if !strings.Contains(body, "Alpha: testing sqlite") {
		t.Errorf("agent summary not found after restart, got: %s", body)
	}

	// Task should survive restart.
	_, tasksBody := getBody(t, base2+"/spaces/persist-test/tasks")
	if !strings.Contains(tasksBody, "SQLite task") {
		t.Errorf("task not found after restart, got: %s", tasksBody)
	}
}

func TestSQLiteJSONMigration(t *testing.T) {
	t.Setenv("DB_TYPE", "sqlite")

	dir := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339)
	jsonData := fmt.Sprintf(`{
		"name": "migrated-space",
		"agents": {
			"LegacyBot": {
				"status": "idle",
				"summary": "LegacyBot: from JSON file",
				"updated_at": %q
			}
		},
		"tasks": {},
		"created_at": %q,
		"updated_at": %q
	}`, now, now, now)

	if err := os.WriteFile(dir+"/migrated-space.json", []byte(jsonData), 0644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	s := NewServer(":0", dir)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	base := "http://localhost" + s.Port()
	_, body := getBody(t, base+"/spaces/migrated-space/agent/LegacyBot")
	if !strings.Contains(body, "LegacyBot: from JSON file") {
		t.Errorf("migrated agent not found, got: %s", body)
	}
}

// TestSQLiteDeleteSpacePersists verifies that deleting a space removes it from
// the SQLite DB so it does not reappear after a server restart.
func TestSQLiteDeleteSpacePersists(t *testing.T) {
	t.Setenv("DB_TYPE", "sqlite")
	dir := t.TempDir()

	s := NewServer(":0", dir)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	base := "http://localhost" + s.Port()

	// Create a space with an agent.
	postJSON(t, base+"/spaces/ghost-space/agent/Phantom", map[string]any{
		"status": "active", "summary": "Phantom: will be deleted",
	})

	// Delete the space.
	req, _ := http.NewRequest(http.MethodDelete, base+"/spaces/ghost-space/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d", resp.StatusCode)
	}

	s.Stop()

	// Restart and verify the space is gone.
	s2 := NewServer(":0", dir)
	t.Setenv("DB_TYPE", "sqlite")
	if err := s2.Start(); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	defer s2.Stop()

	base2 := "http://localhost" + s2.Port()
	status, body := getBody(t, base2+"/spaces/ghost-space/agent/Phantom")
	if status != http.StatusNotFound {
		t.Errorf("expected 404 for deleted space after restart, got %d: %s", status, body)
	}
}

// TestSQLiteTaskCrossSpaceIsolation verifies that two spaces can each have a
// TASK-001 without overwriting each other's data in the DB.
func TestSQLiteTaskCrossSpaceIsolation(t *testing.T) {
	t.Setenv("DB_TYPE", "sqlite")
	dir := t.TempDir()

	s := NewServer(":0", dir)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	base := "http://localhost" + s.Port()

	// SpaceA creates its first task.
	postTaskJSON(t, base+"/spaces/space-a/tasks", map[string]any{
		"title": "SpaceA first task",
	}, "BotA")

	// SpaceB creates its first task — same sequence number, different space.
	postTaskJSON(t, base+"/spaces/space-b/tasks", map[string]any{
		"title": "SpaceB first task",
	}, "BotB")

	s.Stop()

	// Restart and verify both tasks are intact in their respective spaces.
	s2 := NewServer(":0", dir)
	t.Setenv("DB_TYPE", "sqlite")
	if err := s2.Start(); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	defer s2.Stop()

	base2 := "http://localhost" + s2.Port()

	_, bodyA := getBody(t, base2+"/spaces/space-a/tasks")
	if !strings.Contains(bodyA, "SpaceA first task") {
		t.Errorf("SpaceA task missing after restart, got: %s", bodyA)
	}
	if strings.Contains(bodyA, "SpaceB first task") {
		t.Errorf("SpaceB task leaked into SpaceA after restart, got: %s", bodyA)
	}

	_, bodyB := getBody(t, base2+"/spaces/space-b/tasks")
	if !strings.Contains(bodyB, "SpaceB first task") {
		t.Errorf("SpaceB task missing after restart, got: %s", bodyB)
	}
	if strings.Contains(bodyB, "SpaceA first task") {
		t.Errorf("SpaceA task leaked into SpaceB after restart, got: %s", bodyB)
	}
}

func TestSQLiteInvalidDBType(t *testing.T) {
	t.Setenv("DB_TYPE", "mysql")
	dir := t.TempDir()
	s := NewServer(":0", dir)
	err := s.Start()
	if err == nil {
		s.Stop()
		t.Fatal("expected error for unsupported DB_TYPE=mysql, got nil")
	}
}

func TestSQLiteCustomPath(t *testing.T) {
	dir := t.TempDir()
	customPath := dir + "/custom.db"
	t.Setenv("DB_TYPE", "sqlite")
	t.Setenv("DB_PATH", customPath)
	s := NewServer(":0", dir)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Stop()
	if _, err := os.Stat(customPath); err != nil {
		t.Errorf("expected DB at %s: %v", customPath, err)
	}
}

func TestSQLiteMessagePersistence(t *testing.T) {
	t.Setenv("DB_TYPE", "sqlite")
	dir := t.TempDir()
	s := NewServer(":0", dir)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	base := "http://localhost" + s.Port()

	postJSON(t, base+"/spaces/msg-test/agent/Sender", map[string]any{
		"status": "active", "summary": "Sender: online",
	})
	postJSON(t, base+"/spaces/msg-test/agent/Receiver", map[string]any{
		"status": "active", "summary": "Receiver: online",
	})

	// Send a message (sender identity via URL-based sender header handled by server).
	msgReq := postJSON(t, base+"/spaces/msg-test/agent/Receiver/message",
		map[string]any{"message": "hello from sqlite test"})
	if msgReq.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(msgReq.Body)
		t.Fatalf("message post status %d: %s", msgReq.StatusCode, body)
	}

	s.Stop()

	s2 := NewServer(":0", dir)
	if err := s2.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer s2.Stop()

	base2 := "http://localhost" + s2.Port()
	_, msgsBody := getBody(t, base2+"/spaces/msg-test/agent/Receiver/messages")
	if !strings.Contains(msgsBody, "hello from sqlite test") {
		t.Errorf("message not found after restart, got: %s", msgsBody)
	}
}

// TestSQLiteDeleteAgentPersists verifies that deleting an agent removes it from
// SQLite so it does not reappear after a server restart.
func TestSQLiteDeleteAgentPersists(t *testing.T) {
	t.Setenv("DB_TYPE", "sqlite")
	dir := t.TempDir()

	s := NewServer(":0", dir)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	base := "http://localhost" + s.Port()

	// Create a space with an agent.
	postJSON(t, base+"/spaces/del-agent-space/agent/GhostAgent", map[string]any{
		"status": "active", "summary": "GhostAgent: will be deleted",
	})

	// Verify agent exists.
	code, _ := getBody(t, base+"/spaces/del-agent-space/agent/GhostAgent")
	if code != http.StatusOK {
		t.Fatalf("agent should exist before delete, got %d", code)
	}

	// Delete the agent.
	req, _ := http.NewRequest(http.MethodDelete, base+"/spaces/del-agent-space/agent/GhostAgent", nil)
	req.Header.Set("X-Agent-Name", "GhostAgent")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d", resp.StatusCode)
	}

	s.Stop()

	// Restart and verify the agent is gone.
	s2 := NewServer(":0", dir)
	t.Setenv("DB_TYPE", "sqlite")
	if err := s2.Start(); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	defer s2.Stop()

	base2 := "http://localhost" + s2.Port()
	// Agent GET returns {} (not 404) when the agent doesn't exist in a space.
	// Verify the agent was actually removed from the space's agent list.
	code2, body2 := getBody(t, base2+"/spaces/del-agent-space/agent/GhostAgent")
	if code2 != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", code2, body2)
	}
	if strings.TrimSpace(body2) != "{}" {
		t.Errorf("expected empty agent {} after restart (agent should be gone), got: %s", body2)
	}
	// Also verify the agent is absent from the space listing.
	_, spaceBody := getBody(t, base2+"/spaces/del-agent-space/")
	if strings.Contains(spaceBody, "GhostAgent") {
		t.Errorf("GhostAgent still listed in space after restart: %s", spaceBody)
	}
}

// TestSQLiteDeleteTaskPersists verifies that deleting a task removes it from
// SQLite so it does not reappear after a server restart.
func TestSQLiteDeleteTaskPersists(t *testing.T) {
	t.Setenv("DB_TYPE", "sqlite")
	dir := t.TempDir()

	s := NewServer(":0", dir)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	base := "http://localhost" + s.Port()

	// Create a task.
	resp := postTaskJSON(t, base+"/spaces/del-task-space/tasks", map[string]any{
		"title": "ghost task",
	}, "BotCreator")
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create task status %d: %s", resp.StatusCode, body)
	}

	// Verify task exists.
	code, body := getBody(t, base+"/spaces/del-task-space/tasks")
	if code != http.StatusOK || !strings.Contains(body, "ghost task") {
		t.Fatalf("task should exist before delete, got %d: %s", code, body)
	}

	// Find the task ID (first task in new space is TASK-001).
	taskID := fmt.Sprintf("TASK-%03d", 1)

	// Delete the task.
	delReq, _ := http.NewRequest(http.MethodDelete,
		base+"/spaces/del-task-space/tasks/"+taskID, nil)
	delReq.Header.Set("X-Agent-Name", "BotCreator")
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("task delete status %d", delResp.StatusCode)
	}

	s.Stop()

	// Restart and verify the task is gone.
	s2 := NewServer(":0", dir)
	t.Setenv("DB_TYPE", "sqlite")
	if err := s2.Start(); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	defer s2.Stop()

	base2 := "http://localhost" + s2.Port()
	_, body2 := getBody(t, base2+"/spaces/del-task-space/tasks")
	if strings.Contains(body2, "ghost task") {
		t.Errorf("deleted task reappeared after restart: %s", body2)
	}
	// Verify the specific task returns 404.
	code3, body3 := getBody(t, base2+"/spaces/del-task-space/tasks/"+taskID)
	if code3 != http.StatusNotFound {
		t.Errorf("expected 404 for deleted task after restart, got %d: %s", code3, body3)
	}
}
