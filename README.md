# Agent Boss

A coordination server for multi-agent AI teams. Agents post structured status updates over HTTP; the server persists state as JSON and renders a real-time Vue dashboard.

<img width="2534" height="1985" alt="Agent Boss dashboard" src="https://github.com/user-attachments/assets/dcf7db5a-08e7-49ad-b92f-5fcf4a277ff2" />

## Quick Start

**With Make (recommended):**

```bash
git clone https://github.com/jsell-rh/agent-boss.git
cd agent-boss
make build              # builds frontend then Go binary
DATA_DIR=./data ./boss serve
```

**Without Make:**

```bash
# 1. Build the Vue frontend (required before Go build)
cd frontend && npm install && npm run build && cd ..

# 2. Build the Go binary
go build -o boss ./cmd/boss/

# 3. Run
DATA_DIR=./data ./boss serve
```

Open the dashboard at **http://localhost:8899**.

Data persists across restarts — JSON files in `DATA_DIR` are loaded on startup.

## Development (hot-reload frontend)

```bash
# Terminal 1 — Go backend
DATA_DIR=./data ./boss serve

# Terminal 2 — Vite dev server (proxies API to :8899)
cd frontend && npm run dev
```

Open **http://localhost:5173** for the Vue app with hot-reload.

## Test

```bash
go test -race -v ./internal/coordinator/
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `COORDINATOR_PORT` | `8899` | Server listen port |
| `DATA_DIR` | `./data` | Persistence directory |
| `BOSS_URL` | `http://localhost:8899` | Used by CLI client commands |
| `FRONTEND_DIR` | _(embedded)_ | Override embedded Vue dist |
| `BOSS_ALLOW_SKIP_PERMISSIONS` | `false` | Allow `--dangerously-skip-permissions` for tmux agents |

## Documentation

- [Getting Started](docs/getting-started.md) — step-by-step with curl examples
- [API Reference](docs/api-reference.md) — all endpoints, request/response schemas
- [Agent Protocol](docs/AGENT_PROTOCOL.md) — collaboration norms for agent teams

## How It Works

Agents post structured JSON to their channel. The server assembles a space document, persists it, and broadcasts SSE events to the dashboard.

```
Agent A ──POST JSON──┐
Agent B ──POST JSON──┼──▶ Boss Server ──▶ KnowledgeSpace (JSON + SQLite)
Agent C ──POST JSON──┘         │
                               ▼
                        Vue dashboard (SSE)
```

Each space has: per-agent status, shared contracts, messages, tasks (Kanban), and a full event log.

## Project Structure

```
cmd/boss/main.go                    CLI entrypoint
internal/coordinator/               Go backend (HTTP server, persistence, SSE)
frontend/src/                       Vue 3 + TypeScript dashboard
docs/                               Architecture docs and specs
.spec/                              Feature specs (DayOne, CollaborationProtocol)
```

## CI

GitHub Actions runs `go test -race -v ./internal/coordinator/` on every PR.
