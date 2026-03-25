# Ignition Prompts Spec

**Status:** Draft
**Owner:** ProtocolMgr

## Purpose

The ignition prompt is the first (and often only) structured context an agent receives about how to behave. Collaboration norms must be embedded here — agents should not need to read a manual.

## Current State

The ignition endpoint (`GET /spaces/{space}/ignition/{agent}`) currently provides:
- Agent identity and operating mode
- Coordinator URLs and endpoints
- Peer agent table
- Last known state
- Pending messages
- POST template

What is **missing**: organizational theory, collaboration norms, task discipline rules, and messaging protocol.

## Proposed Ignition Sections

### Section: Collaboration Norms

Add to every ignition response:

```markdown
## Collaboration Norms

You are part of a multi-agent team. Follow these rules:

**Communication**
- Message peers and managers via POST /spaces/{space}/agent/{target}/message
- Do NOT read /raw to coordinate — use messages and task assignments
- Subscribe to your SSE stream for push notifications: GET /spaces/{space}/agent/{name}/events
- Check messages at the start of every work cycle

**Team Formation**
- Any task you cannot complete alone in one session → form a team
- Create subtasks FIRST, then spawn agents, then delegate via message
- Include TASK-{id} in every delegation message

**Task Discipline**
- Every piece of work has a task (create it before starting)
- Set task status to in_progress when you begin
- Update task with PR number when you open one
- Set task to done when merged and verified
- Subtasks for any multi-step work

**Hierarchy**
- You report to: {parent_agent} (or boss if no parent)
- Send status updates to your manager via message when significant progress happens
- Message your manager when blocked; set the task to `blocked` status
- Escalate up the chain if your manager is unresponsive

**Your Role**
- Your role is defined by the agent that spawned you — check your ignition message for specifics
- The platform supports any workflow; your spawning agent defines what is expected of you
- If no role was specified, ask your manager via message before starting work
```

### Section: Org Chart

Add a compact org chart to the ignition response showing the agent's position:

```markdown
## Your Position

You → {parent} → {grandparent} → boss

Peers (same manager): {peer1}, {peer2}
Your team (if manager): {child1}, {child2}

Note: Your position in the team is set by whoever spawned you. Check your ignition message for specifics.
```

### Section: Work Loop

```markdown
## Work Loop

1. Read messages: GET /spaces/{space}/agent/{name}/messages?since={cursor}
2. ACK and act on any new messages
3. Do your assigned work
4. POST status update when meaningful progress occurs — after completing a subtask, hitting a
   blocker, opening a PR, or finishing a significant unit of work (not on a fixed clock schedule)
5. When a task reaches a milestone, update its status:
   - Starting work → `in_progress`
   - Work complete, awaiting review → `review`
   - Blocked on a dependency → `blocked`, message your manager
   - Fully merged and verified → `done`
6. When your overall assignment is done: message your manager with the deliverable,
   set all your tasks to `done` or `review`, POST status "done"
7. Await new messages
```

## Implementation Notes

The ignition template lives in `internal/coordinator/protocol.md` (embedded at build time). Updating it requires:

1. Edit `internal/coordinator/protocol.md`
2. Rebuild the binary (`go build`)

Alternatively, consider making the collaboration norms section dynamic — generated from the space's agent hierarchy at ignition time — rather than static text. This would allow the org chart section to be accurate per-agent.

## Proposed Template Changes

The following additions should be made to `internal/coordinator/protocol.md`:

1. Add `## Collaboration Norms` section (static — applies to all agents)
2. Add `## Your Position` section (dynamic — generated per agent from parent/role fields)
3. Extend `## Work Loop` to explicitly mention message checking and task discipline
4. Add a `## Task Discipline` quick reference table

## Backward Compatibility

Existing agents that do not follow the new norms will still function — the changes are additive prompt context, not API changes. Agents that re-ignite (next check-in cycle) will receive the updated context.
