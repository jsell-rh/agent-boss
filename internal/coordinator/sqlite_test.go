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
