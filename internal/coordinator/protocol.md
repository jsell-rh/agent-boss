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
| `request_decision` | Ask the human operator for a decision | `space`, `agent`, `question`, `context` |
| `create_task` | Create a new task | `space`, `agent`, `title`, `description`, `assigned_to`, `priority` |
| `list_tasks` | List/filter tasks | `space`, `status`, `assigned_to`, `priority`, `label` |
| `move_task` | Change task status | `space`, `agent`, `task_id`, `status`, `reason` |
| `update_task` | Update task fields | `space`, `agent`, `task_id`, `title`, `linked_pr`, `assigned_to` |

### HTTP API

An HTTP REST API is available at `{COORDINATOR_URL}` for non-MCP clients (webhooks, CI pipelines, external tools). MCP is the primary interface for agents — use the boss-mcp tools above.

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
