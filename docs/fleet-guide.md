# Fleet Guide — Export, Edit, Import

The `odis export` and `odis import` commands let you capture a running agent team as a portable YAML file, version-control it, and replay it into any OpenDispatch instance. The format is called a **fleet file** (or `agent-compose.yaml`).

---

## Quick start

```bash
# Capture the current state of a space
odis export "My Project" --output fleet.yaml

# Inspect and edit the file (see sections below)
$EDITOR fleet.yaml

# Replay into the same space (or a new one)
odis import fleet.yaml --dry-run   # preview changes
odis import fleet.yaml             # apply
```

---

## What gets exported

| Included | Excluded |
|----------|----------|
| Space name and description | Tasks |
| All agents (role, parent, work\_dir, backend) | Session IDs / tmux panes |
| All personas (name, description, prompt) | Auth tokens |
| `shared_contracts` if set | Runtime status (active/idle/done) |
| `initial_prompt` per agent | Conversation history |

Credentials embedded in `repo_url` (e.g. `https://user:token@github.com/...`) are stripped automatically.

---

## Fleet file format

```yaml
version: "1"

space:
  name: "My Project"
  description: "Full-stack Node.js / React / Postgres app"     # optional
  shared_contracts: |                                           # optional
    All agents coordinate via odis-mcp.
    Check in every 10 minutes during active work.

personas:
  arch:
    name: "Architecture Expert"
    description: "Structural integrity, clean boundaries"
    prompt: |
      You are an architecture expert for a Node.js/React/Postgres stack.
      You know the codebase deeply. You focus on clean domain boundaries
      and consistent patterns across the codebase.

agents:
  cto:
    role: manager
    description: "Engineering lead — owns architecture and team coordination"
    personas: [cto-base]
    initial_prompt: |
      You are the CTO. Your team: arch reports to you.
      Repository: https://github.com/org/myapp
      Start by orienting yourself and assigning initial work to your team.

  arch:
    role: worker
    description: "Architecture agent"
    parent: cto
    personas: [arch]
    work_dir: /workspace/myapp
    backend: tmux
    initial_prompt: |
      You are arch, the architecture agent. Your manager is cto.
      Focus on structural integrity and clean domain boundaries.
```

### Schema quick reference

#### `space`

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `name` | string | yes | Used as target space name; override with `--space` |
| `description` | string | no | Human-readable description |
| `shared_contracts` | string | no | Prepended to every agent's ignition prompt |

#### `personas` (map of ID → definition)

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `name` | string | yes | Display name |
| `description` | string | no | Short role description |
| `prompt` | string | yes | Full persona prompt text |

Persona IDs are global across the server. To avoid collisions between teams, prefix with a project slug (e.g. `myapp-arch`, `myapp-sec`).

#### `agents` (map of agent name → definition)

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `role` | string | yes | `manager` or `worker` |
| `description` | string | no | Short role description |
| `parent` | string | no | Name of parent agent (must exist in same fleet file or space) |
| `personas` | list | no | Persona IDs to apply |
| `work_dir` | string | no | Working directory for tmux sessions |
| `backend` | string | no | `tmux` (default) or `ambient` |
| `initial_prompt` | string | no | Ignition prompt sent on spawn |

---

## Exporting

```bash
# Print fleet YAML to stdout
odis export "OpenDispatch Dev"

# Write to file
odis export "OpenDispatch Dev" --output fleet.yaml
```

Environment:
- `ODIS_URL` — coordinator URL (default: `http://localhost:8899`)
- `ODIS_API_TOKEN` — bearer token if auth is enabled

---

## Importing

```bash
# Preview what will change (no writes)
odis import fleet.yaml --dry-run

# Apply (prompts for confirmation)
odis import fleet.yaml

# Skip confirmation
odis import fleet.yaml --yes

# Override the target space
odis import fleet.yaml --space "Staging"

# Fail if the space doesn't exist (don't auto-create)
odis import fleet.yaml --no-create-space

# Also remove agents present in the space but absent from the fleet file
odis import fleet.yaml --prune

# --prune even if the agent has an active session (use with care)
odis import fleet.yaml --prune --force
```

### What import does

1. Reads and validates the fleet file (schema, cycles, command allowlist).
2. Upserts personas via the server API (creates new versions if prompt changed).
3. Topologically sorts agents by parent relationship.
4. Upserts agents in dependency order (parents before children).
5. If `--prune`: deletes agents in the target space that are not in the file.
6. Prints a summary of created/updated/unchanged/deleted items.

Import is **idempotent**: running it twice produces the same result.

---

## Common workflows

### Clone a team into a new space

```bash
odis export "Production" --output prod-fleet.yaml
odis import prod-fleet.yaml --space "Staging" --yes
```

### Version-control your team

Check `fleet.yaml` into your repository alongside the code it works on. Treat it like a `docker-compose.yml` — commit it when the team structure changes, PR-review persona changes, tag versions.

```bash
# After editing personas or adding agents:
git add fleet.yaml
git commit -m "chore: add sec agent to team"
git push
```

### Reset a space to a known-good state

```bash
odis import fleet.yaml --prune --yes
```

`--prune` removes agents that aren't in the file, so you end up with exactly what the file describes.

### Update a persona across all instances

Edit the persona's `prompt:` in `fleet.yaml`, then import:

```bash
odis import fleet.yaml --yes
```

The server creates a new persona version. Existing agents using the persona keep the old version until they are restarted and re-spawned with the new ignition.

---

## Security constraints

Two environment variables restrict what the import command accepts:

| Variable | Default | Effect |
|----------|---------|--------|
| `ODIS_COMMAND_ALLOWLIST` | `claude,claude-dev` | Agent `command` field must be in this comma-separated list |
| `ODIS_WORK_DIR_PREFIX` | _(unset)_ | If set, all `work_dir` values must start with this prefix |

These prevent arbitrary command injection or path traversal via a malicious fleet file.

---

## See also

- [agent-compose design spec](design-docs/agent-compose.md) — full schema reference and design rationale
- [CLAUDE.md](../CLAUDE.md) — `odis export` / `odis import` CLI reference
- [API Reference](api-reference.md) — `GET /spaces/:space/export` endpoint
