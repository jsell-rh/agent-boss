# Session Backend Abstraction — Design Overview

## Spec Documents

| Doc | Contents |
|-----|----------|
| [01-tmux-audit.md](01-tmux-audit.md) | Every tmux touchpoint in the codebase, categorized |
| [02-session-backend-interface.md](02-session-backend-interface.md) | `SessionBackend` interface, data model changes, migration plan |
| [03-tmux-backend.md](03-tmux-backend.md) | `TmuxSessionBackend` implementation, method mapping, file layout |
| [04-ambient-backend.md](04-ambient-backend.md) | `AmbientSessionBackend` implementation, API mapping, behavioral differences |
| [05-agentcore-feasibility.md](05-agentcore-feasibility.md) | AWS Bedrock AgentCore feasibility analysis |

## Summary

### What exists today

- **tmux hardcoded everywhere**: 10+ functions in `tmux.go`, called directly from lifecycle
  handlers, liveness loop, broadcast, introspect, approve, and reply.
- **`AgentBackend` interface** in `agent_backend.go` (PR #47) with `Spawn/Stop/List/Name`.
  Only used by `handleCreateAgents`. All other code bypasses it.
- **`AgentUpdate.TmuxSession`** is the field that links an agent to its session. Used across
  types, DB models, handlers, frontend, scripts, and docs.
- **`tmuxDefaultSession`** (PR #49, open) proposes space-scoped names `{space}-{agent}` — not adopted here.

### What this design introduces

- **`SessionBackend` interface** with 13 methods covering the full surface: identity
  (`Name`, `Available`), lifecycle (`CreateSession`, `KillSession`, `SessionExists`,
  `ListSessions`), status (`GetStatus`), observability (`IsIdle`, `CaptureOutput`,
  `CheckApproval`), interaction (`SendInput`, `Approve`, `Interrupt`), and discovery
  (`DiscoverSessions`).
- **Role interfaces** (`SessionLifecycle`, `SessionObserver`, `SessionActor`) for
  narrow consumer dependencies and easier testing.
- **`TmuxSessionBackend`** — wraps existing tmux functions. Preserves current
  `agentdeck_*` naming convention. Zero behavior change.
- **`AmbientSessionBackend`** — backed by the ACP public API (`POST /sessions`,
  `POST /message`, `GET /output`, `DELETE /sessions/{id}`, `POST /interrupt`, etc.).
  Depends on platform PR #855.
- **Subsumes `AgentBackend`** — the existing interface from PR #47 is folded into
  `SessionBackend`. `agent_backend.go` is deleted.
- **`AgentUpdate.SessionID`** + **`AgentUpdate.BackendType`** — replaces `TmuxSession`
  with backend-agnostic fields. No backward-compat shim (clean break).
- **`Server.backends`** registry — map of backend name to implementation. Agents carry
  their backend type; the server resolves the right implementation per-agent.
- **`SessionStatus`** enum — unified status model (`unknown`, `pending`, `running`, `idle`,
  `completed`, `failed`, `missing`) that all backends map into.
- **`BackendOpts interface{}`** — backend-specific creation options. Each backend defines
  its own options struct (`TmuxCreateOpts`, `AmbientCreateOpts`), keeping backend-specific
  code contained within each backend.

### Interface at a glance

```go
type SessionBackend interface {
    Name() string
    Available() bool

    CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error)
    KillSession(ctx context.Context, sessionID string) error
    SessionExists(sessionID string) bool
    ListSessions() ([]string, error)

    GetStatus(ctx context.Context, sessionID string) (SessionStatus, error)

    IsIdle(sessionID string) bool
    CaptureOutput(sessionID string, lines int) ([]string, error)
    CheckApproval(sessionID string) ApprovalInfo

    SendInput(sessionID string, text string) error
    Approve(sessionID string) error
    Interrupt(ctx context.Context, sessionID string) error

    DiscoverSessions() (map[string]string, error)
}
```

### Scope of changes

| Area | Files affected | Nature of change |
|------|---------------|-----------------|
| Interface definition | New: `session_backend.go` | New file |
| Tmux backend | New: `session_backend_tmux.go` | Wraps existing functions |
| Old backend | Delete: `agent_backend.go` | Superseded (folded into SessionBackend) |
| Tmux primitives | `tmux.go` | Unchanged (kept as unexported helpers) |
| Data model | `types.go`, `db/models.go`, `db/convert.go`, `db_adapter.go` | Rename `TmuxSession` -> `SessionID`, add `BackendType` |
| Server | `server.go` | Add `backends` map, `backendFor()` helper |
| Lifecycle | `lifecycle.go` | Route through backend |
| Liveness | `liveness.go` | Route through backend |
| Handlers | `handlers_agent.go` | Route through backend, rename API endpoint |
| Broadcast | `tmux.go` (orchestration funcs) | Route through backend |
| Frontend | `types/index.ts`, `AgentDetail.vue`, `client.ts` | Rename `tmux_session` -> `session_id` |
| Tests | `server_test.go`, `hierarchy_test.go`, `lifecycle_test.go` | Update field names, add mock backend tests |

### Known gaps (deferred)

| Gap | Notes |
|-----|-------|
| Context/tool injection for Ambient | Ambient sessions don't inherit local boss commands. Needs workflow or MCP server approach. Deferred to Phase 2. |
| Cross-space session name collisions | Current `agentdeck_*` naming doesn't include space. Same agent name in two spaces can collide. PR #49 proposes a fix but is out of scope here. |
| Session ownership/filtering | `tmuxListSessions` returns all sessions, not just agent-boss. Mitigated by naming convention but not solved. |
| Idle detection brittleness | `isShellPrompt` relies on PS1 heuristics. Claude Code hooks would be cleaner. |
| Model switching compaction risk | Switching from opus to haiku with large context triggers compaction. Needs separate evaluation. |
