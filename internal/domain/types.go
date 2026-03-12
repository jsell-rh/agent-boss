// Package domain contains the pure business types for agent-boss.
// This package must import only the Go standard library — no ORM, no HTTP,
// no database drivers. All external concerns are the responsibility of adapters.
package domain

import "time"

// Space is the top-level workspace that groups agents, tasks, and shared contracts.
// Corresponds to coordinator.KnowledgeSpace but without any persistence concerns.
type Space struct {
	Name            string
	Agents          map[string]*AgentRecord
	Tasks           map[string]*Task
	NextTaskSeq     int
	SharedContracts string
	Archive         string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AgentRecord wraps an agent's runtime status and its durable configuration.
// Config is sticky across restarts; Status reflects the agent's last self-report.
type AgentRecord struct {
	Name   string
	Config *AgentConfig
	Status *AgentStatus
}

// AgentStatus is the runtime state most recently reported by an agent.
type AgentStatus struct {
	Status         string
	Summary        string
	Branch         string
	Worktree       string
	PR             string
	Phase          string
	TestCount      *int
	Items          []string
	Sections       []Section
	Questions      []string
	Blockers       []string
	NextSteps      string
	FreeText       string
	SessionID      string
	BackendType    string
	RepoURL        string
	Parent         string
	Children       []string
	Role           string
	InferredStatus string
	Stale          bool
	UpdatedAt      time.Time
}

// Section is a titled list or table within an agent status report.
type Section struct {
	Title string
	Items []string
}

// AgentConfig holds the durable configuration for an agent.
// Unlike AgentStatus (runtime), config fields persist across restarts.
type AgentConfig struct {
	WorkDir       string
	InitialPrompt string
	Backend       string // "tmux" | "ambient"
	Command       string // launch command, default "claude"
	RepoURL       string
	Model         string
}

// Task is the canonical unit of tracked work within a Space.
type Task struct {
	ID           string
	Space        string
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
	Comments     []TaskComment
	Events       []TaskEvent
}

// TaskComment is a human or agent note on a task.
type TaskComment struct {
	ID        string
	TaskID    string
	Space     string
	Author    string
	Body      string
	CreatedAt time.Time
}

// TaskEvent records a point-in-time change to a task.
type TaskEvent struct {
	ID        string
	TaskID    string
	Space     string
	Type      string // "created", "moved", "assigned", "commented", "updated"
	By        string
	Detail    string
	CreatedAt time.Time
}

// Message is a message delivered to a specific agent in a space.
type Message struct {
	ID        string
	Space     string
	Agent     string
	Body      string
	Sender    string
	Priority  string // "info", "directive", "urgent"
	Timestamp time.Time
	Read      bool
}

// StatusSnapshot is a point-in-time record of an agent's status for history.
type StatusSnapshot struct {
	AgentName      string
	Space          string
	Status         string
	InferredStatus string
	Stale          bool
	Timestamp      time.Time
}

// SpaceEvent is a structured event emitted by the coordinator for a space.
type SpaceEvent struct {
	ID        string
	Space     string
	EventType string
	Agent     string
	Payload   []byte
	Timestamp time.Time
}

// Interrupt represents a blocking decision request from an agent.
type Interrupt struct {
	ID          string
	Space       string
	Agent       string
	Type        string
	Question    string
	Context     map[string]string
	ResolvedBy  string
	Answer      string
	ResolvedAt  *time.Time
	WaitSeconds float64
	CreatedAt   time.Time
}
