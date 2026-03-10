# Agent Config Persistence + Duplication UX

**TASK-059 | Areas: (1) Persist cwd/repo/prompts, (3) Agent duplication**

## Current State

`AgentUpdate` in `types.go` stores the agent's runtime status (status, summary, branch, PR,
session_id, etc.) but has no separate "configuration" concept. When a session restarts:

- Working directory is unknown — the tmux session cd's to wherever `claude` was launched
- The initial prompt (`/boss.ignite ...`) is not stored — must be resent manually
- `repo_url` is stored per-update but not as a sticky config field separate from runtime state

`TmuxCreateOpts.WorkDir` exists (used by the agent creation dialog), but is not persisted
anywhere after session creation — it is lost on restart.

## Proposed: AgentConfig

Add a new `AgentConfig` struct stored alongside `AgentUpdate` in `KnowledgeSpace.Agents`.
Config is set at agent creation and updated via a dedicated endpoint. Status updates
(`POST /spaces/{space}/agent/{name}`) do not touch config fields.

### Data Model

```go
// AgentConfig holds the durable configuration for an agent.
// Unlike AgentUpdate (runtime state), config fields persist across restarts
// and are never overwritten by agent status POSTs.
type AgentConfig struct {
    WorkDir       string   `json:"work_dir,omitempty"`       // absolute path or "" for server cwd
    RepoURL       string   `json:"repo_url,omitempty"`       // git remote for display/linking
    InitialPrompt string   `json:"initial_prompt,omitempty"` // command sent after session start
    PersonaIDs    []string `json:"persona_ids,omitempty"`    // ordered list of persona IDs to inject
    Backend       string   `json:"backend,omitempty"`        // "tmux" | "ambient" (default "tmux")
    Command       string   `json:"command,omitempty"`        // override default claude command
}
```

`KnowledgeSpace.Agents` value changes from `*AgentUpdate` to a wrapper:

```go
type AgentRecord struct {
    Config *AgentConfig `json:"config,omitempty"`
    Status *AgentUpdate `json:"status"`
}
```

> **Migration**: Existing JSON files have `agents: { name: AgentUpdate }`. On load, if a key
> decodes directly to an `AgentUpdate`, wrap it in `AgentRecord{Status: &update}`. This is
> backward-compatible with zero data loss.

### API Changes

| Endpoint | Change |
| -------- | ------ |
| `POST /spaces/{space}/agents` (create) | Accept `AgentConfig` fields in body; store as `AgentRecord.Config` |
| `POST /spaces/{space}/agent/{name}` (status) | Unchanged — touches only `AgentRecord.Status` |
| `GET /spaces/{space}/agent/{name}/config` | New: return `AgentConfig` |
| `PATCH /spaces/{space}/agent/{name}/config` | New: partial update of `AgentConfig` fields |
| `POST /spaces/{space}/agent/{name}/spawn` | Read `AgentConfig.WorkDir`, `AgentConfig.Command`, `AgentConfig.InitialPrompt` if not overridden in body |
| `POST /spaces/{space}/agent/{name}/restart` | Same as spawn — use stored config |

### Session Restart Behavior

When `handleAgentSpawn` or `handleAgentRestart` runs:

1. Load `AgentRecord.Config` (if present)
2. Apply config defaults (WorkDir, Command) unless the request body overrides them
3. After session is live, send `AgentConfig.InitialPrompt` instead of hardcoded `/boss.ignite`
4. If `InitialPrompt` is empty, fall back to generating `/boss.ignite "{name}" "{space}"`

This means an agent with:
```json
{
  "work_dir": "/home/jsell/code/sandbox/agent-boss",
  "initial_prompt": "/boss.ignite \"LifecycleMgr\" \"AgentBossDevTeam\""
}
```
...will always restart in the right directory with the right prompt, automatically.

---

## Agent Duplication UX

### Problem

When a manager creates a sub-agent team, each agent is configured the same way (same work_dir,
same backend, same parent). Today this requires filling out the create dialog N times.

### Proposed: Duplicate Agent

Add a "Duplicate" action in the agent card menu (three-dot menu or right-click context menu).

#### Backend: `POST /spaces/{space}/agent/{name}/duplicate`

Request body:
```json
{
  "new_name": "LifecycleDev2",
  "override_config": {
    "persona_ids": ["junior-engineer"]
  }
}
```

Behavior:
1. Load source agent's `AgentConfig`
2. Deep-copy config
3. Apply `override_config` fields (partial patch)
4. Create new `AgentRecord` with copied config and fresh empty `AgentUpdate` (status: idle)
5. Do NOT auto-spawn — user can spawn manually or from the UI

Response:
```json
{
  "ok": true,
  "agent": "LifecycleDev2",
  "config": { ... }
}
```

#### Frontend: Duplicate Dialog

- Triggered from agent card three-dot menu → "Duplicate agent"
- Pre-fills "New name" input with `{original_name}-copy`
- Shows a diff of inherited config fields (work_dir, persona_ids, backend) as read-only
- Optional: allow overriding persona_ids at duplicate time
- On success: new agent card appears in the space view, not yet spawned

### Edge Cases

| Case | Behavior |
| ---- | -------- |
| Duplicate name collision | 409 Conflict — user must choose a different name |
| Source has no AgentConfig | Duplicate inherits empty config (still useful to copy parent/role) |
| Source is actively running | Allowed — only config is copied, not session state |
