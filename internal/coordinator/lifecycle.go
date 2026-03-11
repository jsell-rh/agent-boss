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
		for name, rec := range ks.Agents {
			if rec == nil || rec.Status == nil { continue }
			agent := rec.Status
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
		for name, rec := range ks.Agents {
			if rec == nil || rec.Status == nil { continue }
			agent := rec.Status
			snap := snapshotFromAgent(spaceName, name, agent)
			if err := s.appendSnapshot(snap); err != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] warning: failed to append liveness snapshot: %v", spaceName, name, err))
			}
		}
	}
}

// spawnRequest is the optional body for POST /spaces/{space}/agent/{name}/spawn.
type spawnRequest struct {
	SessionID      string `json:"session_id,omitempty"`      // defaults to agent name
	Command        string `json:"command,omitempty"`         // defaults to "claude"; --dangerously-skip-permissions applied via global server toggle
	Width          int    `json:"width,omitempty"`           // tmux window width, default 220
	Height         int    `json:"height,omitempty"`          // tmux window height, default 50
	Backend        string `json:"backend,omitempty"`         // "tmux" (default) or "ambient"
	InitialMessage string `json:"initial_message,omitempty"` // first message queued to the agent after spawn
	TaskID         string `json:"task_id,omitempty"`         // optional: set assigned_to on this task to the spawned agent
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

	spawnerName := r.Header.Get("X-Agent-Name")
	sessionID, backendName, _, err := s.spawnAgentService(spaceName, agentName, req, spawnerName)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"agent":      agentName,
		"session_id": sessionID,
		"space":      spaceName,
		"backend":    backendName,
	})
}

// handleAgentStop handles POST /spaces/{space}/agent/{name}/stop.
// Kills the agent's session and marks the agent as done.
func (s *Server) handleAgentStop(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	canonical, err := s.stopAgentService(spaceName, agentName)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"agent": canonical,
	})
}

// handleAgentInterrupt handles POST /spaces/{space}/agent/{name}/interrupt.
// Sends an interrupt (Escape key for Claude Code) to the agent's session without killing it.
func (s *Server) handleAgentInterrupt(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
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
	agentStatus, exists := ks.agentStatusOk(canonical)
	var sessionName string
	if exists {
		sessionName = agentStatus.SessionID
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("agent %q not found", agentName), http.StatusNotFound)
		return
	}
	if isNonSessionAgent(agentStatus) {
		nonSessionLifecycleError(w, agentStatus.Registration.AgentType)
		return
	}
	if sessionName == "" {
		http.Error(w, fmt.Sprintf("agent %q has no registered session", canonical), http.StatusBadRequest)
		return
	}

	backend := s.backendFor(agentStatus)
	if !backend.SessionExists(sessionName) {
		http.Error(w, fmt.Sprintf("session %q not found", sessionName), http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	if err := backend.Interrupt(ctx, sessionName); err != nil {
		http.Error(w, fmt.Sprintf("interrupt session: %v", err), http.StatusInternalServerError)
		return
	}

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentStopped, Space: spaceName, Agent: canonical,
		Msg:    fmt.Sprintf("interrupted (Escape sent to session %q)", sessionName),
		Fields: map[string]string{"session_id": sessionName}})
	s.broadcastSSE(spaceName, canonical, "agent_interrupted", canonical)

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

	sessionID, canonical, err := s.restartAgentService(spaceName, agentName, req)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"agent":      canonical,
		"session_id": sessionID,
	})
}

// lifecycleErr is a structured error returned by lifecycle service methods.
// HTTP handlers inspect StatusCode to produce the correct HTTP response.
type lifecycleErr struct {
	StatusCode int
	JSONBody   bool // if true, write JSON {"error": msg}; else plain text
	Msg        string
}

func (e *lifecycleErr) Error() string { return e.Msg }

// writeLifecycleError writes the appropriate HTTP error response for a lifecycleErr.
func writeLifecycleError(w http.ResponseWriter, err error) {
	if le, ok := err.(*lifecycleErr); ok {
		if le.JSONBody {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(le.StatusCode)
			json.NewEncoder(w).Encode(map[string]string{"error": le.Msg})
		} else {
			http.Error(w, le.Msg, le.StatusCode)
		}
	} else {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// spawnAgentService contains the core business logic for spawning an agent.
// spawnerName is the identity making the request (used to set the parent relationship).
func (s *Server) spawnAgentService(spaceName, agentName string, req spawnRequest, spawnerName string) (sessionID, backendName, canonical string, retErr error) {
	// Apply AgentConfig defaults (unless overridden in req body).
	var spawnWorkDir string
	var spawnRepos []SessionRepo
	var spawnInitialPrompt string
	var spawnPersonas []PersonaRef
	if existingKS, hasKS := s.getSpace(spaceName); hasKS {
		s.mu.RLock()
		cfgCanonical := resolveAgentName(existingKS, agentName)
		if cfg := existingKS.agentConfig(cfgCanonical); cfg != nil {
			if req.Backend == "" && cfg.Backend != "" {
				req.Backend = cfg.Backend
			}
			if req.Command == "" && cfg.Command != "" {
				req.Command = cfg.Command
			}
			spawnWorkDir = cfg.WorkDir
			spawnRepos = cfg.Repos
			spawnInitialPrompt = cfg.InitialPrompt
			spawnPersonas = cfg.Personas
		}
		s.mu.RUnlock()
	}
	_ = spawnPersonas // personas are embedded in buildIgnitionText

	backend := s.backendByName(req.Backend)
	sessionName := req.SessionID
	if sessionName == "" {
		sessionName = tmuxDefaultSession(spaceName, agentName)
	}

	// If the agent already exists with a non-session registration, reject the spawn.
	if existingKS, ok := s.getSpace(spaceName); ok {
		s.mu.RLock()
		can := resolveAgentName(existingKS, agentName)
		existingAgent := existingKS.agentStatus(can)
		s.mu.RUnlock()
		if isNonSessionAgent(existingAgent) {
			return "", "", "", &lifecycleErr{
				StatusCode: http.StatusUnprocessableEntity, JSONBody: true,
				Msg: fmt.Sprintf("lifecycle management via session backend is not available for agent_type %q; manage your agent process externally", existingAgent.Registration.AgentType),
			}
		}
	}

	// For tmux, check if session already exists. Ambient generates its own IDs.
	if backend.Name() == "tmux" && backend.SessionExists(sessionName) {
		return "", "", "", &lifecycleErr{StatusCode: http.StatusConflict, Msg: fmt.Sprintf("session %q already exists", sessionName)}
	}

	ctx := context.Background()
	spawnCommand := req.Command
	if backend.Name() == "tmux" && s.allowSkipPermissions && spawnCommand == "" {
		spawnCommand = "claude --dangerously-skip-permissions"
	}
	var createOpts SessionCreateOpts
	if backend.Name() == "ambient" {
		createOpts = SessionCreateOpts{
			SessionID: sessionName,
			Command:   req.Command,
			BackendOpts: AmbientCreateOpts{
				DisplayName: agentName,
				Repos:       spawnRepos,
			},
		}
	} else {
		createOpts = SessionCreateOpts{
			SessionID: sessionName,
			Command:   spawnCommand,
			BackendOpts: TmuxCreateOpts{
				Width:                req.Width,
				Height:               req.Height,
				WorkDir:              spawnWorkDir,
				MCPServerURL:         s.localURL(),
				AllowSkipPermissions: s.allowSkipPermissions,
			},
		}
	}

	sessionID, retErr = backend.CreateSession(ctx, createOpts)
	if retErr != nil {
		return "", "", "", &lifecycleErr{StatusCode: http.StatusInternalServerError, Msg: fmt.Sprintf("create session: %v", retErr)}
	}

	// Register session on the agent record.
	ks := s.getOrCreateSpace(spaceName)
	s.mu.Lock()
	canonical = resolveAgentName(ks, agentName)
	agent := ks.agentStatus(canonical)
	if agent == nil {
		agent = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   fmt.Sprintf("%s: spawned", agentName),
			UpdatedAt: time.Now().UTC(),
		}
		ks.setAgentStatus(canonical, agent)
	}
	agent.SessionID = sessionID
	agent.BackendType = backend.Name()

	// Set Parent from spawner identity, if not already set.
	if spawnerName != "" && !strings.EqualFold(spawnerName, agentName) && agent.Parent == "" {
		agent.Parent = resolveAgentName(ks, spawnerName)
		rebuildChildren(ks)
	}

	if saveErr := s.saveSpace(ks); saveErr != nil {
		s.mu.Unlock()
		s.emit(DomainEvent{Level: LevelError, EventType: EventServerError, Space: spaceName, Agent: agentName,
			Msg: fmt.Sprintf("spawn: save failed: %v", saveErr)})
	} else {
		s.mu.Unlock()
	}

	backendName = backend.Name()
	s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentSpawned, Space: spaceName, Agent: agentName,
		Msg:    fmt.Sprintf("spawned in session \"%s\" (backend: %s)", sessionID, backendName),
		Fields: map[string]string{"session_id": sessionID, "backend": backendName}})
	s.broadcastSSE(spaceName, agentName, "agent_spawned", agentName)

	initialMsg := req.InitialMessage
	cfgInitialPrompt := spawnInitialPrompt
	spawnerIdentity := spawnerName
	if spawnerIdentity == "" {
		spawnerIdentity = "boss"
	}

	if req.TaskID != "" {
		caller := spawnerName
		if caller == "" {
			caller = "boss"
		}
		s.assignTaskToAgent(spaceName, req.TaskID, canonical, caller)
	}

	go func() {
		if ab, ok := backend.(*AmbientSessionBackend); ok {
			pollCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := ab.waitForRunning(pollCtx, sessionID, 60*time.Second); err != nil {
				s.logEvent(fmt.Sprintf("[%s/%s] spawn: session did not reach running state: %v", spaceName, agentName, err))
				return
			}
		} else {
			time.Sleep(5 * time.Second)
		}
		s.mu.RLock()
		ignitePrompt := s.buildIgnitionText(spaceName, agentName, sessionID)
		s.mu.RUnlock()
		if err := backend.SendInput(sessionID, ignitePrompt); err != nil {
			s.emit(DomainEvent{Level: LevelWarn, EventType: EventAgentSpawned, Space: spaceName, Agent: agentName,
				Msg: fmt.Sprintf("spawn: ignite send failed: %v (fetch manually: curl %s/spaces/%s/ignition/%s)", err, s.localURL(), spaceName, agentName)})
		}
		if initialMsg != "" {
			s.deliverInternalMessage(spaceName, agentName, spawnerIdentity, initialMsg)
		}
		if cfgInitialPrompt != "" {
			s.deliverInternalMessage(spaceName, agentName, "boss", cfgInitialPrompt)
		}
	}()

	return sessionID, backendName, canonical, nil
}

// stopAgentService contains the core business logic for stopping an agent.
func (s *Server) stopAgentService(spaceName, agentName string) (canonical string, retErr error) {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		return "", &lifecycleErr{StatusCode: http.StatusNotFound, Msg: fmt.Sprintf("space %q not found", spaceName)}
	}

	s.mu.RLock()
	canonical = resolveAgentName(ks, agentName)
	agent, exists := ks.agentStatusOk(canonical)
	var sessionName string
	if exists {
		sessionName = agent.SessionID
	}
	s.mu.RUnlock()

	if !exists {
		return "", &lifecycleErr{StatusCode: http.StatusNotFound, Msg: fmt.Sprintf("agent %q not found", agentName)}
	}
	if isNonSessionAgent(agent) {
		return "", &lifecycleErr{StatusCode: http.StatusUnprocessableEntity, JSONBody: true,
			Msg: fmt.Sprintf("lifecycle management via session backend is not available for agent_type %q; manage your agent process externally", agent.Registration.AgentType)}
	}
	if sessionName == "" {
		return "", &lifecycleErr{StatusCode: http.StatusBadRequest, Msg: fmt.Sprintf("agent %q has no registered session", canonical)}
	}

	backend := s.backendFor(agent)
	if !backend.SessionExists(sessionName) {
		return "", &lifecycleErr{StatusCode: http.StatusNotFound, Msg: fmt.Sprintf("session %q not found", sessionName)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	if err := backend.KillSession(ctx, sessionName); err != nil {
		return "", &lifecycleErr{StatusCode: http.StatusInternalServerError, Msg: fmt.Sprintf("kill session: %v", err)}
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

	return canonical, nil
}

// restartAgentService contains the core business logic for restarting an agent.
func (s *Server) restartAgentService(spaceName, agentName string, req spawnRequest) (sessionID, canonical string, retErr error) {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		return "", "", &lifecycleErr{StatusCode: http.StatusNotFound, Msg: fmt.Sprintf("space %q not found", spaceName)}
	}

	s.mu.RLock()
	canonical = resolveAgentName(ks, agentName)
	agent, exists := ks.agentStatusOk(canonical)
	var oldSession string
	if exists {
		oldSession = agent.SessionID
	}
	// Load AgentConfig to restore cwd, command, and initial_prompt on restart.
	var restartWorkDir string
	var restartInitialPrompt string
	if cfg := ks.agentConfig(canonical); cfg != nil {
		restartWorkDir = cfg.WorkDir
		restartInitialPrompt = cfg.InitialPrompt
		if req.Command == "" && cfg.Command != "" {
			req.Command = cfg.Command
		}
	}
	s.mu.RUnlock()

	command := req.Command
	if command == "" {
		command = "claude --dangerously-skip-permissions"
	}

	if !exists {
		return "", "", &lifecycleErr{StatusCode: http.StatusNotFound, Msg: fmt.Sprintf("agent %q not found", agentName)}
	}
	if isNonSessionAgent(agent) {
		return "", "", &lifecycleErr{StatusCode: http.StatusUnprocessableEntity, JSONBody: true,
			Msg: fmt.Sprintf("lifecycle management via session backend is not available for agent_type %q; manage your agent process externally", agent.Registration.AgentType)}
	}
	if oldSession == "" {
		return "", "", &lifecycleErr{StatusCode: http.StatusBadRequest, Msg: fmt.Sprintf("agent %q has no registered session", canonical)}
	}

	backend := s.backendFor(agent)

	// Stop the existing session.
	if backend.SessionExists(oldSession) {
		ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
		if err := backend.KillSession(ctx, oldSession); err != nil {
			cancel()
			return "", "", &lifecycleErr{StatusCode: http.StatusInternalServerError, Msg: fmt.Sprintf("kill existing session: %v", err)}
		}
		cancel()
		s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentRestarted, Space: spaceName, Agent: canonical,
			Msg: fmt.Sprintf("restart: killed old session %q", oldSession)})
		time.Sleep(1 * time.Second)
	}

	// Clear the session reference so spawn can proceed.
	s.mu.Lock()
	agent.SessionID = ""
	s.mu.Unlock()

	// Create new session.
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
			BackendOpts: TmuxCreateOpts{
				WorkDir:              restartWorkDir,
				MCPServerURL:         s.localURL(),
				AllowSkipPermissions: s.allowSkipPermissions,
			},
		}
	}

	ctx2 := context.Background()
	sessionID, retErr = backend.CreateSession(ctx2, createOpts)
	if retErr != nil {
		return "", "", &lifecycleErr{StatusCode: http.StatusInternalServerError, Msg: fmt.Sprintf("create new session: %v", retErr)}
	}

	s.mu.Lock()
	agent.SessionID = sessionID
	agent.Status = StatusIdle
	agent.Summary = fmt.Sprintf("%s: restarted", canonical)
	agent.UpdatedAt = time.Now().UTC()
	// Re-pin persona versions so the agent gets the latest prompts.
	if cfg := ks.agentConfig(canonical); cfg != nil && len(cfg.Personas) > 0 {
		cfg.Personas = s.resolvePersonaRefs(cfg.Personas)
	}
	s.saveSpace(ks)
	s.mu.Unlock()

	s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentRestarted, Space: spaceName, Agent: canonical,
		Msg:    fmt.Sprintf("restarted in new session %q", sessionID),
		Fields: map[string]string{"session_id": sessionID}})
	s.broadcastSSE(spaceName, canonical, "agent_restarted", canonical)

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
		s.mu.RLock()
		igniteText := s.buildIgnitionText(spaceName, canonical, sessionID)
		s.mu.RUnlock()
		if err := backend.SendInput(sessionID, igniteText); err != nil {
			s.emit(DomainEvent{Level: LevelWarn, EventType: EventAgentRestarted, Space: spaceName, Agent: canonical,
				Msg: fmt.Sprintf("restart: ignite send failed: %v", err)})
		}
		if restartInitialPrompt != "" {
			s.deliverInternalMessage(spaceName, canonical, "boss", restartInitialPrompt)
		}
	}()

	return sessionID, canonical, nil
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
	agent, exists := ks.agentStatusOk(canonical)
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

// handleRestartAll handles POST /spaces/{space}/restart-all.
// Restarts all agents in the space that have status active/idle/done and a registered session.
// Restarts are sequenced with a 2s delay between each to avoid overwhelming the system.
func (s *Server) handleRestartAll(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	type target struct {
		name    string
		session string
	}
	var targets []target

	s.mu.RLock()
	for name, rec := range ks.Agents {
		if rec == nil || rec.Status == nil {
			continue
		}
		agent := rec.Status
		if agent.SessionID == "" {
			continue
		}
		switch agent.Status {
		case StatusActive, StatusIdle, StatusDone:
			targets = append(targets, target{name: name, session: agent.SessionID})
		}
	}
	s.mu.RUnlock()

	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.name
	}

	// Fire off sequential restarts in a goroutine so this handler returns immediately.
	go func() {
		for i, t := range targets {
			if i > 0 {
				time.Sleep(2 * time.Second)
			}
			// Reuse the per-agent restart handler via a synthetic HTTP round-trip would be
			// complex; replicate the core kill-and-recreate logic directly.
			s.mu.RLock()
			agent, exists := ks.agentStatusOk(t.name)
			var cfg *AgentConfig
			if exists {
				cfg = ks.agentConfig(t.name)
			}
			s.mu.RUnlock()
			if !exists || agent.SessionID == "" {
				continue
			}
			backend := s.backendFor(agent)

			// Kill existing session
			ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
			_ = backend.KillSession(ctx, agent.SessionID)
			cancel()
			time.Sleep(1 * time.Second)

			// Determine work dir and command from stored config
			workDir := ""
			command := "claude --dangerously-skip-permissions"
			initialPrompt := ""
			if cfg != nil {
				workDir = cfg.WorkDir
				if cfg.Command != "" {
					command = cfg.Command
				}
				initialPrompt = cfg.InitialPrompt
			}

			// Create new session
			newSession := tmuxDefaultSession(spaceName, t.name)
			if backend.SessionExists(newSession) {
				newSession = newSession + "-new"
			}
			createOpts := SessionCreateOpts{
				SessionID: newSession,
				Command:   command,
				BackendOpts: TmuxCreateOpts{
					WorkDir:              workDir,
					MCPServerURL:         s.localURL(),
					AllowSkipPermissions: s.allowSkipPermissions,
				},
			}
			sessionID, err := backend.CreateSession(context.Background(), createOpts)
			if err != nil {
				s.emit(DomainEvent{Level: LevelWarn, EventType: EventAgentRestarted, Space: spaceName, Agent: t.name,
					Msg: fmt.Sprintf("restart-all: failed to create session: %v", err)})
				continue
			}

			s.mu.Lock()
			agent.SessionID = sessionID
			agent.Status = StatusIdle
			agent.Summary = fmt.Sprintf("%s: restarted (fleet restart)", t.name)
			agent.UpdatedAt = time.Now().UTC()
			s.saveSpace(ks) //nolint:errcheck
			s.mu.Unlock()

			s.emit(DomainEvent{Level: LevelInfo, EventType: EventAgentRestarted, Space: spaceName, Agent: t.name,
				Msg:    fmt.Sprintf("restart-all: restarted in session %q", sessionID),
				Fields: map[string]string{"session_id": sessionID}})
			s.broadcastSSE(spaceName, t.name, "agent_restarted", t.name)

			// Send ignition asynchronously
			go func(agentName, sid, prompt string) {
				time.Sleep(5 * time.Second)
				s.mu.RLock()
				igniteText := s.buildIgnitionText(spaceName, agentName, sid)
				s.mu.RUnlock()
				if err := backend.SendInput(sid, igniteText); err != nil {
					s.emit(DomainEvent{Level: LevelWarn, EventType: EventAgentRestarted, Space: spaceName, Agent: agentName,
						Msg: fmt.Sprintf("restart-all: ignite failed: %v", err)})
				}
				if prompt != "" {
					s.deliverInternalMessage(spaceName, agentName, "boss", prompt)
				}
			}(t.name, sessionID, initialPrompt)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"agents": names,
		"count":  len(names),
	})
}
