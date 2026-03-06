<script setup lang="ts">
import type { AgentMessage } from '@/types'
import { ref, nextTick, watch, computed } from 'vue'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { SendHorizontal } from 'lucide-vue-next'

const props = defineProps<{
  messages: AgentMessage[]
  agentName: string
}>()

const emit = defineEmits<{
  'send-message': [text: string]
}>()

const messageText = ref('')
const scrollRef = ref<InstanceType<typeof ScrollArea> | null>(null)

// Sender color palette — subtle background tints
const senderColors: Record<string, string> = {}
const colorPalette = [
  'bg-blue-500/10 border-blue-500/20',
  'bg-purple-500/10 border-purple-500/20',
  'bg-amber-500/10 border-amber-500/20',
  'bg-emerald-500/10 border-emerald-500/20',
  'bg-pink-500/10 border-pink-500/20',
  'bg-cyan-500/10 border-cyan-500/20',
  'bg-rose-500/10 border-rose-500/20',
  'bg-indigo-500/10 border-indigo-500/20',
]

function getSenderColor(sender: string): string {
  if (!(sender in senderColors)) {
    const idx = Object.keys(senderColors).length % colorPalette.length
    senderColors[sender] = colorPalette[idx]!
  }
  return senderColors[sender]!
}

function getSenderInitial(sender: string): string {
  return sender.charAt(0).toUpperCase()
}

function formatTime(timestamp: string): string {
  const d = new Date(timestamp)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function formatFullDate(timestamp: string): string {
  return new Date(timestamp).toLocaleString()
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

// Auto-scroll when new messages arrive
watch(
  () => props.messages.length,
  async () => {
    await nextTick()
    const el = scrollRef.value?.$el?.querySelector('[data-radix-scroll-area-viewport]')
    if (el) {
      el.scrollTop = el.scrollHeight
    }
  },
)

const sortedMessages = computed(() => {
  return [...props.messages].sort(
    (a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
  )
})
</script>

<template>
  <div class="flex flex-col h-full" role="log" aria-label="Message history">
    <!-- Messages area -->
    <ScrollArea ref="scrollRef" class="flex-1 px-4 py-3">
      <div v-if="sortedMessages.length === 0" class="flex flex-col items-center justify-center h-32 text-muted-foreground font-text text-sm text-center gap-1">
        <p>No messages yet</p>
        <p class="text-xs">Send a message below to communicate with this agent</p>
      </div>
      <div v-else class="space-y-3" aria-live="polite">
        <div
          v-for="msg in sortedMessages"
          :key="msg.id"
          :class="[
            'rounded-lg border px-3 py-2 max-w-[85%]',
            getSenderColor(msg.sender),
          ]"
          role="article"
          :aria-label="`Message from ${msg.sender} at ${formatTime(msg.timestamp)}`"
        >
          <div class="flex items-center gap-2 mb-1">
            <span
              class="flex items-center justify-center size-5 rounded-full bg-muted text-[10px] font-semibold text-muted-foreground"
              :aria-label="msg.sender"
              role="img"
            >
              {{ getSenderInitial(msg.sender) }}
            </span>
            <span class="text-xs font-medium">{{ msg.sender }}</span>
            <Tooltip>
              <TooltipTrigger as-child>
                <time
                  :datetime="msg.timestamp"
                  class="text-xs text-muted-foreground ml-auto cursor-default"
                >
                  {{ formatTime(msg.timestamp) }}
                </time>
              </TooltipTrigger>
              <TooltipContent>
                {{ formatFullDate(msg.timestamp) }}
              </TooltipContent>
            </Tooltip>
          </div>
          <p class="text-sm font-text leading-relaxed whitespace-pre-wrap">{{ msg.message }}</p>
        </div>
      </div>
    </ScrollArea>

    <!-- Input area -->
    <div class="border-t p-3 flex gap-2">
      <label for="message-input" class="sr-only">Send a message to {{ agentName }}</label>
      <Input
        id="message-input"
        v-model="messageText"
        :placeholder="`Message ${agentName}...`"
        class="flex-1 font-text"
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
