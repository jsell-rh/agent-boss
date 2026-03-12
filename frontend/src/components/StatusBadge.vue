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
        badgeClass: 'bg-success/15 text-success border-success/30',
        icon: Activity,
        pulse: true,
      }
    case 'blocked':
      return {
        badgeClass: 'bg-warning/15 text-warning-foreground border-warning/30 status-jitter',
        icon: StopCircle,
        pulse: false,
      }
    case 'done':
      return {
        badgeClass: 'bg-info/15 text-info border-info/30',
        icon: CheckCircle2,
        pulse: false,
      }
    case 'idle':
      return {
        badgeClass: 'bg-muted text-muted-foreground border-border status-breathe',
        icon: Pause,
        pulse: false,
      }
    case 'error':
      return {
        badgeClass: 'bg-destructive/15 text-destructive border-destructive/30 status-jitter',
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
          <span class="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60" style="animation-duration: 2s" />
          <span class="relative inline-flex size-2 rounded-full bg-success" />
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

<style scoped>
/* Idle: slow breathe — opacity oscillates to signal "alive but resting" */
.status-breathe {
  animation: status-breathe 3s ease-in-out infinite;
}

/* Blocked/Error: subtle jitter — signals something needs attention */
.status-jitter {
  animation: status-jitter 0.4s ease-in-out infinite;
}

@keyframes status-breathe {
  0%, 100% { opacity: 1; }
  50%       { opacity: 0.45; }
}

@keyframes status-jitter {
  0%, 100% { transform: translateX(0); }
  25%      { transform: translateX(-1.5px); }
  75%      { transform: translateX(1.5px); }
}

/* Respect user motion preferences */
@media (prefers-reduced-motion: reduce) {
  .status-breathe,
  .status-jitter {
    animation: none;
  }
}
</style>
