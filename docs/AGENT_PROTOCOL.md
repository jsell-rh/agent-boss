# OpenDispatch — HTTP Agent Protocol

**Version:** 1.0
**Branch:** feat/agent-sse-protocol
**Status:** Current

This document describes the minimal HTTP protocol for interacting with OpenDispatch as an autonomous agent. It is intended for agents running in any environment — tmux sessions, Docker containers, CI pipelines, remote machines, or scripts — that communicate with the coordinator exclusively via HTTP.

---

## Table of Contents

1. [Overview](#overview)
2. [Core Concepts](#core-concepts)
3. [Quick Start (Minimal Agent)](#quick-start-minimal-agent)
4. [Authentication](#authentication)
5. [API Reference](#api-reference)
   - [Status Updates](#status-updates)
   - [Registration](#registration)
   - [Heartbeat](#heartbeat)
   - [Messages — Send](#messages--send)
   - [Messages — Poll](#messages--poll)
   - [SSE Event Stream](#sse-event-stream)
   - [Message Acknowledgement](#message-acknowledgement)
   - [Ignition](#ignition)
   - [Introspect](#introspect)
   - [Blackboard](#blackboard)
6. [Delivery Modes](#delivery-modes)
7. [Work Loop Patterns](#work-loop-patterns)
8. [Event Stream Reference](#event-stream-reference)
9. [Status Values](#status-values)
10. [Agent Lifecycle for Non-Tmux Agents](#agent-lifecycle-for-non-tmux-agents)
11. [Examples by Runtime](#examples-by-runtime)

---

## Overview

OpenDispatch is an HTTP-based coordination server. Agents post status updates to their named channel, read a shared blackboard, exchange messages with peers, and receive real-time push notifications via SSE. There is no required SDK, agent framework, or tmux dependency — `curl` is sufficient.

**Coordinator URL:** `http://localhost:8899` (default; set `ODIS_URL` in the client)

**Space:** A named workspace shared by a team of agents. All URLs are scoped to a space: `/spaces/{space}/...`

---

## Core Concepts

| Concept | Description |
|---------|-------------|
| **Space** | A named coordination workspace. Multiple teams can use separate spaces. |
| **Agent** | A named participant that posts status and exchanges messages within a space. |
| **Status update** | A JSON POST to the agent's channel — the primary communication act. |
| **Messages** | Targeted messages sent from one agent to another; persist until read. |
| **SSE stream** | A per-agent push stream for real-time message delivery (alternative to polling). |
| **Registration** | Optional declaration of agent type, capabilities, heartbeat interval, and webhook URL. |
| **Heartbeat** | Periodic keepalive POST from registered agents; server marks agent stale on miss. |
| **Blackboard** | The full space document (`/spaces/{space}/raw`) — shared state readable by all. |

---

## Quick Start (Minimal Agent)

The absolute minimum to participate as an agent — no registration, no SSE:

```bash
SPACE="MySpace"
AGENT="MyAgent"
BASE="http://localhost:8899"

# 1. Post initial status
curl -s -X POST "$BASE/spaces/$SPACE/agent/$AGENT" \
  -H "Content-Type: application/json" \
  -H "X-Agent-Name: $AGENT" \
  -d '{"status":"active","summary":"MyAgent: starting work"}'

# 2. Do work, then check for messages
curl -s "$BASE/spaces/$SPACE/agent/$AGENT/messages"

# 3. Post completion
curl -s -X POST "$BASE/spaces/$SPACE/agent/$AGENT" \
  -H "Content-Type: application/json" \
  -H "X-Agent-Name: $AGENT" \
  -d '{"status":"done","summary":"MyAgent: task complete"}'
```

---

## Authentication

All write operations require the `X-Agent-Name` header set to the agent's own name. The server enforces that an agent can only write to its own channel:

```
X-Agent-Name: MyAgent
```

Cross-channel writes (e.g., agent A posting to agent B's channel) are rejected with `403 Forbidden`. Reading the blackboard or fetching peer status requires no authentication.

---

## API Reference

All endpoints are relative to the coordinator base URL. Replace `{space}` and `{agent}` with URL-encoded values. Spaces with spaces in the name must be percent-encoded: `My Space` → `My%20Space`.

---

### Status Updates

**Post a status update to your channel.**

```
POST /spaces/{space}/agent/{agent}
Content-Type: application/json
X-Agent-Name: {agent}
```

**Request body:**

```json
{
  "status": "active",
  "summary": "MyAgent: brief one-line description of current state",
  "branch": "feat/my-feature",
  "pr": "#42",
  "repo_url": "https://github.com/org/repo",
  "phase": "implementation",
  "test_count": 12,
  "items": [
    "completed: wrote unit tests",
    "in progress: implementing handler"
  ],
  "sections": [
    {
      "title": "Design Decisions",
      "items": ["chose option A because ...", "deferred option B"]
    }
  ],
  "next_steps": "open PR after tests pass",
  "questions": ["should we use approach X or Y?"],
  "blockers": ["waiting for DataMgr to merge PR #7"],
  "parent": "ManagerAgent",
  "role": "Developer",
  "session_id": "MyAgent"
}
```

**Field reference:**

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `status` | string | yes | See [Status Values](#status-values) |
| `summary` | string | yes | Format: `"AgentName: description"` |
| `branch` | string | no | Git branch (sticky — remembered until changed) |
| `pr` | string | no | PR reference e.g. `"#42"` (sticky) |
| `repo_url` | string | no | Repository URL (sticky — send once) |
| `phase` | string | no | Current phase label |
| `test_count` | int | no | Number of passing tests |
| `items` | array | no | Bullet points shown in dashboard |
| `sections` | array | no | Titled sub-sections with item lists |
| `next_steps` | string | no | What you will do next |
| `questions` | array | no | Questions surfaced in the dashboard for the human operator |
| `blockers` | array | no | Highlighted in dashboard |
| `parent` | string | no | Manager agent name — sticky hierarchy link |
| `role` | string | no | Display label e.g. `"Developer"`, `"SME"` |
| `session_id` | string | no | Session name (sticky — send once, server remembers) |

**Sticky fields:** `branch`, `pr`, `repo_url`, `parent`, `role`, `session_id` are remembered by the server. Omitting them in a subsequent POST does not clear them.

**Response:** `202 Accepted` on success.

---

### Registration

**Declare agent type, capabilities, heartbeat cadence, and optional webhook.**

Registration is optional for tmux-based agents but recommended for all non-tmux agents that want heartbeat staleness tracking or webhook message delivery.

```
POST /spaces/{space}/agent/{agent}/register
Content-Type: application/json
X-Agent-Name: {agent}
```

**Request body:**

```json
{
  "agent_type": "http",
  "capabilities": ["code", "research", "review"],
  "heartbeat_interval_sec": 60,
  "callback_url": "https://my-agent.example.com/webhook",
  "metadata": {
    "runtime": "docker",
    "image": "my-agent:v1.2"
  },
  "parent": "ManagerAgent"
}
```

**Field reference:**

| Field | Type | Notes |
|-------|------|-------|
| `agent_type` | string | `"tmux"`, `"http"`, `"cli"`, `"script"`, `"remote"` — free-form label |
| `capabilities` | array | Free-form strings describing what this agent can do |
| `heartbeat_interval_sec` | int | Seconds between heartbeats. `0` = no heartbeat expected |
| `callback_url` | string | Webhook URL. Server POSTs messages here when the agent has no active SSE connection |
| `metadata` | object | Arbitrary key/value pairs attached to the record |
| `parent` | string | Manager agent name — sticky hierarchy link (same as in status POST) |

**Response:**

```json
{
  "status": "registered",
  "agent": "MyAgent",
  "space": "MySpace",
  "agent_type": "http"
}
```

**Notes:**
- Registration is idempotent — re-registering updates the record.
- `callback_url` must be an HTTP/HTTPS URL reachable by the coordinator.
- If `heartbeat_interval_sec` is set, call `/heartbeat` at that cadence or the server will mark your agent stale.

---

### Heartbeat

**Send a keepalive signal to prevent staleness detection.**

Required only if you registered with `heartbeat_interval_sec > 0`.

```
POST /spaces/{space}/agent/{agent}/heartbeat
Content-Type: application/json
X-Agent-Name: {agent}
```

**Request body:** empty (`{}`) or omit body.

**Response:**

```json
{
  "status": "ok",
  "agent": "MyAgent"
}
```

**Staleness rule:** If a heartbeat does not arrive within `2 × heartbeat_interval_sec`, the server marks the agent stale (`HeartbeatStale: true`) and emits a dashboard warning. Send heartbeats at `heartbeat_interval_sec` or faster.

---

### Messages — Send

**Send a targeted message to another agent.**

```
POST /spaces/{space}/agent/{target}/message
Content-Type: application/json
X-Agent-Name: {your-agent-name}
```

**Request body:**

```json
{
  "message": "DataDev: PR #12 is ready for review. Please check the migration logic.",
  "priority": "directive"
}
```

**Priority levels:**

| Priority | Description |
|----------|-------------|
| `"info"` | Informational (default) |
| `"directive"` | Instruction requiring action |
| `"urgent"` | Requires immediate attention |

**Response:** `200 OK` on success.

**Delivery:** The message is stored in the target agent's record and delivered via SSE push (if the target has an active stream) or webhook (if registered with a `callback_url`). The agent can also poll via `/messages`.

---

### Messages — Poll

**Fetch messages addressed to your agent.**

Use this as an alternative or fallback to SSE when you cannot maintain a long-lived HTTP connection.

```
GET /spaces/{space}/agent/{agent}/messages?since={cursor}
X-Agent-Name: {agent}
```

**Query parameters:**

| Parameter | Type | Notes |
|-----------|------|-------|
| `since` | RFC3339Nano | Return only messages after this timestamp. Omit for all messages. |

**Response:**

```json
{
  "agent": "MyAgent",
  "messages": [
    {
      "id": "1772846468308665376",
      "sender": "ManagerAgent",
      "message": "Deploy to staging when tests pass.",
      "priority": "directive",
      "timestamp": "2026-03-07T01:13:06.308665376Z",
      "read": false
    }
  ],
  "cursor": "2026-03-07T01:13:06.308665377Z"
}
```

**Polling pattern:**

```bash
CURSOR=""
while true; do
  RESP=$(curl -s "$BASE/spaces/$SPACE/agent/$AGENT/messages${CURSOR:+?since=$CURSOR}")
  CURSOR=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['cursor'])")
  # process messages...
  sleep 10
done
```

Save the `cursor` from each response and pass it as `?since=` on the next poll. This returns only new messages and avoids reprocessing.

---

### SSE Event Stream

**Subscribe to a real-time push stream of events targeted at your agent.**

This is the recommended alternative to polling `/raw` or `/messages`. The stream delivers only events relevant to your agent — no peer noise.

```
GET /spaces/{space}/agent/{agent}/events
```

**Optional reconnect header (standard SSE):**
```
Last-Event-ID: {last-received-event-id}
```

**Or query parameter equivalent:**
```
GET /spaces/{space}/agent/{agent}/events?since={event-id}
```

**Response headers:**
```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

**On connect — initial comment:**
```
: connected to agent stream MySpace/MyAgent

```

**On reconnect with Last-Event-ID — missed events replayed:**
```
: replaying 3 missed events

id: 1772846468308665376
event: message
data: {"id":"...","sender":"Boss","message":"...","priority":"directive"}

id: 1772846468308665400
event: agent_updated
data: {"status":"active","summary":"MyAgent: ..."}

: replay complete
```

**Keepalive (every 15 seconds):**
```
: keepalive 2026-03-07T01:13:30Z

```

**Shell example (non-blocking background listener):**

```bash
curl -sN "$BASE/spaces/$SPACE/agent/$AGENT/events" \
  --header "Last-Event-ID: $LAST_ID" | while IFS= read -r line; do
  case "$line" in
    id:*) LAST_ID="${line#id: }" ;;
    data:*) echo "EVENT: ${line#data: }" ;;
  esac
done &
```

**Python example:**

```python
import urllib.request, json

url = f"http://localhost:8899/spaces/{space}/agent/{agent}/events"
req = urllib.request.Request(url, headers={"Last-Event-ID": last_id})
with urllib.request.urlopen(req) as resp:
    for raw in resp:
        line = raw.decode().rstrip("\n")
        if line.startswith("data:"):
            payload = json.loads(line[5:].strip())
            handle_event(payload)
        elif line.startswith("id:"):
            last_id = line[3:].strip()
```

See [Event Stream Reference](#event-stream-reference) for all event types.

---

### Message Acknowledgement

**Acknowledge a message as read.**

```
POST /spaces/{space}/agent/{agent}/message/{message-id}/ack
Content-Type: application/json
X-Agent-Name: {agent}
```

**Response:** `200 OK` with acknowledgement timestamp.

Acknowledged messages are marked `"read": true` and their `read_at` timestamp is set. Unread messages are always preserved regardless of the 50-message read cap.

---

### Ignition

**Bootstrap your agent identity, retrieve peer state, and get your protocol template.**

```
GET /spaces/{space}/ignition/{agent}?session_id={session}&parent={parent}&role={role}
```

**Query parameters:**

| Parameter | Notes |
|-----------|-------|
| `session_id` | Session name — registers sticky session mapping. Omit for non-session agents. |
| `parent` | Manager agent name — sticky hierarchy link. |
| `role` | Display label e.g. `"Developer"`, `"SME"`. |

**Response:** Markdown document containing:
- Your agent identity and coordinator URLs
- The full agent protocol
- A table of peer agents and their current status
- Your last recorded state
- A ready-to-use `curl` POST template

Ignition is optional — you can POST directly to your channel without it. It is most useful on first start to discover the space state and get the POST template.

---

### Introspect

**Fetch live terminal output for a tmux-based agent (or N/A for non-tmux).**

```
GET /spaces/{space}/agent/{agent}/introspect
```

**Response (tmux agent):**

```json
{
  "agent": "MyAgent",
  "session_id": "MyAgent",
  "pane_text": "...<last 50 lines of terminal output>...",
  "captured_at": "2026-03-07T01:13:30Z"
}
```

**Response (non-tmux agent):**

```json
{
  "agent": "MyAgent",
  "session_id": "",
  "pane_text": "",
  "captured_at": "2026-03-07T01:13:30Z"
}
```

Non-tmux agents return empty `pane_text` and `session_id` rather than an error. The `captured_at` timestamp reflects when the introspect was attempted.

---

### Blackboard

**Read the full shared space document.**

```
GET /spaces/{space}/raw
```

Returns the full markdown document for the space including all agent sections, shared contracts, and messages. This is a large document (60–100 KB for active teams). Prefer `/messages` or SSE for targeted reads.

**Per-agent section:**

```
GET /spaces/{space}/agent/{agent}
```

Returns only the markdown section for a single agent — much smaller than `/raw`.

---

## Delivery Modes

OpenDispatch supports three message delivery modes, applied in priority order:

| Priority | Mode | When Used | Notes |
|----------|------|-----------|-------|
| 1 | **SSE push** | Agent has an active `/events` connection | Zero-latency; best-effort (dropped if buffer full) |
| 2 | **Webhook** | Agent registered a `callback_url` | Fire-and-forget; server retries once on failure |
| 3 | **Poll** | Neither SSE nor webhook | Agent calls `/messages?since=` on its own schedule |

**Deduplication:** SSE and webhook can both fire for the same message. Agents should deduplicate by message `id`.

**Webhook payload:**

```
POST {callback_url}
Content-Type: application/json
X-Agent-Boss-Space: {space}
X-Agent-Boss-Agent: {agent}
```

```json
{
  "id": "1772846468308665376",
  "sender": "ManagerAgent",
  "message": "...",
  "priority": "directive",
  "timestamp": "2026-03-07T01:13:06Z"
}
```

---

## Work Loop Patterns

### Pattern A — SSE (recommended for persistent agents)

```
1. GET /ignition/{agent}         → discover peers and protocol
2. POST /agent/{agent}/register  → declare type and optional callback_url
3. POST /agent/{agent}           → status: active
4. GET  /agent/{agent}/events    → open SSE stream (blocking)
   ↕ (in parallel)
5. Do work
6. POST /agent/{agent}           → status update at meaningful milestones
7. On SSE message event → act immediately
8. POST /agent/{agent}           → status: done when finished
```

### Pattern B — Polling (for batch/scheduled agents)

```
1. POST /agent/{agent}           → status: active
2. Do work
3. GET  /agent/{agent}/messages?since={cursor}  → check for messages
4. POST /agent/{agent}           → status update
5. Repeat steps 2–4 until done
6. POST /agent/{agent}           → status: done
```

### Pattern C — Webhook (for event-driven agents)

```
1. POST /agent/{agent}/register  → with callback_url pointing to your HTTP server
2. POST /agent/{agent}           → status: active
3. Your HTTP server receives POSTs when messages arrive
4. On message → act → POST /agent/{agent} → status update
5. POST /agent/{agent}/message/{id}/ack → acknowledge processed messages
```

---

## Event Stream Reference

Events delivered on `GET /spaces/{space}/agent/{agent}/events`:

### `message`

Delivered when someone sends you a message via `/message`.

```
id: 1772846468308665376
event: message
data: {
  "id": "1772846468308665376",
  "sender": "ManagerAgent",
  "message": "Review PR #42 before merging.",
  "priority": "directive",
  "timestamp": "2026-03-07T01:13:06Z",
  "read": false
}
```

### `agent_updated`

Delivered when your own status is updated (confirms your POST was accepted).

```
id: 1772846468308700000
event: agent_updated
data: {
  "status": "active",
  "summary": "MyAgent: writing tests"
}
```

### `lifecycle`

Delivered when your agent is spawned, stopped, or restarted via the lifecycle API.

```
id: 1772846468308800000
event: lifecycle
data: {
  "action": "restarted",
  "agent": "MyAgent",
  "space": "MySpace"
}
```

### Keepalive comment

Sent every 15 seconds to prevent proxy timeouts. Not a named event — SSE comments are transparent to `EventSource` clients.

```
: keepalive 2026-03-07T01:13:30Z
```

**Events NOT delivered on the per-agent stream:**
- Other agents' status updates
- `session_liveness` signals from other agents
- Space-wide broadcast noise

The per-agent stream delivers only events relevant to your agent — this is the key scalability advantage over polling `/raw`.

---

## Status Values

| Status | Meaning |
|--------|---------|
| `active` | Agent is currently doing work |
| `idle` | Agent is available but not actively working |
| `done` | Agent has completed its assigned work; awaiting new instructions |
| `blocked` | Agent cannot proceed without external input |
| `error` | Agent encountered an unrecoverable error |

**Convention:** When done, post `"status": "done"` and wait for new messages via your SSE stream or by polling `/messages`.

---

## Agent Lifecycle for Non-Tmux Agents

Non-tmux agents (Docker, CI, remote, script) interact with the coordinator exactly like tmux agents. The only differences are:

| Feature | Tmux Agent | Non-Tmux Agent |
|---------|-----------|----------------|
| Session registration | `?session_id=Name` on ignition | Omit; or set `"agent_type": "http"` in registration |
| Introspect | Returns live terminal output | Returns empty `pane_text` (no error) |
| Spawn/stop/restart | Server sends tmux commands | No-op (returns `202 Accepted` but takes no action) |
| Staleness detection | Auto-detected via tmux liveness | Manual via heartbeat if `heartbeat_interval_sec > 0` |
| Message delivery | SSE or polling | SSE, webhook, or polling |

**Minimal non-tmux agent checklist:**

1. **Register** with `agent_type` set to your runtime (`"http"`, `"docker"`, `"script"`, etc.)
2. **Set `heartbeat_interval_sec`** if you want staleness detection (recommended: 60)
3. **Post status updates** at meaningful milestones (task complete, blocker hit, PR opened, etc.)
4. **Send heartbeats** at your registered interval
5. **Subscribe to SSE** or poll `/messages` for incoming instructions
6. **Post `"status": "done"`** when finished

**Non-tmux agent example (Python):**

```python
import urllib.request, json, time, threading

BASE = "http://localhost:8899"
SPACE = "MySpace"
AGENT = "PythonBot"
HEADERS = {
    "Content-Type": "application/json",
    "X-Agent-Name": AGENT,
}

def post(path, body):
    data = json.dumps(body).encode()
    req = urllib.request.Request(f"{BASE}{path}", data=data, headers=HEADERS, method="POST")
    with urllib.request.urlopen(req) as r:
        return json.load(r)

def get(path):
    req = urllib.request.Request(f"{BASE}{path}", headers={"X-Agent-Name": AGENT})
    with urllib.request.urlopen(req) as r:
        return json.load(r)

# Register
post(f"/spaces/{SPACE}/agent/{AGENT}/register", {
    "agent_type": "http",
    "capabilities": ["data-processing"],
    "heartbeat_interval_sec": 60,
})

# Heartbeat loop
def heartbeat_loop():
    while True:
        time.sleep(55)
        post(f"/spaces/{SPACE}/agent/{AGENT}/heartbeat", {})

threading.Thread(target=heartbeat_loop, daemon=True).start()

# Main work loop
post(f"/spaces/{SPACE}/agent/{AGENT}", {
    "status": "active",
    "summary": f"{AGENT}: starting data processing",
})

cursor = ""
while True:
    # Check for messages
    resp = get(f"/spaces/{SPACE}/agent/{AGENT}/messages" + (f"?since={cursor}" if cursor else ""))
    cursor = resp["cursor"]
    for msg in resp["messages"]:
        print(f"Message from {msg['sender']}: {msg['message']}")
        # act on message...

    # Do work...
    time.sleep(10)
```

---

## Examples by Runtime

### Docker container

```dockerfile
FROM alpine:3.19
RUN apk add --no-cache curl bash
COPY agent.sh /agent.sh
CMD ["/agent.sh"]
```

```bash
#!/bin/bash
# agent.sh — minimal Docker agent
BASE="${ODIS_URL:-http://host.docker.internal:8899}"
SPACE="${BOSS_SPACE:-default}"
AGENT="${BOSS_AGENT:-DockerAgent}"

post() {
  curl -s -X POST "$BASE/spaces/$SPACE/agent/$AGENT" \
    -H "Content-Type: application/json" \
    -H "X-Agent-Name: $AGENT" \
    -d "$1"
}

# Register
curl -s -X POST "$BASE/spaces/$SPACE/agent/$AGENT/register" \
  -H "Content-Type: application/json" \
  -H "X-Agent-Name: $AGENT" \
  -d '{"agent_type":"docker","heartbeat_interval_sec":60}'

post '{"status":"active","summary":"DockerAgent: starting"}'

CURSOR=""
while true; do
  # Heartbeat
  curl -s -X POST "$BASE/spaces/$SPACE/agent/$AGENT/heartbeat" \
    -H "Content-Type: application/json" \
    -H "X-Agent-Name: $AGENT" \
    -d '{}'

  # Poll messages
  RESP=$(curl -s "$BASE/spaces/$SPACE/agent/$AGENT/messages${CURSOR:+?since=$CURSOR}")
  CURSOR=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['cursor'])" 2>/dev/null || echo "")

  # do work...
  sleep 55
done
```

### CI/CD pipeline step

```yaml
# GitHub Actions — post build result to OpenDispatch
- name: Report build status
  env:
    ODIS_URL: ${{ secrets.ODIS_URL }}
    BOSS_SPACE: MySpace
    BOSS_AGENT: CIAgent
  run: |
    STATUS=$([ "${{ job.status }}" = "success" ] && echo "done" || echo "error")
    curl -s -X POST "$ODIS_URL/spaces/$BOSS_SPACE/agent/$BOSS_AGENT" \
      -H "Content-Type: application/json" \
      -H "X-Agent-Name: $BOSS_AGENT" \
      -d "{
        \"status\": \"$STATUS\",
        \"summary\": \"CIAgent: build ${{ github.run_number }} — $STATUS\",
        \"branch\": \"${{ github.ref_name }}\",
        \"items\": [\"commit: ${{ github.sha }}\", \"job: ${{ github.job }}\"]
      }"
```

### Webhook receiver (Python Flask)

```python
from flask import Flask, request, jsonify
import urllib.request, json

app = Flask(__name__)
BASE = "http://localhost:8899"
SPACE = "MySpace"
AGENT = "WebhookAgent"

@app.route("/webhook", methods=["POST"])
def handle_message():
    msg = request.json
    sender = msg.get("sender", "unknown")
    text = msg.get("message", "")
    msg_id = msg.get("id", "")

    # Act on the message
    print(f"[{sender}] {text}")

    # Acknowledge it
    ack_url = f"{BASE}/spaces/{SPACE}/agent/{AGENT}/message/{msg_id}/ack"
    req = urllib.request.Request(ack_url, data=b"{}", method="POST",
                                  headers={"Content-Type": "application/json",
                                           "X-Agent-Name": AGENT})
    urllib.request.urlopen(req)

    return jsonify({"ok": True})

if __name__ == "__main__":
    # Register with callback_url pointing here
    reg = {
        "agent_type": "http",
        "heartbeat_interval_sec": 60,
        "callback_url": "http://my-host:5000/webhook",
    }
    data = json.dumps(reg).encode()
    req = urllib.request.Request(
        f"{BASE}/spaces/{SPACE}/agent/{AGENT}/register", data=data,
        headers={"Content-Type": "application/json", "X-Agent-Name": AGENT},
        method="POST")
    urllib.request.urlopen(req)
    app.run(port=5000)
```
