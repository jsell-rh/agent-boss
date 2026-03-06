<script setup lang="ts">
import type { KnowledgeSpace, TmuxAgentStatus } from '@/types'
import { ref, computed, nextTick } from 'vue'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
  MessageSquare,
  SendHorizontal,
  HelpCircle,
  AlertTriangle,
  MessageSquareReply,
  GitBranch,
  ExternalLink,
  Clock,
} from 'lucide-vue-next'
import StatusBadge from './StatusBadge.vue'
import InterruptTracker from './InterruptTracker.vue'
import AgentAvatar from './AgentAvatar.vue'

const props = defineProps<{
  space: KnowledgeSpace
  tmuxStatus: Record<string, TmuxAgentStatus> | null
}>()

const emit = defineEmits<{
  'select-agent': [name: string]
  broadcast: []
  'delete-agent': [name: string]
  'broadcast-agent': [name: string]
  'send-message-to-agent': [agentName: string, text: string]
}>()

const deleteDialogOpen = ref(false)
const deleteDialogAgent = ref<string | null>(null)
const messageDialogOpen = ref(false)
const messageDialogAgent = ref<string | null>(null)
const messageText = ref('')
const messageInputRef = ref<HTMLInputElement | null>(null)

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
  emit('send-message-to-agent', messageDialogAgent.value, text)
  messageDialogOpen.value = false
  messageDialogAgent.value = null
  messageText.value = ''
}

/** Returns a relative time string like "3m ago" */
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

/** Returns freshness tier for visual indicator */
function freshness(dateStr: string): 'live' | 'recent' | 'normal' | 'stale' {
  const diff = Date.now() - new Date(dateStr).getTime()
  if (diff < 60_000) return 'live'     // < 1 min
  if (diff < 300_000) return 'recent'  // < 5 min
  if (diff < 1_800_000) return 'normal' // < 30 min
  return 'stale'
}

function handleCardKeydown(e: KeyboardEvent, name: string) {
  const tag = (e.target as HTMLElement)?.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'BUTTON') return
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    emit('select-agent', name)
  }
}

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

/** Check if an agent has any attention items */
function hasAttention(agent: { questions?: string[]; blockers?: string[] }): boolean {
  return (agent.questions?.length ?? 0) > 0 || (agent.blockers?.length ?? 0) > 0
}
</script>

<template>
  <ScrollArea class="h-full">
    <div class="p-6 space-y-6 max-w-6xl">
      <!-- Header -->
      <div class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight">{{ space.name }}</h1>
          <p class="text-sm text-muted-foreground font-text">
            {{ headerSummary }}
          </p>
        </div>
        <Tooltip>
          <TooltipTrigger as-child>
            <Button
              variant="outline"
              size="sm"
              :disabled="agentCount === 0"
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
      </div>

      <!-- Tabs: Agents / Inbox -->
      <Tabs default-value="agents">
        <TabsList>
          <TabsTrigger value="agents">Agents</TabsTrigger>
          <TabsTrigger value="inbox" class="gap-1.5">
            Inbox
            <Badge
              v-if="inboxPending > 0"
              variant="destructive"
              class="h-5 min-w-5 px-1 text-[10px] font-semibold tabular-nums"
            >
              {{ inboxPending }}
            </Badge>
          </TabsTrigger>
        </TabsList>

        <TabsContent value="agents">
          <!-- Agent Grid -->
          <div
            class="grid gap-4 grid-cols-1 md:grid-cols-2 lg:grid-cols-3"
            role="list"
            aria-label="Agents in this space"
          >
            <Card
              v-for="[name, agent] in sortedAgents"
              :key="name"
              role="listitem"
              tabindex="0"
              class="group cursor-pointer transition-all duration-150 hover:bg-accent/50 focus-visible:outline-2 focus-visible:outline-ring focus-visible:outline-offset-2 relative flex flex-col min-h-[180px]"
              :class="[
                agent.blockers?.length
                  ? 'border-l-4 border-l-red-500 shadow-md shadow-red-500/5'
                  : agent.questions?.length
                    ? 'border-l-4 border-l-amber-500 shadow-md shadow-amber-500/5'
                    : '',
                agent.status === 'done' ? 'opacity-70' : '',
              ]"
              :aria-label="`Agent ${name}, status: ${agent.status}${agent.summary ? ', ' + agent.summary : ''}`"
              @click="emit('select-agent', name)"
              @keydown="handleCardKeydown($event, name)"
            >
              <CardContent class="flex flex-col flex-1 p-4 gap-3">
                <!-- Row 1: Header — Avatar + Name + Status -->
                <div class="flex items-center justify-between gap-2">
                  <div class="flex items-center gap-2.5 min-w-0">
                    <AgentAvatar :name="name" :size="28" />
                    <span class="text-base font-semibold truncate">{{ name }}</span>
                  </div>
                  <div class="flex items-center gap-1.5 shrink-0">
                    <!-- Freshness indicator -->
                    <Tooltip>
                      <TooltipTrigger as-child>
                        <span
                          class="relative inline-flex size-2.5 shrink-0"
                          :aria-label="`Last updated ${relativeTime(agent.updated_at)}`"
                        >
                          <span
                            v-if="freshness(agent.updated_at) === 'live'"
                            class="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-400 opacity-75"
                          />
                          <span
                            class="relative inline-flex size-2.5 rounded-full"
                            :class="{
                              'bg-green-500': freshness(agent.updated_at) === 'live',
                              'bg-green-500/70': freshness(agent.updated_at) === 'recent',
                              'bg-muted-foreground/40': freshness(agent.updated_at) === 'normal',
                              'bg-muted-foreground/20': freshness(agent.updated_at) === 'stale',
                            }"
                          />
                        </span>
                      </TooltipTrigger>
                      <TooltipContent>
                        Updated {{ relativeTime(agent.updated_at) }} — {{ formatFullDate(agent.updated_at) }}
                      </TooltipContent>
                    </Tooltip>
                    <StatusBadge :status="agent.status" />
                    <Tooltip v-if="tmuxStatus?.[name]?.needs_approval">
                      <TooltipTrigger as-child>
                        <Badge
                          variant="outline"
                          class="border-primary/50 text-primary text-[10px] h-5 px-1.5"
                        >
                          Approval
                        </Badge>
                      </TooltipTrigger>
                      <TooltipContent>
                        Agent is waiting for tool-use approval
                      </TooltipContent>
                    </Tooltip>
                  </div>
                </div>

                <!-- Row 2: Summary — THE HERO -->
                <p class="text-sm font-text text-foreground/90 leading-relaxed line-clamp-4 flex-1">
                  {{ agent.summary || 'No summary available' }}
                </p>

                <!-- Row 3: Metadata — compact, muted -->
                <div class="flex items-center gap-2 text-[11px] text-muted-foreground font-text flex-wrap">
                  <span v-if="agent.phase" class="truncate max-w-[100px]" :title="`Phase: ${agent.phase}`">
                    {{ agent.phase }}
                  </span>
                  <span v-if="agent.phase && (agent.branch || agent.pr)" class="text-border">·</span>
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
                    v-if="agent.pr"
                    :href="agent.pr"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="inline-flex items-center gap-0.5 text-primary/70 hover:text-primary transition-colors"
                    :title="agent.pr"
                    @click.stop
                  >
                    <ExternalLink class="size-3" />
                    PR
                  </a>
                  <span class="ml-auto inline-flex items-center gap-1 shrink-0">
                    <Clock class="size-3" />
                    {{ relativeTime(agent.updated_at) }}
                  </span>
                </div>

                <!-- Row 4: Attention banner (only if questions/blockers) -->
                <div
                  v-if="hasAttention(agent)"
                  class="space-y-1.5"
                  @click.stop
                >
                  <!-- Blocker banner -->
                  <div
                    v-if="agent.blockers?.length"
                    class="flex items-start gap-2 rounded-md bg-red-500/10 border border-red-500/20 px-3 py-2"
                  >
                    <AlertTriangle class="size-3.5 text-red-500 shrink-0 mt-0.5" />
                    <div class="flex-1 min-w-0">
                      <p class="text-[11px] font-semibold text-red-600 dark:text-red-400 leading-none mb-0.5">
                        {{ agent.blockers.length }} blocker{{ agent.blockers.length !== 1 ? 's' : '' }}
                      </p>
                      <p class="text-[11px] text-red-600/80 dark:text-red-400/80 font-text line-clamp-1">
                        {{ agent.blockers[0] }}
                      </p>
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      class="h-6 px-2 text-[10px] border-red-500/30 text-red-600 dark:text-red-400 hover:bg-red-500/10 shrink-0"
                      aria-label="Reply to blocker"
                      @click.stop="emit('select-agent', name)"
                    >
                      <MessageSquareReply class="size-3" />
                      Reply
                    </Button>
                  </div>
                  <!-- Question banner -->
                  <div
                    v-if="agent.questions?.length"
                    class="flex items-start gap-2 rounded-md bg-amber-500/10 border border-amber-500/20 px-3 py-2"
                  >
                    <HelpCircle class="size-3.5 text-amber-500 shrink-0 mt-0.5" />
                    <div class="flex-1 min-w-0">
                      <p class="text-[11px] font-semibold text-amber-600 dark:text-amber-400 leading-none mb-0.5">
                        {{ agent.questions.length }} question{{ agent.questions.length !== 1 ? 's' : '' }}
                      </p>
                      <p class="text-[11px] text-amber-600/80 dark:text-amber-400/80 font-text line-clamp-1">
                        {{ agent.questions[0] }}
                      </p>
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      class="h-6 px-2 text-[10px] border-amber-500/30 text-amber-600 dark:text-amber-400 hover:bg-amber-500/10 shrink-0"
                      aria-label="Reply to question"
                      @click.stop="emit('select-agent', name)"
                    >
                      <MessageSquareReply class="size-3" />
                      Reply
                    </Button>
                  </div>
                </div>

                <!-- Row 5: Footer — Actions -->
                <div class="flex items-center gap-2 pt-1 border-t border-border/50" @click.stop>
                  <Tooltip>
                    <TooltipTrigger as-child>
                      <Button
                        variant="outline"
                        size="sm"
                        class="h-7 px-2.5 text-xs"
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
                        class="h-7 px-2.5 text-xs"
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
                        class="h-7 w-7 p-0 text-muted-foreground/40 hover:text-destructive transition-colors"
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

          <!-- Empty state -->
          <div
            v-if="agentCount === 0"
            class="flex flex-col items-center justify-center py-16 text-muted-foreground font-text text-center"
          >
            <p class="text-lg">No agents in this space yet</p>
            <p class="text-sm mt-1">Agents will appear here when they register via the API</p>
          </div>
        </TabsContent>

        <TabsContent value="inbox">
          <InterruptTracker ref="inboxRef" :space-name="space.name" />
        </TabsContent>
      </Tabs>

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
            <div class="flex gap-2">
              <Input
                ref="messageInputRef"
                v-model="messageText"
                placeholder="Type your message..."
                class="flex-1"
                @keydown.escape="messageDialogOpen = false"
              />
              <Button
                type="submit"
                size="sm"
                class="shrink-0"
                :disabled="!messageText.trim()"
              >
                <SendHorizontal class="size-4" />
                Send
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  </ScrollArea>
</template>
