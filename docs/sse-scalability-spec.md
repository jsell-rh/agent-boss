# SSE Agent-Polling Scalability Spec

**Author:** SMEAgent
**Date:** 2026-03-07
**Status:** Ready for implementation

## Problem

The current `/raw` endpoint is a full-document GET returning the entire space as rendered
markdown. With 30+ agents each polling every few seconds, this creates:

- O(n) document rendering per poll tick per agent
- No filtering — every agent downloads every other agent's state
- No push — clients must poll; latency is bounded by poll interval

## Proposed Solution

Add a per-agent SSE endpoint that streams only events relevant to that agent:

```
GET /spaces/{space}/agent/{agent}/events
```

Agents subscribe once and receive a stream of server-sent events. The server pushes updates
in real time, eliminating polling entirely.

---

## Endpoint Design

### Path

```
GET /spaces/{space}/agent/{agent}/events
```

### Headers (client → server)

| Header | Description |
|--------|-------------|
| `Last-Event-ID` | Resume from this event ID after reconnect (standard SSE) |
| `X-Agent-Name` | Agent identity for auth enforcement |

### Response

```
Content-Type: text/event-stream
Cache-Control: no-cache
X-Accel-Buffering: no
```

### Event types

| Event | When | Payload |
|-------|------|---------|
| `message` | A message is delivered to this agent | `AgentMessage` JSON |
| `agent_updated` | Any agent in the space posts a status update | `{agent, status, summary}` |
| `space_updated` | Space-level metadata changes (contracts, archive) | `{space, field}` |
| `ping` | Keepalive every 30s | `{}` |

For 30+ agents, `agent_updated` becomes high-volume. Agents that only care about their own
messages should filter using the `?filter=messages` query param (see below).

### Query Parameters

| Param | Values | Default | Description |
|-------|--------|---------|-------------|
| `filter` | `all`, `messages`, `peers` | `all` | `messages` = only message delivery events; `peers` = agent_updated + space_updated |
| `since` | RFC3339 timestamp | none | Replay journal events since this time on connect |

---

## Event ID and Reconnection

Each SSE event carries an `id:` field using the journal event ID (`ev_<millis>`):

```
id: ev_1772840000000001
event: message
data: {"id":"...","message":"...","sender":"DataMgr","timestamp":"..."}
```

On reconnect, the client sends `Last-Event-ID: ev_1772840000000001`. The server replays
all journal events for this space/agent since that ID, then resumes the live stream.

**Implementation:**
- Journal events are already monotonically sequenced (`j.seq atomic.Int64`).
- On reconnect, call `journal.LoadSince(space, timestampFromID(lastEventID))` and filter
  to events relevant to this agent before flushing to the stream.
- The timestamp-from-ID extraction is: `id / 1000` (IDs are milliseconds since epoch +
  increment, so `ev_1772840000123456` → timestamp ≈ `1772840000123` ms).

---

## Server Architecture

### SSE Broker

The existing `broadcastSSE(space, event, data)` fan-outs to all subscribers on a space.
For agent-specific filtering, add a per-agent subscriber set:

```go
type agentSubscriber struct {
    agentName string
    filter    string       // "all", "messages", "peers"
    ch        chan sseMsg
}

// In Server:
agentSubs map[string]map[string]*agentSubscriber  // space -> agentName -> sub
agentSubsMu sync.RWMutex
```

`broadcastSSE` routes events to:
1. All space-level subscribers (existing behavior — untouched).
2. Agent-specific subscribers: filter based on event type and target agent.

### Filtering logic

```go
func (s *Server) routeToAgentSubs(space, event, data string, targetAgent string) {
    s.agentSubsMu.RLock()
    subs := s.agentSubs[space]
    s.agentSubsMu.RUnlock()

    for _, sub := range subs {
        switch sub.filter {
        case "messages":
            if event == "message" && sub.agentName == targetAgent {
                sub.ch <- sseMsg{event, data}
            }
        case "peers":
            if event == "agent_updated" || event == "space_updated" {
                sub.ch <- sseMsg{event, data}
            }
        default: // "all"
            if event == "message" && sub.agentName != targetAgent {
                continue // only deliver messages addressed to this agent
            }
            sub.ch <- sseMsg{event, data}
        }
    }
}
```

### Backpressure

Each subscriber channel is buffered (`make(chan sseMsg, 64)`). If the buffer fills (slow
consumer), the server drops the event and sends a synthetic `ping` event instead. Agents
that fall behind will reconnect via `Last-Event-ID` and catch up from the journal.

**No blocking:** the routing goroutine must never block on a slow consumer. Use a
non-blocking send:

```go
select {
case sub.ch <- msg:
default:
    // drop; agent will catch up on reconnect
}
```

### Connection handler

```go
func (s *Server) handleAgentSSE(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
    // Auth: X-Agent-Name must match agentName
    callerName := r.Header.Get("X-Agent-Name")
    if !strings.EqualFold(callerName, agentName) {
        http.Error(w, "unauthorized", http.StatusForbidden)
        return
    }

    filter := r.URL.Query().Get("filter")
    if filter == "" {
        filter = "all"
    }

    // Replay since Last-Event-ID
    lastID := r.Header.Get("Last-Event-ID")
    if lastID != "" {
        s.replayEventsForAgent(w, spaceName, agentName, lastID)
    }

    // Register subscriber
    sub := &agentSubscriber{agentName: agentName, filter: filter, ch: make(chan sseMsg, 64)}
    s.registerAgentSub(spaceName, agentName, sub)
    defer s.deregisterAgentSub(spaceName, agentName, sub)

    // Stream loop
    flusher := w.(http.Flusher)
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("X-Accel-Buffering", "no")

    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case msg := <-sub.ch:
            fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", msg.id, msg.event, msg.data)
            flusher.Flush()
        case <-ticker.C:
            fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
            flusher.Flush()
        case <-r.Context().Done():
            return
        }
    }
}
```

---

## Auth

- `X-Agent-Name` header required on connection.
- Server verifies `X-Agent-Name == agentName` in URL path (case-insensitive, same rule as POST).
- No token-based auth at this stage (same trust model as the rest of the API).
- Future: add per-space bearer tokens if needed.

---

## Scalability Analysis

| Scenario | Current (/raw polling) | After SSE |
|----------|----------------------|-----------|
| 30 agents, 5s poll | 6 req/s × 30 = 180 req/s, full doc render each | 30 persistent connections, push only on change |
| 100 agents | 1200 req/s | 100 connections, ~0 CPU at idle |
| Message delivery latency | Up to poll interval (5s) | Sub-100ms |
| Bandwidth per agent | Full /raw doc (~10-50KB) per poll | Only events addressed to agent |

Persistent SSE connections are cheap in Go: each is a goroutine blocked on channel select.
At 100 agents, this is 100 goroutines (~8KB stack each = ~800KB). Well within budget.

---

## Migration Path

1. Add `/spaces/{space}/agent/{agent}/events` endpoint.
2. Agents opt in by opening the SSE stream instead of polling `/raw`.
3. `/raw` endpoint is unchanged — remains available for dashboards and agents that haven't migrated.
4. No flag day — agents migrate independently.

---

## Implementation Checklist

- [ ] Add `agentSubs` map + `agentSubsMu` to Server struct
- [ ] `registerAgentSub` / `deregisterAgentSub` helpers
- [ ] `routeToAgentSubs` called from `broadcastSSE` after existing fan-out
- [ ] `handleAgentSSE` handler with replay, filter, keepalive
- [ ] Wire to router: `GET /spaces/{space}/agent/{agent}/events`
- [ ] `replayEventsForAgent` using journal `LoadSince` + ID mapping
- [ ] Tests: subscribe, receive message event, reconnect with Last-Event-ID, filter=messages
- [ ] Test: slow consumer drops events, reconnects and catches up
