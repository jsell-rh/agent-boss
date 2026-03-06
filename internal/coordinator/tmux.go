package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
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

var tmuxSessionAliases = map[string]string{
	"control-plane": "CP",
	"boss-app":      "",
}

func parseTmuxAgentName(session string) string {
	if !strings.HasPrefix(session, "agentdeck_") {
		return ""
	}
	rest := strings.TrimPrefix(session, "agentdeck_")
	idx := strings.LastIndex(rest, "_")
	if idx <= 0 {
		return ""
	}
	name := rest[:idx]
	if alias, ok := tmuxSessionAliases[name]; ok {
		return alias
	}
	return name
}

func (s *Server) TmuxAutoDiscover(spaceName string) int {
	if !tmuxAvailable() {
		return 0
	}
	sessions, err := tmuxListSessions()
	if err != nil {
		return 0
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		return 0
	}

	matched := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range sessions {
		name := parseTmuxAgentName(session)
		if name == "" {
			continue
		}
		for agentName, agent := range ks.Agents {
			if agent.TmuxSession != "" {
				continue
			}
			if strings.EqualFold(agentName, name) ||
				strings.EqualFold(strings.ReplaceAll(agentName, "-", ""), strings.ReplaceAll(name, "-", "")) {
				agent.TmuxSession = session
				matched++
				s.logEvent(fmt.Sprintf("[%s/%s] tmux session auto-discovered: %s", spaceName, agentName, session))
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

type approvalInfo struct {
	NeedsApproval bool   `json:"needs_approval"`
	ToolName      string `json:"tool_name,omitempty"`
	PromptText    string `json:"prompt_text,omitempty"`
}

func tmuxCheckApproval(session string) approvalInfo {
	if tmuxIsIdle(session) {
		return approvalInfo{}
	}
	lines, err := tmuxCapturePaneLines(session, 60)
	if err != nil || len(lines) == 0 {
		return approvalInfo{}
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
		return approvalInfo{}
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
		return approvalInfo{}
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
	return approvalInfo{
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

func tmuxIsIdle(session string) bool {
	lines, err := tmuxCapturePaneLines(session, 5)
	if err != nil {
		return false
	}
	for _, line := range lines {
		inner := strings.TrimSpace(strings.ReplaceAll(line, "│", ""))
		if inner == ">" {
			return true
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "?") && strings.Contains(trimmed, "for shortcuts") {
			return true
		}
		if strings.Contains(trimmed, "auto-compact") || strings.Contains(trimmed, "auto-accept") {
			return true
		}
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
	agent, exists := ks.Agents[agentName]
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
	s.broadcastSSE(spaceName, "broadcast_progress", string(data))
}

func (s *Server) runAgentCheckIn(spaceName, canonical, tmuxSession, checkModel, workModel string, result *BroadcastResult) {
	progress := func(msg string) {
		full := fmt.Sprintf("[%s/%s] %s", spaceName, canonical, msg)
		s.logEvent(full)
		s.broadcastProgress(spaceName, canonical+": "+msg)
	}

	// Model economy: switch to a lightweight model for check-ins if configured.
	// If checkModel is empty, skip model switching entirely.
	if checkModel != "" {
		progress("switching to " + checkModel)
		if err := tmuxSendKeys(tmuxSession, "/model "+checkModel); err != nil {
			result.addError(canonical + ": model switch failed: " + err.Error())
			return
		}

		progress("waiting for model switch...")
		if err := waitForIdle(tmuxSession, idlePollTimeout); err != nil {
			result.addError(canonical + ": model switch did not complete: " + err.Error())
			return
		}
	}

	boardTimeBefore := s.agentUpdatedAt(spaceName, canonical)

	progress("sending /boss.check prompt")
	if err := tmuxSendKeys(tmuxSession, "/boss.check "+canonical+" "+spaceName); err != nil {
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

	// Restore the working model if one was specified
	if workModel != "" {
		progress("waiting for idle before model restore...")
		if err := waitForIdle(tmuxSession, idlePollTimeout); err != nil {
			result.addError(canonical + ": post-checkin idle wait failed: " + err.Error())
		}

		progress("restoring " + workModel)
		if err := tmuxSendKeys(tmuxSession, "/model "+workModel); err != nil {
			result.addError(canonical + ": model restore failed: " + err.Error())
			return
		}

		progress("waiting for model restore...")
		if err := waitForIdle(tmuxSession, idlePollTimeout); err != nil {
			result.addError(canonical + ": model restore did not complete: " + err.Error())
		}
	}

	progress("complete")
}

func (s *Server) BroadcastCheckIn(spaceName, checkModel, workModel string) *BroadcastResult {
	result := &BroadcastResult{}

	if !tmuxAvailable() {
		result.Errors = append(result.Errors, "tmux not found in PATH")
		return result
	}

	s.TmuxAutoDiscover(spaceName)

	ks, ok := s.getSpace(spaceName)
	if !ok {
		result.Errors = append(result.Errors, "space not found: "+spaceName)
		return result
	}

	s.mu.RLock()
	type target struct {
		agentName   string
		tmuxSession string
	}
	var targets []target
	for name, agent := range ks.Agents {
		if agent.TmuxSession != "" {
			targets = append(targets, target{
				agentName:   name,
				tmuxSession: agent.TmuxSession,
			})
		}
	}
	s.mu.RUnlock()

	if len(targets) == 0 {
		result.Errors = append(result.Errors, "no agents have registered tmux sessions")
		return result
	}

	s.logEvent(fmt.Sprintf("[%s] broadcast: processing %d registered agents concurrently", spaceName, len(targets)))

	var wg sync.WaitGroup
	for i, t := range targets {
		if !tmuxSessionExists(t.tmuxSession) {
			result.addSkipped(t.agentName + " (session not found: " + t.tmuxSession + ")")
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if !tmuxIsIdle(t.tmuxSession) {
			result.addSkipped(t.agentName + " (busy)")
			time.Sleep(200 * time.Millisecond)
			continue
		}
		wg.Add(1)
		go func(agentName, tmuxSession string) {
			defer wg.Done()
			s.runAgentCheckIn(spaceName, agentName, tmuxSession, checkModel, workModel, result)
		}(t.agentName, t.tmuxSession)
		if i < len(targets)-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	wg.Wait()

	s.logEvent(fmt.Sprintf("[%s] broadcast complete: %d sent, %d skipped, %d errors",
		spaceName, len(result.Sent), len(result.Skipped), len(result.Errors)))
	return result
}

func (s *Server) SingleAgentCheckIn(spaceName, agentName, checkModel, workModel string) *BroadcastResult {
	result := &BroadcastResult{}

	if !tmuxAvailable() {
		result.Errors = append(result.Errors, "tmux not found in PATH")
		return result
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		result.Errors = append(result.Errors, "space not found: "+spaceName)
		return result
	}

	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var tmuxSession string
	if exists {
		tmuxSession = agent.TmuxSession
	}
	s.mu.RUnlock()

	if !exists {
		result.Errors = append(result.Errors, "agent not found: "+agentName)
		return result
	}
	if tmuxSession == "" {
		result.Errors = append(result.Errors, canonical+": no tmux session registered")
		return result
	}
	if !tmuxSessionExists(tmuxSession) {
		result.Skipped = append(result.Skipped, canonical+" (session not found: "+tmuxSession+")")
		return result
	}
	if !tmuxIsIdle(tmuxSession) {
		result.Skipped = append(result.Skipped, canonical+" (busy)")
		return result
	}

	s.runAgentCheckIn(spaceName, canonical, tmuxSession, checkModel, workModel, result)
	return result
}
