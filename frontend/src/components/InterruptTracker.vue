<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import type { Interrupt, InterruptMetrics } from '@/types'
import { api } from '@/api/client'
import { relativeTime, formatFullDate, formatWaitTime } from '@/composables/useTime'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { RefreshCw, ShieldCheck, CornerDownLeft, Clock, CheckCheck, X } from 'lucide-vue-next'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

const props = defineProps<{
  spaceName: string
}>()

const interrupts = ref<Interrupt[]>([])
const metrics = ref<InterruptMetrics | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const showAll = ref(false)
const lastFetched = ref<Date | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

// Per-interrupt reply text for decision types
const replyTexts = ref<Record<string, string>>({})
// Track in-flight actions
const acting = ref<Record<string, boolean>>({})
// Action feedback messages
const actionFeedback = ref<Record<string, { ok: boolean; msg: string }>>({})
// Approve confirmation dialog state
const approveDialogOpen = ref(false)
const approveDialogItem = ref<Interrupt | null>(null)
// Mark all resolved dialog state
const markAllDialogOpen = ref(false)
const markingAll = ref(false)

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
    lastFetched.value = new Date()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to fetch inbox'
    interrupts.value = []
    metrics.value = null
  } finally {
    loading.value = false
  }
}

// Expose pending count and refresh for parent (SSE-triggered refresh)
const pendingCount = computed(() => {
  return interrupts.value.filter(i => !i.resolution).length
})

defineExpose({ pendingCount, refresh: fetchData })

const totalCount = computed(() => interrupts.value.length)

const filteredInterrupts = computed(() => {
  const sorted = [...interrupts.value].sort((a, b) => {
    // Pending first
    const aPending = !a.resolution
    const bPending = !b.resolution
    if (aPending !== bPending) return aPending ? -1 : 1
    if (aPending && bPending) {
      // Oldest pending first — they've waited longest and are most urgent
      return new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
    }
    // Resolved: newest first
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

const lastFetchedLabel = computed(() => {
  if (!lastFetched.value) return ''
  const seconds = Math.floor((Date.now() - lastFetched.value.getTime()) / 1000)
  if (seconds < 5) return 'just now'
  if (seconds < 60) return `${seconds}s ago`
  return `${Math.floor(seconds / 60)}m ago`
})

function waitingLabel(dateStr: string): string {
  const now = Date.now()
  const then = new Date(dateStr).getTime()
  const seconds = Math.max(0, Math.floor((now - then) / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ${minutes % 60}m`
}

// Returns additional CSS classes for a pending item based on how long it has waited
function urgencyClass(item: Interrupt): string {
  if (item.resolution) return ''
  const minutes = (Date.now() - new Date(item.created_at).getTime()) / 60000
  if (minutes > 15) return 'border-orange-500/40 bg-orange-500/8'
  if (minutes > 5) return 'border-amber-500/40 bg-amber-500/8'
  return ''
}

// Returns a label for the urgency level of a pending item
function urgencyLevel(item: Interrupt): 'critical' | 'high' | 'normal' | null {
  if (item.resolution) return null
  const minutes = (Date.now() - new Date(item.created_at).getTime()) / 60000
  if (minutes > 15) return 'critical'
  if (minutes > 5) return 'high'
  return 'normal'
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

function requestApprove(item: Interrupt) {
  approveDialogItem.value = item
  approveDialogOpen.value = true
}

async function confirmApprove() {
  const item = approveDialogItem.value
  approveDialogOpen.value = false
  approveDialogItem.value = null
  if (!item) return
  await handleApprove(item)
}

async function handleApprove(item: Interrupt) {
  acting.value[item.id] = true
  actionFeedback.value[item.id] = { ok: true, msg: '' }
  try {
    await api.approveAgent(props.spaceName, item.agent)
    actionFeedback.value[item.id] = { ok: true, msg: 'Approved' }
    setTimeout(() => {
      if (actionFeedback.value[item.id]?.msg === 'Approved') {
        delete actionFeedback.value[item.id]
      }
    }, 3000)
    setTimeout(() => fetchData(), 500)
  } catch (err) {
    actionFeedback.value[item.id] = {
      ok: false,
      msg: err instanceof Error ? err.message : 'Approve failed',
    }
    setTimeout(() => {
      delete actionFeedback.value[item.id]
    }, 3000)
  } finally {
    acting.value[item.id] = false
  }
}

async function handleDismiss(item: Interrupt) {
  acting.value[item.id] = true
  actionFeedback.value[item.id] = { ok: true, msg: '' }
  try {
    await api.resolveInterrupt(props.spaceName, item.id, 'dismissed')
    actionFeedback.value[item.id] = { ok: true, msg: 'Dismissed' }
    // Optimistically update local state
    interrupts.value = interrupts.value.map(i => {
      if (i.id === item.id) {
        return {
          ...i,
          resolution: {
            resolved_by: 'human',
            answer: 'dismissed',
            resolved_at: new Date().toISOString(),
            wait_seconds: (Date.now() - new Date(i.created_at).getTime()) / 1000,
          },
        }
      }
      return i
    })
    setTimeout(() => fetchData(), 500)
  } catch (err) {
    actionFeedback.value[item.id] = {
      ok: false,
      msg: err instanceof Error ? err.message : 'Dismiss failed',
    }
    setTimeout(() => {
      delete actionFeedback.value[item.id]
    }, 3000)
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
    await api.sendMessage(props.spaceName, item.agent, text, 'boss')
    // Resolve the decision interrupt after replying
    await api.resolveInterrupt(props.spaceName, item.id, text)
    replyTexts.value[item.id] = ''
    actionFeedback.value[item.id] = { ok: true, msg: 'Sent' }
    setTimeout(() => {
      if (actionFeedback.value[item.id]?.msg === 'Sent') {
        delete actionFeedback.value[item.id]
      }
    }, 3000)
    setTimeout(() => fetchData(), 500)
  } catch (err) {
    actionFeedback.value[item.id] = {
      ok: false,
      msg: err instanceof Error ? err.message : 'Reply failed',
    }
    setTimeout(() => {
      delete actionFeedback.value[item.id]
    }, 3000)
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

async function confirmMarkAllResolved() {
  markAllDialogOpen.value = false
  markingAll.value = true
  try {
    const pending = interrupts.value.filter(i => !i.resolution)
    await Promise.allSettled([
      ...pending
        .filter(item => item.type === 'approval')
        .map(item => api.approveAgent(props.spaceName, item.agent)),
      ...pending
        .filter(item => item.type !== 'approval')
        .map(item => api.resolveInterrupt(props.spaceName, item.id, 'Bulk resolved')),
    ])
    // Optimistically mark all pending items as resolved locally
    const resolvedAt = new Date().toISOString()
    interrupts.value = interrupts.value.map(i => {
      if (!i.resolution) {
        return {
          ...i,
          resolution: {
            resolved_by: 'human',
            answer: 'Bulk resolved',
            resolved_at: resolvedAt,
            wait_seconds: (Date.now() - new Date(i.created_at).getTime()) / 1000,
          },
        }
      }
      return i
    })
    setTimeout(() => fetchData(), 1000)
  } finally {
    markingAll.value = false
  }
}

function startPolling() {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = setInterval(fetchData, 15000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

// Refetch when the space changes
watch(() => props.spaceName, () => {
  interrupts.value = []
  metrics.value = null
  replyTexts.value = {}
  acting.value = {}
  actionFeedback.value = {}
  showAll.value = false
  lastFetched.value = null
  fetchData()
})

onMounted(() => {
  fetchData()
  startPolling()
})

onUnmounted(stopPolling)
</script>

<template>
  <div class="space-y-4">
    <!-- Header with refresh and bulk action -->
    <div class="flex items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <h2 class="text-lg font-semibold tracking-tight">Inbox</h2>
        <span v-if="lastFetched" class="text-xs text-muted-foreground font-text tabular-nums">
          &middot; {{ lastFetchedLabel }}
        </span>
      </div>
      <div class="flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          :disabled="pendingCount === 0 || markingAll"
          @click="markAllDialogOpen = true"
        >
          <CheckCheck class="size-4" />
          Mark all resolved
        </Button>
        <Button variant="outline" size="sm" :disabled="loading" @click="fetchData">
          <RefreshCw :class="['size-4', loading && 'animate-spin']" />
          Refresh
        </Button>
      </div>
    </div>

    <!-- Summary bar (Card) -->
    <Card v-if="metrics && !error">
      <CardContent class="flex items-center gap-4 px-4 py-3 text-sm font-text">
        <div class="flex items-center gap-1.5">
          <span :class="['text-xl font-bold tabular-nums leading-none', pendingCount > 0 ? 'text-destructive' : 'text-foreground']">
            {{ pendingCount }}
          </span>
          <span class="text-muted-foreground text-xs">pending</span>
        </div>
        <div class="w-px h-6 bg-border" />
        <div class="flex items-center gap-1.5">
          <span class="text-xl font-bold tabular-nums leading-none">{{ resolvedTodayCount }}</span>
          <span class="text-muted-foreground text-xs">resolved today</span>
        </div>
        <div class="w-px h-6 bg-border" />
        <div class="flex items-center gap-1.5">
          <Clock class="size-3.5 text-muted-foreground" />
          <span class="text-muted-foreground text-xs">Avg wait:</span>
          <span class="font-semibold tabular-nums">{{ formatWaitTime(metrics.avg_wait_seconds) }}</span>
        </div>
      </CardContent>
    </Card>

    <!-- Filter toggle with counts -->
    <div class="flex items-center gap-2" role="group" aria-label="Filter interrupts">
      <Button
        :variant="!showAll ? 'default' : 'outline'"
        size="sm"
        :aria-pressed="!showAll"
        @click="showAll = false"
      >
        Pending<span v-if="pendingCount > 0" class="ml-1 tabular-nums">({{ pendingCount }})</span>
      </Button>
      <Button
        :variant="showAll ? 'default' : 'outline'"
        size="sm"
        :aria-pressed="showAll"
        @click="showAll = true"
      >
        All<span v-if="totalCount > 0" class="ml-1 tabular-nums">({{ totalCount }})</span>
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
        <template v-else>No pending items &mdash; all clear</template>
      </div>

      <div class="space-y-2">
        <Card
          v-for="item in filteredInterrupts"
          :key="item.id"
          :class="[
            'transition-colors',
            item.resolution ? 'opacity-60' : '',
            urgencyClass(item),
          ]"
          :role="item.resolution ? undefined : 'alert'"
        >
          <CardContent class="p-4 space-y-2">
            <!-- Top row: agent badge, type badge, urgency wait time, timestamp -->
            <div class="flex items-center gap-2 flex-wrap">
              <Badge variant="secondary" class="font-mono text-xs shrink-0">
                {{ item.agent }}
              </Badge>
              <Badge variant="outline" :class="['text-xs capitalize shrink-0', typeColor(item.type)]">
                {{ item.type }}
              </Badge>

              <!-- Urgency wait time badge for pending items -->
              <template v-if="!item.resolution">
                <Badge
                  v-if="urgencyLevel(item) === 'critical'"
                  class="text-xs shrink-0 gap-1 bg-orange-500/15 text-orange-500 border-orange-500/30"
                  variant="outline"
                >
                  <Clock class="size-3" />
                  {{ waitingLabel(item.created_at) }} waiting
                </Badge>
                <Badge
                  v-else-if="urgencyLevel(item) === 'high'"
                  class="text-xs shrink-0 gap-1 bg-amber-500/15 text-amber-500 border-amber-500/30"
                  variant="outline"
                >
                  <Clock class="size-3" />
                  {{ waitingLabel(item.created_at) }} waiting
                </Badge>
                <span
                  v-else
                  class="text-xs text-muted-foreground font-text tabular-nums"
                >
                  {{ waitingLabel(item.created_at) }} waiting
                </span>
              </template>

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
            <div v-if="!item.resolution" class="pt-1 space-y-2">
              <!-- Approval type: confirm then approve + dismiss -->
              <div v-if="item.type === 'approval'" class="flex items-center gap-2">
                <Button
                  size="sm"
                  :disabled="acting[item.id]"
                  @click="requestApprove(item)"
                >
                  <ShieldCheck class="size-4" /> Approve
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  :disabled="acting[item.id]"
                  class="text-muted-foreground hover:text-foreground"
                  @click="handleDismiss(item)"
                >
                  <X class="size-4" /> Dismiss
                </Button>
              </div>

              <!-- Decision type: inline reply textarea + dismiss -->
              <div v-else-if="item.type === 'decision'" class="space-y-1.5">
                <Textarea
                  :model-value="replyTexts[item.id] ?? ''"
                  placeholder="Type reply... (Enter to send, Shift+Enter for newline)"
                  class="min-h-[60px] text-sm font-text resize-y"
                  :disabled="acting[item.id]"
                  @update:model-value="(v: string | number) => replyTexts[item.id] = String(v)"
                  @keydown="handleReplyKeydown($event, item)"
                />
                <div class="flex items-center gap-2">
                  <Button
                    size="sm"
                    :disabled="acting[item.id] || !(replyTexts[item.id] ?? '').trim()"
                    @click="handleReply(item)"
                  >
                    <CornerDownLeft class="size-4" /> {{ acting[item.id] ? 'Sending...' : 'Reply' }}
                  </Button>
                  <span class="text-xs text-muted-foreground font-text">Enter to send</span>
                  <Button
                    variant="ghost"
                    size="sm"
                    :disabled="acting[item.id]"
                    class="ml-auto text-muted-foreground hover:text-foreground"
                    @click="handleDismiss(item)"
                  >
                    <X class="size-4" /> Dismiss
                  </Button>
                </div>
              </div>

              <!-- All other types (staleness, review, sequencing): dismiss -->
              <div v-else class="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  :disabled="acting[item.id]"
                  @click="handleDismiss(item)"
                >
                  <X class="size-4" /> {{ acting[item.id] ? 'Dismissing...' : 'Dismiss' }}
                </Button>
              </div>

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
              <p v-if="item.resolution.answer && item.resolution.answer !== 'dismissed'" class="text-foreground/70 italic">
                {{ item.resolution.answer }}
              </p>
            </div>
          </CardContent>
        </Card>
      </div>
    </template>
  </div>

  <!-- Approve confirmation dialog -->
  <Dialog :open="approveDialogOpen" @update:open="approveDialogOpen = $event">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>Confirm Approval</DialogTitle>
        <DialogDescription>
          This will send an approval signal to agent
          <span class="font-mono font-semibold">{{ approveDialogItem?.agent }}</span>.
          This action cannot be undone.
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" @click="approveDialogOpen = false">Cancel</Button>
        <Button @click="confirmApprove">
          <ShieldCheck class="size-4" /> Confirm Approve
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <!-- Mark all resolved confirmation dialog -->
  <Dialog :open="markAllDialogOpen" @update:open="markAllDialogOpen = $event">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>Mark all resolved?</DialogTitle>
        <DialogDescription>
          This will resolve all {{ pendingCount }} pending item{{ pendingCount === 1 ? '' : 's' }}.
          Approval-type items will send an approval signal to the agent.
          This action cannot be undone.
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" @click="markAllDialogOpen = false">Cancel</Button>
        <Button @click="confirmMarkAllResolved">
          <CheckCheck class="size-4" /> Resolve all
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
