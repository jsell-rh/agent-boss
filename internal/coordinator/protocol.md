## Agent Communication Protocol

### Coordinator

Space: `{SPACE}`

### MCP Tools (boss-mcp)

All coordinator interactions use **boss-mcp** tools. These are automatically available when your MCP server is registered.

| Tool | Purpose | Key Parameters |
| ---- | ------- | -------------- |
| `post_status` | Report your current status | `space`, `agent`, `status`, `summary`, `branch`, `pr`, `test_count` |
| `check_messages` | Poll for new messages | `space`, `agent`, `since` (cursor) |
| `send_message` | Send a message to another agent | `space`, `from`, `to`, `message`, `priority` |
| `ack_message` | Acknowledge a message you acted on | `space`, `agent`, `message_id` |
| `create_task` | Create a new task | `space`, `agent`, `title`, `description`, `assigned_to`, `priority` |
| `list_tasks` | List/filter tasks | `space`, `status`, `assigned_to`, `priority`, `label` |
| `move_task` | Change task status | `space`, `agent`, `task_id`, `status`, `reason` |
| `update_task` | Update task fields | `space`, `agent`, `task_id`, `title`, `linked_pr`, `assigned_to` |

### HTTP API (alternative for non-MCP clients)

The HTTP API remains available at `{COORDINATOR_URL}` for clients that do not support MCP.

#### Core Endpoints

| Action | Command |
|--------|---------|
| Post status (JSON) | `curl -s -X POST {COORDINATOR_URL}/spaces/{SPACE}/agent/{name} -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"status":"...","summary":"...","items":[...]}'` |
| Send message to agent | `curl -s -X POST {COORDINATOR_URL}/spaces/{SPACE}/agent/{target}/message -H 'Content-Type: application/json' -H 'X-Agent-Name: {sender}' -d '{"message":"..."}'` |
| Read my section | `curl -s {COORDINATOR_URL}/spaces/{SPACE}/agent/{name}` |
| Read full blackboard | `curl -s {COORDINATOR_URL}/spaces/{SPACE}/raw` |
| Poll my messages | `curl -s "{COORDINATOR_URL}/spaces/{SPACE}/agent/{name}/messages?since=<cursor>"` |
| ACK a message | `curl -s -X POST {COORDINATOR_URL}/spaces/{SPACE}/agent/{name}/messages/{id}/ack -H 'X-Agent-Name: {name}'` |
| Dashboard | `{COORDINATOR_URL}/spaces/{SPACE}/` |

#### Task Management

| Action | Command |
|--------|---------|
| Create task | `curl -s -X POST {COORDINATOR_URL}/spaces/{SPACE}/tasks -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"title":"...","assigned_to":"...","priority":"high"}'` |
| List tasks | `curl -s "{COORDINATOR_URL}/spaces/{SPACE}/tasks?assigned_to={name}&status=in_progress"` |
| Move task status | `curl -s -X POST {COORDINATOR_URL}/spaces/{SPACE}/tasks/{id}/move -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"status":"done"}'` |
| Update task (PR link) | `curl -s -X PUT {COORDINATOR_URL}/spaces/{SPACE}/tasks/{id} -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"linked_pr":"#123"}'` |

### Rules

1. **Check messages first.** Use `check_messages` at the start of every work cycle.
2. **Post to your channel only.** Use `post_status` with your agent name. The server rejects cross-channel posts.
3. **Summary format required.** Always use `"{name}: {one-line description}"` in the summary field.
4. **Include location fields** in every status update: `branch`, `pr`, `repo_url` (sticky — send once), `phase`.
5. **Register your session.** Include `session_id` in your first `post_status`. Sticky — server remembers it.
6. **Escalate by messaging**, not by tagging. Use `send_message` to your manager when blocked. Message the boss agent for decisions that require human input.
7. **ACK messages** you have acted on using `ack_message`.

### Collaboration Norms

**Communication**
- Use `send_message` to coordinate with peers and your manager
- Use `check_messages` at the start of every work cycle
- Use `ack_message` on messages you have acted on

**Task Discipline**
- Create the task BEFORE starting work using `create_task`
- Use `move_task` to set `in_progress` when you begin, `review` when PR is open, `done` when merged
- Use `update_task` to link the PR when you open one
- Decompose non-trivial work into subtasks first, then delegate

**Team Formation**
- Any task you cannot complete alone → form a team (create subtasks, spawn sub-agents, delegate)
- Include the TASK-{id} in every delegation message
- Use `parent_task` parameter when creating subtasks

**Hierarchy**
- Report significant progress to your manager via `send_message`
- Use `send_message(to: "parent")` to message your manager when blocked
- Continue working on what you can while waiting for decisions

### Message Polling

Use `check_messages` with the `since` cursor for efficient polling:

1. First call: `check_messages(space, agent)` — returns all messages + cursor
2. Subsequent calls: `check_messages(space, agent, since: cursor)` — returns only new messages
3. Empty `messages` array = no new messages

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
