<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import type { Interrupt, InterruptMetrics } from '@/types'
import { api } from '@/api/client'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { RefreshCw, ShieldCheck, CornerDownLeft } from 'lucide-vue-next'

const props = defineProps<{
  spaceName: string
}>()

const interrupts = ref<Interrupt[]>([])
const metrics = ref<InterruptMetrics | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const showAll = ref(false)

// Per-interrupt reply text for decision types
const replyTexts = ref<Record<string, string>>({})
// Track in-flight actions
const acting = ref<Record<string, boolean>>({})
// Action feedback messages
const actionFeedback = ref<Record<string, { ok: boolean; msg: string }>>({})

async function fetchData() {
  loading.value = true
  error.value = null
  try {
    const [interruptList, metricData] = await Promise.all([
      api.fetchInterrupts(props.spaceName),
      api.fetchMetrics(props.spaceName),
    ])
    interrupts.value = interruptList
    metrics.value = metricData
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to fetch inbox'
    interrupts.value = []
    metrics.value = null
  } finally {
    loading.value = false
  }
}

// Expose pending count for parent components
const pendingCount = computed(() => {
  return interrupts.value.filter(i => !i.resolution).length
})

defineExpose({ pendingCount, refresh: fetchData })

const filteredInterrupts = computed(() => {
  const sorted = [...interrupts.value].sort((a, b) => {
    // Pending first, then by creation time descending
    const aPending = !a.resolution
    const bPending = !b.resolution
    if (aPending !== bPending) return aPending ? -1 : 1
    return new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
  })
  if (showAll.value) return sorted
  return sorted.filter(i => !i.resolution)
})

const resolvedTodayCount = computed(() => {
  const todayStart = new Date()
  todayStart.setHours(0, 0, 0, 0)
  return interrupts.value.filter(i => {
    if (!i.resolution?.resolved_at) return false
    return new Date(i.resolution.resolved_at).getTime() >= todayStart.getTime()
  }).length
})

function formatWaitTime(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}s`
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`
  return `${(seconds / 3600).toFixed(1)}h`
}

function relativeTime(dateStr: string): string {
  const now = Date.now()
  const then = new Date(dateStr).getTime()
  const diff = now - then
  if (diff < 0) return 'just now'
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

function formatFullDate(dateStr: string): string {
  return new Date(dateStr).toLocaleString()
}

const typeColors: Record<string, string> = {
  decision: 'bg-primary/15 text-primary border-primary/30',
  approval: 'bg-amber-500/15 text-amber-500 border-amber-500/30',
  staleness: 'bg-muted text-muted-foreground border-border',
  review: 'bg-blue-500/15 text-blue-400 border-blue-500/30',
  sequencing: 'bg-teal-500/15 text-teal-500 border-teal-500/30',
}

function typeColor(type: string): string {
  return typeColors[type] ?? 'bg-muted text-muted-foreground border-border'
}

async function handleApprove(item: Interrupt) {
  acting.value[item.id] = true
  actionFeedback.value[item.id] = { ok: true, msg: '' }
  try {
    await api.approveAgent(props.spaceName, item.agent)
    actionFeedback.value[item.id] = { ok: true, msg: 'Approved' }
    // Refresh data after a short delay to let backend process
    setTimeout(() => fetchData(), 500)
  } catch (err) {
    actionFeedback.value[item.id] = {
      ok: false,
      msg: err instanceof Error ? err.message : 'Approve failed',
    }
  } finally {
    acting.value[item.id] = false
  }
}

async function handleReply(item: Interrupt) {
  const text = (replyTexts.value[item.id] ?? '').trim()
  if (!text) return
  acting.value[item.id] = true
  actionFeedback.value[item.id] = { ok: true, msg: '' }
  try {
    await api.replyToAgent(props.spaceName, item.agent, text)
    replyTexts.value[item.id] = ''
    actionFeedback.value[item.id] = { ok: true, msg: 'Sent' }
    setTimeout(() => fetchData(), 500)
  } catch (err) {
    actionFeedback.value[item.id] = {
      ok: false,
      msg: err instanceof Error ? err.message : 'Reply failed',
    }
  } finally {
    acting.value[item.id] = false
  }
}

function handleReplyKeydown(e: KeyboardEvent, item: Interrupt) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleReply(item)
  }
}

onMounted(fetchData)
</script>

<template>
  <div class="space-y-4">
    <!-- Header with refresh -->
    <div class="flex items-center justify-between">
      <h2 class="text-lg font-semibold tracking-tight">Inbox</h2>
      <Button variant="outline" size="sm" :disabled="loading" @click="fetchData">
        <RefreshCw :class="['size-4', loading && 'animate-spin']" />
        Refresh
      </Button>
    </div>

    <!-- Summary bar -->
    <div
      v-if="metrics && !error"
      class="flex items-center gap-4 rounded-md border bg-muted/30 px-4 py-2.5 text-sm font-text"
    >
      <span>
        <span :class="['font-semibold tabular-nums', pendingCount > 0 ? 'text-destructive' : '']">{{ pendingCount }}</span>
        <span class="text-muted-foreground ml-1">pending</span>
      </span>
      <span class="text-border">|</span>
      <span>
        <span class="font-semibold tabular-nums">{{ resolvedTodayCount }}</span>
        <span class="text-muted-foreground ml-1">resolved today</span>
      </span>
      <span class="text-border">|</span>
      <span>
        <span class="text-muted-foreground">Avg wait:</span>
        <span class="font-semibold tabular-nums ml-1">{{ formatWaitTime(metrics.avg_wait_seconds) }}</span>
      </span>
    </div>

    <!-- Filter toggle -->
    <div class="flex items-center gap-2">
      <Button
        :variant="!showAll ? 'default' : 'outline'"
        size="sm"
        @click="showAll = false"
      >
        Pending
      </Button>
      <Button
        :variant="showAll ? 'default' : 'outline'"
        size="sm"
        @click="showAll = true"
      >
        All
      </Button>
    </div>

    <!-- Loading -->
    <div v-if="loading && interrupts.length === 0" class="flex items-center justify-center py-12 text-muted-foreground font-text">
      <div class="h-6 w-6 animate-spin rounded-full border-2 border-muted-foreground border-t-primary" role="status">
        <span class="sr-only">Loading inbox...</span>
      </div>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="rounded-md border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive font-text">
      {{ error }}
    </div>

    <!-- Interrupt list -->
    <template v-else>
      <div v-if="filteredInterrupts.length === 0" class="text-center py-8 text-muted-foreground font-text text-sm">
        <template v-if="showAll">No interrupts recorded yet</template>
        <template v-else>No pending items — all clear</template>
      </div>

      <div class="space-y-2">
        <Card
          v-for="item in filteredInterrupts"
          :key="item.id"
          :class="[
            'transition-colors',
            item.resolution ? 'opacity-60' : '',
          ]"
          :role="item.resolution ? undefined : 'alert'"
        >
          <CardContent class="p-4 space-y-2">
            <!-- Top row: agent badge, type badge, timestamp -->
            <div class="flex items-center gap-2 flex-wrap">
              <Badge variant="secondary" class="font-mono text-xs shrink-0">
                {{ item.agent }}
              </Badge>
              <Badge variant="outline" :class="['text-xs capitalize shrink-0', typeColor(item.type)]">
                {{ item.type }}
              </Badge>
              <div class="ml-auto shrink-0">
                <Tooltip>
                  <TooltipTrigger as-child>
                    <span class="text-xs text-muted-foreground font-text cursor-default tabular-nums">
                      {{ relativeTime(item.created_at) }}
                    </span>
                  </TooltipTrigger>
                  <TooltipContent>{{ formatFullDate(item.created_at) }}</TooltipContent>
                </Tooltip>
              </div>
            </div>

            <!-- Question / content -->
            <p class="text-sm font-text leading-relaxed">
              {{ item.question }}
            </p>

            <!-- Context details (tool name etc.) -->
            <div v-if="item.context && Object.keys(item.context).length > 0" class="flex items-center gap-2 flex-wrap">
              <span
                v-for="(val, key) in item.context"
                :key="key"
                class="text-xs text-muted-foreground font-text"
              >
                <span class="font-mono">{{ key }}:</span> {{ val }}
              </span>
            </div>

            <!-- Actions for PENDING items -->
            <div v-if="!item.resolution" class="flex items-center gap-2 pt-1">
              <!-- Approval type: one-click approve -->
              <template v-if="item.type === 'approval'">
                <Button
                  size="sm"
                  :disabled="acting[item.id]"
                  @click="handleApprove(item)"
                >
                  <ShieldCheck class="size-4" /> Approve
                </Button>
              </template>

              <!-- Decision type: inline reply -->
              <template v-if="item.type === 'decision'">
                <Input
                  :model-value="replyTexts[item.id] ?? ''"
                  placeholder="Type reply..."
                  class="flex-1 h-8 text-sm font-text"
                  @update:model-value="(v: string | number) => replyTexts[item.id] = String(v)"
                  @keydown="handleReplyKeydown($event, item)"
                />
                <Button
                  size="sm"
                  variant="outline"
                  :disabled="acting[item.id] || !(replyTexts[item.id] ?? '').trim()"
                  @click="handleReply(item)"
                >
                  <CornerDownLeft class="size-4" /> Reply
                </Button>
              </template>

              <!-- Feedback -->
              <span
                v-if="actionFeedback[item.id]?.msg"
                :class="['text-xs font-text', actionFeedback[item.id]?.ok ? 'text-teal-500' : 'text-destructive']"
              >
                {{ actionFeedback[item.id]?.msg }}
              </span>
            </div>

            <!-- Resolved state -->
            <div v-if="item.resolution" class="text-xs text-muted-foreground font-text pt-1 space-y-0.5">
              <div class="flex items-center gap-2">
                <span>Resolved by <span class="font-medium text-foreground">{{ item.resolution.resolved_by || 'auto' }}</span></span>
                <span class="text-border">|</span>
                <Tooltip>
                  <TooltipTrigger as-child>
                    <span class="cursor-default tabular-nums">{{ relativeTime(item.resolution.resolved_at) }}</span>
                  </TooltipTrigger>
                  <TooltipContent>{{ formatFullDate(item.resolution.resolved_at) }}</TooltipContent>
                </Tooltip>
                <span class="text-border">|</span>
                <span class="tabular-nums">waited {{ formatWaitTime(item.resolution.wait_seconds) }}</span>
              </div>
              <p v-if="item.resolution.answer" class="text-foreground/70 italic">
                {{ item.resolution.answer }}
              </p>
            </div>
          </CardContent>
        </Card>
      </div>
    </template>
  </div>
</template>
