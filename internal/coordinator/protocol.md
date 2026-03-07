## Communication Protocol

### Coordinator (8899)

All agents use `localhost:8899` exclusively.

Space: `{SPACE}`

### Endpoints

#### Core (all agents)

| Action | Command |
|--------|---------|
| Post status (JSON) | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name} -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"status":"...","summary":"...","items":[...]}'` |
| Post status (text) | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name} -H 'Content-Type: text/plain' -H 'X-Agent-Name: {name}' --data-binary @/tmp/my_update.md` |
| Send message | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{target}/message -H 'Content-Type: application/json' -H 'X-Agent-Name: {sender}' -d '{"message":"..."}'` |
| Read my section | `curl -s http://localhost:8899/spaces/{SPACE}/agent/{name}` |
| Read full doc | `curl -s http://localhost:8899/spaces/{SPACE}/raw` |
| Poll my messages | `curl -s "http://localhost:8899/spaces/{SPACE}/agent/{name}/messages?since=<RFC3339>"` |
| Browser | `http://localhost:8899/spaces/{SPACE}/` (polls every 3s) |

#### Registration & Heartbeat (non-tmux agents)

| Action | Command |
|--------|---------|
| Register | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name}/register -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"agent_type":"http","heartbeat_interval_sec":30,"callback_url":"http://host:port/cb","capabilities":["code"]}'` |
| Heartbeat | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name}/heartbeat -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}'` |

### Rules

1. **Read before you write.** Always `GET /raw` first (or use `GET /agent/{name}/messages?since=` for efficient message polling).
2. **Post to your endpoint only.** Use `POST /spaces/{SPACE}/agent/{name}`.
3. **Identify yourself.** Every POST requires `-H 'X-Agent-Name: {name}'` matching the URL. The server rejects cross-channel posts (403).
4. **Tag questions with `[?BOSS]`** — they render highlighted in the dashboard.
5. **Concise summaries.** Always Use "{name}: {summary}" (required!).
6. **Safe writes.** Write to a temp file first, then POST with `--data-binary @/tmp/file.md`.
7. **Report your location and metrics.** Include `"branch"`, `"pr"`, `"jira"`, `"test_count"`, and `"repo_url"` in every POST. `"branch"` is the git branch you are working on. `"pr"` is the merge request number (e.g. `"#699"`). `"jira"` is the Jira issue key (e.g. `"OCPAPI-1234"`) — **every PR must have an associated Jira issue**. `"test_count"` is the number of passing tests. `"repo_url"` is the full HTTPS URL of your GitLab repository (e.g. `"https://gitlab.cee.redhat.com/ocm/platform"`). All five are **required** whenever applicable — the dashboard uses `repo_url` + `pr` to create clickable links to merge requests and `jira` to link to Red Hat Jira. `repo_url` is **sticky** like `tmux_session` — send it once and the server remembers it.

> **IMPORTANT: `repo_url` is REQUIRED in your first POST.** Without it, PR links in the dashboard are broken. Find it with `git remote get-url origin` and include it as `"repo_url": "https://..."`. You only need to send it once — the server remembers it.
8. **Register your tmux session.** Include `"tmux_session"` in your **first** POST so the coordinator can send you check-in broadcasts. Find your session name with `tmux display-message -p '#S'`. This field is **sticky** — the server preserves it automatically on subsequent POSTs, so you only need to send it once.
9. **Check your messages efficiently.** Prefer `GET /agent/{name}/messages?since=<cursor>` over reading `/raw` — it returns only your new messages and a cursor for the next poll, avoiding context pollution. The `#### Messages` section in `/raw` is still available as a fallback.
10. **Model economy.** Status check-ins (`boss check`) are read/post operations — not heavy reasoning. Use a lightweight model (e.g. Haiku) for check-ins, then switch back to your working model (e.g. Opus) for real work. The broadcast script handles this automatically via `/model` switching.

### Agent Registration (optional, for non-tmux agents)

Non-tmux agents (CLI tools, scripts, remote processes) should register once on startup:

```bash
curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name}/register \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: {name}' \
  -d '{
    "agent_type": "http",
    "heartbeat_interval_sec": 30,
    "callback_url": "http://my-agent-host:9000/messages",
    "capabilities": ["code", "research"]
  }'
```

- `agent_type`: `"tmux"`, `"http"`, `"cli"`, `"script"`, or `"remote"`
- `heartbeat_interval_sec`: how often the agent will call `/heartbeat` (0 = no heartbeat)
- `callback_url`: server will POST new messages here instead of waiting for polling (optional)
- `capabilities`: free-form list for discoverability (optional)

**Heartbeat:** send periodically to prevent staleness detection:
```bash
curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name}/heartbeat \
  -H 'X-Agent-Name: {name}'
```
Agents that miss `2 × heartbeat_interval_sec` are marked stale and an `agent_stale` SSE event is emitted.

### Efficient Message Polling

Instead of reading the full `/raw` document (which grows with agents), poll only your messages:

```bash
# Initial fetch — get all pending messages + cursor
curl -s "http://localhost:8899/spaces/{SPACE}/agent/{name}/messages"

# Subsequent polls — pass cursor from previous response
curl -s "http://localhost:8899/spaces/{SPACE}/agent/{name}/messages?since=2026-03-06T23:00:00.000000001Z"
```

Response:
```json
{
  "agent": "MyAgent",
  "messages": [
    {"id": "123", "sender": "Boss", "message": "do the thing", "timestamp": "..."}
  ],
  "cursor": "2026-03-06T23:00:00.000000002Z"
}
```

Store the `cursor` value and pass it as `?since=` on each subsequent poll. Empty `messages` array means no new messages — use the same cursor next time.

### Webhook / Callback Delivery

If you registered a `callback_url`, the server will POST to it whenever a message arrives:

```json
{
  "event": "message",
  "space": "{SPACE}",
  "agent": "{name}",
  "message_id": "1234567",
  "sender": "Boss",
  "message": "do the thing",
  "timestamp": "2026-03-06T23:00:00Z"
}
```

Respond with `200 OK` to acknowledge. On failure, the server logs the error and the message remains available via `GET /messages`.

### JSON Format Reference

```json
{
  "status": "active|done|blocked|idle|error",
  "summary": "One-line summary (required)",
  "branch": "feat/my-feature",
  "worktree": "../platform-api-server/",
  "pr": "#699",
  "jira": "OCPAPI-1234",
  "repo_url": "https://gitlab.cee.redhat.com/ocm/platform",
  "phase": "current phase",
  "test_count": 0,
  "items": ["bullet point 1", "bullet point 2"],
  "sections": [{"title": "Section Name", "items": ["detail"]}],
  "questions": ["tagged [?BOSS] automatically"],
  "blockers": ["highlighted automatically"],
  "tmux_session": "my-tmux-session",
  "next_steps": "What you're doing next"
}
```