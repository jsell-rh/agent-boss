# AmbientSessionBackend Design

Implementation of `SessionBackend` backed by the Ambient Code Platform public API.

## Ambient Public API Summary

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/v1/sessions` | GET | List sessions |
| `/v1/sessions` | POST | Create session (task, display_name, model, repos) |
| `/v1/sessions/{id}` | GET | Get session (status: pending/running/completed/failed) |
| `/v1/sessions/{id}` | DELETE | Delete session permanently |
| `/v1/sessions/{id}/message` | POST | Send user message (creates a run) |
| `/v1/sessions/{id}/output` | GET | Get output (transcript/compact/events format) |
| `/v1/sessions/{id}/runs` | GET | List runs (status, timestamps, event counts) |
| `/v1/sessions/{id}/runs` | POST | Create run (low-level AG-UI) |
| `/v1/sessions/{id}/start` | POST | Resume a stopped/completed session |
| `/v1/sessions/{id}/stop` | POST | Stop session (pod terminated, session preserved) |
| `/v1/sessions/{id}/interrupt` | POST | Cancel current run without killing session |

Authentication: Bearer token via `Authorization` header.
Scoping: `X-Ambient-Project` header selects the target namespace.

---

## Conceptual Mapping: Tmux vs Ambient

The two backends have fundamentally different interaction models:

| Concept | Tmux | Ambient |
|---------|------|---------|
| Session identity | tmux session name (local) | Kubernetes resource ID (remote) |
| "Exists" | `tmux list-sessions` contains name | `GET /sessions/{id}` returns 200 |
| "Idle" | Terminal shows shell prompt or Claude `>` | Session is `running` AND latest run is `completed` or `error` (no active run) |
| "Busy" | Terminal shows active output, no prompt | Latest run status is `running` |
| "Capture output" | Read terminal pane lines | Fetch transcript messages, format as lines |
| "Send input" | `tmux send-keys` text + Enter | `POST /sessions/{id}/message` |
| "Approval check" | Parse terminal for "Do you want...?" | Not applicable (sessions run with configured permissions). Returns `NeedsApproval: false`. |
| "Approve" | `tmux send-keys Enter` | Not applicable. No-op. |
| "Kill" | `tmux kill-session` (gone forever) | `POST /sessions/{id}/stop` (session data preserved) |
| "Create" | `tmux new-session -d -s name` | `POST /sessions` (async pod creation) |
| "Discovery" | Parse `agentdeck_*` session names | List sessions, match by `display_name` convention |
| "Interrupt" | `tmux send-keys C-c` | `POST /sessions/{id}/interrupt` |
| "Status" | Inferred from existence + idle + approval | Explicit: pending/running/completed/failed |
| "Resume" | Not possible (create new session) | `POST /sessions/{id}/start` |

---

## Interface Gaps Revealed by Ambient

The Ambient API exposes capabilities that the current `SessionBackend` interface
(from `02-session-backend-interface.md`) does not cover. These need to be added.

### Gap 1: `GetStatus` — structured session status

**Problem:** The current interface only has `SessionExists(id) bool` and `IsIdle(id) bool`.
Ambient sessions have four distinct states (`pending`, `running`, `completed`, `failed`).
The coordinator needs richer status to make correct decisions (e.g., don't send a message
to a `pending` session that hasn't started yet).

**Addition to interface:**

```go
// SessionStatus represents the state of a session.
type SessionStatus string

const (
    SessionStatusUnknown   SessionStatus = "unknown"   // can't determine (e.g., tmux binary missing)
    SessionStatusPending   SessionStatus = "pending"    // created but not yet running
    SessionStatusRunning   SessionStatus = "running"    // session is active
    SessionStatusIdle      SessionStatus = "idle"       // session is running but waiting for input
    SessionStatusCompleted SessionStatus = "completed"  // session finished
    SessionStatusFailed    SessionStatus = "failed"     // session errored
    SessionStatusMissing   SessionStatus = "missing"    // session does not exist
)

// GetStatus returns the current status of a session.
// For tmux: derives from SessionExists + IsIdle + CheckApproval.
// For ambient: maps directly from the API response status field.
GetStatus(ctx context.Context, sessionID string) (SessionStatus, error)
```

**Tmux mapping:**

```
session not found         -> SessionStatusMissing
session exists + idle     -> SessionStatusIdle
session exists + busy     -> SessionStatusRunning
tmux unavailable          -> SessionStatusUnknown
```

**Ambient mapping:**

```
GET /sessions/{id} 404    -> SessionStatusMissing
status: "pending"         -> SessionStatusPending
status: "running" + latest run "running" -> SessionStatusRunning
status: "running" + latest run "completed"/"error"/no runs -> SessionStatusIdle
status: "completed"       -> SessionStatusCompleted
status: "failed"          -> SessionStatusFailed
API error                 -> SessionStatusUnknown
```

### Gap 2: `Interrupt` — cancel current work without killing session

**Problem:** Ambient has `POST /sessions/{id}/interrupt` to cancel the current run
while keeping the session alive. This is semantically different from both `KillSession`
(destroys the session) and `SendInput` (sends new work). The coordinator needs this
for scenarios like: "agent is stuck on a bad task, cancel and reassign."

**Addition to interface:**

```go
// Interrupt cancels the session's current work without killing the session.
// The session remains alive and can accept new messages.
// For tmux: sends Ctrl-C (C-c) to the session.
// For ambient: calls POST /sessions/{id}/interrupt.
Interrupt(ctx context.Context, sessionID string) error
```

### Gap 3: `SessionCreateOpts` needs ambient-specific fields

**Problem:** Ambient sessions need `task` (initial prompt), `display_name`, `model`,
and `repos`. The current `SessionCreateOpts` only has tmux-specific fields (`Width`,
`Height`).

**Solution:** Make `SessionCreateOpts` a union of all backend fields. Each backend
ignores fields it doesn't use.

```go
type SessionCreateOpts struct {
    // Common
    SessionID string // desired session name/ID
    Command   string // tmux: shell command; ambient: mapped to task

    // Tmux-specific (ignored by ambient)
    WorkDir string
    Width   int
    Height  int

    // Ambient-specific (ignored by tmux)
    DisplayName string            // human-readable session name
    Model       string            // Claude model to use
    Repos       []SessionRepo     // repositories to clone
}

type SessionRepo struct {
    URL    string `json:"url"`
    Branch string `json:"branch,omitempty"`
}
```

### Non-Gap: `CheckApproval` and `Approve`

These are tmux-specific concepts. Ambient sessions run with configured permissions
and don't present terminal approval prompts. The Ambient backend returns no-op values:

- `CheckApproval` -> `ApprovalInfo{NeedsApproval: false}`
- `Approve` -> `nil` (no-op)

This is correct behavior, not a gap. The coordinator already checks `NeedsApproval`
before acting, so a backend that never needs approval simply never triggers that path.

### Non-Gap: Resume

Ambient supports `POST /sessions/{id}/start` to resume a stopped session. Tmux does not
(you create a new session). This is valuable but not needed in the interface for the
initial implementation — the coordinator's `handleAgentRestart` already does
kill + create, which works for both backends. Resume can be added later as an optimization.

---

## AmbientSessionBackend Implementation

### Configuration

```go
type AmbientSessionBackend struct {
    apiURL     string       // e.g., "https://public-api-ambient-code.apps.okd1.timslab/v1"
    token      string       // Bearer token
    project    string       // X-Ambient-Project header value
    httpClient *http.Client // with timeouts
}

type AmbientBackendConfig struct {
    APIURL  string `json:"api_url"`
    Token   string `json:"token"`
    Project string `json:"project"`
}

func NewAmbientSessionBackend(cfg AmbientBackendConfig) *AmbientSessionBackend {
    return &AmbientSessionBackend{
        apiURL:  strings.TrimRight(cfg.APIURL, "/"),
        token:   cfg.Token,
        project: cfg.Project,
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }
}
```

### Method Implementations

#### `Name() string`

```go
func (b *AmbientSessionBackend) Name() string { return "ambient" }
```

#### `Available() bool`

Calls `GET /sessions` with a short timeout. Returns true if 200, false otherwise.
Caches result for 30 seconds to avoid hammering the API on every liveness tick.

```go
func (b *AmbientSessionBackend) Available() bool {
    // Check cached result (30s TTL)
    // If stale: GET /sessions, check for 200
    // Note: any 2xx/4xx means the API is reachable (available).
    // Only network errors or 502 mean unavailable.
}
```

#### `CreateSession(ctx, opts) (string, error)`

Maps to `POST /v1/sessions`.

```go
func (b *AmbientSessionBackend) CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error) {
    body := map[string]interface{}{
        "task": opts.Command, // Command maps to the initial task/prompt
    }
    if opts.DisplayName != "" {
        body["display_name"] = opts.DisplayName
    } else if opts.SessionID != "" {
        body["display_name"] = opts.SessionID
    }
    if opts.Model != "" {
        body["model"] = opts.Model
    }
    if len(opts.Repos) > 0 {
        body["repos"] = opts.Repos
    }

    // POST /v1/sessions
    // Returns {"id": "session-abc123", "message": "Session created"}
    // Return the session ID
}
```

**Note on `Command` -> `task` mapping:** For tmux, `Command` is the shell command
to execute (e.g., `claude --dangerously-skip-permissions`). For Ambient, the platform
handles launching Claude — `Command` is repurposed as the initial prompt/task. If
the caller sets `opts.Command` to a shell command, it becomes the session's task.
This is acceptable because agents spawned through the coordinator always get an
ignite prompt as their first message anyway.

#### `KillSession(ctx, id) error`

Maps to `POST /v1/sessions/{id}/stop`. Does NOT delete — preserves session data.

```go
func (b *AmbientSessionBackend) KillSession(ctx context.Context, sessionID string) error {
    // POST /v1/sessions/{id}/stop
    // Accept 202 (stopped) or 422 (already stopped) as success
}
```

#### `SessionExists(id) bool`

Maps to `GET /v1/sessions/{id}`. Returns true for any status; false on 404.

```go
func (b *AmbientSessionBackend) SessionExists(sessionID string) bool {
    // GET /v1/sessions/{id}
    // 200 -> true (any status counts as "exists")
    // 404 -> false
    // Error -> false
}
```

#### `ListSessions() ([]string, error)`

Maps to `GET /v1/sessions`. Returns IDs of all sessions in the project.

```go
func (b *AmbientSessionBackend) ListSessions() ([]string, error) {
    // GET /v1/sessions
    // Extract .items[].id
}
```

#### `IsIdle(id) bool`

Checks if the session is `running` but has no active run.

```go
func (b *AmbientSessionBackend) IsIdle(sessionID string) bool {
    // Step 1: GET /v1/sessions/{id} -> check status == "running"
    //   If not running -> not idle (it's stopped, pending, or failed)
    //
    // Step 2: GET /v1/sessions/{id}/runs -> check latest run
    //   If no runs exist -> idle (session running, nothing to do)
    //   If latest run status == "completed" or "error" -> idle
    //   If latest run status == "running" -> not idle (working)
}
```

#### `CaptureOutput(id, lines) ([]string, error)`

Maps to `GET /v1/sessions/{id}/output?format=transcript`. Formats the last N
transcript messages as human-readable lines (matching the `[]string` contract).

```go
func (b *AmbientSessionBackend) CaptureOutput(sessionID string, lines int) ([]string, error) {
    // GET /v1/sessions/{id}/output?format=transcript
    // Format each message as: "[role] content" (truncated to ~200 chars)
    // Return last N lines
    //
    // Example output:
    //   "[user] /boss.check FE my-project"
    //   "[assistant] I'll check in now. Reading the blackboard..."
    //   "[tool] Bash: curl -s http://localhost:8899/spaces/my-project/raw"
    //   "[assistant] Posted status update to the blackboard."
}
```

#### `CheckApproval(id) ApprovalInfo`

Ambient sessions run with configured permissions. No terminal approval prompts.

```go
func (b *AmbientSessionBackend) CheckApproval(sessionID string) ApprovalInfo {
    return ApprovalInfo{NeedsApproval: false}
}
```

#### `SendInput(id, text) error`

Maps to `POST /v1/sessions/{id}/message`.

```go
func (b *AmbientSessionBackend) SendInput(sessionID string, text string) error {
    // POST /v1/sessions/{id}/message
    // Body: {"content": text}
    // Accept 202 as success
    // Return error on 422 (session not running) — caller should check status
}
```

#### `Approve(id) error`

No-op for Ambient.

```go
func (b *AmbientSessionBackend) Approve(sessionID string) error {
    return nil // Ambient sessions don't have terminal approval prompts
}
```

#### `DiscoverSessions() (map[string]string, error)`

Lists all sessions and matches by `display_name`. Convention: sessions created by
agent-boss use `display_name` = agent name.

```go
func (b *AmbientSessionBackend) DiscoverSessions() (map[string]string, error) {
    // GET /v1/sessions
    // For each session where status is "running":
    //   discovered[session.display_name] = session.id
    // Return map
}
```

#### `GetStatus(ctx, id) (SessionStatus, error)`

Maps directly from the API response.

```go
func (b *AmbientSessionBackend) GetStatus(ctx context.Context, sessionID string) (SessionStatus, error) {
    // GET /v1/sessions/{id}
    // 404 -> SessionStatusMissing, nil
    // 200 -> map status field:
    //   "pending"   -> SessionStatusPending
    //   "completed" -> SessionStatusCompleted
    //   "failed"    -> SessionStatusFailed
    //   "running"   -> check runs:
    //     latest run "running" -> SessionStatusRunning
    //     else                 -> SessionStatusIdle
    // Error -> SessionStatusUnknown, err
}
```

#### `Interrupt(ctx, id) error`

Maps to `POST /v1/sessions/{id}/interrupt`.

```go
func (b *AmbientSessionBackend) Interrupt(ctx context.Context, sessionID string) error {
    // POST /v1/sessions/{id}/interrupt
    // Accept 200 as success
}
```

---

## Behavioral Differences from Tmux

### 1. Asynchronous session creation

Tmux `CreateSession` is synchronous — by the time it returns, the tmux session
exists and the command is running. Ambient `CreateSession` is asynchronous — the
API returns a session ID immediately, but the pod may take seconds to start
(status transitions: `pending` -> `running`).

**Impact on coordinator:** After `CreateSession`, the coordinator currently sends
an ignite command after a 5-second sleep. For Ambient, it should poll `GetStatus`
until the session reaches `running` (or `idle`) before sending the first message.

```go
// After creating an ambient session:
for i := 0; i < 30; i++ {  // up to 60s
    status, _ := backend.GetStatus(ctx, sessionID)
    if status == SessionStatusRunning || status == SessionStatusIdle {
        break
    }
    time.Sleep(2 * time.Second)
}
```

### 2. No terminal = no idle heuristics

Tmux idle detection reads terminal output and matches against patterns (shell
prompts, Claude `>` indicator, status bar keywords). This is inherently heuristic.

Ambient idle detection is structural: check the session status and the latest run
status. It's deterministic — no false positives from prompt-like text in output.

### 3. `SendInput` creates a run

In tmux, `SendInput` types text into a terminal. The text could be anything — a
slash command, a prompt, arbitrary keystrokes. There's no concept of "runs."

In Ambient, `SendInput` calls `POST /message`, which creates a new AG-UI run.
Each `SendInput` call is a discrete unit of work with its own run ID, start time,
end time, and event stream. This is important for:

- **Broadcast check-in:** Each `/boss.check` creates a run. The coordinator can
  poll `GET /runs` to know exactly when the check-in completed (run status =
  `completed`) instead of heuristically waiting for idle.
- **Model switching:** `/model sonnet` as a tmux command becomes an Ambient
  message. This may not work as intended — Ambient sessions have a fixed model
  set at creation. Model switching may need to be a no-op or handled differently.

### 4. No approval flow

Ambient sessions run with the permissions configured at session creation. There
are no interactive approval prompts. The entire approval detection + interrupt
ledger pipeline in the liveness loop becomes a no-op for Ambient agents.

### 5. Persistent sessions

Tmux sessions are ephemeral — if the machine reboots, they're gone. Ambient
sessions are persistent Kubernetes resources with stored state. `KillSession`
(= stop) preserves the session and its history. This means:

- Sessions can be resumed (`POST /start`) after being stopped
- Session output/history survives restarts
- The coordinator could reconnect to existing sessions after its own restart

---

## Configuration and Initialization

### Environment variables

```bash
AMBIENT_API_URL=https://public-api-ambient-code.apps.okd1.timslab/v1
AMBIENT_TOKEN=<bearer-token>
AMBIENT_PROJECT=my-project
```

### Server initialization

```go
func NewServer(port, dataDir string) *Server {
    s := &Server{
        // ... existing fields ...
        backends:       make(map[string]SessionBackend),
        defaultBackend: "tmux",
    }

    // Always register tmux backend
    s.backends["tmux"] = NewTmuxSessionBackend()

    // Register ambient backend if configured
    if apiURL := os.Getenv("AMBIENT_API_URL"); apiURL != "" {
        cfg := AmbientBackendConfig{
            APIURL:  apiURL,
            Token:   os.Getenv("AMBIENT_TOKEN"),
            Project: os.Getenv("AMBIENT_PROJECT"),
        }
        s.backends["ambient"] = NewAmbientSessionBackend(cfg)

        // If ambient is configured and tmux is not available,
        // default to ambient
        if !s.backends["tmux"].Available() {
            s.defaultBackend = "ambient"
        }
    }

    return s
}
```

---

## Impact on Broadcast / Check-In

The broadcast system (`BroadcastCheckIn`, `runAgentCheckIn`) is the most complex
tmux-dependent flow. Here's how it adapts for Ambient:

### Current tmux flow (per agent)

```
1. Switch to check model:   tmuxSendKeys("/model haiku")
2. Wait for idle:           poll tmuxIsIdle() every 3s
3. Send check-in:           tmuxSendKeys("/boss.check Agent Space")
4. Wait for board post:     poll agentUpdatedAt() every 3s
5. Restore work model:      tmuxSendKeys("/model sonnet")
6. Wait for idle:           poll tmuxIsIdle() every 3s
```

### Ambient adaptation

```
1. Skip model switch:       Ambient sessions have a fixed model (or handle it via message)
2. Check status:            backend.GetStatus() == idle
3. Send check-in:           backend.SendInput("/boss.check Agent Space")
4. Wait for board post:     poll agentUpdatedAt() every 3s (same — blackboard is boss-side)
5. Skip model restore:      (see step 1)
6. Check status:            backend.GetStatus() == idle
```

The key difference: steps 1 and 5 (model switching) may be no-ops for Ambient.
The coordinator should check `backend.Name()` and skip model switching for
non-tmux backends, or attempt it as a message and tolerate failure.

---

## File Layout

```
internal/coordinator/
  session_backend.go              # Interface, types, SessionStatus
  session_backend_tmux.go         # TmuxSessionBackend
  session_backend_ambient.go      # AmbientSessionBackend (this spec)
  tmux.go                         # Low-level tmux primitives (unchanged)
```
