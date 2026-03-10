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

Personas are stored at the **space level** (not global) to keep configuration local and
exportable.

```go
// Persona is a reusable prompt injection for an agent.
type Persona struct {
    ID          string    `json:"id"`           // slug: "senior-engineer", "go-expert"
    Name        string    `json:"name"`         // display: "Senior Engineer"
    Description string    `json:"description"`  // one-line summary shown in UI
    Prompt      string    `json:"prompt"`       // full text injected before initial_prompt
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

`KnowledgeSpace` gains a `Personas` map:

```go
type KnowledgeSpace struct {
    // ... existing fields ...
    Personas map[string]*Persona `json:"personas,omitempty"` // keyed by Persona.ID
}
```

`AgentConfig.PersonaIDs` is an **ordered** list — prompts are injected in that order.

### Prompt Assembly

When the server builds the initial command to send to a newly spawned session:

```
[persona 1 prompt]

[persona 2 prompt]

[agent initial_prompt]
```

For the tmux backend, this assembled text is what `SendInput` types into the session.
For the ambient backend, it is the `Command` field of `SessionCreateOpts`.

Example assembled prompt for an agent with `persona_ids: ["senior-engineer", "go-expert"]`:

```
You are a senior software engineer. You write clean, minimal code with good tests.
Prefer editing existing files over creating new ones. Never over-engineer.

You are an expert Go programmer. Prefer stdlib over external dependencies.
Use table-driven tests. Keep goroutines simple.

/boss.ignite "LifecycleMgr" "AgentBossDevTeam"
```

---

## API

| Endpoint | Method | Description |
| -------- | ------ | ----------- |
| `/spaces/{space}/personas` | GET | List all personas in the space |
| `/spaces/{space}/personas` | POST | Create a new persona |
| `/spaces/{space}/personas/{id}` | GET | Get a single persona |
| `/spaces/{space}/personas/{id}` | PUT | Replace a persona |
| `/spaces/{space}/personas/{id}` | PATCH | Partial update (name, description, prompt) |
| `/spaces/{space}/personas/{id}` | DELETE | Delete persona (error if assigned to any agent) |

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

Via `AgentConfig`:

```json
PATCH /spaces/{space}/agent/{name}/config
{
  "persona_ids": ["senior-engineer", "go-expert"]
}
```

Or at creation time via `POST /spaces/{space}/agents`.

---

## Frontend

### Persona Library Panel

- Accessible from the space sidebar: "Personas" section (below Agents)
- List view: ID, Name, Description, assigned-to count
- Click to expand: shows full prompt text
- "+ New Persona" button opens an inline editor (name, description, prompt textarea)
- Edit button on each persona card opens the same editor

### Agent Create / Edit Dialog

- "Personas" multi-select dropdown: shows all personas in the space
- Ordered list — user can drag to reorder
- Preview button: shows assembled prompt so the user can verify

### Persona Delete Guard

If a persona is assigned to any agent, the delete button is disabled with a tooltip:
"Assigned to 3 agents. Remove from those agents first."

---

## Migration

- Existing spaces have no `personas` map — treat as empty, backward compatible
- Existing agents have no `persona_ids` — treat as empty list, no injection

---

## Open Questions

- **[?BOSS] Scope**: Should personas be space-scoped or global (shared across spaces)?
  Current proposal: space-scoped for simplicity. Global sharing can be added in v2 via
  an import/export mechanism.

- **[?BOSS] Persona versioning**: Should editing a persona re-inject into running agents?
  Current proposal: No — injection only happens at spawn/restart time. Running agents are
  not disturbed.
