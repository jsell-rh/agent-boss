# Organizational Model Spec

**Status:** Draft
**Owner:** ProtoSME (delegated from ProtocolMgr)
**SME Review:** ProtoSME — research complete, hierarchy design cross-referenced with `hierarchy-design.md`

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
CTO blocked   → message Boss    → Boss tags [?BOSS] resolved → CTO unblocks work
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
- Peers coordinate laterally (via direct messages, pre-authorized by manager)

### 5. Context at the edge

Agents closest to the work have the most context. Managers should trust their judgment on implementation details; agents should escalate only genuine blockers, not minor decisions.

---

## Cross-Team Coordination

Different manager domains occasionally need to coordinate. The correct channel is **Manager-to-Manager messaging**, not direct Developer-to-Developer communication.

### Correct Pattern

```
FrontendDev needs a new API field from DataDev:
  1. FrontendDev messages FrontendMgr: "Need DataDev to add X field to /tasks API"
  2. FrontendMgr messages DataMgr: "FrontendMgr → DataMgr: TASK-{n} needs X field added"
  3. DataMgr creates a subtask, assigns to DataDev, sends mission message
  4. DataDev implements and messages DataMgr on completion
  5. DataMgr messages FrontendMgr: "X field available in main"
  6. FrontendMgr messages FrontendDev: "DataDev done, proceed"
```

### Exception: Pre-authorized Peer Coordination

Managers may explicitly authorize direct cross-team communication when:
- The interface is well-defined and low-risk (e.g., agreeing on a JSON field name)
- Both managers approve in their mission messages

Even when authorized, both developers must CC their managers in their next status update.

### API Aid: Subtree Messages

When a Manager needs to broadcast to their entire team:
```bash
POST /spaces/{space}/agent/{Manager}/message?scope=subtree
  X-Agent-Name: {sender}
  {"message": "Team-wide directive: ..."}
```
This delivers to the Manager and all descendants. Fan-out cap: 50 recipients.

### API Aid: Parent Escalation

Workers can escalate without knowing their manager's name:
```bash
POST /spaces/{space}/agent/parent/message
  X-Agent-Name: {WorkerAgent}
  {"message": "[?MANAGER] TASK-{id} blocked: {reason}"}
```
The server resolves `parent` to the caller's declared parent agent.
