# Gap Analysis — Current API vs. Required Capabilities

**Status:** Draft (to be expanded by ProtoDev with TASK-062)
**Owner:** ProtoDev (research) / ProtocolMgr (integration)

## Current Messaging API

The current system provides:

| Endpoint | Method | Purpose | Status |
|----------|--------|---------|--------|
| `/spaces/{space}/agent/{name}/message` | POST | Send message to agent | ✅ Exists |
| `/spaces/{space}/agent/{name}/messages` | GET | Poll messages (cursor) | ✅ Exists |
| `/spaces/{space}/agent/{name}/messages/{id}/ack` | POST | ACK a message | ✅ Exists |
| `/spaces/{space}/agent/{name}/events` | GET SSE | Push notification stream | ✅ Exists |

## Current Task API

| Endpoint | Method | Purpose | Status |
|----------|--------|---------|--------|
| `/spaces/{space}/tasks` | POST | Create task | ✅ Exists |
| `/spaces/{space}/tasks` | GET | List tasks (filterable) | ✅ Exists |
| `/spaces/{space}/tasks/{id}` | GET | Get task | ✅ Exists |
| `/spaces/{space}/tasks/{id}` | PATCH | Update task (status, pr, etc.) | ✅ Exists |

## Current Ignition API

| Endpoint | Method | Purpose | Status |
|----------|--------|---------|--------|
| `/spaces/{space}/ignition/{agent}` | GET | Get ignition prompt + register session | ✅ Exists |
| `?parent=X&role=Y` | query params | Register hierarchy | ✅ Exists |

## Current Agent Lifecycle API

| Endpoint | Method | Purpose | Status |
|----------|--------|---------|--------|
| `/spaces/{space}/agent/{name}/spawn` | POST | Spawn agent via tmux | ✅ Exists |
| `/spaces/{space}/agent/{name}/stop` | POST | Stop agent session | ✅ Exists |

## Gap Analysis

### GAP-1: No API-based sub-agent spawning for non-tmux agents

**Current:** Spawn only works for tmux sessions on the local machine.
**Required:** A manager running in Docker or CI should be able to spawn sub-agents via the API.
**Proposal:** Extend `/spawn` to accept a `mode` field:
- `mode: "tmux"` — current behavior (local tmux)
- `mode: "http"` — registers an agent slot; caller is responsible for starting the process
- `mode: "webhook"` — send webhook to an orchestration system that starts the agent

### GAP-2: No sub-agent spawn with pre-loaded mission

**Current:** Spawn creates a session; agent must receive mission via separate message.
**Required:** Option to pre-load a mission message that the agent sees on first ignition.
**Proposal:** Add `initial_message` field to spawn request body. Server queues it before agent ignites.

### GAP-3: Ignition prompt lacks collaboration norms

**Current:** Ignition provides identity, peers, and protocol basics.
**Required:** Agents need org theory, task discipline, and messaging rules baked in.
**Proposal:** Extend `protocol.md` template per `ignition-prompts.md` spec.

### GAP-4: No task-to-agent spawn link

**Current:** Tasks and agent spawning are independent.
**Required:** Spawned agents should have task IDs pre-associated.
**Proposal:** Add optional `task_id` field to spawn request; server sets `assigned_to` automatically.

### GAP-5: No stale task detection

**Current:** Tasks can stay `in_progress` indefinitely with no update.
**Required:** Flagging mechanism for stale tasks (no update > 1 hour).
**Proposal:** Dashboard visual indicator; optional SSE event when a task becomes stale.

### GAP-6: No bulk task update on agent done

**Current:** Agent must manually PATCH each subtask to `done`.
**Required:** When an agent posts `"status": "done"`, optionally cascade to their assigned tasks.
**Proposal:** Query param `?close_tasks=true` on agent status POST; server PATCHes all `in_progress` tasks for that agent.

## Priority

| Gap | Priority | Effort |
|-----|----------|--------|
| GAP-3 (ignition norms) | High | Low — text change only |
| GAP-1 (API spawn modes) | Medium | Medium |
| GAP-2 (pre-loaded mission) | Medium | Low |
| GAP-5 (stale tasks) | Medium | Low |
| GAP-4 (task-spawn link) | Low | Low |
| GAP-6 (bulk done) | Low | Medium |

## Note for ProtoDev

Please expand this document after auditing `internal/coordinator/server.go`, `protocol.go`, and `types.go`. Confirm or correct the "Status" column above, and add any additional gaps found in the implementation.
