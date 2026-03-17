# agent-compose.yaml — Design Spec

**Status:** Proposed
**Task:** TASK-098
**Author:** arch

## Overview

`agent-compose.yaml` is a portable **team blueprint** file that captures the full agent hierarchy for a space — roles, relationships, personas, and initial instructions — so any team member can load it up and instantly have a coordinated agent team ready to work.

The analogy is `docker-compose.yml` for container services, or a git repo for code: a shareable, versionable artifact that defines a team, not a point-in-time snapshot of one session's state.

**Not included:** tasks, runtime status, session IDs, agent tokens. Tasks are ephemeral per-session scratchpad state — like uncommitted editor buffers. The YAML is the team's structure, not their current work.

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

On import: personas are upserted to the server. If a persona with this ID already exists and the prompt differs, a new version is created (server version history is preserved). Agents pinned to an older version stay on it until restarted.

#### `agents` (map of agent name to config)
| Field | Type | Required | Description |
|---|---|---|---|
| `role` | string | no | Display label: `manager`, `worker`, `sme`, etc. |
| `parent` | string | no | Agent name of this agent's manager. Omit for root nodes. |
| `personas` | string[] | no | Ordered list of persona IDs from the `personas:` section (or pre-existing server persona IDs). |
| `work_dir` | string | no | Absolute working directory path. Omit for server default. |
| `backend` | string | no | `tmux` (default) or `ambient`. |
| `command` | string | no | Launch command. Default: `claude`. |
| `initial_prompt` | string | no | Instructions injected into the agent at session start. |
| `repo_url` | string | no | (tmux) Primary git remote for display/linking. |
| `repos` | list | no | (ambient) Git repos to clone into the session. |
| `model` | string | no | (ambient) Model override. |

**Not included in YAML:** agent tokens (generated server-side at spawn), session IDs, runtime status.

---

## Import Semantics — "Sync" Model

`boss import` reconciles the YAML against the current space state, like `kubectl apply` or `git pull`. It never silently destroys data.

| Situation | Default behavior | With `--prune` |
|---|---|---|
| Agent in YAML, not in space | **Create** agent with config | same |
| Agent in both YAML and space | **Update config** (leaves running session intact) | same |
| Agent in space, not in YAML | **Leave unchanged** | **Remove** (with confirmation) |
| Persona in YAML, not on server | **Create** persona | same |
| Persona in YAML, exists with same prompt | No-op | same |
| Persona in YAML, exists with different prompt | **Create new version** | same |

Config updates take effect on the agent's **next spawn/restart** — running sessions are not interrupted.

### Import flags

```bash
boss import fleet.yaml                        # sync into space named in file
boss import fleet.yaml --space "Staging"      # import into a different space
boss import fleet.yaml --prune                # also remove agents not in YAML
boss import fleet.yaml --dry-run              # preview diff without applying
boss import fleet.yaml --restart-drifted      # restart agents whose config changed
```

### Import preview (dry-run / UI confirmation)

Before committing, show a diff:

```
Importing into "My Project" (space already exists)

  ~ arch     config updated (persona v2->v3, work_dir changed)
  + devops   new agent (will be created, not yet spawned)
  = cto      no changes
  = sec      no changes
  ! qa       in space but not in YAML -- use --prune to remove

[Cancel]  [Import]
```

---

## Export

```bash
boss export "My Project"                 # YAML to stdout
boss export "My Project" > fleet.yaml   # save to file
```

Export produces a YAML capturing all agents' current configs and the latest version of each persona they reference. The file is human-readable and git-committable.

---

## Drift Detection

After import, the server tracks the **desired state** for each agent (what the YAML said). If an agent's running config diverges from the last-imported config, it is considered **drifted**.

### How it works

- On import, store a `fleet_config_hash` on each agent (SHA of their YAML entry).
- If the agent's live config hash differs from the last-imported hash, the agent is drifted.
- Persona version pinning uses the existing mechanism: if a persona was bumped on re-import, agents pinned to the old version show as outdated (already implemented via `PersonaRef.PinnedVersion`).

### UI surface

- **Agent card badge**: "out of sync" indicator when drifted (same pattern as the existing persona outdated warning).
- **Per-agent action**: "Restart to sync" — kills and respawns with latest config.
- **Space overview button**: "Sync fleet" — restarts all drifted agents in one click.

### API endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/spaces/:space/drift` | Returns list of drifted agents with diff summary |
| `POST` | `/spaces/:space/sync` | Restarts all drifted agents |
| `POST` | `/spaces/:space/agents/:agent/sync` | Restart one drifted agent |

---

## CLI Surface

```bash
# Export
boss export "Agent Boss Dev"
boss export "Agent Boss Dev" > fleet.yaml

# Import
boss import fleet.yaml
boss import fleet.yaml --space "Staging"
boss import fleet.yaml --prune
boss import fleet.yaml --dry-run
boss import fleet.yaml --restart-drifted

# Drift
boss drift "Agent Boss Dev"              # show drifted agents
boss sync "Agent Boss Dev"               # restart all drifted agents
```

---

## UI Additions

### Space overview
- **"Export fleet"** button: downloads `<space-slug>-fleet.yaml`
- **"Sync fleet"** button (shown when any agent is drifted): restarts all out-of-sync agents

### Home / spaces page
- **"Import fleet"** button: opens modal with file picker or drag-and-drop, shows diff preview, user confirms

### Agent cards
- Out-of-sync drift badge when `fleet_config_hash` differs from live config
- "Restart to sync" action in agent context menu

---

## Implementation Phases

**Phase 1 — Export + Import (no drift)**
- `GET /spaces/:space/export` returns YAML
- `POST /spaces/import` parses YAML, upserts personas, creates/updates agents
- CLI: `boss export`, `boss import`
- UI: export button on space overview, import modal on home page

**Phase 2 — Drift detection**
- Add `fleet_config_hash` to `AgentConfig` (set on import)
- `GET /spaces/:space/drift` endpoint
- `POST /spaces/:space/sync` bulk restart
- UI: drift badge on agent cards, "Sync fleet" button

**Phase 3 — Polish**
- `--prune` support (remove agents not in YAML)
- `--restart-drifted` flag on import
- Import history / audit log

---

## Security

- No credentials, tokens, or secrets in YAML ever. Per-agent tokens are generated server-side at spawn time.
- Import endpoints guarded by `BOSS_API_TOKEN` middleware when auth is enabled.
- `--prune` requires explicit confirmation (destructive operation).
- Space name in YAML is a suggestion; `--space` flag overrides. No cross-space data bleed.
