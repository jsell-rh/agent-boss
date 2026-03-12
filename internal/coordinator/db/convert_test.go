package db

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestToAgentRow_BasicFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a := &Agent{
		SpaceName:      "test-space",
		AgentName:      "Bot",
		Status:         "active",
		Summary:        "Bot summary",
		Branch:         "feat/x",
		Worktree:       "/tmp/worktree",
		PR:             "https://github.com/org/repo/pull/1",
		Phase:          "build",
		NextSteps:      "deploy",
		FreeText:       "extra",
		SessionID:      "sess-123",
		RepoURL:        "https://github.com/org/repo",
		Parent:         "ParentAgent",
		Role:           "developer",
		InferredStatus: "working",
		Stale:          true,
		Registration:   `{"key":"val"}`,
		LastHeartbeat:  now,
		HeartbeatStale: true,
		UpdatedAt:      now,
		Sections:       `[{"title":"s1"}]`,
		Documents:      `[{"title":"d1"}]`,
		Items:          MarshalJSON([]string{"item1", "item2"}),
		Questions:      MarshalJSON([]string{"q1"}),
		Blockers:       MarshalJSON([]string{"blocker1"}),
		Children:       MarshalJSON([]string{"ChildA", "ChildB"}),
	}

	row := ToAgentRow(a)

	if row.SpaceName != "test-space" {
		t.Errorf("SpaceName: got %q want %q", row.SpaceName, "test-space")
	}
	if row.AgentName != "Bot" {
		t.Errorf("AgentName: got %q want %q", row.AgentName, "Bot")
	}
	if row.Status != "active" {
		t.Errorf("Status: got %q want %q", row.Status, "active")
	}
	if row.Summary != "Bot summary" {
		t.Errorf("Summary: got %q", row.Summary)
	}
	if row.Branch != "feat/x" {
		t.Errorf("Branch: got %q", row.Branch)
	}
	if row.Worktree != "/tmp/worktree" {
		t.Errorf("Worktree: got %q", row.Worktree)
	}
	if row.PR != "https://github.com/org/repo/pull/1" {
		t.Errorf("PR: got %q", row.PR)
	}
	if row.Phase != "build" {
		t.Errorf("Phase: got %q", row.Phase)
	}
	if row.NextSteps != "deploy" {
		t.Errorf("NextSteps: got %q", row.NextSteps)
	}
	if row.FreeText != "extra" {
		t.Errorf("FreeText: got %q", row.FreeText)
	}
	if row.SessionID != "sess-123" {
		t.Errorf("SessionID: got %q", row.SessionID)
	}
	if row.RepoURL != "https://github.com/org/repo" {
		t.Errorf("RepoURL: got %q", row.RepoURL)
	}
	if row.Parent != "ParentAgent" {
		t.Errorf("Parent: got %q", row.Parent)
	}
	if row.Role != "developer" {
		t.Errorf("Role: got %q", row.Role)
	}
	if row.InferredStatus != "working" {
		t.Errorf("InferredStatus: got %q", row.InferredStatus)
	}
	if !row.Stale {
		t.Errorf("Stale: expected true")
	}
	if row.RegistrationRaw != `{"key":"val"}` {
		t.Errorf("RegistrationRaw: got %q", row.RegistrationRaw)
	}
	if !row.LastHeartbeat.Equal(now) {
		t.Errorf("LastHeartbeat: got %v want %v", row.LastHeartbeat, now)
	}
	if !row.HeartbeatStale {
		t.Errorf("HeartbeatStale: expected true")
	}
	if !row.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt: got %v want %v", row.UpdatedAt, now)
	}
	if row.SectionsRaw != `[{"title":"s1"}]` {
		t.Errorf("SectionsRaw: got %q", row.SectionsRaw)
	}
	if row.DocumentsRaw != `[{"title":"d1"}]` {
		t.Errorf("DocumentsRaw: got %q", row.DocumentsRaw)
	}
}

func TestToAgentRow_TestCount(t *testing.T) {
	// Valid TestCount.
	n := 42
	a := &Agent{TestCount: sql.NullInt64{Int64: 42, Valid: true}}
	row := ToAgentRow(a)
	if row.TestCount == nil {
		t.Fatal("TestCount: expected non-nil pointer")
	}
	if *row.TestCount != n {
		t.Errorf("TestCount: got %d want %d", *row.TestCount, n)
	}

	// Invalid (NULL) TestCount.
	a2 := &Agent{TestCount: sql.NullInt64{Valid: false}}
	row2 := ToAgentRow(a2)
	if row2.TestCount != nil {
		t.Errorf("TestCount: expected nil for NULL, got %d", *row2.TestCount)
	}
}

func TestToAgentRow_JSONSlices(t *testing.T) {
	a := &Agent{
		Items:    MarshalJSON([]string{"a", "b"}),
		Questions: MarshalJSON([]string{"q?"}),
		Blockers:  MarshalJSON([]string{"blocked"}),
		Children:  MarshalJSON([]string{"Child1", "Child2"}),
	}
	row := ToAgentRow(a)

	if len(row.Items) != 2 || row.Items[0] != "a" || row.Items[1] != "b" {
		t.Errorf("Items: got %v", row.Items)
	}
	if len(row.QuestionsRaw) != 1 || row.QuestionsRaw[0] != "q?" {
		t.Errorf("QuestionsRaw: got %v", row.QuestionsRaw)
	}
	if len(row.BlockersRaw) != 1 || row.BlockersRaw[0] != "blocked" {
		t.Errorf("BlockersRaw: got %v", row.BlockersRaw)
	}
	if len(row.Children) != 2 || row.Children[0] != "Child1" {
		t.Errorf("Children: got %v", row.Children)
	}
}

func TestToAgentRow_EmptyJSONSlices(t *testing.T) {
	a := &Agent{} // empty/zero strings for JSON fields
	row := ToAgentRow(a)

	if row.Items != nil {
		t.Errorf("Items: expected nil for empty, got %v", row.Items)
	}
	if row.QuestionsRaw != nil {
		t.Errorf("QuestionsRaw: expected nil for empty, got %v", row.QuestionsRaw)
	}
}

func TestFromAgentFields_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	n := 7
	items := []string{"x", "y"}
	questions := []string{"q1"}
	blockers := []string{"b1"}
	children := []string{"C1"}

	a := FromAgentFields(
		"space", "Agent", "active", "summary", "branch", "worktree", "pr", "phase",
		&n,
		items, questions, blockers, children,
		`{"s":"v"}`, `{"d":"v"}`, `{"r":"v"}`,
		"next", "free", "sess", "repo", "parent", "role",
		"inferred",
		true, false,
		now, now,
	)

	if a.SpaceName != "space" {
		t.Errorf("SpaceName: got %q", a.SpaceName)
	}
	if !a.TestCount.Valid || a.TestCount.Int64 != 7 {
		t.Errorf("TestCount: got %+v", a.TestCount)
	}

	var gotItems []string
	if err := UnmarshalJSON(a.Items, &gotItems); err != nil || len(gotItems) != 2 {
		t.Errorf("Items marshal/unmarshal: got %v err %v", gotItems, err)
	}

	// Round-trip through ToAgentRow.
	row := ToAgentRow(a)
	if row.TestCount == nil || *row.TestCount != n {
		t.Errorf("round-trip TestCount: got %v", row.TestCount)
	}
	if len(row.Items) != 2 || row.Items[0] != "x" {
		t.Errorf("round-trip Items: got %v", row.Items)
	}
	if len(row.Children) != 1 || row.Children[0] != "C1" {
		t.Errorf("round-trip Children: got %v", row.Children)
	}
}

func TestFromAgentFields_NilTestCount(t *testing.T) {
	now := time.Now().UTC()
	a := FromAgentFields(
		"space", "Agent", "idle", "", "", "", "", "",
		nil,
		nil, nil, nil, nil,
		"", "", "",
		"", "", "", "", "", "",
		"",
		false, false,
		now, now,
	)
	if a.TestCount.Valid {
		t.Errorf("TestCount should be invalid (NULL) when nil passed, got %+v", a.TestCount)
	}
}

func TestToMessageRow(t *testing.T) {
	now := time.Now().UTC()
	readAt := now.Add(time.Minute)

	m := &AgentMessage{
		ID:        "msg-1",
		SpaceName: "space",
		AgentName: "Agent",
		Message:   "hello",
		Sender:    "boss",
		Priority:  "high",
		Timestamp: now,
		Read:      true,
		ReadAt:    sql.NullTime{Time: readAt, Valid: true},
	}

	row := ToMessageRow(m)

	if row.ID != "msg-1" {
		t.Errorf("ID: got %q", row.ID)
	}
	if row.Message != "hello" {
		t.Errorf("Message: got %q", row.Message)
	}
	if row.Sender != "boss" {
		t.Errorf("Sender: got %q", row.Sender)
	}
	if row.Priority != "high" {
		t.Errorf("Priority: got %q", row.Priority)
	}
	if !row.Read {
		t.Errorf("Read: expected true")
	}
	if row.ReadAt == nil {
		t.Fatal("ReadAt: expected non-nil")
	}
	if !row.ReadAt.Equal(readAt) {
		t.Errorf("ReadAt: got %v want %v", *row.ReadAt, readAt)
	}
}

func TestToMessageRow_NullReadAt(t *testing.T) {
	m := &AgentMessage{
		ID:      "msg-2",
		ReadAt:  sql.NullTime{Valid: false},
	}
	row := ToMessageRow(m)
	if row.ReadAt != nil {
		t.Errorf("ReadAt: expected nil for NULL, got %v", row.ReadAt)
	}
}

func TestToNotificationRow(t *testing.T) {
	now := time.Now().UTC()
	n := &AgentNotification{
		ID:        "notif-1",
		SpaceName: "space",
		AgentName: "Agent",
		Type:      "task_assigned",
		Title:     "New task",
		Body:      "Check TASK-001",
		FromAgent: "boss",
		TaskID:    "TASK-001",
		Timestamp: now,
		Read:      true,
	}

	row := ToNotificationRow(n)

	if row.ID != "notif-1" {
		t.Errorf("ID: got %q", row.ID)
	}
	if row.Type != "task_assigned" {
		t.Errorf("Type: got %q", row.Type)
	}
	if row.Title != "New task" {
		t.Errorf("Title: got %q", row.Title)
	}
	if row.FromAgent != "boss" {
		t.Errorf("FromAgent: got %q", row.FromAgent)
	}
	if row.TaskID != "TASK-001" {
		t.Errorf("TaskID: got %q", row.TaskID)
	}
	if !row.Read {
		t.Errorf("Read: expected true")
	}
	if !row.Timestamp.Equal(now) {
		t.Errorf("Timestamp: got %v want %v", row.Timestamp, now)
	}
}

func TestToTaskRow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(24 * time.Hour)

	task := &Task{
		ID:           "TASK-001",
		SpaceName:    "space",
		Title:        "My task",
		Description:  "Description",
		Status:       "in_progress",
		Priority:     "high",
		AssignedTo:   "Agent",
		CreatedBy:    "boss",
		Labels:       MarshalJSON([]string{"backend", "urgent"}),
		ParentTask:   "TASK-000",
		Subtasks:     MarshalJSON([]string{"TASK-002"}),
		LinkedBranch: "feat/task",
		LinkedPR:     "https://github.com/org/repo/pull/5",
		CreatedAt:    now,
		UpdatedAt:    now,
		DueAt:        sql.NullTime{Time: due, Valid: true},
	}

	comment := &TaskComment{
		ID:        "cmt-1",
		TaskID:    "TASK-001",
		Author:    "Agent",
		Body:      "done",
		CreatedAt: now,
	}
	event := &TaskEvent{
		ID:        "evt-1",
		TaskID:    "TASK-001",
		Type:      "status_changed",
		By:        "Agent",
		Detail:    "backlog→in_progress",
		CreatedAt: now,
	}

	row := ToTaskRow(task, []*TaskComment{comment}, []*TaskEvent{event})

	if row.ID != "TASK-001" {
		t.Errorf("ID: got %q", row.ID)
	}
	if row.Title != "My task" {
		t.Errorf("Title: got %q", row.Title)
	}
	if row.Status != "in_progress" {
		t.Errorf("Status: got %q", row.Status)
	}
	if len(row.Labels) != 2 || row.Labels[0] != "backend" {
		t.Errorf("Labels: got %v", row.Labels)
	}
	if len(row.Subtasks) != 1 || row.Subtasks[0] != "TASK-002" {
		t.Errorf("Subtasks: got %v", row.Subtasks)
	}
	if row.DueAt == nil {
		t.Fatal("DueAt: expected non-nil")
	}
	if !row.DueAt.Equal(due) {
		t.Errorf("DueAt: got %v want %v", *row.DueAt, due)
	}
	if len(row.Comments) != 1 || row.Comments[0].Body != "done" {
		t.Errorf("Comments: got %+v", row.Comments)
	}
	if len(row.Events) != 1 || row.Events[0].Type != "status_changed" {
		t.Errorf("Events: got %+v", row.Events)
	}
}

func TestToTaskRow_NullDueAt(t *testing.T) {
	task := &Task{DueAt: sql.NullTime{Valid: false}}
	row := ToTaskRow(task, nil, nil)
	if row.DueAt != nil {
		t.Errorf("DueAt: expected nil for NULL, got %v", row.DueAt)
	}
}

func TestFromTaskFields_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(48 * time.Hour)
	labels := []string{"label1"}
	subtasks := []string{"TASK-002", "TASK-003"}

	t1 := FromTaskFields(
		"TASK-001", "space", "title", "desc", "backlog", "medium",
		"Agent", "boss",
		labels, subtasks,
		"", "feat/x", "https://github.com/pr/1",
		now, now,
		&due,
	)

	if t1.ID != "TASK-001" {
		t.Errorf("ID: got %q", t1.ID)
	}
	if !t1.DueAt.Valid {
		t.Errorf("DueAt should be valid when non-nil passed")
	}
	if !t1.DueAt.Time.Equal(due) {
		t.Errorf("DueAt: got %v want %v", t1.DueAt.Time, due)
	}

	var gotLabels []string
	if err := UnmarshalJSON(t1.Labels, &gotLabels); err != nil || len(gotLabels) != 1 || gotLabels[0] != "label1" {
		t.Errorf("Labels: got %v err %v", gotLabels, err)
	}
	var gotSubtasks []string
	if err := UnmarshalJSON(t1.Subtasks, &gotSubtasks); err != nil || len(gotSubtasks) != 2 {
		t.Errorf("Subtasks: got %v err %v", gotSubtasks, err)
	}

	// Round-trip through ToTaskRow.
	row := ToTaskRow(t1, nil, nil)
	if row.DueAt == nil || !row.DueAt.Equal(due) {
		t.Errorf("round-trip DueAt: got %v", row.DueAt)
	}
	if len(row.Labels) != 1 {
		t.Errorf("round-trip Labels: got %v", row.Labels)
	}
}

func TestFromTaskFields_NilDueAt(t *testing.T) {
	now := time.Now().UTC()
	t1 := FromTaskFields(
		"TASK-002", "space", "t", "d", "backlog", "low",
		"", "bot",
		nil, nil, "", "", "",
		now, now, nil,
	)
	if t1.DueAt.Valid {
		t.Errorf("DueAt should be invalid (NULL) when nil passed")
	}
}

func TestToSnapshotRow(t *testing.T) {
	now := time.Now().UTC()
	s := &StatusSnapshot{
		AgentName:      "Agent",
		SpaceName:      "space",
		Status:         "active",
		InferredStatus: "working",
		Stale:          true,
		Timestamp:      now,
	}

	row := ToSnapshotRow(s)

	if row.AgentName != "Agent" {
		t.Errorf("AgentName: got %q", row.AgentName)
	}
	if row.SpaceName != "space" {
		t.Errorf("SpaceName: got %q", row.SpaceName)
	}
	if row.Status != "active" {
		t.Errorf("Status: got %q", row.Status)
	}
	if row.InferredStatus != "working" {
		t.Errorf("InferredStatus: got %q", row.InferredStatus)
	}
	if !row.Stale {
		t.Errorf("Stale: expected true")
	}
	if !row.Timestamp.Equal(now) {
		t.Errorf("Timestamp: got %v want %v", row.Timestamp, now)
	}
}

func TestRawJSON(t *testing.T) {
	if got := RawJSON(nil); got != nil {
		t.Errorf("RawJSON(nil): expected nil, got %q", got)
	}

	type S struct{ X int }
	b := RawJSON(S{X: 5})
	var got S
	if err := json.Unmarshal(b, &got); err != nil || got.X != 5 {
		t.Errorf("RawJSON: got %q err %v", b, err)
	}
}

func TestMarshalUnmarshalJSON(t *testing.T) {
	in := []string{"a", "b", "c"}
	s := MarshalJSON(in)
	if s == "" {
		t.Fatal("MarshalJSON: expected non-empty")
	}

	var out []string
	if err := UnmarshalJSON(s, &out); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if len(out) != 3 || out[0] != "a" {
		t.Errorf("UnmarshalJSON: got %v", out)
	}

	// Empty input returns nil error.
	if err := UnmarshalJSON("", &out); err != nil {
		t.Errorf("UnmarshalJSON empty: expected nil err, got %v", err)
	}
	if err := UnmarshalJSON("null", &out); err != nil {
		t.Errorf("UnmarshalJSON null: expected nil err, got %v", err)
	}

	// Nil input returns "" (empty string sentinel for NULL).
	if s := MarshalJSON(nil); s != "" {
		t.Errorf("MarshalJSON(nil): expected empty string, got %q", s)
	}
}
