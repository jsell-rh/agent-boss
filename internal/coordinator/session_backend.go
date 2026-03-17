package coordinator

import "context"

// SessionBackend is the interface for managing agent sessions.
// Each backend (tmux, ambient, etc.) implements this interface.
// The coordinator routes operations through it instead of calling
// tmux functions directly.
type SessionBackend interface {
	// --- Identity ---

	// Name returns the backend identifier ("tmux", "ambient", etc.).
	Name() string

	// Available reports whether this backend is operational.
	Available() bool

	// --- Lifecycle ---

	// CreateSession creates a new session and launches the given command.
	// Returns the backend-specific session ID.
	CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error)

	// KillSession permanently destroys a session by ID.
	KillSession(ctx context.Context, sessionID string) error

	// SessionExists checks whether a session with the given ID is alive.
	SessionExists(sessionID string) bool

	// ListSessions returns all session IDs managed by this backend.
	ListSessions() ([]string, error)

	// --- Status ---

	// GetStatus returns the current status of a session.
	GetStatus(ctx context.Context, sessionID string) (SessionStatus, error)

	// --- Observability ---

	// IsIdle reports whether the session is waiting for user input.
	IsIdle(sessionID string) bool

	// CaptureOutput returns the last N non-empty lines from the session.
	CaptureOutput(sessionID string, lines int) ([]string, error)

	// CheckApproval inspects the session output for a pending tool-use approval prompt.
	CheckApproval(sessionID string) ApprovalInfo

	// --- Interaction ---

	// SendInput sends text to the session.
	SendInput(sessionID string, text string) error

	// Approve sends an approval response to a pending prompt (option 1: "Yes").
	Approve(sessionID string) error

	// AlwaysAllow sends the "always allow" response to a pending prompt
	// (option 2: "Yes, and don't ask again for this command").
	AlwaysAllow(sessionID string) error

	// Interrupt cancels the session's current work without killing it.
	Interrupt(ctx context.Context, sessionID string) error

	// --- Discovery ---

	// DiscoverSessions finds sessions that match known agent naming
	// conventions and returns a map of agentName -> sessionID.
	DiscoverSessions() (map[string]string, error)
}

// SessionLifecycle covers session creation and destruction.
type SessionLifecycle interface {
	CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error)
	KillSession(ctx context.Context, sessionID string) error
	SessionExists(sessionID string) bool
	ListSessions() ([]string, error)
}

// SessionObserver covers session status and observability.
type SessionObserver interface {
	GetStatus(ctx context.Context, sessionID string) (SessionStatus, error)
	IsIdle(sessionID string) bool
	CaptureOutput(sessionID string, lines int) ([]string, error)
	CheckApproval(sessionID string) ApprovalInfo
}

// SessionActor covers session interaction.
type SessionActor interface {
	SendInput(sessionID string, text string) error
	Approve(sessionID string) error
	Interrupt(ctx context.Context, sessionID string) error
}

// SessionStatus represents the state of a session.
type SessionStatus string

const (
	SessionStatusUnknown   SessionStatus = "unknown"
	SessionStatusPending   SessionStatus = "pending"
	SessionStatusRunning   SessionStatus = "running"
	SessionStatusIdle      SessionStatus = "idle"
	SessionStatusCompleted SessionStatus = "completed"
	SessionStatusFailed    SessionStatus = "failed"
	SessionStatusMissing   SessionStatus = "missing"
)

// SessionCreateOpts holds common parameters for creating a new session.
type SessionCreateOpts struct {
	SessionID   string      // desired session name/ID
	Command     string      // shell command to run (tmux) or initial prompt (ambient)
	BackendOpts any // backend-specific options (TmuxCreateOpts, etc.)
}

// TmuxCreateOpts holds tmux-specific session creation options.
type TmuxCreateOpts struct {
	WorkDir              string // working directory to cd into before launching
	Width                int    // terminal width (default 220)
	Height               int    // terminal height (default 50)
	MCPServerURL         string // if set, run "claude mcp add" before launching
	MCPServerName        string // MCP server name (e.g. "boss-mcp", "boss-mcp-8889"); defaults to "boss-mcp"
	AgentToken           string // bearer token embedded inline in --mcp-config JSON for MCP auth; never written to ~/.claude.json
	AllowSkipPermissions bool   // if true, append --dangerously-skip-permissions to command
}

// AmbientCreateOpts holds Ambient-specific session creation options.
type AmbientCreateOpts struct {
	DisplayName string            `json:"display_name,omitempty"`
	Model       string            `json:"model,omitempty"`
	Repos       []SessionRepo     `json:"repos,omitempty"`
	Workflow    *WorkflowRef      `json:"workflow,omitempty"`    // override per-session workflow
	EnvVars     map[string]string `json:"env_vars,omitempty"`   // per-session environment variables
	SpaceName   string            `json:"space_name,omitempty"` // used for label construction
}

// WorkflowRef identifies an ACP workflow by git repository location.
type WorkflowRef struct {
	GitURL string `json:"gitUrl"`
	Branch string `json:"branch,omitempty"`
	Path   string `json:"path,omitempty"`
}

// SessionRepo describes a repository to clone into an Ambient session.
type SessionRepo struct {
	URL    string `json:"url"`
	Branch string `json:"branch,omitempty"`
}

// ApprovalInfo describes a pending tool-use approval prompt.
type ApprovalInfo struct {
	NeedsApproval bool   `json:"needs_approval"`
	ToolName      string `json:"tool_name,omitempty"`
	PromptText    string `json:"prompt_text,omitempty"`
}
