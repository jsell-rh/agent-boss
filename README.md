# Boss Coordinator

A shared memory bus for multi-agent AI coordination. Agents post structured status updates to a central HTTP server, which persists state as JSON (with optional SQLite backing) and renders human-readable markdown. A Vue 3 dashboard provides real-time mission control.

![Mission Control Dashboard](img.png)

## The Problem: Cold Context

Multi-agent AI development has a fundamental problem: agents forget. Every time a session compacts, resumes, or starts fresh, the model reconstructs its understanding from scratch. This leads to predictable failures:

1. **Contradictory decisions.** Agent A decides on approach X, compacts, and picks approach Y because it forgot the reasoning.
2. **Duplicated work.** Agent B solves a problem Agent C already solved three hours ago.
3. **Lost tribal knowledge.** The team discovered KSUIDs contain uppercase letters and K8s rejects them. Nobody wrote it down. The next agent hits the same bug.
4. **Coordination breakdown.** Five agents working on the same system, each with a different understanding of the API contract.

## The Solution: Shared Blackboard

Boss gives agents a shared, persistent, structured document that acts as collective memory. Instead of each agent maintaining its own private understanding that evaporates at compaction, they all read from and write to a single source of truth that outlives any individual session.

```
Agent A ──POST JSON──┐
Agent B ──POST JSON──┤
Agent C ──POST JSON──┼──▶ Boss Server ──▶ KnowledgeSpace (in-memory + SQLite)
Agent D ──POST JSON──┤         │                  │
Agent E ──POST JSON──┘         ▼                  ▼
                          space.json          space.md
                         (structured)     (human-readable)
```

Each space is organized into sections with specific purposes:

- **Session Dashboard:** One-line status per agent. Any agent (or human) can glance at this and know who is doing what.
- **Shared Contracts:** Agreed API surfaces, pagination rules, naming conventions. Hard truths no agent may contradict.
- **Agent Sections:** Each agent's current status, decisions, open questions, and technical details.
- **Archive:** Resolved items that no longer need active context but should be recoverable.

A fresh agent reads the document top-down and reconstructs its understanding in a single pass.

### Why structured data over raw markdown?

Raw markdown coordination documents corrupt easily when multiple agents splice text concurrently. Structured JSON input with server-side markdown rendering eliminates broken tables, dashboard corruption, and lost sections. Agents POST structured data. The server assembles guaranteed well-formed markdown.

## Context Warmth

Context warmth has four dimensions:

| Dimension | Cold | Warm | How the coordinator helps |
|---|---|---|---|
| **Situational awareness** | "What's happening?" | Agent knows every peer's status | Dashboard table, read on every cycle |
| **Technical fidelity** | "What are the rules?" | Agent knows exact API contracts, state machines | Shared Contracts section |
| **Task continuity** | "What was I doing?" | Agent can resume mid-task after compaction | Agent's own section with timestamped entries |
| **Decision history** | "Why did we choose X?" | Agent knows past decisions and reasoning | Archive section with Key Decisions log |

A fully warm agent scores high on all four. The blackboard provides all four in a single read. In practice, an agent recovers to approximately 95% effectiveness after compaction. The 5% loss is procedural memory, not factual knowledge.

### The Foreman Pattern

One agent (the Foreman) acts as the managing agent. It reads every other agent's section, evaluates progress, identifies blockers, issues standing orders, and tracks overall progress. The Foreman doesn't write code — it writes strategy.

This creates a hierarchy: agents write to their sections, the Foreman reads all sections and posts directives, the Boss (human) reads the Foreman's analysis and makes final calls. Questions flow up (tagged with `[?BOSS]`), decisions flow down (via standing orders).

## Production Results

We coordinated 6 agents (API, Control Plane, SDK, Backend Expert, Frontend, Overlord) through a full platform refactoring: replacing a monolithic Go backend (75 routes, K8s CRDs/etcd) with three focused components (API server + PostgreSQL, control plane, multi-language SDK).

- **370 tests written across 5 components in a single day.** No test contradicted another component's contract.
- **Zero coordination conflicts.** Five agents writing concurrently, never clobbering each other's content.
- **Recovery from compaction in one read.** Agents read the blackboard and were immediately productive.
- **Bugs caught before they spread.** The Backend Expert identified 6 behavioral gaps. Every agent saw the same gap list.
- **Progressive decision-making.** Ambiguous questions were tagged `[?BOSS]`, decided by the human, and recorded in Shared Contracts. Every agent respected the ruling.

## Anti-Patterns

Things that degrade context warmth:

1. **Skipping the read.** An agent that posts without reading first will contradict something decided while it was compacted.
2. **Hoarding context.** Keeping important information in your own section instead of promoting it to Shared Contracts.
3. **Stale standing orders.** Orders that were completed but never archived. Agents waste time figuring out if a directive is current or historical.
4. **Unbounded sections.** An agent that never compacts. Its section grows until it dominates the document.
5. **Silent agents.** The Foreman can't evaluate what it can't see. Other agents can't coordinate with a ghost.

## Quick Start

```bash
go build -o boss ./cmd/boss/
DATA_DIR=./data ./boss serve
open http://localhost:8899
```

Server starts on `:8899` (configurable via `COORDINATOR_PORT`). Dashboard at `http://localhost:8899`. Data persists to `DATA_DIR` as JSON + rendered markdown.

### Development (hot-reload frontend)

During frontend development, run the Vite dev server and the Go binary together:

```bash
# Terminal 1 — Go backend
DATA_DIR=./data ./boss serve

# Terminal 2 — Vite dev server (proxies API to :8899)
cd frontend && npm run dev
```

The Vite dev server proxies `/spaces`, `/events`, `/api`, and `/agent` to the Go backend. Open `http://localhost:5173` for the Vue app with hot-reload.

### Paude (Secure Container) Setup

For secure multi-agent coordination with minimal human interrupts:

```bash
# 1. Build Paude base image (first time only)
git clone https://github.com/bbrowning/paude.git
cd paude && podman build -t localhost/paude-proxy-centos9:latest .

# 2. Build integrated Claude Code image
./scripts/build-paude-claude.sh

# 3. Start Agent Boss server
DATA_DIR=./data ./boss serve

# 4. Boot all agents in secure containers
./scripts/boss.sh sdk-backend-replacement
```

See [docs/paude.md](docs/paude.md) for the complete integration guide.

## API Reference

See [docs/api-reference.md](docs/api-reference.md) for the full API reference including request/response schemas and examples.

### Spaces

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/` | HTML dashboard listing all spaces |
| `GET` | `/spaces` | JSON array of space summaries |
| `GET` | `/spaces/{space}/` | HTML viewer (auto-polls every 3s) |
| `GET` | `/spaces/{space}/raw` | Full space as markdown |
| `DELETE` | `/spaces/{space}` | Delete a space |

### Agents

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/spaces/{space}/agent/{name}` | Get agent state as JSON |
| `POST` | `/spaces/{space}/agent/{name}` | Update agent status (requires `X-Agent-Name`) |
| `DELETE` | `/spaces/{space}/agent/{name}` | Remove agent from space |
| `GET` | `/spaces/{space}/api/agents` | All agents as JSON map |

### Messages & Conversations

Agents communicate point-to-point via the message API. The dashboard renders a Conversations view grouped by agent pairs.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/spaces/{space}/agent/{name}/message` | Send a message to an agent |
| `GET` | `/spaces/{space}/agent/{name}/messages` | Read messages with cursor pagination |
| `POST` | `/spaces/{space}/agent/{name}/message/{id}/ack` | Acknowledge a message (marks read) |

**Cursor pagination:** `GET /messages?since=<cursor>` returns only new messages since the last check-in. The response includes a `cursor` field to save for the next call. This avoids re-reading the full history on every check-in.

**Message priority:** Messages carry a `priority` field: `info`, `directive`, or `urgent`.

### Tasks (Kanban Board)

A built-in Kanban board tracks work items across a space. Tasks move through columns: `backlog → in_progress → review → done` (or `blocked`). The Vue dashboard renders the board with column-based task cards.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/spaces/{space}/tasks` | List tasks (filter: `?status=`, `?assigned_to=`, `?priority=`) |
| `POST` | `/spaces/{space}/tasks` | Create a task |
| `GET` | `/spaces/{space}/tasks/{id}` | Get task detail |
| `PUT` | `/spaces/{space}/tasks/{id}` | Update task fields |
| `DELETE` | `/spaces/{space}/tasks/{id}` | Delete a task |
| `POST` | `/spaces/{space}/tasks/{id}/move` | Move task to a different status column |
| `POST` | `/spaces/{space}/tasks/{id}/assign` | Assign task to an agent |
| `POST` | `/spaces/{space}/tasks/{id}/comment` | Add a comment to a task |
| `POST` | `/spaces/{space}/tasks/{id}/subtasks` | Create a subtask under this task |

When a task is assigned, the coordinator automatically sends a `task_assigned` message to the assigned agent's inbox and delivers a notification in their `#### Messages` section.

Task fields: `id`, `title`, `description`, `status`, `priority` (`low`/`medium`/`high`/`urgent`), `assigned_to`, `labels`, `parent_task`, `subtasks`, `linked_branch`, `linked_pr`, `comments`, `events`.

### Registration & Heartbeat

For non-tmux agents (scripts, CLI tools, remote processes, HTTP services):

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/spaces/{space}/agent/{name}/register` | Register agent with type, capabilities, and optional callback URL |
| `POST` | `/spaces/{space}/agent/{name}/heartbeat` | Send liveness heartbeat |

Registration fields:

| Field | Description |
|-------|-------------|
| `agent_type` | `"tmux"`, `"http"`, `"cli"`, `"script"`, `"remote"` |
| `capabilities` | Free-form list: `["code", "research", "review"]` |
| `heartbeat_interval_sec` | Expected heartbeat cadence (0 = no tracking) |
| `callback_url` | Webhook URL for push message delivery instead of polling |
| `parent` | Manager agent name for hierarchy pre-registration |

Registration is optional for tmux agents (backward compatible) but required for HTTP agents that want heartbeat tracking or webhook delivery.

### SSE Streams

Real-time push events. Per-agent streams deliver only events targeted at that agent.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/events` | Global SSE stream (all spaces) |
| `GET` | `/spaces/{space}/events` | Space-wide SSE stream |
| `GET` | `/spaces/{space}/agent/{name}/events` | Per-agent SSE stream with Last-Event-ID replay |

### Lifecycle

Manage agent tmux sessions remotely. Agents registered with `agent_type != "tmux"` receive `HTTP 422` from these endpoints with an error directing them to manage their own process externally.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/spaces/{space}/agent/{name}/spawn` | Create tmux session and launch agent command |
| `POST` | `/spaces/{space}/agent/{name}/stop` | Kill agent's tmux session |
| `POST` | `/spaces/{space}/agent/{name}/restart` | Kill and respawn agent's tmux session |
| `GET` | `/spaces/{space}/agent/{name}/introspect` | Registration info, liveness state, and captured pane output |
| `GET` | `/spaces/{space}/agent/{name}/history` | Historical status snapshots for an agent |
| `GET` | `/spaces/{space}/history` | All-agent history snapshots for a space |

Spawn options (POST body): `tmux_session`, `command` (default: `claude --dangerously-skip-permissions`), `width`, `height`. On spawn, the server automatically sends a `/boss.ignite` command to the new session after a 5-second initialization delay.

### Agent Hierarchy

Agents can declare a `parent` to form a management tree. The coordinator tracks the hierarchy and renders it in the dashboard.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/spaces/{space}/hierarchy` | Full hierarchy tree as JSON |

Set hierarchy via status POST: include `"parent": "ManagerName"` and `"role": "Developer"`. Both are sticky once set.

### Ignition

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/spaces/{space}/ignition/{agent}?session_id=` | Bootstrap agent with context, peer list, standing orders, and pending messages |

Append `&parent=NAME&role=ROLE` to pre-register hierarchy position. The `session_id` is sticky — the server preserves it after first registration.

### Shared Data

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET/POST` | `/spaces/{space}/contracts` | Shared contracts (append-only text) |
| `GET/POST` | `/spaces/{space}/archive` | Archive of resolved items |

### Backward Compatibility

Routes without `/spaces/` prefix operate on the `"default"` space:

| Endpoint | Equivalent |
|----------|------------|
| `/raw` | `/spaces/default/raw` |
| `/agent/{name}` | `/spaces/default/agent/{name}` |
| `/api/agents` | `/spaces/default/api/agents` |

## Agent Update Format

```json
{
  "status": "active",
  "summary": "One-line summary (required)",
  "branch": "feat/my-feature",
  "pr": "#42",
  "repo_url": "https://github.com/org/repo",
  "phase": "implementation",
  "test_count": 88,
  "parent": "ManagerAgent",
  "role": "Developer",
  "items": ["bullet point 1", "bullet point 2"],
  "sections": [
    {
      "title": "Section Name",
      "items": ["detail 1", "detail 2"],
      "table": {
        "headers": ["Col A", "Col B"],
        "rows": [["val1", "val2"]]
      }
    }
  ],
  "questions": ["auto-tagged with [?BOSS] in rendered output"],
  "blockers": ["rendered with red indicator"],
  "next_steps": "What you plan to do next"
}
```

### Status Values

| Status | Emoji | Meaning |
|--------|-------|---------|
| `active` | 🟢 | Currently working |
| `done` | ✅ | Work complete |
| `blocked` | 🔴 | Waiting on dependency |
| `idle` | ⏸️ | Standing by |
| `error` | ❌ | Something failed |

### Sticky Fields

Several fields are preserved across status POSTs once sent — omitting them does not clear them:

| Field | Description |
|-------|-------------|
| `repo_url` | Full HTTPS URL of the git repository (used for dashboard PR links) |
| `parent` | Manager agent name (hierarchy) |
| `session_id` | Registered via `?session_id=` on the ignition endpoint |

### Plain Text Fallback

If you POST with `Content-Type: text/plain`, the body is wrapped into an `AgentUpdate` with `status: active` and the first line as `summary`:

```bash
curl -s -X POST http://localhost:8899/spaces/my-feature/agent/api \
  -H 'Content-Type: text/plain' \
  -H 'X-Agent-Name: api' \
  --data-binary @/tmp/my_update.md
```

## Channel Enforcement

Agents must identify themselves via the `X-Agent-Name` header on every POST. The header value must match the agent name in the URL path (case-insensitive). This prevents agents from posting to each other's channels.

```bash
# Accepted: header matches URL
curl -X POST http://localhost:8899/spaces/my-feature/agent/API \
  -H 'X-Agent-Name: API' \
  -H 'Content-Type: application/json' \
  -d '{"status":"active","summary":"API: working"}'

# Rejected (403): Bob cannot post to API's channel
curl -X POST http://localhost:8899/spaces/my-feature/agent/API \
  -H 'X-Agent-Name: Bob' \
  -H 'Content-Type: application/json' \
  -d '{"status":"active","summary":"impersonation attempt"}'
```

## Distributed Agent Architecture

The bus is agent-location-agnostic. Any process that can HTTP POST can participate, regardless of where it runs:

| Use Case | Agent Location | Bus Sees |
|----------|---------------|----------|
| Local development | Claude Code terminal | `{"status":"active","summary":"API: 51 tests passing"}` |
| Cloud IDE | Ambient pod on Kubernetes | `{"status":"done","summary":"SDK: generated 3 clients"}` |
| Security isolation | Air-gapped pod with IAM role | `{"status":"done","summary":"Rotation successful"}` |
| GPU workloads | Node with H100 + vector DB | `{"status":"done","summary":"Analysis complete"}` |

The `AgentUpdate` schema is the declassification boundary. Agents distill privileged access into structured results. The bus never sees raw credentials, embeddings, or PII.

## Persistence

On every mutation the server writes files to `DATA_DIR`:

| File | Format | Purpose |
|------|--------|---------|
| `{space}.json` | Structured JSON | Source of truth, loaded on startup |
| `{space}.md` | Rendered markdown | Human-readable snapshot |
| `{space}-history.json` | Append-only NDJSON | Status snapshots for the history API |

The `.md` file is regenerated from the `.json` on every write. JSON is canonical.

An optional SQLite backend (via the `db/` package) is available for deployments that need relational queries or concurrent write durability beyond file-based storage.

## Observability

The server emits structured domain events to `DATA_DIR/events.jsonl`. Each event carries a timestamp, type, and context:

| Category | Event Types |
|----------|------------|
| Agent lifecycle | `agent.spawned`, `agent.stopped`, `agent.restarted` |
| Agent status | `agent.status_updated`, `agent.created`, `agent.removed` |
| Message delivery | `message.delivered`, `message.acked`, `webhook.delivered` |
| Liveness | `liveness.agent_stale`, `liveness.heartbeat_received`, `liveness.nudge_triggered` |
| Registration | `registration.agent_registered` |
| Persistence | `persistence.space_loaded`, `persistence.space_created` |

## CI

GitHub Actions runs on every pull request and push to `main`:

```yaml
go test -race -v ./internal/coordinator/
```

## Project Structure

```
cmd/boss/main.go                          CLI entrypoint (serve, post, check)
internal/coordinator/
  types.go                                AgentUpdate, Task, KnowledgeSpace, markdown renderer
  server.go                               HTTP server, routing, persistence, SSE
  handlers_agent.go                       Agent POST/GET/DELETE, ignition, messaging
  handlers_space.go                       Space-level routes (raw, contracts, archive, tasks)
  handlers_sse.go                         SSE stream endpoints
  handlers_task.go                        Task CRUD + move/assign/comment/subtask actions
  lifecycle.go                            Spawn/stop/restart/introspect (tmux lifecycle)
  liveness.go                             Heartbeat tracking, staleness detection
  protocol.go                             Agent registration and webhook delivery
  history.go                              Status snapshot append and query
  journal.go                              Structured event logging
  logger.go                               Domain event types and write-ahead log
  session_backend.go                      Session backend interface
  session_backend_tmux.go                 tmux session backend
  session_backend_ambient.go              Ambient (non-tmux) session backend
  storage.go                              JSON file persistence
  db/                                     SQLite/GORM storage layer
  db_adapter.go                           Storage interface adapter
  deck.go                                 Multi-space deck management
  client.go                               Go client for programmatic access
  frontend_embed.go                       go:embed declaration for Vue dist
  frontend/                               Vue build output (gitignored, built by npm run build)
  server_test.go                          Integration tests with -race
frontend/
  src/                                    Vue 3 + TypeScript source
  vite.config.ts                          Vite config (outDir → ../internal/coordinator/frontend)
data/
  {space}.json                            Source of truth per space
  {space}.md                              Rendered markdown snapshot
  {space}-history.json                    Append-only status snapshot log
  events.jsonl                            Structured domain event log
docs/                                     Architecture specs and design documents
```

## Key Conventions

- Vue SPA is embedded in the binary via `//go:embed all:frontend` in `frontend_embed.go`
- `npm run build` inside `frontend/` must run before `go build` to populate the embed dir
- `FRONTEND_DIR` env var overrides the embedded assets at runtime (useful during development)
- JSON is canonical; `.md` files are regenerated from JSON on every write
- Agent channel enforcement: POST requires `X-Agent-Name` header matching the URL path agent name
- Agent updates are structured JSON (`AgentUpdate` in `types.go`), not raw markdown
- Non-tmux agents (registered with `agent_type != "tmux"`) receive HTTP 422 from lifecycle endpoints with an error message directing them to manage their own process

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `COORDINATOR_PORT` | `8899` | Server listen port |
| `DATA_DIR` | `./data` | Persistence directory |
| `BOSS_URL` | `http://localhost:8899` | Used by CLI client commands |
| `FRONTEND_DIR` | _(embedded)_ | Override embedded Vue dist with a local directory |

## Build

The Vue frontend is embedded in the Go binary via `//go:embed`. You must build the frontend before building Go:

```bash
# Step 1: Build the Vue frontend (outputs to internal/coordinator/frontend/)
cd frontend && npm install && npm run build && cd ..

# Step 2: Build the Go binary (embeds the compiled frontend)
go build -o boss ./cmd/boss/
```

The binary is self-contained — no `FRONTEND_DIR` env var needed at runtime.

## Test

```bash
go test -race -v ./internal/coordinator/
```
