// Package ports defines the outbound port interfaces that domain logic uses to
// interact with external infrastructure. Adapters in internal/adapters/ implement
// these interfaces; the domain package itself has no knowledge of the implementations.
package ports

import (
	"time"

	"github.com/ambient/platform/components/boss/internal/domain"
)

// StoragePort is the repository abstraction used by the coordinator.
// It is derived from the operations that internal/coordinator/server.go
// performs on the concrete db.Repository, lifted to use domain types.
//
// Implementations live in internal/adapters/sqlite/ (SQLite) and may include
// a future internal/adapters/postgres/ for Postgres.
type StoragePort interface {
	// ---- Space operations ----

	// IsEmpty returns true when the store has no spaces (fresh install).
	IsEmpty() (bool, error)

	// ListSpaces returns the names of all spaces.
	ListSpaces() ([]string, error)

	// GetSpace returns the space with the given name, or (nil, nil) if absent.
	GetSpace(name string) (*domain.Space, error)

	// UpsertSpace creates or updates a space record.
	UpsertSpace(space *domain.Space) error

	// DeleteSpace removes a space and all its associated data atomically.
	DeleteSpace(name string) error

	// NextTaskSeqForSpace atomically increments and returns the next task ID
	// sequence number for the given space.
	NextTaskSeqForSpace(spaceName string) (int, error)

	// ---- Agent operations ----

	// ListAgents returns all agent records for a space.
	ListAgents(spaceName string) ([]*domain.AgentRecord, error)

	// GetAgent returns the agent record for the given space and name, or (nil, nil).
	GetAgent(spaceName, agentName string) (*domain.AgentRecord, error)

	// UpsertAgent creates or replaces an agent record in a space.
	UpsertAgent(spaceName string, rec *domain.AgentRecord) error

	// DeleteAgent removes an agent record from a space.
	DeleteAgent(spaceName, agentName string) error

	// ---- Message operations ----

	// SaveMessage persists a message to an agent's inbox.
	SaveMessage(msg *domain.Message) error

	// GetMessages returns messages for an agent, optionally filtered to those
	// after the given timestamp. A nil since returns all messages.
	GetMessages(spaceName, agentName string, since *time.Time) ([]*domain.Message, error)

	// MarkMessageRead marks a specific message as read at the given time.
	MarkMessageRead(id string, at time.Time) error

	// DeleteMessages removes all messages for an agent (called on agent deletion).
	DeleteMessages(spaceName, agentName string) error

	// ---- Task operations ----

	// UpsertTask creates or updates a task within a space.
	UpsertTask(task *domain.Task) error

	// GetTask returns a task by ID including its comments and events.
	// Returns (nil, nil) if the task does not exist.
	GetTask(spaceName, taskID string) (*domain.Task, error)

	// ListTasks returns tasks for a space with optional key-value filters.
	// Supported filter keys: "status", "assigned_to", "priority".
	ListTasks(spaceName string, filters map[string]string) ([]*domain.Task, error)

	// DeleteTask removes a task and its comments and events.
	DeleteTask(spaceName, taskID string) error

	// SaveTaskComment adds a comment to a task.
	SaveTaskComment(comment *domain.TaskComment) error

	// SaveTaskEvent records a lifecycle event for a task.
	SaveTaskEvent(event *domain.TaskEvent) error

	// ---- Status snapshot operations ----

	// SaveSnapshot persists a point-in-time agent status snapshot.
	SaveSnapshot(snap *domain.StatusSnapshot) error

	// GetSnapshots returns status snapshots for a space, optionally filtered by
	// agent name and/or a start time.
	GetSnapshots(spaceName, agentName string, since *time.Time) ([]*domain.StatusSnapshot, error)

	// ---- Space event log operations ----

	// AppendSpaceEvent persists a structured coordinator event for a space.
	AppendSpaceEvent(event *domain.SpaceEvent) error

	// LoadSpaceEventsSince returns events at or after the given time.
	// A zero since value returns all retained events.
	LoadSpaceEventsSince(spaceName string, since time.Time) ([]*domain.SpaceEvent, error)

	// ---- Settings operations ----

	// GetSetting returns the value for the given key, or ("", nil) if absent.
	GetSetting(key string) (string, error)

	// SetSetting upserts a key-value configuration setting.
	SetSetting(key, value string) error

	// ---- Interrupt operations ----

	// SaveInterrupt persists a blocking decision request.
	SaveInterrupt(rec *domain.Interrupt) error

	// LoadInterrupts returns all interrupts for a space ordered by creation time.
	LoadInterrupts(spaceName string) ([]*domain.Interrupt, error)

	// ResolveInterrupt marks the interrupt as resolved with the given answer.
	ResolveInterrupt(spaceName, id, resolvedBy, answer string) error

	// DeleteInterrupts removes all interrupt records for a space.
	DeleteInterrupts(spaceName string) error
}
