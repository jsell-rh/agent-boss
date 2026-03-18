# agent-compose.yaml — Design Spec

**Status:** Proposed
**Task:** TASK-098
**Author:** arch

## Overview

`agent-compose.yaml` is a portable **team blueprint** file that captures the full agent hierarchy for a space — roles, relationships, personas, and initial instructions — so any team member can load it and instantly have a coordinated agent team ready to work.

The analogy is `docker-compose.yml` for container services, or a git repo for code: a shareable, versionable artifact that defines a team's domain expertise and structure, not a point-in-time snapshot of one session's state.

**Not included:** tasks, runtime status, session IDs, agent tokens. Tasks are ephemeral per-session scratchpad state — like uncommitted editor buffers. The YAML is the team's structure and knowledge, not their current work.

**Design principles:**
- The **server** is responsible only for managing resources (agents, personas). It exposes primitives.
- The **CLI** orchestrates import logic — reads local YAML, fetches current state, computes diff, applies changes.
- No server-side fleet state tracking: the YAML file itself is the source of truth, like a terraform configuration.

---

## File Format

```yaml
version: "1"

space:
  name: "My Project"
  description: "Full-stack Node.js / React / Postgres app"       # optional
  shared_contracts: |                                              # optional
    All agents coordinate via odis-mcp.
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
    description: "Engineering lead — owns architecture decisions and team coordination"
    personas: [cto-base]
    initial_prompt: |
      You are the CTO. Your team: arch and sec report to you.
      Repository: https://github.com/org/myapp
      Start by orienting yourself and assigning initial work to your team.

  arch:
    role: worker
    description: "Architecture agent — enforces structural boundaries"
    parent: cto
    personas: [arch]
    work_dir: /workspace/myapp
    backend: tmux                  # "tmux" (default) or "ambient"
    initial_prompt: |
      You are arch, the architecture agent. Your manager is cto.
      Focus on structural integrity and clean domain boundaries.

  sec:
    role: worker
    description: "Security reviewer — OWASP, auth, secrets"
    parent: cto
    personas: [sec]
    backend: tmux
```

### Schema reference

#### `space`
| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Space name. Used as default on import; overridable with `--space` flag. If the space does not exist, the CLI/UI offers to create it (or errors clearly with `--no-create-space`). |
| `description` | string | no | Human-readable description of the project/team. |
| `shared_contracts` | string | no | Context prepended to every agent's ignition text during spawn, before persona prompts and `initial_prompt`. |

#### `personas` (map of persona ID to definition)
| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Display name. |
| `description` | string | no | Short description of the persona's role. |
| `prompt` | string | yes | The persona prompt text. Inline in the YAML — this is the team's domain expertise traveling with the file. |

**Persona ID namespace:** Persona IDs are global across all spaces on a server. To avoid collisions between teams, use unique IDs — e.g., prefix with a project slug (`myapp-arch`, `myapp-sec`). This is a known tradeoff of a shared namespace: two teams each defining a persona called `arch` will collide, bumping each other's version. The import dry-run shows the full prompt of any persona being created or changed so operators can catch unexpected collisions before applying.

On import: personas are upserted via existing persona endpoints. If a persona with this ID already exists and the prompt differs, a new version is created (server version history is preserved).

#### `agents` (map of agent name to config)
| Field | Type | Required | Description |
|---|---|---|---|
| `role` | string | no | Display label: `manager`, `worker`, `sme`, etc. |
| `description` | string | no | Short description of the agent's purpose. Aids readability in large fleet files. |
| `parent` | string | no | Agent name of this agent's manager. Omit for root nodes. |
| `personas` | string[] | no | Ordered list of persona IDs from the `personas:` section (or pre-existing server persona IDs). |
| `work_dir` | string | no | Absolute working directory path. Must be an absolute path; relative paths and paths containing `..` after cleaning are rejected. Must begin with `ODIS_WORK_DIR_PREFIX` if that env var is set. |
| `backend` | string | no | `tmux` (default) or `ambient`. |
| `command` | string | no | Launch command. Default: `claude`. Must be in the server's command allowlist (see Security). |
| `initial_prompt` | string | no | Instructions injected into the agent at session start. |
| `repo_url` | string | no | (tmux) Primary git remote for display/linking. Embedded userinfo is stripped on export. |
| `repos` | list | no | (ambient) Git repo URLs to clone into the session. HTTPS only; no RFC 1918 or link-local targets. |
| `model` | string | no | (ambient) Model override. |

**Not included in YAML:** agent tokens (generated server-side at spawn), session IDs, runtime status.

### Instruction composition order

At spawn time, three instruction sources are combined in this order (general to specific):

```
shared_contracts → persona prompt(s) → initial_prompt
```

Each source is prepended to the next. If multiple personas are listed, they are concatenated in order before `initial_prompt`.

---

## CLI Architecture — Import as a Client-Side Tool

`odis import` is implemented entirely in the CLI. It does not call a single monolithic server endpoint. Instead, it:

1. Reads and validates the local YAML file (size ≤ 1 MB, ≤ 100 agents)
2. Fetches current space state from the server (`GET /spaces/:space`)
3. Computes the diff **client-side** — what to create, update, skip
4. Shows the diff preview with full persona prompt text for any persona being created or changed
5. Asks for confirmation (or proceeds with `--yes`)
6. Applies changes by calling **existing server endpoints** in dependency order:
   - Persona upserts → existing persona endpoint
   - Agent creates → `POST /spaces/:space/agents`
   - Agent config updates → existing agent update endpoint
7. Reports post-import state: how many agents created/updated, and that none are running yet

The server exposes resource primitives. The import logic (diff, ordering, confirmation) lives in the CLI. This mirrors how terraform works: the CLI computes the plan against the API; the API is not aware of the plan concept.

---

## Import Semantics

`odis import` reconciles the YAML against the current space state. It never silently destroys data.

| Situation | Default behavior | With `--prune` |
|---|---|---|
| Agent in YAML, not in space | **Create** agent with config | same |
| Agent in both YAML and space | **Update config** (leaves running session intact) | same |
| Agent in space, not in YAML | **Leave unchanged** | **Remove** (blocked if session is live; requires explicit confirmation) |
| Persona in YAML, not on server | **Create** persona | same |
| Persona in YAML, exists with same prompt | No-op | same |
| Persona in YAML, exists with different prompt | **Create new version** | same |
| Space does not exist | **Offer to create** (or error with `--no-create-space`) | same |

Config updates take effect on the agent's **next spawn/restart** — running sessions are not interrupted.

### Topological ordering

Agents are created/updated in dependency order (parents before children). The CLI builds a DAG from `parent` references and detects cycles before applying any changes. A cycle in the hierarchy is a validation error that aborts the import.

### Concurrent import conflict handling

If two concurrent imports race, the CLI treats a 409 conflict on agent create as already-done and continues. Concurrent imports are safe but may produce duplicate version bumps on personas. The post-apply report notes any conflicts encountered.

### Import flags

```bash
odis import fleet.yaml                        # sync into space named in file
odis import fleet.yaml --space "Staging"      # import into a different space
odis import fleet.yaml --prune                # also remove agents not in YAML
odis import fleet.yaml --dry-run              # preview full diff without applying
odis import fleet.yaml --yes                  # skip confirmation (see CI/CD safety note)
odis import fleet.yaml --restart-changed      # restart agents whose config changed
odis import fleet.yaml --spawn-after-import   # spawn all agents after import completes
odis import fleet.yaml --no-create-space      # error if space doesn't exist
```

**`--yes` CI/CD safety:** `--yes` skips all confirmation prompts. Only use with fleet.yaml files from trusted, version-controlled sources. Running `odis import --yes` against a YAML file fetched from the network without prior review is a prompt injection risk — malicious persona prompts would be applied without preview.

**`--restart-changed` timing:** The diff is computed before applying changes. The set of "changed agents" is determined from the pre-apply diff and is not re-fetched after apply. This avoids restarting agents whose configs were concurrently modified by other users.

### Dry-run preview — full persona diff

The dry-run displays the full text of any persona being created or changed. A one-line summary is insufficient to catch malicious or unexpected prompt content:

```
Importing into "My Project" (space already exists)

  PERSONAS
  ~ myapp-arch   prompt changed (v2 → v3):
    --- v2
    +++ v3
    @@ -1,4 +1,5 @@
     You are an architecture expert...
    +Focus especially on the payments subsystem.

  AGENTS
  ~ arch     config updated (persona v3, work_dir /workspace/myapp)
  + devops   new agent (created, not yet running)
  = cto      no changes
  = sec      no changes
  ! qa       in space but not in YAML — use --prune to remove

Apply these changes? [y/N]
```

### Post-import state

After a successful import into an empty space:

```
Import complete. 5 agents created, 2 personas upserted. No agents are running yet.
Run: odis spawn --all "My Project"   (or use "Spawn all" in the UI)
```

---

## Export

```bash
odis export "My Project"                 # YAML to stdout
odis export "My Project" > fleet.yaml   # save to file
```

Export calls the server for current space state and serializes to YAML. The file captures all agents' current configs and the latest version of each persona they reference.

**Round-trip fidelity:** Export always includes all fields explicitly, even those matching server defaults (e.g., `backend: tmux`). This ensures re-import produces no phantom diffs from absent fields.

**Credential scrubbing:** On export, `repo_url` fields are parsed and any embedded userinfo (username:password in the URL) is stripped before writing to YAML.

A server endpoint `GET /spaces/:space/export` returns the export-safe struct (no tokens, no session IDs, no runtime fields). The CLI calls this endpoint rather than constructing the YAML from raw space data.

---

## Security

### No secrets in YAML
Per-agent tokens are generated server-side at spawn time. They are never exported or imported. The YAML is safe to commit to a version-controlled repository.

### Persona prompt safety
The server does not sanitize persona prompts — they are stored as-is and injected at spawn time. Operators are responsible for reviewing persona content before importing from untrusted sources. The dry-run preview displays the full text of any persona being created or changed.

### Command allowlist
The `command` field is validated against a server-side allowlist. The allowlist is configured via the `ODIS_COMMAND_ALLOWLIST` environment variable (comma-separated values; defaults to `claude`). Arbitrary shell commands and paths not in the allowlist are rejected with a 400 error. This prevents the YAML from being used as a code execution vector.

### YAML bomb protection
Both the CLI and the server enforce limits before parsing:
- Maximum file size: 1 MB
- Maximum agent count: 100 agents per fleet file

The CLI enforces these before sending any data to the server. The server enforces them independently on any endpoint that processes imported YAML, covering the UI upload path which bypasses the CLI.

### `repos` URL validation (ambient backend)
Git repo URLs in the `repos` field are validated server-side before use:
- HTTPS scheme required (`http://` and `file://` rejected)
- Hostname must not resolve to RFC 1918 addresses (10.x, 172.16–31.x, 192.168.x) or link-local ranges (169.254.x, ::1, fc00::/7)
- The server performs a DNS pre-check before passing URLs to the ambient runner

### `work_dir` path validation
- Absolute path required (relative paths rejected)
- Paths containing `..` after `filepath.Clean` are rejected
- If `ODIS_WORK_DIR_PREFIX` env var is set, `work_dir` must begin with that prefix (e.g., `/workspace`)

### `--prune` safety
`--prune` will not remove an agent with an active tmux or ambient session without explicit confirmation. The CLI checks session liveness before proposing removal.

### UI diff XSS
All YAML-sourced content in the diff preview (persona prompts, agent names, descriptions, initial prompts) must be rendered as plain text. No `innerHTML` insertion of user-controlled strings. The UI uses text nodes or a safe escaping function for all diff content.

### Auth
Export and import require a valid `ODIS_API_TOKEN` when the server has auth enabled. CORS and token validation apply to all underlying agent/persona endpoints.

---

## UI Additions

### Space overview
- **"Export fleet"** button: calls the export endpoint and downloads `<space-slug>-fleet.yaml`
- **"Import fleet"** button: opens the import modal

### Import modal flow
1. User uploads or pastes YAML (frontend requires `js-yaml` or equivalent YAML parser in the bundle)
2. UI validates file size client-side (≤ 1 MB) before sending to the server
3. UI fetches current space state and computes the diff client-side
4. Shows the full diff preview — including complete persona prompt diffs (rendered as plain text, no innerHTML)
5. "Spawn all after import" checkbox (default: off)
6. User confirms — UI calls existing agent/persona endpoints in topological order
7. Success state shows agent count + "None are running yet" + "Spawn all" button if checkbox was unchecked

---

## CLI Surface

```bash
# Export current space to YAML
odis export "OpenDispatch Dev"
odis export "OpenDispatch Dev" > fleet.yaml

# Import YAML into a space
odis import fleet.yaml
odis import fleet.yaml --space "Staging"
odis import fleet.yaml --prune
odis import fleet.yaml --dry-run
odis import fleet.yaml --restart-changed
odis import fleet.yaml --spawn-after-import
odis import fleet.yaml --yes
odis import fleet.yaml --no-create-space
```

---

## Implementation Phases

**Phase 1 — Export + Import core**
Exporting without importing is a dead-end milestone — users can serialize a space but cannot use the file. Export and import ship together.

- `GET /spaces/:space/export` endpoint (YAML-safe struct, no runtime fields)
- `odis export` CLI command
- `odis import` CLI command: parse YAML → validate → fetch current state → compute diff → topological sort → show full diff preview → apply via existing endpoints → report post-import state
- `--dry-run`, `--yes`, `--spawn-after-import`, `--no-create-space` flags
- Server-side guards: command allowlist, YAML bomb limits, `work_dir` validation, `repos` URL validation
- UI: "Export fleet" button on space overview; "Import fleet" modal with full diff preview

**Phase 2 — Advanced import options + audit**
- `--prune` support (remove agents not in YAML, blocked on live sessions)
- `--restart-changed` flag
- Import audit log (which agents created/updated, by whom, from which file hash, timestamp)
- Schema versioning and forward-compatibility warnings for unknown fields

**Phase 3 — Polish**
- `--spawn-after-import` integrated with live session tracking
- Schema version migration path as format evolves
- `odis import --yes` safety guardrails in CI (e.g., require `--trusted-source` flag to acknowledge the risk)
