# Task Management System Design

**Author:** TaskDesignMgr
**Branch:** feat/task-design
**Status:** Design — awaiting CTO review before implementation
**Date:** 2026-03-07

---

## Executive Summary

This document specifies a first-class task management system for OpenDispatch. Tasks are not bolted on — they are a core entity alongside agents and spaces, with a Kanban board as the primary human-facing view. Both humans and agents can create, update, and close tasks via HTTP API. Tasks integrate with the existing hierarchy, messaging, and Gantt timeline systems.

---

## 1. Motivation

The current OpenDispatch workflow uses agent status updates as a proxy for task tracking. This works at small scale but breaks down with 10+ agents: there is no canonical list of "what work is open," no way to see blocked items, no assignment history, and no structured handoff between agents. A task system fills this gap while remaining lightweight (stdlib-only Go, no external task DB).

---

## 2. Data Model

### 2.1 Task

```go
// TaskStatus is the Kanban column a task occupies.
type TaskStatus string

const (
    TaskStatusBacklog    TaskStatus = "backlog"
    TaskStatusInProgress TaskStatus = "in_progress"
    TaskStatusReview     TaskStatus = "review"
    TaskStatusDone       TaskStatus = "done"
    TaskStatusBlocked    TaskStatus = "blocked"
)

// TaskPriority controls visual ordering and filtering on the board.
type TaskPriority string

const (
    TaskPriorityLow    TaskPriority = "low"
    TaskPriorityMedium TaskPriority = "medium"
    TaskPriorityHigh   TaskPriority = "high"
    TaskPriorityUrgent TaskPriority = "urgent"
)

// Task is the canonical unit of tracked work within a KnowledgeSpace.
type Task struct {
    ID          string       `json:"id"`           // "TASK-001" sequential, space-scoped
    Space       string       `json:"space"`
    Title       string       `json:"title"`
    Description string       `json:"description,omitempty"` // markdown
    Status      TaskStatus   `json:"status"`
    Priority    TaskPriority `json:"priority,omitempty"`

    // Assignment
    AssignedTo  string       `json:"assigned_to,omitempty"`  // agent name
    CreatedBy   string       `json:"created_by"`              // agent name or "boss"

    // Relationships
    Labels      []string     `json:"labels,omitempty"`
    ParentTask  string       `json:"parent_task,omitempty"`  // subtask support
    Subtasks    []string     `json:"subtasks,omitempty"`

    // Cross-system links
    LinkedBranch string      `json:"linked_branch,omitempty"` // git branch
    LinkedPR     string      `json:"linked_pr,omitempty"`     // PR number e.g. "#25"

    // Timestamps
    CreatedAt   time.Time    `json:"created_at"`
    UpdatedAt   time.Time    `json:"updated_at"`
    DueAt       *time.Time   `json:"due_at,omitempty"`

    // Activity
    Comments    []TaskComment `json:"comments,omitempty"`
}

// TaskComment is a human or agent note on a task.
type TaskComment struct {
    ID        string    `json:"id"`
    Author    string    `json:"author"`  // agent name or "boss"
    Body      string    `json:"body"`    // markdown
    CreatedAt time.Time `json:"created_at"`
}
```

### 2.2 ID Scheme

Task IDs are **human-readable sequential strings** scoped to a space: `TASK-001`, `TASK-002`, etc. The `KnowledgeSpace` tracks a `NextTaskSeq int` counter for atomic ID generation under the existing `s.mu` write lock. This avoids UUIDs that are opaque in agent logs.

### 2.3 KnowledgeSpace Extension

```go
type KnowledgeSpace struct {
    Name            string                  `json:"name"`
    Agents          map[string]*AgentUpdate `json:"agents"`
    Tasks           map[string]*Task        `json:"tasks,omitempty"`    // NEW
    NextTaskSeq     int                     `json:"next_task_seq,omitempty"` // NEW
    SharedContracts string                  `json:"shared_contracts,omitempty"`
    Archive         string                  `json:"archive,omitempty"`
    CreatedAt       time.Time               `json:"created_at"`
    UpdatedAt       time.Time               `json:"updated_at"`
}
```

**Persistence:** Tasks are stored inline in the existing `{space}.json` snapshot and replayed via the existing JSONL event journal using new event types (`task_created`, `task_updated`, `task_deleted`, `task_commented`). No separate file is needed — the event journal already provides durability and replay.

---

## 3. Backend API

All endpoints follow the existing pattern under `/spaces/{space}/`. Authentication is the existing `X-Agent-Name` header used for authorship attribution (not access control — same model as messaging).

### 3.1 Endpoint Table

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/spaces/{space}/tasks` | Create a task |
| `GET` | `/spaces/{space}/tasks` | List tasks (filterable) |
| `GET` | `/spaces/{space}/tasks/{id}` | Get a single task |
| `PUT` | `/spaces/{space}/tasks/{id}` | Update task fields |
| `DELETE` | `/spaces/{space}/tasks/{id}` | Delete a task |
| `POST` | `/spaces/{space}/tasks/{id}/move` | Move to a new status (Kanban drag) |
| `POST` | `/spaces/{space}/tasks/{id}/assign` | Assign to an agent |
| `POST` | `/spaces/{space}/tasks/{id}/comment` | Add a comment |

### 3.2 Request/Response Shapes

#### Create Task — `POST /spaces/{space}/tasks`

Request headers: `X-Agent-Name: {creator}`

```json
{
  "title": "Implement SSE reconnect logic",
  "description": "Handle Last-Event-ID on reconnect to prevent message loss.",
  "priority": "high",
  "assigned_to": "ProtocolDev",
  "labels": ["backend", "reliability"]
}
```

Response `201 Created`:
```json
{
  "id": "TASK-007",
  "space": "AgentBossDevTeam",
  "title": "Implement SSE reconnect logic",
  "status": "backlog",
  "priority": "high",
  "assigned_to": "ProtocolDev",
  "created_by": "ProtocolMgr",
  "labels": ["backend", "reliability"],
  "created_at": "2026-03-07T22:45:00Z",
  "updated_at": "2026-03-07T22:45:00Z"
}
```

#### List Tasks — `GET /spaces/{space}/tasks`

Query params (all optional, combinable):
- `?status=in_progress` — filter by status
- `?assigned_to=DevAgent` — filter by assignee
- `?label=backend` — filter by label
- `?priority=high` — filter by priority

Response `200 OK`: `{ "tasks": [...], "total": 12 }`

#### Move Task — `POST /spaces/{space}/tasks/{id}/move`

```json
{ "status": "in_progress" }
```

Response `200 OK`: full updated Task object.

#### Assign Task — `POST /spaces/{space}/tasks/{id}/assign`

```json
{ "assigned_to": "DevAgent" }
```

#### Add Comment — `POST /spaces/{space}/tasks/{id}/comment`

Request headers: `X-Agent-Name: {author}`
```json
{ "body": "Started investigation — looks like a goroutine leak." }
```

### 3.3 SSE Integration

Task mutations broadcast a new SSE event type to all space subscribers:

```
event: task_updated
data: {"id":"TASK-007","status":"in_progress","assigned_to":"DevAgent","space":"AgentBossDevTeam"}
```

The frontend Kanban subscribes to the existing space SSE stream (`/spaces/{space}/sse`) and moves cards in real-time on `task_updated` events, without polling.

### 3.4 Event Journal Events

New event types appended to `{space}.events.jsonl`:

| Event Type | Payload |
|------------|---------|
| `task_created` | full Task struct |
| `task_updated` | `{id, changed_fields}` |
| `task_deleted` | `{id}` |
| `task_commented` | `{task_id, comment}` |
| `task_moved` | `{id, from_status, to_status, by}` |
| `task_assigned` | `{id, from_agent, to_agent, by}` |

Replay in `journal.ReplayInto()` builds `ks.Tasks` from these events, giving full audit history and crash recovery.

### 3.5 Route Registration

In `handleSpaceRoute`, add after the existing `/hierarchy` and `/history` checks:

```go
case strings.HasPrefix(rest, "tasks"):
    s.handleSpaceTasks(w, r, spaceName, strings.TrimPrefix(rest, "tasks"))
```

---

## 4. Frontend — Kanban Board

### 4.1 New Components

| Component | Purpose |
|-----------|---------|
| `KanbanView.vue` | Full-page Kanban board — the primary task UI |
| `KanbanColumn.vue` | A single status column (Backlog, In Progress, etc.) |
| `TaskCard.vue` | Compact card shown in a column |
| `TaskDetailPanel.vue` | Right-side slide-over panel for task detail/edit |
| `NewTaskDialog.vue` | Modal dialog to create a task |

### 4.2 KanbanView Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│  Tasks — AgentBossDevTeam                          [+ New Task]      │
│  Filter: [All Assignees ▼] [All Labels ▼] [All Priorities ▼]       │
├──────────────┬───────────────┬─────────────┬────────────┬──────────┤
│   BACKLOG    │  IN PROGRESS  │   REVIEW    │    DONE    │ BLOCKED  │
│   (4)        │   (3)         │   (1)       │   (12)     │  (1)     │
├──────────────┼───────────────┼─────────────┼────────────┼──────────┤
│ ┌──────────┐ │ ┌───────────┐ │ ┌─────────┐ │            │          │
│ │TASK-001  │ │ │TASK-003   │ │ │TASK-007 │ │            │          │
│ │Fix SSE   │ │ │Kanban UI  │ │ │API docs │ │            │          │
│ │● High    │ │ │● Urgent   │ │ │● Medium │ │            │          │
│ │[DevAgent]│ │ │[UIAgent]  │ │ │[ProtDev]│ │            │          │
│ └──────────┘ │ └───────────┘ │ └─────────┘ │            │          │
└──────────────┴───────────────┴─────────────┴────────────┴──────────┘
```

### 4.3 TaskCard

Each card shows:
- Task ID (`TASK-007`) — small, muted
- Title (truncated at 2 lines)
- Priority badge (color-coded: urgent=red, high=orange, medium=blue, low=gray)
- Assignee avatar (AgentAvatar component, hover shows AgentProfileCard)
- Label chips (up to 3, then "+N more")

Click on card → opens `TaskDetailPanel` as a right-side sheet (using existing Sheet component).

### 4.4 TaskDetailPanel

Right-side slide panel (Sheet) with:
- Editable title (click to edit)
- Status selector (segmented control matching Kanban columns)
- Priority selector
- Assignee dropdown (all space agents)
- Description editor (textarea, markdown preview toggle)
- Labels (chip input)
- Linked branch / PR (auto-links to GitHub using existing `buildPrUrl` util)
- Due date picker
- Comments thread (styled like ConversationsView messages)
  - Add comment input at bottom
- Subtasks list with checkbox completion
- "View Agent" button → navigates to `/:space/:agent` detail
- "View Conversation" button → navigates to `/:space/conversations/:agent`
- Created by / created at / updated at footer

### 4.5 NewTaskDialog

Triggered by "+ New Task" button:
- Title (required)
- Description (optional)
- Priority (select, default: medium)
- Assigned To (agent dropdown, optional)
- Labels (chip input, optional)

Submits `POST /spaces/{space}/tasks` with `X-Agent-Name: boss`.

### 4.6 Drag and Drop

Use native HTML5 drag-and-drop (no external library needed):
- Each TaskCard has `draggable="true"` and `@dragstart` handler
- Each KanbanColumn listens for `@dragover` and `@drop`
- On drop: call `POST /spaces/{space}/tasks/{id}/move` with new status
- Optimistic update: move card immediately, revert on API error

This keeps the zero-external-dependencies spirit of the Go side applied to the frontend. (If native DnD proves too rough on UX, we can evaluate `vue-draggable-plus` which is already used in similar shadcn/vue stacks — flag for CTO.)

### 4.7 Real-time Updates

`KanbanView` subscribes to the existing space SSE stream. On `task_updated` events:
- If card exists in board: update its column/fields in-place
- If task is new: insert into correct column
- If task deleted: remove from board

### 4.8 Filtering

Filter bar at top of board:
- Assignee dropdown → filters cards to show only that agent's tasks
- Label multi-select
- Priority filter

Filters are client-side (all tasks loaded on mount), with debounce. For spaces with >200 tasks, add server-side `?status=&assigned_to=` query params.

---

## 5. Routing

Add to `frontend/src/router/index.ts`:

```typescript
{
  path: '/:space/kanban',
  name: 'kanban',
  component: Empty,
}
```

App.vue reads `route.name === 'kanban'` and renders `KanbanView` in place of `SpaceOverview`.

### 5.1 AppSidebar

Add "Tasks" nav item with `LayoutDashboard` or `KanbanSquare` icon (from lucide-vue-next, already a dependency). Position it between "Overview" and "Conversations" in the sidebar.

---

## 6. Integration with Existing Systems

### 6.1 Hierarchy

- Managers can assign tasks to any agent in their subtree (enforced client-side in the assignee dropdown — only show subtree agents when the actor is not `boss`).
- The Kanban filter can scope to "My Team" using the `/hierarchy` endpoint.
- Task creation in the ignition prompt: managers are instructed to create tasks for their sub-agents via the API rather than sending long message directives.

### 6.2 Agent Protocol

Agents reference tasks in their status updates using `TASK-{id}` notation. The UI detects this pattern in `summary` and `items` fields and renders them as clickable links to the task detail panel.

```bash
# Agent creates a task for itself
curl -X POST http://localhost:8899/spaces/AgentBossDevTeam/tasks \
  -H 'X-Agent-Name: ProtocolMgr' \
  -d '{"title":"Implement webhook retry","assigned_to":"ProtocolDev","priority":"high"}'

# Agent moves task to in_progress
curl -X POST http://localhost:8899/spaces/AgentBossDevTeam/tasks/TASK-003/move \
  -H 'X-Agent-Name: ProtocolDev' \
  -d '{"status":"in_progress"}'
```

The boss.ignite skill can be updated to include current open tasks assigned to the agent in the ignition payload (`GET /spaces/{space}/tasks?assigned_to={name}`), giving agents their workqueue on startup.

### 6.3 Gantt Timeline

The `/history` endpoint already records `StatusSnapshot` per agent. Task status transitions can be appended similarly as `TaskStatusSnapshot` records, allowing the Gantt view to optionally overlay task lifecycle bars on the timeline. This is a stretch goal — the Gantt integration is optional for Phase 1.

### 6.4 Conversations

In `ConversationsView`, when viewing a conversation thread, show a sidebar widget "Tasks" listing open tasks assigned to that agent (fetched from `/spaces/{space}/tasks?assigned_to={name}`). Clicking a task opens `TaskDetailPanel`.

In `AgentDetail.vue`, add a "Tasks" tab alongside existing tabs, listing that agent's open tasks.

---

## 7. Migration Plan

### Phase 1 — Backend (no frontend changes, unblocks agents to use tasks now)

1. Add `TaskStatus`, `TaskPriority`, `Task`, `TaskComment` to `types.go`
2. Add `Tasks map[string]*Task` and `NextTaskSeq int` to `KnowledgeSpace`
3. Implement `handleSpaceTasks` in `server.go` covering all 8 endpoints
4. Add task event types to event journal + replay logic
5. Broadcast `task_updated` SSE events on mutations
6. Write tests: create/list/move/assign/comment, filter query params, SSE broadcast, journal replay (~20 new tests)

### Phase 2 — Frontend Kanban

1. `KanbanView.vue` + `KanbanColumn.vue` + `TaskCard.vue`
2. `TaskDetailPanel.vue` (Sheet-based)
3. `NewTaskDialog.vue`
4. Router route `/:space/kanban`
5. AppSidebar "Tasks" nav item
6. SSE subscription in KanbanView
7. `AgentDetail` Tasks tab

### Phase 3 — Integration Polish

1. `TASK-{id}` pattern detection in agent status/items (auto-link)
2. Boss.ignite task queue injection
3. Conversation view task widget
4. Gantt timeline task overlay (optional)
5. Hierarchy-scoped assignee filtering

---

## 8. Open Questions for CTO [?BOSS]

1. **Cross-space tasks:** Should tasks ever span spaces, or is per-space always correct? (Current design: per-space only.)

2. **ID format:** Human-readable `TASK-001` sequential IDs vs. short UUIDs. Sequential is friendlier in agent logs but requires the server to maintain a counter. Preference?

3. **Drag-and-drop library:** Native HTML5 DnD is zero-dependency but has rough edges on mobile/touch. Should we allow adding `vue-draggable-plus` to the frontend if native DnD proves too clunky in practice?

4. **Agent create authority:** Should any agent be able to CREATE tasks, or only `boss` and managers (role-based)? Current design allows all agents to create tasks (same model as messaging).

5. **Kanban as default view:** Should `/:space` default to the Kanban board instead of the agent overview once tasks are a first-class citizen? Or keep overview as default and tasks as a secondary tab?

---

## 9. Non-Goals

- No external task tracking integration (Jira, Linear) — not in scope
- No deployment automation or CI/CD hooks
- No time tracking or story points
- No email/webhook notifications outside existing SSE+webhook system
- No task dependency graph (blockers are a status, not a DAG)

---

## Appendix: File Change Summary

| File | Change |
|------|--------|
| `internal/coordinator/types.go` | Add Task, TaskComment, TaskStatus, TaskPriority; extend KnowledgeSpace |
| `internal/coordinator/server.go` | Add handleSpaceTasks and sub-handlers; register route; SSE broadcast |
| `internal/coordinator/deck.go` | Extend journal event replay for task events |
| `frontend/src/router/index.ts` | Add `/:space/kanban` route |
| `frontend/src/components/KanbanView.vue` | New — main board |
| `frontend/src/components/KanbanColumn.vue` | New — column |
| `frontend/src/components/TaskCard.vue` | New — card |
| `frontend/src/components/TaskDetailPanel.vue` | New — detail sheet |
| `frontend/src/components/NewTaskDialog.vue` | New — create dialog |
| `frontend/src/components/AppSidebar.vue` | Add Tasks nav item |
| `frontend/src/components/AgentDetail.vue` | Add Tasks tab |
| `frontend/src/components/ConversationsView.vue` | Add task widget per agent |
| `frontend/src/api/index.ts` | Add task API methods |
