# SessionBackend Interface Design

## Problem Statement

Tmux session management is hardcoded throughout the coordinator. The existing `AgentBackend` interface
(Spawn/Stop/List/Name) only covers creation and is only used by one handler (`handleCreateAgents`).
All other session operations — liveness polling, idle detection, approval checking, introspection,
broadcasting, sending input — call tmux functions directly.

This makes it impossible to swap in a different session manager (e.g., Ambient Code Platform sessions)
without forking the entire coordinator.

## Design Goals

1. **Single interface** that covers the full lifecycle: create, destroy, observe, interact
2. **Drop-in tmux implementation** that wraps existing functions with zero behavior change
3. **Ambient backend** implementable against the ACP public API
4. **Per-agent backend selection** — agents in the same space can use different backends
5. **Backward compatible** — existing `TmuxSession` field, ignition `?tmux_session=` param, and
   all API responses continue to work

## Non-Goals

- Changing the agent protocol (blackboard, messages, tasks, SSE)
- Modifying the frontend beyond renaming JSON fields
- Implementing the Ambient backend in this design doc (separate spec)

---

## Interface Definition

```go
// SessionBackend is the interface for managing agent sessions.
// Each backend (tmux, ambient, etc.) implements this interface.
// The coordinator routes operations through it instead of calling
// tmux functions directly.
type SessionBackend interface {
    // --- Identity ---

    // Name returns the backend identifier ("tmux", "ambient", etc.).
    Name() string

    // Available reports whether this backend is operational.
    // For tmux: checks if the binary is in PATH.
    // For ambient: checks if the API is reachable.
    Available() bool

    // --- Lifecycle ---

    // CreateSession creates a new session and launches the given command.
    // Returns the backend-specific session ID.
    // For tmux: creates a detached session and sends the command.
    // For ambient: calls POST /sessions with the command as the task.
    CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error)

    // KillSession stops/destroys a session by ID.
    // For tmux: kills the tmux session (gone forever).
    // For ambient: calls POST /sessions/{id}/stop (session data preserved).
    KillSession(ctx context.Context, sessionID string) error

    // SessionExists checks whether a session with the given ID is alive.
    SessionExists(sessionID string) bool

    // ListSessions returns all session IDs managed by this backend.
    ListSessions() ([]string, error)

    // --- Status ---

    // GetStatus returns the current status of a session.
    // For tmux: derives from SessionExists + IsIdle + CheckApproval.
    // For ambient: maps directly from the API response status field
    //   and the latest run status.
    GetStatus(ctx context.Context, sessionID string) (SessionStatus, error)

    // --- Observability ---

    // IsIdle reports whether the session is waiting for user input
    // (no agent or process actively running).
    // For tmux: checks terminal output for idle indicators (prompts, etc.).
    // For ambient: session is "running" AND latest run is completed/error.
    IsIdle(sessionID string) bool

    // CaptureOutput returns the last N non-empty lines from the session.
    // For tmux: captures terminal pane lines.
    // For ambient: fetches transcript messages and formats as
    //   "[role] content" lines.
    CaptureOutput(sessionID string, lines int) ([]string, error)

    // CheckApproval inspects the session output for a pending tool-use
    // approval prompt (e.g., "Do you want to run Bash?").
    // For tmux: parses terminal output for approval patterns.
    // For ambient: always returns NeedsApproval=false (sessions run
    //   with configured permissions, no interactive prompts).
    CheckApproval(sessionID string) ApprovalInfo

    // --- Interaction ---

    // SendInput sends text to the session.
    // For tmux: sends keystrokes followed by Enter.
    // For ambient: calls POST /sessions/{id}/message (creates a new run).
    SendInput(sessionID string, text string) error

    // Approve sends an approval response to a pending prompt.
    // For tmux: sends Enter key to accept.
    // For ambient: no-op (returns nil).
    Approve(sessionID string) error

    // Interrupt cancels the session's current work without killing it.
    // The session remains alive and can accept new messages.
    // For tmux: sends Ctrl-C (C-c) to the session.
    // For ambient: calls POST /sessions/{id}/interrupt.
    Interrupt(ctx context.Context, sessionID string) error

    // --- Discovery ---

    // DiscoverSessions finds sessions that match known agent naming
    // conventions and returns a map of agentName -> sessionID.
    // For tmux: parses agentdeck_* session names.
    // For ambient: lists sessions and matches by display_name.
    // Backends that don't support discovery return an empty map.
    DiscoverSessions() (map[string]string, error)
}
```

**Method count: 13** (was 11 in the initial draft; +`GetStatus`, +`Interrupt`)

### Supporting Types

```go
// SessionStatus represents the state of a session.
type SessionStatus string

const (
    SessionStatusUnknown   SessionStatus = "unknown"   // can't determine (backend unavailable)
    SessionStatusPending   SessionStatus = "pending"    // created but not yet running (ambient only)
    SessionStatusRunning   SessionStatus = "running"    // actively working
    SessionStatusIdle      SessionStatus = "idle"       // alive but waiting for input
    SessionStatusCompleted SessionStatus = "completed"  // finished
    SessionStatusFailed    SessionStatus = "failed"     // errored
    SessionStatusMissing   SessionStatus = "missing"    // session does not exist
)

// SessionCreateOpts holds parameters for creating a new session.
// Each backend uses the fields relevant to it and ignores the rest.
type SessionCreateOpts struct {
    // Common
    SessionID string // desired session name/ID (backend may adjust)
    Command   string // tmux: shell command to run; ambient: initial task/prompt

    // Tmux-specific (ignored by ambient)
    WorkDir string // working directory to cd into before launching
    Width   int    // terminal width (default 220)
    Height  int    // terminal height (default 50)

    // Ambient-specific (ignored by tmux)
    DisplayName string        // human-readable session name
    Model       string        // Claude model to use
    Repos       []SessionRepo // repositories to clone into the session
}

// SessionRepo describes a repository to attach to an ambient session.
type SessionRepo struct {
    URL    string `json:"url"`
    Branch string `json:"branch,omitempty"`
}

// ApprovalInfo describes a pending tool-use approval prompt.
// Exported from the existing unexported approvalInfo in tmux.go.
type ApprovalInfo struct {
    NeedsApproval bool   `json:"needs_approval"`
    ToolName      string `json:"tool_name,omitempty"`
    PromptText    string `json:"prompt_text,omitempty"`
}
```

### Rename: `approvalInfo` -> `ApprovalInfo`

The existing `approvalInfo` struct in `tmux.go` is unexported. Since it's now part of the
interface contract, it gets exported. The struct body is unchanged.

---

## Data Model Changes

### `AgentUpdate` field rename

```go
// Before:
TmuxSession string `json:"tmux_session,omitempty"`

// After:
SessionID   string `json:"session_id,omitempty"`
BackendType string `json:"backend_type,omitempty"` // "tmux", "ambient", etc.
```

**Backward compatibility:** The JSON tag changes from `tmux_session` to `session_id`. Since agents
POST status updates and the server preserves `SessionID` as a sticky field, old agents sending
`tmux_session` will need a migration shim in the JSON unmarshaler:

```go
func (u *AgentUpdate) UnmarshalJSON(data []byte) error {
    type Alias AgentUpdate
    aux := &struct {
        TmuxSession string `json:"tmux_session,omitempty"`
        *Alias
    }{Alias: (*Alias)(u)}
    if err := json.Unmarshal(data, aux); err != nil {
        return err
    }
    // Backward compat: accept tmux_session if session_id is empty
    if u.SessionID == "" && aux.TmuxSession != "" {
        u.SessionID = aux.TmuxSession
        u.BackendType = "tmux"
    }
    return nil
}
```

### DB schema

```sql
-- Rename column (SQLite requires table rebuild via GORM automigrate)
-- agents.tmux_session -> agents.session_id
-- Add new column: agents.backend_type TEXT DEFAULT ''
```

### Ignition query param

```
-- Before:
GET /spaces/{space}/ignition/{agent}?tmux_session=X

-- After (both accepted, old one triggers compat path):
GET /spaces/{space}/ignition/{agent}?session_id=X&backend=tmux
GET /spaces/{space}/ignition/{agent}?tmux_session=X  (compat: sets session_id=X, backend=tmux)
```

---

## Server Integration

### Backend registry on `Server`

```go
type Server struct {
    // ... existing fields ...

    // backends maps backend name -> implementation.
    // Populated at startup. At minimum: {"tmux": &TmuxSessionBackend{}}.
    backends map[string]SessionBackend

    // defaultBackend is the name of the backend to use when none is specified.
    // Defaults to "tmux".
    defaultBackend string
}
```

### Resolving the backend for an agent

Every operation that currently reads `agent.TmuxSession` and calls a tmux function needs to:

1. Read `agent.BackendType` (defaulting to `"tmux"` if empty)
2. Look up the backend in `s.backends[agent.BackendType]`
3. Call the backend method with `agent.SessionID`

Helper:

```go
// backendFor returns the SessionBackend for the given agent.
// Returns the default backend if the agent has no BackendType set.
func (s *Server) backendFor(agent *AgentUpdate) SessionBackend {
    if agent.BackendType != "" {
        if b, ok := s.backends[agent.BackendType]; ok {
            return b
        }
    }
    return s.backends[s.defaultBackend]
}
```

---

## Migration Plan: Which Code Changes

### Phase 1: Interface + TmuxBackend (this PR)

| Current code | Change |
|-------------|--------|
| `tmux.go` top-level functions | Keep as-is. `TmuxSessionBackend` delegates to them. |
| `agent_backend.go` | Replace `AgentBackend` with `SessionBackend`. Remove `TmuxBackend`/`CloudBackend` (superseded). |
| `lifecycle.go` handlers | Route through `s.backendFor(agent)` instead of calling tmux directly |
| `liveness.go` loop | Route through backend instead of calling tmux directly |
| `handlers_agent.go` approve/reply/introspect/tmux-status | Route through backend |
| `tmux.go` broadcast/check-in | Route through backend for sendkeys/idle/approve |
| `types.go` `AgentUpdate` | Rename `TmuxSession` -> `SessionID`, add `BackendType` |
| `db/models.go` | Rename column |
| `db/convert.go` | Update field mappings |
| `handlers_agent.go` ignition | Accept both `?session_id=` and `?tmux_session=` |
| `server.go` | Add `backends` map, initialize with tmux backend |
| Frontend types | Rename `tmux_session` -> `session_id` |

### Phase 2: Ambient Backend (follow-up PR)

Implement `AmbientSessionBackend` using ACP public API. Separate spec.

---

## Handler Migration Details

### `handleAgentSpawn` -> route through backend

```
Before:
  exec.Command("tmux", "new-session", ...)
  tmuxSendKeys(session, command)
  tmuxSendKeys(session, igniteCmd)

After:
  backend := s.backends["tmux"]  // or from request
  sessionID, err := backend.CreateSession(ctx, opts)
  backend.SendInput(sessionID, igniteCmd)
```

### `handleAgentStop` -> route through backend

```
Before:
  tmuxSessionExists(session) -> exec.Command("tmux", "kill-session", ...)

After:
  backend := s.backendFor(agent)
  backend.KillSession(ctx, agent.SessionID)
```

### `handleAgentRestart` -> route through backend

```
Before:
  kill old tmux session -> create new tmux session -> send command + ignite

After:
  backend := s.backendFor(agent)
  backend.KillSession(ctx, agent.SessionID)
  newID, _ := backend.CreateSession(ctx, opts)
  backend.SendInput(newID, igniteCmd)
```

### `handleAgentIntrospect` -> route through backend

```
Before:
  tmuxSessionExists -> tmuxIsIdle -> tmuxCapturePaneLines -> tmuxCheckApproval

After:
  backend := s.backendFor(agent)
  exists := backend.SessionExists(agent.SessionID)
  idle := backend.IsIdle(agent.SessionID)
  lines, _ := backend.CaptureOutput(agent.SessionID, 50)
  approval := backend.CheckApproval(agent.SessionID)
```

### `checkAllSessionLiveness` -> route through backend

```
Before:
  if !tmuxAvailable() { return }
  for each agent with TmuxSession:
    tmuxSessionExists -> tmuxIsIdle -> tmuxCheckApproval

After:
  for each agent with SessionID:
    backend := s.backendFor(agent)
    if !backend.Available() { continue }
    exists := backend.SessionExists(agent.SessionID)
    idle := backend.IsIdle(agent.SessionID)
    approval := backend.CheckApproval(agent.SessionID)
```

### `handleApproveAgent` -> route through backend

```
Before:
  tmuxSessionExists -> tmuxCheckApproval -> tmuxApprove

After:
  backend := s.backendFor(agent)
  backend.SessionExists(agent.SessionID)
  backend.CheckApproval(agent.SessionID)
  backend.Approve(agent.SessionID)
```

### `handleReplyAgent` -> route through backend

```
Before:
  tmuxSessionExists -> tmuxSendKeys(session, message)

After:
  backend := s.backendFor(agent)
  backend.SessionExists(agent.SessionID)
  backend.SendInput(agent.SessionID, message)
```

### `handleSpaceTmuxStatus` -> generalize to `handleSpaceSessionStatus`

Rename route from `/api/tmux-status` to `/api/session-status` (keep `/api/tmux-status` as alias).
Response struct rename `tmuxAgentStatus` -> `agentSessionStatus`.

### `BroadcastCheckIn` / `SingleAgentCheckIn` -> route through backend

```
Before:
  if !tmuxAvailable() { error }
  TmuxAutoDiscover(...)
  tmuxSessionExists -> tmuxIsIdle -> tmuxSendKeys(checkModel) -> waitForIdle -> tmuxSendKeys(check)

After:
  backend := s.backendFor(agent)
  if !backend.Available() { error }
  // discovery only for tmux (other backends register explicitly)
  backend.SessionExists(sessionID)
  backend.IsIdle(sessionID)
  backend.SendInput(sessionID, "/model "+checkModel)
  // waitForIdle uses backend.IsIdle in its poll loop
  backend.SendInput(sessionID, "/boss.check ...")
```

### `TmuxAutoDiscover` -> route through backend

```
Before:
  tmuxListSessions -> parseTmuxAgentName -> match to agents

After:
  backend := s.backends["tmux"]  // discovery is tmux-specific
  discovered := backend.DiscoverSessions()
  // match discovered sessions to agents
```

---

## API Response Changes

### `/api/tmux-status` -> `/api/session-status`

```json
// Before:
{ "agent": "FE", "session": "agentdeck_FE_1", "registered": true, "exists": true, "idle": false, "needs_approval": true }

// After:
{ "agent": "FE", "session_id": "agentdeck_FE_1", "backend": "tmux", "registered": true, "exists": true, "idle": false, "needs_approval": true }
```

### Agent JSON

```json
// Before:
{ "status": "active", "tmux_session": "FE", ... }

// After:
{ "status": "active", "session_id": "FE", "backend_type": "tmux", ... }
```

### Spawn/restart responses

```json
// Before:
{ "ok": true, "tmux_session": "FE" }

// After:
{ "ok": true, "session_id": "FE", "backend": "tmux" }
```

---

## SSE Event Changes

### `tmux_liveness` -> `session_liveness`

Keep `tmux_liveness` as an alias for one release cycle. The payload changes from
`"session": "X"` to `"session_id": "X"` with a new `"backend": "tmux"` field.

---

## Test Strategy

1. **All existing tests pass** after refactoring (behavior-preserving)
2. **New unit tests** for `TmuxSessionBackend` implementing `SessionBackend`
3. **Mock backend** for integration tests that don't require tmux
4. **Backward compat tests** for `tmux_session` JSON field and `?tmux_session=` query param
