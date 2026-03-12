// Package sqlite provides a StoragePort adapter backed by the existing GORM
// db.Repository. It is a thin translation layer: it converts domain types to
// db (GORM) types and delegates every operation to the wrapped repository.
//
// Nothing in this package is allowed to import internal/coordinator or any
// other adapter — only domain/ and internal/coordinator/db/.
package sqlite

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/ambient/platform/components/boss/internal/coordinator/db"
	"github.com/ambient/platform/components/boss/internal/domain"
)

// StorageAdapter wraps db.Repository to implement domain/ports.StoragePort.
type StorageAdapter struct {
	repo *db.Repository
}

// New creates a StorageAdapter that delegates to the given db.Repository.
func New(repo *db.Repository) *StorageAdapter {
	return &StorageAdapter{repo: repo}
}

// ---- Space operations ----

func (a *StorageAdapter) IsEmpty() (bool, error) {
	return a.repo.IsEmpty()
}

func (a *StorageAdapter) ListSpaces() ([]string, error) {
	return a.repo.ListSpaces()
}

func (a *StorageAdapter) GetSpace(name string) (*domain.Space, error) {
	sp, err := a.repo.GetSpace(name)
	if err != nil || sp == nil {
		return nil, err
	}
	return dbSpaceToDomain(sp), nil
}

func (a *StorageAdapter) UpsertSpace(space *domain.Space) error {
	return a.repo.UpsertSpace(domainSpaceToDB(space))
}

func (a *StorageAdapter) DeleteSpace(name string) error {
	return a.repo.DeleteSpace(name)
}

func (a *StorageAdapter) NextTaskSeqForSpace(spaceName string) (int, error) {
	return a.repo.NextTaskSeqForSpace(spaceName)
}

// ---- Agent operations ----

func (a *StorageAdapter) ListAgents(spaceName string) ([]*domain.AgentRecord, error) {
	agents, err := a.repo.ListAgents(spaceName)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.AgentRecord, len(agents))
	for i, ag := range agents {
		out[i] = dbAgentToDomain(ag)
	}
	return out, nil
}

func (a *StorageAdapter) GetAgent(spaceName, agentName string) (*domain.AgentRecord, error) {
	ag, err := a.repo.GetAgent(spaceName, agentName)
	if err != nil || ag == nil {
		return nil, err
	}
	return dbAgentToDomain(ag), nil
}

func (a *StorageAdapter) UpsertAgent(spaceName string, rec *domain.AgentRecord) error {
	return a.repo.UpsertAgent(domainAgentToDB(spaceName, rec))
}

func (a *StorageAdapter) DeleteAgent(spaceName, agentName string) error {
	return a.repo.DeleteAgent(spaceName, agentName)
}

// ---- Message operations ----

func (a *StorageAdapter) SaveMessage(msg *domain.Message) error {
	return a.repo.SaveMessage(domainMessageToDB(msg))
}

func (a *StorageAdapter) GetMessages(spaceName, agentName string, since *time.Time) ([]*domain.Message, error) {
	msgs, err := a.repo.GetMessages(spaceName, agentName, since)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Message, len(msgs))
	for i, m := range msgs {
		out[i] = dbMessageToDomain(m)
	}
	return out, nil
}

func (a *StorageAdapter) MarkMessageRead(id string, at time.Time) error {
	return a.repo.MarkMessageRead(id, at)
}

func (a *StorageAdapter) DeleteMessages(spaceName, agentName string) error {
	return a.repo.DeleteMessages(spaceName, agentName)
}

// ---- Task operations ----

func (a *StorageAdapter) UpsertTask(task *domain.Task) error {
	return a.repo.UpsertTask(domainTaskToDB(task))
}

func (a *StorageAdapter) GetTask(spaceName, taskID string) (*domain.Task, error) {
	t, comments, events, err := a.repo.GetTask(spaceName, taskID)
	if err != nil || t == nil {
		return nil, err
	}
	return dbTaskToDomain(t, comments, events), nil
}

func (a *StorageAdapter) ListTasks(spaceName string, filters map[string]string) ([]*domain.Task, error) {
	tasks, err := a.repo.ListTasks(spaceName, filters)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Task, len(tasks))
	for i, t := range tasks {
		out[i] = dbTaskToDomain(t, nil, nil)
	}
	return out, nil
}

func (a *StorageAdapter) DeleteTask(spaceName, taskID string) error {
	return a.repo.DeleteTask(spaceName, taskID)
}

func (a *StorageAdapter) SaveTaskComment(comment *domain.TaskComment) error {
	return a.repo.SaveComment(&db.TaskComment{
		ID:        comment.ID,
		TaskID:    comment.TaskID,
		SpaceName: comment.Space,
		Author:    comment.Author,
		Body:      comment.Body,
		CreatedAt: comment.CreatedAt,
	})
}

func (a *StorageAdapter) SaveTaskEvent(event *domain.TaskEvent) error {
	return a.repo.SaveTaskEvent(&db.TaskEvent{
		ID:        event.ID,
		TaskID:    event.TaskID,
		SpaceName: event.Space,
		Type:      event.Type,
		By:        event.By,
		Detail:    event.Detail,
		CreatedAt: event.CreatedAt,
	})
}

// ---- Status snapshot operations ----

func (a *StorageAdapter) SaveSnapshot(snap *domain.StatusSnapshot) error {
	return a.repo.SaveSnapshot(&db.StatusSnapshot{
		AgentName:      snap.AgentName,
		SpaceName:      snap.Space,
		Status:         snap.Status,
		InferredStatus: snap.InferredStatus,
		Stale:          snap.Stale,
		Timestamp:      snap.Timestamp,
	})
}

func (a *StorageAdapter) GetSnapshots(spaceName, agentName string, since *time.Time) ([]*domain.StatusSnapshot, error) {
	snaps, err := a.repo.GetSnapshots(spaceName, agentName, since)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.StatusSnapshot, len(snaps))
	for i, s := range snaps {
		out[i] = &domain.StatusSnapshot{
			AgentName:      s.AgentName,
			Space:          s.SpaceName,
			Status:         s.Status,
			InferredStatus: s.InferredStatus,
			Stale:          s.Stale,
			Timestamp:      s.Timestamp,
		}
	}
	return out, nil
}

// ---- Space event log operations ----

func (a *StorageAdapter) AppendSpaceEvent(event *domain.SpaceEvent) error {
	return a.repo.AppendSpaceEvent(&db.SpaceEventLog{
		ID:        event.ID,
		SpaceName: event.Space,
		EventType: event.EventType,
		Agent:     event.Agent,
		Payload:   string(event.Payload),
		Timestamp: event.Timestamp,
	})
}

func (a *StorageAdapter) LoadSpaceEventsSince(spaceName string, since time.Time) ([]*domain.SpaceEvent, error) {
	evs, err := a.repo.LoadSpaceEventsSince(spaceName, since)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.SpaceEvent, len(evs))
	for i, e := range evs {
		ev := &domain.SpaceEvent{
			ID:        e.ID,
			Space:     e.SpaceName,
			EventType: e.EventType,
			Agent:     e.Agent,
			Timestamp: e.Timestamp,
		}
		if e.Payload != "" {
			ev.Payload = []byte(e.Payload)
		}
		out[i] = ev
	}
	return out, nil
}

// ---- Settings operations ----

func (a *StorageAdapter) GetSetting(key string) (string, error) {
	return a.repo.GetSetting(key)
}

func (a *StorageAdapter) SetSetting(key, value string) error {
	return a.repo.SetSetting(key, value)
}

// ---- Interrupt operations ----

func (a *StorageAdapter) SaveInterrupt(rec *domain.Interrupt) error {
	dbRec := &db.InterruptRecord{
		ID:          rec.ID,
		SpaceName:   rec.Space,
		Agent:       rec.Agent,
		Type:        rec.Type,
		Question:    rec.Question,
		ResolvedBy:  rec.ResolvedBy,
		Answer:      rec.Answer,
		WaitSeconds: rec.WaitSeconds,
		CreatedAt:   rec.CreatedAt,
	}
	if len(rec.Context) > 0 {
		if b, err := json.Marshal(rec.Context); err == nil {
			dbRec.Context = string(b)
		}
	}
	if rec.ResolvedAt != nil {
		dbRec.ResolvedAt = sql.NullTime{Time: *rec.ResolvedAt, Valid: true}
	}
	return a.repo.SaveInterrupt(dbRec)
}

func (a *StorageAdapter) LoadInterrupts(spaceName string) ([]*domain.Interrupt, error) {
	recs, err := a.repo.LoadInterrupts(spaceName)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Interrupt, len(recs))
	for i, r := range recs {
		intr := &domain.Interrupt{
			ID:          r.ID,
			Space:       r.SpaceName,
			Agent:       r.Agent,
			Type:        r.Type,
			Question:    r.Question,
			ResolvedBy:  r.ResolvedBy,
			Answer:      r.Answer,
			WaitSeconds: r.WaitSeconds,
			CreatedAt:   r.CreatedAt,
		}
		if r.Context != "" {
			_ = json.Unmarshal([]byte(r.Context), &intr.Context)
		}
		if r.ResolvedAt.Valid {
			t := r.ResolvedAt.Time
			intr.ResolvedAt = &t
		}
		out[i] = intr
	}
	return out, nil
}

func (a *StorageAdapter) ResolveInterrupt(spaceName, id, resolvedBy, answer string) error {
	return a.repo.ResolveInterrupt(spaceName, id, resolvedBy, answer)
}

func (a *StorageAdapter) DeleteInterrupts(spaceName string) error {
	return a.repo.DeleteInterrupts(spaceName)
}

// ---- db → domain conversions ----

func dbSpaceToDomain(s *db.Space) *domain.Space {
	return &domain.Space{
		Name:            s.Name,
		NextTaskSeq:     s.NextTaskSeq,
		SharedContracts: s.SharedContracts,
		Archive:         s.Archive,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		Agents:          make(map[string]*domain.AgentRecord),
		Tasks:           make(map[string]*domain.Task),
	}
}

func dbAgentToDomain(a *db.Agent) *domain.AgentRecord {
	status := &domain.AgentStatus{
		Status:         a.Status,
		Summary:        a.Summary,
		Branch:         a.Branch,
		Worktree:       a.Worktree,
		PR:             a.PR,
		Phase:          a.Phase,
		NextSteps:      a.NextSteps,
		FreeText:       a.FreeText,
		SessionID:      a.SessionID,
		BackendType:    a.BackendType,
		RepoURL:        a.RepoURL,
		Parent:         a.Parent,
		Role:           a.Role,
		InferredStatus: a.InferredStatus,
		Stale:          a.Stale,
		UpdatedAt:      a.UpdatedAt,
	}
	if a.TestCount.Valid {
		n := int(a.TestCount.Int64)
		status.TestCount = &n
	}
	_ = db.UnmarshalJSON(a.Items, &status.Items)
	_ = db.UnmarshalJSON(a.Questions, &status.Questions)
	_ = db.UnmarshalJSON(a.Blockers, &status.Blockers)
	_ = db.UnmarshalJSON(a.Children, &status.Children)
	if a.Sections != "" && a.Sections != "null" {
		_ = json.Unmarshal([]byte(a.Sections), &status.Sections)
	}

	rec := &domain.AgentRecord{
		Name:   a.AgentName,
		Status: status,
	}
	if a.Config != "" && a.Config != "null" {
		var cfg domain.AgentConfig
		if json.Unmarshal([]byte(a.Config), &cfg) == nil {
			rec.Config = &cfg
		}
	}
	return rec
}

func dbMessageToDomain(m *db.AgentMessage) *domain.Message {
	return &domain.Message{
		ID:        m.ID,
		Space:     m.SpaceName,
		Agent:     m.AgentName,
		Body:      m.Message,
		Sender:    m.Sender,
		Priority:  m.Priority,
		Timestamp: m.Timestamp,
		Read:      m.Read,
	}
}

func dbTaskToDomain(t *db.Task, comments []*db.TaskComment, events []*db.TaskEvent) *domain.Task {
	task := &domain.Task{
		ID:           t.ID,
		Space:        t.SpaceName,
		Title:        t.Title,
		Description:  t.Description,
		Status:       t.Status,
		Priority:     t.Priority,
		AssignedTo:   t.AssignedTo,
		CreatedBy:    t.CreatedBy,
		ParentTask:   t.ParentTask,
		LinkedBranch: t.LinkedBranch,
		LinkedPR:     t.LinkedPR,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
	if t.DueAt.Valid {
		d := t.DueAt.Time
		task.DueAt = &d
	}
	_ = db.UnmarshalJSON(t.Labels, &task.Labels)
	_ = db.UnmarshalJSON(t.Subtasks, &task.Subtasks)
	task.Comments = make([]domain.TaskComment, len(comments))
	for i, c := range comments {
		task.Comments[i] = domain.TaskComment{
			ID:        c.ID,
			TaskID:    c.TaskID,
			Space:     c.SpaceName,
			Author:    c.Author,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		}
	}
	task.Events = make([]domain.TaskEvent, len(events))
	for i, e := range events {
		task.Events[i] = domain.TaskEvent{
			ID:        e.ID,
			TaskID:    e.TaskID,
			Space:     e.SpaceName,
			Type:      e.Type,
			By:        e.By,
			Detail:    e.Detail,
			CreatedAt: e.CreatedAt,
		}
	}
	return task
}

// ---- domain → db conversions ----

func domainSpaceToDB(s *domain.Space) *db.Space {
	return &db.Space{
		Name:            s.Name,
		SharedContracts: s.SharedContracts,
		Archive:         s.Archive,
		NextTaskSeq:     s.NextTaskSeq,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
	}
}

func domainAgentToDB(spaceName string, rec *domain.AgentRecord) *db.Agent {
	agentName := rec.Name
	var status *domain.AgentStatus
	if rec.Status != nil {
		status = rec.Status
	} else {
		status = &domain.AgentStatus{}
	}
	agent := &db.Agent{
		SpaceName:      spaceName,
		AgentName:      agentName,
		Status:         status.Status,
		Summary:        status.Summary,
		Branch:         status.Branch,
		Worktree:       status.Worktree,
		PR:             status.PR,
		Phase:          status.Phase,
		Items:          db.MarshalJSON(status.Items),
		Questions:      db.MarshalJSON(status.Questions),
		Blockers:       db.MarshalJSON(status.Blockers),
		NextSteps:      status.NextSteps,
		FreeText:       status.FreeText,
		SessionID:      status.SessionID,
		BackendType:    status.BackendType,
		RepoURL:        status.RepoURL,
		Parent:         status.Parent,
		Children:       db.MarshalJSON(status.Children),
		Role:           status.Role,
		InferredStatus: status.InferredStatus,
		Stale:          status.Stale,
		UpdatedAt:      status.UpdatedAt,
	}
	if status.TestCount != nil {
		agent.TestCount = sql.NullInt64{Int64: int64(*status.TestCount), Valid: true}
	}
	if len(status.Sections) > 0 {
		if b, err := json.Marshal(status.Sections); err == nil {
			agent.Sections = string(b)
		}
	}
	if rec.Config != nil {
		if b, err := json.Marshal(rec.Config); err == nil {
			agent.Config = string(b)
		}
	}
	return agent
}

func domainMessageToDB(msg *domain.Message) *db.AgentMessage {
	return &db.AgentMessage{
		ID:        msg.ID,
		SpaceName: msg.Space,
		AgentName: msg.Agent,
		Message:   msg.Body,
		Sender:    msg.Sender,
		Priority:  msg.Priority,
		Timestamp: msg.Timestamp,
		Read:      msg.Read,
	}
}

func domainTaskToDB(t *domain.Task) *db.Task {
	task := &db.Task{
		ID:           t.ID,
		SpaceName:    t.Space,
		Title:        t.Title,
		Description:  t.Description,
		Status:       t.Status,
		Priority:     t.Priority,
		AssignedTo:   t.AssignedTo,
		CreatedBy:    t.CreatedBy,
		ParentTask:   t.ParentTask,
		LinkedBranch: t.LinkedBranch,
		LinkedPR:     t.LinkedPR,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
		Labels:       db.MarshalJSON(t.Labels),
		Subtasks:     db.MarshalJSON(t.Subtasks),
	}
	if t.DueAt != nil {
		task.DueAt = sql.NullTime{Time: *t.DueAt, Valid: true}
	}
	return task
}
