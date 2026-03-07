<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import type { StatusSnapshot, AgentUpdate } from '@/types'
import api from '@/api/client'
import AgentProfileCard from './AgentProfileCard.vue'

const props = defineProps<{
  spaceName: string
  agents?: Record<string, AgentUpdate>
}>()

const emit = defineEmits<{
  'select-agent': [name: string]
}>()

// Window options in hours
const WINDOW_OPTIONS = [1, 2, 4, 8]
const windowHours = ref(2)
const snapshots = ref<StatusSnapshot[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

async function load() {
  loading.value = true
  error.value = null
  try {
    const windowMs = windowHours.value * 3600 * 1000
    snapshots.value = await api.fetchHistory(props.spaceName, windowMs)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

// Group snapshots by agent and build bar segments
interface Segment {
  status: string
  startPct: number
  widthPct: number
  startTime: Date
  endTime: Date
  stale: boolean
}

const ganttRows = computed(() => {
  const windowMs = windowHours.value * 3600 * 1000
  const now = Date.now()
  const windowStart = now - windowMs

  // Group by agent
  const byAgent: Record<string, StatusSnapshot[]> = {}
  for (const snap of snapshots.value) {
    const name = snap.agent_name
    if (!byAgent[name]) byAgent[name] = []
    byAgent[name].push(snap)
  }

  return Object.entries(byAgent)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([agent, snaps]) => {
      // Sort by timestamp ascending
      const sorted = [...snaps].sort(
        (a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
      )

      const segments: Segment[] = []
      for (let i = 0; i < sorted.length; i++) {
        const snap = sorted[i]!
        const snapTs = new Date(snap.timestamp).getTime()
        const next = sorted[i + 1]
        const nextTs = next !== undefined ? new Date(next.timestamp).getTime() : now

        // Clamp to window
        const segStart = Math.max(snapTs, windowStart)
        const segEnd = Math.min(nextTs, now)
        if (segEnd <= segStart) continue

        segments.push({
          status: snap.status,
          startPct: ((segStart - windowStart) / windowMs) * 100,
          widthPct: ((segEnd - segStart) / windowMs) * 100,
          startTime: new Date(segStart),
          endTime: new Date(segEnd),
          stale: snap.stale ?? false,
        })
      }

      return { agent, segments }
    })
    .filter((row) => row.segments.length > 0)
})

// Axis labels: evenly spaced time markers
const axisLabels = computed(() => {
  const windowMs = windowHours.value * 3600 * 1000
  const now = Date.now()
  const count = windowHours.value <= 2 ? 5 : 5
  return Array.from({ length: count }, (_, i) => {
    const ts = now - windowMs + (windowMs * i) / (count - 1)
    return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  })
})

// Color for a segment
function segmentClass(status: string, stale: boolean): string {
  if (stale) return 'bg-yellow-500/70'
  return {
    active: 'bg-green-500',
    blocked: 'bg-red-500',
    done: 'bg-teal-500',
    idle: 'bg-slate-400',
    error: 'bg-orange-500',
  }[status] ?? 'bg-slate-400'
}

// Tooltip state
const tooltip = ref<{
  visible: boolean
  x: number
  y: number
  status: string
  start: string
  end: string
  stale: boolean
}>({ visible: false, x: 0, y: 0, status: '', start: '', end: '', stale: false })

const TOOLTIP_W = 180
const TOOLTIP_H = 60

function clampedTooltipPos(clientX: number, clientY: number): { x: number; y: number } {
  const x = Math.min(clientX + 12, window.innerWidth - TOOLTIP_W - 8)
  const y = Math.min(clientY - 8, window.innerHeight - TOOLTIP_H - 8)
  return { x, y }
}

function showTooltip(e: MouseEvent, seg: Segment) {
  const fmt = (d: Date) =>
    d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  const { x, y } = clampedTooltipPos(e.clientX, e.clientY)
  tooltip.value = {
    visible: true,
    x,
    y,
    status: seg.status,
    start: fmt(seg.startTime),
    end: fmt(seg.endTime),
    stale: seg.stale,
  }
}

function hideTooltip() {
  tooltip.value.visible = false
}

function moveTooltip(e: MouseEvent) {
  const { x, y } = clampedTooltipPos(e.clientX, e.clientY)
  tooltip.value.x = x
  tooltip.value.y = y
}

watch([() => props.spaceName, windowHours], () => load(), { immediate: false })

onMounted(() => {
  load()
  pollTimer = setInterval(load, 30_000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="space-y-3">
    <!-- Header row: window selector -->
    <div class="flex items-center justify-between">
      <span class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        Agent Timeline
      </span>
      <div class="flex gap-1">
        <button
          v-for="h in WINDOW_OPTIONS"
          :key="h"
          class="rounded px-2 py-0.5 text-[11px] border transition-colors"
          :class="
            windowHours === h
              ? 'bg-primary/10 border-primary/40 text-primary font-semibold'
              : 'bg-muted border-border text-muted-foreground hover:text-foreground'
          "
          @click="windowHours = h"
        >
          {{ h }}h
        </button>
      </div>
    </div>

    <!-- Loading / error -->
    <div v-if="loading && ganttRows.length === 0" class="py-6 text-center text-xs text-muted-foreground">
      Loading history…
    </div>
    <div v-else-if="error" class="py-4 text-center text-xs text-destructive">
      {{ error }}
    </div>
    <div v-else-if="ganttRows.length === 0" class="py-6 text-center text-xs text-muted-foreground italic">
      No status history in the last {{ windowHours }}h.
    </div>

    <!-- Gantt rows -->
    <div v-else class="space-y-1">
      <div
        v-for="row in ganttRows"
        :key="row.agent"
        class="grid items-center gap-2"
        style="grid-template-columns: 88px 1fr"
      >
        <AgentProfileCard
          :agent-name="row.agent"
          :agent="agents?.[row.agent]"
          @select-agent="emit('select-agent', $event)"
        >
          <button
            class="truncate text-[11px] font-semibold text-muted-foreground text-right pr-1 hover:text-primary transition-colors cursor-pointer w-full"
            :title="`View ${row.agent}`"
            @click="emit('select-agent', row.agent)"
          >
            {{ row.agent }}
          </button>
        </AgentProfileCard>
        <div class="relative h-4 rounded bg-muted border border-border overflow-hidden">
          <div
            v-for="(seg, si) in row.segments"
            :key="si"
            class="absolute top-0 h-full cursor-pointer transition-opacity hover:opacity-75"
            :class="segmentClass(seg.status, seg.stale)"
            :style="`left:${seg.startPct}%;width:${seg.widthPct}%`"
            @mouseenter="(e) => showTooltip(e, seg)"
            @mousemove="moveTooltip"
            @mouseleave="hideTooltip"
          />
        </div>
      </div>

      <!-- Time axis -->
      <div class="grid gap-2 mt-1" style="grid-template-columns: 88px 1fr">
        <div />
        <div class="flex justify-between">
          <span
            v-for="(label, i) in axisLabels"
            :key="i"
            class="text-[10px] text-muted-foreground tabular-nums"
          >
            {{ label }}
          </span>
        </div>
      </div>
    </div>

    <!-- Legend -->
    <div class="flex flex-wrap gap-3 pt-1">
      <div v-for="[status, cls] in [
        ['active', 'bg-green-500'],
        ['blocked', 'bg-red-500'],
        ['done', 'bg-teal-500'],
        ['idle', 'bg-slate-400'],
        ['error', 'bg-orange-500'],
        ['stale', 'bg-yellow-500/70'],
      ]" :key="status" class="flex items-center gap-1">
        <span class="inline-block h-2.5 w-2.5 rounded-sm" :class="cls" />
        <span class="text-[10px] text-muted-foreground capitalize">{{ status }}</span>
      </div>
    </div>
  </div>

  <!-- Tooltip portal -->
  <Teleport to="body">
    <div
      v-if="tooltip.visible"
      class="fixed z-50 pointer-events-none rounded border bg-popover px-2.5 py-2 text-[11px] shadow-lg"
      :style="`left:${tooltip.x}px;top:${tooltip.y}px`"
    >
      <div class="font-semibold capitalize mb-0.5" :class="tooltip.stale ? 'text-yellow-500' : ''">
        {{ tooltip.status }}{{ tooltip.stale ? ' (stale)' : '' }}
      </div>
      <div class="text-muted-foreground">{{ tooltip.start }} – {{ tooltip.end }}</div>
    </div>
  </Teleport>
</template>
