package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func (s *Server) handleSpaceAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if agentName == "" {
		writeJSONError(w, "missing agent name", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		s.mu.RLock()
		canonical := resolveAgentName(ks, agentName)
		agent, exists := ks.Agents[canonical]
		s.mu.RUnlock()
		if !exists {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "{}")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agent)

	case http.MethodPost:
		callerName := r.Header.Get("X-Agent-Name")
		if callerName == "" {
			writeJSONError(w, "missing X-Agent-Name header: agents must identify themselves", http.StatusBadRequest)
			return
		}
		if !strings.EqualFold(callerName, agentName) {
			writeJSONError(w, fmt.Sprintf("agent %q cannot post to %q's channel", callerName, agentName), http.StatusForbidden)
			return
		}

		contentType := r.Header.Get("Content-Type")
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodySize))
		if err != nil {
			writeJSONError(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}

		var update AgentUpdate

		if strings.Contains(contentType, "application/json") {
			if err := json.Unmarshal(body, &update); err != nil {
				writeJSONError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
				return
			}
		} else {
			update = AgentUpdate{
				Status:   StatusActive,
				Summary:  truncateLine(string(body), 120),
				FreeText: string(body),
			}
		}

		sanitizeAgentUpdate(&update)

		if err := update.Validate(); err != nil {
			writeJSONError(w, fmt.Sprintf("validation: %v", err), http.StatusBadRequest)
			return
		}

		update.UpdatedAt = time.Now().UTC()

		// "parent" is a reserved agent name used for parent escalation in message routing.
		if strings.EqualFold(agentName, "parent") {
			writeJSONError(w, `"parent" is a reserved agent name`, http.StatusBadRequest)
			return
		}

		// Children is server-managed — zero any agent-supplied value before processing.
		incomingParent := update.Parent
		incomingRole := update.Role
		update.Children = nil

		s.mu.Lock()
		ks := s.getOrCreateSpaceLocked(spaceName)
		canonical := resolveAgentName(ks, agentName)

		// Canonicalize parent name under lock so resolveAgentName sees current agents.
		if incomingParent != "" {
			incomingParent = resolveAgentName(ks, incomingParent)
			update.Parent = incomingParent
		}

		// Cycle detection: must be atomic with the write inside this lock.
		if incomingParent != "" && hasCycle(ks, canonical, incomingParent) {
			s.mu.Unlock()
			writeJSONError(w, "cycle detected: parent assignment would create a loop", http.StatusBadRequest)
			return
		}

		parentChanged := false
		if existing, ok := ks.Agents[canonical]; ok {
			if update.SessionID == "" && existing.SessionID != "" {
				update.SessionID = existing.SessionID
			}
			if update.RepoURL == "" && existing.RepoURL != "" {
				update.RepoURL = existing.RepoURL
			}
			// Preserve messages — agents don't include them in updates
			if len(update.Messages) == 0 && len(existing.Messages) > 0 {
				update.Messages = existing.Messages
			}
			// Preserve and mark-read notifications — agent posting means it has checked in.
			if len(existing.Notifications) > 0 {
				for i := range existing.Notifications {
					existing.Notifications[i].Read = true
				}
				update.Notifications = existing.Notifications
				pruneNotifications(&update)
			}
			// Preserve documents — managed via the /agent/{name}/{slug} endpoint
			if len(update.Documents) == 0 && len(existing.Documents) > 0 {
				update.Documents = existing.Documents
			}
			// Preserve protocol registration fields (set via /register and /heartbeat)
			if update.Registration == nil && existing.Registration != nil {
				update.Registration = existing.Registration
			}
			if update.LastHeartbeat.IsZero() && !existing.LastHeartbeat.IsZero() {
				update.LastHeartbeat = existing.LastHeartbeat
			}
			update.HeartbeatStale = existing.HeartbeatStale
			// Sticky hierarchy fields: only update if incoming POST includes them.
			// An omitted parent/role does not clear the existing value.
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
		ks.Agents[canonical] = &update
		ks.UpdatedAt = time.Now().UTC()
		// Rebuild children whenever the parent relationship may have changed.
		if parentChanged {
			rebuildChildren(ks)
		}
		// Auto-link PR: when an agent posts with a pr field, set linked_pr on all
		// tasks assigned to that agent that don't already have a linked_pr.
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
			writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()

		s.logEvent(fmt.Sprintf("[%s/%s] %s: %s", spaceName, canonical, update.Status, update.Summary))
		s.journal.Append(spaceName, EventAgentUpdated, canonical, &update)
		s.maybeCompact(spaceName)
		s.recordDecisionInterrupts(spaceName, canonical, &update)
		snap := snapshotFromAgent(spaceName, canonical, &update)
		if err := s.appendSnapshot(snap); err != nil {
			s.logEvent(fmt.Sprintf("[%s/%s] warning: failed to append snapshot: %v", spaceName, canonical, err))
		}
		sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical, "status": string(update.Status), "summary": update.Summary})
		s.broadcastSSE(spaceName, canonical, "agent_updated", string(sseData))
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "accepted for [%s] in space %q", canonical, spaceName)

	case http.MethodDelete:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		s.mu.Lock()
		canonical := resolveAgentName(ks, agentName)
		// Capture session info before removing the agent so we can cascade-delete.
		var sessionID, backendType string
		if agent, exists := ks.Agents[canonical]; exists && agent != nil {
			sessionID = agent.SessionID
			backendType = agent.BackendType
		}
		delete(ks.Agents, canonical)
		rebuildChildren(ks) // keep children lists consistent after removal
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.deleteAgentFromDB(spaceName, canonical)

		// Cascade: kill the backing session if one exists.
		if sessionID != "" {
			backend := s.backendByName(backendType)
			if backend != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := backend.KillSession(ctx, sessionID); err != nil {
					s.logEvent(fmt.Sprintf("[%s/%s] warning: cascade delete session %s: %v", spaceName, canonical, sessionID, err))
				} else {
					s.logEvent(fmt.Sprintf("[%s/%s] cascade-deleted session %s (%s)", spaceName, canonical, sessionID, backend.Name()))
				}
				cancel()
			}
		}

		s.logEvent(fmt.Sprintf("[%s/%s] agent removed", spaceName, canonical))
		s.journal.Append(spaceName, EventAgentRemoved, canonical, nil)
		sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
		s.broadcastSSE(spaceName, canonical, "agent_removed", string(sseData))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "removed [%s] from space %q", canonical, spaceName)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentMessage(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentName = strings.TrimRight(agentName, "/")

	// Sender authentication - require X-Agent-Name header
	senderName := r.Header.Get("X-Agent-Name")
	if senderName == "" {
		writeJSONError(w, "missing X-Agent-Name header: sender must identify themselves", http.StatusBadRequest)
		return
	}

	// "parent" is a reserved target: resolve to the sender's actual parent agent.
	// This check must precede resolveAgentName to avoid collision with an agent literally named "parent".
	if strings.EqualFold(agentName, "parent") {
		ks, ok := s.getSpace(spaceName)
		if !ok {
			writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		s.mu.RLock()
		senderCanonical := resolveAgentName(ks, senderName)
		sender, senderExists := ks.Agents[senderCanonical]
		s.mu.RUnlock()
		if !senderExists || sender.Parent == "" {
			writeJSONError(w, "agent has no declared parent", http.StatusBadRequest)
			return
		}
		agentName = sender.Parent
	}

	var messageReq AgentMessage
	if err := json.NewDecoder(r.Body).Decode(&messageReq); err != nil {
		writeJSONError(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if strings.TrimSpace(messageReq.Message) == "" {
		writeJSONError(w, "message content is required", http.StatusBadRequest)
		return
	}

	// Sanitize and set message properties
	messageReq.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	messageReq.Message = strings.TrimSpace(messageReq.Message)
	messageReq.Sender = senderName
	messageReq.Timestamp = time.Now().UTC()

	// Validate and default priority
	switch messageReq.Priority {
	case PriorityInfo, PriorityDirective, PriorityUrgent:
		// valid
	case "":
		messageReq.Priority = PriorityInfo
	default:
		writeJSONError(w, fmt.Sprintf("invalid priority %q: must be info, directive, or urgent", messageReq.Priority), http.StatusBadRequest)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		// Create space if it doesn't exist for messages
		ks = &KnowledgeSpace{
			Name:      spaceName,
			Agents:    make(map[string]*AgentUpdate),
			UpdatedAt: time.Now().UTC(),
		}
		s.mu.Lock()
		s.spaces[spaceName] = ks
		s.mu.Unlock()
	}

	// Determine recipients based on scope query parameter.
	// scope=subtree: named agent + all descendants (capped at 50, async delivery, 202 response).
	// scope=direct (default): named agent only.
	scope := r.URL.Query().Get("scope")
	const subtreeCap = 50

	s.mu.Lock()
	canonical := resolveAgentName(ks, agentName)

	var recipients []string
	if scope == "subtree" {
		recipients = collectSubtree(ks, canonical)
		if len(recipients) > subtreeCap {
			s.logEvent(fmt.Sprintf("[%s/%s] subtree fan-out capped at %d recipients", spaceName, canonical, subtreeCap))
			recipients = recipients[:subtreeCap]
		}
	} else {
		recipients = []string{canonical}
	}

	// Deliver message to all recipients in one critical section, one save.
	for _, r := range recipients {
		ag, exists := ks.Agents[r]
		if !exists {
			ag = &AgentUpdate{
				Status:    StatusIdle,
				Summary:   fmt.Sprintf("%s: pending message delivery", r),
				Messages:  []AgentMessage{},
				UpdatedAt: time.Now().UTC(),
			}
			ks.Agents[r] = ag
		}
		if ag.Messages == nil {
			ag.Messages = []AgentMessage{}
		}
		ag.Messages = append(ag.Messages, messageReq)

		// Create a typed notification so the agent immediately sees why it was woken up.
		notif := AgentNotification{
			ID:        fmt.Sprintf("%s-%d", r, time.Now().UnixNano()),
			Type:      NotifTypeMessage,
			Title:     fmt.Sprintf("New message from %s", senderName),
			Body:      truncateLine(messageReq.Message, 120),
			From:      senderName,
			Timestamp: time.Now().UTC(),
		}
		ag.Notifications = append(ag.Notifications, notif)
		pruneNotifications(ag)

		// Retain all unread messages; cap read messages at 50.
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

	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	// Log and broadcast SSE outside the lock (sseMu is distinct from s.mu — no deadlock).
	// For subtree fan-out: fire-and-forget per recipient (async, 202 response).
	for _, recipient := range recipients {
		s.emit(DomainEvent{Level: LevelInfo, EventType: EventMsgDelivered, Space: spaceName, Agent: recipient,
			Msg:    fmt.Sprintf("message from %s delivered", senderName),
			Fields: map[string]string{"sender": senderName, "priority": string(messageReq.Priority)}})
		s.journal.Append(spaceName, EventMessageSent, recipient, &messageReq)
		sseData, _ := json.Marshal(map[string]interface{}{
			"space":    spaceName,
			"agent":    recipient,
			"sender":   senderName,
			"message":  messageReq.Message,
			"priority": string(messageReq.Priority),
		})
		notifSSEData, _ := json.Marshal(map[string]any{
			"space":  spaceName,
			"agent":  recipient,
			"type":   string(NotifTypeMessage),
			"title":  fmt.Sprintf("New message from %s", senderName),
			"sender": senderName,
		})
		go func(r string, data string, notifData string) {
			s.broadcastSSE(spaceName, r, "agent_message", data)
			s.broadcastSSE(spaceName, r, "agent_notification", notifData)
			s.tryWebhookDelivery(spaceName, r, messageReq)
			s.nudgeMu.Lock()
			s.nudgePending[spaceName+"/"+r] = time.Now()
			s.nudgeMu.Unlock()
		}(recipient, string(sseData), string(notifSSEData))
	}

	w.Header().Set("Content-Type", "application/json")
	if scope == "subtree" {
		w.WriteHeader(http.StatusAccepted) // 202 — async fan-out
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "accepted",
			"messageId":  messageReq.ID,
			"recipients": recipients,
		})
	} else {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "delivered",
			"messageId": messageReq.ID,
			"recipient": canonical,
		})
	}
}

func (s *Server) handleAgentDocument(w http.ResponseWriter, r *http.Request, spaceName, agentName, documentSlug string) {
	agentName = strings.TrimRight(agentName, "/")

	// Agent name enforcement - ensure X-Agent-Name header matches for writes
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		callerName := r.Header.Get("X-Agent-Name")
		if callerName == "" {
			writeJSONError(w, "missing X-Agent-Name header: agents must identify themselves", http.StatusBadRequest)
			return
		}
		if !strings.EqualFold(callerName, agentName) {
			writeJSONError(w, fmt.Sprintf("agent %q cannot post to %q's documents", callerName, agentName), http.StatusForbidden)
			return
		}
	}

	// Sanitize document slug
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(documentSlug) {
		writeJSONError(w, "invalid document slug: must be alphanumeric with underscores and dashes only", http.StatusBadRequest)
		return
	}

	// Create agent document directory
	agentDir := filepath.Join(s.dataDir, spaceName, agentName)
	docPath := filepath.Join(agentDir, documentSlug+".md")

	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(docPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSONError(w, "document not found", http.StatusNotFound)
				return
			}
			writeJSONError(w, fmt.Sprintf("read document: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		w.Write(content)

	case http.MethodPost, http.MethodPut:
		contentType := r.Header.Get("Content-Type")
		if !strings.Contains(contentType, "text/markdown") && !strings.Contains(contentType, "text/plain") {
			writeJSONError(w, "Content-Type must be text/markdown or text/plain", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()
		content, err := io.ReadAll(io.LimitReader(r.Body, MaxBodySize))
		if err != nil {
			writeJSONError(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}

		// Create agent directory if it doesn't exist
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			writeJSONError(w, fmt.Sprintf("create directory: %v", err), http.StatusInternalServerError)
			return
		}

		// Write document
		if err := os.WriteFile(docPath, content, 0644); err != nil {
			writeJSONError(w, fmt.Sprintf("write document: %v", err), http.StatusInternalServerError)
			return
		}

		// Update agent's documents list in the knowledge space
		s.mu.Lock()
		ks := s.getOrCreateSpaceLocked(spaceName)
		canonical := resolveAgentName(ks, agentName)
		if ks.Agents[canonical] == nil {
			ks.Agents[canonical] = &AgentUpdate{
				Status:    StatusActive,
				Summary:   "Document uploaded",
				UpdatedAt: time.Now().UTC(),
			}
		}

		agent := ks.Agents[canonical]

		// Add or update document in the list
		found := false
		for i, doc := range agent.Documents {
			if doc.Slug == documentSlug {
				agent.Documents[i].Content = string(content)
				found = true
				break
			}
		}
		if !found {
			agent.Documents = append(agent.Documents, AgentDocument{
				Slug:    documentSlug,
				Title:   documentSlug, // Default title is the slug, agents can override via JSON
				Content: string(content),
			})
		}

		agent.UpdatedAt = time.Now().UTC()
		ks.UpdatedAt = time.Now().UTC()

		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			writeJSONError(w, fmt.Sprintf("save space: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()

		s.logEvent(fmt.Sprintf("[%s/%s] document %q uploaded", spaceName, canonical, documentSlug))
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "document %q saved for [%s] in space %q", documentSlug, canonical, spaceName)

	case http.MethodDelete:
		if err := os.Remove(docPath); err != nil {
			if os.IsNotExist(err) {
				writeJSONError(w, "document not found", http.StatusNotFound)
				return
			}
			writeJSONError(w, fmt.Sprintf("delete document: %v", err), http.StatusInternalServerError)
			return
		}

		// Remove document from agent's list
		ks, ok := s.getSpace(spaceName)
		if ok {
			s.mu.Lock()
			canonical := resolveAgentName(ks, agentName)
			if agent := ks.Agents[canonical]; agent != nil {
				for i, doc := range agent.Documents {
					if doc.Slug == documentSlug {
						agent.Documents = append(agent.Documents[:i], agent.Documents[i+1:]...)
						break
					}
				}
				agent.UpdatedAt = time.Now().UTC()
				ks.UpdatedAt = time.Now().UTC()
				s.saveSpace(ks)
			}
			s.mu.Unlock()
		}

		s.logEvent(fmt.Sprintf("[%s/%s] document %q deleted", spaceName, agentName, documentSlug))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "document %q deleted", documentSlug)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleIgnition(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if agentName == "" {
		writeJSONError(w, "missing agent name: GET /spaces/{space}/ignition/{agent}", http.StatusBadRequest)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	// ## TODO - REMOVE ME — backward compat for agents still using ?tmux_session= ## TODO
	if sessionID == "" {
		sessionID = r.URL.Query().Get("tmux_session")
	}
	parentParam := r.URL.Query().Get("parent")
	roleParam := r.URL.Query().Get("role")

	if sessionID != "" || parentParam != "" || roleParam != "" {
		s.mu.Lock()
		ks := s.getOrCreateSpaceLocked(spaceName)
		canonical := resolveAgentName(ks, agentName)

		// Validate parent param before making any changes.
		if parentParam != "" {
			if strings.EqualFold(parentParam, agentName) || strings.EqualFold(parentParam, canonical) {
				s.mu.Unlock()
				writeJSONError(w, "self-parent not allowed", http.StatusBadRequest)
				return
			}
			if hasCycle(ks, canonical, parentParam) {
				s.mu.Unlock()
				writeJSONError(w, "parent would create a cycle", http.StatusBadRequest)
				return
			}
		}

		var agentRecord *AgentUpdate
		if existing, ok := ks.Agents[canonical]; ok {
			agentRecord = existing
		} else {
			agentRecord = &AgentUpdate{
				Status:    StatusIdle,
				Summary:   canonical + ": awaiting ignition",
				UpdatedAt: time.Now().UTC(),
			}
			ks.Agents[canonical] = agentRecord
		}
		if sessionID != "" {
			agentRecord.SessionID = sessionID
		}
		// Set Parent and Role only if not already set (sticky).
		if parentParam != "" && agentRecord.Parent == "" {
			agentRecord.Parent = resolveAgentName(ks, parentParam)
			rebuildChildren(ks)
		}
		if roleParam != "" && agentRecord.Role == "" {
			agentRecord.Role = roleParam
		}
		ks.UpdatedAt = time.Now().UTC()
		s.saveSpace(ks)
		s.mu.Unlock()
		if sessionID != "" {
			s.logEvent(fmt.Sprintf("[%s/%s] session registered via ignition: %s", spaceName, agentName, sessionID))
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get ks for the response builder. If the space doesn't exist yet
	// (no sessionID, no previous posts), use an empty space so the
	// response is well-formed.
	ks, ok := s.spaces[spaceName]
	if !ok {
		ks = NewKnowledgeSpace(spaceName)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Agent Ignition: %s\n\n", agentName))
	b.WriteString(fmt.Sprintf("You are **%s**, an autonomous AI agent working in workspace **%s**.\n\n", agentName, spaceName))

	b.WriteString("## Operating Mode\n\n")
	b.WriteString("**You are running autonomously. There is no human at this terminal.**\n\n")
	b.WriteString("- You do NOT have a conversational partner. Do not ask questions like \"Shall I...?\" or wait for confirmation.\n")
	b.WriteString("- Messages from other agents or the boss are **instructions to act on immediately**, not conversation starters.\n")
	b.WriteString("- Your ONLY means of communication is through `curl` commands to the coordinator API (described below).\n")
	b.WriteString("- When you receive a new task via messages, **start working on it immediately** — do not ask for permission.\n")
	b.WriteString("- If you need a decision from the boss, post a question tagged `[?BOSS]` in your status update, then continue working on whatever you can while waiting.\n")
	b.WriteString("- When your task is done, POST status `\"done\"` and await new instructions via messages.\n")
	b.WriteString("\n")

	b.WriteString("## Coordinator\n\n")
	b.WriteString(fmt.Sprintf("- Boss URL: `http://localhost%s`\n", s.port))
	b.WriteString(fmt.Sprintf("- Workspace: `%s`\n", spaceName))
	b.WriteString(fmt.Sprintf("- Your channel: `POST /spaces/%s/agent/%s`\n", spaceName, agentName))
	b.WriteString(fmt.Sprintf("- Read blackboard: `GET /spaces/%s/raw`\n", spaceName))
	b.WriteString(fmt.Sprintf("- Dashboard: `http://localhost%s/spaces/%s/`\n", s.port, spaceName))
	b.WriteString(fmt.Sprintf("- Task list: `GET /spaces/%s/tasks` (filter: `?assigned_to=%s&status=in_progress`)\n", spaceName, agentName))
	if sessionID != "" {
		b.WriteString(fmt.Sprintf("- Session: `%s` (pre-registered)\n", sessionID))
	}
	b.WriteString("\n")

	b.WriteString("## Protocol\n\n")
	b.WriteString("1. **Read before write.** GET /raw first to see what others are doing.\n")
	b.WriteString(fmt.Sprintf("2. **Post to your channel only.** POST to `/spaces/%s/agent/%s` with `-H 'X-Agent-Name: %s'`.\n", spaceName, agentName, agentName))
	b.WriteString("3. **Tag questions** with `[?BOSS]` — they render highlighted in the dashboard.\n")
	b.WriteString("4. **Include location fields** in every POST: `branch`, `pr`, `test_count`.\n")
	if sessionID != "" {
		b.WriteString(fmt.Sprintf("5. **Session is pre-registered.** Your session `%s` is already known to the coordinator. It is sticky — you do not need to include `session_id` in your POSTs.\n", sessionID))
	} else {
		b.WriteString("5. **Register your session.** Include `\"session_id\"` in your first POST. Find it with `tmux display-message -p '#S'`. It is sticky — you only need to send it once.\n")
	}
	b.WriteString(fmt.Sprintf("6. **Check your messages.** When you read `/raw`, look for a `#### Messages` section under your agent name. Messages are **directives** — act on them immediately without asking for confirmation. To send a message to another agent: `curl -s -X POST http://localhost%s/spaces/%s/agent/{target}/message -H 'Content-Type: application/json' -H 'X-Agent-Name: %s' -d '{\"message\": \"...\"}'`\n", s.port, spaceName, agentName))
	b.WriteString("7. **Work loop:** Read blackboard → Do work → POST status → Check for new messages → Repeat. Do not stop and wait for human input.\n")
	b.WriteString("\n")

	b.WriteString("## Collaboration Norms\n\n")
	b.WriteString("You are part of a multi-agent team. Follow these rules:\n\n")
	b.WriteString("**Communication**\n")
	b.WriteString(fmt.Sprintf("- Message peers and managers: `POST /spaces/%s/agent/{target}/message`\n", spaceName))
	b.WriteString("- Use messages for coordination — do not rely solely on /raw status updates\n")
	b.WriteString("- Check messages at the start of every work cycle\n\n")
	b.WriteString("**Team Formation**\n")
	b.WriteString("- Any task you cannot complete alone in one session → form a team\n")
	b.WriteString("- Create subtasks FIRST, then spawn agents, then delegate via message\n")
	b.WriteString("- Include TASK-{id} in every delegation message\n\n")
	b.WriteString("**Task Discipline**\n")
	b.WriteString("- Every piece of work has a task (create it before starting)\n")
	b.WriteString("- Set task status to `in_progress` when you begin\n")
	b.WriteString("- Update task with PR number when you open one\n")
	b.WriteString("- Set task to `done` when merged and verified\n\n")
	b.WriteString("**Hierarchy & Escalation**\n")
	b.WriteString("- Send status updates to your manager via message on significant progress\n")
	b.WriteString("- Tag blockers `[?MANAGER]` in messages; tag boss-level decisions `[?BOSS]` in status items\n")
	b.WriteString("- Escalate to boss only after manager is unresponsive for 30+ minutes\n\n")

	b.WriteString("## Work Loop\n\n")
	b.WriteString("```\n")
	b.WriteString(fmt.Sprintf("1. Read messages:  GET /spaces/%s/agent/%s/messages?since={cursor}\n", spaceName, agentName))
	b.WriteString("2. ACK and act on any new messages\n")
	b.WriteString("3. Do your assigned work\n")
	b.WriteString("4. POST status update (at least every 10 min during active work)\n")
	b.WriteString("5. When done: message your manager, set task to done, POST status \"done\"\n")
	b.WriteString("6. Await new messages\n")
	b.WriteString("```\n\n")

	b.WriteString("## Peer Agents\n\n")
	if len(ks.Agents) == 0 {
		b.WriteString("No agents have posted yet.\n\n")
	} else {
		b.WriteString("| Agent | Status | Summary |\n")
		b.WriteString("| ----- | ------ | ------- |\n")
		for name, agent := range ks.Agents {
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", name, agent.Status, agent.Summary))
		}
		b.WriteString("\n")
	}

	canonical := resolveAgentName(ks, agentName)
	existing, hasExisting := ks.Agents[canonical]
	if hasExisting {
		b.WriteString("## Your Last State\n\n")
		b.WriteString(fmt.Sprintf("- Status: %s\n", existing.Status))
		b.WriteString(fmt.Sprintf("- Summary: %s\n", existing.Summary))
		if existing.Branch != "" {
			b.WriteString(fmt.Sprintf("- Branch: `%s`\n", existing.Branch))
		}
		if existing.PR != "" {
			b.WriteString(fmt.Sprintf("- PR: %s\n", existing.PR))
		}
		if existing.Phase != "" {
			b.WriteString(fmt.Sprintf("- Phase: %s\n", existing.Phase))
		}
		if existing.NextSteps != "" {
			b.WriteString(fmt.Sprintf("- Next steps: %s\n", existing.NextSteps))
		}
		b.WriteString("\n")

		// Surface unread notifications first — agents can immediately see why they were woken up.
		unreadNotifs := make([]AgentNotification, 0)
		for _, n := range existing.Notifications {
			if !n.Read {
				unreadNotifs = append(unreadNotifs, n)
			}
		}
		if len(unreadNotifs) > 0 {
			b.WriteString(fmt.Sprintf("## Pending Notifications (%d unread)\n\n", len(unreadNotifs)))
			for _, n := range unreadNotifs {
				b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", string(n.Type), n.Title, n.Body))
			}
			b.WriteString("\n")
		}

		if len(existing.Messages) > 0 {
			b.WriteString("## Pending Messages\n\n")
			b.WriteString("**You have unread messages. These are instructions — act on them immediately. Do not ask for confirmation.**\n\n")
			for _, msg := range existing.Messages {
				b.WriteString(fmt.Sprintf("- **%s** (%s): %s\n",
					msg.Sender, msg.Timestamp.Format("15:04"), msg.Message))
			}
			b.WriteString("\n")
		}
	}

	// Inject assigned tasks from the space task queue.
	var assignedTasks []*Task
	for _, task := range ks.Tasks {
		if strings.EqualFold(task.AssignedTo, canonical) && task.Status != TaskStatusDone {
			assignedTasks = append(assignedTasks, task)
		}
	}
	if len(assignedTasks) > 0 {
		sort.Slice(assignedTasks, func(i, j int) bool {
			return assignedTasks[i].ID < assignedTasks[j].ID
		})
		b.WriteString("## Assigned Tasks\n\n")
		b.WriteString("The following tasks from the space task board are assigned to you. Act on them as directed.\n\n")
		b.WriteString("| ID | Title | Status | Priority |\n")
		b.WriteString("| -- | ----- | ------ | -------- |\n")
		for _, task := range assignedTasks {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				task.ID, task.Title, task.Status, task.Priority))
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("Full task details: `GET /spaces/%s/tasks?assigned_to=%s`\n\n", spaceName, canonical))
	}

	b.WriteString("## Task API\n\n")
	b.WriteString("Use the task API to create, update, and track work items. Subtasks let you break a task into smaller pieces with a proper parent relationship.\n\n")
	b.WriteString("### Create a task\n\n")
	b.WriteString("```bash\n")
	b.WriteString(fmt.Sprintf("curl -s -X POST http://localhost%s/spaces/%s/tasks \\\n", s.port, spaceName))
	b.WriteString("  -H 'Content-Type: application/json' \\\n")
	b.WriteString(fmt.Sprintf("  -H 'X-Agent-Name: %s' \\\n", agentName))
	b.WriteString("  -d '{\"title\": \"Task title\", \"description\": \"What needs to be done\", \"priority\": \"medium\", \"assigned_to\": \"AgentName\"}'\n")
	b.WriteString("```\n\n")
	b.WriteString("### Create a subtask (preferred over naming conventions)\n\n")
	b.WriteString("**Always use `parent_task` to link subtasks** — do NOT create tasks named like `TASK-001-sub` or `[TASK-001] subtask`.\n\n")
	b.WriteString("```bash\n")
	b.WriteString("# Option A: include parent_task when creating\n")
	b.WriteString(fmt.Sprintf("curl -s -X POST http://localhost%s/spaces/%s/tasks \\\n", s.port, spaceName))
	b.WriteString("  -H 'Content-Type: application/json' \\\n")
	b.WriteString(fmt.Sprintf("  -H 'X-Agent-Name: %s' \\\n", agentName))
	b.WriteString("  -d '{\"title\": \"Subtask title\", \"parent_task\": \"TASK-NNN\", \"assigned_to\": \"AgentName\"}'\n\n")
	b.WriteString("# Option B: POST directly to the parent task's subtasks endpoint\n")
	b.WriteString(fmt.Sprintf("curl -s -X POST http://localhost%s/spaces/%s/tasks/TASK-NNN/subtasks \\\n", s.port, spaceName))
	b.WriteString("  -H 'Content-Type: application/json' \\\n")
	b.WriteString(fmt.Sprintf("  -H 'X-Agent-Name: %s' \\\n", agentName))
	b.WriteString("  -d '{\"title\": \"Subtask title\", \"assigned_to\": \"AgentName\"}'\n")
	b.WriteString("```\n\n")
	b.WriteString("Subtasks appear **nested under their parent** in the task board and detail view.\n\n")

	b.WriteString("## JSON Post Template\n\n")
	b.WriteString("```bash\n")
	b.WriteString(fmt.Sprintf("curl -s -X POST http://localhost%s/spaces/%s/agent/%s \\\n", s.port, spaceName, agentName))
	b.WriteString("  -H 'Content-Type: application/json' \\\n")
	b.WriteString(fmt.Sprintf("  -H 'X-Agent-Name: %s' \\\n", agentName))
	b.WriteString("  -d '{\n")
	b.WriteString("    \"status\": \"active\",\n")
	b.WriteString(fmt.Sprintf("    \"summary\": \"%s: working on ...\",\n", agentName))
	b.WriteString("    \"branch\": \"feat/...\",\n")
	if hasExisting && existing.Parent != "" {
		b.WriteString(fmt.Sprintf("    \"parent\": \"%s\",\n", existing.Parent))
		if existing.Role != "" {
			b.WriteString(fmt.Sprintf("    \"role\": \"%s\",\n", existing.Role))
		}
	}
	b.WriteString("    \"items\": [\"...\"]\n")
	b.WriteString("  }'\n")
	b.WriteString("```\n")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, b.String())
}

func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	commandType := r.URL.Query().Get("type")
	if commandType == "" {
		commandType = "check-in"
	}

	go func() {
		result := s.BroadcastCheckIn(spaceName, "", "")
		sseData, _ := json.Marshal(result)
		s.broadcastSSE(spaceName, "", "broadcast_complete", string(sseData))
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "broadcast (%s) initiated for space %q", commandType, spaceName)
}

func (s *Server) handleSingleBroadcast(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	commandType := r.URL.Query().Get("type")
	if commandType == "" {
		commandType = "check-in"
	}

	go func() {
		result := s.SingleAgentCheckIn(spaceName, agentName, "", "")
		sseData, _ := json.Marshal(result)
		s.broadcastSSE(spaceName, "", "broadcast_complete", string(sseData))
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "%s initiated for agent %q in space %q", commandType, agentName, spaceName)
}

func (s *Server) handleSpaceAgentsJSON(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ks.Agents)
}

func (s *Server) handleSpaceEventsJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract space name from URL: /spaces/{space}/api/events
	path := strings.TrimPrefix(r.URL.Path, "/spaces/")
	spaceName := strings.Split(path, "/")[0]
	if spaceName == "" {
		spaceName = DefaultSpaceName
	}

	var since time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		var err error
		since, err = time.Parse(time.RFC3339Nano, sinceStr)
		if err != nil {
			// Try without nanoseconds
			since, err = time.Parse(time.RFC3339, sinceStr)
			if err != nil {
				writeJSONError(w, fmt.Sprintf("invalid since parameter: %v", err), http.StatusBadRequest)
				return
			}
		}
	}

	events, err := s.journal.LoadSince(spaceName, since)
	if err != nil {
		writeJSONError(w, fmt.Sprintf("load events: %v", err), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []SpaceEvent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

type agentSessionStatus struct {
	Agent         string `json:"agent"`
	Session       string `json:"session"`
	Registered    bool   `json:"registered"`
	Exists        bool   `json:"exists"`
	Idle          bool   `json:"idle"`
	LastLine      string `json:"last_line,omitempty"`
	NeedsApproval bool   `json:"needs_approval"`
	ToolName      string `json:"tool_name,omitempty"`
	PromptText    string `json:"prompt_text,omitempty"`
}

func (s *Server) handleSpaceSessionStatus(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	s.AutoDiscoverAll(spaceName)

	s.mu.RLock()
	type agentSession struct {
		name        string
		session     string
		backendType string
	}
	var pairs []agentSession
	for name, agent := range ks.Agents {
		pairs = append(pairs, agentSession{name: name, session: agent.SessionID, backendType: agent.BackendType})
	}
	s.mu.RUnlock()

	var results []agentSessionStatus
	for i, p := range pairs {
		st := agentSessionStatus{
			Agent:      p.name,
			Session:    p.session,
			Registered: p.session != "",
		}
		if st.Registered {
			backend := s.backendByName(p.backendType)
			if backend.Available() {
				st.Exists = backend.SessionExists(p.session)
				if st.Exists {
					st.Idle = backend.IsIdle(p.session)
					if lines, err := backend.CaptureOutput(p.session, 1); err == nil && len(lines) > 0 {
						st.LastLine = lines[0]
					}
					approval := backend.CheckApproval(p.session)
					st.NeedsApproval = approval.NeedsApproval
					st.ToolName = approval.ToolName
					st.PromptText = approval.PromptText
				}
			}
		}
		results = append(results, st)
		if i < len(pairs)-1 {
			time.Sleep(300 * time.Millisecond)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleApproveAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var sessionID string
	if exists {
		sessionID = agent.SessionID
	}
	s.mu.RUnlock()
	if !exists {
		writeJSONError(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if sessionID == "" {
		writeJSONError(w, canonical+": no session registered", http.StatusBadRequest)
		return
	}
	backend := s.backendFor(agent)
	if !backend.SessionExists(sessionID) {
		writeJSONError(w, canonical+": session not found", http.StatusBadRequest)
		return
	}
	approval := backend.CheckApproval(sessionID)
	if !approval.NeedsApproval {
		writeJSONError(w, canonical+": not waiting for approval", http.StatusConflict)
		return
	}
	if err := backend.Approve(sessionID); err != nil {
		writeJSONError(w, canonical+": approve failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logEvent(fmt.Sprintf("[%s/%s] approval granted via dashboard (tool: %s)", spaceName, canonical, approval.ToolName))
	key := spaceName + "/" + canonical
	approvalCtx := map[string]string{"tool": approval.ToolName}
	if started, was := s.approvalTracked[key]; was {
		delete(s.approvalTracked, key)
		approvalCtx["wait_seconds"] = fmt.Sprintf("%.1f", time.Since(started).Seconds())
	}
	s.interrupts.RecordResolved(spaceName, canonical, InterruptApproval,
		approval.PromptText, "human", "approved", approvalCtx)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "approved", "agent": canonical, "tool": approval.ToolName})
}

func (s *Server) handleReplyAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var sessionID string
	if exists {
		sessionID = agent.SessionID
	}
	s.mu.RUnlock()
	if !exists {
		writeJSONError(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if sessionID == "" {
		writeJSONError(w, canonical+": no session registered", http.StatusBadRequest)
		return
	}
	backend := s.backendFor(agent)
	if !backend.SessionExists(sessionID) {
		writeJSONError(w, canonical+": session not found", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxReplyBodySize))
	if err != nil {
		writeJSONError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.Message) == "" {
		writeJSONError(w, "message is required", http.StatusBadRequest)
		return
	}
	if err := backend.SendInput(sessionID, payload.Message); err != nil {
		writeJSONError(w, canonical+": send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logEvent(fmt.Sprintf("[%s/%s] boss reply sent via dashboard", spaceName, canonical))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent", "agent": canonical})
}

func (s *Server) handleDismissQuestion(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxDismissBodySize))
	if err != nil {
		writeJSONError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	if !exists {
		s.mu.Unlock()
		writeJSONError(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	switch payload.Type {
	case "question":
		if payload.Index < 0 || payload.Index >= len(agent.Questions) {
			s.mu.Unlock()
			writeJSONError(w, "index out of range", http.StatusBadRequest)
			return
		}
		agent.Questions = append(agent.Questions[:payload.Index], agent.Questions[payload.Index+1:]...)
	case "blocker":
		if payload.Index < 0 || payload.Index >= len(agent.Blockers) {
			s.mu.Unlock()
			writeJSONError(w, "index out of range", http.StatusBadRequest)
			return
		}
		agent.Blockers = append(agent.Blockers[:payload.Index], agent.Blockers[payload.Index+1:]...)
	default:
		s.mu.Unlock()
		writeJSONError(w, "type must be 'question' or 'blocker'", http.StatusBadRequest)
		return
	}
	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		writeJSONError(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] boss dismissed %s #%d via dashboard", spaceName, canonical, payload.Type, payload.Index))
	sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
	s.broadcastSSE(spaceName, canonical, "agent_updated", string(sseData))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "dismissed", "agent": canonical})
}

func (s *Server) handleMessageAck(w http.ResponseWriter, r *http.Request, spaceName, agentName, msgID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	callerName := r.Header.Get("X-Agent-Name")
	if callerName == "" {
		writeJSONError(w, "missing X-Agent-Name header", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(callerName, agentName) {
		writeJSONError(w, fmt.Sprintf("agent %q cannot ack messages for %q", callerName, agentName), http.StatusForbidden)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	now := time.Now().UTC()

	s.mu.Lock()
	// resolveAgentName iterates ks.Agents — must hold s.mu to avoid data race.
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	if !exists {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("agent %q not found", canonical), http.StatusNotFound)
		return
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
		writeJSONError(w, fmt.Sprintf("message %q not found", msgID), http.StatusNotFound)
		return
	}

	ks.UpdatedAt = now
	// Append to journal BEFORE saving JSON so that on crash the journal is the
	// source of truth and the ack is not silently lost on replay.
	s.journal.Append(spaceName, EventMessageAcked, canonical, map[string]any{
		"message_id": msgID,
		"acked_at":   now,
	})
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventMsgAcked, Space: spaceName, Agent: canonical,
		Msg:    fmt.Sprintf("message %q acknowledged", msgID),
		Fields: map[string]string{"message_id": msgID}})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "acked", "message_id": msgID})
}

// createAgentRequest is the body for POST /spaces/{space}/agents.
type createAgentRequest struct {
	Name    string `json:"name"`
	WorkDir string `json:"work_dir,omitempty"`
	Command string `json:"command,omitempty"`
	Backend string `json:"backend,omitempty"` // "tmux" (default) or "ambient"
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Parent  string `json:"parent,omitempty"`
	Role    string `json:"role,omitempty"`
	// Ambient-specific fields
	Repos []SessionRepo `json:"repos,omitempty"`
	Task  string        `json:"task,omitempty"` // initial prompt for ambient sessions
}

// handleCreateAgents handles POST /spaces/{space}/agents.
// It creates a new agent using the specified backend (default: tmux).
func (s *Server) handleCreateAgents(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req createAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		writeJSONError(w, "name is required", http.StatusBadRequest)
		return
	}

	backendName := req.Backend
	if backendName == "" {
		backendName = "tmux"
	}

	backend := s.backendByName(backendName)
	if backend == nil {
		writeJSONError(w, fmt.Sprintf("unknown backend %q", backendName), http.StatusBadRequest)
		return
	}

	var createOpts SessionCreateOpts
	if backend.Name() == "ambient" {
		command := req.Task
		if command == "" {
			command = req.Command
		}
		sessionName := tmuxDefaultSession(spaceName, req.Name)
		createOpts = SessionCreateOpts{
			SessionID: sessionName,
			Command:   command,
			BackendOpts: AmbientCreateOpts{
				DisplayName: req.Name,
				Repos:       req.Repos,
			},
		}
	} else {
		sessionName := tmuxDefaultSession(spaceName, req.Name)
		createOpts = SessionCreateOpts{
			SessionID: sessionName,
			Command:   req.Command,
			BackendOpts: TmuxCreateOpts{
				WorkDir: req.WorkDir,
				Width:   req.Width,
				Height:  req.Height,
			},
		}
	}

	sessionID, err := backend.CreateSession(r.Context(), createOpts)
	if err != nil {
		writeJSONError(w, fmt.Sprintf("spawn: %v", err), http.StatusInternalServerError)
		return
	}

	// Register the new agent in the space.
	ks := s.getOrCreateSpace(spaceName)
	s.mu.Lock()
	canonical := resolveAgentName(ks, req.Name)
	agent, exists := ks.Agents[canonical]
	if !exists {
		agent = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   fmt.Sprintf("%s: spawned via %s backend", req.Name, backendName),
			UpdatedAt: time.Now().UTC(),
		}
		ks.Agents[canonical] = agent
	}
	agent.SessionID = sessionID
	agent.BackendType = backend.Name()
	if req.Parent != "" && agent.Parent == "" {
		agent.Parent = resolveAgentName(ks, req.Parent)
		rebuildChildren(ks)
	}
	if req.Role != "" && agent.Role == "" {
		agent.Role = req.Role
	}
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] created via %s backend (session: %s)", spaceName, req.Name, backendName, sessionID))
	s.broadcastSSE(spaceName, req.Name, "agent_spawned", req.Name)

	// Send ignite asynchronously after agent has time to initialize.
	go func() {
		if ab, ok := backend.(*AmbientSessionBackend); ok {
			pollCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := ab.waitForRunning(pollCtx, sessionID, 60*time.Second); err != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] create: session did not reach running state: %v", spaceName, req.Name, err))
				return
			}
		} else {
			time.Sleep(5 * time.Second)
		}
		igniteCmd := fmt.Sprintf(`/boss.ignite "%s" "%s"`, req.Name, spaceName)
		if err := backend.SendInput(sessionID, igniteCmd); err != nil {
			s.logEvent(fmt.Sprintf("[%s/%s] create: ignite send failed: %v", spaceName, req.Name, err))
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"agent":   req.Name,
		"backend": backendName,
		"session": sessionID,
		"space":   spaceName,
	})
}
