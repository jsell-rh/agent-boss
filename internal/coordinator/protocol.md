## Agent Communication Protocol

### Coordinator (8899)

All agents use `http://localhost:8899` exclusively.

Space: `{SPACE}`

### Endpoints

#### Core (all agents)

| Action | Command |
|--------|---------|
| Post status (JSON) | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name} -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"status":"...","summary":"...","items":[...]}'` |
| Send message to agent | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{target}/message -H 'Content-Type: application/json' -H 'X-Agent-Name: {sender}' -d '{"message":"..."}'` |
| Read my section | `curl -s http://localhost:8899/spaces/{SPACE}/agent/{name}` |
| Read full blackboard | `curl -s http://localhost:8899/spaces/{SPACE}/raw` |
| Poll my messages | `curl -s "http://localhost:8899/spaces/{SPACE}/agent/{name}/messages?since=<cursor>"` |
| ACK a message | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name}/messages/{id}/ack -H 'X-Agent-Name: {name}'` |
| Dashboard | `http://localhost:8899/spaces/{SPACE}/` |

#### Task Management

| Action | Command |
|--------|---------|
| Create task | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/tasks -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"title":"...","assigned_to":"...","priority":"high"}'` |
| List tasks | `curl -s "http://localhost:8899/spaces/{SPACE}/tasks?assigned_to={name}&status=in_progress"` |
| Move task status | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/tasks/{id}/move -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"status":"done"}'` |
| Update task (PR link) | `curl -s -X PUT http://localhost:8899/spaces/{SPACE}/tasks/{id} -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"linked_pr":"#123"}'` |

#### Agent Lifecycle (tmux agents)

| Action | Command |
|--------|---------|
| Spawn | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name}/spawn -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{}'` |
| Restart | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name}/restart -H 'X-Agent-Name: {name}'` |
| Stop | `curl -s -X POST http://localhost:8899/spaces/{SPACE}/agent/{name}/stop -H 'X-Agent-Name: {name}'` |

### Rules

1. **Read before you write.** Check messages first: `GET /agent/{name}/messages?since=<cursor>`
2. **Post to your channel only.** Use `POST /spaces/{SPACE}/agent/{name}` with `X-Agent-Name: {name}`. The server rejects cross-channel posts (403).
3. **Summary format required.** Always use `"{name}: {one-line description}"` in the summary field.
4. **Include location fields** in every POST: `branch`, `pr`, `repo_url` (sticky — send once), `phase`.
5. **Register your session.** Include `"session_id"` in your first POST (`tmux display-message -p '#S'`). Sticky — server remembers it.
6. **Escalate by messaging**, not by tagging. Message your manager directly when blocked. Message the boss channel for decisions that require human input. Do not use tag syntax like `[?BOSS]` — it is not supported.
7. **ACK messages** you have acted on via `POST /messages/{id}/ack`.

### Collaboration Norms

**Communication**
- Message peers and your manager via POST to their `/message` endpoint
- Check messages at the start of every work cycle using the cursor-based polling endpoint
- Acknowledge messages you have acted on

**Task Discipline**
- Create the task BEFORE starting work
- Set `in_progress` when you begin, `review` when PR is open, `done` when merged
- Link the PR: update task with `linked_pr` field when you open one
- Decompose non-trivial work into subtasks first, then delegate

**Team Formation**
- Any task you cannot complete alone → form a team (create subtasks, spawn sub-agents, delegate)
- Include the TASK-{id} in every delegation message
- Spawn sub-agents via `POST /spawn` with an `initial_message` field to pre-load their mission

**Hierarchy**
- Report significant progress to your manager via message
- Message your manager when blocked; describe the blocker and what you need to unblock
- Continue working on what you can while waiting for decisions

### Efficient Message Polling

```bash
# Initial fetch — get all pending messages + cursor
curl -s "http://localhost:8899/spaces/{SPACE}/agent/{name}/messages"

# Subsequent polls — pass cursor from previous response
curl -s "http://localhost:8899/spaces/{SPACE}/agent/{name}/messages?since=<cursor>"
```

Store the `cursor` and pass it as `?since=` on each poll. Empty `messages` = no new messages.

### JSON Format Reference

```json
{
  "status": "active|done|blocked|idle|review",
  "summary": "{name}: one-line description",
  "branch": "feat/my-feature",
  "pr": "#123",
  "repo_url": "https://github.com/org/repo",
  "phase": "implementation",
  "test_count": 0,
  "items": ["completed item", "in-progress item"],
  "next_steps": "what you will do next"
}
```

### MCP Resources (available via boss-mcp)

| Resource | URI |
|----------|-----|
| This protocol | `boss://protocol` |
| Agent bootstrap | `boss://bootstrap/{space}/{agent}` |
| Space blackboard | `boss://space/{space}/blackboard` |
