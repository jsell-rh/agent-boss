# OpenDispatch — Development Guide

## Build

The Vue frontend is embedded in the Go binary via `//go:embed`. You **must** build the frontend before building Go:

```bash
# Step 1: Build the Vue frontend (outputs to internal/coordinator/frontend/)
cd frontend && npm install && npm run build && cd ..

# Step 2: Build the Go binary (embeds the compiled frontend)
go build -o /tmp/odis ./cmd/boss/
```

The binary is self-contained — no `FRONTEND_DIR` env var needed at runtime.

## Test

```bash
go test -race -v ./internal/coordinator/
```

## Run

```bash
DATA_DIR=./data /tmp/odis serve
```

Server starts on `:8899`. Dashboard at `http://localhost:8899`. Data persists to `DATA_DIR/boss.db` (SQLite).

### Development (hot-reload frontend)

During frontend development, run the Vite dev server and the Go binary together:

```bash
# Terminal 1 — Go backend
DATA_DIR=./data /tmp/odis serve

# Terminal 2 — Vite dev server (proxies API to :8899)
cd frontend && npm run dev
```

The Vite dev server proxies `/spaces`, `/events`, `/api`, `/raw`, `/agent`, `/personas`, `/mcp`, and `/settings` to the Go backend. Open `http://localhost:5173` for the Vue app with hot-reload.

To override the embedded frontend at runtime (e.g. for testing a fresh build):

```bash
DATA_DIR=./data FRONTEND_DIR=./internal/coordinator/frontend /tmp/odis serve
```

## Fleet CLI (Export / Import)

Capture a running space as a portable `agent-compose.yaml` fleet file and replay it:

```bash
# Export a space to YAML (stdout or file)
odis export "My Space"
odis export "My Space" --output fleet.yaml

# Preview what import will do (no writes)
odis import fleet.yaml --dry-run

# Apply (interactive confirmation)
odis import fleet.yaml

# Apply without prompt; override target space
odis import fleet.yaml --yes --space "Staging"

# Also remove agents not in the fleet file
odis import fleet.yaml --prune --yes
```

See **[docs/fleet-guide.md](docs/fleet-guide.md)** for the full workflow guide, schema reference, and common patterns.

## Project Structure

```
cmd/boss/main.go                       CLI entrypoint (serve, post, check, export, import)
cmd/boss-observe/main.go               Standalone MCP observability plugin (4 read-only tools)
internal/coordinator/
  types.go                             AgentUpdate, KnowledgeSpace, markdown renderer
  server.go                            HTTP server, routing, persistence, SSE
  server_test.go                       Integration tests with -race
  client.go                            Go client for programmatic access
  deck.go                              Multi-space deck management
  frontend_embed.go                    go:embed declaration for Vue dist
  frontend/                            Vue build output (gitignored, built by npm run build)
frontend/
  src/                                 Vue 3 + TypeScript source
  vite.config.ts                       Vite config (outDir → ../internal/coordinator/frontend)
scripts/
  boss-observe.sh                      Mid-session curl wrapper for observability (no MCP restart needed)
data/
  boss.db                              SQLite database (primary store — spaces, agents, tasks, events)
  protocol.md                          Agent communication protocol template
```

## Key Conventions

- SQLite (`data/boss.db`) is the primary store — spaces, agents, tasks, messages, events, history, settings
- Zero external Go dependencies beyond GORM and glebarez/sqlite (pure-Go SQLite driver, no CGO)
- Vue SPA is embedded in the binary via `//go:embed all:frontend` in `frontend_embed.go`
- `npm run build` inside `frontend/` must run before `go build` to populate the embed dir
- `FRONTEND_DIR` env var overrides the embedded assets at runtime (useful during development)
- Agent channel enforcement: POST requires `X-Agent-Name` header matching the URL path agent name
- Agent updates are structured JSON (`AgentUpdate` in `types.go`), not raw markdown
- Legacy JSON/JSONL files in `DATA_DIR` are only read once (on first start with empty DB) for migration

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `COORDINATOR_PORT` | `8899` | Server listen port |
| `DATA_DIR` | `./data` | Persistence directory |
| `DB_TYPE` | `sqlite` | Database backend: `sqlite` or `postgres` |
| `DB_PATH` | `$DATA_DIR/boss.db` | SQLite file path |
| `DB_DSN` | _(required for postgres)_ | Postgres DSN (e.g. `host=... user=... dbname=... sslmode=disable`) |
| `ODIS_URL` | `http://localhost:8899` | Used by CLI client commands |
| `ODIS_API_TOKEN` | _(unset = open mode)_ | Bearer token for all mutating endpoints (POST/PATCH/DELETE/PUT). When unset, auth is disabled. |
| `ODIS_ALLOWED_ORIGINS` | _(unset)_ | Comma-separated extra CORS origins beyond `localhost:8899` and `localhost:5173` |
| `ODIS_ALLOW_SKIP_PERMISSIONS` | `false` | Set `true` to pass `--dangerously-skip-permissions` to Claude CLI in tmux sessions |
| `COORDINATOR_HOST` | _(all interfaces)_ | Listen interface override (e.g. `127.0.0.1`) |
| `STALENESS_THRESHOLD` | `5m` | Duration after which an agent heartbeat is considered stale |
| `LOG_FORMAT` | `text` | Log output format: `text` or `json` |
| `FRONTEND_DIR` | _(embedded)_ | Override embedded Vue dist with a local directory |
| `AMBIENT_API_URL` | _(unset)_ | Enable the ambient session backend; set to the ambient API base URL |
| `AMBIENT_TOKEN` | _(unset)_ | Auth token for the ambient API |
| `AMBIENT_PROJECT` | _(unset)_ | Project identifier for ambient sessions |
| `AMBIENT_WORKFLOW_URL` | _(unset)_ | Workflow URL used to launch ambient agents |
| `AMBIENT_WORKFLOW_BRANCH` | _(unset)_ | Git branch for the ambient workflow |
| `AMBIENT_WORKFLOW_PATH` | _(unset)_ | Path to workflow definition file for the ambient backend |
| `AMBIENT_SKIP_TLS_VERIFY` | `false` | Skip TLS verification for ambient API calls |
| `COORDINATOR_EXTERNAL_URL` | _(unset)_ | External URL injected into ambient sessions as `ODIS_URL` |
| `ODIS_COMMAND_ALLOWLIST` | `claude,claude-dev` | Comma-separated allowlist of valid launch commands for `odis import`. Prevents arbitrary command injection via fleet YAML. |
| `ODIS_WORK_DIR_PREFIX` | _(unset)_ | If set, all `work_dir` values in fleet YAML must start with this prefix. Restricts agent working directories to a safe subtree. |

**Backward compatibility:** The server accepts legacy `BOSS_*` environment variables (`BOSS_URL`, `BOSS_API_TOKEN`, `BOSS_ALLOW_SKIP_PERMISSIONS`) and falls back to them if the corresponding `ODIS_*` variables are not set.

## MCP Tool Stack Composition

Agents can be equipped with multiple MCP servers at spawn time via `--mcp-config`. The recommended tool stack for a dev instance:

```json
{"mcpServers":{
  "odis-mcp":     {"type":"http","url":"http://localhost:8899/mcp"},
  "boss-observe": {"type":"stdio","command":"./bin/boss-observe","args":["--boss-url","http://localhost:8899"]}
}}
```

**Note:** The MCP server is named `odis-mcp` to align with the OpenDispatch rebrand. The `boss-observe` tool retains its legacy name and `--boss-url` flag for backward compatibility.

Build `boss-observe` first:

```bash
go build -o ./bin/boss-observe ./cmd/boss-observe/
```

Pass the JSON to `claude --mcp-config` at agent spawn time (TASK-021 integrates this into the spawn flow). Tools become available from the start of the session.

### boss-observe tools

| Tool | Parameters | Description |
|------|-----------|-------------|
| `get_session_output` | `session_id`, `lines=50` | Last N lines from a tmux pane |
| `list_sessions` | `filter?` | All tmux sessions with idle/running status |
| `get_recent_events` | `space`, `limit=20`, `event_type?` | Recent events from boss event log |
| `get_agent_status` | `space`, `agent` | Combined health: status + session + last 10 output lines |

### Mid-session fallback (no MCP restart required)

When `boss-observe` is not registered in the current session, use the curl wrapper:

```bash
# Quick overview of all agents in a space
bash scripts/boss-observe.sh check-all "My Space"

# Get a specific agent's status + tmux output
bash scripts/boss-observe.sh get-agent-status "My Space" worker1

# Tail recent events
bash scripts/boss-observe.sh get-recent-events "My Space" 20 agent_updated

# See what an agent's tmux pane is showing
bash scripts/boss-observe.sh get-session-output my-space-worker1 50
```

## Restart Procedure

```bash
pkill -f '/tmp/odis serve'
sleep 1
git pull
cd frontend && npm install && npm run build && cd ..
go build -o /tmp/odis ./cmd/boss/
DATA_DIR=./data nohup /tmp/odis serve > /tmp/odis.log 2>&1 &
```

Data survives restarts — SQLite DB (`DATA_DIR/boss.db`) is loaded on startup.

## Knowledge Base

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — System map: domain layers, key files, invariants, data flows. Start here for a new contributor orientation.
- **[docs/index.md](docs/index.md)** — Table of contents for all docs grouped by type (design-docs, exec-plans, product-specs) with implementation status.
- **[docs/QUALITY.md](docs/QUALITY.md)** — Quality grades (A–D) for each major subsystem.
- **[docs/exec-plans/tech-debt-tracker.md](docs/exec-plans/tech-debt-tracker.md)** — Prioritized list of known tech debt items.

## Doc Gardening

The `garden` agent keeps the knowledge base current after every sprint. See **[docs/exec-plans/doc-gardening-agent.md](docs/exec-plans/doc-gardening-agent.md)** for the standing instructions — what to check, how to update grades, and how to open the PR.
## Linting

### TypeScript typechecking

Run the TypeScript typecheck locally before pushing:

```bash
make typecheck
```

To enforce this automatically on every commit, install the pre-commit hook:

```bash
make install-hooks
```

The hook runs `vue-tsc -b` in `frontend/` only when `.ts` or `.vue` files are staged. The same check runs as a standalone CI job (`typecheck`) on every PR in parallel with Go tests.

### Go architecture + taste-invariant linters

Run the architectural boundary and taste-invariant linters with:

```bash
go test ./internal/domain/... ./internal/coordinator/...
```

These tests fail if any of the following rules are violated:

### Boundary enforcement (`internal/domain/architecture_test.go`)
- `internal/domain/` must import **only** the Go standard library — no external packages, no coordinator, no adapters.
- `internal/adapters/` packages (once created) must not import sibling adapter packages.
- The domain boundary test logs the `internal/coordinator` import baseline for migration tracking.

### Taste invariants (`internal/coordinator/lint_test.go`)
1. **No `fmt.Print*` in server code** — use the structured logger (`log.Info`, `log.Error`, etc.). `fmt.Sprintf` for string formatting is fine; `fmt.Printf` / `fmt.Println` / `fmt.Fprintf(os.Stderr, ...)` are not.
2. **File size limit** — no new `.go` file in `internal/coordinator/` may exceed 600 lines. Files that already exceed this limit are grandfathered (see `grandfatheredLargeFiles` in `lint_test.go`) — do not add new files to the grandfather list without a cleanup task.
3. **Handler naming** — HTTP handler methods on `*Server` must follow `handle{Noun}{Verb}` (e.g. `handleAgentCreate`, `handleTaskGet`). Known legacy violations are grandfathered in `grandfatheredHandlers`. New handlers must conform.
4. **Agent experience surface** — `TmuxCreateOpts` literals that set `MCPServerURL` must also set `AgentToken`. See below.

When a linter test fails, the error message includes the rule and an exact remediation instruction.

## Agent Experience Invariants

Every agent spawn must deliver the full experience surface: MCP URL, auth token, working directory, and ignition prompt. The structural test `TestAgentExperienceSurfaceInvariants` (`internal/coordinator/lint_test.go`) enforces the most failure-prone coupling: **if `TmuxCreateOpts.MCPServerURL` is set, `AgentToken` must also be set**. This prevents the silent failure mode where auth is enabled on the server but spawned agents never receive the credential to call MCP tools.

See **[docs/design-docs/agent-experience-surface.md](docs/design-docs/agent-experience-surface.md)** for the full contract and spawn flow diagram.

## Dev Loop (per-worktree testing)

Each worktree can run its own isolated boss instance — its own port, its own `data-dev/` directory, its own PID file. Multiple agents can run dev instances in parallel without conflict.

### One-shot bootstrap

```bash
bash scripts/dev-setup.sh
```

This builds the boss binary and prints the MCP registration command:

```
claude mcp add odis-dev --transport http http://localhost:<PORT>/mcp
```

Register it once in your claude session to get `odis-dev` MCP tools pointed at your local instance.

### Daily workflow

```bash
# Start your isolated dev instance (auto-detects a free port >= 9000)
make dev-start

# Check status + last 20 log lines
make dev-status

# After code changes — rebuild and restart in one step
make dev-restart

# Shut down when done
make dev-stop
```

### Spawning a dev sub-agent

Agents that need a focused worker to iterate on a specific change can spawn a sub-agent with the full dev tool surface pre-wired:

```bash
make dev-spawn AGENT=<name> SPACE=<space>
# e.g.
make dev-spawn AGENT=worker1 SPACE="My Space"
```

This calls `scripts/spawn-dev-agent.sh`, which:

1. Starts the dev instance (`make dev-start`) if it is not already running
2. Reads the port from `data-dev/boss.port`
3. Launches a new tmux session with **both** MCP servers in `--mcp-config`:
   - `odis-mcp` — production coordinator (`http://localhost:8899/mcp`) for check-in, tasks, and messages; auth header set if `ODIS_API_TOKEN` is configured
   - `odis-dev` — local dev instance (`http://localhost:<PORT>/mcp`) for testing API changes
4. Uses `--strict-mcp-config` so the agent sees only these two servers
5. Wraps Claude in the standard restart loop so session loss is handled automatically

**What the spawned agent can do:**

| Capability | How |
|------------|-----|
| Check in / post status / tasks / messages | `odis-mcp.*` tools |
| Test API changes against local build | `odis-dev.*` tools |
| Rebuild and redeploy local binary | `make dev-restart` |
| Run Playwright e2e against dev instance | `make e2e-dev` |
| Observe running sessions | `boss-observe.*` tools (if registered) |

The dev instance is isolated (`data-dev/boss.db`), so the sub-agent can freely create spaces and post updates without affecting shared production state.

### What you can observe

Once started, use the `odis-dev` MCP tools (or `http://localhost:<PORT>`) to:
- Create spaces, post agent updates, create/move tasks
- Verify new API behavior against your branch's code
- Inspect logs via `make dev-status` or `tail -f data-dev/boss.log`

The dev instance uses `data-dev/boss.db` (separate from the production `data/boss.db`), so you can freely experiment without affecting shared state.

### Port files

| File | Contents |
|------|----------|
| `data-dev/boss.port` | Port the dev instance is/was listening on |
| `data-dev/boss.pid` | PID of the running process |
| `data-dev/boss.log` | Server log output |
| `data-dev/boss.db` | Isolated SQLite database |

## E2E Testing & Agent Frontend Visibility

### Run the full Playwright suite

```bash
make e2e           # build + start server + run all 15 specs + teardown
make e2e-ui        # same, headed (opens browser)
make e2e-report    # open the HTML report from the last run
```

### Validate changes against a running dev instance

```bash
# Start dev instance on a custom port (e.g. 9000):
DATA_DIR=./data COORDINATOR_PORT=9000 /tmp/odis serve

# In another terminal — run e2e without rebuilding:
DEV_PORT=9000 make e2e-dev
```

`make e2e-dev` sets `BASE_URL=http://localhost:${DEV_PORT:-9000}` and `SKIP_BUILD=1`, so it
tests against your live instance without rebuilding the binary.

### Visual snapshots — see the current UI state

```bash
make e2e-screenshots          # capture key pages from http://localhost:8899
ODIS_URL=http://localhost:9000 make e2e-screenshots   # custom URL
```

This runs `e2e/scripts/screenshots.ts` and saves PNGs to `e2e/snapshots/`:

- `01-home.png` — landing page
- `02-space.png` — first space overview
- `03-kanban.png` — task kanban board
- `04-conversations.png` — conversation log
- `05-agent-detail.png` — agent detail panel

Agents can read these images to understand current UI state without a browser.

### Interactive browser control via Playwright MCP

Add the Playwright MCP server to give Claude direct browser access to your dev instance:

```bash
claude mcp add playwright npx @playwright/mcp --browser chromium
```

Then in a Claude session you can navigate, click, fill forms, and take screenshots
against the running dev instance to validate UI changes interactively.
