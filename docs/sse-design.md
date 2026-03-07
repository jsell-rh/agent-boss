# Per-Agent SSE Stream — Design Specification

**Author:** ProtocolSME
**Branch:** feat/agent-protocol
**Status:** Draft — for ProtocolDev implementation review

---

## 1. Problem Statement

Current agent communication has three delivery modes, all with gaps:

| Mode | Mechanism | Problem |
|------|-----------|---------|
| Polling | `GET /spaces/{space}/raw` | Pollutes agent context; 36KB+ response; misses messages in noise |
| Webhook | `callback_url` in registration | Agent must run HTTP server; no fallback on failure |
| SSE (existing) | `GET /spaces/{space}/agent/{name}/events` | No `Last-Event-ID`; no keepalive; fragile agent name filtering; no message backlog on reconnect |

With 30+ agents, polling `/raw` every few seconds causes:
- Context window pollution (agents read irrelevant peer status)
- Message miss rate (new messages buried in noise)
- Server load (full space serialization per poll)

**Goal:** Make the per-agent SSE stream production-ready so agents can subscribe once and receive targeted, reliable push notifications.

---

## 2. Current State Analysis

### What exists (`handleAgentSSE` in `protocol.go:396`)

```
GET /spaces/{space}/agent/{name}/events
```

- Opens SSE stream filtered to one agent
- Sends initial comment `: connected to agent stream {space}/{name}`
- Agent filtering: `strings.Contains(strings.ToLower(data), strings.ToLower(agent))` — **fragile**: "Dev" matches "DevAgent", "DataDev", etc.
- No `id:` field on events
- No keepalive
- No `Last-Event-ID` reconnect support
- No message backlog delivered on fresh connect

### What exists (`broadcastSSE` in `server.go:1527`)

```go
func (s *Server) broadcastSSE(space, event, data string) {
    msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
    // fan-out to all sseClients, filtered by space and agent name substring
}
```

Events currently broadcast (all with raw JSON data):
- `agent_updated` — any POST to agent channel
- `agent_removed`
- `agent_message` — when a message is sent to an agent
- `tmux_liveness` — every second per agent
- `broadcast_complete`
- `space_deleted`
- `agent_spawned` / `agent_stopped` / `agent_restarted` (lifecycle)

---

## 3. Design

### 3.1 Event Format

Each SSE event MUST include an `id:` line to support `Last-Event-ID` reconnect.

**Format:**
```
id: {event-id}
event: {event-type}
data: {json-payload}

```

**Event ID format:** monotonic integer or timestamp-based string — must be lexicographically orderable (e.g., `1772846468308665376` — same format as message IDs already used).

**Example — message delivered to agent:**
```
id: 1772846468308665376
event: message
data: {"id":"1772846468308665376","sender":"Cto","message":"[CTO DIRECTIVE] ...","priority":"directive","timestamp":"2026-03-07T01:13:06Z"}

```

**Example — keepalive comment (no `id:`, transparent to EventSource):**
```
: keepalive 2026-03-07T01:13:30Z

```

### 3.2 Event Types on Per-Agent Stream

An agent subscribing to `GET /spaces/{space}/agent/{name}/events` should receive ONLY events relevant to that agent:

| Event Type | Trigger | Payload |
|------------|---------|---------|
| `message` | Someone POSTs a message to this agent | Full `AgentMessage` JSON |
| `message_acked` | This agent's message is acked | `{"id":"...","acked_at":"..."}` |
| `agent_updated` | This agent's own status is updated | Trimmed `AgentUpdate` (omit messages array) |
| `lifecycle` | Agent spawned/stopped/restarted | `{"action":"spawned"|"stopped"|"restarted","agent":"..."}` |
| `space_event` | Space-level events (contracts updated, etc.) | `{"type":"...","space":"..."}` |

Events NOT included (to prevent context pollution):
- Other agents' `agent_updated` events
- `tmux_liveness` for other agents
- Space-wide broadcast noise

### 3.3 Agent Filtering Fix

**Current (fragile):**
```go
strings.Contains(strings.ToLower(data), strings.ToLower(c.agent))
```

**Proposed (exact match on event metadata):**

Extend `broadcastSSE` to accept a target agent parameter:

```go
func (s *Server) broadcastSSETargeted(space, targetAgent, event, data string)
```

Or embed target in the event's JSON envelope before broadcasting, and filter on exact field match:

```go
type sseEnvelope struct {
    ID        string          `json:"id"`
    TargetAgent string        `json:"target_agent,omitempty"` // empty = broadcast
    Event     string          `json:"event"`
    Data      json.RawMessage `json:"data"`
}
```

Simplest correct fix: **add `targetAgent string` to `broadcastSSE`** and filter `c.agent == targetAgent` (case-insensitive exact match) instead of substring search.

### 3.4 `Last-Event-ID` Reconnect

**HTTP spec:** Browser `EventSource` automatically sends `Last-Event-ID` header on reconnect. Server should replay missed events.

**Implementation options:**

**Option A — Event buffer per agent (recommended):**
- Server keeps a ring buffer of the last N events (e.g., 200) per agent, in memory
- On `GET /spaces/{space}/agent/{name}/events?since={id}` or with `Last-Event-ID` header, replay events with ID > last seen
- Buffer lives in memory; not persisted across restart
- On reconnect after server restart, agent gets a `reconnected` event and must re-fetch via `/messages?since=` to catch up

**Option B — Journal-backed replay:**
- Use the event journal (PR #7) to replay events since `Last-Event-ID`
- More durable but higher latency; better for long disconnections

**Recommendation: Option A** for simplicity. The journal can serve as fallback for long disconnections.

**Implementation sketch:**
```go
type agentEventBuffer struct {
    mu     sync.Mutex
    events []sseEvent  // ring buffer, cap 200
    cursor int
}

type sseEvent struct {
    ID    string
    Type  string
    Data  []byte
}
```

Server stores one buffer per `space/agent` key. On connect, reads `Last-Event-ID` header, replays buffered events with ID > that value before entering the streaming loop.

### 3.5 Keepalive

SSE connections through proxies/load balancers time out after 30-90s of silence. Send a keepalive comment every 15 seconds:

```
: keepalive 2026-03-07T01:13:30Z

```

**Implementation:** Add a keepalive ticker to `serveSSE` and `handleAgentSSE`:

```go
keepalive := time.NewTicker(15 * time.Second)
defer keepalive.Stop()

for {
    select {
    case <-ctx.Done():
        return
    case msg := <-client.ch:
        w.Write(msg)
        flusher.Flush()
    case t := <-keepalive.C:
        fmt.Fprintf(w, ": keepalive %s\n\n", t.UTC().Format(time.RFC3339))
        flusher.Flush()
    }
}
```

### 3.6 Integration with Webhook Delivery

Current flow (in `handleAgentMessage`):
1. Store message in agent record
2. Call `tryWebhookDelivery` (goroutine, fire-and-forget)

**Proposed unified delivery priority:**
1. SSE push (if agent has active SSE connection) — zero latency, zero server cost
2. Webhook (if registered callback URL) — agent doesn't poll
3. Polling fallback (`/messages?since=`) — agent polls when it wakes

**Implementation:** Track whether an agent has an active SSE connection. Add `hasActiveSSE(space, agent string) bool` helper that checks `sseClients` for a matching client:

```go
func (s *Server) hasActiveSSE(space, agent string) bool {
    s.sseMu.Lock()
    defer s.sseMu.Unlock()
    for c := range s.sseClients {
        if c.space == space && strings.EqualFold(c.agent, agent) {
            return true
        }
    }
    return false
}
```

Modified delivery in `handleAgentMessage`:
```go
// 1. Always store the message
// 2. SSE push (best effort — if connection exists)
s.broadcastSSETargeted(spaceName, canonical, "message", sseData)
// 3. Webhook fallback only if no active SSE connection
if !s.hasActiveSSE(spaceName, canonical) {
    s.tryWebhookDelivery(spaceName, canonical, msg)
}
```

Note: SSE push is best-effort (buffered channel, drops if full). Webhook is the reliability layer. Polling is the safety net. Both SSE and webhook can fire — agents should deduplicate by message ID.

### 3.7 Endpoint Specification

```
GET /spaces/{space}/agent/{name}/events
```

**Query parameters:**
- `since={event-id}` — optional; replay events with ID > this value from buffer

**Request headers:**
- `Last-Event-ID: {event-id}` — standard SSE reconnect header; equivalent to `?since=`

**Response headers:**
```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
Access-Control-Allow-Origin: *
X-Accel-Buffering: no  // ADD THIS — prevents nginx from buffering SSE
```

**Initial response on connect:**
```
: connected to agent stream {space}/{name}
id: {last-buffered-event-id}
event: connected
data: {"agent":"{name}","space":"{space}","buffered_since":"{oldest-buffered-event-id}"}

```

**On reconnect with Last-Event-ID:**
```
: replaying N missed events

id: {event-id-1}
event: message
data: {...}

id: {event-id-2}
event: agent_updated
data: {...}

: replay complete
```

---

## 4. Gaps in Current Implementation

| Gap | Severity | Fix |
|-----|----------|-----|
| No `id:` on SSE events | High | Add event ID generation to `broadcastSSE` |
| Agent name substring match | High | Change to exact match in `broadcastSSETargeted` |
| No keepalive | Medium | Add 15s ticker to SSE serve loop |
| No `Last-Event-ID` replay | Medium | Add per-agent event ring buffer |
| No `X-Accel-Buffering: no` header | Medium | Add to SSE response headers |
| Webhook fires even when SSE active | Low | Check `hasActiveSSE` before webhook |
| `tmux_liveness` events in stream | Low | Exclude from per-agent stream |

---

## 5. Implementation Notes for ProtocolDev

**Recommended implementation order:**
1. Fix agent name filtering (exact match) — correctness bug, easiest fix
2. Add `id:` to all broadcasted events — enables Last-Event-ID
3. Add keepalive ticker — prevents proxy timeouts
4. Add `X-Accel-Buffering: no` header
5. Add per-agent event buffer + Last-Event-ID replay
6. Integrate SSE check into webhook delivery

**Files to modify:**
- `internal/coordinator/server.go` — `sseClient`, `broadcastSSE`, `serveSSE`
- `internal/coordinator/protocol.go` — `handleAgentSSE`, `tryWebhookDelivery`
- `internal/coordinator/types.go` — possibly add `sseEvent`, `agentEventBuffer`

**Testing:**
- `TestSSEAgentFilter` — verify exact match, not substring
- `TestSSEKeepalive` — verify `: keepalive` sent after 15s
- `TestSSELastEventID` — connect, disconnect, reconnect with Last-Event-ID, verify replay
- `TestSSEWebhookFallback` — verify webhook NOT fired when SSE active; fired when not

---

## 6. Non-Goals

- Persistent SSE event log across server restarts (use `/messages?since=` for that)
- Fan-out to multiple subscribers per agent (one SSE connection per agent is expected)
- Binary/protobuf encoding (JSON only, consistent with zero-external-deps constraint)
