<script setup lang="ts">
import { ref, watch, nextTick, onMounted, onUnmounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { api } from '@/api/client'
import { XCircle, ArrowDown, MessageSquare } from 'lucide-vue-next'

export interface EventLogEntry {
  id: number
  timestamp: string
  rawDate: Date
  type: string
  message: string
  source: 'server' | 'sse'
}

const props = defineProps<{
  spaceName: string
  agentNames?: string[]
}>()

const router = useRouter()

function escapeHtml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function escapeRegex(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function linkifyMessage(msg: string): string {
  const names = props.agentNames
  if (!names?.length) return escapeHtml(msg)
  let result = escapeHtml(msg)
  for (const name of names) {
    const safe = escapeHtml(name)
    result = result.replace(
      new RegExp(`\\b${escapeRegex(safe)}\\b`, 'g'),
      `<button class="font-medium text-primary hover:underline cursor-pointer" data-agent="${safe}">${safe}</button>`,
    )
  }
  return result
}

function handleLogClick(e: MouseEvent) {
  const target = (e.target as HTMLElement).closest('[data-agent]') as HTMLElement | null
  if (target?.dataset.agent && props.spaceName) {
    router.push(`/${encodeURIComponent(props.spaceName)}/${encodeURIComponent(target.dataset.agent)}`)
  }
}

// For message-type entries, extract the sender so we can navigate to the conversation.
// SSE format:    "[RECIPIENT] message from SENDER"
// Server format: "[SPACE/RECIPIENT] message from SENDER delivered"
function parseMsgSender(msg: string): string | null {
  const m = msg.match(/message from ([^\s]+)/)
  return m ? m[1]! : null
}

function openConversation(entry: EventLogEntry) {
  if (entry.type !== 'message' || !props.spaceName) return
  const sender = parseMsgSender(entry.message)
  if (sender) {
    router.push(`/${encodeURIComponent(props.spaceName)}/conversations/${encodeURIComponent(sender)}`)
  } else {
    router.push(`/${encodeURIComponent(props.spaceName)}/conversations`)
  }
}

const entries = ref<EventLogEntry[]>([])
const isOpen = ref(true)
const autoScroll = ref(true)
const scrollContainer = ref<HTMLElement | null>(null)
const panelHeight = ref(220)
const expandedId = ref<number | null>(null)
const isResizing = ref(false)
// Unread count: events that arrived while panel was closed or user scrolled up
const unreadCount = ref(0)
// Live SSE indicator: pulses briefly when a live event arrives
const sseActive = ref(false)
let sseActiveTimer: ReturnType<typeof setTimeout> | null = null
// Text search
const searchQuery = ref('')

// Filter state — empty set = show all
const activeTypes = ref<Set<string>>(new Set())
// Incremented every 10s to trigger relative timestamp recomputation
const tick = ref(0)

const MIN_HEIGHT = 100
const MAX_HEIGHT = 600
const STORAGE_KEY_HEIGHT = 'eventlog-panel-height'

let nextId = 0
let refreshTimer: ReturnType<typeof setInterval> | null = null
let tickTimer: ReturnType<typeof setInterval> | null = null

// Track which server-side log messages we've already seen (to avoid duplicates)
const seenServerMessages = new Set<string>()

// Format a Date as a human-readable relative string ("2s ago", "5m ago", etc.)
function formatRelative(date: Date, _tick: number): string {
  const diffMs = Date.now() - date.getTime()
  const diffSec = Math.floor(diffMs / 1000)
  if (diffSec < 5) return 'just now'
  if (diffSec < 60) return `${diffSec}s ago`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  return date.toLocaleTimeString('en-GB', { hour12: false })
}

// Parse "[HH:MM:SS] message" format from the server event log
function parseEventLogEntry(raw: string): EventLogEntry {
  const match = raw.match(/^\[([^\]]+)\]\s*(.*)/)
  const now = new Date()
  if (match) {
    // Reconstruct a Date from the HH:MM:SS timestamp portion
    const timeParts = match[1]!.split(':')
    let rawDate = now
    if (timeParts.length === 3) {
      const candidate = new Date(now)
      candidate.setHours(
        parseInt(timeParts[0]!, 10),
        parseInt(timeParts[1]!, 10),
        parseInt(timeParts[2]!, 10),
        0,
      )
      // If the reconstructed time is in the future, assume it was yesterday
      if (candidate > now) candidate.setDate(candidate.getDate() - 1)
      rawDate = candidate
    }
    return {
      id: nextId++,
      timestamp: match[1]!,
      rawDate,
      type: inferEventType(match[2]!),
      message: match[2]!,
      source: 'server',
    }
  }
  return {
    id: nextId++,
    timestamp: now.toLocaleTimeString('en-GB', { hour12: false }),
    rawDate: now,
    type: 'info',
    message: raw,
    source: 'server',
  }
}

// Infer event type from message content for badge coloring
function inferEventType(msg: string): string {
  if (msg.includes('approval') || msg.includes('Approve') || msg.includes('approved')) return 'approval'
  if (msg.includes('interrupt')) return 'interrupt'
  if (msg.includes('broadcast') || msg.includes('Broadcast')) return 'broadcast'
  if (msg.includes('error') || msg.includes('failed') || msg.includes('Failed')) return 'error'
  if (msg.includes('deleted') || msg.includes('removed')) return 'removed'
  if (msg.includes('created') || msg.includes('loaded')) return 'system'
  if (msg.includes('started') || msg.includes('stopped')) return 'system'
  if (msg.includes('Message from') || msg.includes('reply')) return 'message'
  if (msg.includes('document')) return 'document'
  if (msg.includes('contracts') || msg.includes('archive')) return 'update'
  // Agent updates are the most common
  if (/\[.+\/.+\]/.test(msg)) return 'agent'
  return 'info'
}

// SSE event type to badge type
function sseEventToBadge(sseType: string): string {
  const map: Record<string, string> = {
    agent_updated: 'agent',
    agent_removed: 'removed',
    space_deleted: 'removed',
    agent_message: 'message',
    session_liveness: 'session',
    broadcast_complete: 'broadcast',
    broadcast_progress: 'broadcast',
  }
  return map[sseType] || 'info'
}

const badgeStyles: Record<string, string> = {
  agent: 'bg-green-500/15 text-green-600 dark:text-green-400 border-green-500/20',
  approval: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/20',
  interrupt: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/20',
  broadcast: 'bg-cyan-500/15 text-cyan-600 dark:text-cyan-400 border-cyan-500/20',
  error: 'bg-red-500/15 text-red-600 dark:text-red-400 border-red-500/20',
  removed: 'bg-gray-500/15 text-gray-600 dark:text-gray-400 border-gray-500/20',
  system: 'bg-blue-500/15 text-blue-600 dark:text-blue-400 border-blue-500/20',
  message: 'bg-purple-500/15 text-purple-600 dark:text-purple-400 border-purple-500/20',
  document: 'bg-violet-500/15 text-violet-600 dark:text-violet-400 border-violet-500/20',
  update: 'bg-sky-500/15 text-sky-600 dark:text-sky-400 border-sky-500/20',
  tmux: 'bg-slate-500/15 text-slate-600 dark:text-slate-400 border-slate-500/20',
  info: 'bg-muted text-muted-foreground border-border',
}

function getBadgeClass(type: string): string {
  return badgeStyles[type] || badgeStyles.info!
}

// All distinct event types present in the log, with counts, sorted by count descending
const availableTypes = computed(() => {
  const counts = new Map<string, number>()
  for (const e of entries.value) {
    counts.set(e.type, (counts.get(e.type) ?? 0) + 1)
  }
  return [...counts.entries()].sort(([, a], [, b]) => b - a)
})

// Entries filtered by active type selection and text search
const filteredEntries = computed(() => {
  let result = entries.value
  if (activeTypes.value.size > 0) {
    result = result.filter(e => activeTypes.value.has(e.type))
  }
  const q = searchQuery.value.trim().toLowerCase()
  if (q) {
    result = result.filter(e => e.message.toLowerCase().includes(q) || e.type.toLowerCase().includes(q))
  }
  return result
})

// Badge always shows total entry count (not filtered)
const entryCount = computed(() => entries.value.length)

function toggleTypeFilter(type: string) {
  const next = new Set(activeTypes.value)
  if (next.has(type)) {
    next.delete(type)
  }
  else {
    next.add(type)
  }
  activeTypes.value = next
}

function clearFilters() {
  activeTypes.value = new Set()
  searchQuery.value = ''
}

// Load events from the server API (initial + periodic refresh)
async function loadEvents() {
  if (!props.spaceName) return
  try {
    const raw = await api.fetchEvents(props.spaceName)
    if (Array.isArray(raw)) {
      // On first load, populate everything
      if (seenServerMessages.size === 0) {
        entries.value = raw.map(parseEventLogEntry)
        for (const r of raw) {
          seenServerMessages.add(r)
        }
        if (isOpen.value) {
          nextTick(scrollToBottom)
        }
      }
      else {
        // On refresh, only add new entries we haven't seen
        let added = false
        for (const r of raw) {
          if (!seenServerMessages.has(r)) {
            seenServerMessages.add(r)
            entries.value.push(parseEventLogEntry(r))
            added = true
          }
        }
        if (added) {
          if (!isOpen.value) {
            unreadCount.value++
          }
          else if (autoScroll.value) {
            scrollToBottom()
          }
          else {
            unreadCount.value++
          }
        }
      }
      // Cap entries
      if (entries.value.length > 500) {
        entries.value = entries.value.slice(-500)
      }
    }
  }
  catch {
    // Silently fail — events are non-critical
  }
}

// Start periodic refresh of server events (catches things SSE doesn't push)
// Only polls when panel is open — saves bandwidth when collapsed
function startRefresh() {
  stopRefresh()
  refreshTimer = setInterval(() => {
    if (isOpen.value) loadEvents()
  }, 3000)
  tickTimer = setInterval(() => { tick.value++ }, 10000)
}

function stopRefresh() {
  if (refreshTimer !== null) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
  if (tickTimer !== null) {
    clearInterval(tickTimer)
    tickTimer = null
  }
}

// Public method: push a live SSE event into the log
function pushSSEEvent(sseType: string, summary: string) {
  const now = new Date()
  const entry: EventLogEntry = {
    id: nextId++,
    timestamp: now.toLocaleTimeString('en-GB', { hour12: false }),
    rawDate: now,
    type: sseEventToBadge(sseType),
    message: summary,
    source: 'sse',
  }
  entries.value.push(entry)
  if (entries.value.length > 500) {
    entries.value = entries.value.slice(-500)
  }

  // Flash the SSE live indicator
  sseActive.value = true
  if (sseActiveTimer !== null) clearTimeout(sseActiveTimer)
  sseActiveTimer = setTimeout(() => { sseActive.value = false }, 1500)

  if (!isOpen.value) {
    unreadCount.value++
  }
  else if (autoScroll.value) {
    scrollToBottom()
  }
  else {
    unreadCount.value++
  }
}

function clearLog() {
  entries.value = []
  seenServerMessages.clear()
  unreadCount.value = 0
}

function toggleOpen() {
  isOpen.value = !isOpen.value
  if (isOpen.value) {
    unreadCount.value = 0
    nextTick(scrollToBottom)
    // Resume polling immediately on open
    loadEvents()
  }
}

function scrollToBottom() {
  nextTick(() => {
    const el = scrollContainer.value
    if (el) {
      el.scrollTop = el.scrollHeight
    }
  })
}

function jumpToBottom() {
  autoScroll.value = true
  unreadCount.value = 0
  scrollToBottom()
}

function handleScroll() {
  const el = scrollContainer.value
  if (!el) return
  const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
  autoScroll.value = atBottom
  if (atBottom) unreadCount.value = 0
}

// localStorage height persistence
function loadStoredHeight() {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_HEIGHT)
    if (stored) {
      const h = parseInt(stored, 10)
      if (h >= MIN_HEIGHT && h <= MAX_HEIGHT) panelHeight.value = h
    }
  }
  catch { /* ignore */ }
}

function saveHeight() {
  try {
    localStorage.setItem(STORAGE_KEY_HEIGHT, String(panelHeight.value))
  }
  catch { /* ignore */ }
}

// Keyboard shortcut: 'e' toggles the panel
function handleKeydown(e: KeyboardEvent) {
  const tag = (e.target as HTMLElement).tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || (e.target as HTMLElement).isContentEditable) return
  if (e.key === 'e' && !e.metaKey && !e.ctrlKey && !e.altKey) {
    toggleOpen()
  }
}

// Resize handling
function startResize(e: MouseEvent | TouchEvent) {
  e.preventDefault()
  isResizing.value = true
  const startY = 'touches' in e ? e.touches[0]!.clientY : e.clientY
  const startHeight = panelHeight.value

  function getY(ev: MouseEvent | TouchEvent): number {
    return 'touches' in ev ? ev.touches[0]!.clientY : (ev as MouseEvent).clientY
  }

  function onMove(moveEvent: MouseEvent | TouchEvent) {
    // Dragging up increases height (mouse goes up = smaller Y = positive delta)
    const delta = startY - getY(moveEvent)
    panelHeight.value = Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, startHeight + delta))
  }

  function onEnd() {
    isResizing.value = false
    saveHeight()
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onEnd)
    document.removeEventListener('touchmove', onMove as EventListener)
    document.removeEventListener('touchend', onEnd)
  }

  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onEnd)
  document.addEventListener('touchmove', onMove as EventListener, { passive: false })
  document.addEventListener('touchend', onEnd)
}

// Reload events when space changes
watch(() => props.spaceName, () => {
  entries.value = []
  seenServerMessages.clear()
  nextId = 0
  activeTypes.value = new Set()
  searchQuery.value = ''
  unreadCount.value = 0
  loadEvents()
})

onMounted(() => {
  loadStoredHeight()
  loadEvents()
  startRefresh()
  document.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  stopRefresh()
  document.removeEventListener('keydown', handleKeydown)
  if (sseActiveTimer !== null) clearTimeout(sseActiveTimer)
})

defineExpose({ pushSSEEvent, clearLog })
</script>

<template>
  <div class="border-t bg-card flex flex-col" :class="{ 'select-none': isResizing }">
    <!-- Resize handle (only when open) -->
    <div
      v-if="isOpen"
      class="h-3 cursor-ns-resize group shrink-0 flex items-center justify-center relative"
      @mousedown.prevent="startResize"
      @touchstart.prevent="startResize"
    >
      <!-- Invisible wider hit area -->
      <div class="absolute inset-x-0 -top-1 -bottom-1" />
      <div class="h-0.5 rounded-full bg-border group-hover:bg-primary/50 group-active:bg-primary transition-colors w-16" />
    </div>

    <!-- Toggle bar -->
    <div class="w-full flex items-center justify-between px-4 py-1.5 text-xs shrink-0">
      <button
        class="flex items-center gap-2 hover:bg-accent/50 transition-colors cursor-pointer select-none rounded px-1 -mx-1"
        :aria-expanded="isOpen"
        aria-label="Toggle event log panel (E)"
        title="Toggle event log (E)"
        @click="toggleOpen"
      >
        <!-- Terminal/console icon -->
        <svg
          class="size-3.5 text-muted-foreground"
          :class="{ 'text-foreground': isOpen }"
          xmlns="http://www.w3.org/2000/svg"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <polyline points="4 17 10 11 4 5" />
          <line x1="12" x2="20" y1="19" y2="19" />
        </svg>
        <span class="font-semibold text-muted-foreground uppercase tracking-wider">Event Log</span>
        <!-- Total count badge -->
        <Badge
          v-if="entryCount > 0"
          variant="secondary"
          class="h-4 min-w-5 px-1 text-[10px] font-semibold tabular-nums"
        >
          {{ entryCount }}
        </Badge>
        <!-- Unread badge when panel is closed or user scrolled up -->
        <span
          v-if="unreadCount > 0"
          class="inline-flex items-center rounded-full px-1.5 py-0 text-[10px] font-bold bg-primary text-primary-foreground animate-pulse"
        >
          +{{ unreadCount }}
        </span>
        <!-- Live SSE indicator dot -->
        <span
          v-if="sseActive"
          class="size-1.5 rounded-full bg-green-500 animate-ping"
          title="Live event received"
        />
      </button>
      <div class="flex items-center gap-2">
        <Button
          v-if="isOpen && entries.length > 0"
          variant="ghost"
          size="sm"
          class="h-5 px-2 text-[10px] text-muted-foreground hover:text-foreground"
          @click="clearLog"
        >
          <XCircle class="size-3.5" /> Clear
        </Button>
        <!-- Chevron indicator -->
        <svg
          class="size-3 text-muted-foreground transition-transform duration-200"
          :class="isOpen ? 'rotate-180' : 'rotate-0'"
          xmlns="http://www.w3.org/2000/svg"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="m18 15-6-6-6 6" />
        </svg>
      </div>
    </div>

    <!-- Search + filter toolbar (shown when open and there are events) -->
    <div
      v-if="isOpen && (availableTypes.length > 1 || searchQuery)"
      class="flex flex-wrap items-center gap-1 px-4 pb-2 shrink-0"
    >
      <!-- Text search input -->
      <input
        v-model="searchQuery"
        type="search"
        placeholder="Search events…"
        class="h-5 rounded border border-border bg-background px-2 text-[10px] text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary/50 w-32"
        aria-label="Search event log"
      >
      <!-- Type filter chips -->
      <div
        v-if="availableTypes.length > 1"
        class="flex flex-wrap items-center gap-1"
        role="group"
        aria-label="Filter by event type"
      >
        <button
          v-if="activeTypes.size > 0 || searchQuery"
          class="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium border transition-colors bg-primary/10 text-primary border-primary/30 hover:bg-primary/20 cursor-pointer"
          @click="clearFilters"
        >
          All
        </button>
        <button
          v-for="[type, count] in availableTypes"
          :key="type"
          :class="[
            'inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide border transition-all cursor-pointer',
            activeTypes.size === 0 || activeTypes.has(type)
              ? getBadgeClass(type)
              : 'bg-muted/30 text-muted-foreground/40 border-border/30',
          ]"
          :aria-pressed="activeTypes.has(type)"
          @click="toggleTypeFilter(type)"
        >
          {{ type }}
          <span class="tabular-nums opacity-70 font-normal not-italic">{{ count }}</span>
        </button>
      </div>
    </div>

    <!-- Log content -->
    <div
      v-show="isOpen"
      ref="scrollContainer"
      class="overflow-y-auto font-mono text-xs leading-relaxed border-t shrink-0 pb-3 relative"
      :style="{ height: panelHeight + 'px' }"
      role="log"
      aria-live="polite"
      aria-label="Event log"
      @scroll="handleScroll"
    >
      <div v-if="filteredEntries.length === 0" class="text-center text-muted-foreground py-8 italic text-xs">
        {{ entries.length === 0 ? 'No events yet' : 'No events match the current filter' }}
      </div>
      <table v-else class="w-full">
        <tbody>
          <tr
            v-for="(entry, index) in filteredEntries"
            :key="entry.id"
            class="border-b border-border/50 hover:bg-accent/40 transition-colors cursor-pointer"
            :class="index % 2 === 0 ? 'bg-background' : 'bg-muted/20'"
            tabindex="0"
            role="row"
            :aria-expanded="expandedId === entry.id"
            @click="expandedId = expandedId === entry.id ? null : entry.id"
            @keydown.enter.prevent="expandedId = expandedId === entry.id ? null : entry.id"
            @keydown.space.prevent="expandedId = expandedId === entry.id ? null : entry.id"
          >
            <td
              class="py-0.5 pl-4 pr-2 text-muted-foreground whitespace-nowrap align-top w-[70px] tabular-nums"
              :title="entry.timestamp"
            >
              {{ formatRelative(entry.rawDate, tick) }}
            </td>
            <td class="py-0.5 px-2 align-top w-[80px]">
              <span
                :class="[
                  'inline-flex items-center rounded px-1.5 py-0 text-[10px] font-semibold uppercase tracking-wide border',
                  getBadgeClass(entry.type),
                ]"
              >
                {{ entry.type }}
              </span>
            </td>
            <td class="py-0.5 px-2 pr-4 text-foreground/80 max-w-0 w-full" @click.stop="handleLogClick">
              <div
                v-if="expandedId === entry.id"
                class="py-1.5 whitespace-pre-wrap break-words leading-relaxed text-foreground/90 overflow-x-auto"
              >
                <div class="text-muted-foreground text-[9px] mb-1 uppercase tracking-wider flex items-center gap-2">
                  {{ entry.timestamp }} · {{ entry.source }}
                  <button
                    v-if="entry.type === 'message'"
                    class="inline-flex items-center gap-0.5 text-primary hover:underline text-[9px] font-medium"
                    title="Open conversation"
                    @click.stop="openConversation(entry)"
                  >
                    <MessageSquare class="size-2.5" />
                    open conversation
                  </button>
                </div>
                <!-- eslint-disable-next-line vue/no-v-html -->
                <span v-html="linkifyMessage(entry.message)" />
              </div>
              <div v-else class="flex items-center gap-1 min-w-0">
                <!-- eslint-disable-next-line vue/no-v-html -->
                <span class="truncate" v-html="linkifyMessage(entry.message)" />
                <button
                  v-if="entry.type === 'message'"
                  class="shrink-0 text-primary/60 hover:text-primary transition-colors"
                  title="Open conversation"
                  @click.stop="openConversation(entry)"
                >
                  <MessageSquare class="size-3" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>

      <!-- Sticky jump-to-bottom button inside scroll area -->
      <button
        v-if="!autoScroll"
        class="sticky bottom-3 float-right mr-3 inline-flex items-center gap-1 rounded-full bg-primary px-2.5 py-1 text-[10px] font-semibold text-primary-foreground shadow-lg hover:bg-primary/90 transition-colors cursor-pointer"
        aria-label="Jump to latest event"
        @click="jumpToBottom"
      >
        <ArrowDown class="size-3" />
        <span v-if="unreadCount > 0">{{ unreadCount }} new</span>
        <span v-else>Latest</span>
      </button>
    </div>
  </div>
</template>
