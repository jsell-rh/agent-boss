package coordinator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// handleSpaceMessageList serves GET /spaces/{space}/messages.
// Returns messages for every agent in the space, indexed by agent name.
//
// Query params:
//   - limit=N   — max messages per agent (default 50, max 200)
//   - before=T  — only return messages with timestamp < T (RFC3339); used for pagination
//
// Response: { agentName: { messages: [...], has_more: bool } }
func (s *Server) handleSpaceMessageList(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	const defaultLimit = 50
	const maxLimit = 200
	limit := defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxLimit {
				n = maxLimit
			}
			limit = n
		}
	}
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			before = t
		} else if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = t
		}
	}

	s.mu.RLock()
	type agentMsgs struct {
		Messages []AgentMessage `json:"messages"`
		HasMore  bool           `json:"has_more"`
	}
	result := make(map[string]agentMsgs, len(ks.Agents))
	for name, ag := range ks.Agents {
		if ag.Status == nil {
			result[name] = agentMsgs{Messages: []AgentMessage{}}
			continue
		}
		msgs := ag.Status.Messages
		// Filter by before timestamp if provided.
		if !before.IsZero() {
			filtered := msgs[:0:0]
			for _, m := range msgs {
				if m.Timestamp.Before(before) {
					filtered = append(filtered, m)
				}
			}
			msgs = filtered
		}
		hasMore := len(msgs) > limit
		if hasMore {
			msgs = msgs[len(msgs)-limit:]
		}
		out := make([]AgentMessage, len(msgs))
		copy(out, msgs)
		result[name] = agentMsgs{Messages: out, HasMore: hasMore}
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
