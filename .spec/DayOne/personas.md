# Personas — Reusable Prompt Injections

**TASK-059 | Area: (2) Personas concept**

## Motivation

Today, agent behavior is shaped entirely by the `/boss.ignite` prompt and whatever the manager
types into the session. There is no way to say "this agent should always behave like a senior
Go engineer" without copy-pasting a long system prompt into every agent's initial_prompt.

Personas solve this: a persona is a named, reusable prompt fragment that can be assigned to
one or more agents at creation time and edited independently of any agent.

## Data Model

### Persona

Personas are **global** — stored at the server level, not per-space. This allows the
same persona (e.g., "senior-engineer") to be reused across all projects without duplication.
The UI shows which spaces each persona is used in.

```go
// Persona is a reusable, global prompt injection.
type Persona struct {
    ID          string    `json:"id"`           // slug: "senior-engineer", "go-expert"
    Name        string    `json:"name"`         // display: "Senior Engineer"
    Description string    `json:"description"`  // one-line summary shown in UI
    Prompt      string    `json:"prompt"`       // full text injected before agent initial_prompt
    Version     int       `json:"version"`      // incremented on every edit
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

Personas are stored in a global `personas.json` in `DATA_DIR` (alongside space JSON files).

`AgentConfig` tracks the persona version at assignment time:

```go
type PersonaRef struct {
    ID             string `json:"id"`              // persona slug
    PinnedVersion  int    `json:"pinned_version"`  // version when assigned; stale if < current
}

type AgentConfig struct {
    // ...
    Personas []PersonaRef `json:"personas,omitempty"` // ordered; stale if pinned_version < current
}
```

`AgentConfig.Personas` is an **ordered** list — prompts are injected in that order.

### Persona Versioning

When a persona is edited:
1. `Persona.Version` is incremented
2. `Persona.UpdatedAt` is updated
3. The server computes a staleness flag for any agent whose `PersonaRef.PinnedVersion` is
   less than the current `Persona.Version`

Staleness is surfaced:
- In the agent card: a yellow badge "Persona outdated — restart to apply"
- In the space escalation tray: informational notification (type=fyi)
- Clicking the badge shows a "Restart agent" quick action

Editing a persona does **not** restart running agents automatically.

### Prompt Assembly

When the server builds the initial command to send to a newly spawned session:

```
[persona 1 prompt]

[persona 2 prompt]

[agent initial_prompt]
```

For the tmux backend, this assembled text is what `SendInput` types into the session.
For the ambient backend, it is the `Command` field of `SessionCreateOpts`.

Example assembled bootstrap resource for an agent with personas `["senior-engineer", "go-expert"]`:

```
You are a senior software engineer. You write clean, minimal code with good tests.
Prefer editing existing files over creating new ones. Never over-engineer.

You are an expert Go programmer. Prefer stdlib over external dependencies.
Use table-driven tests. Keep goroutines simple.

You are LifecycleMgr in space AgentBossDevTeam.
Coordinator: http://localhost:8899
[... rest of bootstrap context ...]
```

The persona prompts appear before the agent context — no slash commands.

---

## API

Personas are global — the API is at the server root, not under a space:

| Endpoint | Method | Description |
| -------- | ------ | ----------- |
| `/personas` | GET | List all global personas (with `spaces_used` summary) |
| `/personas` | POST | Create a new persona |
| `/personas/{id}` | GET | Get a single persona |
| `/personas/{id}` | PUT | Replace a persona (increments version) |
| `/personas/{id}` | PATCH | Partial update — name, description, prompt (increments version) |
| `/personas/{id}` | DELETE | Delete persona (error if assigned to any agent across any space) |

### Create Request Body

```json
{
  "id": "senior-engineer",
  "name": "Senior Engineer",
  "description": "Focuses on clean code and minimal changes",
  "prompt": "You are a senior software engineer..."
}
```

### Assign to Agent

Via `AgentConfig` (personas are global — reference by ID):

```json
PATCH /spaces/{space}/agent/{name}/config
{
  "personas": [
    {"id": "senior-engineer"},
    {"id": "go-expert"}
  ]
}
```

The server resolves the current version and stores `pinned_version` automatically.
Or at creation time via `POST /spaces/{space}/agents`.

---

## Frontend

### Global Persona Library

- Accessible from the top navigation: "Personas" link (global, not per-space)
- List view: ID, Name, Description, Version, spaces-used count (with breakdown on hover)
- Click to expand: shows full prompt text and list of agents currently using this persona
- "+ New Persona" button opens an inline editor (name, description, prompt textarea)
- Edit button on each persona card opens the same editor; saving increments version
- On save: a banner shows "3 agents are using an older version of this persona" with a
  "Restart all affected agents" bulk action

### Agent Create / Edit Dialog

- "Personas" multi-select dropdown: shows all global personas
- Ordered list — user can drag to reorder
- Preview button: shows assembled prompt so the user can verify
- Stale persona warning: if a selected persona has a newer version than the agent's
  pinned version, show "Outdated (v2 → v3)" next to the persona name

### Agent Card — Stale Persona Badge

When an agent has at least one stale persona (pinned_version < current version):
- Yellow badge on agent card: "Persona outdated"
- Tooltip: "senior-engineer v2 → v3. Restart to apply."
- Quick action button: "Restart agent"

### Persona Delete Guard

If a persona is assigned to any agent (across all spaces), the delete button shows:
"Used by 3 agents across 2 spaces. Remove assignments first."

---

## Migration

- Existing spaces have no persona assignments — treat as empty, backward compatible
- Existing agents have no `personas` config — treat as empty list, no injection
- Global `DATA_DIR/personas.json` is created fresh on first use
