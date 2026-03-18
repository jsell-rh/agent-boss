# Software Factory: Autonomous Multi-Agent Production

## Vision

OpenDispatch today is a coordination blackboard with a human in the loop. The human (Boss) reads the dashboard, answers `[?BOSS]` questions, approves tool use, unblocks stalled agents, and decides merge order. Every one of these touchpoints is an **interrupt** — a moment where production stops waiting for a human.

The software factory pattern eliminates interrupts progressively until the system runs as a **dark factory**: spec in, deployed software out, no humans on the floor.

## The Interrupt Problem

Looking at the live `sdk-backend-replacement` workspace, we can observe the following interrupt categories:

| Category | Example from Dashboard | Current Handler |
|----------|----------------------|-----------------|
| **Decision requests** | FE: "Should I rebase #640 or start fresh?" | Human reads inbox, posts answer |
| **Approval gates** | Tool-use popups (Bash, Edit) requiring "y" | Human clicks Approve in dashboard |
| **Pipeline stalls** | BE: "Pipeline appears stalled: should CP wait?" | Human reads, issues directive |
| **Dependency questions** | CP: "Should CP test against SDK PR #746 before merge?" | Human decides sequencing |
| **Status staleness** | 7 of 11 agents showing STALE | Human broadcasts check-in |

Every interrupt has a **cost**: context-switch time for the human, idle compute for the agent, and stalled downstream agents waiting on the blocked one. In the current workspace, the Inbox shows 7 pending questions — each one a production stoppage.

## Interrupt Taxonomy

To reduce interrupts, we must first classify them. Each interrupt type has a different automation path.

### Type 1: Tool Approval Interrupts

**What**: Claude Code asks permission to run Bash commands, edit files, etc.
**Frequency**: Highest — dozens per agent per session.
**Current state**: Dashboard polls tmux sessions every 2s, shows approval popup, human clicks Approve.
**Automation path**: Configure Claude Code's `allowedTools` and `permissions` in `.claude/settings.json` per agent. Agents working in isolated worktrees can safely auto-approve file edits and builds within their scope. Tool approval becomes a project configuration problem, not a runtime decision.
**Residual risk**: Destructive commands outside the agent's scope. Mitigate with filesystem sandboxing (worktree-scoped permissions).

### Type 2: Decision Interrupts

**What**: Agent doesn't know which path to take and asks `[?BOSS]`.
**Frequency**: Medium — a few per phase transition.
**Examples**: "Rebase or start fresh?", "Which branch?", "Wait or proceed independently?"
**Automation path**: Encode decisions as **factory rules** in the pipeline spec. The Overlord agent reads the spec and makes these decisions autonomously based on predefined policies. Decisions that can't be pre-encoded escalate to the human, but the system learns from each escalation (see Interrupt Learning below).

### Type 3: Sequencing Interrupts

**What**: Agent doesn't know if it's their turn to work.
**Frequency**: Medium — happens at every stage boundary.
**Examples**: "Should CP test against SDK PR #746 before it merges?"
**Automation path**: The factory pipeline defines explicit stage gates and triggers. Agents watch the blackboard for upstream `gate: pass` signals. No human needed to say "go" — the pipeline topology encodes the sequencing.

### Type 4: Staleness Interrupts

**What**: Agents go idle or lose context, requiring human to broadcast check-ins.
**Frequency**: Low but costly — each stale agent requires reignition.
**Automation path**: Heartbeat protocol with automatic escalation. If an agent doesn't post within N minutes, the coordinator auto-broadcasts a check-in. If the agent doesn't respond, the coordinator logs a stale event and the Overlord reassigns or restarts.

### Type 5: Review Interrupts

**What**: Code review and quality gates requiring human judgment.
**Frequency**: Once per agent per stage.
**Automation path**: Deterministic gates (build, test, lint) run automatically. LLM-based review by the Reviewer agent handles code quality. Human review becomes optional at higher autonomy levels.

## Architecture: Factory-Aware OpenDispatch

### New Data Model: Factory Plan

The factory plan replaces the static Inbox with a live dependency graph. The plan is a first-class object in the coordinator, persisted alongside the space JSON.

```json
{
  "factory": {
    "spec_name": "Workflow",
    "spec_hash": "sha256:abc123...",
    "autonomy_level": 2,
    "created_at": "2026-03-01T...",
    
    "stages": [
      {
        "id": 1,
        "name": "CRD Definition",
        "agent": "API",
        "depends_on": [],
        "status": "completed",
        "gate": "pass",
        "started_at": "...",
        "completed_at": "...",
        "interrupts": []
      },
      {
        "id": 2,
        "name": "API Plugin",
        "agent": "API",
        "depends_on": [1],
        "status": "in-review",
        "gate": "pending",
        "started_at": "...",
        "interrupts": [
          {
            "type": "decision",
            "question": "Should GORM model use soft deletes?",
            "resolved_by": "overlord",
            "resolution": "Yes, use gorm.DeletedAt for all Kinds",
            "resolved_at": "...",
            "human_required": false
          }
        ]
      },
      {
        "id": 3,
        "name": "Operator Reconciler",
        "agent": "Operator",
        "depends_on": [1],
        "status": "working",
        "gate": "n/a",
        "parallel_with": [2, 4]
      }
    ],
    
    "metrics": {
      "total_interrupts": 14,
      "human_interrupts": 3,
      "auto_resolved_interrupts": 11,
      "interrupt_rate": 0.21,
      "stage_durations": {"1": 420, "2": 1800},
      "total_elapsed": 7200,
      "total_idle": 2400
    }
  }
}
```

### Interrupt Tracking

Every interrupt is recorded with structured metadata. This is the foundation for measuring factory efficiency and training the system to reduce interrupts over time.

```json
{
  "id": "int_001",
  "timestamp": "2026-03-01T14:22:00Z",
  "type": "decision|approval|sequencing|staleness|review",
  "agent": "FE",
  "stage": 7,
  "question": "Should I rebase #640 or start fresh?",
  "context": {
    "branch": "feat/frontend_to_api",
    "pr": "#640",
    "days_stale": 13,
    "upstream_changes": 47
  },
  "resolution": {
    "resolved_by": "human|overlord|policy|timeout",
    "answer": "Abandon #640, start fresh from main with SDK",
    "resolved_at": "2026-03-01T14:25:00Z",
    "wait_duration_seconds": 180
  },
  "learnable": true,
  "pattern": "stale-pr-rebase-vs-fresh"
}
```

### Interrupt Metrics API

New endpoints on the coordinator:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/spaces/{space}/factory` | GET | Current factory plan with all stages and metrics |
| `/spaces/{space}/factory` | POST | Submit a new factory plan (spec → stages) |
| `/spaces/{space}/factory/interrupts` | GET | All interrupts for the current factory run |
| `/spaces/{space}/factory/metrics` | GET | Aggregated interrupt metrics over time |
| `/spaces/{space}/factory/metrics/history` | GET | Historical metrics across multiple factory runs |

### Dashboard: Factory View

The Inbox card transforms into a **Factory Pipeline** view:

```
┌─────────────────────────────────────────────────────────────┐
│  FACTORY PIPELINE: Workflow Kind                            │
│  Autonomy: L2 (Supervised)  Interrupts: 3/14 human         │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ✅ CRD ──┬──► ✅ API ──► 🔄 SDK ──┬──► ⏳ CLI            │
│           │                       ├──► ⏳ FE              │
│           ├──► ✅ Operator ───────┤                        │
│           └──► 🔄 Backend ────────┴──► ⏳ CP              │
│                                                             │
│  Stage 3/8 · 2h elapsed · 40min idle · 3 human interrupts  │
├─────────────────────────────────────────────────────────────┤
│  RECENT INTERRUPTS                                          │
│  🟡 FE  [decision] "Rebase or fresh?" → PENDING 3m         │
│  🟢 API [approval] Bash: go test → AUTO-APPROVED           │
│  🟢 SDK [sequencing] Wait for API gate → AUTO-RESOLVED     │
└─────────────────────────────────────────────────────────────┘
```

Key differences from current Inbox:
- Shows the **dependency graph** visually, not just a flat list of questions
- Each stage shows its gate status (pass/fail/pending)
- Interrupts are categorized and show whether they were resolved by human or machine
- Aggregate metrics are always visible: interrupt count, idle time, elapsed time

## Autonomy Levels

The factory operates at progressive autonomy levels. Each level removes human checkpoints.

| Level | Name | Human Does | System Does | Interrupt Target |
|-------|------|-----------|-------------|------------------|
| **L0** | Manual | Everything | Blackboard display only | N/A (current state without factory) |
| **L1** | Assisted | Writes spec, reviews PRs, approves tools, answers questions | Agents implement, coordinator tracks | Track all interrupts |
| **L2** | Supervised | Writes spec, answers novel questions only | Auto-approve tools, auto-sequence stages, Reviewer gates, Overlord decisions from policy | < 5 human interrupts per factory run |
| **L3** | Approved | Writes spec, reviews final E2E result | Full pipeline including merges, rebase cascade | < 1 human interrupt per factory run |
| **L4** | Dark | Submits spec via API, walks away | Everything: implement, review, merge, deploy, validate | 0 human interrupts |

### Current State Assessment

The `sdk-backend-replacement` workspace is operating at approximately **L0.5** — the blackboard exists and agents coordinate through it, but every stage transition requires human intervention. The dashboard shows 7 `[?BOSS]` questions pending and multiple stale agents, indicating the human is the bottleneck.

### Path to L1

L1 requires no new code beyond interrupt tracking. The value is **measurement**: once we count interrupts, we can see which types dominate and target them for automation.

Requirements:
- [ ] Interrupt data model in `types.go`
- [ ] Interrupt recording in POST handler (when agent posts a `[?BOSS]` question)
- [ ] Interrupt resolution tracking (when human/overlord answers)
- [ ] `/factory/metrics` endpoint
- [ ] Dashboard interrupt counter

### Path to L2

L2 requires the Overlord agent to make decisions autonomously using factory rules.

Requirements:
- [ ] Factory plan data model and persistence
- [ ] Stage/gate tracking in coordinator
- [ ] Overlord reads factory plan and issues directives without human input
- [ ] Tool approval auto-resolve for scoped operations (per-agent `.claude/settings.json`)
- [ ] Staleness auto-broadcast (coordinator heartbeat, no human trigger)
- [ ] Decision policy engine: Overlord consults factory rules before escalating to `[?BOSS]`
- [ ] Interrupt classification: system auto-tags each interrupt with its type
- [ ] Dashboard factory pipeline view

### Path to L3

L3 requires automated merging and rebase cascading.

Requirements:
- [ ] Auto-merge on Reviewer gate pass (configurable per autonomy level)
- [ ] Rebase cascade: when upstream PR merges, coordinator triggers downstream rebase
- [ ] Retry budget: configurable attempts before escalation
- [ ] Rollback: Cluster agent reverts on E2E failure
- [ ] Pipeline idempotency: any stage can be re-run safely

### Path to L4

L4 requires a spec parser and code generation templates.

Requirements:
- [ ] Kind spec YAML parser in coordinator
- [ ] Spec-to-work-order translation (Overlord or dedicated parser)
- [ ] Per-component code generation templates (reduce LLM reasoning to template-filling)
- [ ] Automated E2E validation with acceptance criteria from spec
- [ ] Zero-human CI/CD pipeline integration

## Interrupt Learning

The most valuable long-term feature is **interrupt learning**: the system observes which interrupts required human resolution, what the human decided, and whether similar situations recur. Over time, the system builds a decision library.

### Learning Loop

```
1. Agent posts [?BOSS] question
2. Coordinator records interrupt with context
3. Human (or Overlord) resolves with answer
4. System records {question_pattern, context, answer} tuple
5. Next time a similar question arises:
   a. Coordinator checks decision library
   b. If match found with high confidence → auto-resolve
   c. If no match → escalate to human, record new pattern
6. Interrupt rate decreases over time
```

### Decision Library Schema

```json
{
  "patterns": [
    {
      "id": "stale-pr-strategy",
      "trigger": "PR is stale > 7 days AND upstream has > 20 new commits",
      "question_regex": "(rebase|abandon|start fresh|cherry-pick)",
      "default_answer": "Abandon stale PR, create fresh branch from main",
      "confidence": 0.85,
      "times_applied": 3,
      "last_override": null
    },
    {
      "id": "wait-vs-proceed",
      "trigger": "Agent blocked on upstream stage AND upstream is active",
      "question_regex": "(wait|proceed independently|should .* wait)",
      "default_answer": "Wait for upstream gate pass, do not proceed independently",
      "confidence": 0.92,
      "times_applied": 7,
      "last_override": "2026-02-28T..."
    }
  ]
}
```

### Measuring Factory Value

The core metric is **interrupts per factory run** over time:

```
Factory Run #1 (Workflow Kind):     14 interrupts, 8 human
Factory Run #2 (Task Kind):         11 interrupts, 4 human  ← learned from Run #1
Factory Run #3 (Agent Kind):         7 interrupts, 2 human  ← learned from Run #1, #2
Factory Run #4 (Skill Kind):         4 interrupts, 1 human  ← approaching L3
Factory Run #5 (WorkflowTask Kind):  2 interrupts, 0 human  ← dark factory achieved
```

Secondary metrics:
- **Idle time ratio**: time agents spent waiting / total elapsed time
- **Stage duration**: how long each pipeline stage takes
- **Gate failure rate**: how often Reviewer rejects and agents retry
- **Auto-resolution rate**: interrupts resolved without human / total interrupts
- **Time-to-resolution**: how long each interrupt waited before resolution

These metrics should be visible in the dashboard and queryable via API, enabling trend analysis across factory runs.

## Integration with Existing OpenDispatch

The factory pattern is an **overlay** on the existing blackboard, not a replacement. Spaces that don't use the factory pattern continue to work exactly as they do today. The factory adds:

1. **Factory plan** stored alongside space JSON (`{space}.factory.json`)
2. **Interrupt log** appended to on every `[?BOSS]` question and resolution
3. **Stage/gate fields** in `AgentUpdate` (optional, backward-compatible)
4. **Factory view** in dashboard (new card replacing/augmenting Inbox)
5. **Metrics endpoints** for historical analysis
6. **Overlord policy engine** consuming factory rules to auto-resolve decisions

### Agent Update Extension

```json
{
  "status": "active",
  "summary": "API: implementing Workflow plugin",
  "branch": "feat/api-workflow",
  "pr": "#750",
  "test_count": 65,
  
  "factory": {
    "stage": 2,
    "gate": "pending",
    "ready_for_cherry_pick": false,
    "downstream_artifacts": [],
    "upstream_cherry_picks": [
      {"from_agent": "CRD", "commit_sha": "abc1234"}
    ]
  }
}
```

The `factory` field is optional. Agents that don't include it are treated as non-factory agents (backward compatible with current behavior).

## Implementation Phases

### Phase 1: Measurement (L1)

Add interrupt tracking to the existing coordinator. No behavioral changes — just record what's happening.

- Interrupt data model
- Record `[?BOSS]` questions as interrupts
- Record resolutions (human answers via dashboard or blackboard)
- Interrupt metrics endpoint
- Dashboard interrupt counter (badge on Inbox card)
- Historical interrupt log viewable in dashboard

### Phase 2: Factory Plan (L2 foundation)

Add the factory plan data model and pipeline visualization.

- Factory plan JSON schema and persistence
- Stage/gate tracking
- Factory pipeline view in dashboard (dependency graph)
- Overlord reads factory plan for sequencing decisions
- Auto-broadcast on staleness (coordinator heartbeat)
- Tool approval policies per agent

### Phase 3: Autonomous Overlord (L2)

Give the Overlord agent the ability to make decisions without human input.

- Decision policy engine
- Overlord consults factory rules before escalating
- Auto-resolve sequencing interrupts from pipeline topology
- Auto-resolve decision interrupts from decision library
- Interrupt learning: record patterns, suggest auto-resolutions
- Retry budget with configurable escalation threshold

### Phase 4: Merge Automation (L3)

Automate the merge cascade.

- Auto-merge on Reviewer gate pass
- Rebase cascade triggered by upstream merge events
- Rollback on E2E failure
- Pipeline idempotency

### Phase 5: Spec-Driven Generation (L4)

Full dark factory.

- Kind spec YAML parser
- Spec-to-work-order translation
- Per-component code generation templates
- Automated E2E acceptance criteria
- Zero-human pipeline

## Relationship to Ambient Platform

The `sdk-backend-replacement` workspace is the proving ground. The Ambient platform's component dependency tree (CRD → API → SDK → CLI/FE → CP → Cluster) is the canonical factory pipeline. Every new Kind follows the same 8-step cascade documented in the Ambient `software-factory.md`.

OpenDispatch becomes the **factory controller** — the system that:
1. Accepts a Kind spec
2. Decomposes it into staged work orders
3. Dispatches work to agents via the blackboard
4. Tracks progress through stage gates
5. Records and learns from interrupts
6. Measures factory efficiency over time
7. Progressively eliminates human touchpoints

The Ambient platform benefits directly: each new Kind (Workflow, Task, Skill, Agent, etc.) runs through the factory faster than the last, because the interrupt library grows and the automation deepens.

## Success Criteria

| Metric | L0 (Today) | L1 Target | L2 Target | L4 Target |
|--------|-----------|-----------|-----------|-----------|
| Human interrupts per Kind | ~20+ (unmeasured) | Measured | < 5 | 0 |
| Idle time ratio | ~40% (estimated) | Measured | < 20% | < 5% |
| Time to deploy a new Kind | Days | Days (measured) | Hours | Minutes |
| Auto-resolution rate | 0% | 0% (measuring only) | > 70% | 100% |
| Stale agent incidents | Common | Measured | Rare (auto-broadcast) | None (heartbeat) |
