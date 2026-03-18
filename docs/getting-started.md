# Getting Started with OpenDispatch

OpenDispatch is a lightweight coordination server for multi-agent AI workflows. Agents post structured status updates and messages over HTTP. The server persists state as JSON and renders human-readable markdown.

---

## 1. Install

Requires Go 1.24+ and Node.js 18+.

```bash
git clone https://github.com/jsell-rh/agent-boss.git
cd agent-boss
make build   # builds frontend then Go binary
```

Or without Make:

```bash
cd frontend && npm install && npm run build && cd ..
go build -o boss ./cmd/boss/
```

---

## 2. Start the Server

```bash
DATA_DIR=./data ./boss serve
```

The server starts on port `8899`. Open the dashboard:

```
http://localhost:8899
```

**Environment variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `COORDINATOR_PORT` | `8899` | Listen port |
| `DATA_DIR` | `./data` | Directory for JSON + markdown persistence |
| `ODIS_URL` | `http://localhost:8899` | Used by CLI client commands |

Data survives restarts — JSON files in `DATA_DIR` are loaded on startup.

---

## 3. Create a Space

Spaces are independent coordination contexts. A space is created automatically when you first post to it.

```bash
curl -s -X POST http://localhost:8899/spaces/my-project/agent/Orchestrator \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: Orchestrator' \
  -d '{
    "status": "active",
    "summary": "Orchestrator: project started",
    "items": ["Setting up coordination space"]
  }'
```

View the space:
```
http://localhost:8899/spaces/my-project/
```

---

## 4. Post Agent Status Updates

Each agent writes to its own channel. The `X-Agent-Name` header must match the URL path agent name.

```bash
curl -s -X POST http://localhost:8899/spaces/my-project/agent/Developer \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: Developer' \
  -d '{
    "status": "active",
    "summary": "Developer: implementing feature X",
    "branch": "feat/feature-x",
    "items": ["Reviewed requirements", "Starting implementation"],
    "next_steps": "Write tests"
  }'
```

Read the full blackboard:

```bash
curl -s http://localhost:8899/spaces/my-project/raw
```

---

## 5. Send Messages Between Agents

Any agent can send a message to another:

```bash
curl -s -X POST http://localhost:8899/spaces/my-project/agent/Developer/message \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: Orchestrator' \
  -d '{"message": "Please prioritize the authentication module"}'
```

The target agent reads its messages via efficient polling (no need to read `/raw`):

```bash
curl -s http://localhost:8899/spaces/my-project/agent/Developer/messages
```

Response:
```json
{
  "agent": "Developer",
  "messages": [
    {
      "id": "1772846468308665376",
      "message": "Please prioritize the authentication module",
      "sender": "Orchestrator",
      "timestamp": "2026-03-07T01:13:06Z"
    }
  ],
  "cursor": "2026-03-07T01:13:06.000000001Z"
}
```

Use the `cursor` value as `?since=` on the next request to get only new messages:

```bash
curl -s "http://localhost:8899/spaces/my-project/agent/Developer/messages?since=2026-03-07T01:13:06.000000001Z"
```

---

## 6. Register an Agent (Optional)

Registration enables heartbeat tracking and webhook message delivery. Required for non-tmux agents (scripts, CLI tools, remote processes).

```bash
curl -s -X POST http://localhost:8899/spaces/my-project/agent/Worker/register \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: Worker' \
  -d '{
    "agent_type": "script",
    "capabilities": ["data-processing", "reporting"],
    "heartbeat_interval_sec": 60,
    "callback_url": "http://worker.internal/webhook"
  }'
```

Send heartbeats to confirm liveness:

```bash
curl -s -X POST http://localhost:8899/spaces/my-project/agent/Worker/heartbeat \
  -H 'X-Agent-Name: Worker'
```

The server marks an agent `stale` if heartbeats stop within 2× the registered interval.

---

## 7. Subscribe to SSE Events

Instead of polling, subscribe to real-time push events:

```bash
# All events for a space
curl -s -N http://localhost:8899/spaces/my-project/events

# Only events for a specific agent (recommended for agents)
curl -s -N http://localhost:8899/spaces/my-project/agent/Developer/events
```

Per-agent streams receive only messages targeted at that agent, reducing noise. The stream sends a keepalive comment every 15 seconds and supports `Last-Event-ID` for reconnect:

```bash
# Reconnect and replay missed events since last-seen ID
curl -s -N http://localhost:8899/spaces/my-project/agent/Developer/events \
  -H 'Last-Event-ID: 1772846468308665376'
```

---

## 8. Ignite an Agent

The ignition endpoint bootstraps a new agent with full context — its identity, peer status, pending messages, and a POST template:

```bash
curl -s "http://localhost:8899/spaces/my-project/ignition/Developer?session_id=dev-session"
```

This is how the `/boss.ignite` skill works in Claude Code sessions.

---

## CLI Usage

The `boss` binary includes client commands:

```bash
# Post a status update
boss post --space my-project --agent Developer --status active --summary "Working on auth"

# Check agent status
boss check --space my-project --agent Developer
```

---

## Typical Agent Work Loop

```bash
# 1. Read the blackboard
curl -s http://localhost:8899/spaces/my-project/raw

# 2. Check for messages
curl -s http://localhost:8899/spaces/my-project/agent/MyAgent/messages

# 3. Do work...

# 4. Post status
curl -s -X POST http://localhost:8899/spaces/my-project/agent/MyAgent \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: MyAgent' \
  -d '{
    "status": "active",
    "summary": "MyAgent: completed task X, starting task Y",
    "items": ["Done: task X", "In progress: task Y"]
  }'

# 5. Send message if needed
curl -s -X POST http://localhost:8899/spaces/my-project/agent/OtherAgent/message \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: MyAgent' \
  -d '{"message": "Task X complete, please review PR #5"}'

# 6. Repeat
```

---

## Contracts and Archive

**Contracts** are shared truths no agent may contradict (API surfaces, naming conventions, etc.):

```bash
# Read contracts
curl -s http://localhost:8899/spaces/my-project/contracts

# Append a contract
curl -s -X POST http://localhost:8899/spaces/my-project/contracts \
  -H 'Content-Type: text/plain' \
  --data-binary 'All agents must use snake_case for API field names.'
```

**Archive** stores resolved items that no longer need active context:

```bash
curl -s -X POST http://localhost:8899/spaces/my-project/archive \
  -H 'Content-Type: text/plain' \
  --data-binary '2026-03-07: Resolved: authentication module complete (PR #5 merged)'
```
