package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

// This file provides conversion helpers between db models and the raw
// map/struct values that the coordinator package works with.
//
// Rather than importing coordinator types here (which would create an
// import cycle), we use plain Go types and the coordinator package
// converts them in a thin adapter layer.

// AgentRow is a flat representation of an agent's full state, used when
// loading agents from the DB to reconstruct in-memory KnowledgeSpace maps.
type AgentRow struct {
	SpaceName      string
	AgentName      string
	Status         string
	Summary        string
	Branch         string
	Worktree       string
	PR             string
	Phase          string
	TestCount      *int
	Items          []string
	SectionsRaw    string // raw JSON
	QuestionsRaw   []string
	BlockersRaw    []string
	DocumentsRaw   string // raw JSON
	NextSteps      string
	FreeText       string
	SessionID    string
	RepoURL        string
	Parent         string
	Children       []string
	Role           string
	InferredStatus string
	Stale          bool
	RegistrationRaw string // raw JSON
	LastHeartbeat  time.Time
	HeartbeatStale bool
	UpdatedAt      time.Time
}

// ToAgentRow converts a db.Agent to an AgentRow for coordinator consumption.
func ToAgentRow(a *Agent) *AgentRow {
	row := &AgentRow{
		SpaceName:       a.SpaceName,
		AgentName:       a.AgentName,
		Status:          a.Status,
		Summary:         a.Summary,
		Branch:          a.Branch,
		Worktree:        a.Worktree,
		PR:              a.PR,
		Phase:           a.Phase,
		NextSteps:       a.NextSteps,
		FreeText:        a.FreeText,
		SessionID:     a.SessionID,
		RepoURL:         a.RepoURL,
		Parent:          a.Parent,
		Role:            a.Role,
		InferredStatus:  a.InferredStatus,
		Stale:           a.Stale,
		RegistrationRaw: a.Registration,
		LastHeartbeat:   a.LastHeartbeat,
		HeartbeatStale:  a.HeartbeatStale,
		UpdatedAt:       a.UpdatedAt,
		SectionsRaw:     a.Sections,
		DocumentsRaw:    a.Documents,
	}
	if a.TestCount.Valid {
		n := int(a.TestCount.Int64)
		row.TestCount = &n
	}
	_ = UnmarshalJSON(a.Items, &row.Items)
	_ = UnmarshalJSON(a.Questions, &row.QuestionsRaw)
	_ = UnmarshalJSON(a.Blockers, &row.BlockersRaw)
	_ = UnmarshalJSON(a.Children, &row.Children)
	return row
}

// FromAgentFields converts coordinator-level fields into a db.Agent for persistence.
// testCount may be nil.
func FromAgentFields(
	spaceName, agentName, status, summary, branch, worktree, pr, phase string,
	testCount *int,
	items, questions, blockers, children []string,
	sectionsJSON, documentsJSON, registrationJSON string,
	nextSteps, freeText, tmuxSession, repoURL, parent, role string,
	inferredStatus string,
	stale, heartbeatStale bool,
	lastHeartbeat, updatedAt time.Time,
) *Agent {
	a := &Agent{
		SpaceName:      spaceName,
		AgentName:      agentName,
		Status:         status,
		Summary:        summary,
		Branch:         branch,
		Worktree:       worktree,
		PR:             pr,
		Phase:          phase,
		Items:          MarshalJSON(items),
		Sections:       sectionsJSON,
		Questions:      MarshalJSON(questions),
		Blockers:       MarshalJSON(blockers),
		Documents:      documentsJSON,
		NextSteps:      nextSteps,
		FreeText:       freeText,
		SessionID:    tmuxSession,
		RepoURL:        repoURL,
		Parent:         parent,
		Children:       MarshalJSON(children),
		Role:           role,
		InferredStatus: inferredStatus,
		Stale:          stale,
		Registration:   registrationJSON,
		LastHeartbeat:  lastHeartbeat,
		HeartbeatStale: heartbeatStale,
		UpdatedAt:      updatedAt,
	}
	if testCount != nil {
		a.TestCount = sql.NullInt64{Int64: int64(*testCount), Valid: true}
	}
	return a
}

// MessageRow is a flat message for coordinator consumption.
type MessageRow struct {
	ID        string
	SpaceName string
	AgentName string
	Message   string
	Sender    string
	Priority  string
	Timestamp time.Time
	Read      bool
	ReadAt    *time.Time
}

// ToMessageRow converts a db.AgentMessage to a MessageRow.
func ToMessageRow(m *AgentMessage) *MessageRow {
	row := &MessageRow{
		ID:        m.ID,
		SpaceName: m.SpaceName,
		AgentName: m.AgentName,
		Message:   m.Message,
		Sender:    m.Sender,
		Priority:  m.Priority,
		Timestamp: m.Timestamp,
		Read:      m.Read,
	}
	if m.ReadAt.Valid {
		t := m.ReadAt.Time
		row.ReadAt = &t
	}
	return row
}

// NotificationRow is a flat notification for coordinator consumption.
type NotificationRow struct {
	ID        string
	SpaceName string
	AgentName string
	Type      string
	Title     string
	Body      string
	FromAgent string
	TaskID    string
	Timestamp time.Time
	Read      bool
}

// ToNotificationRow converts a db.AgentNotification to a NotificationRow.
func ToNotificationRow(n *AgentNotification) *NotificationRow {
	return &NotificationRow{
		ID:        n.ID,
		SpaceName: n.SpaceName,
		AgentName: n.AgentName,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		FromAgent: n.FromAgent,
		TaskID:    n.TaskID,
		Timestamp: n.Timestamp,
		Read:      n.Read,
	}
}

// TaskRow bundles a task with its comments and events.
type TaskRow struct {
	ID           string
	SpaceName    string
	Title        string
	Description  string
	Status       string
	Priority     string
	AssignedTo   string
	CreatedBy    string
	Labels       []string
	ParentTask   string
	Subtasks     []string
	LinkedBranch string
	LinkedPR     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DueAt        *time.Time
	Comments     []CommentRow
	Events       []TaskEventRow
}

type CommentRow struct {
	ID        string
	TaskID    string
	Author    string
	Body      string
	CreatedAt time.Time
}

type TaskEventRow struct {
	ID        string
	TaskID    string
	Type      string
	By        string
	Detail    string
	CreatedAt time.Time
}

// ToTaskRow converts db task+comments+events into a TaskRow.
func ToTaskRow(t *Task, comments []*TaskComment, events []*TaskEvent) *TaskRow {
	row := &TaskRow{
		ID:           t.ID,
		SpaceName:    t.SpaceName,
		Title:        t.Title,
		Description:  t.Description,
		Status:       t.Status,
		Priority:     t.Priority,
		AssignedTo:   t.AssignedTo,
		CreatedBy:    t.CreatedBy,
		LinkedBranch: t.LinkedBranch,
		LinkedPR:     t.LinkedPR,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
	if t.DueAt.Valid {
		d := t.DueAt.Time
		row.DueAt = &d
	}
	_ = UnmarshalJSON(t.Labels, &row.Labels)
	_ = UnmarshalJSON(t.Subtasks, &row.Subtasks)

	row.Comments = make([]CommentRow, len(comments))
	for i, c := range comments {
		row.Comments[i] = CommentRow{ID: c.ID, TaskID: c.TaskID, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt}
	}
	row.Events = make([]TaskEventRow, len(events))
	for i, e := range events {
		row.Events[i] = TaskEventRow{ID: e.ID, TaskID: e.TaskID, Type: e.Type, By: e.By, Detail: e.Detail, CreatedAt: e.CreatedAt}
	}
	return row
}

// FromTaskFields builds a db.Task from coordinator-level fields.
func FromTaskFields(
	id, spaceName, title, description, status, priority,
	assignedTo, createdBy string,
	labels, subtasks []string,
	parentTask, linkedBranch, linkedPR string,
	createdAt, updatedAt time.Time,
	dueAt *time.Time,
) *Task {
	t := &Task{
		ID:           id,
		SpaceName:    spaceName,
		Title:        title,
		Description:  description,
		Status:       status,
		Priority:     priority,
		AssignedTo:   assignedTo,
		CreatedBy:    createdBy,
		Labels:       MarshalJSON(labels),
		ParentTask:   parentTask,
		Subtasks:     MarshalJSON(subtasks),
		LinkedBranch: linkedBranch,
		LinkedPR:     linkedPR,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
	if dueAt != nil {
		t.DueAt = sql.NullTime{Time: *dueAt, Valid: true}
	}
	return t
}

// SnapshotRow is a status snapshot for Gantt/history consumption.
type SnapshotRow struct {
	AgentName      string
	SpaceName      string
	Status         string
	InferredStatus string
	Stale          bool
	Timestamp      time.Time
}

// ToSnapshotRow converts a db.StatusSnapshot to a SnapshotRow.
func ToSnapshotRow(s *StatusSnapshot) *SnapshotRow {
	return &SnapshotRow{
		AgentName:      s.AgentName,
		SpaceName:      s.SpaceName,
		Status:         s.Status,
		InferredStatus: s.InferredStatus,
		Stale:          s.Stale,
		Timestamp:      s.Timestamp,
	}
}

// RawJSON safely returns the raw JSON bytes of a value, or nil on error.
func RawJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
