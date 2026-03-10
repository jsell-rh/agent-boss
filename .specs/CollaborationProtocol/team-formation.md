# Team Formation Spec

**Status:** Draft
**Owner:** ProtoSME (delegated from ProtocolMgr)

## Principle: Teams for Non-Trivial Work

Any task that cannot be completed in a single focused session by a single agent is **non-trivial** and must be staffed with a team.

## Definition: Non-Trivial Task

A task is non-trivial if it meets any of these criteria:
- Requires touching more than 2 subsystems (frontend + backend + tests = 3 → non-trivial)
- Has estimated effort > 1 hour of focused work
- Requires specialized knowledge in more than one domain
- Has >3 acceptance criteria
- Is a planning or design task (always non-trivial — requires at least a researcher)

## Required Team Roles

Every team must include:

| Role | Responsibility | Min Count |
|------|---------------|-----------|
| **Manager** | Task decomposition, delegation, integration | 1 |
| **Developer** | Implementation | 1+ |
| **SME/Researcher** | Domain expertise, review, research | 1 (for complex/novel tasks) |

For pure implementation tasks (well-defined, low risk), SME is optional.

## Spawning a Team

### Step 1: Decompose the parent task

Before spawning agents, the manager must:
1. Break the parent task into subtasks (via `POST /spaces/{space}/tasks` with `parent_id`)
2. Assign each subtask to a specific agent role
3. Determine what agent types are needed

### Step 2: Spawn agents via API

```bash
# Spawn a developer agent
POST /spaces/{space}/agent/{AgentName}/spawn
  X-Agent-Name: {Manager}
  {
    "session_name": "{AgentName}",
    "command": "claude --dangerously-skip-permissions"
  }
```

Alternatively (if API spawn not yet supported), spawn via tmux:
```bash
tmux new-session -d -s "{AgentName}" -x 220 -y 50
tmux send-keys -t "{AgentName}" "claude --dangerously-skip-permissions" Enter
sleep 8
tmux send-keys -t "{AgentName}" '/boss.ignite "{AgentName}" "{Space}"' Enter
```

### Step 3: Register hierarchy

Use ignition `?parent={Manager}&role={Role}` to register the agent in the hierarchy:
```
/ignition/{AgentName}?session_id={session}&parent={Manager}&role=Developer
```

### Step 4: Delegate via message

After the agent ignites, send a mission message:
```
Manager → Developer:
  "TASK-{id} assigned: {description}. Branch: {branch}.
   Deliverable: {output_spec}.
   Message me when done or if blocked."
```

## Team Naming Conventions

| Manager | Developer pattern | SME pattern |
|---------|------------------|-------------|
| ProtocolMgr | ProtoDev, ProtoDev2 | ProtoSME |
| DataMgr | DataDev, DataDev2 | DataSME |
| FrontendMgr | FrontendDev | FrontendSME |
| LifecycleMgr | LifecycleDev | LifecycleSME |
| QAMgr | QADev | QASME |

Naming is `{Domain}{Role}` where Role is `Dev`, `Dev2`, `SME`, `Doc`.

## Team Lifecycle

1. **Spawn**: Manager creates session and sends ignite command
2. **Mission**: Manager sends mission message with task ID and deliverable
3. **Work**: Agent works and reports via messages + status updates
4. **Done**: Agent sends `"status": "done"` and messages manager
5. **Teardown**: Manager kills the session and optionally removes from dashboard

```bash
# Teardown
curl -X POST .../agent/{AgentName} -d '{"status": "done", "summary": "..."}'
tmux kill-session -t {AgentName}
curl -X DELETE .../agent/{AgentName} -H 'X-Agent-Name: {Manager}'
```

## Anti-Patterns

| Anti-Pattern | Correct Approach |
|-------------|-----------------|
| Manager implements code directly | Delegate to a Developer agent |
| Solo work on multi-subsystem task | Spawn a team |
| Reuse a "done" agent for new tasks | Spawn a fresh session; or re-ignite |
| Spawn agents without task IDs | Always create tasks first, then spawn |
| Spawn more agents than subtasks | 1 subtask → 1 agent; don't over-staff |
