<script setup lang="ts">
import type { AgentUpdate } from '@/types'
import { ref, computed } from 'vue'
import { useTime } from '@/composables/useTime'
import { useRouter } from 'vue-router'
import AgentAvatar from './AgentAvatar.vue'
import StatusBadge from './StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { GitBranch, ExternalLink, Clock, ArrowUpRight, MessageSquare, Crown } from 'lucide-vue-next'
import { prLink } from '@/lib/utils'

const props = defineProps<{
  agentName: string
  agent?: AgentUpdate | null
  spaceName?: string
}>()

const emit = defineEmits<{
  'select-agent': [name: string]
}>()

const router = useRouter()

const { relativeTime } = useTime()

// Hover state with show/hide delays for UX polish
const visible = ref(false)
const triggerEl = ref<HTMLElement | null>(null)
const cardStyle = ref({ top: '0px', left: '0px' })
let showTimer: ReturnType<typeof setTimeout> | null = null
let hideTimer: ReturnType<typeof setTimeout> | null = null

const CARD_WIDTH = 280

function computePosition() {
  if (!triggerEl.value) return
  const rect = triggerEl.value.getBoundingClientRect()
  const viewportW = window.innerWidth
  const viewportH = window.innerHeight

  // Try placing below the trigger, else above
  let top = rect.bottom + 8
  let left = rect.left

  // Clamp left to viewport
  if (left + CARD_WIDTH > viewportW - 8) {
    left = viewportW - CARD_WIDTH - 8
  }
  if (left < 8) left = 8

  // If card would go off the bottom, flip above
  const estimatedCardH = 320
  if (top + estimatedCardH > viewportH - 8) {
    top = rect.top - estimatedCardH - 8
    if (top < 8) top = 8
  }

  cardStyle.value = { top: `${top}px`, left: `${left}px` }
}

function onMouseEnter(e: MouseEvent) {
  if (hideTimer) { clearTimeout(hideTimer); hideTimer = null }
  triggerEl.value = e.currentTarget as HTMLElement
  showTimer = setTimeout(() => {
    computePosition()
    visible.value = true
  }, 350)
}

function onMouseLeave() {
  if (showTimer) { clearTimeout(showTimer); showTimer = null }
  hideTimer = setTimeout(() => { visible.value = false }, 200)
}

function onCardMouseEnter() {
  if (hideTimer) { clearTimeout(hideTimer); hideTimer = null }
}

function onCardMouseLeave() {
  hideTimer = setTimeout(() => { visible.value = false }, 150)
}

function goToAgent() {
  visible.value = false
  emit('select-agent', props.agentName)
}

function goToConversations() {
  visible.value = false
  if (props.spaceName) {
    router.push({
      name: 'conversation',
      params: { space: props.spaceName, conversationAgent: props.agentName },
    })
  }
}

const summaryText = computed(() => {
  const s = props.agent?.summary ?? ''
  // Strip "AgentName: " prefix pattern
  return s.replace(/^[^:]+:\s*/, '').slice(0, 120)
})
</script>

<template>
  <!-- Trigger wrapper -->
  <span
    class="inline-flex items-center min-w-0 cursor-default"
    @mouseenter="onMouseEnter"
    @mouseleave="onMouseLeave"
  >
    <slot />
  </span>

  <!-- Floating profile card -->
  <Teleport to="body">
    <Transition
      enter-active-class="transition-all duration-150"
      enter-from-class="opacity-0 scale-95 -translate-y-1"
      leave-active-class="transition-all duration-100"
      leave-to-class="opacity-0 scale-95 -translate-y-1"
    >
      <div
        v-if="visible"
        class="fixed z-[9999] rounded-xl border bg-popover text-popover-foreground shadow-xl"
        :style="[cardStyle, { width: CARD_WIDTH + 'px' }]"
        @mouseenter="onCardMouseEnter"
        @mouseleave="onCardMouseLeave"
      >
        <!-- Boss special card -->
        <template v-if="agentName === 'boss'">
          <div class="p-4 pb-3 flex items-start gap-3">
            <div class="shrink-0 mt-0.5 size-10 rounded-full bg-amber-500/15 border border-amber-500/30 flex items-center justify-center">
              <Crown class="size-5 text-amber-500" />
            </div>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-1.5 flex-wrap">
                <span class="font-semibold text-sm leading-tight">boss</span>
                <Badge variant="outline" class="text-[10px] h-4 px-1 border-amber-500/40 text-amber-600 dark:text-amber-400">Human Operator</Badge>
              </div>
              <p class="text-xs text-muted-foreground mt-0.5">Project owner — gives orders, reviews PRs, manages the team</p>
            </div>
          </div>
        </template>

        <!-- Regular agent card -->
        <template v-else>
          <!-- Header -->
          <div class="p-4 pb-3 flex items-start gap-3">
            <AgentAvatar :name="agentName" :size="40" class="shrink-0 mt-0.5" />
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-1.5 flex-wrap">
                <span class="font-semibold text-sm leading-tight truncate">{{ agentName }}</span>
                <StatusBadge v-if="agent" :status="agent.status" />
              </div>
              <div v-if="agent?.role" class="mt-0.5">
                <Badge variant="outline" class="text-[10px] h-4 px-1 border-role/40 text-role">
                  {{ agent.role }}
                </Badge>
              </div>
            </div>
          </div>

          <div v-if="agent" class="px-4 pb-3 space-y-2">
            <!-- Summary -->
            <p v-if="summaryText" class="text-xs text-muted-foreground leading-relaxed line-clamp-3">
              {{ summaryText }}
            </p>

            <!-- Hierarchy: parent -->
            <div v-if="agent.parent" class="flex items-center gap-1 text-[11px] text-muted-foreground">
              <span class="opacity-60">Reports to</span>
              <button
                class="font-medium text-foreground hover:text-primary transition-colors hover:underline"
                @click.stop="emit('select-agent', agent.parent!)"
              >
                {{ agent.parent }}
              </button>
            </div>

            <!-- Meta row: branch + PR -->
            <div class="flex items-center gap-2 flex-wrap">
              <span
                v-if="agent.branch"
                class="inline-flex items-center gap-1 font-mono text-[10px] bg-muted px-1.5 py-0.5 rounded text-muted-foreground"
              >
                <GitBranch class="size-2.5 shrink-0" />
                {{ agent.branch }}
              </span>
              <a
                v-if="agent.pr && prLink(agent)"
                :href="prLink(agent)!"
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex items-center gap-0.5 text-[10px] text-primary/70 hover:text-primary transition-colors"
                @click.stop
              >
                <ExternalLink class="size-2.5" />
                {{ agent.pr }}
              </a>
            </div>

            <!-- Last update -->
            <div class="flex items-center gap-1 text-[10px] text-muted-foreground">
              <Clock class="size-2.5 shrink-0" />
              Updated {{ relativeTime(agent.updated_at) }}
            </div>
          </div>

          <!-- No data fallback -->
          <div v-else class="px-4 pb-3">
            <p class="text-xs text-muted-foreground italic">No data available</p>
          </div>

          <!-- Footer: View Details + Message -->
          <div class="border-t px-3 py-2 flex gap-1">
            <Button
              variant="ghost"
              size="sm"
              class="flex-1 h-7 text-xs justify-start gap-1.5 text-primary hover:text-primary"
              @click.stop="goToAgent"
            >
              <ArrowUpRight class="size-3" />
              View details
            </Button>
            <Button
              variant="ghost"
              size="sm"
              class="h-7 text-xs gap-1.5"
              :disabled="!spaceName"
              @click.stop="goToConversations"
            >
              <MessageSquare class="size-3" />
              Message
            </Button>
          </div>
        </template>
      </div>
    </Transition>
  </Teleport>
</template>
