<script setup lang="ts">
import type { KnowledgeSpace, SessionAgentStatus, HierarchyTree } from '@/types'
import { ref, computed, nextTick, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useTime } from '@/composables/useTime'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Radio,
  Bell,
  Trash2,
  Archive,
  MessageSquare,
  SendHorizontal,
  HelpCircle,
  AlertTriangle,
  MessageSquareReply,
  GitBranch,
  ExternalLink,
  Clock,
  Layers,
  Search,
  Plus,
  RotateCcw,
} from 'lucide-vue-next'
import StatusBadge from './StatusBadge.vue'
import InterruptTracker from './InterruptTracker.vue'
import AgentAvatar from './AgentAvatar.vue'
import AgentProfileCard from './AgentProfileCard.vue'
import GanttTimeline from './GanttTimeline.vue'
import HierarchyView from './HierarchyView.vue'
import AgentCreateDialog from './AgentCreateDialog.vue'
import { prLink } from '@/lib/utils'

const props = defineProps<{
  space: KnowledgeSpace
  tmuxStatus: Record<string, SessionAgentStatus> | null
  broadcasting?: boolean
  restartAllProgress?: { agents: string[]; completed: number } | null
  hierarchy?: HierarchyTree | null
}>()

const emit = defineEmits<{
  'select-agent': [name: string]
  broadcast: []
  'restart-all': []
  'delete-agent': [name: string]
  'broadcast-agent': [name: string]
  'send-message-to-agent': [agentName: string, text: string]
  'delete-space': []
  'archive-space': []
  'clear-done-agents': [names: string[]]
}>()

const agentSearch = ref('')
const activeTab = ref('agents')
const agentCreateDialogOpen = ref(false)
const deleteDialogOpen = ref(false)
const deleteDialogAgent = ref<string | null>(null)
const deleteSpaceDialogOpen = ref(false)
const clearDoneDialogOpen = ref(false)
const messageDialogOpen = ref(false)
const messageDialogAgent = ref<string | null>(null)
const messageText = ref('')
const messageInputRef = ref<HTMLTextAreaElement | null>(null)

function openDeleteDialog(name: string) {
  deleteDialogAgent.value = name
  deleteDialogOpen.value = true
}

function confirmDeleteAgent() {
  if (deleteDialogAgent.value) {
    emit('delete-agent', deleteDialogAgent.value)
  }
  deleteDialogOpen.value = false
  deleteDialogAgent.value = null
}

function openMessageDialog(name: string) {
  messageDialogAgent.value = name
  messageText.value = ''
  messageDialogOpen.value = true
  nextTick(() => {
    messageInputRef.value?.focus()
  })
}

function sendQuickMessage() {
  const text = messageText.value.trim()
  if (!text || !messageDialogAgent.value) return
  const targetAgent = messageDialogAgent.value
  emit('send-message-to-agent', targetAgent, text)
  messageDialogOpen.value = false
  messageDialogAgent.value = null
  messageText.value = ''
  // Navigate to the conversation thread after sending
  router.push({
    name: 'conversation',
    params: { space: props.space.name, conversationAgent: targetAgent },
  })
}

const { relativeTime, formatFullDate, freshness } = useTime()
const router = useRouter()

function handleCardKeydown(e: KeyboardEvent, name: string) {
  const tag = (e.target as HTMLElement)?.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'BUTTON') return
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    emit('select-agent', name)
  }
}

function switchToInbox() {
  activeTab.value = 'inbox'
}

function refreshInbox() {
  inboxRef.value?.refresh()
}

defineExpose({ switchToInbox, refreshInbox })

const sortedAgents = computed(() => {
  return Object.entries(props.space.agents).sort(([, a], [, b]) => {
    // Agents needing attention first (blockers > questions), then by name
    const aAttention = (a.blockers?.length ?? 0) * 2 + (a.questions?.length ?? 0)
    const bAttention = (b.blockers?.length ?? 0) * 2 + (b.questions?.length ?? 0)
    if (aAttention !== bAttention) return bAttention - aAttention
    // Active agents before done/idle
    const statusOrder: Record<string, number> = { error: 0, blocked: 1, active: 2, idle: 3, done: 4 }
    const aOrder = statusOrder[a.status] ?? 5
    const bOrder = statusOrder[b.status] ?? 5
    if (aOrder !== bOrder) return aOrder - bOrder
    return 0
  })
})

const agentCount = computed(() => Object.keys(props.space.agents).length)

// Setup checklist: shown for new spaces (<24h old, <3 agents) until dismissed.
const checklistDismissed = ref(
  localStorage.getItem(`checklist-dismissed:${props.space.name}`) === '1'
)
function dismissChecklist() {
  checklistDismissed.value = true
  localStorage.setItem(`checklist-dismissed:${props.space.name}`, '1')
}
const isNewSpace = computed(() => {
  const created = new Date(props.space.created_at).getTime()
  return Date.now() - created < 24 * 60 * 60 * 1000
})
const showChecklist = computed(
  () => isNewSpace.value && agentCount.value < 3 && !checklistDismissed.value
)
const hasAgent = computed(() => agentCount.value > 0)
const hasAgentWithWorkDir = computed(() =>
  Object.values(props.space.agents).some((a) => (a as any).work_dir)
)
const hasSpawnedSession = computed(() =>
  Object.values(props.space.agents).some((a) => (a as any).session_id)
)

const inboxRef = ref<InstanceType<typeof InterruptTracker> | null>(null)

const attentionCount = computed(() => {
  let count = 0
  for (const agent of Object.values(props.space.agents)) {
    count += (agent.questions?.length ?? 0) + (agent.blockers?.length ?? 0)
  }
  return count
})

const needsAttentionCount = computed(() => {
  let count = 0
  for (const agent of Object.values(props.space.agents)) {
    if ((agent.questions?.length ?? 0) > 0 || (agent.blockers?.length ?? 0) > 0) {
      count++
    }
  }
  return count
})

const headerSummary = computed(() => {
  const total = agentCount.value
  const attn = needsAttentionCount.value
  const agentWord = total === 1 ? 'agent' : 'agents'
  if (total === 0) return 'No agents'
  if (attn === 0) return `${total} ${agentWord} — all clear`
  return `${total} ${agentWord} — ${attn} need${attn === 1 ? 's' : ''} attention`
})

const inboxPending = computed(() => inboxRef.value?.pendingCount ?? attentionCount.value)

/** Returns Tailwind bg class for the freshness dot on the avatar */
function freshnessDotClass(dateStr: string): string {
  const tier = freshness(dateStr)
  if (tier === 'live') return 'bg-blue-400'
  if (tier === 'recent') return 'bg-teal-400'
  if (tier === 'normal') return 'bg-gray-400/50'
  return 'bg-amber-400/60'
}

// Track recently-updated agents for flash animation.
// Shallow-watch only updated_at timestamps instead of { deep: true } on the full
// agents map — avoids recursively diffing all nested arrays on every update.
const recentlyUpdated = ref<Set<string>>(new Set())

const agentTimestamps = computed<Record<string, string>>(() => {
  const result: Record<string, string> = {}
  for (const [name, agent] of Object.entries(props.space.agents)) {
    result[name] = agent.updated_at
  }
  return result
})

watch(agentTimestamps, (timestamps, prev) => {
  for (const [name, ts] of Object.entries(timestamps)) {
    if (prev[name] && prev[name] !== ts) {
      recentlyUpdated.value.add(name)
      setTimeout(() => {
        recentlyUpdated.value.delete(name)
      }, 2000)
      // Refresh inbox when an agent updates — new questions/blockers may have arrived
      inboxRef.value?.refresh()
    }
  }
})

/** Check if an agent has any attention items */
function hasAttention(agent: { questions?: string[]; blockers?: string[] }): boolean {
  return (agent.questions?.length ?? 0) > 0 || (agent.blockers?.length ?? 0) > 0
}

/** Agents filtered by the search query */
const filteredSortedAgents = computed(() => {
  const q = agentSearch.value.trim().toLowerCase()
  if (!q) return sortedAgents.value
  return sortedAgents.value.filter(([name, agent]) =>
    name.toLowerCase().includes(q) || agent.summary?.toLowerCase().includes(q)
  )
})

/** Agents with blockers/questions — shown first as full cards */
const needsAttentionAgents = computed(() =>
  filteredSortedAgents.value.filter(([, agent]) => hasAttention(agent))
)

/** Active/error/blocked agents without attention items — shown as full cards */
const activeAgents = computed(() =>
  filteredSortedAgents.value.filter(([, agent]) =>
    !hasAttention(agent) && !['done', 'idle'].includes(agent.status)
  )
)

/** Done/idle agents without attention items — shown as compact rows to save space */
const doneIdleAgents = computed(() =>
  filteredSortedAgents.value.filter(([, agent]) =>
    !hasAttention(agent) && ['done', 'idle'].includes(agent.status)
  )
)

/** Card-grid sections (attention first, then active) */
const activeSections = computed(() => [
  {
    key: 'attention',
    label: 'Needs Attention',
    agents: needsAttentionAgents.value,
    headerClass: 'text-orange-600 dark:text-orange-400',
    dividerClass: 'border-orange-500/20',
    showIcon: true,
    ariaLabel: 'Agents needing attention',
  },
  {
    key: 'active',
    label: 'Active',
    agents: activeAgents.value,
    headerClass: 'text-foreground/60',
    dividerClass: 'border-border/50',
    showIcon: false,
    ariaLabel: 'Active agents',
  },
])
</script>

<template>
  <ScrollArea class="flex-1 min-h-0">
    <div class="p-6 space-y-6 max-w-7xl">
      <!-- Archived banner -->
      <div
        v-if="space.archive"
        class="flex items-center gap-2 px-3 py-2 rounded-md bg-muted border border-border text-sm text-muted-foreground mb-2"
        role="status"
      >
        <Archive class="size-4 shrink-0" aria-hidden="true" />
        <span>This space is archived. <span class="font-mono text-xs">{{ space.archive }}</span></span>
      </div>

      <!-- Fleet restart progress banner -->
      <div
        v-if="restartAllProgress"
        class="flex items-center gap-2 px-3 py-2 rounded-md bg-primary/5 border border-primary/20 text-sm"
        role="status"
      >
        <RotateCcw class="size-4 shrink-0 animate-spin text-primary" aria-hidden="true" />
        <span>
          Restarting {{ restartAllProgress.agents.length }} agent{{ restartAllProgress.agents.length !== 1 ? 's' : '' }}…
          <span class="font-medium">({{ restartAllProgress.completed }}/{{ restartAllProgress.agents.length }} complete)</span>
        </span>
      </div>

      <!-- Header -->
      <div class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight">{{ space.name }}</h1>
          <p class="text-sm text-muted-foreground font-text">
            {{ headerSummary }}
          </p>
        </div>
        <div class="flex items-center gap-2">
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                size="sm"
                @click="agentCreateDialogOpen = true"
              >
                <Plus class="size-4" />
                Add Agent
              </Button>
            </TooltipTrigger>
            <TooltipContent>Spawn a new agent in this space</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="outline"
                size="sm"
                :disabled="agentCount === 0 || broadcasting"
                @click="emit('broadcast')"
              >
                <Radio class="size-4" />
                Nudge All ({{ agentCount }})
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Send a nudge to all {{ agentCount }} agent{{ agentCount !== 1 ? 's' : '' }} in this space
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="outline"
                size="sm"
                :disabled="agentCount === 0 || !!restartAllProgress"
                @click="emit('restart-all')"
              >
                <RotateCcw class="size-4" :class="restartAllProgress ? 'animate-spin' : ''" />
                {{ restartAllProgress
                  ? `Restarting… (${restartAllProgress.completed}/${restartAllProgress.agents.length})`
                  : 'Restart All' }}
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {{ restartAllProgress
                ? `Fleet restart in progress: ${restartAllProgress.completed} of ${restartAllProgress.agents.length} done`
                : 'Restart all active/idle agents in this space' }}
            </TooltipContent>
          </Tooltip>
          <Tooltip v-if="doneIdleAgents.length > 0">
            <TooltipTrigger as-child>
              <Button
                variant="outline"
                size="sm"
                class="text-muted-foreground hover:text-destructive hover:border-destructive/50"
                @click="clearDoneDialogOpen = true"
              >
                <Trash2 class="size-4" />
                Clear Done/Idle ({{ doneIdleAgents.length }})
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Remove all {{ doneIdleAgents.length }} done or idle agent{{ doneIdleAgents.length !== 1 ? 's' : '' }} from this space
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="outline"
                size="sm"
                :class="space.archive ? 'text-muted-foreground' : ''"
                @click="emit('archive-space')"
              >
                <Archive class="size-4" />
                {{ space.archive ? 'Unarchive' : 'Archive Space' }}
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {{ space.archive ? 'Remove archived status from this space' : 'Mark this space as archived' }}
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="outline"
                size="sm"
                class="text-destructive border-destructive/30 hover:bg-destructive/10 hover:border-destructive"
                @click="deleteSpaceDialogOpen = true"
              >
                <Trash2 class="size-4" />
                Delete Space
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Permanently delete this space and all its agent data
            </TooltipContent>
          </Tooltip>
        </div>
      </div>

      <!-- Agent search bar -->
      <div class="relative">
        <Search class="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground pointer-events-none" aria-hidden="true" />
        <Input
          v-model="agentSearch"
          type="search"
          data-search-focus
          placeholder="Search agents by name or summary…"
          class="pl-9"
          aria-label="Filter agents by name or summary"
        />
      </div>

      <!-- Setup checklist for new spaces -->
      <div
        v-if="showChecklist"
        class="rounded-lg border border-border bg-muted/40 p-4 text-sm font-text"
        role="region"
        aria-label="Getting started checklist"
      >
        <div class="flex items-start justify-between gap-2">
          <p class="font-medium text-foreground mb-2">Getting started checklist</p>
          <button
            class="text-muted-foreground hover:text-foreground transition-colors text-xs shrink-0"
            aria-label="Dismiss checklist"
            @click="dismissChecklist"
          >
            Dismiss
          </button>
        </div>
        <ul class="space-y-1.5 text-muted-foreground">
          <li class="flex items-center gap-2">
            <span :class="hasAgent ? 'text-green-600 dark:text-green-400' : 'text-muted-foreground'">{{ hasAgent ? '✓' : '○' }}</span>
            Add your first agent
          </li>
          <li class="flex items-center gap-2">
            <span :class="hasAgentWithWorkDir ? 'text-green-600 dark:text-green-400' : 'text-muted-foreground'">{{ hasAgentWithWorkDir ? '✓' : '○' }}</span>
            Set a working directory for the agent
          </li>
          <li class="flex items-center gap-2">
            <span :class="hasSpawnedSession ? 'text-green-600 dark:text-green-400' : 'text-muted-foreground'">{{ hasSpawnedSession ? '✓' : '○' }}</span>
            Spawn the agent session
          </li>
        </ul>
      </div>

      <!-- Tabs: Agents / Inbox -->
      <Tabs v-model="activeTab">
        <TabsList>
          <TabsTrigger value="agents">Agents</TabsTrigger>
          <TabsTrigger value="inbox" class="gap-1.5" :aria-label="inboxPending > 0 ? 'Inbox, ' + inboxPending + ' pending items' : 'Inbox'">
            Inbox
            <Badge
              v-if="inboxPending > 0"
              variant="destructive"
              class="h-5 min-w-5 px-1 text-[10px] font-semibold tabular-nums"
            >
              {{ inboxPending }}
            </Badge>
          </TabsTrigger>
          <TabsTrigger value="timeline">Timeline</TabsTrigger>
          <TabsTrigger value="hierarchy">Hierarchy</TabsTrigger>
        </TabsList>

        <TabsContent value="agents">
          <div class="space-y-6">
            <!-- Grouped sections: Needs Attention + Active (full cards) -->
            <template v-for="section in activeSections" :key="section.key">
              <div v-if="section.agents.length > 0">
                <!-- Section header -->
                <div class="flex items-center gap-2 mb-3">
                  <AlertTriangle
                    v-if="section.showIcon"
                    class="size-3.5 text-orange-500 shrink-0"
                    aria-hidden="true"
                  />
                  <span class="text-xs font-semibold uppercase tracking-wide" :class="section.headerClass">
                    {{ section.label }}
                  </span>
                  <span class="text-xs text-muted-foreground tabular-nums">{{ section.agents.length }}</span>
                  <div class="flex-1 border-t" :class="section.dividerClass" />
                </div>
                <!-- Cards grid -->
                <div
                  class="grid gap-4 grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
                  role="list"
                  :aria-label="section.ariaLabel"
                >
                  <Card
                    v-for="[name, agent] in section.agents"
                    :key="name"
                    role="listitem"
                    tabindex="0"
                    class="group cursor-pointer transition-all duration-150 hover:bg-accent/50 focus-visible:outline-2 focus-visible:outline-ring focus-visible:outline-offset-2 relative flex flex-col !py-0 !gap-0 min-h-[180px]"
                    :class="[
                      agent.blockers?.length
                        ? 'border-l-4 border-l-orange-500 shadow-md shadow-orange-500/5'
                        : agent.questions?.length
                          ? 'border-l-4 border-l-amber-500 shadow-md shadow-amber-500/5'
                          : '',
                      recentlyUpdated.has(name) ? 'ring-2 ring-primary/40 transition-shadow' : '',
                    ]"
                    :aria-label="`Agent ${name}, status: ${agent.status}${agent.summary ? ', ' + agent.summary.slice(0, 80) + (agent.summary.length > 80 ? '…' : '') : ''}`"
                    @click="emit('select-agent', name)"
                    @keydown="handleCardKeydown($event, name)"
                  >
                    <CardContent class="flex flex-col flex-1 p-4 gap-2">
                      <!-- Row 1: Header — Avatar + Name + StatusBadge -->
                      <div class="flex items-center justify-between gap-2">
                        <div class="flex items-center gap-2.5 min-w-0 overflow-hidden">
                          <AgentProfileCard
                            :agent-name="name"
                            :agent="agent"
                            :space-name="space.name"
                            @select-agent="emit('select-agent', $event)"
                          >
                            <div class="flex items-center gap-2.5 min-w-0" @click.stop>
                              <div class="relative inline-block shrink-0">
                                <AgentAvatar :name="name" :size="28" aria-hidden="true" />
                                <span
                                  class="absolute -bottom-0.5 -right-0.5 block size-2.5 rounded-full ring-2 ring-card"
                                  :class="freshnessDotClass(agent.updated_at)"
                                />
                              </div>
                              <h3 class="text-base font-semibold truncate m-0">{{ name }}</h3>
                            </div>
                          </AgentProfileCard>
                        </div>
                        <div class="flex items-center gap-1.5 shrink-0">
                          <StatusBadge :status="agent.status" />
                          <Tooltip v-if="agent.stale">
                            <TooltipTrigger as-child>
                              <Badge
                                variant="outline"
                                class="border-orange-500/50 text-orange-500 text-[10px] h-5 px-1.5"
                              >
                                Stale
                              </Badge>
                            </TooltipTrigger>
                            <TooltipContent>Agent has not posted an update recently</TooltipContent>
                          </Tooltip>
                          <Tooltip v-if="agent.inferred_status && agent.inferred_status !== 'working'">
                            <TooltipTrigger as-child>
                              <Badge
                                variant="outline"
                                class="border-muted-foreground/40 text-muted-foreground text-[10px] h-5 px-1.5 capitalize"
                              >
                                {{ agent.inferred_status.replace('_', ' ') }}
                              </Badge>
                            </TooltipTrigger>
                            <TooltipContent>Server-inferred status from tmux observation</TooltipContent>
                          </Tooltip>
                          <Tooltip v-if="tmuxStatus?.[name]?.needs_approval">
                            <TooltipTrigger as-child>
                              <Badge
                                variant="outline"
                                class="border-primary/50 text-primary text-[10px] h-5 px-1.5"
                              >
                                Approval
                              </Badge>
                            </TooltipTrigger>
                            <TooltipContent>Agent is waiting for tool-use approval</TooltipContent>
                          </Tooltip>
                        </div>
                      </div>

                      <!-- Row 2: Summary -->
                      <p class="text-sm font-text text-foreground/90 leading-relaxed line-clamp-4">
                        {{ agent.summary || 'No summary available' }}
                      </p>

                      <!-- Row 2b: Next Steps (if present) -->
                      <p v-if="agent.next_steps" class="text-xs font-text text-muted-foreground leading-snug line-clamp-2 italic">
                        <span class="not-italic font-medium text-foreground/70">Next:</span> {{ agent.next_steps }}
                      </p>

                      <!-- Row 2c: Hierarchy badges (parent / role) -->
                      <div v-if="agent.parent || agent.role || agent.children?.length" class="flex items-center gap-1.5 flex-wrap" @click.stop>
                        <Tooltip v-if="agent.parent">
                          <TooltipTrigger as-child>
                            <button
                              class="inline-flex items-center gap-1 bg-muted/60 border border-border/60 px-1.5 py-0.5 rounded text-[10px] text-muted-foreground hover:text-primary hover:border-primary/40 transition-colors shrink-0"
                              @click.stop="emit('select-agent', agent.parent!)"
                            >
                              ↑ {{ agent.parent }}
                            </button>
                          </TooltipTrigger>
                          <TooltipContent>Go to parent: {{ agent.parent }}</TooltipContent>
                        </Tooltip>
                        <template v-if="agent.children?.length">
                          <Tooltip v-for="child in agent.children" :key="child">
                            <TooltipTrigger as-child>
                              <button
                                class="inline-flex items-center gap-1 bg-muted/60 border border-border/60 px-1.5 py-0.5 rounded text-[10px] text-muted-foreground hover:text-primary hover:border-primary/40 transition-colors shrink-0"
                                @click.stop="emit('select-agent', child)"
                              >
                                ↓ {{ child }}
                              </button>
                            </TooltipTrigger>
                            <TooltipContent>Go to: {{ child }}</TooltipContent>
                          </Tooltip>
                        </template>
                        <span
                          v-if="agent.role"
                          class="inline-flex items-center gap-1 bg-role/10 border border-role/20 px-1.5 py-0.5 rounded text-[10px] text-role shrink-0"
                        >
                          {{ agent.role }}
                        </span>
                      </div>

                      <!-- Row 3: Metadata — badges on left, timestamp on right -->
                      <div class="flex items-end justify-between gap-2 text-[11px] text-muted-foreground">
                        <div class="flex flex-col gap-1 min-w-0">
                          <Tooltip v-if="agent.phase">
                            <TooltipTrigger as-child>
                              <span class="inline-flex items-center gap-1 bg-muted px-1.5 py-0.5 rounded text-[10px] truncate max-w-[160px] cursor-default w-fit">
                                <Layers class="size-3 shrink-0" />
                                {{ agent.phase }}
                              </span>
                            </TooltipTrigger>
                            <TooltipContent>Phase: {{ agent.phase }}</TooltipContent>
                          </Tooltip>
                          <div v-if="agent.branch || agent.pr || agent.items?.length" class="flex items-center gap-1.5 flex-wrap">
                            <Tooltip v-if="agent.branch">
                              <TooltipTrigger as-child>
                                <span class="inline-flex items-center gap-1 font-mono bg-muted px-1.5 py-0.5 rounded text-[10px] truncate max-w-[140px] cursor-default">
                                  <GitBranch class="size-3 shrink-0" />
                                  {{ agent.branch }}
                                </span>
                              </TooltipTrigger>
                              <TooltipContent>
                                <p>Branch: {{ agent.branch }}</p>
                                <p v-if="agent.repo_url">Repo: {{ agent.repo_url }}</p>
                              </TooltipContent>
                            </Tooltip>
                            <a
                              v-if="agent.pr && prLink(agent)"
                              :href="prLink(agent)!"
                              target="_blank"
                              rel="noopener noreferrer"
                              class="inline-flex items-center gap-0.5 text-primary/70 hover:text-primary transition-colors shrink-0"
                              :title="prLink(agent)!"
                              @click.stop
                            >
                              <ExternalLink class="size-3" />
                              {{ agent.pr }}
                            </a>
                            <!-- Items count chip: reporting depth at a glance -->
                            <Tooltip v-if="agent.items?.length">
                              <TooltipTrigger as-child>
                                <span class="inline-flex items-center gap-1 bg-muted px-1.5 py-0.5 rounded text-[10px] text-muted-foreground cursor-default tabular-nums">
                                  {{ agent.items.length }} item{{ agent.items.length !== 1 ? 's' : '' }}
                                </span>
                              </TooltipTrigger>
                              <TooltipContent>Agent reported {{ agent.items.length }} item{{ agent.items.length !== 1 ? 's' : '' }}</TooltipContent>
                            </Tooltip>
                          </div>
                        </div>
                        <Tooltip>
                          <TooltipTrigger as-child>
                            <span class="inline-flex items-center gap-1 cursor-default whitespace-nowrap shrink-0 font-text">
                              <Clock class="size-3 shrink-0" />
                              {{ relativeTime(agent.updated_at) }}
                            </span>
                          </TooltipTrigger>
                          <TooltipContent>{{ formatFullDate(agent.updated_at) }}</TooltipContent>
                        </Tooltip>
                      </div>

                      <!-- Row 4: Compact attention indicator -->
                      <div
                        v-if="hasAttention(agent)"
                        class="flex items-center gap-1.5 text-[11px] overflow-hidden"
                        @click.stop
                      >
                        <AlertTriangle v-if="agent.blockers?.length" class="size-3 shrink-0 text-orange-500" />
                        <span v-if="agent.blockers?.length" class="text-orange-600 dark:text-orange-400 truncate">
                          {{ agent.blockers.length }} blocker{{ agent.blockers.length !== 1 ? 's' : '' }}: {{ agent.blockers[0] }}
                        </span>
                        <span v-if="agent.blockers?.length && agent.questions?.length" class="shrink-0 text-border">·</span>
                        <HelpCircle v-if="agent.questions?.length" class="size-3 shrink-0 text-amber-500" />
                        <span v-if="agent.questions?.length" class="text-amber-600 dark:text-amber-400 truncate">
                          {{ agent.questions.length }} question{{ agent.questions.length !== 1 ? 's' : '' }}: {{ agent.questions[0] }}
                        </span>
                        <Button
                          variant="outline"
                          size="sm"
                          class="h-8 px-1.5 text-[10px] ml-auto shrink-0"
                          aria-label="View agent and respond"
                          @click.stop="emit('select-agent', name)"
                        >
                          <MessageSquareReply class="size-3" />
                          Respond
                        </Button>
                      </div>

                      <!-- Row 5: Footer Actions -->
                      <div class="flex items-center gap-2 pt-1 border-t border-border/50 opacity-40 group-hover:opacity-100 focus-within:opacity-100 group-focus-within:opacity-100 transition-opacity" @click.stop>
                        <Tooltip>
                          <TooltipTrigger as-child>
                            <Button
                              variant="outline"
                              size="sm"
                              class="h-8 px-2.5 text-xs"
                              aria-label="Nudge agent"
                              @click.stop="emit('broadcast-agent', name)"
                            >
                              <Bell class="size-3.5" />
                              Nudge
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>Send a nudge to {{ name }}</TooltipContent>
                        </Tooltip>
                        <Tooltip>
                          <TooltipTrigger as-child>
                            <Button
                              variant="outline"
                              size="sm"
                              class="h-8 px-2.5 text-xs"
                              aria-label="Send message to agent"
                              @click.stop="openMessageDialog(name)"
                            >
                              <MessageSquare class="size-3.5" />
                              Message
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>Send a message to {{ name }}</TooltipContent>
                        </Tooltip>
                        <div class="flex-1" />
                        <Tooltip>
                          <TooltipTrigger as-child>
                            <Button
                              variant="ghost"
                              size="sm"
                              class="h-8 w-8 p-0 text-muted-foreground/40 hover:text-destructive transition-colors"
                              aria-label="Delete agent"
                              @click.stop="openDeleteDialog(name)"
                            >
                              <Trash2 class="size-3.5" />
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>Delete {{ name }}</TooltipContent>
                        </Tooltip>
                      </div>
                    </CardContent>
                  </Card>
                </div>
              </div>
            </template>

            <!-- Done / Idle — compact list rows to save grid space -->
            <div v-if="doneIdleAgents.length > 0">
              <div class="flex items-center gap-2 mb-2">
                <span class="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Done / Idle</span>
                <span class="text-xs text-muted-foreground tabular-nums">{{ doneIdleAgents.length }}</span>
                <div class="flex-1 border-t border-border/30" />
              </div>
              <div class="space-y-1" role="list" aria-label="Done and idle agents">
                <div
                  v-for="[name, agent] in doneIdleAgents"
                  :key="name"
                  role="listitem"
                  class="group flex items-center gap-2.5 px-3 py-2 rounded-md border border-border/40 bg-muted/10 opacity-70 hover:opacity-100 hover:bg-accent/30 transition-all cursor-pointer"
                  :class="recentlyUpdated.has(name) ? 'ring-2 ring-primary/40 transition-shadow' : ''"
                  :aria-label="`Agent ${name}, status: ${agent.status}`"
                  tabindex="0"
                  @click="emit('select-agent', name)"
                  @keydown="handleCardKeydown($event, name)"
                >
                  <!-- Avatar + freshness dot + name with hover card -->
                  <AgentProfileCard
                    :agent-name="name"
                    :agent="agent"
                    :space-name="space.name"
                    @select-agent="emit('select-agent', $event)"
                  >
                    <div class="flex items-center gap-2 shrink-0" @click.stop>
                      <div class="relative inline-block">
                        <AgentAvatar :name="name" :size="20" aria-hidden="true" />
                        <span
                          class="absolute -bottom-0.5 -right-0.5 block size-1.5 rounded-full ring-1 ring-card"
                          :class="freshnessDotClass(agent.updated_at)"
                        />
                      </div>
                      <span class="font-medium text-sm">{{ name }}</span>
                    </div>
                  </AgentProfileCard>
                  <!-- Status badge -->
                  <StatusBadge :status="agent.status" />
                  <!-- Summary -->
                  <span class="text-xs text-muted-foreground truncate flex-1 min-w-0">{{ agent.summary || 'No summary' }}</span>
                  <!-- Items count -->
                  <span
                    v-if="agent.items?.length"
                    class="shrink-0 text-[10px] bg-muted px-1.5 py-0.5 rounded text-muted-foreground tabular-nums"
                  >
                    {{ agent.items.length }} item{{ agent.items.length !== 1 ? 's' : '' }}
                  </span>
                  <!-- Timestamp -->
                  <Tooltip>
                    <TooltipTrigger as-child>
                      <span class="shrink-0 text-[11px] text-muted-foreground/70 whitespace-nowrap inline-flex items-center gap-0.5 cursor-default">
                        <Clock class="size-3" />
                        {{ relativeTime(agent.updated_at) }}
                      </span>
                    </TooltipTrigger>
                    <TooltipContent>{{ formatFullDate(agent.updated_at) }}</TooltipContent>
                  </Tooltip>
                  <!-- Hover actions -->
                  <div class="flex items-center gap-1 opacity-40 group-hover:opacity-100 focus-within:opacity-100 transition-opacity shrink-0" @click.stop>
                    <Tooltip>
                      <TooltipTrigger as-child>
                        <Button
                          variant="ghost"
                          size="sm"
                          class="h-6 w-6 p-0"
                          :aria-label="`Message ${name}`"
                          @click.stop="openMessageDialog(name)"
                        >
                          <MessageSquare class="size-3" />
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>Message {{ name }}</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger as-child>
                        <Button
                          variant="ghost"
                          size="sm"
                          class="h-6 w-6 p-0 text-muted-foreground/40 hover:text-destructive transition-colors"
                          :aria-label="`Delete ${name}`"
                          @click.stop="openDeleteDialog(name)"
                        >
                          <Trash2 class="size-3" />
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>Delete {{ name }}</TooltipContent>
                    </Tooltip>
                  </div>
                </div>
              </div>
            </div>

            <!-- Empty state -->
            <div
              v-if="agentCount === 0"
              class="flex flex-col items-center justify-center py-16 text-muted-foreground font-text text-center gap-3"
            >
              <div class="rounded-full bg-muted p-3.5">
                <Plus class="size-6 opacity-50" aria-hidden="true" />
              </div>
              <div>
                <p class="text-base font-medium text-foreground">No agents in this space</p>
                <p class="text-sm mt-1">An agent is an AI session connected to this board.</p>
                <p class="text-sm">Create an agent to get started, or spawn one from the CLI.</p>
              </div>
              <Button size="sm" @click="agentCreateDialogOpen = true">
                <Plus class="size-4 mr-1" />
                Add Agent
              </Button>
            </div>
            <div
              v-else-if="filteredSortedAgents.length === 0"
              class="flex flex-col items-center justify-center py-12 text-muted-foreground font-text text-center"
            >
              <Search class="size-8 mb-3 opacity-30" aria-hidden="true" />
              <p class="text-base">No agents match "{{ agentSearch }}"</p>
              <p class="text-sm mt-1">Try a different search term</p>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="inbox">
          <InterruptTracker ref="inboxRef" :space-name="space.name" />
        </TabsContent>

        <TabsContent value="timeline">
          <GanttTimeline :space-name="space.name" :agents="space.agents" @select-agent="emit('select-agent', $event)" />
        </TabsContent>

        <TabsContent value="hierarchy">
          <HierarchyView
            :tree="hierarchy ?? { space: space.name, roots: [], nodes: {} }"
            :agents="space.agents"
            @select-agent="emit('select-agent', $event)"
          />
        </TabsContent>
      </Tabs>

      <!-- Clear done/idle agents confirmation dialog -->
      <AlertDialog v-model:open="clearDoneDialogOpen">
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear {{ doneIdleAgents.length }} done/idle agent{{ doneIdleAgents.length !== 1 ? 's' : '' }}?</AlertDialogTitle>
            <AlertDialogDescription>
              This will remove all done and idle agents from this space. Active, blocked, and error agents will remain. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              @click="emit('clear-done-agents', doneIdleAgents.map(([name]) => name))"
            >
              <Trash2 class="size-4" />
              Clear {{ doneIdleAgents.length }} agent{{ doneIdleAgents.length !== 1 ? 's' : '' }}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <!-- Delete space confirmation dialog -->
      <AlertDialog v-model:open="deleteSpaceDialogOpen">
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete space "{{ space.name }}"?</AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently delete the space and all its agent data. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              @click="emit('delete-space')"
            >
              <Trash2 class="size-4" />
              Delete Space
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <!-- Delete agent confirmation dialog -->
      <AlertDialog v-model:open="deleteDialogOpen">
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete agent?</AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently remove
              <span class="font-semibold text-foreground">{{ deleteDialogAgent }}</span>.
              This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              @click="confirmDeleteAgent()"
            >
              <Trash2 class="size-4" />
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <!-- Quick message dialog -->
      <Dialog v-model:open="messageDialogOpen">
        <DialogContent class="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              Message {{ messageDialogAgent }}
            </DialogTitle>
            <DialogDescription>
              Send a quick message to this agent. They'll see it on their next check-in.
            </DialogDescription>
          </DialogHeader>
          <form @submit.prevent="sendQuickMessage">
            <div class="flex flex-col gap-2">
              <Textarea
                ref="messageInputRef"
                v-model="messageText"
                placeholder="Type your message..."
                :rows="3"
                @keydown.escape="messageDialogOpen = false"
                @keydown.ctrl.enter.prevent="sendQuickMessage"
              />
              <p class="text-xs text-muted-foreground">Ctrl+Enter to send</p>
              <Button
                type="submit"
                size="sm"
                class="self-end shrink-0"
                :disabled="!messageText.trim()"
              >
                <SendHorizontal class="size-4" />
                Send
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <AgentCreateDialog
        v-model:open="agentCreateDialogOpen"
        :space="space.name"
        @created="() => {}"
      />
    </div>
  </ScrollArea>
</template>
