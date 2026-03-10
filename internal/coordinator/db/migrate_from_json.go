package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// jsonSpace mirrors the on-disk JSON format for migration purposes.
type jsonSpace struct {
	Name            string                    `json:"name"`
	Agents          map[string]*jsonAgent     `json:"agents"`
	Tasks           map[string]*jsonTask      `json:"tasks"`
	NextTaskSeq     int                       `json:"next_task_seq"`
	SharedContracts string                    `json:"shared_contracts"`
	Archive         string                    `json:"archive"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
}

type jsonAgent struct {
	Status         string             `json:"status"`
	Summary        string             `json:"summary"`
	Branch         string             `json:"branch"`
	Worktree       string             `json:"worktree"`
	PR             string             `json:"pr"`
	Phase          string             `json:"phase"`
	TestCount      *int               `json:"test_count"`
	Items          []string           `json:"items"`
	Sections       json.RawMessage    `json:"sections"`
	Questions      []string           `json:"questions"`
	Blockers       []string           `json:"blockers"`
	Documents      json.RawMessage    `json:"documents"`
	NextSteps      string             `json:"next_steps"`
	FreeText       string             `json:"free_text"`
	TmuxSession    string             `json:"tmux_session"`
	RepoURL        string             `json:"repo_url"`
	Parent         string             `json:"parent"`
	Children       []string           `json:"children"`
	Role           string             `json:"role"`
	InferredStatus string             `json:"inferred_status"`
	Stale          bool               `json:"stale"`
	Registration   json.RawMessage    `json:"registration"`
	LastHeartbeat  time.Time          `json:"last_heartbeat"`
	HeartbeatStale bool               `json:"heartbeat_stale"`
	Messages       []jsonMessage      `json:"messages"`
	Notifications  []jsonNotification `json:"notifications"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type jsonMessage struct {
	ID        string     `json:"id"`
	Message   string     `json:"message"`
	Sender    string     `json:"sender"`
	Priority  string     `json:"priority"`
	Timestamp time.Time  `json:"timestamp"`
	Read      bool       `json:"read"`
	ReadAt    *time.Time `json:"read_at"`
}

type jsonNotification struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	From      string    `json:"from"`
	TaskID    string    `json:"task_id"`
	Timestamp time.Time `json:"timestamp"`
	Read      bool      `json:"read"`
}

type jsonTask struct {
	ID           string          `json:"id"`
	Space        string          `json:"space"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Status       string          `json:"status"`
	Priority     string          `json:"priority"`
	AssignedTo   string          `json:"assigned_to"`
	CreatedBy    string          `json:"created_by"`
	Labels       []string        `json:"labels"`
	ParentTask   string          `json:"parent_task"`
	Subtasks     []string        `json:"subtasks"`
	LinkedBranch string          `json:"linked_branch"`
	LinkedPR     string          `json:"linked_pr"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	DueAt        *time.Time      `json:"due_at"`
	Comments     []jsonComment   `json:"comments"`
	Events       []jsonTaskEvent `json:"events"`
}

type jsonComment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type jsonTaskEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	By        string    `json:"by"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// ImportFromDataDir scans dataDir for *.json space files and imports them
// into the repository. It is safe to call multiple times — existing rows are
// skipped via ON CONFLICT DO NOTHING semantics.
func (r *Repository) ImportFromDataDir(dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".events.jsonl") {
			continue
		}
		spaceName := strings.TrimSuffix(name, ".json")

		data, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		var js jsonSpace
		if err := json.Unmarshal(data, &js); err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}

		if err := r.importSpace(spaceName, &js); err != nil {
			return fmt.Errorf("import space %q: %w", spaceName, err)
		}
	}
	return nil
}

func (r *Repository) importSpace(name string, js *jsonSpace) error {
	now := time.Now().UTC()
	createdAt := js.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}

	space := &Space{
		Name:            name,
		SharedContracts: js.SharedContracts,
		Archive:         js.Archive,
		NextTaskSeq:     js.NextTaskSeq,
		CreatedAt:       createdAt,
		UpdatedAt:       now,
	}
	if err := r.UpsertSpace(space); err != nil {
		return fmt.Errorf("upsert space: %w", err)
	}

	for agentName, ja := range js.Agents {
		if err := r.importAgent(name, agentName, ja); err != nil {
			return fmt.Errorf("import agent %q: %w", agentName, err)
		}
	}

	for taskID, jt := range js.Tasks {
		if jt.ID == "" {
			jt.ID = taskID
		}
		if err := r.importTask(name, jt); err != nil {
			return fmt.Errorf("import task %q: %w", taskID, err)
		}
	}

	return nil
}

func (r *Repository) importAgent(spaceName, agentName string, ja *jsonAgent) error {
	var testCount sql.NullInt64
	if ja.TestCount != nil {
		testCount = sql.NullInt64{Int64: int64(*ja.TestCount), Valid: true}
	}

	updatedAt := ja.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	a := &Agent{
		SpaceName:      spaceName,
		AgentName:      agentName,
		Status:         ja.Status,
		Summary:        ja.Summary,
		Branch:         ja.Branch,
		Worktree:       ja.Worktree,
		PR:             ja.PR,
		Phase:          ja.Phase,
		TestCount:      testCount,
		Items:          MarshalJSON(ja.Items),
		NextSteps:      ja.NextSteps,
		FreeText:       ja.FreeText,
		SessionID:    ja.TmuxSession,
		RepoURL:        ja.RepoURL,
		Parent:         ja.Parent,
		Children:       MarshalJSON(ja.Children),
		Role:           ja.Role,
		InferredStatus: ja.InferredStatus,
		Stale:          ja.Stale,
		LastHeartbeat:  ja.LastHeartbeat,
		HeartbeatStale: ja.HeartbeatStale,
		UpdatedAt:      updatedAt,
	}
	if len(ja.Sections) > 0 {
		a.Sections = string(ja.Sections)
	}
	if len(ja.Documents) > 0 {
		a.Documents = string(ja.Documents)
	}
	if len(ja.Registration) > 0 {
		a.Registration = string(ja.Registration)
	}
	if len(ja.Questions) > 0 {
		a.Questions = MarshalJSON(ja.Questions)
	}
	if len(ja.Blockers) > 0 {
		a.Blockers = MarshalJSON(ja.Blockers)
	}

	if err := r.UpsertAgent(a); err != nil {
		return err
	}

	for _, msg := range ja.Messages {
		dbMsg := &AgentMessage{
			ID:        msg.ID,
			SpaceName: spaceName,
			AgentName: agentName,
			Message:   msg.Message,
			Sender:    msg.Sender,
			Priority:  msg.Priority,
			Timestamp: msg.Timestamp,
			Read:      msg.Read,
		}
		if msg.ReadAt != nil {
			dbMsg.ReadAt = sql.NullTime{Time: *msg.ReadAt, Valid: true}
		}
		if err := r.SaveMessage(dbMsg); err != nil {
			return fmt.Errorf("save message %q: %w", msg.ID, err)
		}
	}

	for _, notif := range ja.Notifications {
		dbNotif := &AgentNotification{
			ID:        notif.ID,
			SpaceName: spaceName,
			AgentName: agentName,
			Type:      notif.Type,
			Title:     notif.Title,
			Body:      notif.Body,
			FromAgent: notif.From,
			TaskID:    notif.TaskID,
			Timestamp: notif.Timestamp,
			Read:      notif.Read,
		}
		if err := r.SaveNotification(dbNotif); err != nil {
			return fmt.Errorf("save notification %q: %w", notif.ID, err)
		}
	}

	return nil
}

func (r *Repository) importTask(spaceName string, jt *jsonTask) error {
	var dueAt sql.NullTime
	if jt.DueAt != nil {
		dueAt = sql.NullTime{Time: *jt.DueAt, Valid: true}
	}

	t := &Task{
		ID:           jt.ID,
		SpaceName:    spaceName,
		Title:        jt.Title,
		Description:  jt.Description,
		Status:       string(jt.Status),
		Priority:     string(jt.Priority),
		AssignedTo:   jt.AssignedTo,
		CreatedBy:    jt.CreatedBy,
		Labels:       MarshalJSON(jt.Labels),
		ParentTask:   jt.ParentTask,
		Subtasks:     MarshalJSON(jt.Subtasks),
		LinkedBranch: jt.LinkedBranch,
		LinkedPR:     jt.LinkedPR,
		CreatedAt:    jt.CreatedAt,
		UpdatedAt:    jt.UpdatedAt,
		DueAt:        dueAt,
	}
	if err := r.UpsertTask(t); err != nil {
		return err
	}

	for _, c := range jt.Comments {
		if err := r.SaveComment(&TaskComment{
			ID:        c.ID,
			TaskID:    jt.ID,
			SpaceName: spaceName,
			Author:    c.Author,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		}); err != nil {
			return fmt.Errorf("save comment %q: %w", c.ID, err)
		}
	}

	for _, e := range jt.Events {
		if err := r.SaveTaskEvent(&TaskEvent{
			ID:        e.ID,
			TaskID:    jt.ID,
			SpaceName: spaceName,
			Type:      e.Type,
			By:        e.By,
			Detail:    e.Detail,
			CreatedAt: e.CreatedAt,
		}); err != nil {
			return fmt.Errorf("save task event %q: %w", e.ID, err)
		}
	}

	return nil
}
