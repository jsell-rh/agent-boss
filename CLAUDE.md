# Agent Boss — Development Guide

## Build

Requires Go 1.24.4. The system Go may differ — always use the explicit GOROOT:

```bash
GOROOT=/home/mturansk/go/go1.24.4.linux-amd64/go \
PATH=/home/mturansk/go/go1.24.4.linux-amd64/go/bin:$PATH \
go build -o /tmp/boss ./cmd/boss/
```

## Test

```bash
GOROOT=/home/mturansk/go/go1.24.4.linux-amd64/go \
PATH=/home/mturansk/go/go1.24.4.linux-amd64/go/bin:$PATH \
go test -race -v ./internal/coordinator/
```

## Run

```bash
DATA_DIR=./data /tmp/boss serve
```

Server starts on `:8899`. Dashboard at `http://localhost:8899`. Data persists to `DATA_DIR` as JSON + rendered markdown.

## Project Structure

```
cmd/boss/main.go                       CLI entrypoint (serve, post, check)
internal/coordinator/
  types.go                             AgentUpdate, KnowledgeSpace, markdown renderer
  server.go                            HTTP server, routing, persistence, SSE
  server_test.go                       Integration tests with -race
  client.go                            Go client for programmatic access
  deck.go                              Multi-space deck management
  static/mission-control.html          Dashboard frontend (HTML+CSS+JS, go:embed)
data/
  {space}.json                         Source of truth per space
  {space}.md                           Rendered markdown snapshot
  protocol.md                          Agent communication protocol template
```

## Key Conventions

- Zero external dependencies — stdlib only (see `go.mod`)
- Dashboard is a single embedded HTML file — all CSS and JS inline
- `static/mission-control.html` is embedded via `//go:embed` at compile time
- JSON is canonical; `.md` files are regenerated from JSON on every write
- Agent channel enforcement: POST requires `X-Agent-Name` header matching the URL path agent name
- Agent updates are structured JSON (`AgentUpdate` in `types.go`), not raw markdown

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `COORDINATOR_PORT` | `8899` | Server listen port |
| `DATA_DIR` | `./data` | Persistence directory |
| `BOSS_URL` | `http://localhost:8899` | Used by CLI client commands |

## Restart Procedure

```bash
pkill -f '/tmp/boss'
sleep 1
# rebuild (see Build above)
DATA_DIR=./data nohup /tmp/boss serve > /tmp/boss.log 2>&1 &
```

Data survives restarts — JSON files in `DATA_DIR` are loaded on startup.
