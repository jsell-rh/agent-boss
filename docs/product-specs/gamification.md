# Agent Boss Gamification — Product Spec

**Status:** In progress
**Owner:** ux
**Guiding principle:** Fun features NEVER block or obscure utility. They enhance the experience when things go well, and they are always opt-out (not opt-in).

---

## Problem

Agent Boss is functionally excellent but emotionally flat. Watching a swarm of agents land a complex task should feel exciting — not like reading a server log. Small, well-placed moments of delight increase operator engagement and make sustained monitoring sessions enjoyable.

---

## Feature Backlog (prioritized)

### Tier 1 — High impact, low effort (implement now)

#### 1.1 Confetti on task completion ✅ implemented
- **Trigger:** Any task moves to `done` status (drag-and-drop OR SSE-driven move)
- **Implementation:** Pure canvas particle burst, no library needed. ~80 lines TS.
- **Constraint:** Lasts ≤ 2 seconds. Does not intercept mouse events. Auto-removes the canvas.
- **Never blocks:** Confetti fires and forgets — if canvas fails, task move still succeeds.

#### 1.2 Notification sounds
- **Trigger:** Configurable per-event:
  - Task → `done`: pleasant success chord (Web Audio API, no files needed)
  - New message received: subtle ping
  - Agent status → `done` or `idle` (all agents): gentle chime
- **Implementation:** `useSounds.ts` composable using Web Audio API. Synth tones, zero audio files.
- **Settings:** Toggle in global settings drawer (sound on/off + volume). Persisted in `localStorage`.
- **Constraint:** Muted by default. User must explicitly enable. Browser autoplay policy requires user interaction before sounds play — the first sound after the user clicks anything is safe.
- **Never blocks:** All sound calls wrapped in try/catch. If Web Audio API unavailable, silent fail.

---

### Tier 2 — Medium impact, moderate effort (next sprint)

#### 2.1 Emoji reactions on agent messages
- **Where:** Agent status updates in the conversation/event log
- **Implementation:** Click a `+😀` button on any message to add a reaction (emoji picker). Reactions stored server-side in message metadata (new `reactions` field on `AgentUpdate`).
- **Backend change required:** Add `reactions: map[string][]string` to the message model. One endpoint: `POST /spaces/:space/agents/:agent/reactions`.
- **Constraint:** Reactions are cosmetic. They cannot block message delivery or task actions.
- **Never blocks:** Reaction API failure is silent — optimistic UI update, rollback on error.

#### 2.2 Celebration moment: all agents idle
- **Trigger:** All agents in a space transition to `idle` simultaneously (sprint complete)
- **Implementation:** Check agent statuses in SSE `agent_updated` handler. When all transition to idle: display a brief "🎉 Sprint complete!" banner + confetti burst.
- **Constraint:** Only triggers once per "all-idle" transition (not on every poll). 3-second banner max.
- **Never blocks:** Banner is overlaid, never in the critical path.

---

### Tier 3 — Fun but lower ROI (backlog, revisit quarterly)

#### 3.1 Fun status vocabulary ("Flavor Mode")
- **What:** Optional alternate labels for agent statuses: `active` → "crushing it", `idle` → "chilling", `done` → "nailed it", `blocked` → "stuck in traffic"
- **Toggle:** In settings drawer: "Standard labels / Flavor mode"
- **Constraint:** Status badge tooltip always shows the canonical status. Screen readers use canonical. Never shown in exported data or API responses.

#### 3.2 Agent personality: custom emoji icons
- **What:** Each agent can have an emoji icon shown alongside their avatar (e.g. 🤖, 🦊, 🐬)
- **Where to set:** Agent create dialog / agent detail panel — optional emoji picker
- **Backend:** New optional `icon` field on agent (stored in agent metadata, already extensible)
- **Constraint:** Falls back to existing AgentAvatar if no icon set. Zero visual regression.

#### 3.3 Space color theming
- **What:** Each space gets an accent color (user-selectable). Used for sidebar highlight, column headers.
- **Implementation:** Store color in space settings. Apply via CSS variable override scoped to the space view.
- **Constraint:** Color must pass WCAG AA contrast. Color picker limited to pre-approved accessible palette.

#### 3.4 Streaks and task counters
- **What:** "This space has completed 42 tasks" subtle counter in SpaceOverview. Per-agent "tasks done" in AgentDetail.
- **Backend:** Aggregate query from existing task event log (type=moved, detail contains "to done").
- **Constraint:** Counter is display-only. Never shown on empty/Day-0 spaces.

#### 3.5 Sound themes
- **What:** Selectable soundscape (lo-fi beats, space ambient) while dashboard is open. Uses Web Audio API oscillators + filters for generative music — no audio files.
- **Toggle:** In settings drawer. Off by default.
- **Constraint:** Volume capped at 30% of system volume. Pauses when tab is backgrounded (`document.visibilityState`).

---

## Implementation Notes

### Confetti technical design

```typescript
// Confetti fires from the center of the card/column that triggered the move.
// Falls under gravity + slight horizontal drift. Fades out with opacity.
// Colors: high-saturation, brand-friendly palette.
// Particle count: 60 (desktop) / 30 (prefers-reduced-motion: reduce).
// Duration: 1.8s
// No z-index issues: canvas uses pointer-events: none, z-index: 9999.
```

### Sound technical design

```typescript
// All sounds are synthesized via Web Audio API — no audio files to load.
// "Success" chord: C major triad (C5, E5, G5), each 80ms apart, 300ms sustain, 400ms release.
// "Ping": 880Hz sine, 50ms attack, 150ms release.
// "Chime": 3-note ascending arpeggio (A4, C5, E5), 100ms apart.
// Volume: 0.08 (very subtle). User can adjust 0–100% in settings.
```

### Settings storage

Gamification prefs in `localStorage` key `boss:gamify`:
```json
{
  "confetti": true,
  "sounds": false,
  "soundVolume": 8,
  "flavorMode": false
}
```

---

## Anti-patterns to avoid

| ❌ Don't | ✅ Do instead |
|----------|---------------|
| Block task move until animation completes | Fire animation after state update |
| Show confetti on every SSE event | Only on status transition TO `done` |
| Auto-play sounds without user opt-in | Default sounds off; require settings toggle |
| Cover task cards with particles | `pointer-events: none` on all overlays |
| Break screen reader experience | Canonical status always in aria labels |
| Increase bundle size with animation libraries | Pure TS/CSS/Web Audio only |

---

## Acceptance Criteria (Tier 1)

- [ ] Confetti fires when any task is dragged to the "done" column
- [ ] Confetti fires when SSE delivers a `task_updated` event that transitions a task TO `done`
- [ ] `prefers-reduced-motion` halves particle count and disables rotation
- [ ] Confetti canvas is removed from DOM after animation completes
- [ ] `npm run build` succeeds with no TypeScript errors
- [ ] Existing e2e tests still pass
