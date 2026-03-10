# Human Escalation — Replacing the [?BOSS] Pattern

**TASK-059 | Area: Cross-cutting concern — human review/approval**

## Problem with [?BOSS]

The `[?BOSS]` text tag embedded in agent status updates is an antipattern:

1. **Invisible**: it is a string convention, not a first-class system concept — the
   dashboard must special-case it with regex to render it differently
2. **Untracked**: there is no record of which escalations are open, resolved, or ignored
3. **Not actionable**: there is no structured way for the human to reply or approve
4. **Lost in noise**: buried inside a status block that may be long
5. **No urgency signal**: a blocker and a casual question look the same

## Proposed: Human Review Queue

Replace `[?BOSS]` with a structured **Escalation** object that agents post explicitly
and the dashboard surfaces prominently.

---

## Data Model

```go
// EscalationType categorizes what kind of human input is needed.
type EscalationType string

const (
    EscalationQuestion  EscalationType = "question"   // agent needs a decision
    EscalationApproval  EscalationType = "approval"   // agent needs explicit sign-off before proceeding
    EscalationBlocker   EscalationType = "blocker"    // agent is stuck and cannot continue
    EscalationFYI       EscalationType = "fyi"        // informational, no action required
)

// Escalation is a structured request for human attention.
type Escalation struct {
    ID          string         `json:"id"`
    Space       string         `json:"space"`
    Agent       string         `json:"agent"`
    Type        EscalationType `json:"type"`
    Title       string         `json:"title"`        // one-line summary
    Body        string         `json:"body"`         // full context, options, recommendation
    Options     []string       `json:"options,omitempty"` // for "question" type: presented as buttons
    Status      string         `json:"status"`       // "open" | "resolved" | "dismissed"
    Resolution  string         `json:"resolution,omitempty"` // human's response text
    CreatedAt   time.Time      `json:"created_at"`
    ResolvedAt  *time.Time     `json:"resolved_at,omitempty"`
    ResolvedBy  string         `json:"resolved_by,omitempty"` // human's name or "boss"
}
```

`KnowledgeSpace` gains an `Escalations` slice:

```go
type KnowledgeSpace struct {
    // ... existing fields ...
    Escalations []*Escalation `json:"escalations,omitempty"`
}
```

---

## API

| Endpoint | Method | Description |
| -------- | ------ | ----------- |
| `/spaces/{space}/escalations` | GET | List all escalations (filter: `?status=open`) |
| `/spaces/{space}/escalations` | POST | Agent creates an escalation |
| `/spaces/{space}/escalations/{id}` | GET | Get single escalation |
| `/spaces/{space}/escalations/{id}/resolve` | POST | Human resolves with a response |
| `/spaces/{space}/escalations/{id}/dismiss` | POST | Human dismisses without action |

### Agent creates an escalation

```bash
curl -s -X POST http://localhost:8899/spaces/AgentBossDevTeam/escalations \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: LifecycleMgr' \
  -d '{
    "type": "question",
    "title": "Should personas be space-scoped or global?",
    "body": "Current proposal: space-scoped for v1, global import/export for v2. Tradeoff: space-scoped is simpler to implement but prevents sharing personas across projects. Please advise.",
    "options": ["Space-scoped only (simpler)", "Global from day one (more complex)", "Space-scoped v1 with global planned for v2"]
  }'
```

Response:
```json
{
  "id": "esc-001",
  "status": "open",
  "agent": "LifecycleMgr",
  "type": "question",
  "title": "Should personas be space-scoped or global?"
}
```

### Human resolves

```bash
curl -s -X POST http://localhost:8899/spaces/AgentBossDevTeam/escalations/esc-001/resolve \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: boss' \
  -d '{"resolution": "Space-scoped v1. Global sharing is a v2 concern. Proceed."}'
```

The coordinator then:
1. Marks the escalation resolved
2. Delivers a message to the originating agent: "Escalation [esc-001] resolved: Space-scoped v1. Global sharing is a v2 concern. Proceed."

---

## Frontend

### Escalation Tray

A persistent indicator in the top navigation bar:

```
[ Escalations  3 open  ▼ ]
```

Clicking it expands a tray (slide-in panel from the right) listing all open escalations
across all spaces, sorted by type (approval > blocker > question > fyi) then by time.

Each escalation card shows:
- Type badge (colored: red=blocker/approval, yellow=question, gray=fyi)
- Agent name and space
- Title
- Age (e.g. "12 min ago")
- Action buttons:
  - For `question` with options: rendered as buttons, click to resolve
  - For `approval`: [Approve] [Reject] buttons
  - For `blocker`: [Mark Resolved] + text input for guidance
  - For `fyi`: [Dismiss]

### Agent Card Indicator

Agent cards in the space view show a badge when they have open escalations:

```
[ LifecycleMgr  🟡 1 escalation ]
```

Clicking the badge jumps to that escalation in the tray.

### Space View Escalation Section

The space dashboard gains an "Escalations" section above the agent grid, visible only
when there are open escalations. Shows the same cards as the tray, but scoped to
the current space.

---

## Agent Integration

Agents should post escalations proactively rather than embedding questions in their
status update text. The work loop becomes:

1. Identify a decision point
2. `POST /spaces/{space}/escalations` with type, title, body, options
3. Continue working on parts that don't depend on the decision
4. On next `/messages` check: if resolution arrived, apply it and continue

This is more structured than `[?BOSS]` and allows the human to see all open decisions
in one place without reading every agent's status block.

---

## Migration

- Remove `[?BOSS]` rendering from the markdown renderer and dashboard
- Existing status text that contains `[?BOSS]` will render as plain text (harmless)
- No data migration needed — escalations are a new collection

## Agent Protocol Update

The agent protocol doc (`AGENT_PROTOCOL.md`) should be updated to:
- Remove the `[?BOSS]` convention
- Document the escalation API
- Show the work-loop pattern: post escalation → continue working → check resolution

This is a cross-team change that ProtocolMgr/ProtoDev should own.
