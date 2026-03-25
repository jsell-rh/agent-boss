# Organizational Model Spec

**Status:** Draft
**Owner:** ProtoSME (delegated from ProtocolMgr)

## Hierarchy

```
Boss (human)
  └── CTO (top-level AI delegate)
        ├── ProtocolMgr → ProtoDev, ProtoSME
        ├── DataMgr     → DataDev, DataSME
        ├── FrontendMgr → FrontendDev, FrontendSME
        ├── LifecycleMgr → LifecycleDev, LifecycleSME
        └── QAMgr        → QADev, QASME
```

The hierarchy is registered in the coordinator via `parent=` in ignition. Dashboard renders it visually.

> **Note:** This diagram reflects one common pattern (software development teams), not a
> required template. Agent Boss supports any workflow — scientific research, customer support,
> content pipelines, or anything else. Roles and team shapes are defined by the spawning agent
> and the use case, not by the platform. The org structure also changes over time; treat the
> ignition hierarchy as a starting point, not a permanent arrangement.

## Leadership Responsibilities

A **leader** (Manager or CTO) is responsible for:

| Responsibility | Description |
|---------------|-------------|
| Task decomposition | Break parent tasks into concrete subtasks |
| Assignment | Assign subtasks to the right agents |
| Delegation | Spawn team members and send mission messages |
| Tracking | Monitor task status; follow up if stuck |
| Integration | Merge work products, open PRs, report to parent |
| Escalation | Escalate blockers up the chain via messages |

A leader **must not**:
- Implement code directly (that's what Developer agents are for)
- Assign subtasks to themselves unless genuinely the best person for that specific subtask
- Leave assigned tasks in `backlog` status without a message to the assignee

## Delegation Rules

### Rule 1: Delegate everything below the top level

If you are a Manager, your tasks are:
- Define the work and break it into subtasks
- Spawn the team
- Integrate results and open PRs

You do **not** write the code, run the tests, or do the research.

### Rule 2: Delegate with full context

A delegation message must include:
- Task ID
- Branch name
- Specific deliverable (file, test count, endpoint, etc.)
- Any constraints or gotchas known upfront

### Rule 3: Re-delegate when scope changes

If a delegated subtask grows significantly in scope, the Manager must re-evaluate:
- Can the existing agent handle it alone?
- Does this subtask need its own team?

### Rule 4: Check in, don't micromanage

Managers check in on agents via:
- Reading their messages (SSE or cursor-based poll)
- Checking task status on the board
- Sending a check-in message if no update for >30 minutes

Managers do **not** poll `/raw` to observe agent status narratively.

## Escalation Model

```
Agent blocked → message Manager → Manager unresponsive 30m → agent messages CTO
CTO blocked   → message Boss    → Boss resolves → CTO unblocks work
```

Escalations are always via messages, never via status fields.

## Decision Authority

| Decision Type | Authority |
|--------------|-----------|
| Architecture changes | Boss (human) or CTO if clearly delegated |
| API design | Domain Manager + CTO review |
| Implementation choices | Developer (within manager constraints) |
| Priority changes | Boss |
| Agent spawning | Any Manager (within their domain) |
| PR merge | Boss (human) for main; Manager for feature branches |

## Org Theory Principles

These principles, derived from classic organizational theory, are adapted for AI agent teams:

### 1. Span of Control

A manager should directly supervise at most **5 agents**. Beyond that, introduce intermediate managers.

### 2. Single Point of Assignment

Each task has exactly one `assigned_to`. Ambiguous ownership means no one owns it.

### 3. No Orphaned Tasks

Every task must have: an assignee, a parent (or be a root task), and a status that reflects reality. Stale `in_progress` tasks (no update for >1 hour) are flagged for review.

### 4. Information flows up, decisions flow down

- Agents report status up (via messages and status updates)
- Managers send decisions down (via task assignment and messages)
- Peers coordinate laterally (via direct messages — always allowed, no authorization required)

### 5. Context at the edge

Agents closest to the work have the most context. Managers should trust their judgment on implementation details; agents should escalate only genuine blockers, not minor decisions.
