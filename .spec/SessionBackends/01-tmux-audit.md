# Tmux Usage Audit

Every place in the codebase that directly touches tmux, categorized by purpose.

## 1. Low-Level Tmux Primitives (`tmux.go`)

| Function | What it does | Called by |
|----------|-------------|-----------|
| `tmuxAvailable()` | Checks if `tmux` binary is in PATH | `TmuxAutoDiscover`, `BroadcastCheckIn`, `SingleAgentCheckIn`, `checkAllSessionLiveness` |
| `tmuxListSessions()` | Runs `tmux list-sessions -F #S` | `tmuxSessionExists`, `TmuxAutoDiscover` |
| `tmuxSessionExists(session)` | Checks if a named session is in the list | `handleAgentSpawn`, `handleAgentStop`, `handleAgentRestart`, `handleAgentIntrospect`, `BroadcastCheckIn`, `handleApproveAgent`, `handleReplyAgent`, `handleSpaceTmuxStatus`, `TmuxBackend.Spawn`, `TmuxBackend.Stop`, `checkAllSessionLiveness` |
| `tmuxCapturePaneLines(session, n)` | Runs `tmux capture-pane -t session -p`, returns last N non-empty lines | `tmuxIsIdle`, `tmuxCheckApproval`, `handleAgentIntrospect`, `handleSpaceTmuxStatus` |
| `tmuxCapturePaneLastLine(session)` | Wrapper: captures last 1 line | `handleSpaceTmuxStatus` |
| `tmuxIsIdle(session)` | Checks last 10 lines for idle indicators (shell prompts, Claude Code `>` prompt, etc.) | `tmuxCheckApproval`, `BroadcastCheckIn`, `checkAllSessionLiveness` |
| `tmuxCheckApproval(session)` | Scans pane for "Do you want...?" + numbered choices pattern | `checkAllSessionLiveness`, `handleAgentIntrospect`, `handleApproveAgent`, `handleSpaceTmuxStatus` |
| `tmuxApprove(session)` | Sends `Enter` key to session | `handleApproveAgent` |
| `tmuxSendKeys(session, text)` | Sends text + `C-m` (Enter) to session | `runAgentCheckIn`, `handleAgentSpawn`, `handleAgentRestart`, `handleReplyAgent`, `handleCreateAgents` (ignite), `TmuxBackend.Spawn` |
| `parseTmuxAgentName(session)` | Extracts agent name from `agentdeck_{name}_{id}` pattern | `TmuxAutoDiscover` |

## 2. Idle Detection Helpers (`tmux.go`)

| Function | What it does |
|----------|-------------|
| `lineIsIdleIndicator(line)` | Returns true if a line matches known idle patterns: `>`, shell `$`/`%`/`#`, Claude Code hints, status bars |
| `isShellPrompt(line)` | Detects `$`, `%`, `>`, `#`, `>` as trailing prompt characters |
| `waitForIdle(session, timeout)` | Polls `tmuxIsIdle` every 3s until idle or timeout |
| `waitForBoardPost(space, agent, since, timeout)` | Polls `agentUpdatedAt` every 3s (not tmux-specific, but used exclusively by broadcast which is tmux-only) |

## 3. Broadcast / Check-In (`tmux.go`)

| Function | What it does |
|----------|-------------|
| `runAgentCheckIn(space, agent, tmuxSession, checkModel, workModel, result)` | Switches model, sends `/boss.check`, waits for board post, restores model. All via `tmuxSendKeys` + `waitForIdle`. |
| `BroadcastCheckIn(space, checkModel, workModel)` | Iterates all agents with `TmuxSession`, calls `runAgentCheckIn` concurrently. |
| `SingleAgentCheckIn(space, agent, checkModel, workModel)` | Single-agent version of broadcast. |
| `BroadcastResult` + helpers | Result accumulator for sent/skipped/errors. |

## 4. Lifecycle Handlers (`lifecycle.go`)

| Handler | Tmux operations performed |
|---------|--------------------------|
| `handleAgentSpawn` | `tmuxSessionExists`, `exec tmux new-session -d`, `tmuxSendKeys` (command), `tmuxSendKeys` (ignite) |
| `handleAgentStop` | Gets `agent.TmuxSession`, `tmuxSessionExists`, `exec tmux kill-session` |
| `handleAgentRestart` | Gets `agent.TmuxSession`, `tmuxSessionExists`, `exec tmux kill-session`, `exec tmux new-session`, `tmuxSendKeys` (command + ignite) |
| `handleAgentIntrospect` | Gets `agent.TmuxSession`, `tmuxSessionExists`, `tmuxIsIdle`, `tmuxCapturePaneLines`, `tmuxCheckApproval` |
| `isNonTmuxAgent(agent)` | Checks `agent.Registration.AgentType != "tmux"` to gate lifecycle endpoints |
| `nonTmuxLifecycleError(w, type)` | Returns 422 for non-tmux agents hitting tmux-only endpoints |
| `inferAgentStatus(exists, idle, needsApproval)` | Pure function mapping booleans to string status (not tmux-specific logic) |

## 5. Liveness Loop (`liveness.go`)

| Function | Tmux operations performed |
|----------|--------------------------|
| `checkAllSessionLiveness` | `tmuxAvailable`, iterates all agents with `TmuxSession`, calls `tmuxSessionExists`, `tmuxIsIdle`, `tmuxCheckApproval`. Updates `InferredStatus`, records interrupts, triggers nudges. Broadcasts SSE `tmux_liveness` event. |
| `executeNudge` | Calls `SingleAgentCheckIn` (which uses tmux) |

## 6. Agent Handlers (`handlers_agent.go`)

| Handler | Tmux operations performed |
|---------|--------------------------|
| `handleSpaceAgent` (POST) | Preserves `TmuxSession` as sticky field on agent update |
| `handleIgnition` (GET) | Accepts `?tmux_session=` query param, stores on agent record, references it in ignition text |
| `handleSpaceTmuxStatus` (GET) | `TmuxAutoDiscover`, iterates agents, calls `tmuxSessionExists`, `tmuxIsIdle`, `tmuxCapturePaneLastLine`, `tmuxCheckApproval` |
| `handleApproveAgent` (POST) | Gets `agent.TmuxSession`, `tmuxSessionExists`, `tmuxCheckApproval`, `tmuxApprove` |
| `handleReplyAgent` (POST) | Gets `agent.TmuxSession`, `tmuxSessionExists`, `tmuxSendKeys` |
| `handleCreateAgents` (POST) | Uses `AgentBackend` interface for spawn, but then calls `tmuxSendKeys` directly for ignite |

## 7. Backend Interface (`agent_backend.go`) — already exists but incomplete

| Method | `TmuxBackend` impl | `CloudBackend` impl |
|--------|-------------------|---------------------|
| `Name()` | `"tmux"` | `"cloud"` |
| `Spawn(ctx, spec)` | Creates tmux session, sends command | Returns `ErrNotImplemented` |
| `Stop(ctx, space, name)` | Kills tmux session | Returns `ErrNotImplemented` |
| `List(ctx, space)` | Lists all tmux sessions | Returns `ErrNotImplemented` |

**Only used by:** `handleCreateAgents`. All other lifecycle/liveness/broadcast code bypasses this interface entirely.

## 8. Data Model References

| Location | Field | Notes |
|----------|-------|-------|
| `types.go:96` | `AgentUpdate.TmuxSession` | JSON tag `tmux_session` |
| `db/models.go:82` | `Agent.TmuxSession` | SQLite column |
| `db/convert.go:35` | `AgentRow.TmuxSession` | DB-to-coordinator conversion |
| `db/convert.go:61,113` | `FromAgentFields(..., tmuxSession, ...)` | Coordinator-to-DB conversion |
| `db_adapter.go:317,371` | References `TmuxSession` | DB adapter layer |
| `db/migrate_from_json.go:40` | `jsonAgent.TmuxSession` | JSON migration |

## 9. Frontend References

| File | Usage |
|------|-------|
| `frontend/src/types/index.ts:49,118` | `tmux_session?: string` on agent types |
| `frontend/src/api/client.ts:257,275` | Spawn/restart return `tmux_session` |
| `frontend/src/components/AgentDetail.vue:133,562,918,921` | Displays tmux session, gates pane/controls sections |

## 10. Scripts and Documentation

| File | Usage |
|------|-------|
| `scripts/boss.sh` | `get_tmux_session()`, passes `-e TMUX_SESSION` |
| `scripts/agent-ignition.sh` | `create_tmux_session()` |
| `scripts/coordination-client.py` | Passes `tmux_session` to ignition |
| `commands/boss.ignite.md` | References `?tmux_session=` |
| `commands/boss.check.md` | Notes `tmux_session` is sticky |
| `docs/AGENT_PROTOCOL.md` | Documents `tmux_session` field, ignition params |
| `docs/lifecycle-spec.md` | Spawn/stop/restart reference `tmux_session` |
| `docs/api-reference.md` | API docs reference `tmux_session` |
| `docs/hierarchy-design.md` | Compares parent stickiness to `TmuxSession` |
