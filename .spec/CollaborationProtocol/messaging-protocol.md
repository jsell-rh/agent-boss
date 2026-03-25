# Messaging Protocol Spec

**Status:** Draft
**Owner:** ProtoSME (delegated from ProtocolMgr)

## Principle: Messaging-Only Inter-Agent Communication

Agents **must not** use `/raw` or `/spaces/{space}/agent/{name}` GET endpoints as their primary coordination mechanism. All inter-agent communication happens through the messaging API.

### Why

- `/raw` returns the full space state — O(agents × message_history) context cost
- Reading `/raw` to check peer status is polling, not collaboration
- Messages are push-based, targeted, and structured — they express intent

## The Messaging API (Existing)

```
POST /spaces/{space}/agent/{target}/message
  X-Agent-Name: {sender}
  {"message": "..."}
  → {"messageId": "...", "recipient": "...", "status": "delivered"}

GET /spaces/{space}/agent/{name}/messages?since={cursor}
  → {"agent": "...", "cursor": "...", "messages": [...]}

GET /spaces/{space}/agent/{name}/events  (SSE)
  → text/event-stream with message and keepalive events
```

## Prescribed Communication Patterns

### 1. Task Assignment (Manager → Developer)

When a manager delegates a task:
```
Manager POSTs /tasks to create a task with assigned_to=Developer
Manager sends message to Developer:
  "TASK-{id} assigned: {description}. Branch: {branch}. Deliverable: {output}. Message me when done."
```

### 2. Task Status Update (Developer → Manager)

When a developer completes work or needs to report:
```
Developer sends message to Manager:
  "{AgentName}: TASK-{id} complete. {summary}. Commit: {hash}. Ready for review."
Developer updates task status via PATCH /spaces/{space}/tasks/{id}
```

### 3. Question / Blocker (Any → Manager)

When work is blocked on a decision:
```
Developer sets task status to `blocked`
Developer sends message to Manager:
  "TASK-{id} blocked: {reason}. Waiting on your decision. Continuing with {alternative} in the meantime."
Developer posts status update with next_steps reflecting the blocker and the alternative
```

If the manager is unresponsive and the blocker is critical, the developer escalates up the chain — see
the Escalation section below.

### 4. Peer-to-Peer Coordination

Agents may message any peer directly — no authorization required. Direct peer messaging is always
allowed.

```
DevA sends message to DevB:
  "DevA → DevB: re TASK-{id}: {coordination detail}"
```

Peer exchanges that affect shared state or deliverables should be summarized in the next status
update so the manager has visibility.

### 5. Escalation

If work is blocked and the manager is unresponsive after multiple check cycles, escalate up the
chain — message the manager's manager (or the operator if no higher level exists):
```
Agent sends message to {ManagersManager}:
  "TASK-{id} blocked on {blocker}. {ManagerName} has not responded. Requesting escalation."
```

## Message Discipline

- **Every message must be actionable** — no status messages that duplicate what the dashboard shows
- **Reference tasks by ID** — always include TASK-{id} in messages about work
- **One thread per task** — messages about a task are exchanges between the assigned agent and the assigning manager; avoid forwarding chains
- **Acknowledgment** — ACK a message after reading it; ACK signals "I have seen this" and serves as a lightweight reaction (thumbs-up) for informational messages. It is not a commitment gate — you can ACK and then continue your current work

## Reading Messages

Agents must check messages via:
1. **SSE stream** (preferred): `GET /spaces/{space}/agent/{name}/events` — push, no polling
2. **Cursor-based poll** (fallback): `GET /spaces/{space}/agent/{name}/messages?since={cursor}`

Agents **must not** scan `/raw` to check if they have messages. The `/messages` endpoint with cursor is O(new_messages), not O(space_state).

## Message Retention and ACK

- Messages are retained until explicitly ACK'd
- ACK via: `POST /spaces/{space}/agent/{name}/messages/{id}/ack`
- Unread messages appear in `/ignition` response under `Pending Messages`
- Agents must ACK messages after acting on them (prevents re-delivery confusion)

## Prohibited Patterns

| Pattern | Instead |
|---------|---------|
| Read `/raw` to see what peers are doing | Message them directly or check tasks |
| Repeat status in messages | Messages convey intent and blockers; status goes in POST |
| Send a message without acting | ACK → act → report |
| Leave messages unread | ACK after acting |
