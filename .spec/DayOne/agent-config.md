# Agent Config Persistence + Duplication UX

**TASK-059 | Areas: (1) Persist cwd/repo/prompts, (3) Agent duplication**

## Current State

`AgentUpdate` in `types.go` stores the agent's runtime status (status, summary, branch, PR,
session_id, etc.) but has no separate "configuration" concept. When a session restarts:

- Working directory is unknown — the tmux session cd's to wherever `claude` was launched
- The initial prompt is not stored — must be resent manually
- `repo_url` is stored per-update but not as a sticky config field separate from runtime state
- For the ambient backend, `AmbientCreateOpts.Repos` (a list of `SessionRepo`) is not persisted
  either — the ambient session loses its repo configuration on restart

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
    // Common fields (all backends)
    WorkDir       string        `json:"work_dir,omitempty"`       // absolute path or "" for server cwd
    InitialPrompt string        `json:"initial_prompt,omitempty"` // instructions sent to agent after session start (no slash commands)
    PersonaIDs    []string      `json:"persona_ids,omitempty"`    // ordered list of global persona IDs to inject
    Backend       string        `json:"backend,omitempty"`        // "tmux" | "ambient" (default "tmux")
    Command       string        `json:"command,omitempty"`        // launch command (default: "claude"); --dangerously-skip-permissions is injected by global server toggle, not set here

    // tmux-specific
    RepoURL       string        `json:"repo_url,omitempty"`       // primary git remote for display/linking

    // ambient-specific
    Repos         []SessionRepo `json:"repos,omitempty"`          // git repos to clone into the ambient session
    Model         string        `json:"model,omitempty"`          // model override for ambient backend
}
```

> **Note on InitialPrompt**: This field must not contain or default to slash commands.
> Instead it should be a plain-text instruction that the agent can follow without
> any Claude-specific command infrastructure (e.g., "You are LifecycleMgr. Read the
> blackboard at http://localhost:8899/spaces/AgentBossDevTeam/raw and post your status.").
> The MCP bootstrap resource (see [mcp-bootstrap.md](./mcp-bootstrap.md)) provides the
> full structured context — InitialPrompt supplements it with agent-specific instructions.

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
2. Apply config defaults (WorkDir, Command, Repos) unless the request body overrides them
3. Inject MCP config into the agent session (see [mcp-bootstrap.md](./mcp-bootstrap.md) for
   how the server runs `claude mcp add boss-mcp` to register the MCP server before the agent starts)
4. After session is live, send `AgentConfig.InitialPrompt` as additional instructions
5. There is **no slash command fallback** — if `InitialPrompt` is empty, nothing extra is sent;
   the MCP bootstrap resource provides all necessary context

This means an agent with:
```json
{
  "work_dir": "/home/jsell/code/sandbox/agent-boss",
  "repos": [{"url": "https://github.com/jsell-rh/agent-boss.git"}],
  "initial_prompt": "You are LifecycleMgr. Read your blackboard section and act on any pending tasks."
}
```
...will always restart in the right directory with the right context, automatically, with no
manual intervention and no dependency on slash commands.

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
5. **Auto-spawn** the new agent session immediately after creation

Response:
```json
{
  "ok": true,
  "agent": "LifecycleDev2",
  "session_id": "agentbossdevteam-lifecycledev2",
  "config": { ... }
}
```

#### Auto-Spawn Behavior

Duplicated agents are spawned immediately. This follows the principle that duplication is
used when a manager wants more instances of a working agent — they should be active at once.

Additionally, agents should be auto-spawned (or prompted to spawn) when triggering events
occur, such as:
- A new message is delivered to an agent with a stopped session
- A task is assigned to an agent whose session is not running
- A new comment is added to an assigned task

The server can detect these cases and either auto-spawn (if configured) or surface a
"Spawn to handle this event" button in the dashboard notification.

#### Frontend: Duplicate Dialog

- Triggered from agent card three-dot menu → "Duplicate agent"
- Pre-fills "New name" input with `{original_name}-copy`
- Shows inherited config fields (work_dir, persona_ids, backend) as editable
- On success: new agent card appears in the space view, session spawning in progress (spinner)

### Edge Cases

| Case | Behavior |
| ---- | -------- |
| Duplicate name collision | 409 Conflict — user must choose a different name |
| Source has no AgentConfig | Duplicate inherits empty config (still useful to copy parent/role) |
| Source is actively running | Allowed — only config is copied, not session state |

---

## Global Server Setting: Permission Mode

The `--dangerously-skip-permissions` flag for tmux-backend agents is controlled by a
single server-wide toggle, not a per-agent option. This prevents individual agents from
silently acquiring elevated permissions and makes the risk surface explicit.

### Server Configuration

```
BOSS_ALLOW_SKIP_PERMISSIONS=true  # env var; default false
```

Or via the admin settings API:

```bash
PATCH /settings
{
  "tmux_skip_permissions": true
}
```

When `tmux_skip_permissions` is `true`, the coordinator appends `--dangerously-skip-permissions`
to the effective launch command for every tmux-backend agent spawn/restart.

When `false` (default), agents run as plain `claude` and must confirm tool use interactively.

### Dashboard Settings Page

A settings page (accessible from the nav bar) exposes this toggle with:
- Current state (on/off)
- A toggle switch
- A warning banner when enabled:
  > "Permission skip is ON — all tmux agents can run tools without confirmation.
  > Disable this before running untrusted agents."

The toggle state is persisted in the server's data directory (`settings.json`).
