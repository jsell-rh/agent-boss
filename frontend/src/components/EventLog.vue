<script setup lang="ts">
import { ref, watch, nextTick, onMounted, onUnmounted, computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { api } from '@/api/client'
import { XCircle } from 'lucide-vue-next'

export interface EventLogEntry {
  id: number
  timestamp: string
  type: string
  message: string
  source: 'server' | 'sse'
}

const props = defineProps<{
  spaceName: string
}>()

const entries = ref<EventLogEntry[]>([])
const isOpen = ref(false)
const autoScroll = ref(true)
const scrollContainer = ref<HTMLElement | null>(null)
const panelHeight = ref(220)
const isResizing = ref(false)

const MIN_HEIGHT = 100
const MAX_HEIGHT = 600

let nextId = 0
let refreshTimer: ReturnType<typeof setInterval> | null = null

// Track which server-side log messages we've already seen (to avoid duplicates)
const seenServerMessages = new Set<string>()

// Parse "[HH:MM:SS] message" format from the server event log
function parseEventLogEntry(raw: string): EventLogEntry {
  const match = raw.match(/^\[([^\]]+)\]\s*(.*)/)
  if (match) {
    return {
      id: nextId++,
      timestamp: match[1]!,
      type: inferEventType(match[2]!),
      message: match[2]!,
      source: 'server',
    }
  }
  return {
    id: nextId++,
    timestamp: new Date().toLocaleTimeString('en-GB', { hour12: false }),
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
    tmux_liveness: 'tmux',
    broadcast_complete: 'broadcast',
    broadcast_progress: 'broadcast',
  }
  return map[sseType] || 'info'
}

const badgeStyles: Record<string, string> = {
  agent: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/20',
  approval: 'bg-red-500/15 text-red-600 dark:text-red-400 border-red-500/20',
  interrupt: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/20',
  broadcast: 'bg-cyan-500/15 text-cyan-600 dark:text-cyan-400 border-cyan-500/20',
  error: 'bg-red-500/15 text-red-600 dark:text-red-400 border-red-500/20',
  removed: 'bg-red-500/15 text-red-600 dark:text-red-400 border-red-500/20',
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

const entryCount = computed(() => entries.value.length)

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
      } else {
        // On refresh, only add new entries we haven't seen
        let added = false
        for (const r of raw) {
          if (!seenServerMessages.has(r)) {
            seenServerMessages.add(r)
            entries.value.push(parseEventLogEntry(r))
            added = true
          }
        }
        if (added && autoScroll.value) {
          scrollToBottom()
        }
      }
      // Cap entries
      if (entries.value.length > 500) {
        entries.value = entries.value.slice(-500)
      }
    }
  } catch {
    // Silently fail — events are non-critical
  }
}

// Start periodic refresh of server events (catches things SSE doesn't push)
function startRefresh() {
  stopRefresh()
  refreshTimer = setInterval(loadEvents, 3000)
}

function stopRefresh() {
  if (refreshTimer !== null) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}

// Public method: push a live SSE event into the log
function pushSSEEvent(sseType: string, summary: string) {
  const entry: EventLogEntry = {
    id: nextId++,
    timestamp: new Date().toLocaleTimeString('en-GB', { hour12: false }),
    type: sseEventToBadge(sseType),
    message: summary,
    source: 'sse',
  }
  entries.value.push(entry)
  if (entries.value.length > 500) {
    entries.value = entries.value.slice(-500)
  }
  if (autoScroll.value) {
    scrollToBottom()
  }
}

function clearLog() {
  entries.value = []
  seenServerMessages.clear()
}

function toggleOpen() {
  isOpen.value = !isOpen.value
  if (isOpen.value) {
    nextTick(scrollToBottom)
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

function handleScroll() {
  const el = scrollContainer.value
  if (!el) return
  const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
  autoScroll.value = atBottom
}

// ── Resize handling ──────────────────────────────────────────────
function startResize(e: MouseEvent) {
  e.preventDefault()
  isResizing.value = true
  const startY = e.clientY
  const startHeight = panelHeight.value

  function onMouseMove(moveEvent: MouseEvent) {
    // Dragging up increases height (mouse goes up = smaller Y = positive delta)
    const delta = startY - moveEvent.clientY
    panelHeight.value = Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, startHeight + delta))
  }

  function onMouseUp() {
    isResizing.value = false
    document.removeEventListener('mousemove', onMouseMove)
    document.removeEventListener('mouseup', onMouseUp)
  }

  document.addEventListener('mousemove', onMouseMove)
  document.addEventListener('mouseup', onMouseUp)
}

// Reload events when space changes
watch(() => props.spaceName, () => {
  entries.value = []
  seenServerMessages.clear()
  nextId = 0
  loadEvents()
})

onMounted(() => {
  loadEvents()
  startRefresh()
})

onUnmounted(() => {
  stopRefresh()
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
    >
      <!-- Invisible wider hit area -->
      <div class="absolute inset-x-0 -top-1 -bottom-1" />
      <div class="h-0.5 rounded-full bg-border group-hover:bg-primary/50 group-active:bg-primary transition-colors w-16" />
    </div>

    <!-- Toggle bar -->
    <button
      class="w-full flex items-center justify-between px-4 py-1.5 text-xs hover:bg-accent/50 transition-colors cursor-pointer select-none shrink-0"
      @click="toggleOpen"
    >
      <div class="flex items-center gap-2">
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
        <Badge
          v-if="entryCount > 0"
          variant="secondary"
          class="h-4 min-w-5 px-1 text-[10px] font-semibold tabular-nums"
        >
          {{ entryCount }}
        </Badge>
      </div>
      <div class="flex items-center gap-2">
        <button
          v-if="isOpen && !autoScroll"
          class="text-[10px] text-amber-500 font-medium hover:text-amber-400 cursor-pointer"
          @click.stop="autoScroll = true; scrollToBottom()"
        >
          Auto-scroll paused — click to resume
        </button>
        <Button
          v-if="isOpen && entryCount > 0"
          variant="ghost"
          size="sm"
          class="h-5 px-2 text-[10px] text-muted-foreground hover:text-foreground"
          @click.stop="clearLog"
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
    </button>

    <!-- Log content -->
    <div
      v-show="isOpen"
      ref="scrollContainer"
      class="overflow-y-auto font-mono text-xs leading-relaxed border-t shrink-0 pb-3"
      :style="{ height: panelHeight + 'px' }"
      @scroll="handleScroll"
    >
      <div v-if="entries.length === 0" class="text-center text-muted-foreground py-8 italic text-xs">
        No events yet
      </div>
      <table v-else class="w-full">
        <tbody>
          <tr
            v-for="entry in entries"
            :key="entry.id"
            class="border-b border-border/50 hover:bg-accent/30 transition-colors"
          >
            <td class="py-0.5 pl-4 pr-2 text-muted-foreground whitespace-nowrap align-top w-[70px]">
              {{ entry.timestamp }}
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
            <td class="py-0.5 px-2 pr-4 text-foreground/80 break-words">
              {{ entry.message }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
