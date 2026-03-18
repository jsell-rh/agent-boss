package coordinator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AgentRegistration is submitted by an agent when it registers with the coordinator.
// Registration is optional for tmux-based agents (backward compatible) but required
// for arbitrary agents that want heartbeat tracking and webhook delivery.
type AgentRegistration struct {
	// AgentType describes the transport/runtime: "tmux", "http", "cli", "script", "remote"
	AgentType string `json:"agent_type"`
	// Capabilities is a free-form list of what the agent can do: ["code", "research", "review"]
	Capabilities []string `json:"capabilities,omitempty"`
	// HeartbeatIntervalSec is how often the agent will send heartbeats (seconds).
	// 0 means no heartbeat expected. Default when omitted: 0 (no tracking).
	HeartbeatIntervalSec int `json:"heartbeat_interval_sec,omitempty"`
	// CallbackURL is the webhook URL the server will POST messages to instead of
	// relying on the agent polling /raw. Optional.
	CallbackURL string `json:"callback_url,omitempty"`
	// Metadata is arbitrary key/value info attached to the registration.
	Metadata map[string]string `json:"metadata,omitempty"`
	// Parent declares this agent's manager in the hierarchy. Optional, sticky.
	// If set, the server links this agent as a child of Parent.
	Parent string `json:"parent,omitempty"`
}

// AgentRegistrationRecord is stored server-side after an agent registers.
type AgentRegistrationRecord struct {
	Registration  AgentRegistration `json:"registration"`
	SpaceName     string            `json:"space_name"`
	AgentName     string            `json:"agent_name"`
	RegisteredAt  time.Time         `json:"registered_at"`
	LastHeartbeat time.Time         `json:"last_heartbeat"`
	Stale         bool              `json:"stale"`
}

// registrationKey returns the map key for a space+agent pair.
func registrationKey(spaceName, agentName string) string {
	return strings.ToLower(spaceName) + "/" + strings.ToLower(agentName)
}

// handleAgentRegister handles POST /spaces/{space}/agent/{name}/register.
// Agents call this once to declare their type, capabilities, heartbeat interval,
// and optional callback URL for webhook message delivery.
func (s *Server) handleAgentRegister(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	callerName := r.Header.Get("X-Agent-Name")
	if callerName == "" {
		http.Error(w, "missing X-Agent-Name header", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(callerName, agentName) {
		http.Error(w, fmt.Sprintf("agent %q cannot register as %q", callerName, agentName), http.StatusForbidden)
		return
	}

	var reg AgentRegistration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if reg.AgentType == "" {
		reg.AgentType = "unknown"
	}

	ks := s.getOrCreateSpace(spaceName)
	canonical := resolveAgentName(ks, agentName)

	// "parent" is reserved for message escalation routing.
	if strings.EqualFold(agentName, "parent") {
		http.Error(w, `"parent" is a reserved agent name`, http.StatusBadRequest)
		return
	}

	// Ensure agent exists in the space and persist registration info
	s.mu.Lock()
	agent := ks.agentStatus(canonical)
	if agent == nil {
		agent = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   canonical + ": registered",
			UpdatedAt: time.Now().UTC(),
		}
		ks.setAgentStatus(canonical, agent)
	}
	agent.Registration = &reg
	// Apply hierarchy from registration if provided.
	if reg.Parent != "" {
		if hasCycle(ks, canonical, reg.Parent) {
			s.mu.Unlock()
			http.Error(w, "cycle detected: parent assignment would create a loop", http.StatusBadRequest)
			return
		}
		agent.Parent = reg.Parent
		rebuildChildren(ks)
	}
	ks.UpdatedAt = time.Now().UTC()
	s.saveSpace(ks)
	s.mu.Unlock()

	rec := &AgentRegistrationRecord{
		Registration: reg,
		SpaceName:    spaceName,
		AgentName:    canonical,
		RegisteredAt: time.Now().UTC(),
		Stale:        false,
	}

	s.regMu.Lock()
	s.registrations[registrationKey(spaceName, canonical)] = rec
	s.regMu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] registered (type=%s, heartbeat=%ds, callback=%v)",
		spaceName, canonical, reg.AgentType, reg.HeartbeatIntervalSec, reg.CallbackURL != ""))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "registered",
		"agent":     canonical,
		"space":     spaceName,
		"agent_type": reg.AgentType,
	})
}

// handleAgentHeartbeat handles POST /spaces/{space}/agent/{name}/heartbeat.
// Registered agents call this periodically to indicate they are alive.
// The server marks the agent stale if heartbeats stop arriving within
// 2× the registered HeartbeatIntervalSec.
func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	callerName := r.Header.Get("X-Agent-Name")
	if callerName == "" {
		http.Error(w, "missing X-Agent-Name header", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(callerName, agentName) {
		http.Error(w, fmt.Sprintf("agent %q cannot send heartbeat for %q", callerName, agentName), http.StatusForbidden)
		return
	}

	// Normalize agent name using case-insensitive lookup if the space exists.
	canonical := agentName
	var ks *KnowledgeSpace
	if space, ok := s.getSpace(spaceName); ok {
		ks = space
		canonical = resolveAgentName(ks, agentName)
	}
	key := registrationKey(spaceName, canonical)

	now := time.Now().UTC()

	s.regMu.Lock()
	rec, exists := s.registrations[key]
	if !exists {
		s.regMu.Unlock()
		http.Error(w, "agent not registered; call /register first", http.StatusBadRequest)
		return
	}
	rec.LastHeartbeat = now
	rec.Stale = false
	s.regMu.Unlock()

	// Also persist heartbeat time and clear staleness on the AgentUpdate when possible.
	if ks != nil {
		s.mu.Lock()
		if agent, ok := ks.agentStatusOk(canonical); ok {
			agent.LastHeartbeat = now
			agent.HeartbeatStale = false
			ks.UpdatedAt = now
			s.saveSpace(ks)
		}
		s.mu.Unlock()
	}

	s.logEvent(fmt.Sprintf("[%s/%s] heartbeat received", spaceName, canonical))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"agent":  canonical,
	})
}

// handleAgentMessages handles GET /spaces/{space}/agent/{name}/messages.
// Query params:
//   - since: RFC3339 timestamp — only return messages after this time
//
// Returns only the messages for this agent, not the full /raw document.
// This is the efficient polling alternative to reading /raw.
func (s *Server) handleAgentMessages(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	canonical := resolveAgentName(ks, agentName)

	var since time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		var err error
		since, err = time.Parse(time.RFC3339Nano, sinceStr)
		if err != nil {
			// Also try plain RFC3339
			since, err = time.Parse(time.RFC3339, sinceStr)
			if err != nil {
				http.Error(w, fmt.Sprintf("invalid since timestamp %q: use RFC3339 format", sinceStr), http.StatusBadRequest)
				return
			}
		}
	}

	s.mu.RLock()
	agent, exists := ks.agentStatusOk(canonical)
	s.mu.RUnlock()

	if !exists {
		// Agent has no messages yet — return empty
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"agent":    canonical,
			"messages": []AgentMessage{},
			"cursor":   time.Now().UTC().Format(time.RFC3339Nano),
		})
		return
	}

	s.mu.RLock()
	allMessages := make([]AgentMessage, len(agent.Messages))
	copy(allMessages, agent.Messages)
	s.mu.RUnlock()

	// Filter by since cursor
	var filtered []AgentMessage
	for _, msg := range allMessages {
		if since.IsZero() || msg.Timestamp.After(since) {
			filtered = append(filtered, msg)
		}
	}
	if filtered == nil {
		filtered = []AgentMessage{}
	}

	// The cursor for the next poll is the latest message timestamp + 1ns,
	// or now if there were no messages.
	var cursor time.Time
	if len(filtered) > 0 {
		cursor = filtered[len(filtered)-1].Timestamp.Add(time.Nanosecond)
	} else {
		cursor = time.Now().UTC()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"agent":    canonical,
		"messages": filtered,
		"cursor":   cursor.Format(time.RFC3339Nano),
	})
}

// deliverWebhook attempts to deliver a message to an agent's registered callback URL.
// It POSTs a JSON payload and returns true on success (2xx response).
func deliverWebhook(callbackURL string, spaceName, agentName string, msg AgentMessage) bool {
	payload := map[string]interface{}{
		"event":      "message",
		"space":      spaceName,
		"agent":      agentName,
		"message_id": msg.ID,
		"sender":     msg.Sender,
		"message":    msg.Message,
		"timestamp":  msg.Timestamp.Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(callbackURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// tryWebhookDelivery checks if the agent has a registered callback URL and attempts
// delivery. Falls through silently if not registered or if the webhook fails.
// Called from handleAgentMessage after a message is stored.
func (s *Server) tryWebhookDelivery(spaceName, agentName string, msg AgentMessage) {
	key := registrationKey(spaceName, agentName)
	s.regMu.RLock()
	rec, ok := s.registrations[key]
	var callbackURL string
	if ok && rec.Registration.CallbackURL != "" {
		callbackURL = rec.Registration.CallbackURL
	}
	s.regMu.RUnlock()

	if callbackURL == "" {
		return
	}

	go func() {
		ok := deliverWebhook(callbackURL, spaceName, agentName, msg)
		if ok {
			s.logEvent(fmt.Sprintf("[%s/%s] webhook delivery succeeded (id=%s)", spaceName, agentName, msg.ID))
		} else {
			s.logEvent(fmt.Sprintf("[%s/%s] webhook delivery failed (id=%s), agent must poll /messages", spaceName, agentName, msg.ID))
		}
	}()
}

// checkHeartbeatStaleness is called from the liveness loop to mark registered
// agents as stale when they exceed 2× their expected heartbeat interval.
// It updates both the in-memory registration record and the persisted AgentUpdate.
func (s *Server) checkHeartbeatStaleness() {
	now := time.Now().UTC()

	s.regMu.Lock()
	type staleChange struct {
		spaceName, agentName string
		stale                bool
	}
	var changes []staleChange

	for _, rec := range s.registrations {
		interval := rec.Registration.HeartbeatIntervalSec
		if interval <= 0 {
			continue // no heartbeat expected
		}
		wasStale := rec.Stale
		if rec.LastHeartbeat.IsZero() {
			// Grace: don't mark stale until 2× interval after registration
			if now.Sub(rec.RegisteredAt) > time.Duration(interval*2)*time.Second {
				if !rec.Stale {
					rec.Stale = true
					s.logEvent(fmt.Sprintf("[%s/%s] marked stale (no heartbeat since registration)", rec.SpaceName, rec.AgentName))
					s.broadcastSSE(rec.SpaceName, rec.AgentName, "agent_stale", fmt.Sprintf(`{"space":%q,"agent":%q}`, rec.SpaceName, rec.AgentName))
				}
			}
		} else {
			deadline := rec.LastHeartbeat.Add(time.Duration(interval*2) * time.Second)
			if now.After(deadline) && !rec.Stale {
				rec.Stale = true
				s.logEvent(fmt.Sprintf("[%s/%s] marked stale (last heartbeat %s ago)",
					rec.SpaceName, rec.AgentName, now.Sub(rec.LastHeartbeat).Round(time.Second)))
				s.broadcastSSE(rec.SpaceName, rec.AgentName, "agent_stale", fmt.Sprintf(`{"space":%q,"agent":%q}`, rec.SpaceName, rec.AgentName))
			}
		}
		if rec.Stale != wasStale {
			changes = append(changes, staleChange{rec.SpaceName, rec.AgentName, rec.Stale})
		}
	}
	s.regMu.Unlock()

	// Sync HeartbeatStale to AgentUpdate so it shows in dashboard and persists
	if len(changes) > 0 {
		s.mu.Lock()
		for _, ch := range changes {
			ks, ok := s.spaces[ch.spaceName]
			if !ok {
				continue
			}
			canonical := resolveAgentName(ks, ch.agentName)
			if agent, ok := ks.agentStatusOk(canonical); ok {
				agent.HeartbeatStale = ch.stale
				ks.UpdatedAt = now
				s.saveSpace(ks)
			}
		}
		s.mu.Unlock()
	}
}

// GetRegistration returns the registration record for an agent, if any.
func (s *Server) GetRegistration(spaceName, agentName string) (*AgentRegistrationRecord, bool) {
	key := registrationKey(spaceName, agentName)
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	rec, ok := s.registrations[key]
	if !ok {
		return nil, false
	}
	// Return a copy
	cp := *rec
	return &cp, true
}

// handleAgentSSE handles GET /spaces/{space}/agent/{name}/events.
// It establishes an SSE stream that delivers only events relevant to this
// specific agent (messages sent to it, its status changes, stale events).
// This lets agents subscribe for push notification without polling /raw.
func (s *Server) handleAgentSSE(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Resolve canonical agent name if space exists
	canonical := agentName
	if ks, ok := s.getSpace(spaceName); ok {
		canonical = resolveAgentName(ks, agentName)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	setCORSOriginHeader(w, r)
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send an initial comment to confirm the stream is open
	fmt.Fprintf(w, ": connected to agent stream %s/%s\n\n", spaceName, canonical)
	flusher.Flush()

	// Replay missed events from ring buffer if Last-Event-ID or ?since= is provided.
	sinceID := r.Header.Get("Last-Event-ID")
	if sinceID == "" {
		sinceID = r.URL.Query().Get("since")
	}
	if sinceID != "" {
		key := spaceName + "/" + strings.ToLower(canonical)
		s.sseMu.Lock()
		buf := s.agentSSEBuf[key]
		var replay []sseEvent
		for _, ev := range buf {
			if ev.ID > sinceID {
				replay = append(replay, ev)
			}
		}
		s.sseMu.Unlock()
		if len(replay) > 0 {
			fmt.Fprintf(w, ": replaying %d missed events\n\n", len(replay))
			for _, ev := range replay {
				fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.EventType, ev.Data)
			}
			fmt.Fprintf(w, ": replay complete\n\n")
			flusher.Flush()
		}
	}

	client := &sseClient{
		ch:    make(chan []byte, 64),
		space: spaceName,
		agent: canonical,
	}
	s.sseMu.Lock()
	if s.sseBySpace[spaceName] == nil {
		s.sseBySpace[spaceName] = make(map[*sseClient]struct{})
	}
	s.sseBySpace[spaceName][client] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseBySpace[spaceName], client)
		s.sseMu.Unlock()
	}()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-client.ch:
			w.Write(msg)
			flusher.Flush()
		case t := <-keepalive.C:
			fmt.Fprintf(w, ": keepalive %s\n\n", t.UTC().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}
