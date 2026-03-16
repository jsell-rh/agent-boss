<script setup lang="ts">
import type { SessionAgentStatus } from '@/types'
import { computed } from 'vue'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ShieldCheck } from 'lucide-vue-next'

const props = defineProps<{
  agents: string[]
  tmuxStatus: Record<string, SessionAgentStatus>
  spaceName: string
}>()

const emit = defineEmits<{
  approve: [agent: string, always: boolean]
  'select-agent': [agent: string]
}>()

const count = computed(() => props.agents.length)

function agentToolInfo(name: string): { toolName: string; promptText: string } {
  const s = props.tmuxStatus[name]
  return {
    toolName: s?.tool_name || 'tool use',
    promptText: s?.prompt_text || '',
  }
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <button
        type="button"
        class="relative flex items-center justify-center size-8 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        :aria-label="`${count} agent${count !== 1 ? 's' : ''} waiting for approval`"
        :title="`${count} pending approval${count !== 1 ? 's' : ''}`"
      >
        <ShieldCheck class="size-4" />
        <!-- Pulsing badge -->
        <span
          class="absolute -top-1 -right-1 flex size-4 items-center justify-center"
          aria-hidden="true"
        >
          <span class="absolute inline-flex size-full rounded-full bg-destructive opacity-75 animate-ping" />
          <span class="relative inline-flex size-3.5 rounded-full bg-destructive text-white text-[8px] font-bold leading-none items-center justify-center">
            {{ count > 9 ? '9+' : count }}
          </span>
        </span>
      </button>
    </DropdownMenuTrigger>

    <DropdownMenuContent align="end" class="w-80 p-0">
      <DropdownMenuLabel class="px-4 py-3 flex items-center gap-2 border-b">
        <ShieldCheck class="size-4 text-destructive" />
        <span>Pending Approvals</span>
        <span class="ml-auto text-xs font-normal text-muted-foreground">{{ count }} waiting</span>
      </DropdownMenuLabel>

      <div class="max-h-96 overflow-y-auto">
        <div
          v-for="agentName in agents"
          :key="agentName"
          class="px-4 py-3 border-b last:border-b-0"
        >
          <div class="flex items-start justify-between gap-2 mb-2">
            <div class="min-w-0">
              <button
                class="font-medium text-sm hover:text-primary transition-colors truncate block"
                @click="emit('select-agent', agentName)"
              >
                {{ agentName }}
              </button>
              <p class="text-xs text-muted-foreground mt-0.5">
                Requesting: <span class="font-mono text-foreground">{{ agentToolInfo(agentName).toolName }}</span>
              </p>
              <p
                v-if="agentToolInfo(agentName).promptText"
                class="text-xs text-muted-foreground mt-1 line-clamp-2 font-mono bg-muted rounded px-1.5 py-1"
              >
                {{ agentToolInfo(agentName).promptText }}
              </p>
            </div>
          </div>
          <div class="flex gap-2">
            <!-- Approve once: neutral — safer single-use action -->
            <button
              type="button"
              class="flex-1 h-7 text-xs inline-flex items-center justify-center rounded-md font-medium transition-colors border bg-background hover:bg-accent hover:text-accent-foreground px-3"
              @click.stop="emit('approve', agentName, false)"
            >
              Approve once
            </button>
            <!-- Always allow: destructive — permanent bypass is the dangerous action -->
            <button
              type="button"
              class="flex-1 h-7 text-xs inline-flex items-center justify-center rounded-md font-medium transition-colors bg-destructive hover:bg-destructive/90 text-destructive-foreground px-3"
              @click.stop="emit('approve', agentName, true)"
            >
              Always allow
            </button>
          </div>
        </div>
      </div>

      <div v-if="count === 0" class="px-4 py-6 text-center text-sm text-muted-foreground">
        No pending approvals
      </div>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
