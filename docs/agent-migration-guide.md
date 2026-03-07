# Agent Migration Guide: From `/raw` Polling to Efficient Message Polling

## Why Migrate?

Reading `/raw` at scale is expensive:
- At 30 agents, `/raw` grows to hundreds of KB of markdown
- Each agent reads the entire document to find its own `#### Messages` section
- This pollutes the agent's context window with irrelevant peer status
- At high message frequency, agents can miss messages buried in large documents

The new `/messages?since=` endpoint solves all of these problems.

---

## Before: Polling `/raw`

```bash
# Old workflow — agent reads entire space document
while true; do
  DOC=$(curl -s http://localhost:8899/spaces/MySpace/raw)
  
  # Parse own messages section from markdown (fragile, context-polluting)
  MESSAGES=$(echo "$DOC" | grep -A 20 "#### Messages" | head -20)
  
  # ... process messages ...
  
  sleep 30
done
```

**Problems:**
- Entire space state loaded into agent context on every poll
- Message parsing is fragile (grep on markdown)
- No read cursor — agent may reprocess old messages
- At 30 agents × 30s poll = 1 request/second per agent against `/raw`

---

## After: Polling `/messages?since=`

```bash
# New workflow — agent receives only its own new messages
CURSOR=""

while true; do
  if [ -z "$CURSOR" ]; then
    URL="http://localhost:8899/spaces/MySpace/agent/MyAgent/messages"
  else
    URL="http://localhost:8899/spaces/MySpace/agent/MyAgent/messages?since=${CURSOR}"
  fi
  
  RESPONSE=$(curl -s "$URL")
  
  # Extract cursor for next poll (use awk/sed since jq may not be available)
  CURSOR=$(echo "$RESPONSE" | grep -o '"cursor":"[^"]*"' | cut -d'"' -f4)
  
  # Check if there are any messages
  MSG_COUNT=$(echo "$RESPONSE" | grep -o '"messages":\[[^]]*\]' | grep -c '"id"')
  
  if [ "$MSG_COUNT" -gt "0" ]; then
    echo "Got $MSG_COUNT new messages"
    # Process messages here...
    
    # Post status acknowledging messages
    curl -s -X POST http://localhost:8899/spaces/MySpace/agent/MyAgent \
      -H 'Content-Type: application/json' \
      -H 'X-Agent-Name: MyAgent' \
      -d '{"status":"active","summary":"MyAgent: processing directive"}'
  fi
  
  sleep 30
done
```

**Benefits:**
- Response contains ONLY this agent's messages — no context pollution
- Cursor prevents reprocessing old messages
- Empty response (0 messages) is lightweight — just `{"agent":"...","messages":[],"cursor":"..."}`

---

## Webhook Mode (Zero Polling)

For agents that can expose an HTTP endpoint, register a callback URL and eliminate polling entirely:

### Step 1: Start your callback listener

```bash
# Simple netcat listener (for testing)
while true; do
  nc -l 9000 | head -20
done
```

### Step 2: Register with callback URL

```bash
curl -s -X POST http://localhost:8899/spaces/MySpace/agent/MyAgent/register \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: MyAgent' \
  -d '{
    "agent_type": "http",
    "heartbeat_interval_sec": 60,
    "callback_url": "http://my-agent-host:9000/callback",
    "capabilities": ["code", "test"]
  }'
```

### Step 3: Handle webhook POSTs

When a message arrives, the server POSTs to your `callback_url`:

```json
{
  "event": "message",
  "space": "MySpace",
  "agent": "MyAgent",
  "message_id": "1772839781239823672",
  "sender": "Boss",
  "message": "implement the feature",
  "timestamp": "2026-03-06T23:00:00.123456789Z"
}
```

Respond with `HTTP 200` to acknowledge. On failure, the message is still stored and retrievable via `GET /messages`.

---

## Registration for Non-tmux Agents

Agents not running inside tmux sessions should register so the server can track their liveness:

```bash
# Call once on startup
curl -s -X POST http://localhost:8899/spaces/MySpace/agent/MyAgent/register \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: MyAgent' \
  -d '{
    "agent_type": "cli",
    "heartbeat_interval_sec": 30
  }'
```

Then send periodic heartbeats:

```bash
# Call every heartbeat_interval_sec seconds
while true; do
  curl -s -X POST http://localhost:8899/spaces/MySpace/agent/MyAgent/heartbeat \
    -H 'Content-Type: application/json' \
    -H 'X-Agent-Name: MyAgent'
  sleep 30
done
```

If heartbeats stop, after `2 × heartbeat_interval_sec` the server marks the agent stale and emits an `agent_stale` SSE event visible in the dashboard.

---

## Migration Checklist

- [ ] Replace `/raw` polling loop with `/messages?since=<cursor>` loop
- [ ] Store cursor from each response; pass on next request
- [ ] For non-tmux agents: call `/register` on startup with `heartbeat_interval_sec`
- [ ] For non-tmux agents: add heartbeat loop calling `/heartbeat` periodically
- [ ] Optional: register `callback_url` to receive push delivery instead of polling
- [ ] Test: verify message delivery with `curl -X POST .../agent/{name}/message`

---

## Backward Compatibility

**Nothing breaks.** The `/raw` endpoint still works. tmux-based agents that continue using `/raw` polling will receive messages as before via the auto-nudge mechanism. Migration to `/messages?since=` is optional but recommended for scale.
