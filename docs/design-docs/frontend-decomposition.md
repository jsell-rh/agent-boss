# Frontend Component Decomposition Plan

**Status:** proposed
**Task:** TASK-114 (pending)
**Author:** garden + ux

Addresses TD-003: three Vue components exceed 1300 LOC. This document specifies the decomposition into focused sub-components so ux can implement against a clear contract.

---

## Context

| Component | Current LOC | Target after split |
|-----------|------------|-------------------|
| `SpaceOverview.vue` | 1448 | ~400 (shell + 2–3 children) |
| `ConversationsView.vue` | 1410 | ~300 (shell + 3 children) |
| `AgentDetail.vue` | 1300 | ~350 (shell + 3 children) |

Each parent component becomes a thin shell responsible only for:
- Routing props and emits to/from children
- Top-level data fetching (SSE subscription, initial load)
- Coordinating cross-child state (e.g. selected agent, active conversation)

---

## 1. `SpaceOverview.vue` → 3 children

### `SpaceHeader.vue`

**Responsibility:** Top bar for a space — title, description, action buttons.

**Props:**
```typescript
interface Props {
  space: KnowledgeSpace
  hasUnsavedProtocol: boolean
}
```

**Emits:**
```typescript
'broadcast-nudge': () => void
'export-fleet': () => void
'open-import-fleet': () => void
'archive-space': () => void
'edit-protocol': () => void
```

**Contains:** Space title/description display, Broadcast/Nudge button, Export fleet button, Import fleet button (opens `ImportFleetModal`), Archive button, Protocol edit toggle.

**Does NOT contain:** Agent cards, task board, conversation list, SSE logic.

---

### `AgentGrid.vue`

**Responsibility:** Renders the grid of agent cards with filtering and pulse animations.

**Props:**
```typescript
interface Props {
  agents: AgentRecord[]
  selectedAgentName: string | null
  searchQuery: string
}
```

**Emits:**
```typescript
'select-agent': (name: string) => void
'search-change': (query: string) => void
```

**Contains:** Agent card grid, search/filter input, status badge rendering, pulse ring animation (3s ring on @mention), staleness indicator.

**Does NOT contain:** Agent detail panel, task board, spawn controls (those are in AgentDetail or SpaceHeader).

---

### `SpaceProtocolTab.vue` _(optional — extract if >200 LOC in parent after split)_

**Responsibility:** The "Protocol / Contracts" editor tab for a space.

**Props:**
```typescript
interface Props {
  space: KnowledgeSpace
  editable: boolean
}
```

**Emits:**
```typescript
'save-protocol': (content: string) => void
```

**Contains:** Protocol textarea, shared contracts editor, save button, markdown preview toggle.

---

## 2. `ConversationsView.vue` → 3 children

### `ConversationList.vue`

**Responsibility:** Left panel — list of agent conversations with search and unread indicators.

**Props:**
```typescript
interface Props {
  conversations: Conversation[]
  selectedAgent: string | null
  searchQuery: string
}
```

**Emits:**
```typescript
'select-conversation': (agentName: string) => void
'search-change': (query: string) => void
'new-conversation': () => void
```

**Contains:** Conversation list items, unread badge counts, search input, "New conversation" button, last-message preview.

**Does NOT contain:** Message thread, compose box.

---

### `ConversationThread.vue`

**Responsibility:** Right panel — full message thread for the selected conversation.

**Props:**
```typescript
interface Props {
  messages: AgentMessage[]
  loading: boolean
  agentName: string
}
```

**Emits:** _(none — read-only display)_

**Contains:** Message list with markdown rendering, skeleton loaders while fetching, auto-scroll to latest, timestamp formatting, sender avatar/badge.

**Does NOT contain:** Compose box, conversation switcher.

---

### `ConversationCompose.vue`

**Responsibility:** Compose and send a new message.

**Props:**
```typescript
interface Props {
  recipientName: string
  disabled: boolean
}
```

**Emits:**
```typescript
'send': (body: string) => void
```

**Contains:** Textarea with @mention highlighting, send button, character count if relevant, keyboard shortcut (Ctrl+Enter).

**Does NOT contain:** Message display, conversation selection.

---

## 3. `AgentDetail.vue` → 3 children

### `AgentStatusCard.vue`

**Responsibility:** Top section of agent detail — current status, mood, sticky fields, persona badge.

**Props:**
```typescript
interface Props {
  agent: AgentRecord
  personaOutdated: boolean
}
```

**Emits:**
```typescript
'spawn': () => void
'kill': () => void
'restart': () => void
'nudge': () => void
'update-persona': () => void
```

**Contains:** Status badge, mood text, branch/PR display, session ID, parent/children links, persona name + outdated indicator, spawn/kill/restart/nudge action buttons.

**Does NOT contain:** Update history, reply compose, task list.

---

### `AgentHistoryPanel.vue`

**Responsibility:** Scrollable feed of agent status updates with markdown rendering.

**Props:**
```typescript
interface Props {
  history: AgentStatusSnapshot[]
  loading: boolean
}
```

**Emits:** _(none — read-only)_

**Contains:** Status snapshot list, markdown rendering per update, timestamp, phase/items/next\_steps display, "load more" pagination trigger.

**Does NOT contain:** Current status card, reply compose.

---

### `AgentReplyCompose.vue`

**Responsibility:** Send a direct message to an agent.

**Props:**
```typescript
interface Props {
  agentName: string
  disabled: boolean
}
```

**Emits:**
```typescript
'send': (body: string) => void
```

**Contains:** Textarea, send button, @mention support, keyboard shortcut (Ctrl+Enter).

**Note:** `ConversationCompose.vue` and `AgentReplyCompose.vue` may share a common `<MessageCompose>` base component if the logic is identical — ux's call.

---

## Implementation order (suggested)

1. `ConversationCompose.vue` + `AgentReplyCompose.vue` — smallest, lowest risk, good warmup
2. `ConversationList.vue` + `ConversationThread.vue` — self-contained split from ConversationsView
3. `AgentStatusCard.vue` — isolates the action buttons from the history feed
4. `AgentHistoryPanel.vue` — isolates the long history list
5. `SpaceHeader.vue` + `AgentGrid.vue` — largest parent, save for last

Each step should keep all existing E2E tests green. Run `make e2e` after each extraction.

---

## Definition of done

- [ ] All three parent components are <600 LOC after split
- [ ] Each extracted component has clear props/emits matching this spec (or documented divergence)
- [ ] `make typecheck` passes (vue-tsc -b)
- [ ] `make e2e` passes (all 15 Playwright specs)
- [ ] `docs/QUALITY.md` updated with new LOC counts and upgraded frontend grade (target: B-)
- [ ] `ARCHITECTURE.md` Key Files table updated with new component entries

---

## Vitest coverage (TD-004)

As components are extracted, add Vitest unit tests for:
- `ConversationList.vue` — filtering, unread badge count logic
- `ConversationCompose.vue` / `AgentReplyCompose.vue` — send emit on Ctrl+Enter, disabled state
- `AgentStatusCard.vue` — persona outdated indicator, action button visibility by agent status

Existing: `importFleetModal.spec.ts` (PR #233). Target: 5+ component test files by end of sprint.
