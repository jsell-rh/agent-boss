# Task Management Spec

**Status:** Draft
**Owner:** ProtocolMgr

## Principle: Tasks Are the Source of Truth

Every piece of work must have a task. Every task must be kept in sync with actual work. There is no invisible work.

## Task Lifecycle

```
backlog → in_progress → review → done
                ↘ blocked
```

| Status | Meaning | Who Sets |
|--------|---------|---------|
| `backlog` | Created but not started | Creator |
| `in_progress` | Actively being worked | Assignee (on start) |
| `blocked` | Cannot progress; needs resolution | Assignee |
| `review` | Work complete; awaiting review/merge | Assignee |
| `done` | Fully complete and merged | Manager or assignee |

## Rules

### Rule 1: Create the task before the work

When a manager decides to do something, the task is created **before** agents are spawned. Agents receive task IDs in their mission messages.

```bash
# Correct order:
1. POST /tasks → TASK-{id}
2. Spawn agent
3. Message agent with TASK-{id}
```

### Rule 2: Set status on_start

The first thing an agent does when beginning a task is update its status:
```bash
curl -X PATCH /spaces/{space}/tasks/{id} \
  -H 'X-Agent-Name: {assignee}' \
  -d '{"status": "in_progress"}'
```

### Rule 3: Subtasks for non-trivial work

Non-trivial tasks (see team-formation.md) must be decomposed into subtasks before work begins:
```bash
curl -X POST /spaces/{space}/tasks \
  -H 'X-Agent-Name: {manager}' \
  -d '{
    "title": "...",
    "parent_id": "TASK-{parent}",
    "assigned_to": "{agent}",
    "priority": "high"
  }'
```

Each subtask is assigned to exactly one agent. Parent task is owned by the manager.

### Rule 4: One agent per task

A task must have a single `assigned_to`. If two agents need to collaborate, one is assigned and coordinates the other as a peer (documented in the task description).

### Rule 5: Link the PR

When a PR is opened, update the task:
```bash
curl -X PATCH /spaces/{space}/tasks/{id} \
  -d '{"pr": "#72", "status": "review"}'
```

### Rule 6: Mark done on completion

When work is merged and verified:
```bash
curl -X PATCH /spaces/{space}/tasks/{id} \
  -d '{"status": "done"}'
```

The manager is responsible for marking parent tasks done after all subtasks complete.

### Rule 7: Blocked tasks get messages

When a task is blocked, the assignee:
1. Sets status to `blocked`
2. Messages their manager with `[?MANAGER] TASK-{id} blocked: {reason}`
3. Posts a status update reflecting the blocker in `next_steps`

### Rule 8: No stale in_progress

Any task in `in_progress` with no status update for > 1 hour is considered **stale**. The dashboard may flag these. Managers should follow up via message.

## Task Fields Reference

| Field | Required | Description |
|-------|----------|-------------|
| `title` | Yes | One-line description |
| `description` | Recommended | Full context, acceptance criteria |
| `assigned_to` | Yes | Single agent name |
| `priority` | Recommended | `urgent`, `high`, `normal`, `low` |
| `parent_id` | For subtasks | ID of parent task |
| `pr` | When opened | PR number e.g. `#72` |
| `status` | Yes | See lifecycle above |

## Anti-Patterns

| Anti-Pattern | Correct Approach |
|-------------|-----------------|
| Working without a task ID | Create the task first |
| Leaving tasks in `in_progress` after completion | Set to `review` or `done` |
| Assigning tasks to a team (not a person) | Assign to exactly one agent |
| Not creating subtasks for complex work | Decompose first, then delegate |
| Creating tasks with no description | Include acceptance criteria |
| Not linking the PR | Update task with `pr` field when opened |
