# Agent Boss — Development Guide

## Build

The Vue frontend is embedded in the Go binary via `//go:embed`. You **must** build the frontend before building Go:

```bash
# Step 1: Build the Vue frontend (outputs to internal/coordinator/frontend/)
cd frontend && npm install && npm run build && cd ..

# Step 2: Build the Go binary (embeds the compiled frontend)
go build -o /tmp/boss ./cmd/boss/
```

The binary is self-contained — no `FRONTEND_DIR` env var needed at runtime.

## Test

```bash
go test -race -v ./internal/coordinator/
```

## Run

```bash
DATA_DIR=./data /tmp/boss serve
```

Server starts on `:8899`. Dashboard at `http://localhost:8899`. Data persists to `DATA_DIR/boss.db` (SQLite).

### Development (hot-reload frontend)

During frontend development, run the Vite dev server and the Go binary together:

```bash
# Terminal 1 — Go backend
DATA_DIR=./data /tmp/boss serve

# Terminal 2 — Vite dev server (proxies API to :8899)
cd frontend && npm run dev
```

The Vite dev server proxies `/spaces`, `/events`, `/api`, `/raw`, and `/agent` to the Go backend. Open `http://localhost:5173` for the Vue app with hot-reload.

To override the embedded frontend at runtime (e.g. for testing a fresh build):

```bash
DATA_DIR=./data FRONTEND_DIR=./internal/coordinator/frontend /tmp/boss serve
```

## Project Structure

```
cmd/boss/main.go                       CLI entrypoint (serve, post, check)
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
| `BOSS_URL` | `http://localhost:8899` | Used by CLI client commands |
| `FRONTEND_DIR` | _(embedded)_ | Override embedded Vue dist with a local directory |

## Restart Procedure

```bash
pkill -f '/tmp/boss'
sleep 1
# rebuild (see Build above)
DATA_DIR=./data nohup /tmp/boss serve > /tmp/boss.log 2>&1 &
```

Data survives restarts — SQLite DB (`DATA_DIR/boss.db`) is loaded on startup.
