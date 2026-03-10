package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// isNonSessionAgent returns true if the agent has an explicit registration with an
// agent_type that is not session-based (i.e., not "tmux" or ""). Agents without a
// registration are considered potentially session-managed (backward compatible).
func isNonSessionAgent(agent *AgentUpdate) bool {
	if agent == nil || agent.Registration == nil {
		return false
	}
	t := agent.Registration.AgentType
	return t != "" && t != "tmux" && t != "ambient"
}

// nonSessionLifecycleError writes an HTTP 422 response explaining that session-based
// lifecycle management is not available for agents whose agent_type is not session-based.
func nonSessionLifecycleError(w http.ResponseWriter, agentType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	json.NewEncoder(w).Encode(map[string]string{
		"error": fmt.Sprintf(
			"lifecycle management via session backend is not available for agent_type %q; manage your agent process externally",
			agentType,
		),
	})
}

// inferAgentStatus derives a human-readable inferred status string from session observations.
// This is stored as InferredStatus on the agent record and does not override self-reported Status.
func inferAgentStatus(exists, idle, needsApproval bool) string {
	if !exists {
		return "session_missing"
	}
	if needsApproval {
		return "waiting_approval"
	}
	if idle {
		return "idle"
	}
	return "working"
}

// checkStaleness iterates all agents and marks those that have not self-reported
// within StalenessThreshold as stale. Called periodically by the liveness loop.
func (s *Server) checkStaleness() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	for spaceName, ks := range s.spaces {
		changed := false
		for name, agent := range ks.Agents {
			// Only mark active/blocked agents as stale — done/idle are expected to be quiet.
			if agent.Status == StatusDone || agent.Status == StatusIdle {
				if agent.Stale {
					agent.Stale = false
					changed = true
				}
				continue
			}
			wasStale := agent.Stale
			agent.Stale = now.Sub(agent.UpdatedAt) > s.stalenessThreshold
			if agent.Stale != wasStale {
				changed = true
				if agent.Stale {
					s.emit(DomainEvent{Level: LevelWarn, EventType: EventAgentStale, Space: spaceName, Agent: name,
						Msg:    fmt.Sprintf("marked stale (last update: %s ago)", now.Sub(agent.UpdatedAt).Round(time.Second)),
						Fields: map[string]string{"idle_duration": now.Sub(agent.UpdatedAt).Round(time.Second).String()}})
				} else {
					s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentStaleCleared, Space: spaceName, Agent: name,
						Msg: "staleness cleared"})
				}
			}
		}
		if changed {
			s.saveSpace(ks) //nolint:errcheck
		}
		// Record a periodic snapshot for all agents so history captures liveness ticks.
		for name, agent := range ks.Agents {
			snap := snapshotFromAgent(spaceName, name, agent)
			if err := s.appendSnapshot(snap); err != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] warning: failed to append liveness snapshot: %v", spaceName, name, err))
			}
		}
	}
}

// spawnRequest is the optional body for POST /spaces/{space}/agent/{name}/spawn.
type spawnRequest struct {
	SessionID string `json:"session_id,omitempty"` // defaults to agent name
	Command   string `json:"command,omitempty"`    // defaults to "claude --dangerously-skip-permissions"
	Width     int    `json:"width,omitempty"`      // tmux window width, default 220
	Height    int    `json:"height,omitempty"`     // tmux window height, default 50
	Backend   string `json:"backend,omitempty"`    // "tmux" (default) or "ambient"
}

// handleAgentSpawn handles POST /spaces/{space}/agent/{name}/spawn.
// Creates a session via the backend, launches the agent command, and sends the ignite prompt.
func (s *Server) handleAgentSpawn(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req spawnRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
	}

	backendName := req.Backend
	backend := s.backendByName(backendName)

	sessionName := req.SessionID
	if sessionName == "" {
		sessionName = tmuxDefaultSession(spaceName, agentName)
	}

	// If the agent already exists with a non-session registration, reject the spawn.
	if existingKS, ok := s.getSpace(spaceName); ok {
		s.mu.RLock()
		canonical := resolveAgentName(existingKS, agentName)
		existingAgent := existingKS.Agents[canonical]
		s.mu.RUnlock()
		if isNonSessionAgent(existingAgent) {
			nonSessionLifecycleError(w, existingAgent.Registration.AgentType)
			return
		}
	}

	// For tmux, check if session already exists. Ambient generates its own IDs.
	if backend.Name() == "tmux" && backend.SessionExists(sessionName) {
		http.Error(w, fmt.Sprintf("session %q already exists", sessionName), http.StatusConflict)
		return
	}

	ctx := context.Background()
	var createOpts SessionCreateOpts
	if backend.Name() == "ambient" {
		createOpts = SessionCreateOpts{
			SessionID: sessionName,
			Command:   req.Command,
			BackendOpts: AmbientCreateOpts{
				DisplayName: agentName,
			},
		}
	} else {
		createOpts = SessionCreateOpts{
			SessionID: sessionName,
			Command:   req.Command,
			BackendOpts: TmuxCreateOpts{
				Width:  req.Width,
				Height: req.Height,
			},
		}
	}

	sessionID, err := backend.CreateSession(ctx, createOpts)
	if err != nil {
		http.Error(w, fmt.Sprintf("create session: %v", err), http.StatusInternalServerError)
		return
	}

	// Register session on the agent record
	ks := s.getOrCreateSpace(spaceName)
	s.mu.Lock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	if !exists {
		agent = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   fmt.Sprintf("%s: spawned", agentName),
			UpdatedAt: time.Now().UTC(),
		}
		ks.Agents[canonical] = agent
	}
	agent.SessionID = sessionID
	agent.BackendType = backend.Name()

	// Set Parent from the spawner's identity (X-Agent-Name header), if not already set.
	spawnerName := r.Header.Get("X-Agent-Name")
	if spawnerName != "" && !strings.EqualFold(spawnerName, agentName) && agent.Parent == "" {
		agent.Parent = resolveAgentName(ks, spawnerName)
		rebuildChildren(ks)
	}

	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		s.emit(DomainEvent{Level: LevelError, EventType: EventServerError, Space: spaceName, Agent: agentName,
			Msg: fmt.Sprintf("spawn: save failed: %v", err)})
	} else {
		s.mu.Unlock()
	}

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentSpawned, Space: spaceName, Agent: agentName,
		Msg:    fmt.Sprintf("spawned in session \"%s\" (backend: %s)", sessionID, backend.Name()),
		Fields: map[string]string{"session_id": sessionID, "backend": backend.Name()}})
	s.broadcastSSE(spaceName, agentName, "agent_spawned", agentName)

	// Send ignite asynchronously after agent has time to initialize
	go func() {
		if ab, ok := backend.(*AmbientSessionBackend); ok {
			// Poll until the ambient session is running before sending ignite.
			pollCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := ab.waitForRunning(pollCtx, sessionID, 60*time.Second); err != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] spawn: session did not reach running state: %v", spaceName, agentName, err))
				return
			}
		} else {
			time.Sleep(5 * time.Second)
		}
		igniteCmd := fmt.Sprintf(`/boss.ignite "%s" "%s"`, agentName, spaceName)
		if err := backend.SendInput(sessionID, igniteCmd); err != nil {
			s.emit(DomainEvent{Level: LevelWarn, EventType: EventAgentSpawned, Space: spaceName, Agent: agentName,
				Msg: fmt.Sprintf("spawn: ignite send failed: %v (ignite manually)", err)})
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"agent":      agentName,
		"session_id": sessionID,
		"space":      spaceName,
		"backend":    backend.Name(),
	})
}

// handleAgentStop handles POST /spaces/{space}/agent/{name}/stop.
// Kills the agent's session and marks the agent as done.
func (s *Server) handleAgentStop(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var sessionName string
	if exists {
		sessionName = agent.SessionID
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("agent %q not found", agentName), http.StatusNotFound)
		return
	}
	if isNonSessionAgent(agent) {
		nonSessionLifecycleError(w, agent.Registration.AgentType)
		return
	}
	if sessionName == "" {
		http.Error(w, fmt.Sprintf("agent %q has no registered session", canonical), http.StatusBadRequest)
		return
	}

	backend := s.backendFor(agent)
	if !backend.SessionExists(sessionName) {
		http.Error(w, fmt.Sprintf("session %q not found", sessionName), http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	if err := backend.KillSession(ctx, sessionName); err != nil {
		http.Error(w, fmt.Sprintf("kill session: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	agent.Status = StatusDone
	agent.Summary = fmt.Sprintf("%s: stopped", canonical)
	agent.SessionID = ""
	agent.UpdatedAt = time.Now().UTC()
	s.saveSpace(ks)
	s.mu.Unlock()

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentStopped, Space: spaceName, Agent: canonical,
		Msg:    fmt.Sprintf("stopped (session %q killed)", sessionName),
		Fields: map[string]string{"session_id": sessionName}})
	s.broadcastSSE(spaceName, canonical, "agent_stopped", canonical)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"agent": canonical,
	})
}

// handleAgentRestart handles POST /spaces/{space}/agent/{name}/restart.
// Kills the existing session and spawns a new one.
func (s *Server) handleAgentRestart(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req spawnRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
	}
	command := req.Command
	if command == "" {
		command = "claude --dangerously-skip-permissions"
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var oldSession string
	if exists {
		oldSession = agent.SessionID
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("agent %q not found", agentName), http.StatusNotFound)
		return
	}
	if isNonSessionAgent(agent) {
		nonSessionLifecycleError(w, agent.Registration.AgentType)
		return
	}
	if oldSession == "" {
		http.Error(w, fmt.Sprintf("agent %q has no registered session", canonical), http.StatusBadRequest)
		return
	}

	backend := s.backendFor(agent)

	// Stop the existing session
	if oldSession != "" && backend.SessionExists(oldSession) {
		ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
		if err := backend.KillSession(ctx, oldSession); err != nil {
			cancel()
			http.Error(w, fmt.Sprintf("kill existing session: %v", err), http.StatusInternalServerError)
			return
		}
		cancel()
		s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentRestarted, Space: spaceName, Agent: canonical,
			Msg: fmt.Sprintf("restart: killed old session %q", oldSession)})
		time.Sleep(1 * time.Second)
	}

	// Clear the session reference so spawn can proceed
	s.mu.Lock()
	agent.SessionID = ""
	s.mu.Unlock()

	// Create new session
	var createOpts SessionCreateOpts
	if backend.Name() == "ambient" {
		createOpts = SessionCreateOpts{
			Command: command,
			BackendOpts: AmbientCreateOpts{
				DisplayName: canonical,
			},
		}
	} else {
		newSession := tmuxDefaultSession(spaceName, canonical)
		if backend.SessionExists(newSession) {
			newSession = newSession + "-new"
		}
		createOpts = SessionCreateOpts{
			SessionID: newSession,
			Command:   command,
		}
	}

	ctx2 := context.Background()
	sessionID, err := backend.CreateSession(ctx2, createOpts)
	if err != nil {
		http.Error(w, fmt.Sprintf("create new session: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	agent.SessionID = sessionID
	agent.Status = StatusIdle
	agent.Summary = fmt.Sprintf("%s: restarted", canonical)
	agent.UpdatedAt = time.Now().UTC()
	s.saveSpace(ks)
	s.mu.Unlock()

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentRestarted, Space: spaceName, Agent: canonical,
		Msg:    fmt.Sprintf("restarted in new session %q", sessionID),
		Fields: map[string]string{"session_id": sessionID}})
	s.broadcastSSE(spaceName, canonical, "agent_restarted", canonical)

	// Send ignite asynchronously after agent has time to initialize
	go func() {
		if ab, ok := backend.(*AmbientSessionBackend); ok {
			pollCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := ab.waitForRunning(pollCtx, sessionID, 60*time.Second); err != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] restart: session did not reach running state: %v", spaceName, canonical, err))
				return
			}
		} else {
			time.Sleep(5 * time.Second)
		}
		igniteCmd := fmt.Sprintf(`/boss.ignite "%s" "%s"`, canonical, spaceName)
		if err := backend.SendInput(sessionID, igniteCmd); err != nil {
			s.emit(DomainEvent{Level: LevelWarn, EventType: EventAgentRestarted, Space: spaceName, Agent: canonical,
				Msg: fmt.Sprintf("restart: ignite send failed: %v", err)})
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"agent":      canonical,
		"session_id": sessionID,
	})
}

// introspectResponse is returned by GET /spaces/{space}/agent/{name}/introspect.
type introspectResponse struct {
	Agent          string    `json:"agent"`
	Space          string    `json:"space"`
	SessionID      string    `json:"session_id,omitempty"`
	TmuxAvailable  bool      `json:"tmux_available"`
	SessionExists  bool      `json:"session_exists"`
	Idle           bool      `json:"idle"`
	NeedsApproval  bool      `json:"needs_approval"`
	ToolName       string    `json:"tool_name,omitempty"`
	PromptText     string    `json:"prompt_text,omitempty"`
	Lines          []string  `json:"lines"`
	CapturedAt     time.Time `json:"captured_at"`
}

// handleAgentIntrospect handles GET /spaces/{space}/agent/{name}/introspect.
// Captures the recent session output and returns it as JSON.
func (s *Server) handleAgentIntrospect(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var sessionName string
	if exists {
		sessionName = agent.SessionID
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("agent %q not found", agentName), http.StatusNotFound)
		return
	}

	backend := s.backendFor(agent)

	resp := introspectResponse{
		Agent:         canonical,
		Space:         spaceName,
		SessionID:     sessionName,
		TmuxAvailable: !isNonSessionAgent(agent) && backend.Available(),
		Lines:         []string{},
		CapturedAt:    time.Now().UTC(),
	}

	if sessionName != "" && backend.SessionExists(sessionName) {
		resp.SessionExists = true
		resp.Idle = backend.IsIdle(sessionName)
		if lines, err := backend.CaptureOutput(sessionName, 50); err == nil {
			resp.Lines = lines
		}
		if !resp.Idle {
			approval := backend.CheckApproval(sessionName)
			resp.NeedsApproval = approval.NeedsApproval
			resp.ToolName = approval.ToolName
			resp.PromptText = approval.PromptText
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
