<script setup lang="ts">
import type { SpaceSummary, KnowledgeSpace, AgentStatus } from '@/types'
import { STATUS_DISPLAY } from '@/types'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarSeparator,
} from '@/components/ui/sidebar'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Radio } from 'lucide-vue-next'

const props = defineProps<{
  spaces: SpaceSummary[]
  currentSpace: KnowledgeSpace | null
  selectedSpace: string
  selectedAgent: string
}>()

const emit = defineEmits<{
  'select-space': [name: string]
  'select-agent': [name: string]
  broadcast: []
}>()

const router = useRouter()

function handleSelectSpace(name: string) {
  router.push('/' + name)
  emit('select-space', name)
}

function handleSelectAgent(name: string) {
  router.push('/' + props.selectedSpace + '/' + name)
  emit('select-agent', name)
}

const agentsOpen = ref(true)

const sortedSpaces = computed(() => {
  return [...props.spaces].sort((a, b) => {
    return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
  })
})

const sortedAgents = computed(() => {
  if (!props.currentSpace) return []
  return Object.entries(props.currentSpace.agents).sort(([, a], [, b]) => {
    return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
  })
})

function statusDotClass(status: string): string {
  switch (status) {
    case 'active': return 'bg-green-500'
    case 'blocked': return 'bg-primary'
    case 'done': return 'bg-teal-500'
    case 'idle': return 'bg-muted-foreground'
    case 'error': return 'bg-destructive'
    default: return 'bg-muted-foreground'
  }
}

function statusLabel(status: string): string {
  const display = STATUS_DISPLAY[status as AgentStatus]
  return display ? display.label : status
}

function statusTooltip(status: string): string {
  const display = STATUS_DISPLAY[status as AgentStatus]
  return display ? `Task Status: ${display.label} — ${display.tooltip}` : status
}
</script>

<template>
  <Sidebar aria-label="Navigation sidebar">
    <SidebarHeader class="p-4">
      <div class="flex items-center gap-2">
        <div class="h-6 w-1 rounded-full bg-primary" aria-hidden="true" />
        <h2 class="text-lg font-semibold tracking-tight">Agent Boss</h2>
      </div>
    </SidebarHeader>

    <SidebarContent class="overflow-x-hidden">
      <!-- Spaces -->
      <SidebarGroup>
        <SidebarGroupLabel>Spaces</SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem v-for="space in sortedSpaces" :key="space.name">
              <SidebarMenuButton
                :data-active="space.name === selectedSpace"
                :aria-current="space.name === selectedSpace ? 'true' : undefined"
                @click="handleSelectSpace(space.name)"
              >
                <span class="truncate">{{ space.name }}</span>
              </SidebarMenuButton>
              <SidebarMenuBadge :title="`${space.agent_count} agent${space.agent_count !== 1 ? 's' : ''} in this space`">
                {{ space.agent_count }}
              </SidebarMenuBadge>
            </SidebarMenuItem>
            <SidebarMenuItem v-if="spaces.length === 0">
              <div class="px-2 py-3 text-sm text-muted-foreground font-text">
                No spaces yet — agents will create spaces when they register
              </div>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>

      <SidebarSeparator v-if="currentSpace" />

      <!-- Agents in selected space -->
      <SidebarGroup v-if="currentSpace">
        <Collapsible v-model:open="agentsOpen">
          <CollapsibleTrigger as-child>
            <SidebarGroupLabel class="cursor-pointer select-none" :aria-expanded="agentsOpen" role="button">
              Agents
              <svg
                :class="['ml-auto h-4 w-4 transition-transform', agentsOpen && 'rotate-90']"
                xmlns="http://www.w3.org/2000/svg"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
                aria-hidden="true"
              >
                <path d="m9 18 6-6-6-6" />
              </svg>
              <span class="sr-only">{{ agentsOpen ? 'Collapse' : 'Expand' }} agents list</span>
            </SidebarGroupLabel>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <SidebarGroupContent>
              <SidebarMenu>
                <SidebarMenuItem v-for="[name, agent] in sortedAgents" :key="name">
                  <SidebarMenuButton
                    size="lg"
                    class="py-3 h-auto min-h-12"
                    :data-active="name === selectedAgent"
                    :aria-current="name === selectedAgent ? 'true' : undefined"
                    :aria-label="`${name} — ${statusLabel(agent.status)}`"
                    @click="handleSelectAgent(name)"
                  >
                    <Tooltip>
                      <TooltipTrigger as-child>
                        <span
                          :class="['inline-block size-2 rounded-full shrink-0', statusDotClass(agent.status)]"
                          :aria-label="statusLabel(agent.status)"
                          role="img"
                        />
                      </TooltipTrigger>
                      <TooltipContent side="right">
                        {{ statusTooltip(agent.status) }}
                      </TooltipContent>
                    </Tooltip>
                    <div class="flex flex-col gap-0.5 min-w-0 flex-1">
                      <span class="truncate">{{ name }}</span>
                      <div v-if="agent.branch || agent.pr" class="flex items-center gap-1.5">
                        <Tooltip v-if="agent.branch">
                          <TooltipTrigger as-child>
                            <span
                              class="font-mono text-[10px] text-muted-foreground bg-muted px-1 rounded truncate max-w-[100px]"
                            >{{ agent.branch }}</span>
                          </TooltipTrigger>
                          <TooltipContent side="right">
                            <div>Branch: {{ agent.branch }}</div>
                            <div v-if="agent.repo_url">Repo: {{ agent.repo_url }}</div>
                          </TooltipContent>
                        </Tooltip>
                        <a
                          v-if="agent.pr"
                          :href="agent.pr"
                          target="_blank"
                          rel="noopener noreferrer"
                          class="text-[10px] text-primary hover:underline shrink-0"
                          :title="agent.pr"
                          @click.stop
                        >PR</a>
                      </div>
                    </div>
                  </SidebarMenuButton>
                  <SidebarMenuBadge
                    v-if="agent.phase"
                    class="text-muted-foreground text-[10px]"
                    :title="`Current phase: ${agent.phase}`"
                  >
                    {{ agent.phase }}
                  </SidebarMenuBadge>
                </SidebarMenuItem>
                <SidebarMenuItem v-if="sortedAgents.length === 0">
                  <div class="px-2 py-3 text-sm text-muted-foreground font-text">
                    No agents in this space yet
                  </div>
                </SidebarMenuItem>
              </SidebarMenu>
            </SidebarGroupContent>
          </CollapsibleContent>
        </Collapsible>
      </SidebarGroup>
    </SidebarContent>

    <SidebarFooter v-if="currentSpace" class="p-3">
      <Tooltip>
        <TooltipTrigger as-child>
          <Button
            variant="outline"
            size="sm"
            class="w-full"
            @click="emit('broadcast')"
          >
            <Radio class="size-4" /> Nudge {{ currentSpace.name }}
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          Nudge all agents with the latest space state
        </TooltipContent>
      </Tooltip>
    </SidebarFooter>
  </Sidebar>
</template>
