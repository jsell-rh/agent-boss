package coordinator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Server) handleSpaceTasks(w http.ResponseWriter, r *http.Request, spaceName, rest string) {
	if rest == "" {
		// Collection: POST (create) or GET (list)
		switch r.Method {
		case http.MethodPost:
			s.handleTaskCreate(w, r, spaceName)
		case http.MethodGet:
			s.handleTaskList(w, r, spaceName)
		default:
			writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Split rest into taskID and optional action.
	slashIdx := strings.Index(rest, "/")
	var taskID, action string
	if slashIdx < 0 {
		taskID = rest
		action = ""
	} else {
		taskID = rest[:slashIdx]
		action = rest[slashIdx+1:]
	}

	if action == "" {
		// /tasks/{id}: GET, PUT, DELETE
		switch r.Method {
		case http.MethodGet:
			s.handleTaskGet(w, r, spaceName, taskID)
		case http.MethodPut:
			s.handleTaskUpdate(w, r, spaceName, taskID)
		case http.MethodDelete:
			s.handleTaskDelete(w, r, spaceName, taskID)
		default:
			writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch action {
	case "move":
		s.handleTaskMove(w, r, spaceName, taskID)
	case "assign":
		s.handleTaskAssign(w, r, spaceName, taskID)
	case "comment":
		s.handleTaskComment(w, r, spaceName, taskID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request, spaceName string) {
	caller := r.Header.Get("X-Agent-Name")
	if caller == "" {
		writeJSONError(w, "missing X-Agent-Name header", http.StatusBadRequest)
		return
	}

	var req struct {
		Title        string       `json:"title"`
		Description  string       `json:"description"`
		Priority     TaskPriority `json:"priority"`
		AssignedTo   string       `json:"assigned_to"`
		Labels       []string     `json:"labels"`
		ParentTask   string       `json:"parent_task"`
		LinkedBranch string       `json:"linked_branch"`
		LinkedPR     string       `json:"linked_pr"`
		DueAt        *time.Time   `json:"due_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSONError(w, "title is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	ks := s.getOrCreateSpaceLocked(spaceName)
	ks.NextTaskSeq++
	id := fmt.Sprintf("TASK-%03d", ks.NextTaskSeq)
	now := time.Now().UTC()
	task := &Task{
		ID:           id,
		Space:        spaceName,
		Title:        strings.TrimSpace(req.Title),
		Description:  req.Description,
		Status:       TaskStatusBacklog,
		Priority:     req.Priority,
		AssignedTo:   req.AssignedTo,
		CreatedBy:    caller,
		Labels:       req.Labels,
		ParentTask:   req.ParentTask,
		LinkedBranch: req.LinkedBranch,
		LinkedPR:     req.LinkedPR,
		DueAt:        req.DueAt,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if ks.Tasks == nil {
		ks.Tasks = make(map[string]*Task)
	}
	ks.Tasks[id] = task
	ks.UpdatedAt = now
	taskCopy := *task
	snap := ks.snapshot()
	s.mu.Unlock()

	s.journal.Append(spaceName, EventTaskCreated, "", taskCopy)
	s.saveSpace(snap)

	// Broadcast SSE
	if sseData, err := json.Marshal(map[string]any{
		"id": taskCopy.ID, "space": spaceName, "status": taskCopy.Status,
		"title": taskCopy.Title, "assigned_to": taskCopy.AssignedTo,
	}); err == nil {
		s.broadcastSSE(spaceName, "", "task_updated", string(sseData))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(taskCopy)
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request, spaceName string) {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tasks": []any{}, "total": 0})
		return
	}

	filterStatus := r.URL.Query().Get("status")
	filterAssigned := r.URL.Query().Get("assigned_to")
	filterLabel := r.URL.Query().Get("label")
	filterPriority := r.URL.Query().Get("priority")

	s.mu.RLock()
	tasks := make([]*Task, 0, len(ks.Tasks))
	for _, t := range ks.Tasks {
		if filterStatus != "" && string(t.Status) != filterStatus {
			continue
		}
		if filterAssigned != "" && !strings.EqualFold(t.AssignedTo, filterAssigned) {
			continue
		}
		if filterPriority != "" && string(t.Priority) != filterPriority {
			continue
		}
		if filterLabel != "" {
			found := false
			for _, l := range t.Labels {
				if l == filterLabel {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		cp := *t
		tasks = append(tasks, &cp)
	}
	s.mu.RUnlock()

	// Stable sort by ID
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tasks": tasks, "total": len(tasks)})
}

func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request, spaceName, taskID string) {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, "space not found", http.StatusNotFound)
		return
	}
	s.mu.RLock()
	task, ok := ks.Tasks[taskID]
	var cp Task
	if ok {
		cp = *task
	}
	s.mu.RUnlock()
	if !ok {
		writeJSONError(w, fmt.Sprintf("task %q not found", taskID), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cp)
}

func (s *Server) handleTaskUpdate(w http.ResponseWriter, r *http.Request, spaceName, taskID string) {
	caller := r.Header.Get("X-Agent-Name")
	if caller == "" {
		writeJSONError(w, "missing X-Agent-Name header", http.StatusBadRequest)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, "space not found", http.StatusNotFound)
		return
	}

	var req struct {
		Title        *string       `json:"title"`
		Description  *string       `json:"description"`
		Status       *TaskStatus   `json:"status"`
		Priority     *TaskPriority `json:"priority"`
		AssignedTo   *string       `json:"assigned_to"`
		Labels       []string      `json:"labels"`
		LinkedBranch *string       `json:"linked_branch"`
		LinkedPR     *string       `json:"linked_pr"`
		DueAt        *time.Time    `json:"due_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	task, exists := ks.Tasks[taskID]
	if !exists {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("task %q not found", taskID), http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	if req.Title != nil {
		task.Title = strings.TrimSpace(*req.Title)
	}
	if req.Description != nil {
		task.Description = *req.Description
	}
	if req.Status != nil {
		if !req.Status.Valid() {
			s.mu.Unlock()
			writeJSONError(w, fmt.Sprintf("invalid status %q", *req.Status), http.StatusBadRequest)
			return
		}
		task.Status = *req.Status
	}
	if req.Priority != nil {
		task.Priority = *req.Priority
	}
	if req.AssignedTo != nil {
		task.AssignedTo = *req.AssignedTo
	}
	if req.Labels != nil {
		task.Labels = req.Labels
	}
	if req.LinkedBranch != nil {
		task.LinkedBranch = *req.LinkedBranch
	}
	if req.LinkedPR != nil {
		task.LinkedPR = *req.LinkedPR
	}
	if req.DueAt != nil {
		task.DueAt = req.DueAt
	}
	task.UpdatedAt = now
	taskCopy := *task
	snap := ks.snapshot()
	s.mu.Unlock()

	s.journal.Append(spaceName, EventTaskUpdated, "", taskCopy)
	s.saveSpace(snap)

	if sseData, err := json.Marshal(map[string]any{
		"id": taskCopy.ID, "space": spaceName, "status": taskCopy.Status,
		"title": taskCopy.Title, "assigned_to": taskCopy.AssignedTo,
	}); err == nil {
		s.broadcastSSE(spaceName, "", "task_updated", string(sseData))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(taskCopy)
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request, spaceName, taskID string) {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, "space not found", http.StatusNotFound)
		return
	}
	s.mu.Lock()
	_, exists := ks.Tasks[taskID]
	if !exists {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("task %q not found", taskID), http.StatusNotFound)
		return
	}
	delete(ks.Tasks, taskID)
	ks.UpdatedAt = time.Now().UTC()
	snap := ks.snapshot()
	s.mu.Unlock()

	s.journal.Append(spaceName, EventTaskDeleted, "", map[string]string{"id": taskID})
	s.saveSpace(snap)

	if sseData, err := json.Marshal(map[string]any{"id": taskID, "space": spaceName, "deleted": true}); err == nil {
		s.broadcastSSE(spaceName, "", "task_updated", string(sseData))
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTaskMove(w http.ResponseWriter, r *http.Request, spaceName, taskID string) {
	caller := r.Header.Get("X-Agent-Name")
	if caller == "" {
		writeJSONError(w, "missing X-Agent-Name header", http.StatusBadRequest)
		return
	}

	var req struct {
		Status TaskStatus `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if !req.Status.Valid() {
		writeJSONError(w, fmt.Sprintf("invalid status %q", req.Status), http.StatusBadRequest)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, "space not found", http.StatusNotFound)
		return
	}

	s.mu.Lock()
	task, exists := ks.Tasks[taskID]
	if !exists {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("task %q not found", taskID), http.StatusNotFound)
		return
	}
	fromStatus := task.Status
	task.Status = req.Status
	task.UpdatedAt = time.Now().UTC()
	taskCopy := *task
	snap := ks.snapshot()
	s.mu.Unlock()

	s.journal.Append(spaceName, EventTaskMoved, "", map[string]string{
		"id": taskID, "from_status": string(fromStatus), "status": string(req.Status), "by": caller,
	})
	s.saveSpace(snap)

	if sseData, err := json.Marshal(map[string]any{
		"id": taskID, "space": spaceName, "status": taskCopy.Status, "assigned_to": taskCopy.AssignedTo,
	}); err == nil {
		s.broadcastSSE(spaceName, "", "task_updated", string(sseData))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(taskCopy)
}

func (s *Server) handleTaskAssign(w http.ResponseWriter, r *http.Request, spaceName, taskID string) {
	caller := r.Header.Get("X-Agent-Name")
	if caller == "" {
		writeJSONError(w, "missing X-Agent-Name header", http.StatusBadRequest)
		return
	}

	var req struct {
		AssignedTo string `json:"assigned_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, "space not found", http.StatusNotFound)
		return
	}

	s.mu.Lock()
	task, exists := ks.Tasks[taskID]
	if !exists {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("task %q not found", taskID), http.StatusNotFound)
		return
	}
	fromAgent := task.AssignedTo
	task.AssignedTo = req.AssignedTo
	task.UpdatedAt = time.Now().UTC()
	taskCopy := *task
	snap := ks.snapshot()
	s.mu.Unlock()

	s.journal.Append(spaceName, EventTaskAssigned, "", map[string]string{
		"id": taskID, "from_agent": fromAgent, "assigned_to": req.AssignedTo, "by": caller,
	})
	s.saveSpace(snap)

	if sseData, err := json.Marshal(map[string]any{
		"id": taskID, "space": spaceName, "status": taskCopy.Status, "assigned_to": taskCopy.AssignedTo,
	}); err == nil {
		s.broadcastSSE(spaceName, "", "task_updated", string(sseData))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(taskCopy)
}

func (s *Server) handleTaskComment(w http.ResponseWriter, r *http.Request, spaceName, taskID string) {
	caller := r.Header.Get("X-Agent-Name")
	if caller == "" {
		writeJSONError(w, "missing X-Agent-Name header", http.StatusBadRequest)
		return
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeJSONError(w, "body is required", http.StatusBadRequest)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, "space not found", http.StatusNotFound)
		return
	}

	s.mu.Lock()
	task, exists := ks.Tasks[taskID]
	if !exists {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("task %q not found", taskID), http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	comment := TaskComment{
		ID:        fmt.Sprintf("%d", now.UnixNano()),
		Author:    caller,
		Body:      req.Body,
		CreatedAt: now,
	}
	task.Comments = append(task.Comments, comment)
	task.UpdatedAt = now
	taskCopy := *task
	snap := ks.snapshot()
	s.mu.Unlock()

	s.journal.Append(spaceName, EventTaskCommented, "", map[string]any{
		"task_id": taskID, "comment": comment,
	})
	s.saveSpace(snap)

	if sseData, err := json.Marshal(map[string]any{
		"id": taskID, "space": spaceName, "status": taskCopy.Status,
	}); err == nil {
		s.broadcastSSE(spaceName, "", "task_updated", string(sseData))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(taskCopy)
}
