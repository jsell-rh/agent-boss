# Day One UX — Holistic New-User Experience

**TASK-059 | Area: (5) New-user onboarding**

## Current State

A user who clones Agent Boss and runs `boss serve` sees an empty dashboard.
There is no guidance on:
- What a "space" is and how to create one
- How to add an agent
- What to do after creating an agent
- How to connect to an existing AI session

The user must read the README, the protocol doc, and manually set up symlinks.
This creates a drop-off point before the system demonstrates any value.

## Goals

1. A new user can go from `git clone` to a running agent in under 5 minutes
2. The UI guides the user at each step, not the README
3. Restarting an agent is a one-click operation (not a manual tmux command)
4. Users who know what they are doing are not slowed down by the onboarding

---

## Proposed Changes

### 1. Empty State Guidance

**Space list (empty):**

```
No spaces yet.

A space is a shared coordination board for a team of agents.
Create your first space to get started.

[+ Create Space]  [View documentation]
```

**Space view (no agents):**

```
No agents in this space.

An agent is an AI session connected to this board.
Create an agent to get started, or spawn one from the CLI.

[+ Add Agent]  [See example setup]
```

These are informational empty-state components, not modals. They disappear once content
exists.

### 2. Agent Create Dialog — Full Config

The current create dialog asks only for name and work_dir. The improved dialog
exposes all `AgentConfig` fields in a single form, grouped by section:

**Identity**
- Agent name (required)
- Role (optional, e.g. "Manager", "Developer")
- Parent agent (optional dropdown of existing agents)

**Environment**
- Working directory (file path input, validated client-side)
- Repository URL (optional, used for display and linking)
- Backend (dropdown: tmux / ambient)

**Behavior**
- Personas (multi-select from global persona library)
- Initial prompt (optional textarea — plain text instructions, no slash commands)
  - Shows assembled preview below the textarea (persona prompts + initial_prompt)

**Advanced**
- Launch command (default: `claude`; user types the full command if they want flags)
- "Skip permission prompts" checkbox — when checked, appends `--dangerously-skip-permissions`
  to the launch command. **Unchecked by default.** Displays a warning:
  > "This allows the agent to run tools without asking for confirmation. Only enable if you
  > trust this agent's behavior."

**Actions** (at bottom)
- [Create] — creates agent record, does not spawn
- [Create and Spawn] — creates and immediately spawns the session

### 3. First-Run Setup Wizard

On first visit to a fresh installation (no spaces, no data), show a one-time setup guide.
This is a multi-step modal:

**Step 1: Welcome**
> Agent Boss coordinates AI agents working on your project. Let's set up your first space.

**Step 2: Create a space**
- Space name input (pre-filled with "MyProject")
- [Create space and continue →]

**Step 3: Add your first agent**
- Mini version of the Agent Create dialog (name, work_dir, backend)
- [Create agent and continue →]

**Step 4: Spawn the agent**
> Your agent is ready. Click Spawn to start a tmux session with Claude.
> Claude will connect to this board automatically.

- [Spawn agent] button — calls `POST /spaces/{space}/agent/{name}/spawn`
- Shows live status: "Starting session... waiting for agent..."
- SSE-driven: transitions to "Agent is active" when agent first POSTs status

**Step 5: Done**
> Agent {name} is running. You can now assign tasks, send messages, and monitor progress.

[Go to dashboard →]

The wizard state is stored in `localStorage`. Users can skip it at any step.

### 4. Restart Controls

The current stop/restart flow requires CLI commands. The dashboard gains restart
controls at both the individual and fleet level.

#### Individual Agent Restart

- Located in the agent card action menu (three-dot menu)
- Calls `POST /spaces/{space}/agent/{name}/restart`
- Shows a spinner while the session restarts
- SSE-driven: card updates when the agent re-connects
- Uses stored `AgentConfig` — no need to remember the work_dir or command

#### Restart All Agents (Fleet Restart)

A "Restart all" button in the space header action bar:

- Calls `POST /spaces/{space}/restart-all` (new bulk endpoint)
- Restarts all agents with status `active`, `idle`, or `done` that have a registered session
- Sequenced with a 2s delay between each restart to avoid overwhelming the system
- Progress shown in a banner: "Restarting 4 agents... (2/4 complete)"
- Useful after a coordinator restart or major configuration change

The bulk endpoint is also useful for applying updated personas to all agents at once.

### 5. Setup Checklist (persistent, dismissible)

A dismissible banner in the space view for new spaces (< 24 hours old, < 3 agents):

```
Getting started checklist:
[ ] Add your first agent
[ ] Set a working directory for the agent
[ ] Spawn the agent session
[ ] Assign the agent a task
[Dismiss]
```

Items check off automatically as they are completed (via state observation).

### 6. CLI Quick-Start Command

Add `boss init` CLI command that automates the manual setup steps:

```
boss init [space-name]
```

This command:
1. Creates the space (if it does not exist)
2. Registers the boss MCP server with Claude (`claude mcp add boss-mcp ...`)
3. Prints the URL to open in a browser
4. Offers to open it automatically (`--open` flag)

Output:
```
Space "MyProject" created.
MCP server registered: boss-mcp → http://localhost:8899/mcp
Open http://localhost:8899/spaces/MyProject/ to manage your agents.
```

Note: `boss init` does **not** create a "boss" agent — the human operator is a first-class
citizen in the system, not an agent record. The dashboard already has a dedicated human
interface. A "boss" agent entry is only needed if the human wants a named channel on the
blackboard, which is optional and not part of init.

### 7. Boss as First-Class Citizen

The human operator ("boss") is not an agent — they are the dashboard user. The system
distinguishes between:

- **Agents**: AI sessions that POST status updates, receive messages, and act on tasks
- **Boss (human)**: the person viewing the dashboard, assigning tasks, reviewing escalations,
  sending messages, and making decisions

The boss does not need a tmux session or a spawned AI. Their "presence" in the system is:
- The dashboard UI itself (the boss IS the dashboard)
- The `created_by: "boss"` field on tasks and comments
- The `resolved_by: "boss"` field on escalations
- Messages sent via the dashboard message composer (which identifies them as "boss")

If a team wants a dedicated human-readable blackboard entry for coordination notes, they
can create a space with a static agent called "boss" that only receives messages — but
this is optional and not enforced by `boss init`.

---

## UX Anti-Patterns to Avoid

| Anti-pattern | Mitigation |
| ------------ | ---------- |
| Wizard fatigue (too many steps) | Wizard is optional — users can skip to blank dashboard |
| Empty state paralysis | Every empty state has exactly one primary action |
| Modal overload | Agent create uses a slide-over panel, not a blocking modal |
| Stale docs | All guidance text lives in the frontend, not in a separate wiki |

---

## Metrics for Success

- Time from `boss serve` to first agent active: target < 5 minutes for new users
- Fraction of new spaces that have at least one agent spawned: target > 80%
- Restart operations done from dashboard vs. CLI: target > 50% dashboard

These are design targets, not production metrics. Measurement requires usage telemetry
which is out of scope for this spec.

---

## Open Questions

- **Wizard vs. inline guidance**: Boss resolved: inline empty states preferred.
  Spec updated — wizard is optional/secondary, inline empty states are primary.

- **`boss init` scope**: Boss resolved: get away from slash command dependency entirely.
  `boss init` now registers the MCP server with Claude (`claude mcp add`) instead of
  setting up the `./commands/` directory.
