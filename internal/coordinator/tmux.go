package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	tmuxSendDelay    = 800 * time.Millisecond
	tmuxCmdTimeout   = 5 * time.Second
	idlePollInterval = 3 * time.Second
	idlePollTimeout  = 60 * time.Second
	boardPollTimeout = 3 * time.Minute
)

func tmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

func tmuxListSessions() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#S").CombinedOutput()
	if err != nil {
		return nil, err
	}
	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}

func (s *Server) TmuxAutoDiscover(spaceName string) int {
	backend := s.backends[s.defaultBackend]
	if !backend.Available() {
		return 0
	}

	discovered, err := backend.DiscoverSessions()
	if err != nil || len(discovered) == 0 {
		return 0
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		return 0
	}

	matched := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, session := range discovered {
		if name == "" {
			continue
		}
		for agentName, rec := range ks.Agents {
			if rec == nil || rec.Status == nil { continue }
			agent := rec.Status
			if agent.SessionID != "" {
				continue
			}
			if strings.EqualFold(agentName, name) ||
				strings.EqualFold(strings.ReplaceAll(agentName, "-", ""), strings.ReplaceAll(name, "-", "")) {
				agent.SessionID = session
				matched++
				s.logEvent(fmt.Sprintf("[%s/%s] session auto-discovered: %s", spaceName, agentName, session))
				break
			}
		}
	}
	if matched > 0 {
		s.saveSpace(ks)
	}
	return matched
}

func tmuxSessionExists(session string) bool {
	sessions, err := tmuxListSessions()
	if err != nil {
		return false
	}
	for _, s := range sessions {
		if s == session {
			return true
		}
	}
	return false
}

func tmuxCapturePaneLines(session string, n int) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-t", session, "-p").CombinedOutput()
	if err != nil {
		return nil, err
	}
	raw := strings.Split(string(out), "\n")
	var nonEmpty []string
	for _, l := range raw {
		l = strings.TrimRight(l, " \t")
		if l != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	if n > 0 && len(nonEmpty) > n {
		nonEmpty = nonEmpty[len(nonEmpty)-n:]
	}
	return nonEmpty, nil
}

func tmuxCapturePaneLastLine(session string) (string, error) {
	lines, err := tmuxCapturePaneLines(session, 1)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", nil
	}
	return strings.TrimSpace(lines[0]), nil
}

func tmuxCheckApproval(session string) ApprovalInfo {
	if tmuxIsIdle(session) {
		return ApprovalInfo{}
	}
	lines, err := tmuxCapturePaneLines(session, 60)
	if err != nil || len(lines) == 0 {
		return ApprovalInfo{}
	}
	promptIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.Contains(trimmed, "Do you want") && strings.Contains(trimmed, "?") {
			promptIdx = i
			break
		}
	}
	if promptIdx < 0 {
		return ApprovalInfo{}
	}
	hasNumberedChoice := false
	for i := promptIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		inner := strings.TrimSpace(strings.ReplaceAll(trimmed, "│", ""))
		if strings.HasPrefix(inner, "1.") || strings.HasPrefix(inner, ") 1.") || strings.HasPrefix(inner, "❯") ||
			strings.Contains(inner, "1. Yes") {
			hasNumberedChoice = true
			break
		}
	}
	if !hasNumberedChoice {
		return ApprovalInfo{}
	}
	var toolName string
	var contentLines []string
	for _, l := range lines[:promptIdx] {
		if !strings.Contains(l, "│") {
			continue
		}
		trimmed := strings.TrimSpace(l)
		inner := strings.TrimSpace(strings.ReplaceAll(trimmed, "│", ""))
		if inner == "" {
			continue
		}
		if strings.HasPrefix(inner, "╭") || strings.HasPrefix(inner, "╰") || strings.HasPrefix(inner, "─") {
			continue
		}
		for _, kw := range []string{"Bash", "Read", "Write", "Edit", "MultiEdit", "Glob", "Grep", "WebFetch", "NotebookEdit", "Task"} {
			if strings.HasPrefix(inner, kw+" ") || inner == kw || strings.HasPrefix(inner, kw+"(") {
				toolName = kw
				break
			}
		}
		contentLines = append(contentLines, inner)
	}
	prompt := strings.Join(contentLines, " | ")
	if len(prompt) > 2000 {
		prompt = prompt[:1997] + "..."
	}
	return ApprovalInfo{
		NeedsApproval: true,
		ToolName:      toolName,
		PromptText:    prompt,
	}
}

func tmuxApprove(session string) error {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "Enter").Run()
}

// tmuxIsIdle reports whether the tmux session appears to be waiting for input
// (i.e., no agent or process is actively running). It is intentionally generous:
// a session is "busy" only when there is positive evidence of activity.
func tmuxIsIdle(session string) bool {
	lines, err := tmuxCapturePaneLines(session, 10)
	if err != nil {
		// Cannot read the pane — default to idle rather than falsely reporting busy.
		return true
	}

	// An entirely empty pane (all blank lines) is idle.
	if len(lines) == 0 {
		return true
	}

	// Check each of the last N non-empty lines for idle indicators.
	for _, line := range lines {
		if lineIsIdleIndicator(line) {
			return true
		}
	}
	return false
}

// lineIsIdleIndicator returns true if a single pane line indicates the session
// is idle / waiting for user input.
func lineIsIdleIndicator(line string) bool {
	trimmed := strings.TrimSpace(line)
	// Strip box-drawing characters used by Claude Code / opencode TUI.
	// Both light (│ U+2502) and heavy (┃ U+2503) verticals are used.
	inner := trimmed
	inner = strings.ReplaceAll(inner, "│", "")
	inner = strings.ReplaceAll(inner, "┃", "")
	inner = strings.TrimSpace(inner)

	// ── Claude Code / opencode prompt ──
	// The prompt line inside the TUI box is just ">" (possibly with trailing space).
	if inner == ">" || inner == "> " {
		return true
	}

	// ── Claude Code prompt with suggestion ──
	// Claude Code shows "❯" as its prompt. When idle it may auto-fill a
	// suggested prompt after the ❯ (e.g. "❯ give me something to work on").
	// A line starting with ❯ means the agent is waiting for input regardless
	// of what follows (user-typed text or auto-suggestion).
	if strings.HasPrefix(trimmed, "❯") {
		return true
	}

	// ── Shell prompts ──
	// Common interactive shell prompts end with $, %, >, #, or ❯ possibly
	// followed by a space. We check the last non-space rune of the line.
	if isShellPrompt(trimmed) {
		return true
	}

	// ── Claude Code / opencode hint lines ──
	if strings.HasPrefix(trimmed, "?") && strings.Contains(trimmed, "for shortcuts") {
		return true
	}
	if strings.Contains(trimmed, "auto-compact") || strings.Contains(trimmed, "auto-accept") {
		return true
	}

	// ── Claude Code / opencode status bar ──
	// OpenCode's bottom bar contains "ctrl+p commands" when idle.
	// Claude Code's bottom bar contains "-- INSERT --" or "-- NORMAL --" (vim mode).
	if strings.Contains(trimmed, "ctrl+p commands") {
		return true
	}
	if strings.Contains(trimmed, "-- INSERT --") || strings.Contains(trimmed, "-- NORMAL --") {
		return true
	}

	// ── OpenCode / Claude Code status bar keywords ──
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "waiting for input") ||
		strings.Contains(lower, "ready") ||
		strings.Contains(lower, "type a message") ||
		strings.Contains(lower, "press enter") {
		return true
	}

	return false
}

// isShellPrompt returns true if the line looks like a common shell prompt.
// It matches lines whose last meaningful character is one of $, %, >, #, or ❯,
// but guards against false positives like "50%" or "line #3".
func isShellPrompt(line string) bool {
	s := strings.TrimRight(line, " \t")
	if s == "" {
		return false
	}
	last, size := utf8.DecodeLastRuneInString(s)
	switch last {
	case '$', '❯', '»':
		// These are unambiguous prompt characters.
		return true
	case '>':
		// Reject "=>" (fat arrow), "->" (arrow), but allow bare ">" or ">>> ".
		if len(s) >= 2 {
			prev := s[len(s)-2]
			if prev == '=' || prev == '-' {
				return false
			}
		}
		return true
	case '%', '#':
		// Reject "50%" or "line #3" — these chars are only prompts when NOT
		// preceded by a digit.
		before := s[:len(s)-size]
		before = strings.TrimRight(before, " \t")
		if before == "" {
			return true // bare "%" or "#"
		}
		prevChar := before[len(before)-1]
		if prevChar >= '0' && prevChar <= '9' {
			return false
		}
		return true
	}
	return false
}

func tmuxSendKeys(session, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, text).Run(); err != nil {
		return err
	}
	time.Sleep(tmuxSendDelay)
	ctx2, cancel2 := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel2()
	if err := exec.CommandContext(ctx2, "tmux", "send-keys", "-t", session, "C-m").Run(); err != nil {
		return err
	}
	time.Sleep(tmuxSendDelay)
	return nil
}

func waitForIdle(session string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	time.Sleep(2 * time.Second)
	for time.Now().Before(deadline) {
		if tmuxIsIdle(session) {
			return nil
		}
		time.Sleep(idlePollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for idle", timeout)
}

func (s *Server) agentUpdatedAt(spaceName, agentName string) time.Time {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		return time.Time{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	agent, exists := ks.agentStatusOk(agentName)
	if !exists {
		return time.Time{}
	}
	return agent.UpdatedAt
}

func (s *Server) waitForBoardPost(spaceName, agentName string, since time.Time, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(idlePollInterval)
		current := s.agentUpdatedAt(spaceName, agentName)
		if current.After(since) {
			return nil
		}
	}
	return fmt.Errorf("timed out after %s waiting for board post", timeout)
}

type BroadcastResult struct {
	mu      sync.Mutex `json:"-"`
	Sent    []string   `json:"sent"`
	Skipped []string   `json:"skipped"`
	Errors  []string   `json:"errors"`
}

func (r *BroadcastResult) addSent(s string) {
	r.mu.Lock()
	r.Sent = append(r.Sent, s)
	r.mu.Unlock()
}

func (r *BroadcastResult) addSkipped(s string) {
	r.mu.Lock()
	r.Skipped = append(r.Skipped, s)
	r.mu.Unlock()
}

func (r *BroadcastResult) addError(s string) {
	r.mu.Lock()
	r.Errors = append(r.Errors, s)
	r.mu.Unlock()
}

func (s *Server) broadcastProgress(spaceName, msg string) {
	data, _ := json.Marshal(map[string]string{"space": spaceName, "message": msg})
	s.broadcastSSE(spaceName, "", "broadcast_progress", string(data))
}

func (s *Server) runAgentCheckIn(spaceName, canonical, sessionID string, backend SessionBackend, checkModel, workModel string, result *BroadcastResult) {
	progress := func(msg string) {
		full := fmt.Sprintf("[%s/%s] %s", spaceName, canonical, msg)
		s.logEvent(full)
		s.broadcastProgress(spaceName, canonical+": "+msg)
	}

	// Model economy: switch to a lightweight model for check-ins if configured.
	// Skip model switching for non-tmux backends (ambient sessions have fixed models).
	if checkModel != "" && backend.Name() == "tmux" {
		progress("switching to " + checkModel)
		if err := backend.SendInput(sessionID, "/model "+checkModel); err != nil {
			result.addError(canonical + ": model switch failed: " + err.Error())
			return
		}

		progress("waiting for model switch...")
		if err := waitForIdleBackend(backend, sessionID, idlePollTimeout); err != nil {
			result.addError(canonical + ": model switch did not complete: " + err.Error())
			return
		}
	}

	boardTimeBefore := s.agentUpdatedAt(spaceName, canonical)

	// Send a plain-text check-in prompt. The old /boss.check slash command
	// relied on symlinked command files which no longer exist.
	checkInURL := fmt.Sprintf("http://localhost%s/spaces/%s/agent/%s", s.port, spaceName, canonical)
	msgURL := fmt.Sprintf("http://localhost%s/spaces/%s/agent/%s/messages", s.port, spaceName, canonical)
	checkInPrompt := fmt.Sprintf(
		"Check in now. 1) Fetch new messages: curl -s %q 2) Post your current status: curl -s -X POST %q -H 'Content-Type: application/json' -H 'X-Agent-Name: %s' -d '{\"status\":\"active\",\"summary\":\"%s: checking in\"}' 3) Act on any message directives.",
		msgURL, checkInURL, canonical, canonical,
	)
	progress("sending check-in prompt")
	if err := backend.SendInput(sessionID, checkInPrompt); err != nil {
		result.addError(canonical + ": check-in send failed: " + err.Error())
		return
	}

	progress(fmt.Sprintf("waiting for board post (up to %s)...", boardPollTimeout))
	if err := s.waitForBoardPost(spaceName, canonical, boardTimeBefore, boardPollTimeout); err != nil {
		result.addError(canonical + ": " + err.Error())
		return
	}
	result.addSent(canonical)
	progress("board post received")

	// Restore the working model if one was specified.
	// Skip for non-tmux backends.
	if workModel != "" && backend.Name() == "tmux" {
		progress("waiting for idle before model restore...")
		if err := waitForIdleBackend(backend, sessionID, idlePollTimeout); err != nil {
			result.addError(canonical + ": post-checkin idle wait failed: " + err.Error())
		}

		progress("restoring " + workModel)
		if err := backend.SendInput(sessionID, "/model "+workModel); err != nil {
			result.addError(canonical + ": model restore failed: " + err.Error())
			return
		}

		progress("waiting for model restore...")
		if err := waitForIdleBackend(backend, sessionID, idlePollTimeout); err != nil {
			result.addError(canonical + ": model restore did not complete: " + err.Error())
		}
	}

	progress("complete")
}

func (s *Server) BroadcastCheckIn(spaceName, checkModel, workModel string) *BroadcastResult {
	result := &BroadcastResult{}

	// Auto-discover sessions across all available backends.
	s.AutoDiscoverAll(spaceName)

	ks, ok := s.getSpace(spaceName)
	if !ok {
		result.Errors = append(result.Errors, "space not found: "+spaceName)
		return result
	}

	s.mu.RLock()
	type target struct {
		agentName   string
		sessionID   string
		backendType string
	}
	var targets []target
	for name, rec := range ks.Agents {
		if rec == nil || rec.Status == nil { continue }
		agent := rec.Status
		if agent.SessionID != "" {
			targets = append(targets, target{
				agentName:   name,
				sessionID:   agent.SessionID,
				backendType: agent.BackendType,
			})
		}
	}
	s.mu.RUnlock()

	if len(targets) == 0 {
		result.Errors = append(result.Errors, "no agents have registered sessions")
		return result
	}

	s.logEvent(fmt.Sprintf("[%s] broadcast: processing %d registered agents concurrently", spaceName, len(targets)))

	var wg sync.WaitGroup
	for i, t := range targets {
		backend := s.backendByName(t.backendType)
		if !backend.Available() {
			result.addSkipped(t.agentName + " (backend " + backend.Name() + " unavailable)")
			continue
		}
		if !backend.SessionExists(t.sessionID) {
			result.addSkipped(t.agentName + " (session not found: " + t.sessionID + ")")
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if !backend.IsIdle(t.sessionID) {
			result.addSkipped(t.agentName + " (busy)")
			time.Sleep(200 * time.Millisecond)
			continue
		}
		wg.Add(1)
		go func(agentName, sessionID string, b SessionBackend) {
			defer wg.Done()
			s.runAgentCheckIn(spaceName, agentName, sessionID, b, checkModel, workModel, result)
		}(t.agentName, t.sessionID, backend)
		if i < len(targets)-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	wg.Wait()

	s.logEvent(fmt.Sprintf("[%s] broadcast complete: %d sent, %d skipped, %d errors",
		spaceName, len(result.Sent), len(result.Skipped), len(result.Errors)))
	return result
}

// AutoDiscoverAll runs session discovery across all available backends and
// associates discovered sessions with agents in the given space.
func (s *Server) AutoDiscoverAll(spaceName string) {
	for _, backend := range s.backends {
		if !backend.Available() {
			continue
		}
		discovered, err := backend.DiscoverSessions()
		if err != nil || len(discovered) == 0 {
			continue
		}
		ks, ok := s.getSpace(spaceName)
		if !ok {
			return
		}
		s.mu.Lock()
		for name, session := range discovered {
			if name == "" {
				continue
			}
			for agentName, rec := range ks.Agents {
				if rec == nil || rec.Status == nil { continue }
				agent := rec.Status
				if agent.SessionID != "" {
					continue
				}
				if strings.EqualFold(agentName, name) ||
					strings.EqualFold(strings.ReplaceAll(agentName, "-", ""), strings.ReplaceAll(name, "-", "")) {
					agent.SessionID = session
					agent.BackendType = backend.Name()
					s.logEvent(fmt.Sprintf("[%s/%s] session auto-discovered via %s: %s", spaceName, agentName, backend.Name(), session))
					break
				}
			}
		}
		s.saveSpace(ks) //nolint:errcheck
		s.mu.Unlock()
	}
}

func (s *Server) SingleAgentCheckIn(spaceName, agentName, checkModel, workModel string) *BroadcastResult {
	result := &BroadcastResult{}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		result.Errors = append(result.Errors, "space not found: "+spaceName)
		return result
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
		result.Errors = append(result.Errors, "agent not found: "+agentName)
		return result
	}
	if sessionID == "" {
		result.Errors = append(result.Errors, canonical+": no session registered")
		return result
	}

	backend := s.backendFor(agent)
	if !backend.Available() {
		result.Errors = append(result.Errors, backend.Name()+" not available")
		return result
	}
	if !backend.SessionExists(sessionID) {
		result.Skipped = append(result.Skipped, canonical+" (session not found: "+sessionID+")")
		return result
	}
	if !backend.IsIdle(sessionID) {
		result.Skipped = append(result.Skipped, canonical+" (busy)")
		return result
	}

	s.runAgentCheckIn(spaceName, canonical, sessionID, backend, checkModel, workModel, result)
	return result
}
