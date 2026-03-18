# OpenDispatch Gamification — Product Spec

**Status:** In progress
**Owner:** ux
**Guiding principle:** Fun features NEVER block or obscure utility. They enhance the experience when things go well, and they are always opt-out (not opt-in).

---

## Problem

OpenDispatch is functionally excellent but emotionally flat. Watching a swarm of agents land a complex task should feel exciting — not like reading a server log. Small, well-placed moments of delight increase operator engagement and make sustained monitoring sessions enjoyable.

---

## Feature Backlog (prioritized)

### Tier 1 — High impact, low effort (implement now)

#### 1.1 Confetti on task completion ✅ implemented
- **Trigger:** Any task moves to `done` status (drag-and-drop OR SSE-driven move)
- **Implementation:** Pure canvas particle burst, no library needed. ~80 lines TS.
- **Constraint:** Lasts ≤ 2 seconds. Does not intercept mouse events. Auto-removes the canvas.
- **Never blocks:** Confetti fires and forgets — if canvas fails, task move still succeeds.

#### 1.2 Notification sounds ✅ implemented
- **Trigger:** Configurable per-event:
  - Task → `done`: pleasant success chord (Web Audio API, no files needed)
  - New message received: subtle ping
  - Agent status → `done` or `idle` (all agents): gentle chime
- **Implementation:** `useNotifications.ts` composable using Web Audio API. Synth tones, zero audio files. 4 selectable themes: Classic, Retro 8-bit, Spaceship, Nature.
- **Settings:** Toggle + theme picker in global settings drawer. Persisted in `localStorage`.
- **Constraint:** Muted by default. User must explicitly enable.
- **Never blocks:** All sound calls wrapped in try/catch. If Web Audio API unavailable, silent fail.

---

### Tier 2 — Medium impact, moderate effort (next sprint)

#### 2.1 Emoji reactions on agent messages
- **Where:** Agent status updates in the conversation/event log
- **Implementation:** Click a `+😀` button on any message to add a reaction (emoji picker). Reactions stored server-side in message metadata (new `reactions` field on `AgentUpdate`).
- **Backend change required:** Add `reactions: map[string][]string` to the message model. One endpoint: `POST /spaces/:space/agents/:agent/reactions`.
- **Constraint:** Reactions are cosmetic. They cannot block message delivery or task actions.
- **Never blocks:** Reaction API failure is silent — optimistic UI update, rollback on error.

#### 2.2 Celebration moment: all agents idle ✅ implemented
- **Trigger:** All agents in a space transition to `idle` simultaneously (sprint complete)
- **Implementation:** Check agent statuses in SSE `agent_updated` handler. When all transition to idle: confetti burst + sprint-complete fanfare + "🎉 Sprint complete!" toast.
- **Constraint:** Only triggers once per "all-idle" transition. 3-second toast max.
- **Never blocks:** Banner is overlaid, never in the critical path.

#### 2.3 Agent status pulse rings ✅ implemented
- **Trigger:** Continuous — reflects live agent status on the agent card
- **Implementation:** Pure CSS `@keyframes` animations applied as class variants on the agent status badge/ring:
  - `active` → sonar-ping ring radiating outward (scale + opacity, 2s loop)
  - `idle` → slow breathe (opacity 0.4↔1.0, 3s ease-in-out infinite)
  - `blocked` / `error` → subtle red jitter (translateX ±2px, 0.1s rapid)
  - `done` → static green glow (box-shadow, no animation — calm resolution)
- **Constraint:** Animation plays on the border/ring element only, never covers the badge text. `prefers-reduced-motion` disables all animations.
- **Never blocks:** Pure CSS — no JS in the render path.

#### 2.4 Agent mood / vibes field
- **What:** Agents can include an optional `mood` string in `post_status` (e.g. "in the zone", "fighting a flaky test"). Displayed as small italic flavor text on the agent card beneath the status badge.
- **Backend:** Add optional `mood` field to the `AgentUpdate` struct; render in status display.
- **Frontend:** Show `mood` in italic below status if present. No mood = no UI change.
- **Constraint:** Display only. Never affects routing, task logic, or agent lifecycle.

#### 2.5 Kanban card spring-drop ✅ implemented
- **Trigger:** Any task card that enters a kanban column (drag-drop or SSE-driven move).
- **Implementation:** CSS `@keyframes kanban-spring-drop` — card drops from above with a slight overshoot (+5px) then settles. 0.35s cubic-bezier spring. `prefers-reduced-motion` disables.
- **Never blocks:** Pure CSS on `TransitionGroup` enter animation.

#### 2.6 @mention pulse animation ✅ implemented
- **Trigger:** When `send_message` body contains `@agent-name`.
- **Implementation:** SSE `agent_message` handler parses message content for `@word` patterns. Matched agent cards in the sidebar pulse with a 3s ring animation (box-shadow expand/fade). Reactive `mentionedAgents` Set with auto-expiry.
- **Protocol:** Requires agents to use `@agent-name` syntax in messages. Documented in `internal/coordinator/protocol.md` via PR #171 (garden).
- **Never blocks:** Visual only. No database changes.

#### 2.7 Fleet Vibe roll-up ✅ implemented
- **What:** Live emoji + label in the space header derived from agent status distribution.
  - `error > 0` → "On fire" / `blocked >= 50%` → "Blocked" / `active >= 60%` → "Shipping"
  - `active > 0, no blocks` → "In progress" / `done >= 80%` → "Wrapping up"
  - `all idle/done` → "All resting" / mixed → "Mixed"
- **Implementation:** Computed property on SpaceOverview; updates reactively with SSE.
- **Never blocks:** Pure computed, no API calls.

#### 2.8 Agent spawn warp ✅ implemented
- **Trigger:** `agent_spawned` SSE event.
- **Implementation:** Agent item in sidebar gets `agent-spawn` CSS class for 600ms — scale(0.35) → overshoot → settle(1.0) with a ring pulse. Feels like a portal opening.
- **Never blocks:** Pure CSS + reactive Set with auto-expiry.

#### 2.9 PR badge shimmer ✅ implemented
- **Trigger:** Any agent with a PR link (`agent.pr` set).
- **Implementation:** PR link text gets a traveling CSS shimmer (background-position sweep, 2.4s loop). Signals "code is out there, awaiting review."
- **Never blocks:** Pure CSS animation on the link element.

#### 2.10 Priority-based confetti variations ✅ implemented
- **Trigger:** Task moved to `done` (drag-drop or SSE).
- **Variations:**
  - `critical`: gold/orange palette, 130 particles, wider blast, 2.4s duration
  - `high`: 90 particles, 1.2× speed
  - `medium`: standard (65 particles)
  - `low`: light (40 particles)
- **Implementation:** `useConfetti.ts` `celebrate(x, y, priority)` overload. KanbanView passes `task.priority`.

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

#### 3.5 Sound themes ✅ implemented (Tier 1.2 upgrade)
- 4 themes (Classic, Retro 8-bit, Spaceship, Nature) shipped in PR #168. Theme picker in settings drawer.

#### 3.6 Agent signature chimes
- **What:** Each agent gets a unique tonal "voice" derived from their name hash — a 2-3 note chord that plays when they first post a status update per session. Inspired by character-select sounds (Smash Bros, SF6).
- **Implementation:** Hash agent name to a frequency offset; compose chord from OscillatorNode with different waveforms. Play once on first `agent_updated` SSE event per session.
- **Constraint:** Respects global sound toggle. Only once per page load per agent.

#### 3.7 @mention pulse animation
- **What:** When a `send_message` body contains `@agent-name`, that agent's card in the dashboard pulses with a highlight ring for ~3 seconds.
- **Implementation:** Parse message content for `@mentions` in SSE handler; apply a CSS class to the matching agent card for 3s. Zero DB schema changes.
- **Constraint:** Visual only. Never interrupts agent or operator workflow.

#### 3.8 Event feed waterfall stagger ✅ implemented
- **What:** New SSE events slide in from the top of the event log with a stagger delay, nudging existing items down. High-priority events get a brief background glow on entry.
- **Implementation:** CSS `translateY` + `opacity` transition (`event-entry` keyframe, 0.18s). Applies on DOM insertion via CSS animation (not re-animation on filter).
- **Constraint:** Only on new arrivals. `prefers-reduced-motion` disables.

#### 3.9 Agent signature chimes (backlog)
- **What:** Each agent gets a unique tonal "voice" derived from their name hash — a 2-3 note chord that plays when they first post a status update per session.
- **Implementation:** Hash agent name to a frequency offset; compose chord from OscillatorNode (sine + triangle). Play once per page load per agent. Respects global sound toggle.
- **Constraint:** Once per page load per agent. Never plays on page reload.

#### 3.10 Activity tick on SSE events (backlog)
- **What:** Every `agent_updated` SSE event plays a micro-duration white noise tick (1-5ms). Creates "busy server room" ambience. Very low gain (~0.01).
- **Implementation:** `AudioBufferSourceNode` filled with `Math.random()` values. Category toggle in settings.
- **Constraint:** Off by default. Category: "Ambient sounds."

#### 3.11 Emoji reactions on agent updates (backlog — backend required)
- **What:** Reactions (👍🔥🚀💯) on any agent status update. Stored server-side.
- **Backend:** New `reactions` table; `POST /spaces/:space/agents/:agent/updates/:id/react`.
- **Frontend:** Reaction picker on hover; pill clusters below update; SSE-broadcast delta updates.

#### 3.12 Agent streaks (backlog — backend required)
- **What:** Track consecutive active sessions per agent. Show flame counter (🔥 7-day streak) on agent cards.
- **Backend:** `streak` field on agents table, updated each time `post_status` is called within the staleness threshold.

#### 3.13 Ship It broadcast (backlog — backend required)
- **What:** `POST /spaces/:space/celebrate?type=ship` emits a team-wide celebration SSE event. All connected clients see a ship animation + optional fanfare.
- **Backend:** New endpoint; emits `team_celebration` SSE event.
- **MCP tool:** `celebrate_ship` so agents can trigger it from code.

#### 3.14 Co-working indicators (backlog — backend required)
- **What:** When two agents both update the same task within 5 min, show a "collaborating" badge linking their cards.
- **Backend:** Track `last_updater` per task; emit `agents_collaborating` SSE event on second distinct update.

#### 3.15 Daily standup digest (backlog — backend required)
- **What:** Auto-generate daily digest at 09:00 local: tasks done yesterday, in-progress today, blockers.
- **Backend:** Server-side goroutine ticker; configurable via `STANDUP_TIME` env var.

#### 3.16 Presence indicators (backlog — backend required)
- **What:** Show "N humans watching" cluster in space header. Updates when tabs connect/disconnect.
- **Backend:** In-memory presence map; SSE join/leave events.

#### 3.17 Task velocity leaderboard (backlog — backend required)
- **What:** Collapsible sidebar panel: agents ranked by tasks closed this week.
- **Backend:** `GET /api/spaces/:space/leaderboard?days=7`.
- **Constraint:** Frames as "team velocity," not individual performance.

#### 3.18 Time-of-day chromashift (backlog)
- **What:** Dashboard background hue shifts subtly over 24 hours (dawn=warm amber, midday=cool blue, dusk=purple, night=deep indigo).
- **Implementation:** JS timer updates `--chromashift-hue` CSS custom property on `:root` every 60s. Applied as a very subtle overlay.
- **Constraint:** Maximum ±8° hue shift. Never affects text readability.

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
