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
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	entry := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
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
}

func truncateLine(s string, maxLen int) string {
	line := strings.SplitN(s, "\n", 2)[0]
	line = strings.TrimSpace(line)
	if len(line) > maxLen {
		return line[:maxLen-3] + "..."
	}
	return line
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
