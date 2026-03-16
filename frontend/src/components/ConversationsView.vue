<script setup lang="ts">
import { ref, computed, watch, watchEffect, onMounted, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import type { KnowledgeSpace, AgentUpdate } from '@/types'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import AgentAvatar from './AgentAvatar.vue'
import AgentProfileCard from './AgentProfileCard.vue'
import StatusBadge from './StatusBadge.vue'
import NewTaskDialog from './NewTaskDialog.vue'
import { MessageSquare, Search, X, GitBranch, ExternalLink, SendHorizontal, Plus, Check, HelpCircle, Loader2, CheckCircle2, ChevronDown, ChevronUp, Crown } from 'lucide-vue-next'
import { renderMarkdown, linkTaskRefs } from '@/lib/markdown'
import { prLink } from '@/lib/utils'
import type { Task } from '@/types'
import { relativeTime, formatFullDate } from '@/composables/useTime'
import api from '@/api/client'

const props = defineProps<{
  space: KnowledgeSpace
  preselectAgent?: string
}>()

interface ConversationMessage {
  id: string
  message: string
  sender: string
  recipient: string
  timestamp: string
  priority?: import('@/types').MessagePriority
  type?: import('@/types').MessageType
  resolved?: boolean
  resolution?: string
  read?: boolean
}

interface Conversation {
  key: string
  participants: [string, string]
  messages: ConversationMessage[]
  lastMessageAt: string
}

// Messages fetched from /spaces/:space/messages — decoupled from the space JSON
// (which no longer embeds message histories after the perf fix in PR #195).
const spaceMessages = ref<Record<string, { messages: import('@/types').AgentMessage[]; has_more: boolean }>>({})
const loadingEarlier = ref(false)
// Tracks whether any agent in the current conversation has more messages to load.
const conversationHasMore = computed(() => {
  if (!selectedKey.value) return false
  const conv = conversations.value.find(c => c.key === selectedKey.value)
  if (!conv) return false
  return conv.participants.some(p => spaceMessages.value[p]?.has_more)
})

const MESSAGE_LIMIT = 50

async function loadSpaceMessages() {
  try {
    spaceMessages.value = await api.fetchSpaceMessages(props.space.name, { limit: MESSAGE_LIMIT })
  } catch {
    // non-fatal: falls back to agentData.messages (empty after PR #195)
  }
}

async function loadEarlierMessages() {
  if (loadingEarlier.value || !selectedKey.value) return
  const conv = conversations.value.find(c => c.key === selectedKey.value)
  if (!conv || conv.messages.length === 0) return

  // Find the oldest message timestamp across the conversation.
  const oldest = conv.messages[0]!.timestamp
  loadingEarlier.value = true
  try {
    const older = await api.fetchSpaceMessages(props.space.name, {
      limit: MESSAGE_LIMIT,
      before: oldest,
    })
    // Merge older messages in front of existing ones, deduplicating by id.
    for (const [agent, data] of Object.entries(older)) {
      const existing = spaceMessages.value[agent]
      if (!existing) {
        spaceMessages.value[agent] = data
      } else {
        const existingIds = new Set(existing.messages.map(m => m.id))
        const prepend = data.messages.filter(m => !existingIds.has(m.id))
        spaceMessages.value[agent] = {
          messages: [...prepend, ...existing.messages],
          has_more: data.has_more,
        }
      }
    }
  } catch {
    // non-fatal
  } finally {
    loadingEarlier.value = false
  }
}

onMounted(loadSpaceMessages)
watch(() => props.space.name, loadSpaceMessages)

// Reconstruct pairwise conversation threads from all agents' message inboxes.
const conversations = computed((): Conversation[] => {
  const convMap = new Map<string, Conversation>()

  for (const [agentName, agentData] of Object.entries(props.space.agents)) {
    const msgs = spaceMessages.value[agentName]?.messages ?? agentData.messages ?? []
    for (const msg of msgs) {
      const sorted = [agentName, msg.sender].sort()
      const participants = sorted as [string, string]
      const key = participants.join('\u2194')

      if (!convMap.has(key)) {
        convMap.set(key, { key, participants, messages: [], lastMessageAt: msg.timestamp })
      }

      convMap.get(key)!.messages.push({
        id: msg.id,
        message: msg.message,
        sender: msg.sender,
        recipient: agentName,
        timestamp: msg.timestamp,
        priority: msg.priority,
        type: msg.type,
        resolved: msg.resolved,
        resolution: msg.resolution,
        read: msg.read,
      })
    }
  }

  for (const conv of convMap.values()) {
    conv.messages.sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime())
    const last = conv.messages[conv.messages.length - 1]
    if (last) conv.lastMessageAt = last.timestamp
  }

  return [...convMap.values()].sort(
    (a, b) => new Date(b.lastMessageAt).getTime() - new Date(a.lastMessageAt).getTime(),
  )
})

const searchQuery = ref('')
const selectedKey = ref<string | null>(null)

const filteredConversations = computed(() => {
  const q = searchQuery.value.toLowerCase()
  if (!q) return conversations.value
  return conversations.value.filter(conv =>
    conv.participants.some(p => p.toLowerCase().includes(q)) ||
    conv.messages.some(m => m.message.toLowerCase().includes(q)),
  )
})

const bossConversations = computed(() =>
  filteredConversations.value.filter(c => c.participants.includes('boss')),
)

const agentConversations = computed(() =>
  filteredConversations.value.filter(c => !c.participants.includes('boss')),
)

// selectedConversation — includes virtual entry for preselectAgent with no history
const selectedConversation = computed((): Conversation | null => {
  const found = conversations.value.find(c => c.key === selectedKey.value)
  if (found) return found
  // Virtual conversation (no messages yet) from preselectAgent or New Message picker
  if (selectedKey.value) {
    const parts = selectedKey.value.split('\u2194') as [string, string]
    return { key: selectedKey.value, participants: parts, messages: [], lastMessageAt: '' }
  }
  return null
})

// Unread tracking — only boss ↔ agent conversations can be "unread".
// Agent-to-agent threads do not drive unread badges (boss can't act on them).
const readKeys = ref(new Set<string>())

function isBossConversation(conv: Conversation): boolean {
  return conv.participants.includes('boss')
}

function unreadCount(conv: Conversation): number {
  // Agent-to-agent conversations never show unread badges
  if (!isBossConversation(conv)) return 0
  if (readKeys.value.has(conv.key)) return 0
  // Only count messages directed at boss that haven't been acknowledged on the backend
  return conv.messages.filter(m => m.recipient === 'boss' && !m.read).length
}

// ACK all unread messages to boss in a conversation so the backend persists read state.
// This clears the sidebar badge (which reads msg.read from live space data) and ensures
// the conversation stays read after navigate-away + return.
function ackBossMessages(conv: Conversation) {
  if (!isBossConversation(conv)) return
  const unread = conv.messages.filter(m => m.recipient === 'boss' && !m.read)
  for (const msg of unread) {
    api.ackMessage(props.space.name, 'boss', msg.id, 'boss').catch(() => {})
  }
}

// Mark conversation as read (optimistic local state) and ACK on backend
watch(selectedKey, key => {
  if (!key) return
  readKeys.value.add(key)
  const conv = conversations.value.find(c => c.key === key)
  if (conv) ackBossMessages(conv)
})

// Resolve the best conversation key for a given agent name:
// prefer an existing conversation involving that agent; fall back to boss↔agent.
function resolveConversationKey(agent: string): string {
  const existing = conversations.value.find(c => c.participants.includes(agent))
  if (existing) return existing.key
  return [agent, 'boss'].sort().join('\u2194')
}

// Pre-select from preselectAgent prop (set by App.vue from router param or when starting new conv)
onMounted(() => {
  if (props.preselectAgent) {
    selectedKey.value = resolveConversationKey(props.preselectAgent)
  }
  scrollThreadToBottom()
})

// Also react to preselectAgent prop changes (e.g. navigating between conversation routes)
watch(() => props.preselectAgent, agent => {
  if (agent) {
    selectedKey.value = resolveConversationKey(agent)
  }
})

// Auto-select first conversation if nothing is selected
watch(
  conversations,
  convs => {
    if (!selectedKey.value && convs.length > 0) {
      selectedKey.value = convs[0]!.key
    }
  },
  { immediate: true },
)

function formatRelativeTime(timestamp: string): string {
  const d = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - d.getTime()
  const diffDays = Math.floor(diffMs / 86400000)
  if (diffDays === 0) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (diffDays === 1) return 'Yesterday'
  if (diffDays < 7) return d.toLocaleDateString([], { weekday: 'short' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function formatDaySeparator(timestamp: string): string {
  const d = new Date(timestamp)
  const today = new Date()
  const yesterday = new Date(today)
  yesterday.setDate(yesterday.getDate() - 1)
  if (d.toDateString() === today.toDateString()) return 'Today'
  if (d.toDateString() === yesterday.toDateString()) return 'Yesterday'
  return d.toLocaleDateString([], { weekday: 'long', month: 'short', day: 'numeric' })
}

function getDateKey(timestamp: string): string {
  return new Date(timestamp).toDateString()
}

// Conversation display title — if boss is a participant, show just the other agent's name
function convTitle(conv: { participants: string[] }): string {
  const { participants } = conv
  if (participants.includes('boss')) {
    const other = participants.find(p => p !== 'boss')
    return other ?? participants.join(' ↔ ')
  }
  return participants.join(' ↔ ')
}

// Strip basic markdown for plain-text previews (bold, italic, code, headers, bullets)
function stripMarkdown(text: string): string {
  return text
    .replace(/#{1,6}\s+/g, '')
    .replace(/\*\*(.+?)\*\*/g, '$1')
    .replace(/\*(.+?)\*/g, '$1')
    .replace(/__(.+?)__/g, '$1')
    .replace(/_(.+?)_/g, '$1')
    .replace(/`(.+?)`/g, '$1')
    .replace(/^\s*[-*+]\s+/gm, '')
    .replace(/\[(.+?)\]\(.+?\)/g, '$1')
    .replace(/\n+/g, ' ')
    .trim()
}

// Auto-resize textarea as user types (H-NEW-5)
function autoResizeTextarea(e: Event) {
  const el = e.target as HTMLTextAreaElement
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, 160)}px`
}

// ── Agent detail slideover ──────────────────────────────────────────
const slideoverAgentName = ref<string | null>(null)

const slideoverAgent = computed<AgentUpdate | null>(() => {
  if (!slideoverAgentName.value) return null
  return props.space.agents[slideoverAgentName.value] ?? null
})

function openSlideover(agentName: string) {
  slideoverAgentName.value = agentName
}

function closeSlideover() {
  slideoverAgentName.value = null
}

const router = useRouter()

function goToAgentDetail(agentName: string) {
  slideoverAgentName.value = null
  router.push(`/${encodeURIComponent(props.space.name)}/${encodeURIComponent(agentName)}`)
}

// ── New Message picker ──────────────────────────────────────────────
const newMsgPickerOpen = ref(false)
const newMsgSearch = ref('')
const newMsgInputRef = ref<HTMLInputElement | null>(null)

const allAgentNames = computed(() => Object.keys(props.space.agents).sort())

const filteredAgentNames = computed(() => {
  const q = newMsgSearch.value.toLowerCase()
  if (!q) return allAgentNames.value
  return allAgentNames.value.filter(n => n.toLowerCase().includes(q))
})

function openNewMsgPicker() {
  newMsgSearch.value = ''
  newMsgPickerOpen.value = true
  // Focus the search input after render
  setTimeout(() => newMsgInputRef.value?.focus(), 50)
}

function selectNewMsgAgent(agentName: string) {
  newMsgPickerOpen.value = false
  newMsgSearch.value = ''
  // Navigate to the named conversation route so URL is bookmarkable
  router.push({
    name: 'conversation',
    params: { space: props.space.name, conversationAgent: agentName },
  })
  // Also immediately set selectedKey so the thread shows before navigation processes
  const sorted = [agentName, 'boss'].sort()
  selectedKey.value = sorted.join('\u2194')
}

// ── Auto-scroll ─────────────────────────────────────────────────────
const threadScrollRef = ref<InstanceType<typeof ScrollArea> | null>(null)
const isAtBottom = ref(true)

function getThreadScrollEl(): HTMLElement | null {
  return threadScrollRef.value?.$el?.querySelector('[data-radix-scroll-area-viewport]') as HTMLElement | null
}

function checkScrollPosition() {
  const el = getThreadScrollEl()
  if (!el) return
  isAtBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight < 80
}

function scrollThreadToBottom() {
  // Double nextTick: first tick lets Vue update the v-if/v-for DOM,
  // second tick lets Radix ScrollArea initialize its internal viewport.
  nextTick(() => nextTick(() => {
    const el = getThreadScrollEl()
    if (el) {
      el.scrollTop = el.scrollHeight
      isAtBottom.value = true
    }
  }))
}

// Wire up scroll listener for jump-to-bottom tracking
watchEffect((onCleanup) => {
  const el = getThreadScrollEl()
  if (!el) return
  el.addEventListener('scroll', checkScrollPosition, { passive: true })
  onCleanup(() => el.removeEventListener('scroll', checkScrollPosition))
})

// Scroll on conversation switch — also track whether we've scrolled for this key
const _lastScrolledKey = ref<string | null>(null)
watch(selectedKey, () => {
  _lastScrolledKey.value = null  // reset so the messages-length watcher rescrolls when data arrives
  scrollThreadToBottom()
})
watch(
  () => selectedConversation.value?.messages.length,
  (len) => {
    if (len === undefined) return
    // Always scroll when switching conversations (first load of messages for this key)
    if (selectedKey.value && _lastScrolledKey.value !== selectedKey.value) {
      _lastScrolledKey.value = selectedKey.value
      scrollThreadToBottom()
    } else if (isAtBottom.value) {
      // Subsequent messages: only scroll if user is near bottom
      scrollThreadToBottom()
    }
    // ACK any new unread messages that arrived while this conversation is open
    const conv = selectedConversation.value
    if (conv) ackBossMessages(conv)
  },
)

// ── Inline compose ──────────────────────────────────────────────────
const inlineMessage = ref('')
const inlineSending = ref(false)
const inlineSendError = ref<string | null>(null)
const composeRef = ref<HTMLTextAreaElement | null>(null)

// Boss can compose to the other participant (only if boss is in the conversation)
const composeRecipient = computed(() => {
  if (!selectedConversation.value) return null
  const { participants } = selectedConversation.value
  if (!participants.includes('boss')) return null
  return participants.find(p => p !== 'boss') ?? null
})

async function sendInlineCompose() {
  const text = inlineMessage.value.trim()
  const recipient = composeRecipient.value
  if (!text || !recipient) return
  inlineSending.value = true
  inlineSendError.value = null
  try {
    await api.sendMessage(props.space.name, recipient, text, 'boss')
    inlineMessage.value = ''
  } catch (err) {
    inlineSendError.value = err instanceof Error ? err.message : 'Failed to send message'
  } finally {
    inlineSending.value = false
  }
}

function handleComposeKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    sendInlineCompose()
  }
}

// ── Decision reply ───────────────────────────────────────────────────
const decisionReplyTexts = ref<Record<string, string>>({})
const decisionReplying = ref<Record<string, boolean>>({})
const decisionFeedback = ref<Record<string, { ok: boolean; msg: string }>>({})

// Count of unresolved decision messages across all conversations
const pendingDecisionCount = computed(() => {
  let count = 0
  for (const conv of conversations.value) {
    for (const msg of conv.messages) {
      if (msg.type === 'decision' && !msg.resolved) count++
    }
  }
  return count
})

defineExpose({ pendingDecisionCount })

async function replyToDecision(msgId: string, agentName: string) {
  const text = (decisionReplyTexts.value[msgId] ?? '').trim()
  if (!text) return
  decisionReplying.value[msgId] = true
  decisionFeedback.value[msgId] = { ok: true, msg: '' }
  try {
    // Send the reply to the agent, passing the decision ID so the backend marks it resolved.
    await api.sendMessage(props.space.name, agentName, text, 'boss', msgId)
    decisionReplyTexts.value[msgId] = ''
    decisionFeedback.value[msgId] = { ok: true, msg: 'Reply sent' }
    setTimeout(() => { delete decisionFeedback.value[msgId] }, 3000)
    // Optimistically mark the decision resolved in the local reactive state so the embed
    // immediately flips to "Resolved" without waiting for a full space reload.
    const bossMessages = spaceMessages.value['boss']?.messages
    if (bossMessages) {
      const msg = bossMessages.find(m => m.id === msgId)
      if (msg) {
        msg.resolved = true
        msg.resolution = text
      }
    }
  } catch (err) {
    decisionFeedback.value[msgId] = { ok: false, msg: err instanceof Error ? err.message : 'Failed' }
    setTimeout(() => { delete decisionFeedback.value[msgId] }, 3000)
  } finally {
    decisionReplying.value[msgId] = false
  }
}

function handleDecisionKeydown(e: KeyboardEvent, msgId: string, agentName: string) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    replyToDecision(msgId, agentName)
  }
}

// ── Task widget ──────────────────────────────────────────────────────
const agentTasks = ref<Task[]>([])
const tasksLoading = ref(false)
const showTaskPanel = ref(true)
const newTaskDialogOpen = ref(false)

watch(composeRecipient, async (agent) => {
  agentTasks.value = []
  if (!agent) return
  tasksLoading.value = true
  try {
    agentTasks.value = await api.fetchTasks(props.space.name, { assigned_to: agent })
  } catch {
    agentTasks.value = []
  } finally {
    tasksLoading.value = false
  }
}, { immediate: true })
</script>

<template>
  <div class="flex h-full min-h-0 relative overflow-hidden">
    <!-- Left panel: conversation list -->
    <aside
      class="w-72 shrink-0 border-r flex flex-col min-h-0"
      aria-label="Conversations"
    >
      <!-- Search + New Message button -->
      <div class="p-3 border-b shrink-0">
        <div class="flex items-center gap-2">
          <div class="relative flex-1">
            <Search
              class="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground pointer-events-none"
              aria-hidden="true"
            />
            <Input
              v-model="searchQuery"
              type="search"
              placeholder="Filter conversations…"
              class="pl-8 h-8 text-sm"
              aria-label="Filter conversations by agent name"
            />
          </div>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="outline"
                size="icon-sm"
                class="shrink-0 h-8 w-8"
                aria-label="Start new conversation"
                @click="openNewMsgPicker"
              >
                <Plus class="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>New message</TooltipContent>
          </Tooltip>
        </div>

        <!-- Backdrop to close picker on click-outside (M11) -->
        <div v-if="newMsgPickerOpen" class="fixed inset-0 z-10" aria-hidden="true" @click="newMsgPickerOpen = false" />
        <!-- New message agent picker -->
        <div
          v-if="newMsgPickerOpen"
          class="mt-2 rounded-md border bg-popover shadow-md relative z-20"
        >
          <div class="p-2 border-b">
            <Input
              ref="newMsgInputRef"
              v-model="newMsgSearch"
              placeholder="Search agents…"
              class="h-7 text-xs"
              @keydown.escape="newMsgPickerOpen = false"
            />
          </div>
          <div class="max-h-48 overflow-y-auto">
            <div v-if="filteredAgentNames.length === 0" class="px-3 py-4 text-xs text-muted-foreground text-center">
              No agents found
            </div>
            <button
              v-for="name in filteredAgentNames"
              :key="name"
              class="w-full flex items-center gap-2 px-3 py-1.5 text-xs hover:bg-accent transition-colors text-left"
              @click="selectNewMsgAgent(name)"
            >
              <AgentAvatar :name="name" :size="18" />
              {{ name }}
            </button>
          </div>
        </div>
      </div>

      <!-- List -->
      <ScrollArea class="flex-1 min-h-0">
        <!-- Empty state -->
        <div
          v-if="filteredConversations.length === 0"
          class="flex flex-col items-center justify-center h-40 text-center text-muted-foreground p-4"
          role="status"
        >
          <MessageSquare class="size-7 mb-2 opacity-40" aria-hidden="true" />
          <p class="text-sm">
            {{ searchQuery ? 'No matching conversations' : 'No messages yet' }}
          </p>
          <button
            v-if="!searchQuery"
            class="mt-2 text-xs text-primary hover:underline"
            @click="openNewMsgPicker"
          >
            Start a conversation →
          </button>
        </div>

        <div v-else class="py-1" role="listbox" aria-label="Conversation list">
          <!-- With Boss group -->
          <div v-if="bossConversations.length > 0">
            <div class="px-3 pt-2 pb-1 flex items-center gap-1.5">
              <Crown class="size-3 text-amber-500" aria-hidden="true" />
              <span class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">With Boss</span>
            </div>
            <div v-for="conv in bossConversations" :key="conv.key" role="option" :aria-selected="selectedKey === conv.key">
            <button
              class="w-full text-left px-3 py-2.5 hover:bg-amber-500/5 transition-colors flex items-start gap-2.5 min-w-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset border-l-2"
              :class="selectedKey === conv.key ? 'bg-amber-500/8 border-l-amber-500' : 'border-l-transparent hover:border-l-amber-500/40'"
              @click="selectedKey = conv.key"
            >
              <!-- Stacked avatars (clickable to open agent slideover) -->
              <div class="relative shrink-0 w-9 h-9 mt-0.5" @click.stop>
                <AgentProfileCard
                  :agent-name="conv.participants[0]"
                  :agent="space.agents[conv.participants[0]]"
                  :space-name="space.name"
                  @select-agent="goToAgentDetail($event)"
                >
                  <button
                    class="absolute top-0 left-0 rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    :aria-label="`View ${conv.participants[0]} details`"
                    @click="openSlideover(conv.participants[0])"
                  >
                    <AgentAvatar :name="conv.participants[0]" :size="26" />
                  </button>
                </AgentProfileCard>
                <AgentProfileCard
                  :agent-name="conv.participants[1]"
                  :agent="space.agents[conv.participants[1]]"
                  :space-name="space.name"
                  @select-agent="goToAgentDetail($event)"
                >
                  <button
                    class="absolute bottom-0 right-0 rounded-full ring-2 ring-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    :aria-label="`View ${conv.participants[1]} details`"
                    @click="openSlideover(conv.participants[1])"
                  >
                    <AgentAvatar :name="conv.participants[1]" :size="22" />
                  </button>
                </AgentProfileCard>
              </div>

              <!-- Info -->
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-1 justify-between">
                  <span class="text-sm font-medium truncate">
                    {{ convTitle(conv) }}
                  </span>
                  <div class="flex items-center gap-1 shrink-0">
                    <!-- Priority badge — show highest priority in thread -->
                    <Tooltip v-if="conv.messages.some(m => m.priority === 'urgent')">
                      <TooltipTrigger as-child>
                        <span class="text-[9px] font-bold uppercase tracking-wider px-1 py-0.5 rounded bg-red-500/15 text-red-500 border border-red-500/30 cursor-default">urgent</span>
                      </TooltipTrigger>
                      <TooltipContent>This thread contains an urgent message requiring immediate attention</TooltipContent>
                    </Tooltip>
                    <Tooltip v-else-if="conv.messages.some(m => m.priority === 'directive')">
                      <TooltipTrigger as-child>
                        <span class="text-[9px] font-bold uppercase tracking-wider px-1 py-0.5 rounded bg-yellow-500/15 text-yellow-600 border border-yellow-500/30 dark:text-yellow-400 cursor-default">directive</span>
                      </TooltipTrigger>
                      <TooltipContent>This thread contains a directive — an instruction from your manager</TooltipContent>
                    </Tooltip>
                    <!-- Decision pending badge -->
                    <span
                      v-if="conv.messages.some(m => m.type === 'decision' && !m.resolved)"
                      class="inline-flex items-center justify-center rounded-full bg-amber-500/20 text-amber-500 border border-amber-500/30 text-[10px] font-bold min-w-[16px] h-4 px-1"
                      title="Pending decision"
                    >!</span>
                    <!-- Unread badge (boss conversations only) -->
                    <span
                      v-if="unreadCount(conv) > 0"
                      class="inline-flex items-center justify-center rounded-full bg-primary text-primary-foreground text-[10px] font-bold min-w-[16px] h-4 px-1"
                    >{{ unreadCount(conv) }}</span>
                    <time
                      :datetime="conv.lastMessageAt"
                      class="text-xs text-muted-foreground"
                    >
                      {{ formatRelativeTime(conv.lastMessageAt) }}
                    </time>
                  </div>
                </div>
                <p v-if="conv.messages.length > 0" class="text-xs text-muted-foreground truncate mt-0.5">
                  <span class="font-medium">{{ conv.messages[conv.messages.length - 1]!.sender }}:</span>
                  {{ stripMarkdown(conv.messages[conv.messages.length - 1]!.message) }}
                </p>
                <p class="text-xs text-muted-foreground/70 mt-0.5">
                  {{ conv.messages.length }}
                  {{ conv.messages.length === 1 ? 'message' : 'messages' }}
                </p>
              </div>
            </button>
            </div>
          </div>
          <!-- Agent conversations group -->
          <div v-if="agentConversations.length > 0">
            <div class="px-3 pb-1 border-t border-border/50 mt-1" :class="bossConversations.length > 0 ? 'pt-3' : 'pt-2'">
              <span class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">Agent Conversations</span>
            </div>
            <div v-for="conv in agentConversations" :key="conv.key" role="option" :aria-selected="selectedKey === conv.key">
              <button
                class="w-full text-left px-3 py-2.5 hover:bg-muted/60 transition-colors flex items-start gap-2.5 min-w-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
                :class="{ 'bg-muted': selectedKey === conv.key }"
                @click="selectedKey = conv.key"
              >
                <div class="relative shrink-0 w-9 h-9 mt-0.5" @click.stop>
                  <AgentProfileCard
                    :agent-name="conv.participants[0]"
                    :agent="space.agents[conv.participants[0]]"
                    :space-name="space.name"
                    @select-agent="goToAgentDetail($event)"
                  >
                    <button
                      class="absolute top-0 left-0 rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      :aria-label="`View ${conv.participants[0]} details`"
                      @click="openSlideover(conv.participants[0])"
                    >
                      <AgentAvatar :name="conv.participants[0]" :size="26" />
                    </button>
                  </AgentProfileCard>
                  <AgentProfileCard
                    :agent-name="conv.participants[1]"
                    :agent="space.agents[conv.participants[1]]"
                    :space-name="space.name"
                    @select-agent="goToAgentDetail($event)"
                  >
                    <button
                      class="absolute bottom-0 right-0 rounded-full ring-2 ring-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      :aria-label="`View ${conv.participants[1]} details`"
                      @click="openSlideover(conv.participants[1])"
                    >
                      <AgentAvatar :name="conv.participants[1]" :size="22" />
                    </button>
                  </AgentProfileCard>
                </div>
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-1 justify-between">
                    <span class="text-sm font-medium truncate">
                      {{ convTitle(conv) }}
                    </span>
                    <time :datetime="conv.lastMessageAt" class="text-xs text-muted-foreground shrink-0">
                      {{ formatRelativeTime(conv.lastMessageAt) }}
                    </time>
                  </div>
                  <p v-if="conv.messages.length > 0" class="text-xs text-muted-foreground truncate mt-0.5">
                    <span class="font-medium">{{ conv.messages[conv.messages.length - 1]!.sender }}:</span>
                    {{ stripMarkdown(conv.messages[conv.messages.length - 1]!.message) }}
                  </p>
                  <p class="text-xs text-muted-foreground/70 mt-0.5">
                    {{ conv.messages.length }}
                    {{ conv.messages.length === 1 ? 'message' : 'messages' }}
                  </p>
                </div>
              </button>
            </div>
          </div>
        </div>
      </ScrollArea>
    </aside>

    <!-- Right panel: thread view + task widget -->
    <div class="flex-1 flex flex-row min-h-0 min-w-0">
      <!-- Thread column -->
      <div class="flex-1 flex flex-col min-h-0 min-w-0 relative">
      <template v-if="selectedConversation">
        <!-- Thread header -->
        <div class="flex items-center gap-3 px-4 py-3 border-b shrink-0">
          <div class="relative w-9 h-9 shrink-0" aria-hidden="true">
            <AgentAvatar :name="selectedConversation.participants[0]" :size="26" class="absolute top-0 left-0" />
            <AgentAvatar
              :name="selectedConversation.participants[1]"
              :size="22"
              class="absolute bottom-0 right-0 ring-2 ring-background rounded-full"
            />
          </div>
          <div>
            <h2 class="text-sm font-semibold">
              {{ convTitle(selectedConversation) }}
            </h2>
            <p class="text-xs text-muted-foreground">
              {{ selectedConversation.messages.length }}
              {{ selectedConversation.messages.length === 1 ? 'message' : 'messages' }}
            </p>
          </div>
        </div>

        <!-- Messages -->
        <ScrollArea ref="threadScrollRef" class="flex-1 min-h-0 px-4 py-3">
          <div
            class="flex flex-col"
            role="log"
            aria-label="Conversation thread"
            aria-live="polite"
          >
            <!-- Load earlier button — shown when there are older messages to fetch -->
            <div
              v-if="conversationHasMore && selectedConversation.messages.length > 0"
              class="flex justify-center py-2 mb-2"
            >
              <button
                class="text-xs text-muted-foreground hover:text-foreground flex items-center gap-1.5 px-3 py-1 rounded-full border border-border/50 hover:border-border transition-colors disabled:opacity-50"
                :disabled="loadingEarlier"
                @click="loadEarlierMessages"
              >
                <Loader2 v-if="loadingEarlier" class="size-3 animate-spin" aria-hidden="true" />
                {{ loadingEarlier ? 'Loading…' : 'Load earlier messages' }}
              </button>
            </div>

            <!-- Empty thread state -->
            <div
              v-if="selectedConversation.messages.length === 0"
              class="flex flex-col items-center justify-center py-16 text-center text-muted-foreground gap-2"
              role="status"
            >
              <MessageSquare class="size-8 opacity-30" aria-hidden="true" />
              <p class="text-sm font-medium text-foreground">No messages yet</p>
              <p v-if="composeRecipient" class="text-xs">
                Say hello to {{ composeRecipient }} using the compose box below.
              </p>
            </div>

            <template
              v-for="(msg, i) in selectedConversation.messages"
              :key="msg.id"
            >
              <!-- Day separator -->
              <div
                v-if="i === 0 || getDateKey(msg.timestamp) !== getDateKey(selectedConversation.messages[i - 1]!.timestamp)"
                class="flex items-center gap-3 my-4"
                aria-hidden="true"
              >
                <div class="flex-1 h-px bg-border" />
                <span class="text-xs text-muted-foreground px-1 shrink-0">
                  {{ formatDaySeparator(msg.timestamp) }}
                </span>
                <div class="flex-1 h-px bg-border" />
              </div>

              <!-- Message row -->
              <div
                class="flex items-start gap-2.5 mt-3 rounded-sm transition-colors"
                :class="msg.recipient === 'boss' && !msg.read ? 'bg-primary/5 -mx-2 px-2' : ''"
                role="article"
                :aria-label="`Message from ${msg.sender} to ${msg.recipient}`"
              >
                <AgentProfileCard
                  :agent-name="msg.sender"
                  :agent="space.agents[msg.sender]"
                  :space-name="space.name"
                  @select-agent="goToAgentDetail($event)"
                >
                  <button
                    class="shrink-0 mt-0.5 rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    :aria-label="`View ${msg.sender} details`"
                    @click="openSlideover(msg.sender)"
                  >
                    <AgentAvatar :name="msg.sender" :size="28" />
                  </button>
                </AgentProfileCard>
                <div class="flex-1 min-w-0">
                  <div class="flex items-baseline gap-1.5 mb-1">
                    <AgentProfileCard
                      :agent-name="msg.sender"
                      :agent="space.agents[msg.sender]"
                      :space-name="space.name"
                      @select-agent="goToAgentDetail($event)"
                    >
                      <button
                        class="text-xs font-semibold hover:text-primary transition-colors hover:underline cursor-pointer"
                        :aria-label="`View ${msg.sender} details`"
                        @click="openSlideover(msg.sender)"
                      >{{ msg.sender }}</button>
                    </AgentProfileCard>
                    <span class="text-xs text-muted-foreground">→
                      <AgentProfileCard
                        :agent-name="msg.recipient"
                        :agent="space.agents[msg.recipient]"
                        :space-name="space.name"
                        @select-agent="goToAgentDetail($event)"
                      >
                        <button
                          class="hover:text-foreground transition-colors hover:underline cursor-pointer"
                          :aria-label="`View ${msg.recipient} details`"
                          @click="openSlideover(msg.recipient)"
                        >{{ msg.recipient }}</button>
                      </AgentProfileCard>
                    </span>
                    <Tooltip>
                      <TooltipTrigger as-child>
                        <time
                          :datetime="msg.timestamp"
                          class="text-xs text-muted-foreground ml-auto cursor-default"
                        >
                          {{ relativeTime(msg.timestamp) }}
                        </time>
                      </TooltipTrigger>
                      <TooltipContent>{{ formatFullDate(msg.timestamp) }}</TooltipContent>
                    </Tooltip>
                  </div>
                  <!-- Priority badge -->
                  <div v-if="msg.priority && msg.priority !== 'info'" class="mb-1">
                    <span
                      v-if="msg.priority === 'urgent'"
                      class="text-[9px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded bg-red-500/15 text-red-500 border border-red-500/30"
                    >urgent</span>
                    <span
                      v-else-if="msg.priority === 'directive'"
                      class="text-[9px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded bg-yellow-500/15 text-yellow-600 border border-yellow-500/30 dark:text-yellow-400"
                    >directive</span>
                  </div>
                  <!-- Decision message card -->
                  <div
                    v-if="msg.type === 'decision'"
                    class="border rounded-lg overflow-hidden"
                    :class="msg.resolved ? 'border-muted bg-muted/30' : 'border-amber-500/40 bg-amber-500/5'"
                  >
                    <div class="flex items-center gap-2 px-3 py-1.5 border-b" :class="msg.resolved ? 'border-muted' : 'border-amber-500/20 bg-amber-500/10'">
                      <HelpCircle class="size-3.5" :class="msg.resolved ? 'text-muted-foreground' : 'text-amber-500'" />
                      <span class="text-[10px] font-semibold uppercase tracking-wider" :class="msg.resolved ? 'text-muted-foreground' : 'text-amber-500'">
                        {{ msg.resolved ? 'Decision — Resolved' : 'Decision Requested' }}
                      </span>
                      <CheckCircle2 v-if="msg.resolved" class="size-3.5 text-success ml-auto" />
                    </div>
                    <div class="px-3 py-2">
                      <div class="text-sm break-words leading-relaxed md-content" v-html="renderMarkdown(linkTaskRefs(msg.message, space.name))" />
                      <!-- Resolution text -->
                      <div v-if="msg.resolved && msg.resolution" class="mt-2 pt-2 border-t border-muted">
                        <p class="text-[10px] text-muted-foreground uppercase tracking-wider mb-1">Reply</p>
                        <p class="text-sm text-muted-foreground">{{ msg.resolution }}</p>
                      </div>
                      <!-- Reply form (only for unresolved, and only when boss can interact) -->
                      <div v-if="!msg.resolved" class="mt-2 pt-2 border-t border-amber-500/20">
                        <textarea
                          v-model="decisionReplyTexts[msg.id]"
                          class="w-full text-sm bg-background border rounded-md px-2.5 py-1.5 min-h-[48px] resize-y focus:outline-none focus:ring-1 focus:ring-ring placeholder:text-muted-foreground"
                          placeholder="Type your decision... (Enter to send)"
                          @keydown="handleDecisionKeydown($event, msg.id, msg.sender)"
                        />
                        <div class="flex items-center gap-2 mt-1.5">
                          <Button
                            size="sm"
                            class="h-7 text-xs gap-1"
                            :disabled="decisionReplying[msg.id] || !(decisionReplyTexts[msg.id] ?? '').trim()"
                            @click="replyToDecision(msg.id, msg.sender)"
                          >
                            <Loader2 v-if="decisionReplying[msg.id]" class="size-3 animate-spin" />
                            <SendHorizontal v-else class="size-3" />
                            Reply
                          </Button>
                          <span v-if="decisionFeedback[msg.id]" class="text-xs" :class="decisionFeedback[msg.id]?.ok ? 'text-success' : 'text-destructive'">
                            {{ decisionFeedback[msg.id]?.msg }}
                          </span>
                        </div>
                      </div>
                    </div>
                  </div>
                  <!-- Regular message bubble -->
                  <div
                    v-else
                    class="bg-muted rounded-lg px-3 py-2 text-sm break-words leading-relaxed md-content"
                    v-html="renderMarkdown(linkTaskRefs(msg.message, space.name))"
                  />
                  <!-- Read receipt — always shown for boss-sent messages -->
                  <div v-if="msg.sender === 'boss'" class="flex items-center gap-1 mt-1">
                    <span
                      v-if="msg.read"
                      class="flex items-center gap-0 text-xs font-medium text-primary"
                      title="Read by agent"
                    >
                      <Check class="size-3.5 -mr-1" />
                      <Check class="size-3.5" />
                      <span class="ml-1">Read</span>
                    </span>
                    <span
                      v-else
                      class="flex items-center gap-0.5 text-xs text-muted-foreground"
                      title="Delivered to agent"
                    >
                      <Check class="size-3.5" />
                      <span>Delivered</span>
                    </span>
                  </div>
                </div>
              </div>
            </template>
          </div>
        </ScrollArea>

        <!-- Jump-to-bottom button (H14) -->
        <Transition name="fade">
          <button
            v-if="!isAtBottom"
            class="absolute bottom-20 right-4 z-10 flex items-center gap-1 rounded-full border bg-background px-3 py-1 text-xs text-muted-foreground shadow-md hover:text-foreground transition-colors"
            aria-label="Jump to latest messages"
            @click="scrollThreadToBottom"
          >
            <ChevronDown class="size-3" />
            Latest
          </button>
        </Transition>

        <!-- Note shown when boss is not a participant (agent-to-agent thread) -->
        <div v-if="selectedConversation && !composeRecipient" class="border-t p-3 shrink-0">
          <p class="text-xs text-muted-foreground text-center italic">Compose is only available in boss ↔ agent threads</p>
        </div>

        <!-- Inline compose box — only when boss is a participant -->
        <div v-if="composeRecipient" class="border-t p-3 shrink-0">
          <form class="flex items-end gap-2" @submit.prevent="sendInlineCompose">
            <Textarea
              :ref="(el) => { composeRef = el as HTMLTextAreaElement | null }"
              v-model="inlineMessage"
              :placeholder="`Message ${composeRecipient}… (Enter to send, Shift+Enter for newline)`"
              class="flex-1 min-h-[38px] max-h-40 resize-none text-sm overflow-y-auto"
              :disabled="inlineSending"
              @input="autoResizeTextarea"
              @keydown="handleComposeKeydown"
            />
            <Button
              type="submit"
              size="sm"
              class="shrink-0 h-9"
              :disabled="!inlineMessage.trim() || inlineSending"
              aria-label="Send message"
            >
              <SendHorizontal class="size-4" />
            </Button>
          </form>
          <p v-if="inlineSendError" class="mt-1.5 text-xs text-destructive">{{ inlineSendError }}</p>
        </div>
      </template>

      <!-- No conversation selected -->
      <div
        v-else
        class="flex-1 flex flex-col items-center justify-center text-muted-foreground text-center gap-3"
        role="status"
      >
        <div class="rounded-full bg-muted p-4" aria-hidden="true">
          <MessageSquare class="size-8" />
        </div>
        <div>
          <p class="text-sm font-medium text-foreground">Select a conversation</p>
          <p class="text-xs mt-0.5">Choose a conversation from the list to view its thread</p>
        </div>
      </div>
      </div><!-- end thread column -->

      <!-- Task widget panel -->
      <aside
        v-if="composeRecipient"
        class="w-60 shrink-0 border-l flex flex-col min-h-0"
        aria-label="Agent tasks"
      >
        <div class="flex items-center justify-between px-3 py-2 border-b shrink-0">
          <span class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Tasks</span>
          <div class="flex items-center gap-1">
            <Tooltip>
              <TooltipTrigger as-child>
                <button
                  class="text-xs text-muted-foreground hover:text-primary transition-colors p-0.5"
                  aria-label="Add task"
                  @click="newTaskDialogOpen = true"
                >
                  <Plus class="size-3.5" />
                </button>
              </TooltipTrigger>
              <TooltipContent>Add task for {{ composeRecipient }}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <button
                  class="text-xs text-muted-foreground hover:text-foreground transition-colors ml-1 flex items-center"
                  :aria-label="showTaskPanel ? 'Collapse tasks' : 'Expand tasks'"
                  @click="showTaskPanel = !showTaskPanel"
                >
                  <ChevronUp v-if="showTaskPanel" class="size-3.5" />
                  <ChevronDown v-else class="size-3.5" />
                </button>
              </TooltipTrigger>
              <TooltipContent>{{ showTaskPanel ? 'Collapse' : 'Expand' }} tasks</TooltipContent>
            </Tooltip>
          </div>
        </div>
        <ScrollArea v-if="showTaskPanel" class="flex-1 min-h-0">
          <div v-if="tasksLoading" class="px-3 py-4 text-xs text-muted-foreground text-center">Loading…</div>
          <div v-else-if="agentTasks.length === 0" class="px-3 py-4 text-xs text-muted-foreground text-center">No tasks assigned</div>
          <ul v-else class="py-1">
            <li v-for="task in agentTasks" :key="task.id">
              <a
                :href="`/${encodeURIComponent(space.name)}/kanban#${task.id}`"
                class="flex items-start gap-2 px-3 py-2 hover:bg-muted/60 transition-colors text-xs"
              >
                <span class="font-mono text-muted-foreground shrink-0 mt-0.5">{{ task.id }}</span>
                <span class="flex-1 min-w-0 leading-snug line-clamp-2">{{ task.title }}</span>
                <span
                  class="shrink-0 rounded px-1 py-0.5 text-[10px] font-medium"
                  :class="{
                    'bg-blue-500/10 text-blue-600 dark:text-blue-400': task.status === 'in_progress',
                    'bg-yellow-500/10 text-yellow-600 dark:text-yellow-400': task.status === 'review',
                    'bg-red-500/10 text-red-600 dark:text-red-400': task.status === 'blocked',
                    'bg-green-500/10 text-green-600 dark:text-green-400': task.status === 'done',
                    'bg-muted text-muted-foreground': task.status === 'backlog',
                  }"
                >{{ task.status }}</span>
              </a>
            </li>
          </ul>
        </ScrollArea>
      </aside>
    </div>

    <!-- New Task dialog (pre-filled with conversation partner) -->
    <NewTaskDialog
      v-if="composeRecipient"
      v-model:open="newTaskDialogOpen"
      :space="space"
      :initial-assignee="composeRecipient"
      @created="agentTasks = []"
    />

    <!-- Agent detail slideover -->
    <Transition
      enter-active-class="transition-transform duration-200 ease-out"
      enter-from-class="translate-x-full"
      enter-to-class="translate-x-0"
      leave-active-class="transition-transform duration-150 ease-in"
      leave-from-class="translate-x-0"
      leave-to-class="translate-x-full"
    >
      <aside
        v-if="slideoverAgentName && slideoverAgent"
        class="absolute inset-y-0 right-0 w-80 border-l bg-background shadow-lg flex flex-col z-20"
        aria-label="Agent details"
        role="complementary"
      >
        <!-- Slideover header -->
        <div class="flex items-center gap-3 px-4 py-3 border-b shrink-0">
          <AgentAvatar :name="slideoverAgentName" :size="32" />
          <div class="flex-1 min-w-0">
            <h3 class="text-sm font-semibold truncate">{{ slideoverAgentName }}</h3>
            <StatusBadge :status="slideoverAgent.status" />
          </div>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Open full agent detail page"
                @click="goToAgentDetail(slideoverAgentName)"
              >
                <ExternalLink class="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Open full detail page</TooltipContent>
          </Tooltip>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Close agent details"
            @click="closeSlideover"
          >
            <X class="size-4" />
          </Button>
        </div>

        <!-- Slideover content -->
        <ScrollArea class="flex-1 min-h-0">
          <div class="px-4 py-3 space-y-4 text-sm">
            <!-- Summary -->
            <div>
              <p class="text-xs text-muted-foreground uppercase tracking-wide font-medium mb-1">Summary</p>
              <p class="leading-relaxed">{{ slideoverAgent.summary }}</p>
            </div>

            <!-- Meta: branch, PR, phase, updated -->
            <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
              <span v-if="slideoverAgent.phase">Phase: {{ slideoverAgent.phase }}</span>
              <span v-if="slideoverAgent.branch" class="flex items-center gap-1 font-mono">
                <GitBranch class="size-3" />{{ slideoverAgent.branch }}
              </span>
              <a
                v-if="slideoverAgent.pr && prLink(slideoverAgent)"
                :href="prLink(slideoverAgent)!"
                target="_blank"
                rel="noopener"
                class="text-primary hover:underline font-mono"
              >{{ slideoverAgent.pr }}</a>
              <span>Updated {{ relativeTime(slideoverAgent.updated_at) }}</span>
            </div>

            <!-- Items -->
            <div v-if="slideoverAgent.items && slideoverAgent.items.length > 0">
              <p class="text-xs text-muted-foreground uppercase tracking-wide font-medium mb-1">Activity</p>
              <ul class="space-y-1">
                <li
                  v-for="(item, i) in slideoverAgent.items"
                  :key="i"
                  class="flex gap-2 text-xs leading-relaxed"
                >
                  <span class="text-muted-foreground mt-0.5 shrink-0">•</span>
                  <span v-html="renderMarkdown(item)" class="md-content" />
                </li>
              </ul>
            </div>

            <!-- Next steps -->
            <div v-if="slideoverAgent.next_steps">
              <p class="text-xs text-muted-foreground uppercase tracking-wide font-medium mb-1">Next steps</p>
              <p class="text-xs leading-relaxed text-muted-foreground">{{ slideoverAgent.next_steps }}</p>
            </div>

            <!-- Questions -->
            <div v-if="slideoverAgent.questions && slideoverAgent.questions.length > 0">
              <p class="text-xs text-muted-foreground uppercase tracking-wide font-medium mb-1">Questions</p>
              <ul class="space-y-1">
                <li
                  v-for="(q, i) in slideoverAgent.questions"
                  :key="i"
                  class="text-xs text-yellow-600 dark:text-yellow-400 leading-relaxed"
                >• {{ q }}</li>
              </ul>
            </div>

            <!-- Blockers -->
            <div v-if="slideoverAgent.blockers && slideoverAgent.blockers.length > 0">
              <p class="text-xs text-muted-foreground uppercase tracking-wide font-medium mb-1">Blockers</p>
              <ul class="space-y-1">
                <li
                  v-for="(b, i) in slideoverAgent.blockers"
                  :key="i"
                  class="text-xs text-destructive leading-relaxed"
                >🔴 {{ b }}</li>
              </ul>
            </div>
          </div>
        </ScrollArea>
      </aside>
    </Transition>
  </div>
</template>

<style scoped>
/* Jump-to-bottom fade */
.fade-enter-active, .fade-leave-active { transition: opacity 0.15s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
