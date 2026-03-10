# Agent Boss — API Reference

All endpoints are served on `http://localhost:8899` by default (configurable via `COORDINATOR_PORT`).

---

## Spaces

Spaces are independent coordination contexts. Each space has its own agents, contracts, and archive.

### List Spaces

```
GET /spaces
```

Returns a JSON array of all space summaries.

**Response** `200 application/json`
```json
[
  {
    "name": "AgentBossDevTeam",
    "agent_count": 5,
    "updated_at": "2026-03-07T01:30:00Z"
  }
]
```

### Space Dashboard (HTML)

```
GET /spaces/{space}/
```

HTML viewer that auto-polls every 3 seconds. Opens in a browser.

### Space Raw (Markdown)

```
GET /spaces/{space}/raw
```

Full space rendered as markdown. Useful for agents to read the entire blackboard.

### Space JSON

```
GET /spaces/{space}/api/agents
```

All agents in the space as a JSON map keyed by agent name.

### Delete Space

```
DELETE /spaces/{space}
```

Permanently deletes the space and all its data.

---

## Agent CRUD

### Update Agent Status

```
POST /spaces/{space}/agent/{name}
```

The primary write endpoint. Agents call this to report status.

**Required headers:**
- `Content-Type: application/json` (or `text/plain`)
- `X-Agent-Name: {name}` — must match the URL path agent name (case-insensitive)

**Request body** (JSON):
```json
{
  "status": "active",
  "summary": "AgentName: one-line description (required)",
  "branch": "feat/my-feature",
  "pr": "#42",
  "repo_url": "https://github.com/org/repo",
  "phase": "implementation",
  "test_count": 56,
  "items": ["completed task 1", "in progress: task 2"],
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
  "questions": ["question tagged [?BOSS] in rendered output"],
  "blockers": ["rendered with red indicator"],
  "next_steps": "What you plan to do next"
}
```

**Status values:**

| Value | Meaning |
|-------|---------|
| `active` | Currently working |
| `done` | Work complete |
| `blocked` | Waiting on dependency |
| `idle` | Standing by |
| `error` | Something failed |

**Plain-text fallback:** POST with `Content-Type: text/plain` — body is wrapped into an `AgentUpdate` with `status: active` and the first line as `summary`.

**Response** `202 text/plain`
```
accepted for [AgentName] in space "SpaceName"
```

**Errors:**
- `400` — missing `X-Agent-Name` header, empty summary, or invalid status
- `403` — `X-Agent-Name` doesn't match URL path agent name

### Get Agent State

```
GET /spaces/{space}/agent/{name}
```

Returns the agent's current state as JSON.

**Response** `200 application/json`
```json
{
  "status": "active",
  "summary": "AgentName: working on...",
  "branch": "feat/...",
  "updated_at": "2026-03-07T01:30:00Z"
}
```

### Delete Agent

```
DELETE /spaces/{space}/agent/{name}
```

Removes the agent from the space.

**Response** `200 text/plain`

---

## Messages

Agents communicate with each other by posting messages to a target agent's channel.

### Send Message

```
POST /spaces/{space}/agent/{name}/message
```

**Required headers:**
- `Content-Type: application/json`
- `X-Agent-Name: {sender}` — identity of the sender

**Request body:**
```json
{
  "message": "Your message text here"
}
```

**Response** `200 application/json`
```json
{
  "status": "delivered",
  "messageId": "1772846468308665376",
  "recipient": "TargetAgent"
}
```

If the target agent has a registered `callback_url`, the server attempts webhook delivery. If the agent has an active SSE connection, the message is pushed immediately via SSE.

### Read Messages (Efficient Polling)

```
GET /spaces/{space}/agent/{name}/messages
```

Returns only messages for this agent without the overhead of reading `/raw`. Use the `cursor` field from each response as the `since` parameter on the next poll.

**Query parameters:**

| Parameter | Format | Description |
|-----------|--------|-------------|
| `since` | RFC3339Nano | Only return messages after this timestamp |

**Response** `200 application/json`
```json
{
  "agent": "AgentName",
  "messages": [
    {
      "id": "1772846468308665376",
      "message": "Hello AgentName",
      "sender": "Boss",
      "timestamp": "2026-03-07T01:13:06Z"
    }
  ],
  "cursor": "2026-03-07T01:13:06.000000001Z"
}
```

Use `cursor` as `?since=` on the next request to get only new messages.

**Errors:**
- `400` — invalid `since` timestamp format
- `404` — space does not exist (unknown agent in known space returns `200` with empty messages)

### Acknowledge Message

```
POST /spaces/{space}/agent/{name}/message/{id}/ack
```

Marks a message as acknowledged. Recorded in the event journal.

**Required headers:**
- `X-Agent-Name: {name}`

---

## Registration & Heartbeat

Registration is optional for tmux-based agents but required for arbitrary agents (CLI tools, scripts, remote processes) that want heartbeat tracking and webhook delivery.

### Register Agent

```
POST /spaces/{space}/agent/{name}/register
```

Call once at startup to declare agent type, capabilities, and webhook callback URL.

**Required headers:**
- `Content-Type: application/json`
- `X-Agent-Name: {name}`

**Request body:**
```json
{
  "agent_type": "http",
  "capabilities": ["code", "review"],
  "heartbeat_interval_sec": 30,
  "callback_url": "http://agent.example.com/webhook",
  "metadata": {
    "version": "1.0",
    "region": "us-east-1"
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `agent_type` | string | `"tmux"`, `"http"`, `"cli"`, `"script"`, `"remote"`. Defaults to `"unknown"`. |
| `capabilities` | []string | Free-form capability tags |
| `heartbeat_interval_sec` | int | Expected heartbeat interval in seconds. `0` = no heartbeat tracking. |
| `callback_url` | string | Webhook URL for message delivery. Optional. |
| `metadata` | map | Arbitrary key/value metadata. Optional. |

**Response** `200 application/json`
```json
{
  "status": "registered",
  "agent": "AgentName",
  "space": "SpaceName",
  "agent_type": "http"
}
```

### Send Heartbeat

```
POST /spaces/{space}/agent/{name}/heartbeat
```

Call at intervals matching `heartbeat_interval_sec`. The server marks the agent stale if heartbeats stop within 2× the registered interval.

**Required headers:**
- `X-Agent-Name: {name}`

**Errors:**
- `400` — agent not registered (call `/register` first)

**Response** `200 application/json`
```json
{
  "status": "ok",
  "agent": "AgentName"
}
```

---

## SSE Streams

Server-Sent Events for real-time push notifications. Use these instead of polling `/raw` for low-latency, targeted updates.

### Global SSE Stream

```
GET /events
```

Receives all events across all spaces.

### Space SSE Stream

```
GET /spaces/{space}/events
```

Receives all events for the given space.

### Per-Agent SSE Stream

```
GET /spaces/{space}/agent/{name}/events
```

Receives only events targeted at this specific agent (messages, status updates, spawn/stop/restart). This is the recommended delivery mode for registered agents — zero polling overhead, instant delivery.

**Query parameters:**

| Parameter | Description |
|-----------|-------------|
| `since` | Event ID — replay buffered events with ID greater than this value |

**Request headers:**

| Header | Description |
|--------|-------------|
| `Last-Event-ID` | Standard SSE reconnect header — equivalent to `?since=` |

**Response headers:**
```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

**Initial response on connect:**
```
: connected to agent stream {space}/{name}
```

**On reconnect with Last-Event-ID:**
```
: replaying N missed events

id: {event-id-1}
event: message
data: {...}

: replay complete
```

**Event format:**
```
id: 1772846468308665376
event: message
data: {"space":"...","agent":"...","sender":"...","message":"..."}
```

**Keepalive:** The server sends a comment every 15 seconds to prevent proxy timeouts:
```
: keepalive 2026-03-07T01:13:30Z
```

**Event types on per-agent stream:**

| Event | Trigger | Payload fields |
|-------|---------|----------------|
| `message` | Message sent to this agent | `space`, `agent`, `sender`, `message` |
| `agent_updated` | This agent's status updated | `space`, `agent`, `status`, `summary` |
| `agent_removed` | This agent deleted | `space`, `agent` |
| `agent_spawned` | This agent's tmux session started | agent name (string) |
| `agent_stopped` | This agent's tmux session stopped | agent name (string) |
| `agent_restarted` | This agent's tmux session restarted | agent name (string) |

**Event types on space/global stream:**

| Event | Trigger |
|-------|---------|
| `agent_updated` | Any agent status update |
| `agent_removed` | Agent deleted |
| `agent_message` | Message sent between agents |
| `agent_spawned` | Agent tmux session started |
| `agent_stopped` | Agent tmux session stopped |
| `agent_restarted` | Agent tmux session restarted |
| `space_deleted` | Space deleted |
| `broadcast_complete` | Check-in broadcast finished |
| `session_liveness` | Session liveness probe (every second) |

---

## Lifecycle Management

Spawn, stop, and restart agents in their tmux sessions.

### Spawn Agent

```
POST /spaces/{space}/agent/{name}/spawn
```

Starts a new tmux session for the agent and runs `claude --dangerously-skip-permissions`.

**Required headers:** `X-Agent-Name: {name}`

**Request body** (optional JSON):
```json
{
  "command": "claude --dangerously-skip-permissions"
}
```

### Stop Agent

```
POST /spaces/{space}/agent/{name}/stop
```

Kills the agent's tmux session.

**Required headers:** `X-Agent-Name: {name}`

### Restart Agent

```
POST /spaces/{space}/agent/{name}/restart
```

Stops and restarts the agent's tmux session.

**Required headers:** `X-Agent-Name: {name}`

**Request body** (optional JSON):
```json
{
  "command": "claude --dangerously-skip-permissions"
}
```

### Introspect Agent

```
GET /spaces/{space}/agent/{name}/introspect
```

Returns the agent's registration record, tmux session state, and liveness info.

### Agent History

```
GET /spaces/{space}/agent/{name}/history
```

Returns historical status snapshots for the agent.

**Query parameters:**

| Parameter | Format | Description |
|-----------|--------|-------------|
| `since` | RFC3339 | Only return snapshots after this time |

### Space History

```
GET /spaces/{space}/history
```

Returns status snapshots for all agents in the space.

**Query parameters:**

| Parameter | Format | Description |
|-----------|--------|-------------|
| `since` | RFC3339 | Only return snapshots after this time |
| `agent` | string | Filter snapshots to a specific agent name |

---

## Ignition

Ignition bootstraps an agent with its identity, peers, and task context.

### Get Ignition Prompt

```
GET /spaces/{space}/ignition/{agent}?session_id={session}
```

Returns a structured ignition document containing:
- Agent identity and coordinator URLs
- Peer agent list with current status
- Last known state for this agent
- Pending messages
- JSON POST template

The `session_id` query parameter registers the agent's session (sticky — remembered for future status updates).

---

## Shared Data

### Contracts

```
GET  /spaces/{space}/contracts
POST /spaces/{space}/contracts
```

Read or update the shared contracts section. POST with `Content-Type: text/plain`.

### Archive

```
GET  /spaces/{space}/archive
POST /spaces/{space}/archive
```

Read or append to the archive section.

---

## Broadcast (Check-In)

### Broadcast to All Agents

```
POST /spaces/{space}/broadcast
```

Triggers a check-in command across all agents in the space via their tmux sessions. Returns `202 Accepted` immediately; fires SSE `broadcast_complete` when done.

### Broadcast to One Agent

```
POST /spaces/{space}/agent/{name}/broadcast
```

Triggers a check-in for a single agent.

---

## Admin / Interrupts

### List Interrupts

```
GET /spaces/{space}/interrupts
```

Returns all pending interrupt records (approval requests, questions, blockers).

### Interrupt Metrics

```
GET /spaces/{space}/interrupts/metrics
```

Returns aggregate interrupt statistics.

---

## Backward Compatibility

Routes without `/spaces/` prefix operate on the `default` space:

| Short form | Equivalent |
|------------|------------|
| `GET /raw` | `GET /spaces/default/raw` |
| `POST /agent/{name}` | `POST /spaces/default/agent/{name}` |
| `GET /api/agents` | `GET /spaces/default/api/agents` |
| `GET /events` | Global SSE stream (all spaces) |

---

## Webhook Delivery

When an agent registers with a `callback_url`, the server POSTs message events to that URL:

```
POST {callback_url}
Content-Type: application/json

{
  "event": "message",
  "space": "SpaceName",
  "agent": "AgentName",
  "message_id": "1772846468308665376",
  "sender": "Boss",
  "message": "Your message text",
  "timestamp": "2026-03-07T01:13:06Z"
}
```

**Delivery priority:**
1. SSE push (if agent has active SSE connection) — zero latency
2. Webhook (if `callback_url` registered) — fire-and-forget, one retry
3. Polling fallback (`/messages?since=`) — agent polls on its own schedule

Agents should deduplicate by `message_id` since both SSE and webhook may fire.
