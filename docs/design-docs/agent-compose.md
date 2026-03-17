# agent-compose.yaml — Design Spec

**Status:** Proposed
**Task:** TASK-098
**Author:** arch

## Overview

`agent-compose.yaml` is a portable **team blueprint** file that captures the full agent hierarchy for a space — roles, relationships, personas, and initial instructions — so any team member can load it up and instantly have a coordinated agent team ready to work.

The analogy is `docker-compose.yml` for container services, or a git repo for code: a shareable, versionable artifact that defines a team's domain expertise and structure, not a point-in-time snapshot of one session's state.

**Not included:** tasks, runtime status, session IDs, agent tokens. Tasks are ephemeral per-session scratchpad state — like uncommitted editor buffers. The YAML is the team's structure and knowledge, not their current work.

**Design principles:**
- The **server** is responsible only for managing resources (agents, personas). It exposes primitives.
- The **CLI** orchestrates import logic — reads local YAML, fetches current state, computes diff, applies changes.
- No server-side drift tracking or fleet state: the YAML file itself is the source of truth, like a terraform configuration.

---

## File Format

```yaml
version: "1"

space:
  name: "My Project"
  description: "Full-stack Node.js / React / Postgres app"       # optional
  shared_contracts: |                                              # optional
    All agents coordinate via boss-mcp.
    Check in every 10 minutes during active work.

personas:
  arch:
    name: "Architecture Expert"
    description: "Structural integrity, hexagonal arch, clean domain boundaries"
    prompt: |
      You are an architecture expert for a Node.js/React/Postgres stack.
      You know the codebase deeply. You focus on structural integrity,
      keeping domain logic decoupled from infrastructure, and enforcing
      consistent patterns across the codebase.

  sec:
    name: "Security Reviewer"
    description: "OWASP top-10, auth, input validation, secrets management"
    prompt: |
      You are a security expert. You review PRs and code for OWASP top-10
      vulnerabilities, authentication flows, input validation, and secrets
      management. You are thorough and conservative.

agents:
  cto:
    role: manager
    personas: [cto-base]
    initial_prompt: |
      You are the CTO. Your team: arch and sec report to you.
      Repository: https://github.com/org/myapp
      Start by orienting yourself and assigning initial work to your team.

  arch:
    role: worker
    parent: cto
    personas: [arch]
    work_dir: /workspace/myapp
    backend: tmux                  # "tmux" (default) or "ambient"
    initial_prompt: |
      You are arch, the architecture agent. Your manager is cto.
      Focus on structural integrity and clean domain boundaries.

  sec:
    role: worker
    parent: cto
    personas: [sec]
    backend: tmux
```

### Schema reference

#### `space`
| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Space name. Used as default on import; overridable with `--space` flag. |
| `description` | string | no | Human-readable description of the project/team. |
| `shared_contracts` | string | no | Context injected into every agent in the space. |

#### `personas` (map of persona ID to definition)
| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Display name. |
| `description` | string | no | Short description of the persona's role. |
| `prompt` | string | yes | The persona prompt text. Inline in the YAML — this is the team's domain expertise traveling with the file. |

On import: personas are upserted to the server via existing persona endpoints. If a persona with this ID already exists and the prompt differs, a new version is created (server version history is preserved).

#### `agents` (map of agent name to config)
| Field | Type | Required | Description |
|---|---|---|---|
| `role` | string | no | Display label: `manager`, `worker`, `sme`, etc. |
| `parent` | string | no | Agent name of this agent's manager. Omit for root nodes. |
| `personas` | string[] | no | Ordered list of persona IDs from the `personas:` section (or pre-existing server persona IDs). |
| `work_dir` | string | no | Absolute working directory path. Omit for server default. |
| `backend` | string | no | `tmux` (default) or `ambient`. |
| `command` | string | no | Launch command. Default: `claude`. Must be in the server's command allowlist. |
| `initial_prompt` | string | no | Instructions injected into the agent at session start. |
| `repo_url` | string | no | (tmux) Primary git remote for display/linking. |
| `repos` | list | no | (ambient) Git repos to clone into the session. |
| `model` | string | no | (ambient) Model override. |

**Not included in YAML:** agent tokens (generated server-side at spawn), session IDs, runtime status.

---

## CLI Architecture — Import as a Client-Side Tool

`boss import` is implemented entirely in the CLI. It does not call a single monolithic server endpoint. Instead, it:

1. Reads and validates the local YAML file
2. Fetches current space state from the server (`GET /spaces/:space`)
3. Computes the diff **client-side** (what to create, update, skip)
4. Shows a preview and asks for confirmation (or proceeds with `--yes`)
5. Applies changes by calling **existing server endpoints** in dependency order:
   - Persona upserts → `PUT /spaces/:space/personas/:id`
   - Agent creates → `POST /spaces/:space/agents`
   - Agent config updates → `PATCH /spaces/:space/agents/:agent`

The server exposes resource primitives. The import logic (diff, ordering, confirmation) lives in the CLI. This mirrors how terraform works: the CLI computes the plan against the API; the API is not aware of the plan concept.

---

## Import Semantics

`boss import` reconciles the YAML against the current space state. It never silently destroys data.

| Situation | Default behavior | With `--prune` |
|---|---|---|
| Agent in YAML, not in space | **Create** agent with config | same |
| Agent in both YAML and space | **Update config** (leaves running session intact) | same |
| Agent in space, not in YAML | **Leave unchanged** | **Remove** (with confirmation if session is live) |
| Persona in YAML, not on server | **Create** persona | same |
| Persona in YAML, exists with same prompt | No-op | same |
| Persona in YAML, exists with different prompt | **Create new version** | same |

Config updates take effect on the agent's **next spawn/restart** — running sessions are not interrupted.

### Topological ordering

Agents are created/updated in dependency order (parents before children). The CLI builds a DAG from `parent` references and detects cycles before applying any changes. A cycle in the hierarchy is a validation error.

### Import flags

```bash
boss import fleet.yaml                        # sync into space named in file
boss import fleet.yaml --space "Staging"      # import into a different space
boss import fleet.yaml --prune                # also remove agents not in YAML
boss import fleet.yaml --dry-run              # preview diff without applying
boss import fleet.yaml --yes                  # skip confirmation prompt
boss import fleet.yaml --restart-changed      # restart agents whose config changed
```

### Import preview (dry-run output)

Before applying, the CLI shows a diff. This is computed entirely client-side — the server stores no "desired state":

```
Importing into "My Project" (space already exists)

  ~ arch     config updated (persona prompt changed, work_dir added)
  + devops   new agent (will be created, not yet spawned)
  = cto      no changes
  = sec      no changes
  ! qa       in space but not in YAML -- use --prune to remove

Apply these changes? [y/N]
```

### Re-import workflow (team updates the YAML)

When the team updates the YAML (new persona version, agent added/removed) and a team member re-imports:

1. `boss import fleet.yaml --dry-run` shows exactly what changed since last import
2. User confirms and runs `boss import fleet.yaml`
3. Changed agents' configs are updated on the server
4. User optionally adds `--restart-changed` to immediately restart agents with updated configs

This is the same "compare local file to remote state, apply delta" model as `kubectl apply` or `terraform apply`. No server-side state tracking required.

---

## Export

```bash
boss export "My Project"                 # YAML to stdout
boss export "My Project" > fleet.yaml   # save to file
```

Export calls `GET /spaces/:space` (with agents and personas) and serializes the result to YAML. The file captures all agents' current configs and the latest version of each persona they reference. The file is human-readable and git-committable.

A new server endpoint may be needed to return a clean export payload: `GET /spaces/:space/export` returning the YAML-serializable struct (without runtime fields like tokens, session IDs).

---

## Security

### No secrets in YAML
Per-agent tokens are generated server-side at spawn time. They are never exported or imported. The YAML is safe to commit to a public git repo.

### Command allowlist
The `command` field (launch command override) is validated against a server-side allowlist (e.g. `["claude", "claude-code"]`). Arbitrary shell commands are rejected. This prevents the YAML from being used as a code execution vector.

### YAML bomb protection
The CLI enforces a maximum file size (e.g. 1 MB) and a maximum agent count (e.g. 100 agents) before parsing. This prevents denial-of-service via deeply nested YAML or massive files.

### Prompt injection awareness
Persona prompts are treated as user-controlled content. Imported personas do not gain elevated server permissions — they are stored as text and injected at spawn time like any other persona. The server's existing persona sandboxing applies.

### `--prune` safety
`--prune` will not remove an agent with an active tmux session or ambient session without explicit confirmation. The CLI checks session liveness before proposing removal.

### Auth
Export and import require a valid `BOSS_API_TOKEN` when the server has auth enabled. CORS and token validation apply to all underlying agent/persona endpoints as usual.

---

## UI Additions

### Space overview
- **"Export fleet"** button: calls `boss export` equivalent and downloads `<space-slug>-fleet.yaml`
- **"Import fleet"** button: opens a modal with a file picker or drag-and-drop area

### Import modal flow
1. User uploads or pastes YAML
2. UI fetches current space state and computes the diff client-side
3. Shows the diff preview (same format as CLI dry-run)
4. User confirms — UI calls existing agent/persona endpoints to apply changes
5. Optionally: "Restart changed agents" checkbox to immediately respawn agents with updated configs

This mirrors the CLI: the diff computation is a client concern, not a server concern.

---

## CLI Surface

```bash
# Export current space to YAML
boss export "Agent Boss Dev"
boss export "Agent Boss Dev" > fleet.yaml

# Import YAML into a space
boss import fleet.yaml
boss import fleet.yaml --space "Staging"
boss import fleet.yaml --prune
boss import fleet.yaml --dry-run
boss import fleet.yaml --restart-changed
boss import fleet.yaml --yes
```

---

## Implementation Phases

**Phase 1 — Export**
- `GET /spaces/:space/export` endpoint returning YAML-safe struct (no tokens, no session IDs)
- `boss export` CLI command
- UI: "Export fleet" button on space overview

**Phase 2 — Import**
- `boss import` CLI command: parse YAML → fetch current state → compute diff → apply via existing endpoints
- Topological sort for agent creation ordering
- `--dry-run`, `--prune`, `--restart-changed` flags
- UI: "Import fleet" modal with diff preview and confirmation

**Phase 3 — Polish**
- Import audit log (which agents were created/updated, by whom, from which file hash)
- Schema versioning and forward-compatibility warnings
- `--yes` flag for non-interactive use in CI/CD pipelines
