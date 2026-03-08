# Design Specification — TASK-014 Frontend UX Overhaul

**Agent:** UIOverhaulDesign
**Branch:** feat/task-014-ui-overhaul
**Status:** FINAL — ready for Dev implementation

---

## Executive Summary

The frontend has a solid design token foundation (Red Hat brand palette, CSS custom properties, consistent font stack) but the tokens are inconsistently applied across views. Each view was built semi-independently, resulting in 5 different visual languages. This spec defines the single design language that unifies them.

**Design North Star:** One ops dashboard. Every view is a different lens on the same system — they should feel like siblings, not strangers.

---

## 1. Token Enforcement (Priority: HIGH)

The token system in `style.css` is correct. The problem is components bypassing it with ad-hoc Tailwind color classes.

### Violations to Fix

**StatusBadge.vue** — hardcodes Tailwind color names instead of CSS vars:
```
bg-green-500/15 text-green-400       → use: bg-success/15 text-success
bg-amber-500/15 text-amber-600       → use: bg-warning/15 text-warning-foreground
bg-teal-500/15 text-teal-400         → use: bg-info/15 text-info
bg-destructive/15 text-destructive   → CORRECT (token-based)
bg-muted text-muted-foreground       → CORRECT
```

**HierarchyView.vue + AgentProfileCard.vue** — role badges use ad-hoc purple:
```
border-purple-500/40 text-purple-600 dark:text-purple-400
```
Add a `--role` token to style.css:
```css
:root { --role: oklch(0.555 0.16 285); }
.dark { --role: oklch(0.685 0.18 285); }
```
Then use: `border-[var(--role)]/40 text-[var(--role)]`

**Rule for Dev:** If a color doesn't have a CSS var, add the var first, then use it. Never reach for `green-500`, `amber-600`, `purple-600` directly.

---

## 2. Typography Scale (Priority: HIGH)

All views must use this hierarchy consistently. No deviations.

| Role | Tailwind Classes | Usage |
|------|-----------------|-------|
| View title | `text-base font-semibold` | Tab labels, section names |
| Section header | `text-sm font-semibold` | Card headers, group labels |
| Body | `text-sm` | Main content, descriptions |
| Supporting | `text-xs text-muted-foreground` | Timestamps, meta, hints |
| Micro | `text-[10px] text-muted-foreground` | Badges, counts, labels |
| Mono | `font-mono text-xs` | Branch names, IDs, code |

**Current fragmentation examples:**
- HierarchyView uses `text-sm font-semibold` for agent names (correct)
- SpaceOverview agent card names vary (`text-sm font-medium` vs `text-base font-semibold`)
- GanttTimeline axis labels have no consistent size class

---

## 3. Empty State Pattern (Priority: HIGH)

All empty states must use this exact structure:

```html
<div class="flex flex-col items-center justify-center py-16 text-center gap-3">
  <div class="rounded-full bg-muted p-3.5">
    <ICON class="size-6 text-muted-foreground/60" aria-hidden="true" />
  </div>
  <div class="space-y-1">
    <p class="text-sm font-medium text-foreground">Primary message</p>
    <p class="text-xs text-muted-foreground">Supporting hint or action prompt</p>
  </div>
  <!-- Optional: action button -->
  <Button variant="outline" size="sm" class="mt-2">...</Button>
</div>
```

**Views that need empty state unification:**
- HierarchyView: currently uses `py-16` with muted text but NO icon wrapper `rounded-full bg-muted p-3` — fix by adding the icon wrapper
- GanttTimeline: loading/error states are raw strings — needs full empty state treatment
- KanbanView: add empty-column state matching the above pattern

---

## 4. View/Section Header Pattern (Priority: MEDIUM)

Tabs in SpaceOverview (Agents, Hierarchy, Timeline, Kanban, Inbox) are the top-level nav. Each tab content pane should optionally start with a **section context bar** when context is needed:

```html
<!-- Only add when the view benefits from a top action bar -->
<div class="flex items-center justify-between gap-3 mb-4">
  <div class="flex items-center gap-2">
    <ICON class="size-4 text-muted-foreground" />
    <h2 class="text-sm font-semibold">Section Name</h2>
    <Badge variant="secondary" class="text-xs">{{ count }}</Badge>
  </div>
  <div class="flex items-center gap-2">
    <!-- Actions: filters, buttons -->
  </div>
</div>
```

**Do NOT** add headers to views that already have clear tab labels. The tab IS the header. Only add section context bars when content requires extra orientation (GanttTimeline's window selector, KanbanView's filter bar).

---

## 5. Card Surface Hierarchy

Three surface levels. Use them consistently:

| Level | CSS | Tailwind equivalent | Usage |
|-------|-----|-------------------|-------|
| Page | `--background` | `bg-background` | Full-page base |
| Card | `--card` | `bg-card` | Primary grouping surfaces |
| Raised | `--popover` | `bg-popover` | Popovers, floating elements |

**Agent cards in SpaceOverview** — must use `bg-card border border-border` (they do). Do not introduce a 4th level.

**Hover states:** Use `hover:bg-accent/50` for list rows (correct in HierarchyView). Use `hover:border-primary/50` for card-level hover (where card itself is interactive).

---

## 6. Bug Fix Specs

### BUG-1: ConversationsView Auto-Scroll

**Root cause:** ConversationsView has no scroll-to-bottom logic when messages update or a new conversation is selected.

**Fix spec:** Extract the auto-scroll logic from `AgentMessages.vue` into a composable `frontend/src/composables/useMessageScroll.ts`:

```typescript
// useMessageScroll.ts
import { ref, nextTick } from 'vue'
import type { Ref } from 'vue'

export function useMessageScroll(scrollAreaRef: Ref<{ $el: HTMLElement } | null>) {
  const isAtBottom = ref(true)
  const newMessageCount = ref(0)

  function getScrollEl(): HTMLElement | null {
    return scrollAreaRef.value?.$el?.querySelector('[data-radix-scroll-area-viewport]') ?? null
  }

  function checkAtBottom() {
    const el = getScrollEl()
    if (!el) return
    isAtBottom.value = el.scrollTop + el.clientHeight >= el.scrollHeight - 32
    if (isAtBottom.value) newMessageCount.value = 0
  }

  function scrollToBottom() {
    nextTick(() => {
      const el = getScrollEl()
      if (el) {
        el.scrollTop = el.scrollHeight
        isAtBottom.value = true
        newMessageCount.value = 0
      }
    })
  }

  return { isAtBottom, newMessageCount, checkAtBottom, scrollToBottom, getScrollEl }
}
```

Apply in both `AgentMessages.vue` (refactor to use composable) and `ConversationsView.vue` (add fresh usage). ConversationsView must watch `activeConversation` and the messages array length, calling `scrollToBottom()` on both changes.

### BUG-2: AgentProfileCard Name Truncation / StatusBadge Overlap

**Root cause:** The trigger `<span>` wrapping the slot has `inline-flex` but no `min-w-0`. When placed inside a flex container, the inner truncated text cannot shrink past the span's intrinsic width.

**Fix spec:** Change the trigger element:
```html
<!-- BEFORE -->
<span class="inline-flex items-center cursor-default" ...>

<!-- AFTER -->
<span class="inline-flex items-center min-w-0 cursor-default" ...>
```

Additionally, ensure that wherever `AgentProfileCard` is used as a trigger, the parent flex container also has `min-w-0`:

```html
<!-- In HierarchyView, SpaceOverview agent cards, etc. -->
<AgentProfileCard ...>
  <div class="flex items-center gap-2 min-w-0">  <!-- min-w-0 REQUIRED -->
    <AgentAvatar ... class="shrink-0" />
    <span class="text-sm font-semibold truncate">{{ name }}</span>
  </div>
</AgentProfileCard>
```

The `shrink-0` on Avatar prevents avatar compression; `min-w-0` + `truncate` on the span clips the name before the StatusBadge.

---

## 7. GanttTimeline Design Spec

Currently the timeline is functional but visually raw — no consistent surface treatment.

**Required changes:**
1. Wrap in `bg-card rounded-lg border border-border p-4` to match other card surfaces
2. Agent name column: use `text-sm font-mono text-muted-foreground` (consistent with other agent name mono displays)
3. Status bar colors: map to CSS token colors (currently uses Tailwind status colors directly)
   - active → `bg-success/70`
   - blocked → `bg-warning/70`
   - done → `bg-info/70`
   - idle → `bg-muted`
   - error → `bg-destructive/70`
4. Time axis labels: `text-[10px] font-mono text-muted-foreground`
5. Loading state: use the standard empty state pattern with a spinner, not raw text
6. Window selector buttons: use `Button` component with `variant="ghost" size="sm"`, active state: `variant="secondary"`

---

## 8. KanbanView Unification

The KanbanView is mostly well-structured. Required changes:

1. Column headers: ensure they use `text-sm font-semibold` (section header level)
2. Empty column state: use standard empty state pattern (icon + text) instead of blank space
3. Task cards: ensure `bg-card border border-border` surface (match agent cards)
4. Drag states: `ring-2 ring-primary/50` for drag-over target indication

---

## 9. ConversationsView Design Spec

The left sidebar (conversation list) and right panel (messages) currently have inconsistent surface styling.

**Required unification:**
1. Left sidebar: `bg-card border-r border-border` — conversation rows use `hover:bg-accent/50 px-3 py-2` (match HierarchyView row pattern)
2. Active conversation row: `bg-accent text-accent-foreground`
3. Right message panel: `bg-background` (page level — the sidebar is the card surface)
4. Message bubbles:
   - Boss/incoming: `bg-muted rounded-xl rounded-tl-sm`
   - Agent/outgoing: `bg-primary/10 rounded-xl rounded-tr-sm`
5. Day separators: `text-xs text-muted-foreground font-medium uppercase tracking-wide`
6. Input area: `border-t border-border bg-card`

---

## 10. Spacing Rhythm

Use this spacing scale consistently. Do not invent new values.

| Context | Value | Tailwind |
|---------|-------|---------|
| Between view sections | 24px | `gap-6` / `space-y-6` |
| Between cards | 16px | `gap-4` / `space-y-4` |
| Within card (padding) | 16px | `p-4` |
| Between items in a list | 4px | `gap-1` / `space-y-1` |
| Between inline elements | 8px | `gap-2` |
| Between tightly grouped elements | 6px | `gap-1.5` |

---

## Implementation Priority Order

For UIOverhaulDev, implement in this order:

1. **BUG-2** — AgentProfileCard `min-w-0` fix (5 min, immediate visual fix)
2. **BUG-1** — Extract `useMessageScroll` composable, apply to ConversationsView (20 min)
3. **Token enforcement** — Fix StatusBadge ad-hoc colors, add `--role` token (15 min)
4. **Empty state unification** — HierarchyView, GanttTimeline, KanbanView (30 min)
5. **GanttTimeline surface** — wrap in card, fix bar colors (20 min)
6. **ConversationsView design** — sidebar/panel surface unification (25 min)
7. **KanbanView polish** — empty columns, drag states (15 min)

Total estimated scope: ~2 hours of implementation work.

---

## What NOT to Change

- The `style.css` token system — it's correct, don't restructure it
- The `AgentMessages.vue` scroll logic — extract to composable but don't change the UX behavior
- The `StatusBadge` component structure — only change the color classes
- The `AppSidebar.vue` navigation — out of scope for this task
- Routing structure — no changes needed
- The `AgentAvatar` component — correctly uses a color-deterministic avatar system
