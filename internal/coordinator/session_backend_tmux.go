package coordinator

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Compile-time interface compliance checks.
var _ SessionBackend   = (*TmuxSessionBackend)(nil)
var _ SessionLifecycle = (*TmuxSessionBackend)(nil)
var _ SessionObserver  = (*TmuxSessionBackend)(nil)
var _ SessionActor     = (*TmuxSessionBackend)(nil)

// TmuxSessionBackend implements SessionBackend using local tmux sessions.
type TmuxSessionBackend struct {
	// sessionAliases maps legacy tmux session name fragments to agent names
	// for auto-discovery. Moved from the tmuxSessionAliases global.
	sessionAliases map[string]string
}

// NewTmuxSessionBackend creates a TmuxSessionBackend with default aliases.
func NewTmuxSessionBackend() *TmuxSessionBackend {
	return &TmuxSessionBackend{
		sessionAliases: map[string]string{
			"control-plane": "CP",
			"boss-app":      "",
		},
	}
}

func (b *TmuxSessionBackend) Name() string { return "tmux" }

func (b *TmuxSessionBackend) Available() bool { return tmuxAvailable() }

func (b *TmuxSessionBackend) CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error) {
	sessionID := opts.SessionID
	command := opts.Command
	if command == "" {
		command = "claude"
	}

	width := 220
	height := 50
	var workDir string
	var mcpServerURL string
	var mcpServerName string
	var allowSkipPermissions bool

	if tmuxOpts, ok := opts.BackendOpts.(TmuxCreateOpts); ok {
		if tmuxOpts.Width > 0 {
			width = tmuxOpts.Width
		}
		if tmuxOpts.Height > 0 {
			height = tmuxOpts.Height
		}
		workDir = tmuxOpts.WorkDir
		mcpServerURL = tmuxOpts.MCPServerURL
		mcpServerName = tmuxOpts.MCPServerName
		allowSkipPermissions = tmuxOpts.AllowSkipPermissions
	}
	if mcpServerName == "" {
		mcpServerName = "boss-mcp"
	}

	// Append --dangerously-skip-permissions when global toggle is on.
	if allowSkipPermissions && !strings.Contains(command, "--dangerously-skip-permissions") {
		command += " --dangerously-skip-permissions"
	}

	if b.SessionExists(sessionID) {
		return "", fmt.Errorf("tmux session %q already exists", sessionID)
	}

	if err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", sessionID,
		"-x", fmt.Sprintf("%d", width), "-y", fmt.Sprintf("%d", height)).Run(); err != nil {
		return "", fmt.Errorf("create tmux session: %w", err)
	}

	// Wait for the shell to initialise before sending keys — without this,
	// send-keys races shell startup and the cd keystroke is silently dropped.
	time.Sleep(300 * time.Millisecond)

	if workDir != "" {
		if err := tmuxSendKeys(sessionID, "cd "+shellQuote(workDir)); err != nil {
			exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionID).Run() //nolint:errcheck
			return "", fmt.Errorf("cd to workdir: %w", err)
		}
	}

	// Register boss MCP server with Claude before launching (idempotent).
	if mcpServerURL != "" {
		mcpCmd := fmt.Sprintf("claude mcp add %s --transport http %s/mcp 2>/dev/null || true", mcpServerName, mcpServerURL)
		if err := tmuxSendKeys(sessionID, mcpCmd); err != nil {
			// Non-fatal: log but continue — agent can still function without MCP.
			_ = err
		}
		time.Sleep(300 * time.Millisecond)
	}

	if err := tmuxSendKeys(sessionID, command); err != nil {
		exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionID).Run() //nolint:errcheck
		return "", fmt.Errorf("launch agent command: %w", err)
	}

	return sessionID, nil
}

func (b *TmuxSessionBackend) KillSession(ctx context.Context, sessionID string) error {
	if !b.SessionExists(sessionID) {
		return fmt.Errorf("tmux session %q not found", sessionID)
	}
	if err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionID).Run(); err != nil {
		return fmt.Errorf("kill tmux session: %w", err)
	}
	return nil
}

func (b *TmuxSessionBackend) SessionExists(sessionID string) bool {
	return tmuxSessionExists(sessionID)
}

func (b *TmuxSessionBackend) ListSessions() ([]string, error) {
	return tmuxListSessions()
}

func (b *TmuxSessionBackend) GetStatus(ctx context.Context, sessionID string) (SessionStatus, error) {
	if !b.SessionExists(sessionID) {
		return SessionStatusMissing, nil
	}
	if b.IsIdle(sessionID) {
		return SessionStatusIdle, nil
	}
	return SessionStatusRunning, nil
}

func (b *TmuxSessionBackend) IsIdle(sessionID string) bool {
	return tmuxIsIdle(sessionID)
}

func (b *TmuxSessionBackend) CaptureOutput(sessionID string, lines int) ([]string, error) {
	return tmuxCapturePaneLines(sessionID, lines)
}

func (b *TmuxSessionBackend) CheckApproval(sessionID string) ApprovalInfo {
	return tmuxCheckApproval(sessionID)
}

func (b *TmuxSessionBackend) SendInput(sessionID string, text string) error {
	return tmuxSendKeys(sessionID, text)
}

func (b *TmuxSessionBackend) Approve(sessionID string) error {
	return tmuxApprove(sessionID)
}

func (b *TmuxSessionBackend) AlwaysAllow(sessionID string) error {
	return tmuxAlwaysAllow(sessionID)
}

func (b *TmuxSessionBackend) Interrupt(ctx context.Context, sessionID string) error {
	// Claude Code requires two Escape presses to fully cancel a running operation:
	// the first Escape triggers the "Interrupt?" confirmation prompt, and the second
	// confirms the cancellation. Sending both with a short delay makes a single
	// "Interrupt" button press work end-to-end without needing a second click.
	if err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", sessionID, "Escape").Run(); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	return exec.CommandContext(ctx, "tmux", "send-keys", "-t", sessionID, "Escape").Run()
}

func (b *TmuxSessionBackend) DiscoverSessions() (map[string]string, error) {
	sessions, err := tmuxListSessions()
	if err != nil {
		return nil, err
	}
	discovered := make(map[string]string)
	for _, session := range sessions {
		name := b.parseTmuxAgentName(session)
		if name != "" {
			discovered[name] = session
		}
	}
	return discovered, nil
}

// parseTmuxAgentName extracts the agent name from a tmux session name.
// Uses instance-level sessionAliases instead of the global variable.
func (b *TmuxSessionBackend) parseTmuxAgentName(session string) string {
	if !strings.HasPrefix(session, "agentdeck_") {
		return ""
	}
	rest := strings.TrimPrefix(session, "agentdeck_")
	idx := strings.LastIndex(rest, "_")
	if idx <= 0 {
		return ""
	}
	name := rest[:idx]
	if alias, ok := b.sessionAliases[name]; ok {
		return alias
	}
	return name
}

// tmuxDefaultSession generates a tmux-safe session name that is unique per space+agent pair.
// Format: {sanitized-space}-{sanitized-agent}, replacing non-alphanumeric characters with hyphens.
// This prevents tmux session name collisions when the same agent name is used in different spaces.
func tmuxDefaultSession(spaceName, agentName string) string {
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				b.WriteRune(r)
			} else {
				b.WriteByte('-')
			}
		}
		return strings.Trim(b.String(), "-")
	}
	sp := clean(spaceName)
	ag := clean(agentName)
	if sp == "" {
		return ag
	}
	return sp + "-" + ag
}

// shellQuote wraps a string in single quotes, escaping any existing single quotes.
// This is used to safely pass directory paths to the shell.
func shellQuote(s string) string {
	// Replace ' with '\'' (end quote, literal single quote, start quote).
	out := "'"
	for _, c := range s {
		if c == '\'' {
			out += "'\\''"
		} else {
			out += string(c)
		}
	}
	out += "'"
	return out
}

// waitForIdleBackend polls the given backend until the session is idle or times out.
func waitForIdleBackend(backend SessionBackend, sessionID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	time.Sleep(2 * time.Second)
	for time.Now().Before(deadline) {
		if backend.IsIdle(sessionID) {
			return nil
		}
		time.Sleep(idlePollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for idle", timeout)
}
