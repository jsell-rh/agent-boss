# Agent Lifecycle & Introspection Specification

**Branch:** `feat/lifecycle-introspection`
**Author:** UXSME
**Status:** Final — reflects implemented code

---

## 1. Agent Lifecycle States

### 1.1 Self-Reported Status (`AgentUpdate.Status`)

Agents set their own status via POST to their channel. The server validates and stores it.

| Status | Emoji | Meaning |
|--------|-------|---------|
| `active` | 🟢 | Agent is actively working |
| `blocked` | 🔴 | Agent is blocked on a dependency or decision |
| `done` | ✅ | Agent has completed its work |
| `idle` | ⏸️ | Agent is waiting for new instructions |
| `error` | ❌ | Agent encountered an unrecoverable error |

Validation: `AgentStatus.Valid()` — any other value is rejected with HTTP 400.

### 1.2 Server-Inferred Status (`AgentUpdate.InferredStatus`)

The server independently observes tmux pane output and stores an inferred status. This does **not** override the self-reported `Status` field — it is additive metadata.

| Inferred Value | Condition |
|----------------|-----------|
| `session_missing` | No tmux session registered, or session does not exist |
| `waiting_approval` | Session exists and pane shows a "Do you want to…?" approval prompt |
| `idle` | Session exists, pane shows prompt/shell indicators |
| `working` | Session exists, pane shows active output (no idle indicators) |

Inference function: `inferAgentStatus(exists, idle, needsApproval bool) string` in `lifecycle.go`.

### 1.3 Staleness (`AgentUpdate.Stale`)

A boolean field set by the server. An agent is marked stale when its `UpdatedAt` timestamp is older than `StalenessThreshold` (currently **15 minutes**).

**Rules:**
- Only `active` and `blocked` agents can be marked stale
- `done` and `idle` agents are explicitly exempt — they are expected to be quiet
- `error` agents can be marked stale (they should self-recover or be restarted)
- Staleness clears automatically on the next self-report from the agent
- Staleness is checked every **60 seconds** by the liveness ticker

---

## 2. Lifecycle Management Endpoints

### 2.1 Spawn Agent

```
POST /spaces/{space}/agent/{name}/spawn
```

Creates a tmux session, launches the agent command, and sends the `/boss.ignite` prompt.

**Request body (optional JSON):**
```json
{
  "session_id": "MySession",          // defaults to agent name
  "command": "claude --dangerously-skip-permissions",  // default
  "width": 220,                          // default
  "height": 50                           // default
}
```

**Response (200 OK):**
```json
{
  "ok": true,
  "agent": "AgentName",
  "session_id": "MySession",
  "space": "SpaceName"
}
```

**Error cases:**
- `405 Method Not Allowed` — non-POST request
- `409 Conflict` — tmux session already exists
- `500 Internal Server Error` — tmux creation or command launch failed

**Behavior:**
1. Creates a detached tmux session (`tmux new-session -d`)
2. Sends the agent command via `tmux send-keys` (300ms delay before send)
3. Waits 5 seconds for the agent to initialize
4. Sends `/boss.ignite "{name}" "{space}"` to the session
5. Registers the session name on the agent record (creates agent if not found)
6. Broadcasts `agent_spawned` SSE event

**Important timing note:** The spawn handler blocks for ~5.3 seconds (300ms + 5s sleep) on each call. Callers should use a timeout of at least 15 seconds.

### 2.2 Stop Agent

```
POST /spaces/{space}/agent/{name}/stop
```

Kills the agent's tmux session and marks the agent as `done`.

**Response (200 OK):**
```json
{
  "ok": true,
  "agent": "canonical-name"
}
```

**Error cases:**
- `404 Not Found` — space or agent not found
- `400 Bad Request` — agent has no registered tmux session
- `404 Not Found` — tmux session no longer exists
- `500 Internal Server Error` — kill failed

**Behavior:**
1. Resolves agent canonical name
2. Kills the tmux session (`tmux kill-session -t`)
3. Sets `Status = done`, `TmuxSession = ""`, updates `UpdatedAt`
4. Broadcasts `agent_stopped` SSE event

### 2.3 Restart Agent

```
POST /spaces/{space}/agent/{name}/restart
```

Kills the existing session (if any) and spawns a new one. Agent data is preserved.

**Response (200 OK):**
```json
{
  "ok": true,
  "agent": "canonical-name",
  "session_id": "new-session-name"
}
```

**Session naming:** Uses canonical agent name as the new session name. If that name is already taken by another session, appends `-new` suffix.

**Behavior:**
1. Kills old session if it exists (1 second wait after kill)
2. Creates new tmux session with canonical name
3. Launches `claude --dangerously-skip-permissions` (hardcoded, unlike spawn which allows override)
4. Waits 5 seconds, sends `/boss.ignite`
5. Updates `TmuxSession`, sets `Status = idle`, updates `UpdatedAt`
6. Broadcasts `agent_restarted` SSE event

**Gap:** Unlike `handleAgentSpawn`, restart does not accept a custom command. Both hardcode `220x50` dimensions. This is intentional for simplicity but limits non-Claude agent restarts.

### 2.4 Introspect Agent

```
GET /spaces/{space}/agent/{name}/introspect
```

Captures the agent's current tmux pane output and returns it with inferred state.

**Response (200 OK):**
```json
{
  "agent": "canonical-name",
  "session_id": "session-name",
  "session_exists": true,
  "idle": false,
  "needs_approval": true,
  "tool_name": "Bash",
  "prompt_text": "Bash | rm -rf /tmp/data | Do you want to proceed?",
  "lines": ["last 50 lines of pane output..."],
  "captured_at": "2026-03-06T23:45:00Z"
}
```

**Error cases:**
- `404 Not Found` — space or agent not found
- `405 Method Not Allowed` — non-GET request

**Behavior:**
1. Looks up agent's registered tmux session
2. If session exists: captures last 50 lines, checks idle/approval state
3. If agent needs approval: parses tool name and prompt text from pane
4. Returns all data regardless of session state (graceful degradation)

**When session is missing or agent has no session:**
- `session_exists: false`
- `lines: []` (empty, never null)
- `idle`, `needs_approval` remain `false`

---

## 3. Idle Detection Logic

The `tmuxIsIdle()` function in `tmux.go` determines whether a tmux session is waiting for user input. It reads the last 10 non-empty lines and returns `true` if any line matches an idle indicator.

### Idle Indicators (in priority order)

| Pattern | Description |
|---------|-------------|
| Inner text is `>` or `> ` | Claude Code / opencode TUI prompt |
| Line starts with `❯` | Claude Code prompt (may have suggestion text) |
| Line ends with `$`, `❯`, `»` | Unambiguous shell prompts |
| Line ends with `>` (not `=>` or `->`) | Shell prompt |
| Line ends with `%` or `#` not preceded by digit | Shell prompt |
| Contains `ctrl+p commands` | OpenCode status bar |
| Contains `-- INSERT --` or `-- NORMAL --` | Vim mode indicator |
| Contains `waiting for input`, `ready`, `type a message`, `press enter` | Generic idle text |
| Starts with `?` and contains `for shortcuts` | Claude Code hint line |
| Contains `auto-compact` or `auto-accept` | Claude Code status text |

**Design principle:** Intentionally generous — "idle" only when there is positive evidence of waiting. Uncertain state defaults to "busy" to avoid interrupting active work.

### Approval Detection

`tmuxCheckApproval()` scans the last 60 lines for a "Do you want…?" question followed by numbered choices (e.g., `1. Yes`, `❯`). If found, it backtraces to extract the tool name and prompt text.

Recognized tool names: `Bash`, `Read`, `Write`, `Edit`, `MultiEdit`, `Glob`, `Grep`, `WebFetch`, `NotebookEdit`, `Task`.

---

## 4. Message Priority System

Messages sent to agents via `POST /spaces/{space}/agent/{name}/message` support a `priority` field.

### Priority Levels

| Priority | Value | Use Case |
|----------|-------|----------|
| `info` | Default | Routine updates, FYI messages |
| `directive` | Medium | Task assignments, work orders |
| `urgent` | High | Blockers, immediate action required |

### Validation Rules

- Empty or absent `priority` field defaults to `info`
- Any other value returns HTTP 400 with a descriptive error
- Priority is stored on `AgentMessage.Priority` and rendered in `/raw`

### Message Structure

```go
type AgentMessage struct {
    ID        string          // unique message ID
    Message   string          // message text
    Sender    string          // from X-Agent-Name header
    Priority  MessagePriority // info | directive | urgent
    Timestamp time.Time
}
```

**Note:** Message history is stored inline on the `AgentUpdate` struct. There is a cap (see server.go) on message history length per agent to prevent unbounded growth.

---

## 5. Auto-Status Inference Loop

The server runs a liveness loop that performs two periodic tasks:

### 5.1 Staleness Ticker (every 60 seconds)

Calls `checkStaleness()` which iterates all agents across all spaces and:
1. Skips `done` and `idle` agents (clears `Stale` if previously set)
2. Marks `active`/`blocked`/`error` agents as `Stale = true` if `now - UpdatedAt > 15min`
3. Logs a staleness event on state change
4. Saves the space to disk if any agent changed

### 5.2 Tmux Liveness (existing, not modified by this branch)

The existing liveness loop checks registered tmux sessions and updates `InferredStatus`. The lifecycle branch adds `checkStaleness` to this loop without replacing the existing logic.

---

## 6. Design Assessments & Gaps

### 6.1 Blocking HTTP Handlers

**Issue:** `handleAgentSpawn` and `handleAgentRestart` block for ~5.3 seconds per call due to `time.Sleep(5 * time.Second)` waiting for agent initialization before sending ignite. This ties up an HTTP goroutine and the client connection.

**Recommendation:** Return 202 Accepted immediately and perform the ignite send asynchronously in a background goroutine, with the result available via SSE (`agent_spawned` event already exists).

### 6.2 Restart Does Not Accept Custom Command

**Issue:** `handleAgentRestart` hardcodes `claude --dangerously-skip-permissions`. This is inconsistent with `handleAgentSpawn` which accepts a `command` parameter. An agent restarted via the API cannot customize its launch command.

**Recommendation:** Accept the same `spawnRequest` body in restart, or look up the previously-used command from the agent record.

### 6.3 Session Name Collision on Restart

**Issue:** If the canonical session name is taken, restart appends `-new`. This is not idempotent — a second restart would try the canonical name again (which may now be free if the previous session was killed). Works correctly in practice but the logic is fragile.

### 6.4 No Test Coverage for Live Tmux Operations

Spawn, stop, and restart endpoint tests only cover method validation and 404 cases (8 tests total, all tmux-free). Live tmux behavior is untested in CI. This is acceptable for now since tmux is not available in most CI environments, but integration tests should be flagged with a build tag.

### 6.5 InferredStatus Not Persisted from Liveness Loop

`InferredStatus` is set by the liveness loop but may not be saved to disk depending on whether the loop calls `saveSpace`. Confirm the liveness loop path updates and persists `InferredStatus` to ensure the dashboard reflects it after restart.

### 6.6 StalenessThreshold as a Constant

`StalenessThreshold = 15 * time.Minute` is hardcoded. For teams with different cadences (e.g., a long-running analysis agent that posts every 30 minutes), this will generate false positives.

**Recommendation:** Make staleness threshold configurable per-agent via a field on `AgentUpdate`, falling back to the global default.

---

## 7. Test Coverage Summary

| Test | Coverage |
|------|----------|
| `TestInferAgentStatus` | All 4 inference combinations |
| `TestCheckStaleness` | Mark stale + clear on fresh post |
| `TestStalenessNotMarkedForIdleDone` | Idle/done exemption |
| `TestAgentIntrospect` | No-session baseline response |
| `TestAgentIntrospectNotFound` | 404 for unknown agent |
| `TestMessagePriority` | All 3 valid priorities + invalid + default |
| `TestAgentSpawnMethodNotAllowed` | GET rejected with 405 |
| `TestAgentStopNotFound` | Unknown space returns 404 |

**Total: 8 tests, all passing, race-clean.**

Missing coverage: actual spawn/stop/restart with live tmux, InferredStatus update path, staleness ticker integration.

---

## 8. API Quick Reference

```
POST /spaces/{space}/agent/{name}/spawn       # Create tmux session + ignite
POST /spaces/{space}/agent/{name}/stop        # Kill session + mark done
POST /spaces/{space}/agent/{name}/restart     # Kill + re-spawn
GET  /spaces/{space}/agent/{name}/introspect  # Capture pane + infer state
POST /spaces/{space}/agent/{name}/message     # Send message with priority
```
