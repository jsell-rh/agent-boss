<script setup lang="ts">
import type { AgentStatus } from '@/types'
import { STATUS_DISPLAY } from '@/types'
import { Badge } from '@/components/ui/badge'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { computed } from 'vue'
import { Activity, StopCircle, CheckCircle2, Pause, AlertCircle } from 'lucide-vue-next'

const props = defineProps<{
  status: AgentStatus
}>()

const display = computed(() => STATUS_DISPLAY[props.status] ?? { label: props.status, tooltip: '' })

const config = computed(() => {
  switch (props.status) {
    case 'active':
      return {
        badgeClass: 'bg-green-500/15 text-green-400 border-green-500/30',
        icon: Activity,
        pulse: true,
      }
    case 'blocked':
      return {
        badgeClass: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/30',
        icon: StopCircle,
        pulse: false,
      }
    case 'done':
      return {
        badgeClass: 'bg-teal-500/15 text-teal-400 border-teal-500/30',
        icon: CheckCircle2,
        pulse: false,
      }
    case 'idle':
      return {
        badgeClass: 'bg-muted text-muted-foreground border-border',
        icon: Pause,
        pulse: false,
      }
    case 'error':
      return {
        badgeClass: 'bg-destructive/15 text-destructive border-destructive/30',
        icon: AlertCircle,
        pulse: false,
      }
    default:
      return {
        badgeClass: 'bg-muted text-muted-foreground border-border',
        icon: null,
        pulse: false,
      }
  }
})
</script>

<template>
  <Tooltip>
    <TooltipTrigger as-child>
      <Badge
        variant="outline"
        :class="['gap-1 px-1.5 py-0.5 text-xs', config.badgeClass]"
        role="status"
        :aria-label="`Agent status: ${display.label}`"
      >
        <!-- Animated ping dot for active — shows agent is alive -->
        <span v-if="config.pulse" class="relative inline-flex size-2 shrink-0" aria-hidden="true">
          <span class="absolute inline-flex size-full animate-ping rounded-full bg-green-400 opacity-60" style="animation-duration: 2s" />
          <span class="relative inline-flex size-2 rounded-full bg-green-500" />
        </span>
        <!-- Status icon -->
        <component :is="config.icon" v-if="config.icon" class="size-3 shrink-0" aria-hidden="true" />
        {{ display.label }}
      </Badge>
    </TooltipTrigger>
    <TooltipContent>
      Agent status: {{ display.label }} — {{ display.tooltip }}
    </TooltipContent>
  </Tooltip>
</template>
