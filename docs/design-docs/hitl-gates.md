# Human-in-the-Loop (HITL) Gates

**Status:** Draft v2 — updated per PR #296 review feedback
**Author:** croche
**Date:** 2025-03-25 (v2: 2026-03-26)

---

## 1. Problem Statement

OpenDispatch coordinates autonomous AI agents that run in tmux or ambient sessions. Today, human oversight is fragmented across several partially-connected subsystems, and critical safety guardrails are missing entirely.

### 1.1 No configurable safety guardrails

Agents can spawn new agents, restart or stop peers, and close tasks — all without operator approval. There is no way to declare "this agent must get approval before deploying" or "tasks in this space cannot move to done without sign-off." All human interaction is ad-hoc: agents choose when to ask, and humans react. Nothing is enforced at the infrastructure level.

### 1.2 Decision response flow is fragile

When an agent calls `request_decision`, the question is delivered to the operator's message inbox as a decision-type `AgentMessage`. The human replies via `ConversationsView.vue`, and the backend calls `backend.SendInput()` to paste raw text into the agent's tmux pane. The agent has no structured way to discover whether its decision was answered — it must parse tmux output or hope to find the reply in `check_messages`. This is unreliable and incompatible with the ambient backend.

### 1.3 Tool approval has no deny path

The liveness loop detects Claude Code permission prompts via heuristic tmux pane parsing (`parseApprovalFromLines` in `tmux.go`). The human clicks "Approve" in the dashboard, which sends `"1"` + `Enter` to the tmux pane. There is no way to deny a tool request, no reason capture, and no audit trail beyond the interrupt ledger.

### 1.4 No timeout or escalation

Decision requests and approval prompts sit indefinitely when the operator doesn't respond. There are no timeouts, no escalation chains, and no auto-resolution policies.

### 1.5 Interrupt ledger and message system are disconnected

Questions from agents create both an `Interrupt` record (in `interrupts.go`) and a decision `AgentMessage` (in the operator's inbox), but resolving one doesn't reliably resolve the other. The `InterruptTracker.vue` and `ConversationsView.vue` exist as separate UI surfaces that sometimes show the same underlying request.

---

## 2. Design Principles

1. **Opt-in by default.** Gates are never active unless explicitly configured via HITL policy. Agents without policies behave exactly as today. Zero breaking changes.

2. **Non-blocking MCP tools.** MCP tools always return immediately with a gate ID. Resolution is delivered via message + nudge (the established pattern). `check_gate` is available as a fallback for restart recovery and explicit state queries.

3. **Backend-agnostic.** All HITL interactions flow through MCP tools and the HTTP API. The system works identically whether agents run in tmux or ambient sessions.

4. **Build on existing infrastructure.** Reuse the interrupt ledger patterns, the message system, the journal, and the SSE pipeline. No new persistence backends or external dependencies.

5. **Progressive disclosure.** One line in fleet YAML enables a simple gate. Complex policies (per-agent escalation chains, timeout actions) are achievable without changing the simple-case surface.

6. **Auditable.** Every gate request, resolution, timeout, and escalation is recorded in SQLite with timestamps, actor identity, and context.

---

## 3. Concepts

### 3.1 Gate

A **Gate** is a checkpoint that blocks a specific action until a human (or timeout policy) resolves it. Gates are the single unified primitive for all HITL interactions, replacing the current fragmented system of interrupts, decision messages, and approval parsing.

Every gate has:
- A **type** (what triggered it)
- A **status** (pending → approved / denied / timed_out)
- A **question** (human-readable description of what needs deciding)
- A **context** (structured metadata: branch, PR, task ID, etc.)
- A **resolution** (who resolved it, their response, when, how long it took)

### 3.2 HITL Policy

A **HITL Policy** is a configuration object that declares which actions require gates. Policies are attached at two levels:

- **Space-level** — applies to all agents in the space
- **Agent-level** — overrides or extends the space-level policy for a specific agent

When an action occurs, the system checks the effective policy (agent-level merged over space-level). If a matching rule exists, a gate is created and the action is held until resolved.

---

## 4. Gate Types

### 4.1 Lifecycle Gates

Require operator approval before an agent lifecycle operation executes.

| Action | When gate fires |
|--------|----------------|
| `spawn` | Agent calls `spawn_agent` MCP tool and the target agent has `require_approval.spawn: true` |
| `restart` | Agent calls `restart_agent` and target has `require_approval.restart: true` |
| `stop` | Agent calls `stop_agent` and target has `require_approval.stop: true` |

**Why this matters:** Without lifecycle gates, any agent can self-replicate by spawning children, or take down peers by calling `stop_agent`. In production deployments, operators need to approve topology changes.

**Behavior when gate fires:**
1. MCP tool returns immediately: `{ gate_id: "gate_...", status: "pending_approval", message: "Spawn of agent X requires operator approval. You will receive a message when resolved." }`
2. The lifecycle action is NOT executed.
3. When the operator approves, the action is executed server-side (spawn/restart/stop proceeds). A message is delivered to the requesting agent's inbox and a nudge is fired.
4. When the operator denies, the action is cancelled. A message with the denial reason is delivered to the requesting agent's inbox and a nudge is fired.

### 4.2 Task Transition Gates

Require operator approval before a task moves between specific statuses.

Configured as rules: `{ from: "review", to: "done", require: "approval" }`. When an agent calls `move_task` and the transition matches a rule in the effective HITL policy, a gate is created.

**Why this matters:** Tasks moving to `done` may represent deployments, releases, or completed deliverables. Operators need sign-off before work is considered finished.

**Behavior when gate fires:**
1. `move_task` returns: `{ gate_id: "gate_...", status: "pending_approval", task_id: "TASK-42", message: "Moving TASK-42 from review to done requires approval. You will receive a message when resolved." }`
2. The task remains in its current status.
3. On approval, the task is moved server-side. A message is delivered to the requesting agent: `"Gate gate_abc resolved: TASK-42 has been moved to done (approved by operator)."` A nudge is fired.
4. On denial, the task stays put. A message with the denial reason is delivered to the requesting agent and a nudge is fired.

### 4.3 Decision Gates

Structured request→resolve→deliver flow replacing the fragile `request_decision` → tmux paste loop.

An agent calls `request_gate` with a question. The operator answers via the dashboard. The resolution is delivered as a message to the agent's inbox with a nudge, so the agent picks it up in its next check-in cycle.

**Why this matters:** The current decision flow pastes raw text into a tmux pane. The agent has no reliable way to receive the answer. Decision gates give agents structured resolution delivered through the proven message + nudge system.

**Behavior:**
1. Agent calls `request_gate(question: "Should we use PostgreSQL or MySQL?", context: "...")`
2. Returns: `{ gate_id: "gate_...", status: "pending" }`
3. Agent continues working on other tasks.
4. Operator answers in dashboard with resolution text.
5. Server delivers a message to the agent's inbox: `"Gate gate_abc resolved (approved): Use PostgreSQL — we have existing infrastructure."` A nudge is fired.
6. Agent reads the resolution in its next `check_messages` cycle (triggered by nudge).
7. Optionally, agent can call `check_gate(gate_id)` for the full structured response (useful after restart or for explicit state queries).

### 4.4 Tool Approval Gates (Upgrade)

The existing tool approval system (tmux pane parsing → approve button) is modeled as a gate to gain:
- **Deny path** — operator can reject a tool call with a rationale
- **Reason capture** — operator can explain why they approved or denied
- **Audit trail** — every tool approval/denial is a gate record in SQLite

**Behavior change from current system:**
- Liveness loop still detects `NeedsApproval` via `parseApprovalFromLines` (no change to detection)
- Instead of only recording an `Interrupt`, also creates a `Gate` with `type: "tool_approval"`
- Dashboard shows approve AND deny buttons with reason textarea
- Resolution reason is captured on both approve and deny

**Deny behavior:**
- On deny, the denial rationale is delivered as a message to the agent's inbox: `"Tool [Bash] denied by operator. Reason: [rationale]."` A nudge is fired so the agent picks up the message promptly.
- The dashboard includes an optional **"Also interrupt agent"** checkbox. When checked, the server sends Escape to the session after delivering the message, cancelling the pending tool call.
- Default (checkbox unchecked): the agent receives the rationale and decides how to proceed. The tmux tool prompt remains pending — the agent can cancel it or take another action.
- On the ambient backend, Escape is not applicable — the message-based path is the only option.

This approach keeps the agent operational and informed rather than stopping it without context.

---

## 5. Data Model

### 5.1 Gate Entity

```go
type GateType string

const (
    GateDecision       GateType = "decision"
    GateTaskTransition GateType = "task_transition"
    GateLifecycle      GateType = "lifecycle"
    GateToolApproval   GateType = "tool_approval"
)

type GateStatus string

const (
    GatePending  GateStatus = "pending"
    GateApproved GateStatus = "approved"
    GateDenied   GateStatus = "denied"
    GateTimedOut GateStatus = "timed_out"
    GateExpired  GateStatus = "expired"   // superseded by newer request
)

type Gate struct {
    ID        string            `json:"id"`          // "gate_<ulid>" (ULID avoids collision under concurrent requests)
    Space     string            `json:"space"`
    Agent     string            `json:"agent"`       // requesting agent
    Type      GateType          `json:"type"`
    Status    GateStatus        `json:"status"`

    // Request
    Question  string            `json:"question"`
    Context   map[string]string `json:"context,omitempty"`

    // Task transition fields
    TaskID     string `json:"task_id,omitempty"`
    FromStatus string `json:"from_status,omitempty"`
    ToStatus   string `json:"to_status,omitempty"`

    // Lifecycle fields
    Action      string `json:"action,omitempty"`       // "spawn", "restart", "stop"
    TargetAgent string `json:"target_agent,omitempty"`

    // Tool approval fields
    ToolName   string `json:"tool_name,omitempty"`
    PromptText string `json:"prompt_text,omitempty"`

    // Resolution
    ResolvedBy string     `json:"resolved_by,omitempty"` // "operator", "timeout", agent name
    Resolution string     `json:"resolution,omitempty"`
    ResolvedAt *time.Time `json:"resolved_at,omitempty"`

    // Timeout policy (populated from HITL policy at creation time)
    TimeoutSec    int    `json:"timeout_sec,omitempty"`     // 0 = no timeout
    TimeoutAction string `json:"timeout_action,omitempty"`  // "approve", "deny", "escalate"
    EscalateTo    string `json:"escalate_to,omitempty"`

    // Timestamps
    CreatedAt time.Time  `json:"created_at"`
    ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
```

### 5.2 HITL Policy

```go
type HITLPolicy struct {
    // Task transition gates
    TaskGates []TaskGateRule `json:"task_gates,omitempty" yaml:"task_gates,omitempty"`

    // Lifecycle gates
    RequireApproval struct {
        Spawn   bool `json:"spawn,omitempty"   yaml:"spawn,omitempty"`
        Restart bool `json:"restart,omitempty" yaml:"restart,omitempty"`
        Stop    bool `json:"stop,omitempty"    yaml:"stop,omitempty"`
    } `json:"require_approval,omitempty" yaml:"require_approval,omitempty"`

    // Timeout defaults (can be overridden per request_gate call)
    DefaultTimeoutSec    int    `json:"default_timeout_sec,omitempty"    yaml:"default_timeout_sec,omitempty"`
    DefaultTimeoutAction string `json:"default_timeout_action,omitempty" yaml:"default_timeout_action,omitempty"`
    // "approve" | "deny" | "escalate"
}

type TaskGateRule struct {
    From    string `json:"from" yaml:"from"`       // source task status
    To      string `json:"to"   yaml:"to"`         // target task status
    Require string `json:"require" yaml:"require"` // "approval"
}
```

**Policy resolution order:**
1. Check agent-level `HITLPolicy` (stored in `AgentConfig`)
2. Fall back to space-level `HITLPolicy` (stored on `KnowledgeSpace`)
3. If neither defines a rule for the action, no gate is created

### 5.3 Database Schema

New table:

```sql
CREATE TABLE gates (
    id             TEXT PRIMARY KEY NOT NULL,
    space_name     TEXT NOT NULL,
    agent          TEXT NOT NULL,
    type           TEXT NOT NULL,     -- 'decision', 'task_transition', 'lifecycle', 'tool_approval'
    status         TEXT NOT NULL DEFAULT 'pending',
    question       TEXT,
    context        TEXT,              -- JSON map[string]string
    task_id        TEXT,
    from_status    TEXT,
    to_status      TEXT,
    action         TEXT,              -- 'spawn', 'restart', 'stop'
    target_agent   TEXT,
    tool_name      TEXT,
    prompt_text    TEXT,
    resolved_by    TEXT,
    resolution     TEXT,
    resolved_at    DATETIME,
    timeout_sec    INTEGER DEFAULT 0,
    timeout_action TEXT,
    escalate_to    TEXT,
    created_at     DATETIME NOT NULL,
    expires_at     DATETIME
);

CREATE INDEX idx_gates_space_status ON gates(space_name, status);
CREATE INDEX idx_gates_space_agent  ON gates(space_name, agent, status);
```

Modified tables:

```sql
-- Add HITL policy column to spaces table (JSON text)
ALTER TABLE spaces ADD COLUMN hitl_policy TEXT DEFAULT '';
```

Agent-level `HITLPolicy` lives inside the existing `agents.config` JSON column (extending the `AgentConfig` struct). No schema change needed.

**Space deletion:** When a space is deleted, pending gates must be cascade-deleted or explicitly cancelled. The `space_name` column should reference the `spaces` table with `ON DELETE CASCADE`, or the space delete handler must cancel all pending gates before teardown.

### 5.4 Relationship to Existing Interrupt Ledger

The `gates` table coexists with the `interrupts` table. Existing code paths that record interrupts continue to work unchanged. New HITL flows use gates exclusively.

Migration path:
- **Phase 1 (this spec):** Both tables operate in parallel. `request_decision` creates a gate AND an interrupt for backward compat. When a gate supersedes an interrupt record (e.g., a decision gate resolves), the corresponding interrupt is marked resolved server-side so `InterruptTracker.vue` doesn't show duplicate pending items.
- **Phase 2 (future):** Deprecate interrupt ledger. Migrate historical data. Remove `InterruptTracker.vue` in favor of unified gates panel.

---

## 6. MCP Tools

### 6.1 New: `request_gate`

Agent requests a human decision.

```
Parameters:
  space:        string  (required)  — workspace name
  agent:        string  (required)  — requesting agent name
  question:     string  (required)  — human-readable question
  context:      string  (optional)  — additional context for the operator
  timeout_sec:  number  (optional)  — override default timeout (0 = no timeout)

Returns:
  gate_id:           string  — unique gate ID
  status:            string  — always "pending"
  poll_interval_sec: number  — suggested poll cadence (default: 15)
  message:           string  — instructions for the agent
```

The `poll_interval_sec` hint tells agents the right cadence without requiring them to read the spec. The primary resolution path is message + nudge; `check_gate` is a fallback.

### 6.2 New: `check_gate`

Query tool for gate state. Primarily used for restart recovery and explicit state verification — resolution is delivered via message + nudge.

```
Parameters:
  space:    string  (required)
  agent:    string  (required)
  gate_id:  string  (required)

Returns:
  gate_id:      string  — echo back
  status:       string  — "pending", "approved", "denied", "timed_out"
  resolution:   string  — human's response text (empty if still pending)
  resolved_by:  string  — who resolved: "operator", "timeout", agent name
  resolved_at:  string  — RFC 3339 timestamp (empty if still pending)
  wait_seconds: number  — seconds the gate has been/was pending
```

### 6.3 New: `list_gates`

Agent lists its gates (useful for recovery after restart).

```
Parameters:
  space:   string  (required)
  agent:   string  (required)
  status:  string  (optional)  — filter: "pending", "approved", "denied", "all" (default: "pending")

Returns:
  gates:  Gate[]
  total:  number
```

### 6.4 Modified: `move_task`

When a task transition matches a `TaskGateRule` in the effective HITL policy:

```
Current behavior (no gate): moves task, returns success.

New behavior (gate configured):
  Returns:
    gate_id:  string
    status:   "pending_approval"
    task_id:  string
    message:  "Task TASK-42 transition review→done requires operator approval.
               You will receive a message when resolved."
```

The task remains in its current status until the gate is resolved. On approval, the server moves the task and delivers a message to the agent. On denial, the task stays and the denial reason is delivered as a message. Both trigger a nudge.

### 6.5 Modified: `spawn_agent` / `restart_agent` / `stop_agent`

When the target agent has lifecycle gates configured:

```
Returns:
  gate_id:  string
  status:   "pending_approval"
  target:   string  — target agent name
  action:   string  — "spawn", "restart", or "stop"
  message:  "Spawning agent X requires operator approval.
             You will receive a message when resolved."
```

The lifecycle action is NOT executed until the gate is approved. On approval, the server executes the action (spawn/restart/stop) and delivers a confirmation message to the requesting agent with a nudge. On denial, the action is cancelled and the denial reason is delivered as a message with a nudge.

### 6.6 Preserved: `request_decision`

Backward compatible. Internally creates a decision gate and returns the gate_id in its response alongside the existing text response. Agents that already use `request_decision` continue to work. New agents should prefer `request_gate`.

```
Existing response (preserved):
  "Decision request sent (id: 123456789). The operator will reply via conversations view."

Extended response (added):
  gate_id: "gate_..."  — can be used with check_gate for structured polling
```

---

## 7. HTTP API

### 7.1 Gates CRUD

```
GET  /spaces/{space}/gates
     Query params: status, type, agent, limit, offset
     Returns: { gates: Gate[], total: number }

GET  /spaces/{space}/gates/{id}
     Returns: Gate

POST /spaces/{space}/gates/{id}/resolve
     Body: {
       "action":     "approve" | "deny",
       "resolution":  "reason text",
       "resolved_by": "operator"
     }
     Returns: { ok: true, gate: Gate }

POST /spaces/{space}/gates/batch-resolve
     Body: {
       "gate_ids":   ["gate_1", "gate_2"],
       "action":     "approve" | "deny",
       "resolution":  "batch approval reason"
     }
     Returns: { ok: true, resolved: number, gates: Gate[] }

GET  /spaces/{space}/gates/metrics
     Returns: {
       total: number,
       pending: number,
       approved: number,
       denied: number,
       timed_out: number,
       avg_wait_seconds: number,
       by_type: { decision: N, lifecycle: N, ... },
       by_agent: { agent1: N, agent2: N, ... }
     }
```

### 7.2 HITL Policy

```
GET  /spaces/{space}/hitl-policy
     Returns: HITLPolicy (effective space-level policy)

PUT  /spaces/{space}/hitl-policy
     Body: HITLPolicy
     Returns: { ok: true }

GET  /spaces/{space}/agent/{name}/hitl-policy
     Returns: HITLPolicy (agent-level override, empty if not set)

PUT  /spaces/{space}/agent/{name}/hitl-policy
     Body: HITLPolicy
     Returns: { ok: true }
```

### 7.3 SSE Events

```
gate_created:    { space, agent, gate_id, type, question, context }
gate_resolved:   { space, agent, gate_id, type, status, resolved_by, resolution }
gate_timed_out:  { space, agent, gate_id, type, timeout_action }
gate_escalated:  { space, agent, gate_id, escalate_to }
```

---

## 8. Agent-Side Flow

### 8.1 Decision Gate Sequence

```
Agent                          Coordinator                    Operator
  │                                │                             │
  │── request_gate(question) ─────>│                             │
  │<─ { gate_id, status:pending } ─│                             │
  │                                │── SSE gate_created ────────>│
  │   (continue other work)        │                             │
  │                                │                             │
  │                                │<── POST resolve(approve) ──│
  │                                │── SSE gate_resolved ──────>│
  │                                │── deliver message to agent  │
  │                                │── nudge agent session       │
  │                                │                             │
  │   (nudge triggers check-in)    │                             │
  │── check_messages ─────────────>│                             │
  │<─ "Gate resolved: Go ahead" ──│                             │
  │                                │                             │
  │   (act on resolution)          │                             │
```

After restart, the agent can call `check_gate(gate_id)` to recover the full structured response.

### 8.2 Task Gate Sequence

```
Agent                          Coordinator                    Operator
  │                                │                             │
  │── move_task(review→done) ─────>│                             │
  │                                │── check HITL policy         │
  │                                │── create gate               │
  │<─ { gate_id,                   │                             │
  │     status:pending_approval } ─│                             │
  │                                │── SSE gate_created ────────>│
  │   (continue other work)        │                             │
  │                                │<── POST resolve(approve) ──│
  │                                │── move task server-side     │
  │                                │── deliver message to agent  │
  │                                │── nudge agent session       │
  │                                │── SSE gate_resolved ──────>│
  │                                │                             │
  │   (nudge triggers check-in)    │                             │
  │── check_messages ─────────────>│                             │
  │<─ "TASK-42 moved to done" ────│                             │
```

### 8.3 Lifecycle Gate Sequence

```
Agent                          Coordinator                    Operator
  │                                │                             │
  │── spawn_agent("worker-3") ────>│                             │
  │                                │── check HITL policy         │
  │                                │── create gate               │
  │<─ { gate_id,                   │                             │
  │     status:pending_approval } ─│                             │
  │                                │── SSE gate_created ────────>│
  │                                │                             │
  │                                │<── POST resolve(approve) ──│
  │                                │── execute spawn             │
  │                                │── deliver message to agent  │
  │                                │── nudge agent session       │
  │                                │── SSE gate_resolved ──────>│
  │                                │                             │
  │   (nudge triggers check-in)    │                             │
  │── check_messages ─────────────>│                             │
  │<─ "worker-3 spawned (approved)"│                             │
```

### 8.4 Blocking Behavior

MCP tools **never block**. They always return immediately. When a gate is created:

- **Decision gates:** `request_gate` returns `gate_id`. Agent continues working on other tasks. Resolution arrives as a message via the existing nudge system.
- **Task transition gates:** `move_task` returns `gate_id` instead of moving the task. Agent continues working. Confirmation arrives as a message.
- **Lifecycle gates:** `spawn_agent`/`restart_agent`/`stop_agent` return `gate_id` instead of executing. Agent continues working. Confirmation arrives as a message.

**Resolution delivery is push, not pull.** When a gate resolves, the coordinator delivers a message to the requesting agent's inbox and fires a nudge. The agent reads the resolution in its next `check_messages` cycle (triggered by the nudge). This follows the same established pattern used for all other inter-agent communication.

`check_gate` remains available as a query tool for agents that want to explicitly verify gate state, but it is not the primary resolution mechanism.

### 8.5 Post-Restart Recovery

When an agent restarts, it can call `list_gates(status="all")` to discover gates it created before the restart. If a gate was resolved while the agent was down, it gets the resolution immediately. If still pending, the agent will receive the resolution via message + nudge when the operator acts.

---

## 9. Operator-Side Flow

### 9.1 Dashboard: Approvals Tab

A new tab alongside the existing "Conversations" tab, showing all pending gates across all agents.

**Layout:**
- Sorted by urgency (oldest first, with urgency color coding)
- Each gate shows: type badge, agent name, question/context, waiting time
- Approve / Deny buttons with optional reason textarea
- Batch actions: "Approve all selected" / "Deny all selected"
- Resolved gates shown below with resolution details and wait time

**Urgency coloring:**
- Red border: pending > 15 minutes
- Amber border: pending > 5 minutes
- Default: pending < 5 minutes

**Type-specific rendering:**

| Gate type | Shows |
|-----------|-------|
| `decision` | Question text, agent context (branch, PR, phase) |
| `task_transition` | Task ID + title, from→to status, requesting agent |
| `lifecycle` | Action (spawn/restart/stop), target agent, requesting agent |
| `tool_approval` | Tool name (Bash/Read/Write/etc.), truncated prompt text |

### 9.2 ConversationsView Integration

Decision gates also create a decision-type `AgentMessage` in the operator's inbox (backward compatible). When a gate is resolved via the Approvals tab, the corresponding message is marked `resolved`. When a decision message is replied to via ConversationsView, the corresponding gate is also resolved.

This dual-surface approach ensures backward compatibility while giving operators a dedicated approval workflow.

### 9.3 Notifications

Pending gates contribute to:
- The space's `attention_count` (shown on the space card)
- SSE `gate_created` events (dashboard updates in real-time)
- Browser notifications if the operator has granted permission (future)

### 9.4 Space Summary

The space overview shows a pending gates count alongside the existing agent count and attention count.

---

## 10. Fleet YAML Integration

### 10.1 Schema

```yaml
version: "1"

space:
  name: "Production Deploy"
  hitl:
    task_gates:
      - from: review
        to: done
        require: approval
    default_timeout_sec: 900            # 15 minutes
    default_timeout_action: escalate

agents:
  deployer:
    role: worker
    work_dir: /workspace
    hitl:
      require_approval:
        spawn: true
      task_gates:
        - from: in_progress
          to: done
          require: approval
      default_timeout_sec: 600
      default_timeout_action: deny

  reviewer:
    role: worker
    work_dir: /workspace
    # No hitl section — inherits space-level policy only
```

### 10.2 FleetFile Struct Extension

```go
type FleetSpace struct {
    Name            string      `yaml:"name"`
    Description     string      `yaml:"description,omitempty"`
    SharedContracts string      `yaml:"shared_contracts,omitempty"`
    HITL            *HITLPolicy `yaml:"hitl,omitempty"`
}

type FleetAgent struct {
    // ... existing fields ...
    HITL *HITLPolicy `yaml:"hitl,omitempty"`
}
```

### 10.3 Import Behavior

On `odis import`:
- Space-level HITL policy is applied via `PUT /spaces/{space}/hitl-policy`
- Agent-level HITL policies are included in the agent config upsert
- Dry-run output shows HITL policy changes:

```
Space "Production Deploy":
  HITL policy: task gate review→done requires approval (NEW)
  HITL policy: default timeout 900s, action: escalate (NEW)

Agent "deployer":
  HITL policy: require approval for spawn (NEW)
  HITL policy: task gate in_progress→done requires approval (NEW)
```

### 10.4 Validation

`ParseAndValidateFleetFile` validates:
- `task_gates[].from` and `task_gates[].to` are valid `TaskStatus` values
- `task_gates[].require` is `"approval"` (only supported value for now)
- `default_timeout_action` is one of `"approve"`, `"deny"`, `"escalate"`, or empty
- `default_timeout_sec` is non-negative

---

## 11. Timeout and Escalation

### 11.1 Timeout Loop

A new periodic check runs alongside the existing liveness loop:

```go
func (s *Server) gateTimeoutLoop() {
    ticker := time.NewTicker(30 * time.Second)
    for {
        select {
        case <-s.stopGateLoop:  // dedicated channel — gate timeouts and liveness are separate concerns
            return
        case <-ticker.C:
            s.checkGateTimeouts()
        }
    }
}
```

`checkGateTimeouts` queries all pending gates where `expires_at < now()`:

| `timeout_action` | Behavior |
|---|---|
| `"approve"` | Auto-approve the gate. `resolved_by: "timeout"`. Execute the gated action. |
| `"deny"` | Auto-deny the gate. `resolved_by: "timeout"`. Cancel the gated action. |
| `"escalate"` | Deliver urgent message to `escalate_to` (defaults to operator). Extend `expires_at` by `timeout_sec`. |
| `""` (empty) | No timeout. Gate waits indefinitely. |

### 11.2 Escalation

When `timeout_action` is `"escalate"`:
1. Gate's `expires_at` is extended by `timeout_sec` (gives the escalation target time to respond).
2. An urgent message is delivered to `escalate_to`: "Gate [gate_id] for [agent] has not been resolved after [wait_time]. Original question: [question]."
3. SSE `gate_escalated` event is broadcast.
4. If the extended timeout also expires, the gate is auto-denied (prevents infinite escalation loops).
5. Escalation depth is limited to 1 level.

---

## 12. Backward Compatibility

### 12.1 Guarantees

- All existing MCP tools work unchanged when no HITL policy is configured.
- `request_decision` is preserved. It internally creates a decision gate and includes `gate_id` in its response. Agents that already use `request_decision` continue to work without changes.
- The `interrupts` table is not modified or removed. New gates use the `gates` table. Both coexist.
- Decision messages are still created in the operator inbox for `ConversationsView` compatibility.
- `InterruptTracker.vue` continues to function alongside the new Approvals tab.

### 12.2 Ignition Prompt

When a HITL policy is active for an agent, the ignition prompt (built by `buildIgnitionText`) should include a section informing the agent about active gates:

```markdown
## HITL Policy (Active)

This workspace has human-in-the-loop gates configured:
- Task transitions review→done require operator approval
- Spawning new agents requires operator approval

When an action requires approval, the MCP tool returns a `gate_id` with
status `pending_approval`. **Do not poll for the resolution.** Continue
working on other tasks. When the gate resolves, you will receive a
message with the outcome — treat it like any other inbound message and
act accordingly. Use `check_gate` only to verify state after a restart.
```

Auto-inject this section only when `HITLPolicy` is non-empty to avoid noisy ignition prompts for unconfigured spaces.

---

## 13. Implementation Phases

### Phase 1a: Gate Infrastructure
- `Gate` type and `GateStatus` enum in `types.go`; gate IDs use ULID format
- `GateRecord` GORM model in `db/models.go` with space cascade handling
- Gate repository methods (save, load, resolve, list, metrics) — follow `interrupts.go` pattern
- Gate HTTP endpoints (CRUD + resolve + batch-resolve + metrics)
- On resolve: deliver message to requesting agent's inbox + fire nudge
- SSE events for gates
- Journal event types

### Phase 1b: MCP Tools + Decision Gates
- `request_gate`, `check_gate`, `list_gates` MCP tools in `mcp_tools.go`
- Wire `request_decision` to create a gate alongside the interrupt
- Ensure `check_gate` returns structured resolution

### Phase 1c: HITL Policy + Task Transition Gates
- `HITLPolicy` type in `types.go`
- Space-level and agent-level policy storage and resolution
- Modify `move_task` in `mcp_tools.go` to check policy
- Policy HTTP endpoints
- Gate auto-execution on approval (move the task server-side)

### Phase 1d: Lifecycle Gates
- Modify `spawn_agent`, `restart_agent`, `stop_agent` in `mcp_tools.go`
- Gate auto-execution on approval (execute spawn/restart/stop server-side)

### Phase 1e: Tool Approval Upgrade
- Liveness loop creates `GateToolApproval` gates alongside interrupts
- Deny path: deliver rationale as message + nudge; optional "interrupt agent" sends Escape
- Reason capture on approve/deny
- Mark corresponding interrupt as resolved when gate resolves (dedup)

### Phase 1f: Dashboard + Fleet YAML
- Approvals tab in frontend (follow `InterruptTracker.vue` patterns)
- Fleet YAML schema extension in `fleet.go`
- Validation in `ParseAndValidateFleetFile`
- Dry-run output for HITL policy changes

### Phase 1g: Timeout Loop
- `gateTimeoutLoop` alongside existing liveness loop
- Escalation message delivery
- Auto-approve / auto-deny on timeout

---

## 14. Open Questions

### Resolved (per PR #296 review)

1. **~~Should gate resolution nudge the agent?~~** **Yes.** On gate resolution, deliver a message to the requesting agent's inbox and fire the existing nudge mechanism. This makes resolution push-based (reliable) rather than poll-based (fragile). `check_gate` remains as a fallback for restart recovery and explicit state queries. *(Resolved based on reviewer feedback that polling is unreliable — agents get busy, restart, lose polling loops.)*

2. **~~Should tool approval deny send Escape or just record the denial?~~** **Message by default, optional interrupt.** On deny, deliver the rationale as a message to the agent. The dashboard includes an optional "Also interrupt agent" checkbox that sends Escape when checked. This keeps the agent informed and operational. On the ambient backend, Escape is not applicable — message is the only path. *(Resolved based on reviewer feedback that Escape stops the agent without context.)*

### Still Open

3. **Should gates support multiple approvers?** (e.g., 2-of-3 designated approvers required). Recommendation: not in Phase 1. Start with single-approver. The data model supports adding an `approvals []GateApproval` relation later without breaking `resolved_by`.

4. **How should the ignition prompt reference HITL?** Brief summary (as shown in §12.2) auto-injected only when `HITLPolicy` is non-empty. Full details available via `boss://protocol` MCP resource or a new `boss://hitl-policy/{space}` resource.

5. **Should denied lifecycle gates be retryable?** Recommendation: yes — a new gate is created on each request. The denied gate remains in the audit trail.

6. **Should the Approvals tab replace InterruptTracker.vue?** Recommendation: not immediately. Run both in parallel during Phase 1. When gates supersede an interrupt, mark the interrupt resolved server-side to avoid duplicates. Unify in Phase 2.

---

## 15. Key Files

| File | Role |
|------|------|
| `internal/coordinator/types.go` | `Gate`, `GateStatus`, `GateType`, `HITLPolicy` types |
| `internal/coordinator/db/models.go` | `GateRecord` GORM model |
| `internal/coordinator/interrupts.go` | Pattern to follow for gate ledger |
| `internal/coordinator/mcp_tools.go` | `request_gate`, `check_gate`, `list_gates`; modify `move_task`, `spawn_agent`, `restart_agent`, `stop_agent` |
| `internal/coordinator/lifecycle.go` | Lifecycle gate checks before spawn/restart/stop execution |
| `internal/coordinator/handlers_task.go` | Task gate checks before `move_task` execution |
| `internal/coordinator/liveness.go` | Gate timeout loop; tool approval gate creation |
| `internal/coordinator/server.go` | Gate timeout loop startup |
| `cmd/boss/fleet.go` | Fleet YAML schema extension |
| `frontend/src/components/InterruptTracker.vue` | Pattern for Approvals tab |
| `frontend/src/components/ConversationsView.vue` | Decision gate message integration |
