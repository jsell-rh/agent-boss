package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository provides all data access operations for agent-boss.
// All methods are safe for concurrent use.
type Repository struct {
	db *gorm.DB
}

// New creates a Repository backed by the given GORM DB.
func New(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// ---- Space operations ----

// UpsertSpace creates or updates a space record.
func (r *Repository) UpsertSpace(s *Space) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"shared_contracts", "archive", "next_task_seq", "updated_at"}),
	}).Create(s).Error
}

// GetSpace returns the space with the given name, or (nil, nil) if not found.
func (r *Repository) GetSpace(name string) (*Space, error) {
	var s Space
	err := r.db.Where("name = ?", name).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &s, err
}

// ListSpaces returns all space names.
func (r *Repository) ListSpaces() ([]string, error) {
	var spaces []Space
	if err := r.db.Select("name").Find(&spaces).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(spaces))
	for i, s := range spaces {
		names[i] = s.Name
	}
	return names, nil
}

// IsEmpty returns true if the DB has no spaces (used to detect fresh install).
func (r *Repository) IsEmpty() (bool, error) {
	var count int64
	if err := r.db.Model(&Space{}).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

// ---- Agent operations ----

// UpsertAgent creates or replaces an agent record.
func (r *Repository) UpsertAgent(a *Agent) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "space_name"}, {Name: "agent_name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "summary", "branch", "worktree", "pr", "phase",
			"test_count", "items", "sections", "questions", "blockers",
			"documents", "next_steps", "free_text", "session_id", "backend_type", "repo_url",
			"parent", "children", "role", "inferred_status", "stale",
			"registration", "last_heartbeat", "heartbeat_stale", "updated_at",
		}),
	}).Create(a).Error
}

// GetAgent returns an agent by space and name, or (nil, nil) if not found.
func (r *Repository) GetAgent(spaceName, agentName string) (*Agent, error) {
	var a Agent
	err := r.db.Where("space_name = ? AND agent_name = ?", spaceName, agentName).First(&a).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &a, err
}

// ListAgents returns all agents for a space.
func (r *Repository) ListAgents(spaceName string) ([]*Agent, error) {
	var agents []*Agent
	return agents, r.db.Where("space_name = ?", spaceName).Find(&agents).Error
}

// SaveAgentTokenHash stores the SHA-256 hex of a per-agent bearer token.
// It upserts only the token_hash column, leaving all other fields unchanged.
// Agent-name lookup is case-insensitive (LIKE in SQLite behaves case-insensitively
// for ASCII; for full Unicode safety we also lowercase the input).
func (r *Repository) SaveAgentTokenHash(spaceName, agentName, tokenHash string) error {
	return r.db.Model(&Agent{}).
		Where("space_name = ? AND LOWER(agent_name) = LOWER(?)", spaceName, agentName).
		Update("token_hash", tokenHash).Error
}

// GetAgentTokenHash returns the stored SHA-256 hex token hash for an agent, or "" if none.
// Agent-name lookup is case-insensitive.
func (r *Repository) GetAgentTokenHash(spaceName, agentName string) (string, error) {
	var a Agent
	err := r.db.Select("token_hash").
		Where("space_name = ? AND LOWER(agent_name) = LOWER(?)", spaceName, agentName).
		First(&a).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return a.TokenHash, err
}

// DeleteAgent removes an agent record.
func (r *Repository) DeleteAgent(spaceName, agentName string) error {
	return r.db.Where("space_name = ? AND agent_name = ?", spaceName, agentName).Delete(&Agent{}).Error
}

// ---- AgentMessage operations ----

// SaveMessage persists an agent message.
func (r *Repository) SaveMessage(m *AgentMessage) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"read", "read_at"}),
	}).Create(m).Error
}

// GetMessages returns messages for an agent, optionally since a timestamp.
func (r *Repository) GetMessages(spaceName, agentName string, since *time.Time) ([]*AgentMessage, error) {
	q := r.db.Where("space_name = ? AND agent_name = ?", spaceName, agentName).Order("timestamp ASC")
	if since != nil {
		q = q.Where("timestamp > ?", *since)
	}
	var msgs []*AgentMessage
	return msgs, q.Find(&msgs).Error
}

// MarkMessageRead marks a message as read.
func (r *Repository) MarkMessageRead(id string, at time.Time) error {
	return r.db.Model(&AgentMessage{}).Where("id = ?", id).Updates(map[string]any{
		"read":    true,
		"read_at": at,
	}).Error
}

// DeleteMessages removes all messages for an agent (used when agent is deleted).
func (r *Repository) DeleteMessages(spaceName, agentName string) error {
	return r.db.Where("space_name = ? AND agent_name = ?", spaceName, agentName).Delete(&AgentMessage{}).Error
}

// DeleteNotifications removes all notifications for an agent (used when agent is deleted).
func (r *Repository) DeleteNotifications(spaceName, agentName string) error {
	return r.db.Where("space_name = ? AND agent_name = ?", spaceName, agentName).Delete(&AgentNotification{}).Error
}

// ---- AgentNotification operations ----

// SaveNotification persists a notification.
func (r *Repository) SaveNotification(n *AgentNotification) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"read"}),
	}).Create(n).Error
}

// GetNotifications returns all notifications for an agent.
func (r *Repository) GetNotifications(spaceName, agentName string) ([]*AgentNotification, error) {
	var notifs []*AgentNotification
	return notifs, r.db.Where("space_name = ? AND agent_name = ?", spaceName, agentName).
		Order("timestamp ASC").Find(&notifs).Error
}

// MarkNotificationsRead marks all notifications for an agent as read.
func (r *Repository) MarkNotificationsRead(spaceName, agentName string) error {
	return r.db.Model(&AgentNotification{}).
		Where("space_name = ? AND agent_name = ? AND read = false", spaceName, agentName).
		Update("read", true).Error
}

// ---- Task operations ----

// UpsertTask creates or updates a task.
// Conflict is on the composite key (space_name, id) so tasks with the same
// ID in different spaces are stored as distinct rows.
func (r *Repository) UpsertTask(t *Task) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "space_name"}, {Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"title", "description", "status", "priority", "assigned_to",
			"labels", "parent_task", "subtasks", "linked_branch", "linked_pr",
			"updated_at", "due_at",
		}),
	}).Create(t).Error
}

// GetTask returns a task by ID, including its comments and events.
func (r *Repository) GetTask(spaceName, taskID string) (*Task, []*TaskComment, []*TaskEvent, error) {
	var t Task
	err := r.db.Where("id = ? AND space_name = ?", taskID, spaceName).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}

	var comments []*TaskComment
	if err := r.db.Where("task_id = ?", taskID).Order("created_at ASC").Find(&comments).Error; err != nil {
		return nil, nil, nil, err
	}

	var events []*TaskEvent
	if err := r.db.Where("task_id = ?", taskID).Order("created_at ASC").Find(&events).Error; err != nil {
		return nil, nil, nil, err
	}

	return &t, comments, events, nil
}

// ListTasks returns all tasks for a space, with optional filters.
func (r *Repository) ListTasks(spaceName string, filters map[string]string) ([]*Task, error) {
	q := r.db.Where("space_name = ?", spaceName).Order("created_at DESC")
	for k, v := range filters {
		switch k {
		case "status":
			q = q.Where("status = ?", v)
		case "assigned_to":
			q = q.Where("assigned_to = ?", v)
		case "priority":
			q = q.Where("priority = ?", v)
		}
	}
	var tasks []*Task
	return tasks, q.Find(&tasks).Error
}

// SaveComment adds a comment to a task.
func (r *Repository) SaveComment(c *TaskComment) error {
	return r.db.Save(c).Error
}

// SaveTaskEvent records a task lifecycle event.
func (r *Repository) SaveTaskEvent(e *TaskEvent) error {
	return r.db.Save(e).Error
}

// DeleteTask removes a task and its comments/events.
func (r *Repository) DeleteTask(spaceName, taskID string) error {
	if err := r.db.Where("task_id = ? AND space_name = ?", taskID, spaceName).Delete(&TaskComment{}).Error; err != nil {
		return err
	}
	if err := r.db.Where("task_id = ? AND space_name = ?", taskID, spaceName).Delete(&TaskEvent{}).Error; err != nil {
		return err
	}
	return r.db.Where("id = ? AND space_name = ?", taskID, spaceName).Delete(&Task{}).Error
}

// ---- Setting operations ----

// GetSetting returns the value for the given key, or ("", nil) if not found.
func (r *Repository) GetSetting(key string) (string, error) {
	var s Setting
	err := r.db.Where("key = ?", key).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return s.Value, err
}

// SetSetting upserts a key-value setting.
func (r *Repository) SetSetting(key, value string) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&Setting{Key: key, Value: value}).Error
}

// DeleteSpace removes a space and all its associated data (agents, messages,
// notifications, tasks, comments, events, snapshots, event log) in a single transaction.
func (r *Repository) DeleteSpace(name string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("space_name = ?", name).Delete(&TaskEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("space_name = ?", name).Delete(&TaskComment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("space_name = ?", name).Delete(&Task{}).Error; err != nil {
			return err
		}
		if err := tx.Where("space_name = ?", name).Delete(&AgentNotification{}).Error; err != nil {
			return err
		}
		if err := tx.Where("space_name = ?", name).Delete(&AgentMessage{}).Error; err != nil {
			return err
		}
		if err := tx.Where("space_name = ?", name).Delete(&StatusSnapshot{}).Error; err != nil {
			return err
		}
		if err := tx.Where("space_name = ?", name).Delete(&SpaceEventLog{}).Error; err != nil {
			return err
		}
		if err := tx.Where("space_name = ?", name).Delete(&InterruptRecord{}).Error; err != nil {
			return err
		}
		if err := tx.Where("space_name = ?", name).Delete(&Agent{}).Error; err != nil {
			return err
		}
		return tx.Where("name = ?", name).Delete(&Space{}).Error
	})
}

// ---- StatusSnapshot operations ----

// SaveSnapshot persists a status snapshot.
func (r *Repository) SaveSnapshot(s *StatusSnapshot) error {
	return r.db.Create(s).Error
}

// GetSnapshots returns status snapshots for a space, optionally filtered by agent and time.
func (r *Repository) GetSnapshots(spaceName string, agentName string, since *time.Time) ([]*StatusSnapshot, error) {
	q := r.db.Where("space_name = ?", spaceName).Order("timestamp ASC")
	if agentName != "" {
		q = q.Where("agent_name = ?", agentName)
	}
	if since != nil {
		q = q.Where("timestamp >= ?", *since)
	}
	var snaps []*StatusSnapshot
	return snaps, q.Find(&snaps).Error
}

// ---- SpaceEventLog operations ----

// EventLogWindowSize is the number of recent events retained per space.
const EventLogWindowSize = 500

// AppendSpaceEvent persists a space event and prunes old events beyond the window.
// Pruning is best-effort (silently ignored on error).
func (r *Repository) AppendSpaceEvent(e *SpaceEventLog) error {
	if err := r.db.Create(e).Error; err != nil {
		return err
	}
	// Prune: keep only the most recent EventLogWindowSize events per space.
	r.db.Exec(
		`DELETE FROM space_event_log WHERE space_name = ? AND id NOT IN (
			SELECT id FROM space_event_log WHERE space_name = ? ORDER BY timestamp DESC LIMIT ?
		)`, e.SpaceName, e.SpaceName, EventLogWindowSize,
	)
	return nil
}

// LoadSpaceEventsSince returns events for a space at or after the given time.
// If since is zero, all retained events are returned.
func (r *Repository) LoadSpaceEventsSince(spaceName string, since time.Time) ([]*SpaceEventLog, error) {
	q := r.db.Where("space_name = ?", spaceName).Order("timestamp ASC")
	if !since.IsZero() {
		q = q.Where("timestamp >= ?", since)
	}
	var events []*SpaceEventLog
	return events, q.Find(&events).Error
}

// ---- JSON helpers ----

// MarshalJSON marshals v to a JSON string, returning "" on error.
func MarshalJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// UnmarshalJSON unmarshals a JSON string into v, ignoring errors on empty input.
func UnmarshalJSON(s string, v any) error {
	if s == "" || s == "null" {
		return nil
	}
	return json.Unmarshal([]byte(s), v)
}

// NextTaskSeqForSpace atomically increments and returns the next task sequence number.
func (r *Repository) NextTaskSeqForSpace(spaceName string) (int, error) {
	var space Space
	err := r.db.Where("name = ?", spaceName).First(&space).Error
	if err != nil {
		return 0, fmt.Errorf("space not found: %w", err)
	}
	next := space.NextTaskSeq + 1
	if err := r.db.Model(&Space{}).Where("name = ?", spaceName).Update("next_task_seq", next).Error; err != nil {
		return 0, err
	}
	return next, nil
}

// ---- InterruptRecord operations ----

// SaveInterrupt upserts an interrupt record.
func (r *Repository) SaveInterrupt(rec *InterruptRecord) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"resolved_by", "answer", "resolved_at", "wait_seconds"}),
	}).Create(rec).Error
}

// LoadInterrupts returns all interrupt records for a space, ordered by creation time.
func (r *Repository) LoadInterrupts(spaceName string) ([]*InterruptRecord, error) {
	var recs []*InterruptRecord
	return recs, r.db.Where("space_name = ?", spaceName).Order("created_at ASC").Find(&recs).Error
}

// ResolveInterrupt marks the interrupt with the given ID as resolved.
// Returns an error if the record is not found or already resolved.
func (r *Repository) ResolveInterrupt(spaceName, id, resolvedBy, answer string) error {
	now := time.Now().UTC()
	var rec InterruptRecord
	if err := r.db.Where("id = ? AND space_name = ?", id, spaceName).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("interrupt %q not found", id)
		}
		return err
	}
	if rec.ResolvedAt.Valid {
		return fmt.Errorf("interrupt %q already resolved", id)
	}
	waitSecs := now.Sub(rec.CreatedAt).Seconds()
	return r.db.Model(&rec).Updates(map[string]any{
		"resolved_by":  resolvedBy,
		"answer":       answer,
		"resolved_at":  now,
		"wait_seconds": waitSecs,
	}).Error
}

// DeleteInterrupts removes all interrupt records for a space.
func (r *Repository) DeleteInterrupts(spaceName string) error {
	return r.db.Where("space_name = ?", spaceName).Delete(&InterruptRecord{}).Error
}
