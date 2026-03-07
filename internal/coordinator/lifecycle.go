package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// inferAgentStatus derives a human-readable inferred status string from tmux observations.
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
					s.logEvent(fmt.Sprintf("[%s/%s] marked stale (last update: %s ago)",
						spaceName, name, now.Sub(agent.UpdatedAt).Round(time.Second)))
				} else {
					s.logEvent(fmt.Sprintf("[%s/%s] staleness cleared", spaceName, name))
				}
			}
		}
		if changed {
			s.saveSpace(ks) //nolint:errcheck
		}
	}
}

// spawnRequest is the optional body for POST /spaces/{space}/agent/{name}/spawn.
type spawnRequest struct {
	TmuxSession string `json:"tmux_session,omitempty"` // defaults to agent name
	Command     string `json:"command,omitempty"`      // defaults to "claude --dangerously-skip-permissions"
	Width       int    `json:"width,omitempty"`        // tmux window width, default 220
	Height      int    `json:"height,omitempty"`       // tmux window height, default 50
}

// handleAgentSpawn handles POST /spaces/{space}/agent/{name}/spawn.
// Creates a tmux session, launches the agent command, and sends the ignite prompt.
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

	sessionName := req.TmuxSession
	if sessionName == "" {
		sessionName = agentName
	}
	command := req.Command
	if command == "" {
		command = "claude --dangerously-skip-permissions"
	}
	width := req.Width
	if width <= 0 {
		width = 220
	}
	height := req.Height
	if height <= 0 {
		height = 50
	}

	if tmuxSessionExists(sessionName) {
		http.Error(w, fmt.Sprintf("tmux session %q already exists", sessionName), http.StatusConflict)
		return
	}

	// Create detached tmux session
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", sessionName,
		"-x", fmt.Sprintf("%d", width), "-y", fmt.Sprintf("%d", height)).Run(); err != nil {
		http.Error(w, fmt.Sprintf("create tmux session: %v", err), http.StatusInternalServerError)
		return
	}

	// Launch agent command immediately
	time.Sleep(300 * time.Millisecond)
	if err := tmuxSendKeys(sessionName, command); err != nil {
		http.Error(w, fmt.Sprintf("launch agent command: %v", err), http.StatusInternalServerError)
		return
	}

	// Register tmux session on the agent record
	ks := s.getOrCreateSpace(spaceName)
	s.mu.Lock()
	agent, exists := ks.Agents[strings.ToLower(agentName)]
	if !exists {
		// Check canonical name
		canonical := resolveAgentName(ks, agentName)
		agent = ks.Agents[canonical]
	}
	if agent == nil {
		agent = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   fmt.Sprintf("%s: spawned", agentName),
			UpdatedAt: time.Now().UTC(),
		}
		ks.Agents[agentName] = agent
	}
	agent.TmuxSession = sessionName
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s/%s] spawn: save failed: %v", spaceName, agentName, err))
	} else {
		s.mu.Unlock()
	}

	s.logEvent(fmt.Sprintf("[%s/%s] spawned in tmux session %q", spaceName, agentName, sessionName))
	s.broadcastSSE(spaceName, "agent_spawned", agentName)

	// Send ignite asynchronously after agent has time to initialize
	go func() {
		time.Sleep(5 * time.Second)
		igniteCmd := fmt.Sprintf(`/boss.ignite "%s" "%s"`, agentName, spaceName)
		if err := tmuxSendKeys(sessionName, igniteCmd); err != nil {
			s.logEvent(fmt.Sprintf("[%s/%s] spawn: ignite send failed: %v (ignite manually)", spaceName, agentName, err))
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":           true,
		"agent":        agentName,
		"tmux_session": sessionName,
		"space":        spaceName,
	})
}

// handleAgentStop handles POST /spaces/{space}/agent/{name}/stop.
// Kills the agent's tmux session and marks the agent as done.
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
		sessionName = agent.TmuxSession
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("agent %q not found", agentName), http.StatusNotFound)
		return
	}
	if sessionName == "" {
		http.Error(w, fmt.Sprintf("agent %q has no registered tmux session", canonical), http.StatusBadRequest)
		return
	}
	if !tmuxSessionExists(sessionName) {
		http.Error(w, fmt.Sprintf("tmux session %q not found", sessionName), http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionName).Run(); err != nil {
		http.Error(w, fmt.Sprintf("kill session: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	agent.Status = StatusDone
	agent.Summary = fmt.Sprintf("%s: stopped", canonical)
	agent.TmuxSession = ""
	agent.UpdatedAt = time.Now().UTC()
	s.saveSpace(ks)
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] stopped (session %q killed)", spaceName, canonical, sessionName))
	s.broadcastSSE(spaceName, "agent_stopped", canonical)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"agent": canonical,
	})
}

// handleAgentRestart handles POST /spaces/{space}/agent/{name}/restart.
// Kills the existing tmux session and spawns a new one.
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
		oldSession = agent.TmuxSession
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("agent %q not found", agentName), http.StatusNotFound)
		return
	}

	// Stop the existing session
	if oldSession != "" && tmuxSessionExists(oldSession) {
		ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
		if err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", oldSession).Run(); err != nil {
			cancel()
			http.Error(w, fmt.Sprintf("kill existing session: %v", err), http.StatusInternalServerError)
			return
		}
		cancel()
		s.logEvent(fmt.Sprintf("[%s/%s] restart: killed session %q", spaceName, canonical, oldSession))
		time.Sleep(1 * time.Second)
	}

	// Clear the session reference so spawn can proceed
	s.mu.Lock()
	agent.TmuxSession = ""
	s.mu.Unlock()

	// Spawn a new session using the canonical name as session name
	newSession := canonical
	if tmuxSessionExists(newSession) {
		// Append -new suffix if canonical name is taken
		newSession = canonical + "-new"
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel2()
	if err := exec.CommandContext(ctx2, "tmux", "new-session", "-d", "-s", newSession,
		"-x", "220", "-y", "50").Run(); err != nil {
		http.Error(w, fmt.Sprintf("create new tmux session: %v", err), http.StatusInternalServerError)
		return
	}

	time.Sleep(300 * time.Millisecond)
	if err := tmuxSendKeys(newSession, command); err != nil {
		http.Error(w, fmt.Sprintf("launch agent: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	agent.TmuxSession = newSession
	agent.Status = StatusIdle
	agent.Summary = fmt.Sprintf("%s: restarted", canonical)
	agent.UpdatedAt = time.Now().UTC()
	s.saveSpace(ks)
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] restarted in new session %q", spaceName, canonical, newSession))
	s.broadcastSSE(spaceName, "agent_restarted", canonical)

	// Send ignite asynchronously after agent has time to initialize
	go func() {
		time.Sleep(5 * time.Second)
		igniteCmd := fmt.Sprintf(`/boss.ignite "%s" "%s"`, canonical, spaceName)
		if err := tmuxSendKeys(newSession, igniteCmd); err != nil {
			s.logEvent(fmt.Sprintf("[%s/%s] restart: ignite send failed: %v", spaceName, canonical, err))
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":           true,
		"agent":        canonical,
		"tmux_session": newSession,
	})
}

// introspectResponse is returned by GET /spaces/{space}/agent/{name}/introspect.
type introspectResponse struct {
	Agent         string    `json:"agent"`
	TmuxSession   string    `json:"tmux_session,omitempty"`
	SessionExists bool      `json:"session_exists"`
	Idle          bool      `json:"idle"`
	NeedsApproval bool      `json:"needs_approval"`
	ToolName      string    `json:"tool_name,omitempty"`
	PromptText    string    `json:"prompt_text,omitempty"`
	Lines         []string  `json:"lines"`
	CapturedAt    time.Time `json:"captured_at"`
}

// handleAgentIntrospect handles GET /spaces/{space}/agent/{name}/introspect.
// Captures the recent tmux pane output and returns it as JSON.
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
		sessionName = agent.TmuxSession
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("agent %q not found", agentName), http.StatusNotFound)
		return
	}

	resp := introspectResponse{
		Agent:       canonical,
		TmuxSession: sessionName,
		Lines:       []string{},
		CapturedAt:  time.Now().UTC(),
	}

	if sessionName != "" && tmuxSessionExists(sessionName) {
		resp.SessionExists = true
		resp.Idle = tmuxIsIdle(sessionName)
		if lines, err := tmuxCapturePaneLines(sessionName, 50); err == nil {
			resp.Lines = lines
		}
		if !resp.Idle {
			approval := tmuxCheckApproval(sessionName)
			resp.NeedsApproval = approval.NeedsApproval
			resp.ToolName = approval.ToolName
			resp.PromptText = approval.PromptText
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
