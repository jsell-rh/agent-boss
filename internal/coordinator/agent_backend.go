package coordinator

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrNotImplemented is returned by backend adapters that have not yet been implemented.
var ErrNotImplemented = errors.New("not implemented")

// AgentSpec describes the desired configuration for a new agent.
type AgentSpec struct {
	Name    string `json:"name"`
	Space   string `json:"space"`
	WorkDir string `json:"work_dir,omitempty"` // working directory for the agent process
	Command string `json:"command,omitempty"`  // defaults to "claude --dangerously-skip-permissions"
	Width   int    `json:"width,omitempty"`    // tmux window width, default 220
	Height  int    `json:"height,omitempty"`   // tmux window height, default 50
	Parent  string `json:"parent,omitempty"`   // parent agent name for hierarchy
	Role    string `json:"role,omitempty"`     // display label
}

// AgentInfo describes a running agent as reported by a backend.
type AgentInfo struct {
	Name      string    `json:"name"`
	Space     string    `json:"space"`
	Backend   string    `json:"backend"`
	SessionID string    `json:"session_id,omitempty"` // backend-specific session identifier
	StartedAt time.Time `json:"started_at,omitempty"`
}

// AgentBackend is the port (interface) for agent lifecycle management.
// Concrete adapters (TmuxBackend, CloudBackend) implement this interface.
type AgentBackend interface {
	// Spawn creates and starts a new agent according to spec.
	Spawn(ctx context.Context, spec AgentSpec) (AgentInfo, error)

	// Stop terminates a running agent by name in the given space.
	Stop(ctx context.Context, space, name string) error

	// List returns all agents managed by this backend in the given space.
	List(ctx context.Context, space string) ([]AgentInfo, error)

	// Name returns the backend identifier string (e.g. "tmux", "cloud").
	Name() string
}

// TmuxBackend is the AgentBackend adapter that manages agents via local tmux sessions.
type TmuxBackend struct{}

// Name returns "tmux".
func (b *TmuxBackend) Name() string { return "tmux" }

// Spawn creates a new detached tmux session, launches the agent command, and
// sends the /boss.ignite prompt after initialization.
func (b *TmuxBackend) Spawn(ctx context.Context, spec AgentSpec) (AgentInfo, error) {
	sessionName := tmuxDefaultSession(spec.Space, spec.Name)

	command := spec.Command
	if command == "" {
		command = "claude --dangerously-skip-permissions"
	}
	width := spec.Width
	if width <= 0 {
		width = 220
	}
	height := spec.Height
	if height <= 0 {
		height = 50
	}

	if tmuxSessionExists(sessionName) {
		return AgentInfo{}, fmt.Errorf("tmux session %q already exists", sessionName)
	}

	// Create detached tmux session.
	if err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", sessionName,
		"-x", fmt.Sprintf("%d", width), "-y", fmt.Sprintf("%d", height)).Run(); err != nil {
		return AgentInfo{}, fmt.Errorf("create tmux session: %w", err)
	}

	// Change directory if specified.
	if spec.WorkDir != "" {
		if err := tmuxSendKeys(sessionName, "cd "+shellQuote(spec.WorkDir)); err != nil {
			exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionName).Run() //nolint:errcheck
			return AgentInfo{}, fmt.Errorf("cd to workdir: %w", err)
		}
	}

	// Launch agent command.
	if err := tmuxSendKeys(sessionName, command); err != nil {
		exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionName).Run() //nolint:errcheck
		return AgentInfo{}, fmt.Errorf("launch agent command: %w", err)
	}

	return AgentInfo{
		Name:      spec.Name,
		Space:     spec.Space,
		Backend:   "tmux",
		SessionID: sessionName,
		StartedAt: time.Now().UTC(),
	}, nil
}

// Stop kills the tmux session with the given name.
func (b *TmuxBackend) Stop(ctx context.Context, space, name string) error {
	if !tmuxSessionExists(name) {
		return fmt.Errorf("tmux session %q not found", name)
	}
	if err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", name).Run(); err != nil {
		return fmt.Errorf("kill tmux session: %w", err)
	}
	return nil
}

// List returns AgentInfo for every active tmux session (not space-filtered, since
// tmux sessions are global). Callers should filter by Name if needed.
func (b *TmuxBackend) List(ctx context.Context, space string) ([]AgentInfo, error) {
	sessions, err := tmuxListSessions()
	if err != nil {
		return nil, fmt.Errorf("list tmux sessions: %w", err)
	}
	out := make([]AgentInfo, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, AgentInfo{
			Name:      s,
			Space:     space,
			Backend:   "tmux",
			SessionID: s,
		})
	}
	return out, nil
}

// CloudBackend is a stub AgentBackend adapter for future cloud-based agent infrastructure.
// All methods return ErrNotImplemented.
type CloudBackend struct{}

// Name returns "cloud".
func (b *CloudBackend) Name() string { return "cloud" }

// Spawn is not yet implemented for the cloud backend.
func (b *CloudBackend) Spawn(ctx context.Context, spec AgentSpec) (AgentInfo, error) {
	return AgentInfo{}, fmt.Errorf("cloud backend: %w", ErrNotImplemented)
}

// Stop is not yet implemented for the cloud backend.
func (b *CloudBackend) Stop(ctx context.Context, space, name string) error {
	return fmt.Errorf("cloud backend: %w", ErrNotImplemented)
}

// List is not yet implemented for the cloud backend.
func (b *CloudBackend) List(ctx context.Context, space string) ([]AgentInfo, error) {
	return nil, fmt.Errorf("cloud backend: %w", ErrNotImplemented)
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
