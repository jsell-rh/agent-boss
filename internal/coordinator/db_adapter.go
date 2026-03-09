package coordinator

// db_adapter.go: SQLite repository helpers.
// Provides the DB-side methods that storage.go and history.go call.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	bossdb "github.com/ambient/platform/components/boss/internal/coordinator/db"
)

// ---- Startup load -----------------------------------------------------------

func (s *Server) loadAllSpacesFromRepo() error {
	names, err := s.repo.ListSpaces()
	if err != nil {
		return fmt.Errorf("list spaces from db: %w", err)
	}
	for _, name := range names {
		ks, err := s.loadSpaceFromRepo(name)
		if err != nil {
			s.logEvent(fmt.Sprintf("failed to load space %q from db: %v", name, err))
			continue
		}
		s.spaces[name] = ks
		s.logEvent(fmt.Sprintf("loaded space %q (%d agents) from db", name, len(ks.Agents)))
	}
	return nil
}

func (s *Server) loadSpaceFromRepo(name string) (*KnowledgeSpace, error) {
	sp, err := s.repo.GetSpace(name)
	if err != nil {
		return nil, fmt.Errorf("get space: %w", err)
	}
	if sp == nil {
		return nil, fmt.Errorf("space %q not found in db", name)
	}
	ks := &KnowledgeSpace{
		Name:            sp.Name,
		SharedContracts: sp.SharedContracts,
		Archive:         sp.Archive,
		NextTaskSeq:     sp.NextTaskSeq,
		CreatedAt:       sp.CreatedAt,
		UpdatedAt:       sp.UpdatedAt,
		Agents:          make(map[string]*AgentUpdate),
		Tasks:           make(map[string]*Task),
	}
	agents, err := s.repo.ListAgents(name)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	for _, a := range agents {
		au := dbAgentToUpdate(a)
		msgs, loadErr := s.repo.GetMessages(name, a.AgentName, nil)
		if loadErr != nil {
			return nil, fmt.Errorf("load messages for %s: %w", a.AgentName, loadErr)
		}
		const maxMessages = 50
		if len(msgs) > maxMessages {
			msgs = msgs[len(msgs)-maxMessages:]
		}
		au.Messages = make([]AgentMessage, len(msgs))
		for i, m := range msgs {
			au.Messages[i] = dbMessageToUpdate(m)
		}
		notifs, loadErr := s.repo.GetNotifications(name, a.AgentName)
		if loadErr != nil {
			return nil, fmt.Errorf("load notifications for %s: %w", a.AgentName, loadErr)
		}
		au.Notifications = make([]AgentNotification, len(notifs))
		for i, n := range notifs {
			au.Notifications[i] = dbNotificationToUpdate(n)
		}
		ks.Agents[a.AgentName] = au
	}
	tasks, err := s.repo.ListTasks(name, nil)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	for _, t := range tasks {
		dbTask, comments, events, getErr := s.repo.GetTask(name, t.ID)
		if getErr != nil || dbTask == nil {
			continue
		}
		ks.Tasks[t.ID] = dbTaskToCoordinator(dbTask, comments, events)
	}
	rebuildChildren(ks)
	return ks, nil
}

// ---- Space write-through ----------------------------------------------------

func (s *Server) persistSpaceToDB(ks *KnowledgeSpace) {
	if s.repo == nil {
		return
	}
	sp := &bossdb.Space{
		Name:            ks.Name,
		SharedContracts: ks.SharedContracts,
		Archive:         ks.Archive,
		NextTaskSeq:     ks.NextTaskSeq,
		CreatedAt:       ks.CreatedAt,
		UpdatedAt:       ks.UpdatedAt,
	}
	if err := s.repo.UpsertSpace(sp); err != nil {
		s.logEvent(fmt.Sprintf("warning: db persist space %q: %v", ks.Name, err))
		return
	}
	for agentName, au := range ks.Agents {
		s.upsertAgentToDB(ks.Name, agentName, au)
		for i := range au.Messages {
			s.saveMessageToDB(ks.Name, agentName, &au.Messages[i])
		}
		for i := range au.Notifications {
			s.saveNotificationToDB(ks.Name, agentName, &au.Notifications[i])
		}
	}
	for _, task := range ks.Tasks {
		s.upsertTaskToDB(ks.Name, task)
		for i := range task.Comments {
			s.saveTaskCommentToDB(ks.Name, task, &task.Comments[i])
		}
		for i := range task.Events {
			s.saveTaskEventToDB(ks.Name, task, &task.Events[i])
		}
	}
}

// ---- Incremental write helpers ----------------------------------------------

func (s *Server) upsertAgentToDB(spaceName, agentName string, au *AgentUpdate) {
	if s.repo == nil {
		return
	}
	if err := s.repo.UpsertAgent(coordinatorAgentToDB(spaceName, agentName, au)); err != nil {
		s.logEvent(fmt.Sprintf("warning: upsert agent %s/%s: %v", spaceName, agentName, err))
	}
}

func (s *Server) upsertTaskToDB(spaceName string, task *Task) {
	if s.repo == nil {
		return
	}
	if err := s.repo.UpsertTask(coordinatorTaskToDB(spaceName, task)); err != nil {
		s.logEvent(fmt.Sprintf("warning: upsert task %s: %v", task.ID, err))
	}
}

func (s *Server) upsertSpaceToDB(ks *KnowledgeSpace) {
	if s.repo == nil {
		return
	}
	sp := &bossdb.Space{
		Name:            ks.Name,
		SharedContracts: ks.SharedContracts,
		Archive:         ks.Archive,
		NextTaskSeq:     ks.NextTaskSeq,
		CreatedAt:       ks.CreatedAt,
		UpdatedAt:       ks.UpdatedAt,
	}
	if err := s.repo.UpsertSpace(sp); err != nil {
		s.logEvent(fmt.Sprintf("warning: upsert space %s: %v", ks.Name, err))
	}
}

func (s *Server) saveMessageToDB(spaceName, agentName string, msg *AgentMessage) {
	if s.repo == nil {
		return
	}
	dbMsg := &bossdb.AgentMessage{
		ID:        msg.ID,
		SpaceName: spaceName,
		AgentName: agentName,
		Message:   msg.Message,
		Sender:    msg.Sender,
		Priority:  string(msg.Priority),
		Timestamp: msg.Timestamp,
		Read:      msg.Read,
	}
	if msg.ReadAt != nil {
		dbMsg.ReadAt = sql.NullTime{Time: *msg.ReadAt, Valid: true}
	}
	if err := s.repo.SaveMessage(dbMsg); err != nil {
		s.logEvent(fmt.Sprintf("warning: save message %s: %v", msg.ID, err))
	}
}

func (s *Server) saveNotificationToDB(spaceName, agentName string, n *AgentNotification) {
	if s.repo == nil {
		return
	}
	dbN := &bossdb.AgentNotification{
		ID:        n.ID,
		SpaceName: spaceName,
		AgentName: agentName,
		Type:      string(n.Type),
		Title:     n.Title,
		Body:      n.Body,
		FromAgent: n.From,
		TaskID:    n.TaskID,
		Timestamp: n.Timestamp,
		Read:      n.Read,
	}
	if err := s.repo.SaveNotification(dbN); err != nil {
		s.logEvent(fmt.Sprintf("warning: save notification %s: %v", n.ID, err))
	}
}

func (s *Server) saveTaskCommentToDB(spaceName string, task *Task, c *TaskComment) {
	if s.repo == nil {
		return
	}
	if err := s.repo.SaveComment(&bossdb.TaskComment{
		ID:        c.ID,
		TaskID:    task.ID,
		SpaceName: spaceName,
		Author:    c.Author,
		Body:      c.Body,
		CreatedAt: c.CreatedAt,
	}); err != nil {
		s.logEvent(fmt.Sprintf("warning: save task comment: %v", err))
	}
}

func (s *Server) saveTaskEventToDB(spaceName string, task *Task, ev *TaskEvent) {
	if s.repo == nil {
		return
	}
	if err := s.repo.SaveTaskEvent(&bossdb.TaskEvent{
		ID:        ev.ID,
		TaskID:    task.ID,
		SpaceName: spaceName,
		Type:      ev.Type,
		By:        ev.By,
		Detail:    ev.Detail,
		CreatedAt: ev.CreatedAt,
	}); err != nil {
		s.logEvent(fmt.Sprintf("warning: save task event: %v", err))
	}
}

func (s *Server) saveSnapshotToDB(snap *StatusSnapshot) {
	if s.repo == nil {
		return
	}
	if err := s.repo.SaveSnapshot(&bossdb.StatusSnapshot{
		AgentName:      snap.AgentName,
		SpaceName:      snap.Space,
		Status:         string(snap.Status),
		InferredStatus: snap.InferredStatus,
		Stale:          snap.Stale,
		Timestamp:      snap.Timestamp,
	}); err != nil {
		s.logEvent(fmt.Sprintf("warning: save snapshot: %v", err))
	}
}

func (s *Server) deleteAgentFromDB(spaceName, agentName string) {
	if s.repo == nil {
		return
	}
	if err := s.repo.DeleteAgent(spaceName, agentName); err != nil {
		s.logEvent(fmt.Sprintf("warning: delete agent %s/%s: %v", spaceName, agentName, err))
	}
	if err := s.repo.DeleteMessages(spaceName, agentName); err != nil {
		s.logEvent(fmt.Sprintf("warning: delete messages for %s/%s: %v", spaceName, agentName, err))
	}
}

func (s *Server) deleteTaskFromDB(spaceName, taskID string) {
	if s.repo == nil {
		return
	}
	if err := s.repo.DeleteTask(spaceName, taskID); err != nil {
		s.logEvent(fmt.Sprintf("warning: delete task %s: %v", taskID, err))
	}
}

func (s *Server) loadSnapshotsFromRepo(spaceName, agentFilter string, since *time.Time) ([]StatusSnapshot, error) {
	if s.repo == nil {
		return nil, nil
	}
	dbSnaps, err := s.repo.GetSnapshots(spaceName, agentFilter, since)
	if err != nil {
		return nil, err
	}
	out := make([]StatusSnapshot, len(dbSnaps))
	for i, snap := range dbSnaps {
		out[i] = dbSnapshotToCoordinator(snap)
	}
	return out, nil
}

// ---- coordinator → db conversions ------------------------------------------

func coordinatorAgentToDB(spaceName, agentName string, a *AgentUpdate) *bossdb.Agent {
	agent := &bossdb.Agent{
		SpaceName:      spaceName,
		AgentName:      agentName,
		Status:         string(a.Status),
		Summary:        a.Summary,
		Branch:         a.Branch,
		Worktree:       a.Worktree,
		PR:             a.PR,
		Phase:          a.Phase,
		Items:          bossdb.MarshalJSON(a.Items),
		Sections:       marshalRaw(a.Sections),
		Questions:      bossdb.MarshalJSON(a.Questions),
		Blockers:       bossdb.MarshalJSON(a.Blockers),
		Documents:      marshalRaw(a.Documents),
		NextSteps:      a.NextSteps,
		FreeText:       a.FreeText,
		TmuxSession:    a.TmuxSession,
		RepoURL:        a.RepoURL,
		Parent:         a.Parent,
		Children:       bossdb.MarshalJSON(a.Children),
		Role:           a.Role,
		InferredStatus: a.InferredStatus,
		Stale:          a.Stale,
		Registration:   marshalRaw(a.Registration),
		LastHeartbeat:  a.LastHeartbeat,
		HeartbeatStale: a.HeartbeatStale,
		UpdatedAt:      a.UpdatedAt,
	}
	if a.TestCount != nil {
		agent.TestCount = sql.NullInt64{Int64: int64(*a.TestCount), Valid: true}
	}
	return agent
}

func coordinatorTaskToDB(spaceName string, t *Task) *bossdb.Task {
	task := &bossdb.Task{
		ID:           t.ID,
		SpaceName:    spaceName,
		Title:        t.Title,
		Description:  t.Description,
		Status:       string(t.Status),
		Priority:     string(t.Priority),
		AssignedTo:   t.AssignedTo,
		CreatedBy:    t.CreatedBy,
		Labels:       bossdb.MarshalJSON(t.Labels),
		ParentTask:   t.ParentTask,
		Subtasks:     bossdb.MarshalJSON(t.Subtasks),
		LinkedBranch: t.LinkedBranch,
		LinkedPR:     t.LinkedPR,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
	if t.DueAt != nil {
		task.DueAt = sql.NullTime{Time: *t.DueAt, Valid: true}
	}
	return task
}

// ---- db → coordinator conversions ------------------------------------------

func dbAgentToUpdate(a *bossdb.Agent) *AgentUpdate {
	u := &AgentUpdate{
		Status:         AgentStatus(a.Status),
		Summary:        a.Summary,
		Branch:         a.Branch,
		Worktree:       a.Worktree,
		PR:             a.PR,
		Phase:          a.Phase,
		NextSteps:      a.NextSteps,
		FreeText:       a.FreeText,
		TmuxSession:    a.TmuxSession,
		RepoURL:        a.RepoURL,
		Parent:         a.Parent,
		Role:           a.Role,
		InferredStatus: a.InferredStatus,
		Stale:          a.Stale,
		LastHeartbeat:  a.LastHeartbeat,
		HeartbeatStale: a.HeartbeatStale,
		UpdatedAt:      a.UpdatedAt,
	}
	if a.TestCount.Valid {
		n := int(a.TestCount.Int64)
		u.TestCount = &n
	}
	_ = bossdb.UnmarshalJSON(a.Items, &u.Items)
	_ = bossdb.UnmarshalJSON(a.Questions, &u.Questions)
	_ = bossdb.UnmarshalJSON(a.Blockers, &u.Blockers)
	_ = bossdb.UnmarshalJSON(a.Children, &u.Children)
	if a.Sections != "" && a.Sections != "null" {
		_ = json.Unmarshal([]byte(a.Sections), &u.Sections)
	}
	if a.Documents != "" && a.Documents != "null" {
		_ = json.Unmarshal([]byte(a.Documents), &u.Documents)
	}
	if a.Registration != "" && a.Registration != "null" {
		var reg AgentRegistration
		if json.Unmarshal([]byte(a.Registration), &reg) == nil {
			u.Registration = &reg
		}
	}
	return u
}

func dbMessageToUpdate(m *bossdb.AgentMessage) AgentMessage {
	msg := AgentMessage{
		ID:        m.ID,
		Message:   m.Message,
		Sender:    m.Sender,
		Priority:  MessagePriority(m.Priority),
		Timestamp: m.Timestamp,
		Read:      m.Read,
	}
	if m.ReadAt.Valid {
		t := m.ReadAt.Time
		msg.ReadAt = &t
	}
	return msg
}

func dbNotificationToUpdate(n *bossdb.AgentNotification) AgentNotification {
	return AgentNotification{
		ID:        n.ID,
		Type:      NotificationType(n.Type),
		Title:     n.Title,
		Body:      n.Body,
		From:      n.FromAgent,
		TaskID:    n.TaskID,
		Timestamp: n.Timestamp,
		Read:      n.Read,
	}
}

func dbTaskToCoordinator(t *bossdb.Task, comments []*bossdb.TaskComment, events []*bossdb.TaskEvent) *Task {
	task := &Task{
		ID:           t.ID,
		Space:        t.SpaceName,
		Title:        t.Title,
		Description:  t.Description,
		Status:       TaskStatus(t.Status),
		Priority:     TaskPriority(t.Priority),
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
	_ = bossdb.UnmarshalJSON(t.Labels, &task.Labels)
	_ = bossdb.UnmarshalJSON(t.Subtasks, &task.Subtasks)
	task.Comments = make([]TaskComment, len(comments))
	for i, c := range comments {
		task.Comments[i] = TaskComment{ID: c.ID, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt}
	}
	task.Events = make([]TaskEvent, len(events))
	for i, e := range events {
		task.Events[i] = TaskEvent{ID: e.ID, Type: e.Type, By: e.By, Detail: e.Detail, CreatedAt: e.CreatedAt}
	}
	return task
}

func dbSnapshotToCoordinator(s *bossdb.StatusSnapshot) StatusSnapshot {
	return StatusSnapshot{
		AgentName:      s.AgentName,
		Space:          s.SpaceName,
		Status:         AgentStatus(s.Status),
		InferredStatus: s.InferredStatus,
		Stale:          s.Stale,
		Timestamp:      s.Timestamp,
	}
}

func marshalRaw(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return ""
	}
	return string(b)
}
