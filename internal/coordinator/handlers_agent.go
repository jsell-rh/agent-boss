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
		agent, exists := ks.agentStatusOk(canonical)
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
		if existingRec, ok := ks.Agents[canonical]; ok {
			existing := existingRec.Status
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
		ks.setAgentStatus(canonical, &update)
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

		// ?close_tasks=true: when agent posts done, cascade to their in_progress tasks.
		if update.Status == StatusDone && r.URL.Query().Get("close_tasks") == "true" {
			s.closeAgentTasks(spaceName, canonical)
		}

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
		if agent, exists := ks.agentStatusOk(canonical); exists && agent != nil {
			sessionID = agent.SessionID
			backendType = agent.BackendType
		}
		// Clear dangling parent references: children of this agent become root-level.
		for _, rec := range ks.Agents {
			if rec != nil && rec.Status != nil && strings.EqualFold(rec.Status.Parent, canonical) {
				rec.Status.Parent = ""
			}
		}
		delete(ks.Agents, canonical)
		rebuildChildren(ks) // keep children lists consistent after removal
		ks.UpdatedAt = time.Now().UTC()
		// Clean up in-memory approval tracking for this agent.
		delete(s.approvalTracked, spaceName+"/"+canonical)
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.deleteAgentFromDB(spaceName, canonical)

		// Cascade: kill the backing session if one exists.
		if sessionID != "" {
			backend, backendErr := s.backendByName(backendType)
			if backendErr != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] warning: cascade delete skipped: %v", spaceName, canonical, backendErr))
			} else {
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
		sender, senderExists := ks.agentStatusOk(senderCanonical)
		s.mu.RUnlock()
		if !senderExists || sender.Parent == "" {
			writeJSONError(w, "agent has no declared parent", http.StatusBadRequest)
			return
		}
		agentName = sender.Parent
	}

	// sendMessageBody extends AgentMessage with an optional field to resolve a pending decision.
	var sendBody struct {
		AgentMessage
		ReplyToDecisionID string `json:"reply_to_decision_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&sendBody); err != nil {
		writeJSONError(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
		return
	}
	messageReq := sendBody.AgentMessage
	replyToDecisionID := sendBody.ReplyToDecisionID

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
			Agents:    make(map[string]*AgentRecord),
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
		ag := ks.agentStatus(r)
		if ag == nil {
			ag = &AgentUpdate{
				Status:    StatusIdle,
				Summary:   fmt.Sprintf("%s: pending message delivery", r),
				Messages:  []AgentMessage{},
				UpdatedAt: time.Now().UTC(),
			}
			ks.setAgentStatus(r, ag)
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

	// If the sender provided a decision message ID to resolve, mark it Resolved in the sender's
	// own message list. Decision messages live in the sender's inbox (e.g. boss's messages when
	// an agent calls request_decision), so we look in the canonical sender's messages.
	if replyToDecisionID != "" {
		senderRecord := ks.agentStatus(strings.ToLower(senderName))
		if senderRecord != nil {
			for i := range senderRecord.Messages {
				if senderRecord.Messages[i].ID == replyToDecisionID &&
					senderRecord.Messages[i].Type == "decision" {
					senderRecord.Messages[i].Resolved = true
					senderRecord.Messages[i].Resolution = messageReq.Message
					break
				}
			}
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
		if ks.agentStatus(canonical) == nil {
			ks.setAgentStatus(canonical, &AgentUpdate{
				Status:    StatusActive,
				Summary:   "Document uploaded",
				UpdatedAt: time.Now().UTC(),
			})
		}

		agent := ks.agentStatus(canonical)

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
			if agent := ks.agentStatus(canonical); agent != nil {
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

		agentRecord := ks.agentStatus(canonical)
		if agentRecord == nil {
			agentRecord = &AgentUpdate{
				Status:    StatusIdle,
				Summary:   canonical + ": awaiting ignition",
				UpdatedAt: time.Now().UTC(),
			}
			ks.setAgentStatus(canonical, agentRecord)
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
	text := s.buildIgnitionText(spaceName, agentName, sessionID)
	// Prepend persona prompts if the agent has personas configured.
	// Mirrors the same logic in mcp_server.go for MCP-connected agents.
	if ks, ok := s.spaces[spaceName]; ok {
		canonical := resolveAgentName(ks, agentName)
		if cfg := ks.agentConfig(canonical); cfg != nil && len(cfg.Personas) > 0 {
			if personaPrompt := s.assemblePersonaPrompt(cfg.Personas); personaPrompt != "" {
				text = personaPrompt + "\n\n" + text
			}
		}
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, text)
}

// buildIgnitionText assembles the agent ignition/bootstrap text.
// Must be called with s.mu.RLock held.
func (s *Server) buildIgnitionText(spaceName, agentName, sessionID string) string {
	ks, ok := s.spaces[spaceName]
	if !ok {
		ks = NewKnowledgeSpace(spaceName)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Agent Ignition: %s\n\n", agentName))
	b.WriteString(fmt.Sprintf("You are **%s**, an autonomous AI agent working in workspace **%s**.\n\n", agentName, spaceName))

	// Persona directives — front and center, before operating instructions.
	canonical := resolveAgentName(ks, agentName)
	if cfg := ks.agentConfig(canonical); cfg != nil && len(cfg.Personas) > 0 {
		personaPrompt := s.assemblePersonaPrompt(cfg.Personas)
		if personaPrompt != "" {
			b.WriteString("## Your Role & Persona\n\n")
			b.WriteString("**IMPORTANT: The following directives define who you are and how you must behave. Follow them precisely.**\n\n")
			b.WriteString(personaPrompt)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Operating Mode\n\n")
	b.WriteString("**You are running autonomously. There is no human at this terminal.**\n\n")
	b.WriteString("- You do NOT have a conversational partner. Do not ask questions or wait for confirmation.\n")
	b.WriteString("- Messages from other agents or the boss are **instructions to act on immediately**.\n")
	b.WriteString(fmt.Sprintf("- You interact with the coordinator using your **%s tools** (described below).\n", s.mcpServerName()))
	b.WriteString("- When you receive a new task via messages, **start working on it immediately**.\n")
	b.WriteString("- If you need a decision, use `send_message` to your manager and continue working on what you can.\n")
	b.WriteString("- When your task is done, use `post_status` with status `\"done\"` and await new messages.\n")
	b.WriteString("- **Never exit claude code.** Your session is permanent — boss will kill it when needed. Stay running and await new messages after each task.\n")
	b.WriteString("\n")

	b.WriteString("## Coordinator\n\n")
	b.WriteString(fmt.Sprintf("- Workspace: `%s`\n", spaceName))
	b.WriteString(fmt.Sprintf("- Agent name: `%s`\n", agentName))
	if sessionID != "" {
		b.WriteString(fmt.Sprintf("- Session: `%s` (pre-registered)\n", sessionID))
	}
	b.WriteString("\n")

	mcpName := s.mcpServerName()
	b.WriteString(fmt.Sprintf("## MCP Tools (%s)\n\n", mcpName))
	b.WriteString(fmt.Sprintf("You have **%s** tools available. Use these for ALL coordinator interactions:\n\n", mcpName))
	b.WriteString("| Tool | Purpose |\n")
	b.WriteString("| ---- | ------- |\n")
	b.WriteString("| `post_status` | Report your current status, branch, PR, progress |\n")
	b.WriteString("| `check_messages` | Poll for new messages (call at start of every work cycle) |\n")
	b.WriteString("| `send_message` | Send a message to another agent or your parent |\n")
	b.WriteString("| `ack_message` | Acknowledge a message you have acted on |\n")
	b.WriteString("| `request_decision` | Ask the human operator for a decision when you need human input |\n")
	b.WriteString("| `create_task` | Create a new task (always create before starting work) |\n")
	b.WriteString("| `list_tasks` | List tasks, optionally filtered by status/assignee |\n")
	b.WriteString("| `move_task` | Change task status: backlog → in_progress → review → done |\n")
	b.WriteString("| `update_task` | Update task fields (title, PR link, assignee, etc.) |\n")
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("All tools require `space: \"%s\"` and `agent: \"%s\"` parameters.\n\n", spaceName, agentName))

	b.WriteString("## Protocol\n\n")
	b.WriteString("1. **Check messages first.** Use `check_messages` at the start of every work cycle.\n")
	b.WriteString("2. **Post status regularly.** Use `post_status` at least every 10 minutes during active work.\n")
	b.WriteString("3. **Include location fields** in every status update: `branch`, `pr`, `test_count`.\n")
	b.WriteString("4. **Escalate decisions** — use `request_decision` when you need human input. For peer coordination, use `send_message`.\n")
	b.WriteString("5. **ACK messages** you have acted on using `ack_message`.\n")
	b.WriteString("6. **Task discipline** — create a task before starting work, move it through statuses, link your PR.\n")
	b.WriteString("\n")

	b.WriteString("## Collaboration Norms\n\n")
	b.WriteString("**Communication:** Use `send_message` for coordination. Check messages every work cycle. ACK messages you act on.\n\n")
	b.WriteString("**Team Formation:** Any task you cannot complete alone → create subtasks, delegate via `send_message` with TASK-{id}.\n\n")
	b.WriteString("**Task Discipline:** Create task → `in_progress` → link PR → `review` → `done`. Always use `parent_task` for subtasks.\n\n")
	b.WriteString("**Hierarchy:** Report progress to your manager via `send_message`. Escalate blockers promptly. Continue working while waiting.\n\n")

	b.WriteString("## Work Loop\n\n")
	b.WriteString("```\n")
	b.WriteString("1. check_messages → read and ACK any directives\n")
	b.WriteString("2. Do your assigned work\n")
	b.WriteString("3. post_status (at least every 10 min during active work)\n")
	b.WriteString("4. When done: send_message to manager, move_task to done, post_status \"done\"\n")
	b.WriteString("5. check_messages → await new instructions\n")
	b.WriteString("```\n\n")

	// Peer agents
	b.WriteString("## Peer Agents\n\n")
	if len(ks.Agents) == 0 {
		b.WriteString("No agents have posted yet.\n\n")
	} else {
		b.WriteString("| Agent | Status | Summary |\n")
		b.WriteString("| ----- | ------ | ------- |\n")
		for name, rec := range ks.Agents {
			if rec == nil || rec.Status == nil {
				continue
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", name, rec.Status.Status, rec.Status.Summary))
		}
		b.WriteString("\n")
	}

	// Previous state, notifications, messages
	existing, hasExisting := ks.agentStatusOk(canonical)
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
		if existing.Parent != "" {
			b.WriteString(fmt.Sprintf("- Manager: %s\n", existing.Parent))
		}
		b.WriteString("\n")

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
			b.WriteString("**You have unread messages. These are instructions — act on them immediately.**\n\n")
			for _, msg := range existing.Messages {
				b.WriteString(fmt.Sprintf("- **%s** (%s): %s\n",
					msg.Sender, msg.Timestamp.Format("15:04"), msg.Message))
			}
			b.WriteString("\n")
		}
	}

	// Assigned tasks
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
		b.WriteString("| ID | Title | Status | Priority |\n")
		b.WriteString("| -- | ----- | ------ | -------- |\n")
		for _, task := range assignedTasks {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				task.ID, task.Title, task.Status, task.Priority))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Full Protocol\n\n")
	b.WriteString("For the complete collaboration protocol (detailed norms, JSON format reference, endpoint tables),\n")
	b.WriteString("read the `boss://protocol` MCP resource.\n")

	return b.String()
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
	// Enrich agent records with persona_outdated info.
	type agentWithPersonaStatus struct {
		*AgentRecord
		PersonaOutdated  bool                        `json:"persona_outdated,omitempty"`
		PersonaVersions  map[string]personaVersionInfo `json:"persona_versions,omitempty"`
	}
	result := make(map[string]agentWithPersonaStatus, len(ks.Agents))
	for name, rec := range ks.Agents {
		entry := agentWithPersonaStatus{AgentRecord: rec}
		if rec != nil && rec.Config != nil && s.personas != nil {
			for _, ref := range rec.Config.Personas {
				cur := s.personas.currentVersion(ref.ID)
				if ref.PinnedVersion < cur {
					entry.PersonaOutdated = true
					if entry.PersonaVersions == nil {
						entry.PersonaVersions = make(map[string]personaVersionInfo)
					}
					entry.PersonaVersions[ref.ID] = personaVersionInfo{
						Pinned:  ref.PinnedVersion,
						Current: cur,
					}
				}
			}
		}
		result[name] = entry
	}
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

type personaVersionInfo struct {
	Pinned  int `json:"pinned"`
	Current int `json:"current"`
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
	for name, rec := range ks.Agents {
		if rec == nil || rec.Status == nil { continue }
		pairs = append(pairs, agentSession{name: name, session: rec.Status.SessionID, backendType: rec.Status.BackendType})
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
			backend, _ := s.backendByName(p.backendType)
			if backend != nil && backend.Available() {
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
	agent, exists := ks.agentStatusOk(canonical)
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
	// Get current approval state for metadata (tool name, prompt text).
	// Do NOT block on this check — the user explicitly clicked Approve in the
	// dashboard (meaning an approval interrupt was already recorded by the
	// liveness monitor). A re-check here is a race: the detection pattern may
	// not match on a second read even though the agent is still waiting.
	// We send the approval keystroke regardless and let the liveness monitor
	// clear the interrupt on the next poll.
	always := r.URL.Query().Get("always") == "true"
	approval := backend.CheckApproval(sessionID)
	var approveErr error
	if always {
		approveErr = backend.AlwaysAllow(sessionID)
	} else {
		approveErr = backend.Approve(sessionID)
	}
	if approveErr != nil {
		writeJSONError(w, canonical+": approve failed: "+approveErr.Error(), http.StatusInternalServerError)
		return
	}
	mode := "approved"
	if always {
		mode = "always_allowed"
	}
	s.logEvent(fmt.Sprintf("[%s/%s] approval %s via dashboard (tool: %s)", spaceName, canonical, mode, approval.ToolName))
	key := spaceName + "/" + canonical
	approvalCtx := map[string]string{"tool": approval.ToolName, "mode": mode}
	if started, was := s.approvalTracked[key]; was {
		delete(s.approvalTracked, key)
		approvalCtx["wait_seconds"] = fmt.Sprintf("%.1f", time.Since(started).Seconds())
	}
	s.interrupts.RecordResolved(spaceName, canonical, InterruptApproval,
		approval.PromptText, "human", mode, approvalCtx)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": mode, "agent": canonical, "tool": approval.ToolName})
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
	agent, exists := ks.agentStatusOk(canonical)
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
	agent, exists := ks.agentStatusOk(canonical)
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
	agent, exists := ks.agentStatusOk(canonical)
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

// handleDecisionAck marks a decision message as resolved with the operator's reply text.
// POST /spaces/{space}/agent/{agent}/message/{id}/resolve
func (s *Server) handleDecisionAck(w http.ResponseWriter, r *http.Request, spaceName, agentName, msgID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Resolution string `json:"resolution"`
	}
	json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck — empty body is fine (resolution can be empty)

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	s.mu.Lock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.agentStatusOk(canonical)
	if !exists {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("agent %q not found", canonical), http.StatusNotFound)
		return
	}

	found := false
	for i := range agent.Messages {
		if agent.Messages[i].ID == msgID {
			if agent.Messages[i].Type != MessageTypeDecision {
				s.mu.Unlock()
				writeJSONError(w, "message is not a decision request", http.StatusBadRequest)
				return
			}
			agent.Messages[i].Resolved = true
			agent.Messages[i].Resolution = body.Resolution
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("message %q not found", msgID), http.StatusNotFound)
		return
	}

	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventMsgAcked, Space: spaceName, Agent: canonical,
		Msg:    fmt.Sprintf("decision %q resolved", msgID),
		Fields: map[string]string{"message_id": msgID}})

	// Broadcast agent_message so App.vue reloads the space and the embed shows as resolved.
	sseData, _ := json.Marshal(map[string]any{
		"space": spaceName, "agent": canonical, "message_id": msgID, "type": "decision_resolved",
	})
	s.broadcastSSE(spaceName, canonical, "agent_message", string(sseData))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "resolved", "message_id": msgID})
}

// createAgentRequest is the body for POST /spaces/{space}/agents.
type createAgentRequest struct {
	Name           string `json:"name"`
	WorkDir        string `json:"work_dir,omitempty"`
	Backend        string `json:"backend,omitempty"` // "tmux" (default) or "ambient"
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	Parent         string `json:"parent,omitempty"`
	Role           string `json:"role,omitempty"`
	InitialMessage string `json:"initial_message,omitempty"` // one-time message sent to agent after ignite
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

	backend, err := s.backendByName(backendName)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var createOpts SessionCreateOpts
	if backend.Name() == "ambient" {
		sessionName := tmuxDefaultSession(spaceName, req.Name)
		createOpts = SessionCreateOpts{
			SessionID: sessionName,
			Command:   req.Task,
			BackendOpts: AmbientCreateOpts{
				DisplayName: req.Name,
				Repos:       req.Repos,
				EnvVars: func() map[string]string {
					if s.apiToken == "" {
						return nil
					}
					return map[string]string{"BOSS_API_TOKEN": s.apiToken}
				}(),
			},
		}
	} else {
		sessionName := tmuxDefaultSession(spaceName, req.Name)
		spawnCommand := ""
		if s.allowSkipPermissions {
			spawnCommand = "claude --dangerously-skip-permissions"
		}
		createOpts = SessionCreateOpts{
			SessionID: sessionName,
			Command:   spawnCommand,
			BackendOpts: TmuxCreateOpts{
				WorkDir:              req.WorkDir,
				Width:                req.Width,
				Height:               req.Height,
				MCPServerURL:         s.localURL(),
				MCPServerName:        s.mcpServerName(),
				AgentToken:           s.apiToken,
				AllowSkipPermissions: s.allowSkipPermissions,
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
	agent := ks.agentStatus(canonical)
	if agent == nil {
		agent = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   fmt.Sprintf("%s: spawned via %s backend", req.Name, backendName),
			UpdatedAt: time.Now().UTC(),
		}
		ks.setAgentStatus(canonical, agent)
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
	// Persist work_dir (and any future create-time config) to AgentConfig so it
	// is visible in the agent detail view and survives restarts.
	if req.WorkDir != "" {
		cfg := ks.agentConfig(canonical)
		if cfg == nil {
			cfg = &AgentConfig{}
		}
		cfg.WorkDir = req.WorkDir
		ks.setAgentConfig(canonical, cfg)
	}
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] created via %s backend (session: %s)", spaceName, req.Name, backendName, sessionID))
	spawnedPayload, _ := json.Marshal(map[string]string{"space": spaceName, "agent": req.Name})
	s.broadcastSSE(spaceName, req.Name, "agent_spawned", string(spawnedPayload))

	// Capture closure variables before goroutine.
	initialMsg := req.InitialMessage
	agentNameForIgnite := req.Name

	// Send ignite asynchronously after agent has time to initialize.
	go func() {
		if ab, ok := backend.(*AmbientSessionBackend); ok {
			pollCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := ab.waitForRunning(pollCtx, sessionID, 60*time.Second); err != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] create: session did not reach running state: %v", spaceName, agentNameForIgnite, err))
				return
			}
		} else {
			if err := waitForIdle(sessionID, 60*time.Second); err != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] create: timed out waiting for idle before ignite: %v — sending anyway", spaceName, agentNameForIgnite, err))
			}
		}
		// Send plain-text ignition prompt directly — no slash command required.
		s.mu.RLock()
		igniteText := s.buildIgnitionText(spaceName, agentNameForIgnite, sessionID)
		s.mu.RUnlock()
		if err := backend.SendInput(sessionID, igniteText); err != nil {
			s.logEvent(fmt.Sprintf("[%s/%s] create: ignite send failed: %v", spaceName, agentNameForIgnite, err))
		}
		if initialMsg != "" {
			s.deliverInternalMessage(spaceName, agentNameForIgnite, "boss", initialMsg)
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

// handleAgentConfig handles GET and PATCH /spaces/{space}/agent/{name}/config.
// GET returns the current AgentConfig (or empty object if none).
// PATCH performs a partial update of AgentConfig fields.
func (s *Server) handleAgentConfig(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		canonical := resolveAgentName(ks, agentName)
		cfg := ks.agentConfig(canonical)
		s.mu.RUnlock()
		if cfg == nil {
			cfg = &AgentConfig{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)

	case http.MethodPatch:
		callerName := r.Header.Get("X-Agent-Name")
		if callerName == "" {
			writeJSONError(w, "missing X-Agent-Name header", http.StatusBadRequest)
			return
		}
		var patch AgentConfig
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		canonical := resolveAgentName(ks, agentName)
		cfg := ks.agentConfig(canonical)
		if cfg == nil {
			cfg = &AgentConfig{}
		}
		// Merge non-zero patch fields
		if patch.WorkDir != "" {
			cfg.WorkDir = patch.WorkDir
		}
		if patch.InitialPrompt != "" {
			cfg.InitialPrompt = patch.InitialPrompt
		}
		if patch.Personas != nil {
			cfg.Personas = s.resolvePersonaRefs(patch.Personas)
		}
		if patch.Backend != "" {
			cfg.Backend = patch.Backend
		}
		if patch.Command != "" {
			cfg.Command = patch.Command
		}
		if patch.RepoURL != "" {
			cfg.RepoURL = patch.RepoURL
		}
		if patch.Repos != nil {
			cfg.Repos = patch.Repos
		}
		if patch.Model != "" {
			cfg.Model = patch.Model
		}
		ks.setAgentConfig(canonical, cfg)
		ks.UpdatedAt = time.Now().UTC()
		snap := ks.snapshot()
		s.mu.Unlock()
		s.saveSpace(snap)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAgentDuplicate handles POST /spaces/{space}/agent/{name}/duplicate.
// Creates a new agent by copying the source agent's config, then auto-spawns it.
func (s *Server) handleAgentDuplicate(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NewName        string      `json:"new_name"`
		OverrideConfig AgentConfig `json:"override_config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.NewName) == "" {
		writeJSONError(w, "new_name is required", http.StatusBadRequest)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	s.mu.Lock()
	srcCanonical := resolveAgentName(ks, agentName)
	newCanonical := resolveAgentName(ks, req.NewName)

	// Check for name collision
	if _, exists := ks.Agents[newCanonical]; exists {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("agent %q already exists", req.NewName), http.StatusConflict)
		return
	}

	// Deep-copy source config
	var newCfg AgentConfig
	if srcCfg := ks.agentConfig(srcCanonical); srcCfg != nil {
		newCfg = *srcCfg
		if srcCfg.Personas != nil {
			newCfg.Personas = make([]PersonaRef, len(srcCfg.Personas))
			copy(newCfg.Personas, srcCfg.Personas)
		}
		if srcCfg.Repos != nil {
			newCfg.Repos = make([]SessionRepo, len(srcCfg.Repos))
			copy(newCfg.Repos, srcCfg.Repos)
		}
	}

	// Apply override_config fields
	if req.OverrideConfig.WorkDir != "" {
		newCfg.WorkDir = req.OverrideConfig.WorkDir
	}
	if req.OverrideConfig.InitialPrompt != "" {
		newCfg.InitialPrompt = req.OverrideConfig.InitialPrompt
	}
	if req.OverrideConfig.Personas != nil {
		newCfg.Personas = req.OverrideConfig.Personas
	}
	if req.OverrideConfig.Backend != "" {
		newCfg.Backend = req.OverrideConfig.Backend
	}
	if req.OverrideConfig.Command != "" {
		newCfg.Command = req.OverrideConfig.Command
	}
	if req.OverrideConfig.RepoURL != "" {
		newCfg.RepoURL = req.OverrideConfig.RepoURL
	}
	if req.OverrideConfig.Repos != nil {
		newCfg.Repos = req.OverrideConfig.Repos
	}
	if req.OverrideConfig.Model != "" {
		newCfg.Model = req.OverrideConfig.Model
	}

	// Create new AgentRecord with copied config and fresh idle status
	now := time.Now().UTC()
	newStatus := &AgentUpdate{
		Status:    StatusIdle,
		Summary:   fmt.Sprintf("%s: duplicated from %s", req.NewName, agentName),
		UpdatedAt: now,
	}
	newRec := &AgentRecord{
		Config: &newCfg,
		Status: newStatus,
	}
	ks.Agents[newCanonical] = newRec
	ks.UpdatedAt = now
	snap := ks.snapshot()
	s.mu.Unlock()

	s.saveSpace(snap)
	s.logEvent(fmt.Sprintf("[%s/%s] duplicated from %s", spaceName, req.NewName, agentName))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"agent":  req.NewName,
		"config": newCfg,
	})
}
