<script setup lang="ts">
import type { AgentUpdate, TmuxAgentStatus, TmuxDisplayState, IntrospectResponse } from '@/types'
import { TMUX_STATUS_DISPLAY, getTmuxDisplayState } from '@/types'
import { ref, computed, watch, onUnmounted } from 'vue'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Separator } from '@/components/ui/separator'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Bell, Trash2, ShieldCheck, Terminal, ChevronRight, X, HelpCircle, AlertTriangle, MessageSquareReply, Play, Square, RotateCcw, Search, Loader2, CheckCircle2, XCircle, Radio } from 'lucide-vue-next'
import StatusBadge from './StatusBadge.vue'
import AgentMessages from './AgentMessages.vue'
import AgentAvatar from './AgentAvatar.vue'
import { relativeTime, formatFullDate } from '@/composables/useTime'
import { renderMarkdown, renderMarkdownInline } from '@/lib/markdown'
import api from '@/api/client'

const props = defineProps<{
  agent: AgentUpdate
  agentName: string
  spaceName: string
  tmuxStatus: TmuxAgentStatus | null
}>()

const emit = defineEmits<{
  approve: []
  reply: [text: string]
  broadcast: []
  delete: []
  'dismiss-question': [index: number]
  'dismiss-blocker': [index: number]
  'send-message': [text: string, sender: string]
  'reply-to-question': [agentName: string, questionIndex: number, questionText: string, replyText: string]
  'reply-to-blocker': [agentName: string, blockerIndex: number, blockerText: string, replyText: string]
  'select-agent': [name: string]
}>()

const replyText = ref('')
const tmuxInputOpen = ref(false)
const dismissDialogOpen = ref(false)
const dismissDialogIndex = ref<number | null>(null)
const dismissDialogType = ref<'question' | 'blocker'>('question')
const deleteDialogOpen = ref(false)

// Per-question and per-blocker reply text
const questionReplyTexts = ref<Record<number, string>>({})
const blockerReplyTexts = ref<Record<number, string>>({})
const questionReplying = ref<Record<number, boolean>>({})
const blockerReplying = ref<Record<number, boolean>>({})

function handleQuestionReply(index: number, questionText: string) {
  const text = (questionReplyTexts.value[index] ?? '').trim()
  if (!text) return
  questionReplying.value[index] = true
  emit('reply-to-question', props.agentName, index, questionText, text)
  questionReplyTexts.value[index] = ''
  // Reset loading state after a reasonable timeout
  setTimeout(() => {
    questionReplying.value[index] = false
  }, 2000)
}

function handleBlockerReply(index: number, blockerText: string) {
  const text = (blockerReplyTexts.value[index] ?? '').trim()
  if (!text) return
  blockerReplying.value[index] = true
  emit('reply-to-blocker', props.agentName, index, blockerText, text)
  blockerReplyTexts.value[index] = ''
  // Reset loading state after a reasonable timeout
  setTimeout(() => {
    blockerReplying.value[index] = false
  }, 2000)
}

function handleReply() {
  const text = replyText.value.trim()
  if (!text) return
  emit('reply', text)
  replyText.value = ''
}

function handleReplyKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleReply()
  }
}

function requestDismissQuestion(index: number) {
  dismissDialogIndex.value = index
  dismissDialogType.value = 'question'
  dismissDialogOpen.value = true
}

function requestDismissBlocker(index: number) {
  dismissDialogIndex.value = index
  dismissDialogType.value = 'blocker'
  dismissDialogOpen.value = true
}

function confirmDismiss() {
  if (dismissDialogIndex.value === null) return
  if (dismissDialogType.value === 'question') {
    emit('dismiss-question', dismissDialogIndex.value)
  } else {
    emit('dismiss-blocker', dismissDialogIndex.value)
  }
  dismissDialogOpen.value = false
  dismissDialogIndex.value = null
}

const tmuxState = computed<TmuxDisplayState>(() => {
  // Distinguish "no session registered" from "session registered but offline"
  if (!props.agent.tmux_session) return 'no-session'
  return getTmuxDisplayState(props.tmuxStatus)
})

const tmuxDisplay = computed(() => TMUX_STATUS_DISPLAY[tmuxState.value])

const tmuxLabelClass = computed(() => {
  switch (tmuxState.value) {
    case 'running':
      return 'border-blue-500/50 text-blue-400'
    case 'ready':
      return 'border-border text-muted-foreground'
    case 'approval':
      return 'border-primary/50 text-primary'
    case 'offline':
      return 'border-border text-muted-foreground/50'
    case 'no-session':
      return 'border-border text-muted-foreground/50'
    default:
      return 'border-border text-muted-foreground'
  }
})

const hasQuestions = computed(() => (props.agent.questions?.length ?? 0) > 0)
const hasBlockers = computed(() => (props.agent.blockers?.length ?? 0) > 0)
const hasSections = computed(() => (props.agent.sections?.length ?? 0) > 0)
const hasItems = computed(() => (props.agent.items?.length ?? 0) > 0)

const statusAccentClass = computed(() => {
  switch (props.agent.status) {
    case 'active': return 'border-t-green-500'
    case 'done': return 'border-t-teal-500'
    case 'blocked': return 'border-t-amber-500'
    case 'error': return 'border-t-red-500'
    case 'idle': return 'border-t-slate-400'
    default: return 'border-t-border'
  }
})

const attentionSectionClass = computed(() => {
  if (hasBlockers.value && !hasQuestions.value) return 'bg-orange-500/10 border-orange-500/30'
  return 'bg-amber-500/10 border-amber-500/30'
})

// --------------- Lifecycle ---------------
const lifecycleLoading = ref<'spawn' | 'stop' | 'restart' | null>(null)
const lifecycleToast = ref<{ type: 'success' | 'error'; message: string } | null>(null)
const stopConfirmOpen = ref(false)

let toastTimer: ReturnType<typeof setTimeout> | null = null
function showToast(type: 'success' | 'error', message: string) {
  if (toastTimer) clearTimeout(toastTimer)
  lifecycleToast.value = { type, message }
  toastTimer = setTimeout(() => { lifecycleToast.value = null }, 3500)
}

async function handleSpawn() {
  lifecycleLoading.value = 'spawn'
  try {
    await api.spawnAgent(props.spaceName, props.agentName)
    showToast('success', `${props.agentName} spawned — ignite sent in ~5s`)
  } catch (e) {
    showToast('error', e instanceof Error ? e.message : String(e))
  } finally {
    lifecycleLoading.value = null
  }
}

async function handleStop() {
  stopConfirmOpen.value = false
  lifecycleLoading.value = 'stop'
  try {
    await api.stopAgent(props.spaceName, props.agentName)
    showToast('success', `${props.agentName} stopped`)
  } catch (e) {
    showToast('error', e instanceof Error ? e.message : String(e))
  } finally {
    lifecycleLoading.value = null
  }
}

async function handleRestart() {
  lifecycleLoading.value = 'restart'
  try {
    await api.restartAgent(props.spaceName, props.agentName)
    showToast('success', `${props.agentName} restarting — ignite sent in ~5s`)
  } catch (e) {
    showToast('error', e instanceof Error ? e.message : String(e))
  } finally {
    lifecycleLoading.value = null
  }
}

// --------------- Introspection ---------------
const introspectOpen = ref(false)
const introspectLoading = ref(false)
const introspectLive = ref(false)
const introspectData = ref<IntrospectResponse | null>(null)
const introspectError = ref<string | null>(null)
let introspectPollTimer: ReturnType<typeof setInterval> | null = null

async function loadIntrospect() {
  if (introspectLoading.value) return
  introspectLoading.value = true
  introspectError.value = null
  try {
    introspectData.value = await api.introspectAgent(props.spaceName, props.agentName)
  } catch (e) {
    introspectError.value = e instanceof Error ? e.message : String(e)
  } finally {
    introspectLoading.value = false
  }
}

function startLivePoll() {
  if (introspectPollTimer) clearInterval(introspectPollTimer)
  introspectPollTimer = setInterval(loadIntrospect, 2500)
}

function stopLivePoll() {
  if (introspectPollTimer) {
    clearInterval(introspectPollTimer)
    introspectPollTimer = null
  }
}

function toggleLive() {
  introspectLive.value = !introspectLive.value
  if (introspectLive.value) {
    startLivePoll()
  } else {
    stopLivePoll()
  }
}

function toggleIntrospect() {
  introspectOpen.value = !introspectOpen.value
  if (introspectOpen.value) {
    loadIntrospect()
  } else {
    introspectLive.value = false
    stopLivePoll()
  }
}

// Reset introspect state whenever we navigate to a different agent
watch(() => props.agentName, () => {
  introspectOpen.value = false
  introspectLive.value = false
  introspectData.value = null
  introspectError.value = null
  stopLivePoll()
})

// Clean up on unmount
onUnmounted(() => {
  stopLivePoll()
  if (toastTimer) clearTimeout(toastTimer)
})
</script>

<template>
  <ScrollArea class="h-full">
    <div class="p-6 space-y-6 max-w-4xl border-t-[3px]" :class="statusAccentClass">
      <!-- Header -->
      <div class="flex items-start justify-between gap-4 flex-wrap">
        <div class="space-y-1">
          <div class="flex items-center gap-3">
            <AgentAvatar :name="agentName" :size="36" />
            <h1 class="text-2xl font-semibold tracking-tight">{{ agentName }}</h1>
            <StatusBadge :status="agent.status" />
            <Tooltip v-if="agent.stale">
              <TooltipTrigger as-child>
                <Badge variant="outline" class="border-orange-500/50 text-orange-500 text-[10px] h-5 px-1.5">
                  Stale
                </Badge>
              </TooltipTrigger>
              <TooltipContent>Agent has not posted an update recently</TooltipContent>
            </Tooltip>
            <Tooltip v-if="agent.inferred_status && agent.inferred_status !== 'working'">
              <TooltipTrigger as-child>
                <Badge variant="outline" class="border-muted-foreground/40 text-muted-foreground text-[10px] h-5 px-1.5 capitalize">
                  {{ agent.inferred_status.replace('_', ' ') }}
                </Badge>
              </TooltipTrigger>
              <TooltipContent>Server-inferred status from tmux observation</TooltipContent>
            </Tooltip>
            <Tooltip v-if="agent.test_count != null">
              <TooltipTrigger as-child>
                <div class="flex items-center gap-1 rounded-full bg-emerald-500/10 border border-emerald-500/30 px-2.5 py-0.5 text-xs font-semibold text-emerald-600 dark:text-emerald-400 tabular-nums cursor-default">
                  <span class="inline-block size-1.5 rounded-full bg-emerald-500 shrink-0"></span>
                  {{ agent.test_count }} tests
                </div>
              </TooltipTrigger>
              <TooltipContent>{{ agent.test_count }} passing tests reported</TooltipContent>
            </Tooltip>
          </div>
          <div class="flex items-center gap-3 text-sm text-muted-foreground font-text flex-wrap">
            <span v-if="agent.phase" :title="`Current phase: ${agent.phase}`">Phase: {{ agent.phase }}</span>
            <span v-if="agent.branch" class="font-mono text-xs bg-muted px-1.5 py-0.5 rounded" :title="`Git branch: ${agent.branch}`">{{ agent.branch }}</span>
            <a
              v-if="agent.pr"
              :href="agent.pr"
              target="_blank"
              rel="noopener"
              class="text-primary hover:underline focus-visible:outline-2 focus-visible:outline-ring font-mono text-xs"
              aria-label="Open pull request in new tab"
            >{{ agent.pr }}</a>
            <Tooltip>
              <TooltipTrigger as-child>
                <span class="cursor-default">Updated {{ relativeTime(agent.updated_at) }}</span>
              </TooltipTrigger>
              <TooltipContent>
                {{ formatFullDate(agent.updated_at) }}
              </TooltipContent>
            </Tooltip>
          </div>

          <!-- Hierarchy info row -->
          <div v-if="agent.parent || agent.role || agent.children?.length" class="flex items-center gap-2 flex-wrap mt-1">
            <span
              v-if="agent.role"
              class="inline-flex items-center gap-1 bg-purple-500/10 border border-purple-500/20 px-2 py-0.5 rounded text-xs text-purple-600 dark:text-purple-400"
            >
              {{ agent.role }}
            </span>
            <Tooltip v-if="agent.parent">
              <TooltipTrigger as-child>
                <button
                  class="inline-flex items-center gap-1 bg-muted/60 border border-border/60 px-2 py-0.5 rounded text-xs text-muted-foreground hover:text-primary hover:border-primary/40 transition-colors cursor-pointer"
                  @click="emit('select-agent', agent.parent!)"
                >
                  ↑ {{ agent.parent }}
                </button>
              </TooltipTrigger>
              <TooltipContent>Navigate to parent: {{ agent.parent }}</TooltipContent>
            </Tooltip>
            <template v-if="agent.children?.length">
              <Tooltip v-for="child in agent.children" :key="child">
                <TooltipTrigger as-child>
                  <button
                    class="inline-flex items-center gap-1 bg-muted/60 border border-border/60 px-2 py-0.5 rounded text-xs text-muted-foreground hover:text-primary hover:border-primary/40 transition-colors cursor-pointer"
                    @click="emit('select-agent', child)"
                  >
                    ↓ {{ child }}
                  </button>
                </TooltipTrigger>
                <TooltipContent>Navigate to: {{ child }}</TooltipContent>
              </Tooltip>
            </template>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <div class="flex items-center gap-1.5">
            <span class="text-xs text-muted-foreground">Terminal:</span>
            <Tooltip>
              <TooltipTrigger as-child>
                <Badge variant="outline" :class="tmuxLabelClass" role="status" :aria-label="`Terminal: ${tmuxDisplay.label}`">
                  {{ tmuxDisplay.label }}
                </Badge>
              </TooltipTrigger>
              <TooltipContent>
                Terminal: {{ tmuxDisplay.label }} — {{ tmuxDisplay.tooltip }}
              </TooltipContent>
            </Tooltip>
          </div>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="outline" size="sm" @click="emit('broadcast')">
                <Bell class="size-4" /> Nudge
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Nudge this agent with the latest space state
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="outline" size="sm" class="text-destructive hover:bg-destructive hover:text-destructive-foreground" @click="deleteDialogOpen = true">
                <Trash2 class="size-4" /> Delete
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Remove this agent from the space
            </TooltipContent>
          </Tooltip>

          <!-- Lifecycle buttons -->
          <div class="flex items-center gap-1 border border-border rounded-md p-0.5 bg-muted/30">
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost" size="sm" class="h-7 px-2 text-xs gap-1"
                  :disabled="lifecycleLoading !== null"
                  @click="handleSpawn"
                >
                  <Loader2 v-if="lifecycleLoading === 'spawn'" class="size-3 animate-spin" />
                  <Play v-else class="size-3" />
                  Spawn
                </Button>
              </TooltipTrigger>
              <TooltipContent>Create tmux session and launch agent</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost" size="sm" class="h-7 px-2 text-xs gap-1"
                  :disabled="lifecycleLoading !== null"
                  @click="handleRestart"
                >
                  <Loader2 v-if="lifecycleLoading === 'restart'" class="size-3 animate-spin" />
                  <RotateCcw v-else class="size-3" />
                  Restart
                </Button>
              </TooltipTrigger>
              <TooltipContent>Kill existing session and spawn a new one</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost" size="sm"
                  class="h-7 px-2 text-xs gap-1 text-destructive hover:text-destructive hover:bg-destructive/10"
                  :disabled="lifecycleLoading !== null"
                  @click="stopConfirmOpen = true"
                >
                  <Loader2 v-if="lifecycleLoading === 'stop'" class="size-3 animate-spin" />
                  <Square v-else class="size-3" />
                  Stop
                </Button>
              </TooltipTrigger>
              <TooltipContent>Kill the agent's tmux session</TooltipContent>
            </Tooltip>
          </div>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                size="sm"
                :variant="introspectOpen ? 'secondary' : 'outline'"
                class="gap-1 text-xs"
                @click="toggleIntrospect"
              >
                <Search class="size-3.5" />
                Inspect
              </Button>
            </TooltipTrigger>
            <TooltipContent>Capture live tmux pane output</TooltipContent>
          </Tooltip>
        </div>
      </div>

      <!-- Lifecycle toast notification -->
      <Transition
        enter-active-class="transition-all duration-200"
        enter-from-class="opacity-0 -translate-y-1"
        leave-active-class="transition-all duration-150"
        leave-to-class="opacity-0 -translate-y-1"
      >
        <div
          v-if="lifecycleToast"
          class="flex items-center gap-2 rounded-md border px-3 py-2 text-xs"
          :class="lifecycleToast.type === 'success'
            ? 'border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400'
            : 'border-destructive/30 bg-destructive/10 text-destructive'"
        >
          <CheckCircle2 v-if="lifecycleToast.type === 'success'" class="size-3.5 shrink-0" />
          <XCircle v-else class="size-3.5 shrink-0" />
          {{ lifecycleToast.message }}
          <button class="ml-auto opacity-60 hover:opacity-100" @click="lifecycleToast = null">
            <X class="size-3" />
          </button>
        </div>
      </Transition>

      <!-- Introspection panel -->
      <div v-if="introspectOpen" class="rounded-lg border bg-muted/30 p-4 space-y-2">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <span class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Live Pane — {{ agent.tmux_session || 'no session' }}</span>
            <!-- Live indicator -->
            <span v-if="introspectLive" class="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider text-green-500">
              <span class="inline-block size-1.5 rounded-full bg-green-500 animate-pulse shrink-0"></span>
              Live
            </span>
          </div>
          <div class="flex items-center gap-1">
            <!-- Live toggle -->
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost" size="sm"
                  class="h-6 px-2 text-xs gap-1"
                  :class="introspectLive ? 'text-green-500 bg-green-500/10 hover:bg-green-500/20' : ''"
                  @click="toggleLive"
                >
                  <Radio class="size-3" />
                  {{ introspectLive ? 'Live On' : 'Live Off' }}
                </Button>
              </TooltipTrigger>
              <TooltipContent>{{ introspectLive ? 'Disable auto-refresh (polling every 2.5s)' : 'Enable auto-refresh (poll every 2.5s)' }}</TooltipContent>
            </Tooltip>
            <!-- Manual refresh (only when not live) -->
            <Button
              v-if="!introspectLive"
              variant="ghost" size="sm" class="h-6 px-2 text-xs"
              :disabled="introspectLoading"
              @click="loadIntrospect"
            >
              <Loader2 v-if="introspectLoading" class="size-3 animate-spin" />
              {{ introspectLoading ? 'Loading…' : 'Refresh' }}
            </Button>
            <button class="text-muted-foreground hover:text-foreground p-1" @click="introspectOpen = false">
              <X class="size-3.5" />
            </button>
          </div>
        </div>
        <div v-if="introspectError" class="text-xs text-destructive">{{ introspectError }}</div>
        <div v-else-if="!introspectData" class="text-xs text-muted-foreground italic">Loading…</div>
        <div v-else>
          <div class="flex gap-3 mb-2 text-[11px] text-muted-foreground flex-wrap">
            <span :class="introspectData.session_exists ? 'text-green-500' : 'text-red-500'">
              {{ introspectData.session_exists ? 'session online' : 'session offline' }}
            </span>
            <span v-if="introspectData.idle">idle</span>
            <span v-if="introspectData.needs_approval" class="text-primary">awaiting approval: {{ introspectData.tool_name }}</span>
            <span class="ml-auto tabular-nums">captured {{ new Date(introspectData.captured_at).toLocaleTimeString() }}</span>
          </div>
          <pre class="text-[11px] leading-snug text-foreground/80 bg-background rounded border border-border p-3 overflow-x-auto max-h-64 overflow-y-auto font-mono whitespace-pre-wrap">{{ introspectData.lines.join('\n') }}</pre>
        </div>
      </div>

      <Separator />

      <!-- Questions & Blockers — Actionable Inbox (elevated: shown first when present) -->
      <section
        v-if="hasQuestions || hasBlockers"
        class="space-y-4 rounded-xl border p-4"
        :class="attentionSectionClass"
        aria-label="Questions and blockers"
      >
        <div class="flex items-center gap-2">
          <h2 class="text-sm font-semibold text-foreground">Needs Your Attention</h2>
          <Badge variant="destructive" class="h-5 min-w-5 px-1.5 text-[10px] font-semibold tabular-nums">
            {{ (agent.questions?.length ?? 0) + (agent.blockers?.length ?? 0) }}
          </Badge>
        </div>

        <!-- Questions -->
        <div
          v-for="(q, qi) in agent.questions"
          :key="'q-' + qi"
          class="rounded-lg border-2 border-amber-500/50 bg-amber-500/5 p-4 space-y-3"
          role="article"
          :aria-label="`Question: ${q}`"
        >
          <!-- Question header -->
          <div class="flex items-start gap-3">
            <div class="rounded-full bg-amber-500/15 p-1.5 shrink-0 mt-0.5">
              <HelpCircle class="size-4 text-amber-500" />
            </div>
            <div class="flex-1 min-w-0">
              <p class="text-xs font-medium text-amber-600 dark:text-amber-400 uppercase tracking-wide mb-1">Question</p>
              <div class="font-text text-sm leading-relaxed md-content" v-html="renderMarkdown(q)" />
            </div>
          </div>

          <!-- Inline reply form — visible by default -->
          <div class="pl-10 space-y-2">
            <Textarea
              v-model="questionReplyTexts[qi]"
              :placeholder="`Reply to this question...`"
              class="min-h-[60px] text-sm font-text resize-y border-amber-500/30 focus-visible:ring-amber-500/50"
              :disabled="questionReplying[qi]"
            />
            <div class="flex items-center gap-2">
              <Button
                size="sm"
                class="bg-amber-600 hover:bg-amber-700 text-white"
                :disabled="!(questionReplyTexts[qi] ?? '').trim() || questionReplying[qi]"
                @click="handleQuestionReply(qi, q)"
              >
                <MessageSquareReply class="size-3.5" />
                {{ questionReplying[qi] ? 'Sending...' : 'Reply' }}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                class="text-muted-foreground hover:text-foreground h-8 px-2 text-xs"
                :disabled="questionReplying[qi]"
                @click="requestDismissQuestion(qi)"
              >
                <X class="size-3" /> Dismiss without reply
              </Button>
            </div>
          </div>
        </div>

        <!-- Blockers -->
        <div
          v-for="(b, bi) in agent.blockers"
          :key="'b-' + bi"
          class="rounded-lg border-2 border-orange-500/50 bg-orange-500/5 p-4 space-y-3"
          role="article"
          :aria-label="`Blocker: ${b}`"
        >
          <!-- Blocker header -->
          <div class="flex items-start gap-3">
            <div class="rounded-full bg-orange-500/15 p-1.5 shrink-0 mt-0.5">
              <AlertTriangle class="size-4 text-orange-500" />
            </div>
            <div class="flex-1 min-w-0">
              <p class="text-xs font-medium text-orange-600 dark:text-orange-400 uppercase tracking-wide mb-1">Blocker</p>
              <div class="font-text text-sm leading-relaxed md-content" v-html="renderMarkdown(b)" />
            </div>
          </div>

          <!-- Inline reply form — visible by default -->
          <div class="pl-10 space-y-2">
            <Textarea
              v-model="blockerReplyTexts[bi]"
              :placeholder="`Respond to unblock (e.g. 'You're unblocked because...')...`"
              class="min-h-[60px] text-sm font-text resize-y border-orange-500/30 focus-visible:ring-orange-500/50"
              :disabled="blockerReplying[bi]"
            />
            <div class="flex items-center gap-2">
              <Button
                size="sm"
                variant="destructive"
                :disabled="!(blockerReplyTexts[bi] ?? '').trim() || blockerReplying[bi]"
                @click="handleBlockerReply(bi, b)"
              >
                <MessageSquareReply class="size-3.5" />
                {{ blockerReplying[bi] ? 'Sending...' : 'Reply & Unblock' }}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                class="text-muted-foreground hover:text-foreground h-8 px-2 text-xs"
                :disabled="blockerReplying[bi]"
                @click="requestDismissBlocker(bi)"
              >
                <X class="size-3" /> Dismiss without reply
              </Button>
            </div>
          </div>
        </div>
      </section>

      <!-- Summary -->
      <section v-if="agent.summary" aria-label="Agent summary">
        <h2 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-1">Summary</h2>
        <div class="font-text leading-relaxed md-content" v-html="renderMarkdown(agent.summary)" />
      </section>

      <Separator v-if="agent.summary && (hasItems || hasSections || agent.next_steps)" class="opacity-50" />

      <!-- Items -->
      <section v-if="hasItems" aria-label="Work items">
        <h2 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-2">Items</h2>
        <ol class="space-y-1.5 font-text text-sm">
          <li v-for="(item, i) in agent.items" :key="i" class="flex items-start gap-2.5">
            <span class="shrink-0 mt-0.5 min-w-[1.25rem] text-right text-xs font-mono font-semibold text-muted-foreground/70 select-none">{{ i + 1 }}.</span>
            <span class="leading-relaxed md-content-inline" v-html="renderMarkdownInline(item)" />
          </li>
        </ol>
      </section>

      <Separator v-if="hasItems && (hasSections || agent.next_steps)" class="opacity-50" />

      <!-- Sections -->
      <div v-if="hasSections" class="space-y-4">
        <section v-for="(section, si) in agent.sections" :key="si" :aria-label="section.title">
          <h3 class="text-sm font-semibold mb-2">{{ section.title }}</h3>
          <ol v-if="section.items?.length" class="space-y-1.5 font-text text-sm mb-2">
            <li v-for="(item, ii) in section.items" :key="ii" class="flex items-start gap-2.5">
              <span class="shrink-0 mt-0.5 min-w-[1.25rem] text-right text-xs font-mono font-semibold text-muted-foreground/70 select-none">{{ ii + 1 }}.</span>
              <span class="leading-relaxed md-content-inline" v-html="renderMarkdownInline(item)" />
            </li>
          </ol>
          <!-- Table -->
          <div v-if="section.table" class="overflow-x-auto rounded border">
            <table class="w-full text-sm font-text" :aria-label="`${section.title} table`">
              <thead>
                <tr class="border-b bg-muted/50">
                  <th
                    v-for="(header, hi) in section.table.headers"
                    :key="hi"
                    scope="col"
                    class="px-3 py-2 text-left text-xs font-medium text-muted-foreground"
                  >
                    {{ header }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(row, ri) in section.table.rows" :key="ri" class="border-b last:border-0">
                  <td v-for="(cell, ci) in row" :key="ci" class="px-3 py-2">
                    {{ cell }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>

      <!-- Dismiss without reply confirmation AlertDialog -->
      <AlertDialog v-model:open="dismissDialogOpen">
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Dismiss {{ dismissDialogType }} without replying?</AlertDialogTitle>
            <AlertDialogDescription>
              This will remove the {{ dismissDialogType }} without sending a reply to the agent. The agent won't receive an answer. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction class="bg-destructive text-destructive-foreground hover:bg-destructive/90" @click="confirmDismiss()">
              <X class="size-4" /> Dismiss
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <!-- Stop agent confirmation AlertDialog -->
      <AlertDialog v-model:open="stopConfirmOpen">
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Stop agent?</AlertDialogTitle>
            <AlertDialogDescription>
              This will kill the tmux session for <span class="font-semibold text-foreground">{{ agentName }}</span>. Any in-progress work will be interrupted. You can respawn the agent afterwards.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction class="bg-destructive text-destructive-foreground hover:bg-destructive/90" @click="handleStop">
              <Square class="size-4" /> Stop
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <!-- Delete agent AlertDialog -->
      <AlertDialog v-model:open="deleteDialogOpen">
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete agent?</AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently remove <span class="font-semibold text-foreground">{{ agentName }}</span>. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction class="bg-destructive text-destructive-foreground hover:bg-destructive/90" @click="emit('delete')">
              <Trash2 class="size-4" /> Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <Separator v-if="hasSections && agent.next_steps" class="opacity-50" />

      <!-- Next Steps -->
      <section v-if="agent.next_steps" aria-label="Next steps">
        <h2 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-1">Next Steps</h2>
        <div class="font-text text-sm leading-relaxed md-content" v-html="renderMarkdown(agent.next_steps)" />
      </section>

      <!-- Free Text -->
      <section v-if="agent.free_text" aria-label="Agent notes">
        <h2 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-1">Notes</h2>
        <p class="text-sm leading-relaxed whitespace-pre-wrap bg-muted/30 rounded p-3 font-mono text-xs">{{ agent.free_text }}</p>
      </section>

      <!-- Documents -->
      <section v-if="agent.documents?.length" aria-label="Agent documents">
        <h2 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-2">Documents</h2>
        <nav class="space-y-1" aria-label="Document links">
          <a
            v-for="doc in agent.documents"
            :key="doc.slug"
            :href="`/spaces/${spaceName}/agent/${agentName}/${doc.slug}`"
            target="_blank"
            rel="noopener"
            class="block text-sm text-primary hover:underline font-text focus-visible:outline-2 focus-visible:outline-ring"
            :aria-label="`Open document: ${doc.title} (opens in new tab)`"
          >
            {{ doc.title }}
          </a>
        </nav>
      </section>

      <Separator />

      <!-- Tmux Controls — only shown when agent has a registered tmux session -->
      <section v-if="agent.tmux_session" class="space-y-3" aria-label="Tmux session controls">
        <h2 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Controls</h2>

        <!-- Approval button -->
        <div v-if="tmuxStatus?.needs_approval" class="space-y-2">
          <Card class="border-primary/40 bg-primary/5" role="alert">
            <CardContent class="p-4">
              <div class="flex items-center justify-between gap-3">
                <div>
                  <p class="text-sm font-medium">Approval Required</p>
                  <p v-if="tmuxStatus.tool_name" class="text-xs text-muted-foreground font-text mt-0.5">
                    Tool: <span class="font-mono">{{ tmuxStatus.tool_name }}</span>
                  </p>
                  <p v-if="tmuxStatus.prompt_text" class="text-xs text-muted-foreground font-text mt-1 line-clamp-2">
                    {{ tmuxStatus.prompt_text }}
                  </p>
                </div>
                <Tooltip>
                  <TooltipTrigger as-child>
                    <Button @click="emit('approve')" aria-label="Approve tool execution">
                      <ShieldCheck class="size-4" /> Approve
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>
                    Allow the agent to proceed by sending 'y' to its tmux session
                  </TooltipContent>
                </Tooltip>
              </div>
            </CardContent>
          </Card>
        </div>

        <!-- No tmux session state -->
        <p v-if="!tmuxStatus?.exists" class="text-sm text-muted-foreground font-text">
          Tmux session not detected. Actions may not work if the agent's session uses a non-standard name.
        </p>

        <!-- Tmux keystroke injection (advanced) -->
        <Collapsible v-model:open="tmuxInputOpen">
          <CollapsibleTrigger class="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors cursor-pointer font-text">
            <ChevronRight class="size-3 transition-transform" :class="{ 'rotate-90': tmuxInputOpen }" />
            Tmux Keystroke Injection
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div class="space-y-1 mt-2">
              <p class="text-xs text-muted-foreground font-text">
                Type raw keystrokes directly into the agent's tmux session. Use this for answering tool prompts or typing commands — not for general communication (use Messages below instead).
              </p>
              <div class="flex gap-2">
                <Input
                  id="tmux-input"
                  v-model="replyText"
                  placeholder="Keystrokes to inject into tmux..."
                  class="flex-1 font-text font-mono"
                  @keydown="handleReplyKeydown"
                />
                <Tooltip>
                  <TooltipTrigger as-child>
                    <Button
                      variant="outline"
                      size="sm"
                      :disabled="!replyText.trim()"
                      aria-label="Send keystrokes to tmux session"
                      @click="handleReply"
                    >
                      <Terminal class="size-4" /> Send
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>
                    Send this text as keystrokes to the agent's tmux session
                  </TooltipContent>
                </Tooltip>
              </div>
            </div>
          </CollapsibleContent>
        </Collapsible>
      </section>

      <!-- Messages -->
      <section class="mt-6" aria-label="Agent messages">
        <Separator class="mb-4" />
        <div class="flex items-center gap-2 mb-3">
          <h2 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Messages</h2>
          <Badge v-if="agent.messages?.length" variant="secondary" class="h-4 min-w-4 px-1 text-[10px] font-semibold tabular-nums">
            {{ agent.messages.length }}
          </Badge>
        </div>
        <div class="h-[500px] rounded-xl border bg-card text-card-foreground flex flex-col overflow-hidden">
          <AgentMessages
            :messages="agent.messages ?? []"
            :agent-name="agentName"
            class="min-h-0 flex-1"
            @send-message="(text: string) => emit('send-message', text, 'boss')"
          />
        </div>
      </section>
    </div>
  </ScrollArea>
</template>
