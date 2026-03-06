<script setup lang="ts">
import type { AgentMessage } from '@/types'
import { ref, nextTick, watch, computed, onMounted, onUnmounted } from 'vue'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { SendHorizontal, MessageCircle, Check, ChevronDown } from 'lucide-vue-next'
import AgentAvatar from './AgentAvatar.vue'
import { formatFullDate } from '@/composables/useTime'
import { renderMarkdown } from '@/lib/markdown'

const props = defineProps<{
  messages: AgentMessage[]
  agentName: string
}>()

const emit = defineEmits<{
  'send-message': [text: string]
}>()

const messageText = ref('')
const scrollRef = ref<InstanceType<typeof ScrollArea> | null>(null)
const isAtBottom = ref(true)
const newMessageCount = ref(0)

function formatTime(timestamp: string): string {
  const d = new Date(timestamp)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function formatDay(timestamp: string): string {
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

function send() {
  const text = messageText.value.trim()
  if (!text) return
  emit('send-message', text)
  messageText.value = ''
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

function getScrollEl(): HTMLElement | null {
  return scrollRef.value?.$el?.querySelector('[data-radix-scroll-area-viewport]') ?? null
}

function checkAtBottom() {
  const el = getScrollEl()
  if (!el) return
  isAtBottom.value = el.scrollTop + el.clientHeight >= el.scrollHeight - 32
  if (isAtBottom.value) newMessageCount.value = 0
}

function scrollToBottom() {
  nextTick(() => {
    const el = getScrollEl()
    if (el) {
      el.scrollTop = el.scrollHeight
      isAtBottom.value = true
      newMessageCount.value = 0
    }
  })
}

onMounted(() => {
  nextTick().then(() => {
    scrollToBottom()
    const el = getScrollEl()
    if (el) el.addEventListener('scroll', checkAtBottom, { passive: true })
  })
})

onUnmounted(() => {
  const el = getScrollEl()
  if (el) el.removeEventListener('scroll', checkAtBottom)
})

watch(() => props.messages.length, (newLen, oldLen) => {
  if (isAtBottom.value) {
    scrollToBottom()
  } else {
    newMessageCount.value += newLen - oldLen
  }
})

// Scroll to bottom when switching to a different agent
watch(() => props.agentName, () => {
  newMessageCount.value = 0
  scrollToBottom()
})

type MessageEntry =
  | {
      type: 'message'
      msg: AgentMessage
      isBoss: boolean
      isFirstInGroup: boolean
      isLastInGroup: boolean
    }
  | { type: 'day-separator'; label: string; key: string }

const enrichedMessages = computed((): MessageEntry[] => {
  const sorted = [...props.messages].sort(
    (a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
  )

  const result: MessageEntry[] = []
  let lastDateKey = ''

  for (let i = 0; i < sorted.length; i++) {
    const msg = sorted[i]!
    const dateKey = getDateKey(msg.timestamp)

    if (dateKey !== lastDateKey) {
      result.push({ type: 'day-separator', label: formatDay(msg.timestamp), key: dateKey })
      lastDateKey = dateKey
    }

    const isBoss = msg.sender === 'boss'
    const prevMsg = sorted[i - 1]
    const nextMsg = sorted[i + 1]
    const prevSame =
      prevMsg && prevMsg.sender === msg.sender && getDateKey(prevMsg.timestamp) === dateKey
    const nextSame =
      nextMsg && nextMsg.sender === msg.sender && getDateKey(nextMsg.timestamp) === dateKey

    result.push({
      type: 'message',
      msg,
      isBoss,
      isFirstInGroup: !prevSame,
      isLastInGroup: !nextSame,
    })
  }

  return result
})
</script>

<template>
  <div class="flex flex-col h-full min-h-0" role="log" aria-label="Message history">
    <!-- Messages area -->
    <ScrollArea ref="scrollRef" class="flex-1 min-h-0 px-4 py-3">
      <!-- Empty state -->
      <div
        v-if="enrichedMessages.length === 0"
        class="flex flex-col items-center justify-center h-40 text-muted-foreground text-center gap-3"
      >
        <div class="rounded-full bg-muted p-3">
          <MessageCircle class="size-6" />
        </div>
        <div>
          <p class="text-sm font-medium text-foreground">No messages yet</p>
          <p class="text-xs mt-0.5">Start the conversation below</p>
        </div>
      </div>

      <div v-else class="flex flex-col gap-0.5" aria-live="polite">
        <template
          v-for="entry in enrichedMessages"
          :key="entry.type === 'day-separator' ? entry.key : entry.msg.id"
        >
          <!-- Day separator -->
          <div v-if="entry.type === 'day-separator'" class="flex items-center gap-3 my-4">
            <div class="flex-1 h-px bg-border" />
            <span class="text-xs text-muted-foreground px-1 shrink-0">{{ entry.label }}</span>
            <div class="flex-1 h-px bg-border" />
          </div>

          <!-- Message row -->
          <div
            v-else
            :class="['flex items-end gap-2', entry.isBoss ? 'flex-row-reverse' : 'flex-row', entry.isFirstInGroup ? 'mt-3' : 'mt-0.5']"
            role="article"
            :aria-label="`Message from ${entry.msg.sender} at ${formatTime(entry.msg.timestamp)}`"
          >
            <!-- Avatar spacer / avatar (agent side only) -->
            <div class="flex-shrink-0 w-7">
              <AgentAvatar
                v-if="!entry.isBoss && entry.isLastInGroup"
                :name="entry.msg.sender"
                :size="28"
              />
            </div>

            <!-- Bubble + meta -->
            <div
              :class="[
                'flex flex-col max-w-[72%]',
                entry.isBoss ? 'items-end' : 'items-start',
              ]"
            >
              <!-- Sender name — first in group, agent side only -->
              <span
                v-if="entry.isFirstInGroup && !entry.isBoss"
                class="text-xs text-muted-foreground mb-1 ml-1"
              >
                {{ entry.msg.sender }}
              </span>

              <!-- Bubble -->
              <Tooltip>
                <TooltipTrigger as-child>
                  <div
                    :class="[
                      'px-3 py-2 text-sm font-text leading-relaxed break-words md-content',
                      entry.isBoss
                        ? 'bg-primary text-primary-foreground'
                        : 'bg-muted text-foreground',
                      // Rounded corners — pinched corner toward the avatar
                      entry.isBoss
                        ? (entry.isFirstInGroup && entry.isLastInGroup
                            ? 'rounded-2xl rounded-br-md'
                            : entry.isFirstInGroup
                              ? 'rounded-2xl rounded-br-md'
                              : entry.isLastInGroup
                                ? 'rounded-2xl rounded-tr-md'
                                : 'rounded-xl rounded-r-md')
                        : (entry.isFirstInGroup && entry.isLastInGroup
                            ? 'rounded-2xl rounded-bl-md'
                            : entry.isFirstInGroup
                              ? 'rounded-2xl rounded-bl-md'
                              : entry.isLastInGroup
                                ? 'rounded-2xl rounded-tl-md'
                                : 'rounded-xl rounded-l-md'),
                    ]"
                    v-html="renderMarkdown(entry.msg.message)"
                  />
                </TooltipTrigger>
                <TooltipContent>
                  {{ formatFullDate(entry.msg.timestamp) }}
                </TooltipContent>
              </Tooltip>

              <!-- Timestamp + delivered (last message in group only) -->
              <div
                v-if="entry.isLastInGroup"
                :class="[
                  'flex items-center gap-1 mt-1 px-1',
                  entry.isBoss ? 'flex-row' : 'flex-row',
                ]"
              >
                <time :datetime="entry.msg.timestamp" class="text-xs text-muted-foreground">
                  {{ formatTime(entry.msg.timestamp) }}
                </time>
                <span
                  v-if="entry.isBoss"
                  class="flex items-center gap-0.5 text-xs text-muted-foreground"
                >
                  <Check class="size-3" />Delivered
                </span>
              </div>
            </div>
          </div>
        </template>
      </div>
    </ScrollArea>

    <!-- New messages indicator -->
    <div v-if="newMessageCount > 0 && !isAtBottom" class="flex justify-center py-1.5 border-t border-border/50 bg-card">
      <Button
        size="sm"
        variant="secondary"
        class="h-7 px-3 text-xs gap-1.5 shadow-sm"
        @click="scrollToBottom"
      >
        <ChevronDown class="size-3.5" />
        {{ newMessageCount }} new message{{ newMessageCount === 1 ? '' : 's' }}
      </Button>
    </div>

    <!-- Input area -->
    <div class="border-t p-3 flex gap-2">
      <label for="message-input" class="sr-only">Send a message to {{ agentName }}</label>
      <Textarea
        id="message-input"
        v-model="messageText"
        :placeholder="`Message ${agentName}… (Enter to send, Shift+Enter for newline)`"
        class="flex-1 font-text min-h-[38px] max-h-[120px] resize-none"
        rows="1"
        @keydown="handleKeydown"
      />
      <Button
        size="sm"
        :disabled="!messageText.trim()"
        :aria-label="`Send message to ${agentName}`"
        @click="send"
      >
        <SendHorizontal class="size-4" /> Send
      </Button>
    </div>
  </div>
</template>
