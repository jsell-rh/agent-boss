# TmuxSessionBackend Design

Implementation of `SessionBackend` that wraps the existing tmux functions with zero behavior change.

## Struct

```go
// TmuxSessionBackend implements SessionBackend using local tmux sessions.
type TmuxSessionBackend struct {
    // sessionAliases maps tmux session name fragments to canonical agent names.
    // Used by DiscoverSessions to match agentdeck_* session names.
    // e.g., "control-plane" -> "CP", "boss-app" -> "" (skip)
    sessionAliases map[string]string
}
```

## Method Mapping

Every method delegates to an existing function from `tmux.go`. No new tmux logic is introduced
except `GetStatus` (composite of existing checks) and `Interrupt` (new: sends Escape key).

| SessionBackend method | Delegates to | Notes |
|----------------------|-------------|-------|
| `Name()` | — | Returns `"tmux"` |
| `Available()` | `tmuxAvailable()` | `exec.LookPath("tmux")` |
| `CreateSession(ctx, opts)` | `exec.Command("tmux", "new-session", ...)` + `tmuxSendKeys` | Extracted from `handleAgentSpawn` and `TmuxBackend.Spawn` |
| `KillSession(ctx, id)` | `exec.Command("tmux", "kill-session", ...)` | Extracted from `handleAgentStop` |
| `SessionExists(id)` | `tmuxSessionExists(id)` | Unchanged |
| `ListSessions()` | `tmuxListSessions()` | Unchanged |
| `GetStatus(ctx, id)` | `tmuxSessionExists` + `tmuxIsIdle` | New composite: missing/idle/running/unknown |
| `IsIdle(id)` | `tmuxIsIdle(id)` | Unchanged — all idle detection logic preserved |
| `CaptureOutput(id, n)` | `tmuxCapturePaneLines(id, n)` | Unchanged |
| `CheckApproval(id)` | `tmuxCheckApproval(id)` | Returns exported `ApprovalInfo` instead of `approvalInfo` |
| `SendInput(id, text)` | `tmuxSendKeys(id, text)` | Unchanged |
| `Approve(id)` | `tmuxApprove(id)` | Unchanged |
| `Interrupt(ctx, id)` | `tmux send-keys -t id Escape` | New: sends Escape key (Claude Code interrupt). Not Ctrl-C. |
| `DiscoverSessions()` | `tmuxListSessions()` + `parseTmuxAgentName()` | Returns `map[agentName]sessionID` using existing `agentdeck_*` naming |

## Session Naming

Session naming is **unchanged** from the current codebase. Sessions use the
`agentdeck_{name}_{timestamp}` convention, parsed by `parseTmuxAgentName()`.

### Known issue: cross-space collisions

The current naming convention does not include the space name. If the same agent
name (e.g., "FE") exists in two spaces, their sessions could collide. PR #49
(open) proposes `tmuxDefaultSession(space, agent)` → `{space}-{agent}` to fix
this, but that change is **out of scope** for this refactoring.

This must be resolved before multi-space deployments are common. Options include:
- Adopt PR #49's `{space}-{agent}` convention
- Use `agentdeck_{space}_{agent}_{timestamp}` to preserve backward compat with discovery
- Add `Space` to `SessionCreateOpts` so the backend can incorporate it

When the naming convention does change, `DiscoverSessions()` and
`parseTmuxAgentName()` will need corresponding updates.

## Implementation

```go
func NewTmuxSessionBackend() *TmuxSessionBackend {
    return &TmuxSessionBackend{
        sessionAliases: map[string]string{
            "control-plane": "CP",
            "boss-app":      "", // skip
        },
    }
}

func (b *TmuxSessionBackend) Name() string { return "tmux" }

func (b *TmuxSessionBackend) Available() bool {
    return tmuxAvailable()
}

func (b *TmuxSessionBackend) CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error) {
    sessionID := opts.SessionID
    if sessionID == "" {
        return "", fmt.Errorf("session ID is required")
    }
    if tmuxSessionExists(sessionID) {
        return "", fmt.Errorf("tmux session %q already exists", sessionID)
    }

    // Extract tmux-specific options
    var tmuxOpts TmuxCreateOpts
    if opts.BackendOpts != nil {
        if to, ok := opts.BackendOpts.(TmuxCreateOpts); ok {
            tmuxOpts = to
        }
    }

    width := tmuxOpts.Width
    if width <= 0 {
        width = 220
    }
    height := tmuxOpts.Height
    if height <= 0 {
        height = 50
    }

    createCtx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
    defer cancel()
    if err := exec.CommandContext(createCtx, "tmux", "new-session", "-d", "-s", sessionID,
        "-x", fmt.Sprintf("%d", width), "-y", fmt.Sprintf("%d", height)).Run(); err != nil {
        return "", fmt.Errorf("create tmux session: %w", err)
    }

    // cd to work dir if specified
    if tmuxOpts.WorkDir != "" {
        time.Sleep(300 * time.Millisecond)
        if err := tmuxSendKeys(sessionID, "cd "+shellQuote(tmuxOpts.WorkDir)); err != nil {
            exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionID).Run()
            return "", fmt.Errorf("cd to workdir: %w", err)
        }
    }

    // Launch command
    if opts.Command != "" {
        time.Sleep(300 * time.Millisecond)
        if err := tmuxSendKeys(sessionID, opts.Command); err != nil {
            exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionID).Run()
            return "", fmt.Errorf("launch command: %w", err)
        }
    }

    return sessionID, nil
}

func (b *TmuxSessionBackend) KillSession(ctx context.Context, sessionID string) error {
    killCtx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
    defer cancel()
    return exec.CommandContext(killCtx, "tmux", "kill-session", "-t", sessionID).Run()
}

func (b *TmuxSessionBackend) SessionExists(sessionID string) bool {
    return tmuxSessionExists(sessionID)
}

func (b *TmuxSessionBackend) ListSessions() ([]string, error) {
    return tmuxListSessions()
}

func (b *TmuxSessionBackend) GetStatus(ctx context.Context, sessionID string) (SessionStatus, error) {
    if !b.Available() {
        return SessionStatusUnknown, nil
    }
    if !tmuxSessionExists(sessionID) {
        return SessionStatusMissing, nil
    }
    if tmuxIsIdle(sessionID) {
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
    result := tmuxCheckApproval(sessionID)
    return ApprovalInfo(result)  // type alias or direct conversion
}

func (b *TmuxSessionBackend) SendInput(sessionID string, text string) error {
    return tmuxSendKeys(sessionID, text)
}

func (b *TmuxSessionBackend) Approve(sessionID string) error {
    return tmuxApprove(sessionID)
}

func (b *TmuxSessionBackend) Interrupt(ctx context.Context, sessionID string) error {
    // Claude Code uses Escape to interrupt, not Ctrl-C.
    interruptCtx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
    defer cancel()
    return exec.CommandContext(interruptCtx, "tmux", "send-keys", "-t", sessionID, "Escape").Run()
}

func (b *TmuxSessionBackend) DiscoverSessions() (map[string]string, error) {
    sessions, err := tmuxListSessions()
    if err != nil {
        return nil, err
    }
    discovered := make(map[string]string)
    for _, session := range sessions {
        name := parseTmuxAgentName(session)
        if name == "" {
            continue
        }
        // Apply aliases
        if alias, ok := b.sessionAliases[name]; ok {
            if alias == "" {
                continue // skip
            }
            name = alias
        }
        discovered[name] = session
    }
    return discovered, nil
}
```

## What stays in `tmux.go`

The low-level functions remain in `tmux.go` as unexported helpers:

- `tmuxAvailable()`
- `tmuxListSessions()`
- `tmuxSessionExists(session)`
- `tmuxCapturePaneLines(session, n)`
- `tmuxCapturePaneLastLine(session)` — only used by tmux-status handler, can stay
- `tmuxIsIdle(session)`
- `lineIsIdleIndicator(line)` — pure function, stays
- `isShellPrompt(line)` — pure function, stays (see idle detection note below)
- `tmuxCheckApproval(session)`
- `tmuxApprove(session)`
- `tmuxSendKeys(session, text)`
- `parseTmuxAgentName(session)` — used by DiscoverSessions
- `shellQuote(s)` — used by CreateSession

### Idle detection brittleness

`isShellPrompt` and `lineIsIdleIndicator` rely on heuristic terminal output
matching (checking for `$`, `%`, `>`, `#` as prompt characters). This is
inherently fragile — non-standard PS1 configurations will break it.

A cleaner approach for future work would be to use
[Claude Code hooks](https://code.claude.com/docs/en/hooks) to emit structured
idle/busy signals instead of parsing terminal output. This is out of scope for
the current refactoring but noted as a known limitation.

## What moves out of `tmux.go`

These functions currently in `tmux.go` are **coordinator-level orchestration**, not tmux primitives.
They move to the coordinator layer and use the `SessionBackend` interface:

| Function | New location | Reason |
|----------|-------------|--------|
| `waitForIdle(session, timeout)` | Stays in coordinator, calls `backend.IsIdle()` in loop | Orchestration, not tmux |
| `waitForBoardPost(...)` | Stays as-is (already not tmux-specific) | Not tmux-related |
| `BroadcastCheckIn(...)` | Stays in coordinator, routes through backend | Orchestration |
| `SingleAgentCheckIn(...)` | Stays in coordinator, routes through backend | Orchestration |
| `runAgentCheckIn(...)` | Stays in coordinator, routes through backend | Orchestration |
| `BroadcastResult` + helpers | Stay as-is (not tmux-specific) | Data types |
| `TmuxAutoDiscover(...)` | Becomes `AutoDiscoverSessions(...)`, uses `backend.DiscoverSessions()` | Generalized |

## What gets deleted

| Item | Reason |
|------|--------|
| `agent_backend.go` `AgentBackend` interface | Superseded by `SessionBackend` |
| `agent_backend.go` `TmuxBackend` struct | Superseded by `TmuxSessionBackend` |
| `agent_backend.go` `CloudBackend` struct | Superseded by future `AmbientSessionBackend` |
| `agent_backend.go` `AgentSpec` struct | Replaced by `SessionCreateOpts` + `TmuxCreateOpts` |
| `agent_backend.go` `AgentInfo` struct | No longer needed; `SessionID` + `BackendType` on agent record |
| `agent_backend.go` `tmuxDefaultSession` | Out of scope for this refactoring (PR #49 concern) |
| `agent_backend.go` `shellQuote` | Moves to `session_backend_tmux.go` |
| `tmuxSessionAliases` global var | Moves into `TmuxSessionBackend.sessionAliases` field |

## Session Ownership / Filtering

Currently `tmuxListSessions()` returns ALL tmux sessions on the machine, not just
agent-boss sessions. This is a pre-existing issue that the refactoring preserves
but does not fix.

Sessions are identified by naming convention only (`agentdeck_{name}_{timestamp}`).
For stronger ownership guarantees, a future enhancement could:
- Tag sessions with a tmux environment variable (e.g., `@agent_boss=true`)
- Use a dedicated tmux server socket (`tmux -L agent-boss`)

This is out of scope for the current refactoring.

## File Layout After Refactoring

```
internal/coordinator/
  session_backend.go         # SessionBackend interface, SessionCreateOpts, ApprovalInfo, role interfaces
  session_backend_tmux.go    # TmuxSessionBackend implementation + shellQuote
  tmux.go                    # Low-level tmux primitives (unchanged, unexported)
  # agent_backend.go         # DELETED (superseded)
```
