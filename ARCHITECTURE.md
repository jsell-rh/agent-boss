# Agent Boss — Architecture

Agent Boss is a self-contained coordination server for multi-agent AI workflows. Agents post structured status updates and messages over HTTP; the server persists state in SQLite and renders a Vue SPA dashboard.

---

## Domain Layers

```
┌─────────────────────────────────────────────────────────────┐
│  CLI  (cmd/boss/main.go)                                    │
│  serve | post | check                                       │
└────────────────────┬────────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────────┐
│  HTTP Server  (internal/coordinator/server.go)              │
│  • Routing (net/http mux)                                   │
│  • Server struct + lifecycle (Start/Stop)                   │
│  • SSE fan-out (handlers_sse.go)                            │
│  • MCP server (mcp_server.go + mcp_tools.go)               │
└───┬────────────┬────────────────┬────────────────┬──────────┘
    │            │                │                │
┌───▼──┐  ┌─────▼─────┐  ┌──────▼──────┐  ┌──────▼──────┐
│Space │  │  Agent    │  │  Task       │  │  Persona    │
│handlers  │  handlers │  │  handlers   │  │  handlers   │
│_space│  │  _agent   │  │  _task.go   │  │  personas.go│
│.go   │  │  .go      │  │  (887 LOC)  │  │  (580 LOC)  │
└───┬──┘  └─────┬─────┘  └──────┬──────┘  └─────────────┘
    │            │               │
┌───▼────────────▼───────────────▼───────────────────────────┐
│  Domain Types  (types.go — 802 LOC)                        │
│  AgentUpdate · KnowledgeSpace · AgentRecord                 │
│  Task · TaskComment · TaskEvent                             │
│  Persona · PersonaRef · PersonaVersion                      │
│  HierarchyTree · HierarchyNode                              │
│  AgentMessage · AgentNotification                           │
└────────────────────────┬───────────────────────────────────┘
                         │
┌────────────────────────▼───────────────────────────────────┐
│  Persistence Layer                                         │
│  storage.go        — space load/save, migration            │
│  db/               — GORM models + Repository              │
│  db_adapter.go     — bridge between domain ↔ GORM (552 LOC)│
│  journal.go        — event ring buffer + SQLite sink       │
│  history.go        — status snapshot log                   │
│  interrupts.go     — approval/interrupt ledger             │
└────────────────────────┬───────────────────────────────────┘
                         │
┌────────────────────────▼───────────────────────────────────┐
│  Session Backends                                          │
│  session_backend.go           — interface                  │
│  session_backend_tmux.go      — tmux pane management       │
│  session_backend_ambient.go   — Ambient cloud API          │
│  tmux.go                      — low-level tmux commands    │
└────────────────────────────────────────────────────────────┘

── Hexagonal Foundation (Phase 1 complete, Phase 2 planned) ──

┌────────────────────────────────────────────────────────────┐
│  internal/domain/  (PR #145)                               │
│  types.go          — canonical domain entities             │
│  ports/storage.go  — storage port interface                │
│  architecture_test.go — adapter isolation guard            │
│                                                            │
│  internal/adapters/ — NOT YET CREATED (Phase 2)           │
│  Planned: sqlite/, http/, mcp/, sse/ adapters              │
└────────────────────────────────────────────────────────────┘
```

---

## Key Files

| File | LOC | Purpose |
|------|-----|---------|
| `internal/coordinator/server.go` | 374 | Server struct, routing, Start/Stop |
| `internal/coordinator/types.go` | 804 | All domain types + markdown rendering |
| `internal/coordinator/handlers_agent.go` | 1807 | Agent HTTP handlers (status, spawn, messages) |
| `internal/coordinator/handlers_task.go` | 887 | Task CRUD + Kanban move |
| `internal/coordinator/handlers_space.go` | 567 | Space CRUD, hierarchy, bulk ops, fleet export route |
| `internal/coordinator/handlers_space_messages.go` | 85 | Space-level message broadcast handlers |
| `internal/coordinator/handlers_sse.go` | ~150 | SSE streaming, per-agent ring buffer (cap 200) |
| `internal/coordinator/fleet.go` | 404 | Fleet export/import: YAML blueprint, security validators (PR #231) |
| `internal/coordinator/mcp_tools.go` | 1184 | All MCP tool implementations |
| `internal/coordinator/mcp_server.go` | ~200 | MCP server setup, tool registration |
| `internal/coordinator/lifecycle.go` | 912 | Agent liveness, staleness, nudging |
| `internal/coordinator/liveness.go` | 265 | Liveness probe loop (split from lifecycle.go) |
| `internal/coordinator/personas.go` | 572 | Persona CRUD + version history |
| `internal/coordinator/protocol.go` | 496 | Protocol template rendering |
| `internal/coordinator/journal.go` | 527 | SpaceEvent log (ring buffer + SQLite) |
| `internal/coordinator/storage.go` | 295 | Space load/save, migration (extracted from server.go) |
| `internal/coordinator/middleware.go` | 250 | HTTP middleware: auth, CORS, logging |
| `internal/coordinator/logger.go` | 200 | Structured logger setup |
| `internal/coordinator/helpers.go` | 189 | Shared handler helpers |
| `internal/coordinator/db_adapter.go` | 552 | GORM ↔ domain type bridge |
| `internal/coordinator/tmux.go` | 723 | Tmux session commands |
| `internal/coordinator/session_backend_ambient.go` | 513 | Ambient cloud session backend |
| `frontend/src/components/SpaceOverview.vue` | 1448 | Main dashboard view |
| `frontend/src/components/AgentDetail.vue` | 1300 | Per-agent detail panel |
| `frontend/src/components/ImportFleetModal.vue` | 428 | Fleet YAML import modal (PR #230) |
| `frontend/src/api/client.ts` | 552 | REST API client |
| `internal/domain/types.go` | — | Canonical domain entities (hexagonal Phase 1) |
| `internal/domain/ports/storage.go` | — | Storage port interface (hexagonal Phase 1) |

---

## Invariants

1. **SQLite is the source of truth.** All spaces, agents, tasks, messages, and events are persisted to `DATA_DIR/boss.db`. JSON/JSONL legacy files are migrated on first start and then ignored.

2. **Agent channel enforcement.** POST to `/spaces/{space}/agents/{agent}` requires `X-Agent-Name: {agent}` header. Mismatch → 403.

3. **Agent updates are additive.** Omitting a field in a status POST does not clear it (sticky fields: `branch`, `pr`, `session_id`, `parent`, `registration`).

4. **Children are server-managed.** Agents set `parent`; `children` is computed by `rebuildChildren()` after every status change.

5. **No CGO.** Uses `glebarez/sqlite` (pure-Go SQLite driver). Zero C dependencies.

6. **Frontend is embedded.** Vue SPA is compiled by `npm run build` and embedded via `//go:embed all:frontend`. `FRONTEND_DIR` env var overrides at runtime.

7. **Cycle guard.** `hasCycle()` is called before accepting a `parent` assignment. Cycles are rejected with 409.

8. **SSE ring buffer.** Per-agent SSE event buffer capped at 200 events, keyed `"space/agent"`. Supports `Last-Event-ID` replay.

---

## Data Flow: Agent Status POST

```
Agent → POST /spaces/{space}/agents/{agent}
         X-Agent-Name: {agent}
         Body: AgentUpdate JSON
  ↓
handlers_agent.go: validate, resolve sticky fields
  ↓
types.go: rebuildChildren(), hasCycle()
  ↓
db_adapter.go: upsert agent record to SQLite
  ↓
journal.go: append SpaceEvent to ring buffer + SQLite
  ↓
handlers_sse.go: broadcast to all SSE subscribers
  ↓
lifecycle.go: reset staleness clock
```

---

## Data Flow: Agent Spawn

```
Operator → POST /spaces/{space}/agents/{agent}/spawn
  ↓
handlers_agent.go: load AgentConfig, select backend
  ↓
session_backend.go: interface dispatch
  ├─ tmux: tmux.go → new window, send ignition prompt
  └─ ambient: session_backend_ambient.go → POST to Ambient API
  ↓
protocol.go: render ignition prompt from template
```

---

## Subsystems

- **Knowledge Base:** see [docs/index.md](docs/index.md)
- **Task System:** see [docs/task-system-design.md](docs/task-system-design.md)
- **Hierarchy:** see [docs/hierarchy-design.md](docs/hierarchy-design.md)
- **SSE Streaming:** see [docs/sse-design.md](docs/sse-design.md)
- **Agent Lifecycle:** see [docs/lifecycle-spec.md](docs/lifecycle-spec.md)
- **API Reference:** see [docs/api-reference.md](docs/api-reference.md)
- **Hexagonal Architecture:** see [docs/design-docs/hexagonal-architecture.md](docs/design-docs/hexagonal-architecture.md)
- **Auth Model:** see [docs/design-docs/auth-model.md](docs/design-docs/auth-model.md)
- **Quality & Tech Debt:** see [docs/QUALITY.md](docs/QUALITY.md) and [docs/exec-plans/tech-debt-tracker.md](docs/exec-plans/tech-debt-tracker.md)
