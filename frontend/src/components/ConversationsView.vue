<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import type { KnowledgeSpace, AgentUpdate } from '@/types'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import AgentAvatar from './AgentAvatar.vue'
import AgentProfileCard from './AgentProfileCard.vue'
import StatusBadge from './StatusBadge.vue'
import { MessageSquare, Search, X, GitBranch, ExternalLink, Pencil, SendHorizontal } from 'lucide-vue-next'
import { renderMarkdown } from '@/lib/markdown'
import { relativeTime } from '@/composables/useTime'
import api from '@/api/client'

const props = defineProps<{
  space: KnowledgeSpace
}>()

interface ConversationMessage {
  id: string
  message: string
  sender: string
  recipient: string
  timestamp: string
}

interface Conversation {
  key: string
  participants: [string, string]
  messages: ConversationMessage[]
  lastMessageAt: string
}

// Reconstruct pairwise conversation threads from all agents' message inboxes.
// When B sends to A, the message lands in A's inbox with sender=B.
// A conversation between A and B is: A's inbox msgs where sender=B + B's inbox msgs where sender=A.
const conversations = computed((): Conversation[] => {
  const convMap = new Map<string, Conversation>()

  for (const [agentName, agentData] of Object.entries(props.space.agents)) {
    for (const msg of agentData.messages ?? []) {
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
    conv.participants.some(p => p.toLowerCase().includes(q)),
  )
})

const selectedConversation = computed(() =>
  conversations.value.find(c => c.key === selectedKey.value) ?? null,
)

// Auto-select first conversation on load
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

function prLink(agent: { pr?: string; repo_url?: string }): string | null {
  if (!agent.pr) return null
  if (agent.pr.startsWith('http')) return agent.pr
  if (!agent.repo_url) return null
  const repoBase = agent.repo_url.replace(/\.git$/, '').replace(/\/$/, '')
  const prNum = agent.pr.replace(/^#/, '')
  return `${repoBase}/pull/${prNum}`
}

// ── Compose new message ─────────────────────────────────────────────
const composeOpen = ref(false)
const composeAgent = ref('')
const composeMessage = ref('')
const composeSending = ref(false)
const composeSelectRef = ref<HTMLSelectElement | null>(null)

const allAgentNames = computed(() => Object.keys(props.space.agents).sort())

function openCompose(preselect?: string) {
  composeAgent.value = preselect ?? allAgentNames.value[0] ?? ''
  composeMessage.value = ''
  composeOpen.value = true
  nextTick(() => composeSelectRef.value?.focus())
}

async function sendCompose() {
  const text = composeMessage.value.trim()
  if (!text || !composeAgent.value) return
  composeSending.value = true
  try {
    await api.sendMessage(props.space.name, composeAgent.value, text, 'boss')
    composeOpen.value = false
    composeMessage.value = ''
    // Select the new/existing conversation
    const sorted = [composeAgent.value, 'boss'].sort()
    selectedKey.value = sorted.join('\u2194')
  } catch (_) {
    // message still delivered; close anyway
    composeOpen.value = false
  } finally {
    composeSending.value = false
  }
}
</script>

<template>
  <div class="flex h-full min-h-0 relative overflow-hidden">
    <!-- Left panel: conversation list -->
    <aside
      class="w-72 shrink-0 border-r flex flex-col min-h-0"
      aria-label="Conversations"
    >
      <!-- Search + Compose -->
      <div class="p-3 border-b shrink-0 space-y-2">
        <div class="relative">
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
        <Button
          variant="outline"
          size="sm"
          class="w-full h-8 text-xs gap-1.5 justify-start"
          aria-label="Compose new message"
          @click="openCompose()"
        >
          <Pencil class="size-3.5" />
          New Message
        </Button>
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
        </div>

        <ul v-else class="py-1" role="listbox" aria-label="Conversation list">
          <li v-for="conv in filteredConversations" :key="conv.key" role="option" :aria-selected="selectedKey === conv.key">
            <button
              class="w-full text-left px-3 py-2.5 hover:bg-muted/60 transition-colors flex items-start gap-2.5 min-w-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
              :class="{ 'bg-muted': selectedKey === conv.key }"
              @click="selectedKey = conv.key"
            >
              <!-- Stacked avatars (clickable to open agent slideover) -->
              <div class="relative shrink-0 w-9 h-9 mt-0.5" @click.stop>
                <AgentProfileCard
                  :agent-name="conv.participants[0]"
                  :agent="space.agents[conv.participants[0]]"
                  @select-agent="goToAgentDetail($event)"
                  @message-agent="openCompose($event)"
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
                  @select-agent="goToAgentDetail($event)"
                  @message-agent="openCompose($event)"
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
                    {{ conv.participants[0] }} ↔ {{ conv.participants[1] }}
                  </span>
                  <time
                    :datetime="conv.lastMessageAt"
                    class="text-xs text-muted-foreground shrink-0"
                  >
                    {{ formatRelativeTime(conv.lastMessageAt) }}
                  </time>
                </div>
                <p v-if="conv.messages.length > 0" class="text-xs text-muted-foreground truncate mt-0.5">
                  <span class="font-medium">{{ conv.messages[conv.messages.length - 1]!.sender }}:</span>
                  {{ conv.messages[conv.messages.length - 1]!.message }}
                </p>
                <p class="text-xs text-muted-foreground/70 mt-0.5">
                  {{ conv.messages.length }}
                  {{ conv.messages.length === 1 ? 'message' : 'messages' }}
                </p>
              </div>
            </button>
          </li>
        </ul>
      </ScrollArea>
    </aside>

    <!-- Right panel: thread view -->
    <div class="flex-1 flex flex-col min-h-0 min-w-0">
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
              {{ selectedConversation.participants[0] }} ↔ {{ selectedConversation.participants[1] }}
            </h2>
            <p class="text-xs text-muted-foreground">
              {{ selectedConversation.messages.length }}
              {{ selectedConversation.messages.length === 1 ? 'message' : 'messages' }}
            </p>
          </div>
        </div>

        <!-- Messages -->
        <ScrollArea class="flex-1 min-h-0 px-4 py-3">
          <div
            class="flex flex-col"
            role="log"
            aria-label="Conversation thread"
            aria-live="polite"
          >
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
                class="flex items-start gap-2.5 mt-3"
                role="article"
                :aria-label="`Message from ${msg.sender} to ${msg.recipient}`"
              >
                <AgentProfileCard
                  :agent-name="msg.sender"
                  :agent="space.agents[msg.sender]"
                  @select-agent="goToAgentDetail($event)"
                  @message-agent="openCompose($event)"
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
                      @select-agent="goToAgentDetail($event)"
                  @message-agent="openCompose($event)"
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
                        @select-agent="goToAgentDetail($event)"
                  @message-agent="openCompose($event)"
                      >
                        <button
                          class="hover:text-foreground transition-colors hover:underline cursor-pointer"
                          :aria-label="`View ${msg.recipient} details`"
                          @click="openSlideover(msg.recipient)"
                        >{{ msg.recipient }}</button>
                      </AgentProfileCard>
                    </span>
                    <time
                      :datetime="msg.timestamp"
                      class="text-xs text-muted-foreground ml-auto"
                    >
                      {{ new Date(msg.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}
                    </time>
                  </div>
                  <div
                    class="bg-muted rounded-lg px-3 py-2 text-sm break-words leading-relaxed md-content"
                    v-html="renderMarkdown(msg.message)"
                  />
                </div>
              </div>
            </template>
          </div>
        </ScrollArea>
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
    </div>

    <!-- Compose new message dialog -->
    <Dialog v-model:open="composeOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New Message</DialogTitle>
          <DialogDescription>Send a message from boss to an agent.</DialogDescription>
        </DialogHeader>
        <form class="flex flex-col gap-3" @submit.prevent="sendCompose">
          <div class="flex flex-col gap-1">
            <label class="text-xs font-medium text-muted-foreground" for="compose-agent">To</label>
            <select
              id="compose-agent"
              ref="composeSelectRef"
              v-model="composeAgent"
              class="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              <option v-for="name in allAgentNames" :key="name" :value="name">{{ name }}</option>
            </select>
          </div>
          <div class="flex flex-col gap-1">
            <label class="text-xs font-medium text-muted-foreground" for="compose-msg">Message</label>
            <Textarea
              id="compose-msg"
              v-model="composeMessage"
              placeholder="Type your message…"
              :rows="4"
              @keydown.ctrl.enter.prevent="sendCompose"
              @keydown.escape="composeOpen = false"
            />
            <p class="text-xs text-muted-foreground">Ctrl+Enter to send</p>
          </div>
          <Button
            type="submit"
            size="sm"
            class="self-end gap-1.5"
            :disabled="!composeMessage.trim() || !composeAgent || composeSending"
          >
            <SendHorizontal class="size-3.5" />
            {{ composeSending ? 'Sending…' : 'Send' }}
          </Button>
        </form>
      </DialogContent>
    </Dialog>

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
