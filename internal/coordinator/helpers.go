package coordinator

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// writeJSONError writes a JSON {"error":"..."} response with the given status code.
// All API error paths should use this instead of http.Error to ensure consistent
// Content-Type and body format for programmatic clients.
func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func (s *Server) logEvent(msg string) {
	s.emit(DomainEvent{
		Level:     LevelInfo,
		EventType: EventGeneric,
		Msg:       msg,
	})
}

// emit records a domain event to the Logger and appends a formatted entry to
// the in-memory EventLog ring buffer for backward compatibility.
func (s *Server) emit(e DomainEvent) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	// Emit to structured logger (JSON or pretty, depending on runtime config).
	if s.logger != nil {
		s.logger.Log(e)
	}
	// Also retain in EventLog ring buffer for the dashboard /events endpoint.
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	entry := fmt.Sprintf("[%s] %s", e.Timestamp.Format("15:04:05"), e.Msg)
	s.EventLog = append(s.EventLog, entry)
	if len(s.EventLog) > EventLogCap {
		s.EventLog = s.EventLog[len(s.EventLog)-EventLogCap:]
	}
}

func (s *Server) RecentEvents(n int) []string {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if n > len(s.EventLog) {
		n = len(s.EventLog)
	}
	out := make([]string, n)
	copy(out, s.EventLog[len(s.EventLog)-n:])
	return out
}

func resolveAgentName(ks *KnowledgeSpace, raw string) string {
	for existing := range ks.Agents {
		if strings.EqualFold(existing, raw) {
			return existing
		}
	}
	return raw
}

var devNullPattern = regexp.MustCompile(`\s*<\s*/dev/null\s*`)

func sanitizeInput(s string) string {
	return devNullPattern.ReplaceAllString(s, "")
}

func sanitizeAgentUpdate(u *AgentUpdate) {
	u.Summary = sanitizeInput(u.Summary)
	u.Phase = sanitizeInput(u.Phase)
	u.Mood = sanitizeInput(u.Mood)
	u.FreeText = sanitizeInput(u.FreeText)
	u.NextSteps = sanitizeInput(u.NextSteps)
	for i, item := range u.Items {
		u.Items[i] = sanitizeInput(item)
	}
	for i, q := range u.Questions {
		u.Questions[i] = sanitizeInput(q)
	}
	for i, b := range u.Blockers {
		u.Blockers[i] = sanitizeInput(b)
	}
	for si := range u.Sections {
		u.Sections[si].Title = sanitizeInput(u.Sections[si].Title)
		for i, item := range u.Sections[si].Items {
			u.Sections[si].Items[i] = sanitizeInput(item)
		}
	}
	// ## TODO - REMOVE ME — migrate deprecated "tmux_session" JSON field to SessionID ## TODO
	if u.DeprecatedTmuxSession != "" && u.SessionID == "" {
		u.SessionID = u.DeprecatedTmuxSession
	}
	u.DeprecatedTmuxSession = ""
}

func truncateLine(s string, maxLen int) string {
	line := strings.SplitN(s, "\n", 2)[0]
	line = strings.TrimSpace(line)
	if len(line) > maxLen {
		return line[:maxLen-3] + "..."
	}
	return line
}

// deliverInternalMessage queues a message from senderName to agentName in spaceName.
// Called internally (e.g. from spawn handler) without an HTTP request.
// Must NOT be called while holding s.mu.
func (s *Server) deliverInternalMessage(spaceName, agentName, senderName, message string) {
	ks := s.getOrCreateSpace(spaceName)
	now := time.Now().UTC()
	msg := AgentMessage{
		ID:        fmt.Sprintf("%d", now.UnixNano()),
		Message:   message,
		Sender:    senderName,
		Priority:  PriorityInfo,
		Timestamp: now,
	}

	s.mu.Lock()
	canonical := resolveAgentName(ks, agentName)
	ag := ks.agentStatus(canonical)
	if ag == nil {
		ag = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   fmt.Sprintf("%s: pending message delivery", canonical),
			Messages:  []AgentMessage{},
			UpdatedAt: now,
		}
		ks.setAgentStatus(canonical, ag)
	}
	if ag.Messages == nil {
		ag.Messages = []AgentMessage{}
	}
	ag.Messages = append(ag.Messages, msg)
	notif := AgentNotification{
		ID:        fmt.Sprintf("%s-%d", canonical, now.UnixNano()),
		Type:      NotifTypeMessage,
		Title:     fmt.Sprintf("New message from %s", senderName),
		Body:      truncateLine(message, 120),
		From:      senderName,
		Timestamp: now,
	}
	ag.Notifications = append(ag.Notifications, notif)
	pruneNotifications(ag)
	ks.UpdatedAt = now
	s.saveSpace(ks) //nolint:errcheck
	s.mu.Unlock()

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventMsgDelivered, Space: spaceName, Agent: canonical,
		Msg:    fmt.Sprintf("internal message from %s delivered", senderName),
		Fields: map[string]string{"sender": senderName}})
	s.broadcastSSE(spaceName, canonical, "message", senderName)
}

// pruneNotifications keeps at most 20 notifications per agent.
// Oldest read notifications are dropped first; if still over limit, oldest unread are dropped.
func pruneNotifications(ag *AgentUpdate) {
	const maxNotifications = 20
	if len(ag.Notifications) <= maxNotifications {
		return
	}
	// Separate into unread and read, preserving order (oldest first).
	unread := make([]AgentNotification, 0)
	read := make([]AgentNotification, 0)
	for _, n := range ag.Notifications {
		if !n.Read {
			unread = append(unread, n)
		} else {
			read = append(read, n)
		}
	}
	// Fill up to maxNotifications: unread take priority, then most-recent read.
	readSlots := maxNotifications - len(unread)
	if readSlots < 0 {
		// More unread than limit: keep newest unread only.
		ag.Notifications = unread[len(unread)-maxNotifications:]
		return
	}
	if len(read) > readSlots {
		read = read[len(read)-readSlots:]
	}
	ag.Notifications = append(unread, read...)
}
