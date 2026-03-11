package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

// StringSlice is a []string that serializes as a JSON array in SQLite.
type StringSlice []string

func (s StringSlice) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	type alias []string
	return json.Marshal(alias(s))
}

func (s *StringSlice) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = nil
		return nil
	}
	type alias []string
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = StringSlice(v)
	return nil
}

// JSONBytes is a helper for arbitrary JSON stored as TEXT in SQLite.
type JSONBytes []byte

func (j JSONBytes) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *JSONBytes) UnmarshalJSON(data []byte) error {
	*j = make(JSONBytes, len(data))
	copy(*j, data)
	return nil
}

// Space represents a KnowledgeSpace row.
type Space struct {
	ID              uint      `gorm:"primarykey;autoIncrement"`
	Name            string    `gorm:"uniqueIndex;not null"`
	SharedContracts string    `gorm:"type:text"`
	Archive         string    `gorm:"type:text"`
	NextTaskSeq     int       `gorm:"default:0"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (Space) TableName() string { return "spaces" }

// Agent represents an AgentUpdate row, keyed by (space_name, agent_name).
type Agent struct {
	ID             uint   `gorm:"primarykey;autoIncrement"`
	SpaceName      string `gorm:"uniqueIndex:idx_space_agent;not null;index"`
	AgentName      string `gorm:"uniqueIndex:idx_space_agent;not null"`
	Status         string `gorm:"not null;default:'idle'"`
	Summary        string `gorm:"type:text"`
	Branch         string
	Worktree       string
	PR             string
	Phase          string
	TestCount      sql.NullInt64
	Items          string `gorm:"type:text"` // JSON array
	Sections       string `gorm:"type:text"` // JSON array
	Questions      string `gorm:"type:text"` // JSON array
	Blockers       string `gorm:"type:text"` // JSON array
	Documents      string `gorm:"type:text"` // JSON array
	NextSteps      string `gorm:"type:text"`
	FreeText       string `gorm:"type:text"`
	SessionID      string
	BackendType    string
	RepoURL        string
	Parent         string
	Children       string `gorm:"type:text"` // JSON array
	Role           string
	InferredStatus string
	Stale          bool
	Registration   string    `gorm:"type:text"` // JSON object
	Config         string    `gorm:"type:text"` // JSON AgentConfig
	LastHeartbeat  time.Time
	HeartbeatStale bool
	UpdatedAt      time.Time
}

func (Agent) TableName() string { return "agents" }

// AgentMessage represents a message delivered to an agent.
type AgentMessage struct {
	ID        string    `gorm:"primarykey"`
	SpaceName string    `gorm:"index;not null"`
	AgentName string    `gorm:"index;not null"`
	Message   string    `gorm:"type:text;not null"`
	Sender    string    `gorm:"not null"`
	Priority  string    `gorm:"default:'info'"`
	Timestamp time.Time `gorm:"index"`
	Read      bool
	ReadAt    sql.NullTime
}

func (AgentMessage) TableName() string { return "agent_messages" }

// AgentNotification represents a typed notification for an agent.
type AgentNotification struct {
	ID        string    `gorm:"primarykey"`
	SpaceName string    `gorm:"index;not null"`
	AgentName string    `gorm:"index;not null"`
	Type      string    `gorm:"not null"`
	Title     string    `gorm:"not null"`
	Body      string    `gorm:"type:text"`
	FromAgent string
	TaskID    string
	Timestamp time.Time `gorm:"index"`
	Read      bool
}

func (AgentNotification) TableName() string { return "agent_notifications" }

// Task represents a tracked work item.
// ID is unique per space (e.g. TASK-001), not globally unique — the composite
// primary key (space_name, id) prevents cross-space collisions in the DB.
type Task struct {
	ID        string `gorm:"primaryKey;not null"`
	SpaceName string `gorm:"primaryKey;not null;index"`
	Title        string    `gorm:"not null"`
	Description  string    `gorm:"type:text"`
	Status       string    `gorm:"not null;default:'backlog'"`
	Priority     string    `gorm:"default:'medium'"`
	AssignedTo   string
	CreatedBy    string    `gorm:"not null"`
	Labels       string    `gorm:"type:text"` // JSON array
	ParentTask   string    `gorm:"index"`
	Subtasks     string    `gorm:"type:text"` // JSON array of task IDs
	LinkedBranch string
	LinkedPR     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DueAt        sql.NullTime
}

func (Task) TableName() string { return "tasks" }

// TaskComment represents a comment on a task.
type TaskComment struct {
	ID        string    `gorm:"primarykey"`
	TaskID    string    `gorm:"index;not null"`
	SpaceName string    `gorm:"index;not null"`
	Author    string    `gorm:"not null"`
	Body      string    `gorm:"type:text;not null"`
	CreatedAt time.Time
}

func (TaskComment) TableName() string { return "task_comments" }

// TaskEvent records a point-in-time change to a task.
type TaskEvent struct {
	ID        string    `gorm:"primarykey"`
	TaskID    string    `gorm:"index;not null"`
	SpaceName string    `gorm:"index;not null"`
	Type      string    `gorm:"not null"`
	By        string    `gorm:"not null"`
	Detail    string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index"`
}

func (TaskEvent) TableName() string { return "task_events" }

// StatusSnapshot records a point-in-time agent status for history/Gantt.
type StatusSnapshot struct {
	ID             uint      `gorm:"primarykey;autoIncrement"`
	AgentName      string    `gorm:"index;not null"`
	SpaceName      string    `gorm:"index;not null"`
	Status         string    `gorm:"not null"`
	InferredStatus string
	Stale          bool
	Timestamp      time.Time `gorm:"index"`
}

func (StatusSnapshot) TableName() string { return "status_snapshots" }

// Setting is a simple key-value table for server-wide configuration.
// Replaces the legacy settings.json file.
type Setting struct {
	Key   string `gorm:"primarykey;not null"`
	Value string `gorm:"type:text"`
}

func (Setting) TableName() string { return "settings" }
