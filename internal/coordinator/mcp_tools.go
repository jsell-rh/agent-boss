package coordinator

// mcp_tools.go: MCP tool definitions for agent interactions.
// These tools allow agents to interact with the coordinator natively via MCP
// instead of HTTP/curl. The HTTP API remains available for non-MCP clients.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMCPTools adds all agent-facing tools to the MCP server.
func (s *Server) registerMCPTools(srv *mcp.Server) {
	s.addToolPostStatus(srv)
	s.addToolCheckMessages(srv)
	s.addToolSendMessage(srv)
	s.addToolAckMessage(srv)
	s.addToolRequestDecision(srv)
	s.addToolCreateTask(srv)
	s.addToolListTasks(srv)
	s.addToolMoveTask(srv)
	s.addToolUpdateTask(srv)
	s.addToolSpawnAgent(srv)
	s.addToolRestartAgent(srv)
	s.addToolStopAgent(srv)
}

// jsonSchema builds a JSON Schema object for use as InputSchema.
func jsonSchema(required []string, props map[string]map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

// parseArgs unmarshals CallToolRequest arguments into a map.
func parseArgs(req *mcp.CallToolRequest) (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return args, nil
}

// --- post_status ---

func (s *Server) addToolPostStatus(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "post_status",
		Description: "Post your current status to the coordinator. Call this regularly to report progress.",
		InputSchema: jsonSchema([]string{"space", "agent", "status", "summary"}, map[string]map[string]any{
			"space":      prop("string", "The workspace name"),
			"agent":      prop("string", "Your agent name"),
			"status":     prop("string", "Your current status: active, done, blocked, idle, review, or error"),
			"summary":    prop("string", "One-line summary in format 'AgentName: what you are doing'"),
			"branch":     prop("string", "Current git branch (sticky — send once)"),
			"pr":         prop("string", "Pull request reference e.g. #123 (sticky)"),
			"repo_url":   prop("string", "Repository URL (sticky — send once)"),
			"phase":      prop("string", "Current work phase e.g. implementation, testing, review"),
			"test_count": prop("number", "Number of tests passing"),
			"items":      {"type": "array", "description": "List of completed or in-progress items", "items": map[string]any{"type": "string"}},
			"next_steps": prop("string", "What you plan to do next"),
			"session_id": prop("string", "Your tmux session ID (sticky — send once)"),
			"questions":  {"type": "array", "description": "Questions needing human decision — each creates a decision request visible to the operator", "items": map[string]any{"type": "string"}},
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		agentName := strArg(args, "agent")

		update := AgentUpdate{
			Status:    AgentStatus(strArg(args, "status")),
			Summary:   strArg(args, "summary"),
			Branch:    strArg(args, "branch"),
			PR:        strArg(args, "pr"),
			RepoURL:   strArg(args, "repo_url"),
			Phase:     strArg(args, "phase"),
			NextSteps: strArg(args, "next_steps"),
			SessionID: strArg(args, "session_id"),
			UpdatedAt: time.Now().UTC(),
		}
		if tc, ok := args["test_count"]; ok {
			if v, ok := tc.(float64); ok {
				iv := int(v)
				update.TestCount = &iv
			}
		}
		if items, ok := args["items"]; ok {
			if arr, ok := items.([]any); ok {
				for _, item := range arr {
					if str, ok := item.(string); ok {
						update.Items = append(update.Items, str)
					}
				}
			}
		}

		if questions, ok := args["questions"]; ok {
			if arr, ok := questions.([]any); ok {
				for _, q := range arr {
					if str, ok := q.(string); ok {
						update.Questions = append(update.Questions, str)
					}
				}
			}
		}

		sanitizeAgentUpdate(&update)
		if err := update.Validate(); err != nil {
			return toolError(fmt.Sprintf("validation: %v", err)), nil
		}

		if strings.EqualFold(agentName, "parent") {
			return toolError("\"parent\" is a reserved agent name"), nil
		}

		incomingParent := update.Parent
		incomingRole := update.Role
		update.Children = nil

		s.mu.Lock()
		ks := s.getOrCreateSpaceLocked(spaceName)
		canonical := resolveAgentName(ks, agentName)

		if incomingParent != "" {
			incomingParent = resolveAgentName(ks, incomingParent)
			update.Parent = incomingParent
		}
		if incomingParent != "" && hasCycle(ks, canonical, incomingParent) {
			s.mu.Unlock()
			return toolError("cycle detected: parent assignment would create a loop"), nil
		}

		parentChanged := false
		if existingRec, ok := ks.Agents[canonical]; ok {
			existing := existingRec.Status
			if update.SessionID == "" && existing.SessionID != "" {
				update.SessionID = existing.SessionID
			}
			if update.RepoURL == "" && existing.RepoURL != "" {
				update.RepoURL = existing.RepoURL
			}
			if len(update.Messages) == 0 && len(existing.Messages) > 0 {
				update.Messages = existing.Messages
			}
			if len(existing.Notifications) > 0 {
				for i := range existing.Notifications {
					existing.Notifications[i].Read = true
				}
				update.Notifications = existing.Notifications
				pruneNotifications(&update)
			}
			if len(update.Documents) == 0 && len(existing.Documents) > 0 {
				update.Documents = existing.Documents
			}
			if update.Registration == nil && existing.Registration != nil {
				update.Registration = existing.Registration
			}
			if update.LastHeartbeat.IsZero() && !existing.LastHeartbeat.IsZero() {
				update.LastHeartbeat = existing.LastHeartbeat
			}
			update.HeartbeatStale = existing.HeartbeatStale
			if incomingParent == "" && existing.Parent != "" {
				update.Parent = existing.Parent
			}
			if incomingRole == "" && existing.Role != "" {
				update.Role = existing.Role
			}
			parentChanged = update.Parent != existing.Parent
		} else {
			parentChanged = incomingParent != ""
		}
		ks.setAgentStatus(canonical, &update)
		ks.UpdatedAt = time.Now().UTC()
		if parentChanged {
			rebuildChildren(ks)
		}
		if update.PR != "" && ks.Tasks != nil {
			now := time.Now().UTC()
			for _, task := range ks.Tasks {
				if strings.EqualFold(task.AssignedTo, canonical) && task.LinkedPR == "" {
					task.LinkedPR = update.PR
					task.UpdatedAt = now
					appendTaskEvent(task, "updated", canonical,
						fmt.Sprintf("PR linked: %s", update.PR), now)
				}
			}
		}
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			return toolError(fmt.Sprintf("save: %v", err)), nil
		}
		s.mu.Unlock()

		s.logEvent(fmt.Sprintf("[%s/%s] %s: %s", spaceName, canonical, update.Status, update.Summary))
		s.journal.Append(spaceName, EventAgentUpdated, canonical, &update)
		s.maybeCompact(spaceName)
		s.recordDecisionInterrupts(spaceName, canonical, &update)
		snap := snapshotFromAgent(spaceName, canonical, &update)
		s.appendSnapshot(snap)
		sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical, "status": string(update.Status), "summary": update.Summary})
		s.broadcastSSE(spaceName, canonical, "agent_updated", string(sseData))

		return toolText(fmt.Sprintf("Status posted for %s in %s: %s", canonical, spaceName, update.Status)), nil
	})
}

// --- check_messages ---

func (s *Server) addToolCheckMessages(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "check_messages",
		Description: "Check for new messages. Call this at the start of every work cycle.",
		InputSchema: jsonSchema([]string{"space", "agent"}, map[string]map[string]any{
			"space": prop("string", "The workspace name"),
			"agent": prop("string", "Your agent name"),
			"since": prop("string", "Cursor from previous check (RFC3339 timestamp). Omit for first check."),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		agentName := strArg(args, "agent")
		sinceStr := strArg(args, "since")

		ks, ok := s.getSpace(spaceName)
		if !ok {
			return toolJSON(map[string]any{
				"agent": agentName, "messages": []any{}, "cursor": time.Now().UTC().Format(time.RFC3339Nano),
			}), nil
		}

		canonical := resolveAgentName(ks, agentName)

		var since time.Time
		if sinceStr != "" {
			since, err = time.Parse(time.RFC3339Nano, sinceStr)
			if err != nil {
				since, err = time.Parse(time.RFC3339, sinceStr)
				if err != nil {
					return toolError(fmt.Sprintf("invalid since timestamp %q: use RFC3339 format", sinceStr)), nil
				}
			}
		}

		s.mu.RLock()
		agent, exists := ks.agentStatusOk(canonical)
		var allMessages []AgentMessage
		if exists {
			allMessages = make([]AgentMessage, len(agent.Messages))
			copy(allMessages, agent.Messages)
		}
		s.mu.RUnlock()

		var filtered []AgentMessage
		for _, msg := range allMessages {
			if since.IsZero() || msg.Timestamp.After(since) {
				filtered = append(filtered, msg)
			}
		}
		if filtered == nil {
			filtered = []AgentMessage{}
		}

		var cursor time.Time
		if len(filtered) > 0 {
			cursor = filtered[len(filtered)-1].Timestamp.Add(time.Nanosecond)
		} else {
			cursor = time.Now().UTC()
		}

		return toolJSON(map[string]any{
			"agent":    canonical,
			"messages": filtered,
			"cursor":   cursor.Format(time.RFC3339Nano),
		}), nil
	})
}

// --- send_message ---

func (s *Server) addToolSendMessage(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "send_message",
		Description: "Send a message to another agent. Use this for coordination, delegation, and escalation.",
		InputSchema: jsonSchema([]string{"space", "from", "to", "message"}, map[string]map[string]any{
			"space":    prop("string", "The workspace name"),
			"from":     prop("string", "Your agent name (the sender)"),
			"to":       prop("string", "Target agent name, or 'parent' to message your parent agent"),
			"message":  prop("string", "The message content"),
			"priority": prop("string", "Message priority: info (default), directive, or urgent"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		senderName := strArg(args, "from")
		targetName := strArg(args, "to")
		messageText := strArg(args, "message")
		priority := strArg(args, "priority")

		if strings.TrimSpace(messageText) == "" {
			return toolError("message content is required"), nil
		}

		// Resolve "parent" target
		if strings.EqualFold(targetName, "parent") {
			ks, ok := s.getSpace(spaceName)
			if !ok {
				return toolError(fmt.Sprintf("space %q not found", spaceName)), nil
			}
			s.mu.RLock()
			senderCanonical := resolveAgentName(ks, senderName)
			sender, senderExists := ks.agentStatusOk(senderCanonical)
			s.mu.RUnlock()
			if !senderExists || sender.Parent == "" {
				return toolError("agent has no declared parent"), nil
			}
			targetName = sender.Parent
		}

		msgReq := AgentMessage{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Message:   strings.TrimSpace(messageText),
			Sender:    senderName,
			Timestamp: time.Now().UTC(),
		}

		switch MessagePriority(priority) {
		case PriorityInfo, PriorityDirective, PriorityUrgent:
			msgReq.Priority = MessagePriority(priority)
		case "":
			msgReq.Priority = PriorityInfo
		default:
			return toolError(fmt.Sprintf("invalid priority %q: must be info, directive, or urgent", priority)), nil
		}

		ks, ok := s.getSpace(spaceName)
		if !ok {
			ks = &KnowledgeSpace{
				Name:      spaceName,
				Agents:    make(map[string]*AgentRecord),
				UpdatedAt: time.Now().UTC(),
			}
			s.mu.Lock()
			s.spaces[spaceName] = ks
			s.mu.Unlock()
		}

		s.mu.Lock()
		canonical := resolveAgentName(ks, targetName)
		ag := ks.agentStatus(canonical)
		if ag == nil {
			ag = &AgentUpdate{
				Status:    StatusIdle,
				Summary:   fmt.Sprintf("%s: pending message delivery", canonical),
				Messages:  []AgentMessage{},
				UpdatedAt: time.Now().UTC(),
			}
			ks.setAgentStatus(canonical, ag)
		}
		if ag.Messages == nil {
			ag.Messages = []AgentMessage{}
		}
		ag.Messages = append(ag.Messages, msgReq)

		notif := AgentNotification{
			ID:        fmt.Sprintf("%s-%d", canonical, time.Now().UnixNano()),
			Type:      NotifTypeMessage,
			Title:     fmt.Sprintf("New message from %s", senderName),
			Body:      truncateLine(msgReq.Message, 120),
			From:      senderName,
			Timestamp: time.Now().UTC(),
		}
		ag.Notifications = append(ag.Notifications, notif)
		pruneNotifications(ag)
		pruneReadMessages(ag)

		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			return toolError(fmt.Sprintf("save: %v", err)), nil
		}
		s.mu.Unlock()

		s.emit(DomainEvent{Level: LevelInfo, EventType: EventMsgDelivered, Space: spaceName, Agent: canonical,
			Msg:    fmt.Sprintf("message from %s delivered", senderName),
			Fields: map[string]string{"sender": senderName, "priority": string(msgReq.Priority)}})
		s.journal.Append(spaceName, EventMessageSent, canonical, &msgReq)

		sseData, _ := json.Marshal(map[string]any{
			"space": spaceName, "agent": canonical, "sender": senderName,
			"message": msgReq.Message, "priority": string(msgReq.Priority),
		})
		go func() {
			s.broadcastSSE(spaceName, canonical, "agent_message", string(sseData))
			s.tryWebhookDelivery(spaceName, canonical, msgReq)
			s.nudgeMu.Lock()
			s.nudgePending[spaceName+"/"+canonical] = time.Now()
			s.nudgeMu.Unlock()
		}()

		return toolText(fmt.Sprintf("Message delivered to %s (id: %s)", canonical, msgReq.ID)), nil
	})
}

// --- ack_message ---

func (s *Server) addToolAckMessage(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "ack_message",
		Description: "Acknowledge a message you have acted on. This marks it as read.",
		InputSchema: jsonSchema([]string{"space", "agent", "message_id"}, map[string]map[string]any{
			"space":      prop("string", "The workspace name"),
			"agent":      prop("string", "Your agent name"),
			"message_id": prop("string", "The message ID to acknowledge"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		agentName := strArg(args, "agent")
		msgID := strArg(args, "message_id")

		ks, ok := s.getSpace(spaceName)
		if !ok {
			return toolError(fmt.Sprintf("space %q not found", spaceName)), nil
		}

		now := time.Now().UTC()
		s.mu.Lock()
		canonical := resolveAgentName(ks, agentName)
		agent, exists := ks.agentStatusOk(canonical)
		if !exists {
			s.mu.Unlock()
			return toolError(fmt.Sprintf("agent %q not found", canonical)), nil
		}

		found := false
		for i := range agent.Messages {
			if agent.Messages[i].ID == msgID {
				agent.Messages[i].Read = true
				agent.Messages[i].ReadAt = &now
				found = true
				break
			}
		}
		if !found {
			s.mu.Unlock()
			return toolError(fmt.Sprintf("message %q not found", msgID)), nil
		}

		ks.UpdatedAt = now
		s.journal.Append(spaceName, EventMessageAcked, canonical, map[string]any{
			"message_id": msgID, "acked_at": now,
		})
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			return toolError(fmt.Sprintf("save: %v", err)), nil
		}
		s.mu.Unlock()

		return toolText(fmt.Sprintf("Message %s acknowledged", msgID)), nil
	})
}

// --- request_decision ---

func (s *Server) addToolRequestDecision(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "request_decision",
		Description: "Request a decision from the human operator. Use this when you need human input to proceed. The operator will see your question in the conversations view and can reply.",
		InputSchema: jsonSchema([]string{"space", "agent", "question"}, map[string]map[string]any{
			"space":    prop("string", "The workspace name"),
			"agent":    prop("string", "Your agent name"),
			"question": prop("string", "The question or decision you need from the operator"),
			"context":  prop("string", "Optional context to help the operator understand the situation"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		agentName := strArg(args, "agent")
		question := strArg(args, "question")
		extraContext := strArg(args, "context")

		if strings.TrimSpace(question) == "" {
			return toolError("question is required"), nil
		}

		messageText := question
		if extraContext != "" {
			messageText = question + "\n\nContext: " + extraContext
		}

		msg := s.deliverDecisionMessage(spaceName, agentName, messageText)
		return toolText(fmt.Sprintf("Decision request sent (id: %s). The operator will reply via the conversations view. Continue working on other tasks while waiting.", msg.ID)), nil
	})
}

// --- create_task ---

func (s *Server) addToolCreateTask(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "create_task",
		Description: "Create a new task. Always create a task BEFORE starting work on it.",
		InputSchema: jsonSchema([]string{"space", "agent", "title"}, map[string]map[string]any{
			"space":       prop("string", "The workspace name"),
			"agent":       prop("string", "Your agent name (the creator)"),
			"title":       prop("string", "Task title"),
			"description": prop("string", "Detailed task description"),
			"assigned_to": prop("string", "Agent to assign the task to"),
			"priority":    prop("string", "Priority: low, medium, high, critical"),
			"labels":      {"type": "array", "description": "Labels for categorization", "items": map[string]any{"type": "string"}},
			"parent_task": prop("string", "Parent task ID for subtasks e.g. TASK-001"),
			"status":      prop("string", "Initial status (default: backlog)"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		caller := strArg(args, "agent")
		title := strArg(args, "title")

		if strings.TrimSpace(title) == "" {
			return toolError("title is required"), nil
		}

		s.mu.Lock()
		ks := s.getOrCreateSpaceLocked(spaceName)
		ks.NextTaskSeq++
		id := fmt.Sprintf("TASK-%03d", ks.NextTaskSeq)
		now := time.Now().UTC()

		initialStatus := TaskStatusBacklog
		if statusStr := strArg(args, "status"); statusStr != "" {
			st := TaskStatus(statusStr)
			if st.Valid() {
				initialStatus = st
			}
		}

		task := &Task{
			ID:          id,
			Space:       spaceName,
			Title:       strings.TrimSpace(title),
			Description: strArg(args, "description"),
			Status:      initialStatus,
			Priority:    TaskPriority(strArg(args, "priority")),
			AssignedTo:  strArg(args, "assigned_to"),
			CreatedBy:   caller,
			ParentTask:  strArg(args, "parent_task"),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if labels, ok := args["labels"]; ok {
			if arr, ok := labels.([]any); ok {
				for _, l := range arr {
					if str, ok := l.(string); ok {
						task.Labels = append(task.Labels, str)
					}
				}
			}
		}
		appendTaskEvent(task, "created", caller, fmt.Sprintf("Task created by %s", caller), now)
		if ks.Tasks == nil {
			ks.Tasks = make(map[string]*Task)
		}
		ks.Tasks[id] = task
		if task.ParentTask != "" {
			if parent, ok := ks.Tasks[task.ParentTask]; ok {
				parent.Subtasks = append(parent.Subtasks, id)
				parent.UpdatedAt = now
			}
		}
		ks.UpdatedAt = now
		taskCopy := *task
		snap := ks.snapshot()
		s.mu.Unlock()

		s.journal.Append(spaceName, EventTaskCreated, "", taskCopy)
		s.saveSpace(snap)

		if sseData, err := json.Marshal(map[string]any{
			"id": taskCopy.ID, "space": spaceName, "status": taskCopy.Status,
			"title": taskCopy.Title, "assigned_to": taskCopy.AssignedTo,
		}); err == nil {
			s.broadcastSSE(spaceName, "", "task_updated", string(sseData))
		}
		if taskCopy.AssignedTo != "" {
			s.notifyTaskAssigned(spaceName, taskCopy.ID, taskCopy.Title, taskCopy.AssignedTo, caller)
		}

		return toolJSON(taskCopy), nil
	})
}

// --- list_tasks ---

func (s *Server) addToolListTasks(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "list_tasks",
		Description: "List tasks in a space, optionally filtered by status, assignee, priority, or label.",
		InputSchema: jsonSchema([]string{"space"}, map[string]map[string]any{
			"space":       prop("string", "The workspace name"),
			"status":      prop("string", "Filter by status: backlog, in_progress, review, done, blocked"),
			"assigned_to": prop("string", "Filter by assigned agent name"),
			"priority":    prop("string", "Filter by priority: low, medium, high, critical"),
			"label":       prop("string", "Filter by label"),
			"search":      prop("string", "Search in title and task ID"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")

		ks, ok := s.getSpace(spaceName)
		if !ok {
			return toolJSON(map[string]any{"tasks": []any{}, "total": 0}), nil
		}

		filterStatus := strArg(args, "status")
		filterAssigned := strArg(args, "assigned_to")
		filterPriority := strArg(args, "priority")
		filterLabel := strArg(args, "label")
		filterSearch := strings.ToLower(strArg(args, "search"))

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
			if filterSearch != "" {
				titleMatch := strings.Contains(strings.ToLower(t.Title), filterSearch)
				idMatch := strings.EqualFold(t.ID, filterSearch)
				if !titleMatch && !idMatch {
					continue
				}
			}
			cp := *t
			computeTaskStaleness(&cp)
			tasks = append(tasks, &cp)
		}
		s.mu.RUnlock()

		sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })

		return toolJSON(map[string]any{"tasks": tasks, "total": len(tasks)}), nil
	})
}

// --- move_task ---

func (s *Server) addToolMoveTask(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "move_task",
		Description: "Change a task's status. Use this to move tasks through the workflow: backlog -> in_progress -> review -> done.",
		InputSchema: jsonSchema([]string{"space", "agent", "task_id", "status"}, map[string]map[string]any{
			"space":   prop("string", "The workspace name"),
			"agent":   prop("string", "Your agent name"),
			"task_id": prop("string", "The task ID e.g. TASK-001"),
			"status":  prop("string", "New status: backlog, in_progress, review, done, blocked"),
			"reason":  prop("string", "Reason for the status change"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		caller := strArg(args, "agent")
		taskID := strArg(args, "task_id")
		newStatus := TaskStatus(strArg(args, "status"))
		reason := strArg(args, "reason")

		if !newStatus.Valid() {
			return toolError(fmt.Sprintf("invalid status %q", newStatus)), nil
		}

		ks, ok := s.getSpace(spaceName)
		if !ok {
			return toolError("space not found"), nil
		}

		s.mu.Lock()
		task, exists := ks.Tasks[taskID]
		if !exists {
			s.mu.Unlock()
			return toolError(fmt.Sprintf("task %q not found", taskID)), nil
		}
		fromStatus := task.Status
		task.Status = newStatus
		now := time.Now().UTC()
		task.UpdatedAt = now
		moveDetail := fmt.Sprintf("Moved from %s to %s by %s", fromStatus, newStatus, caller)
		if reason != "" {
			moveDetail += ": " + reason
		}
		appendTaskEvent(task, "moved", caller, moveDetail, now)
		taskCopy := *task
		snap := ks.snapshot()
		s.mu.Unlock()

		s.journal.Append(spaceName, EventTaskMoved, "", map[string]string{
			"id": taskID, "from_status": string(fromStatus), "status": string(newStatus), "by": caller,
		})
		s.saveSpace(snap)

		if sseData, err := json.Marshal(map[string]any{
			"id": taskID, "space": spaceName, "status": taskCopy.Status, "assigned_to": taskCopy.AssignedTo,
		}); err == nil {
			s.broadcastSSE(spaceName, "", "task_updated", string(sseData))
		}

		return toolText(fmt.Sprintf("Task %s moved from %s to %s", taskID, fromStatus, newStatus)), nil
	})
}

// --- update_task ---

func (s *Server) addToolUpdateTask(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "update_task",
		Description: "Update task fields like title, description, assignee, priority, or linked PR.",
		InputSchema: jsonSchema([]string{"space", "agent", "task_id"}, map[string]map[string]any{
			"space":         prop("string", "The workspace name"),
			"agent":         prop("string", "Your agent name"),
			"task_id":       prop("string", "The task ID e.g. TASK-001"),
			"title":         prop("string", "New title"),
			"description":   prop("string", "New description"),
			"assigned_to":   prop("string", "New assignee"),
			"priority":      prop("string", "New priority: low, medium, high, critical"),
			"linked_pr":     prop("string", "Link a PR e.g. #123"),
			"linked_branch": prop("string", "Link a branch"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		caller := strArg(args, "agent")
		taskID := strArg(args, "task_id")

		ks, ok := s.getSpace(spaceName)
		if !ok {
			return toolError("space not found"), nil
		}

		s.mu.Lock()
		task, exists := ks.Tasks[taskID]
		if !exists {
			s.mu.Unlock()
			return toolError(fmt.Sprintf("task %q not found", taskID)), nil
		}

		now := time.Now().UTC()
		prevAssignee := task.AssignedTo

		if v := strArg(args, "title"); v != "" {
			task.Title = strings.TrimSpace(v)
		}
		if v, ok := args["description"]; ok {
			if str, ok := v.(string); ok {
				task.Description = str
			}
		}
		if v := strArg(args, "assigned_to"); v != "" {
			task.AssignedTo = v
		}
		if v := strArg(args, "priority"); v != "" {
			task.Priority = TaskPriority(v)
		}
		if v := strArg(args, "linked_pr"); v != "" {
			task.LinkedPR = v
		}
		if v := strArg(args, "linked_branch"); v != "" {
			task.LinkedBranch = v
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
		if taskCopy.AssignedTo != "" && !strings.EqualFold(taskCopy.AssignedTo, prevAssignee) {
			s.notifyTaskAssigned(spaceName, taskCopy.ID, taskCopy.Title, taskCopy.AssignedTo, caller)
		}

		return toolJSON(taskCopy), nil
	})
}

// --- helpers ---

func strArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}

func toolJSON(v any) *mcp.CallToolResult {
	data, _ := json.MarshalIndent(v, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}
}

// deliverDecisionMessage creates a decision-type message from an agent to "boss".
// This message appears in the conversations view as a rich message with a reply action.
func (s *Server) deliverDecisionMessage(spaceName, agentName, question string) AgentMessage {
	msg := AgentMessage{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Message:   question,
		Sender:    agentName,
		Type:      MessageTypeDecision,
		Priority:  PriorityUrgent,
		Timestamp: time.Now().UTC(),
	}

	s.mu.Lock()
	ks := s.getOrCreateSpaceLocked(spaceName)
	// Deliver to "boss" agent's inbox so it appears in boss conversations.
	boss := ks.agentStatus("boss")
	if boss == nil {
		boss = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   "boss: operator",
			Messages:  []AgentMessage{},
			UpdatedAt: time.Now().UTC(),
		}
		ks.setAgentStatus("boss", boss)
	}
	if boss.Messages == nil {
		boss.Messages = []AgentMessage{}
	}
	boss.Messages = append(boss.Messages, msg)

	notif := AgentNotification{
		ID:        fmt.Sprintf("boss-%d", time.Now().UnixNano()),
		Type:      NotifTypeMessage,
		Title:     fmt.Sprintf("Decision needed from %s", agentName),
		Body:      truncateLine(question, 120),
		From:      agentName,
		Timestamp: time.Now().UTC(),
	}
	boss.Notifications = append(boss.Notifications, notif)
	pruneNotifications(boss)

	ks.UpdatedAt = time.Now().UTC()
	s.saveSpace(ks)
	s.mu.Unlock()

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventMsgDelivered, Space: spaceName, Agent: "boss",
		Msg:    fmt.Sprintf("decision request from %s", agentName),
		Fields: map[string]string{"sender": agentName, "type": "decision"}})
	s.journal.Append(spaceName, EventMessageSent, "boss", &msg)

	sseData, _ := json.Marshal(map[string]any{
		"space": spaceName, "agent": "boss", "sender": agentName,
		"message": question, "type": "decision",
	})
	s.broadcastSSE(spaceName, "boss", "agent_message", string(sseData))

	return msg
}

// pruneReadMessages caps read messages at 50, keeping all unread.
func pruneReadMessages(ag *AgentUpdate) {
	const maxReadMessages = 50
	readCount := 0
	for _, m := range ag.Messages {
		if m.Read {
			readCount++
		}
	}
	if readCount > maxReadMessages {
		toSkip := readCount - maxReadMessages
		skipped := 0
		filtered := make([]AgentMessage, 0, len(ag.Messages))
		for _, m := range ag.Messages {
			if m.Read && skipped < toSkip {
				skipped++
				continue
			}
			filtered = append(filtered, m)
		}
		ag.Messages = filtered
	}
}

// --- spawn_agent ---

func (s *Server) addToolSpawnAgent(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "spawn_agent",
		Description: "Spawn a new agent session in a space. Creates a tmux session, sends the ignition prompt, and returns the session ID.",
		InputSchema: jsonSchema([]string{"space", "name"}, map[string]map[string]any{
			"space":           prop("string", "The workspace name"),
			"name":            prop("string", "The agent name to spawn"),
			"command":         prop("string", "Command to run (default: claude or claude --dangerously-skip-permissions based on server setting)"),
			"work_dir":        prop("string", "Working directory for the session"),
			"initial_message": prop("string", "First message to deliver to the agent after spawn"),
			"parent":          prop("string", "Parent agent name (sets hierarchy relationship)"),
			"task_id":         prop("string", "Task ID to link to this agent (sets assigned_to)"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		agentName := strArg(args, "name")
		if strings.TrimSpace(agentName) == "" {
			return toolError("name is required"), nil
		}

		spawnReq := spawnRequest{
			Command:        strArg(args, "command"),
			InitialMessage: strArg(args, "initial_message"),
			TaskID:         strArg(args, "task_id"),
		}

		// work_dir is applied via AgentConfig; for MCP callers we inject it by
		// temporarily setting a config entry if not already present.
		workDir := strArg(args, "work_dir")
		spawnerName := strArg(args, "parent")

		if workDir != "" {
			// Ensure an AgentConfig entry exists so spawnAgentService picks up the workDir.
			s.mu.Lock()
			ks := s.getOrCreateSpaceLocked(spaceName)
			canonical := resolveAgentName(ks, agentName)
			rec, ok := ks.Agents[canonical]
			if !ok || rec == nil {
				rec = &AgentRecord{}
				ks.Agents[canonical] = rec
			}
			if rec.Config == nil {
				rec.Config = &AgentConfig{}
			}
			if rec.Config.WorkDir == "" {
				rec.Config.WorkDir = workDir
			}
			s.mu.Unlock()
		}

		sessionID, backendName, canonical, err := s.spawnAgentService(spaceName, agentName, spawnReq, spawnerName)
		if err != nil {
			return toolError(err.Error()), nil
		}

		return toolJSON(map[string]any{
			"ok":         true,
			"agent_name": canonical,
			"session_id": sessionID,
			"space":      spaceName,
			"backend":    backendName,
		}), nil
	})
}

// --- restart_agent ---

func (s *Server) addToolRestartAgent(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "restart_agent",
		Description: "Restart an existing agent: kills the current session and spawns a new one with the same config.",
		InputSchema: jsonSchema([]string{"space", "name"}, map[string]map[string]any{
			"space": prop("string", "The workspace name"),
			"name":  prop("string", "The agent name to restart"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		agentName := strArg(args, "name")
		if strings.TrimSpace(agentName) == "" {
			return toolError("name is required"), nil
		}

		sessionID, canonical, err := s.restartAgentService(spaceName, agentName, spawnRequest{})
		if err != nil {
			return toolError(err.Error()), nil
		}

		return toolJSON(map[string]any{
			"ok":         true,
			"agent_name": canonical,
			"session_id": sessionID,
			"space":      spaceName,
		}), nil
	})
}

// --- stop_agent ---

func (s *Server) addToolStopAgent(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "stop_agent",
		Description: "Stop an agent by killing its session and marking it as done.",
		InputSchema: jsonSchema([]string{"space", "name"}, map[string]map[string]any{
			"space": prop("string", "The workspace name"),
			"name":  prop("string", "The agent name to stop"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		agentName := strArg(args, "name")
		if strings.TrimSpace(agentName) == "" {
			return toolError("name is required"), nil
		}

		canonical, err := s.stopAgentService(spaceName, agentName)
		if err != nil {
			return toolError(err.Error()), nil
		}

		return toolJSON(map[string]any{
			"ok":         true,
			"agent_name": canonical,
			"space":      spaceName,
		}), nil
	})
}
