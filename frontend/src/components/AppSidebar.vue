<script setup lang="ts">
import type { SpaceSummary, KnowledgeSpace, AgentStatus } from '@/types'
import { STATUS_DISPLAY } from '@/types'
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { relativeTime } from '@/composables/useTime'
import { prLink } from '@/lib/utils'
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
  SidebarMenuAction,
  SidebarInput,
  SidebarSeparator,
} from '@/components/ui/sidebar'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Radio, AlertCircle, ChevronRight, MoreHorizontal, Trash2, Plus, LayoutDashboard, MessageSquare, Crown, Archive, User, Settings } from 'lucide-vue-next'
import AgentAvatar from './AgentAvatar.vue'

const props = defineProps<{
  spaces: SpaceSummary[]
  currentSpace: KnowledgeSpace | null
  selectedSpace: string
  selectedAgent: string
  broadcasting?: boolean
  mentionedAgents?: Set<string>
  spawnedAgents?: Set<string>
}>()

const emit = defineEmits<{
  'select-space': [name: string]
  'select-agent': [name: string]
  broadcast: []
  'delete-space': [name: string]
  'create-space': [name: string]
  'archive-space': [name: string]
  'open-personas': []
  'open-settings': []
}>()

const router = useRouter()
const route = useRoute()

function handleSelectSpace(name: string) {
  router.push('/' + name)
  emit('select-space', name)
}

function handleSelectAgent(name: string) {
  router.push('/' + props.selectedSpace + '/' + name)
  emit('select-agent', name)
}

const agentsOpen = ref(true)
const agentSearch = ref('')

const sortedSpaces = computed(() => {
  return [...props.spaces].sort((a, b) => {
    return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
  })
})

const activeSpaces = computed(() => sortedSpaces.value.filter(s => !s.archive))
const archivedSpaces = computed(() => sortedSpaces.value.filter(s => !!s.archive))
const archivedOpen = ref(false)

// Sort order matching card grid: error first, then blocked, active, idle, done
const STATUS_ORDER: Record<string, number> = { error: 0, blocked: 1, active: 2, idle: 3, done: 4 }

const sortedAgents = computed(() => {
  if (!props.currentSpace) return []
  return Object.entries(props.currentSpace.agents).sort(([nameA, a], [nameB, b]) => {
    const aOrder = STATUS_ORDER[a.status] ?? 5
    const bOrder = STATUS_ORDER[b.status] ?? 5
    if (aOrder !== bOrder) return aOrder - bOrder
    return nameA.localeCompare(nameB)
  })
})

// Filter agents by search query (name or summary)
const filteredAgents = computed(() => {
  const q = agentSearch.value.trim().toLowerCase()
  if (!q) return sortedAgents.value
  return sortedAgents.value.filter(([name, a]) =>
    name.toLowerCase().includes(q) || a.summary?.toLowerCase().includes(q)
  )
})

// Agents needing attention: error, blocked, active
const activeAgents = computed(() =>
  filteredAgents.value.filter(([, a]) => a.status === 'error' || a.status === 'blocked' || a.status === 'active')
)

// Agents at rest: idle, done
const inactiveAgents = computed(() =>
  filteredAgents.value.filter(([, a]) => a.status === 'idle' || a.status === 'done')
)

// Status count summary line: "2 blocked, 3 active, 1 done" (only non-zero)
const agentCountSummary = computed(() => {
  if (!props.currentSpace) return ''
  const counts: Partial<Record<AgentStatus, number>> = {}
  for (const agent of Object.values(props.currentSpace.agents)) {
    counts[agent.status] = (counts[agent.status] ?? 0) + 1
  }
  const order: AgentStatus[] = ['error', 'blocked', 'active', 'idle', 'done']
  return order
    .filter(s => counts[s])
    .map(s => `${counts[s]} ${s}`)
    .join(', ')
})

// Inactive sub-group: expanded by default only when total agents < 5,
// or when the currently selected agent is in the inactive group
const inactiveOpen = ref(false)
watch(
  () => props.selectedSpace,
  () => {
    // Clear search and reset inactive section when navigating to a different space
    agentSearch.value = ''
    inactiveOpen.value = false
  }
)
watch(
  () => props.currentSpace,
  (space) => {
    if (space) {
      // After space loads, open inactive if it's a small space
      inactiveOpen.value = Object.keys(space.agents).length < 5
    }
  },
  { immediate: true }
)
// Auto-expand inactive group when the selected agent is idle/done
watch(
  () => [props.selectedAgent, props.currentSpace] as const,
  ([agent, space]) => {
    if (!agent || !space) return
    const agentData = space.agents[agent]
    if (agentData && (agentData.status === 'idle' || agentData.status === 'done')) {
      inactiveOpen.value = true
    }
  }
)

// Space delete confirmation
const spaceToDelete = ref<string | null>(null)
const deleteDialogOpen = ref(false)

function requestDeleteSpace(name: string) {
  spaceToDelete.value = name
  deleteDialogOpen.value = true
}

function confirmDeleteSpace() {
  if (spaceToDelete.value) {
    emit('delete-space', spaceToDelete.value)
  }
  spaceToDelete.value = null
  deleteDialogOpen.value = false
}

function cancelDeleteSpace() {
  spaceToDelete.value = null
  deleteDialogOpen.value = false
}

// Attention count: use server-provided value, but for the selected space
// also compute from loaded agent data (in case server hasn't been restarted)
function spaceAttentionCount(space: SpaceSummary): number {
  // For the selected space, compute from actual agent data (always fresh)
  if (space.name === props.selectedSpace && props.currentSpace) {
    let count = 0
    for (const agent of Object.values(props.currentSpace.agents)) {
      count += (agent.questions?.length ?? 0) + (agent.blockers?.length ?? 0)
    }
    return count
  }
  // For other spaces, use the server-provided count
  return space.attention_count ?? 0
}

// Count questions + blockers for a specific agent
function agentAttentionCount(agent: { questions?: string[]; blockers?: string[] }): number {
  return (agent.questions?.length ?? 0) + (agent.blockers?.length ?? 0)
}

function statusDotClass(status: string): string {
  switch (status) {
    case 'active':  return 'bg-green-500 dot-pulse-active'
    case 'blocked': return 'bg-amber-500 dot-jitter-blocked'
    case 'done':    return 'bg-teal-500'
    case 'idle':    return 'bg-muted-foreground dot-breathe-idle'
    case 'error':   return 'bg-destructive dot-jitter-blocked'
    default: return 'bg-muted-foreground'
  }
}

function statusLabel(status: string): string {
  const display = STATUS_DISPLAY[status as AgentStatus]
  return display ? display.label : status
}

// Count unread messages directed at the boss across all boss↔agent conversations.
// Messages in any agent's inbox where sender is an agent (not boss) and recipient is boss,
// plus any messages in the 'boss' agent's own inbox that are unread.
const bossUnreadCount = computed(() => {
  if (!props.currentSpace) return 0
  let count = 0
  // Messages in the 'boss' pseudo-agent inbox (agents sending TO boss)
  const bossAgent = props.currentSpace.agents['boss']
  if (bossAgent?.messages) {
    for (const msg of bossAgent.messages) {
      if (!msg.read) count++
    }
  }
  // Also count messages in agent inboxes from boss that are unread (boss sent these, agent hasn't read)
  // — intentionally excluded here: we want notifications for messages TO boss, not FROM boss
  return count
})

// ── Typing indicator ───────────────────────────────────────────────────────
// Show 3-dot bounce when an agent posted an update within the last 10 seconds.
const now = ref(Date.now())
let _nowTimer = 0
onMounted(() => { _nowTimer = window.setInterval(() => { now.value = Date.now() }, 1000) })
onUnmounted(() => clearInterval(_nowTimer))

function isRecentlyActive(agent: { updated_at?: string; status?: string }): boolean {
  if (!agent.updated_at || agent.status === 'done' || agent.status === 'idle') return false
  return now.value - new Date(agent.updated_at).getTime() < 10_000
}

// New space dialog
const newSpaceDialogOpen = ref(false)
const newSpaceName = ref('')

function submitNewSpace() {
  const name = newSpaceName.value.trim()
  if (!name) return
  emit('create-space', name)
  newSpaceName.value = ''
  newSpaceDialogOpen.value = false
}

function openNewSpaceDialog() {
  newSpaceDialogOpen.value = true
}

defineExpose({ openNewSpaceDialog })
</script>

<template>
  <Sidebar aria-label="Navigation sidebar">
    <SidebarHeader class="p-4">
      <div class="flex items-center gap-2">
        <div class="h-6 w-1 rounded-full bg-primary" aria-hidden="true" />
        <h2 class="text-lg font-semibold tracking-tight">Agent Boss</h2>
      </div>
      <div class="flex items-center gap-1.5 mt-1 text-xs text-amber-600 dark:text-amber-400">
        <Crown class="size-3 shrink-0" aria-hidden="true" />
        <span class="font-medium">You (Boss)</span>
      </div>
    </SidebarHeader>

    <!-- overflow-y-auto enables independent scrolling within the fixed-height sidebar -->
    <SidebarContent class="overflow-x-hidden overflow-y-auto">
      <!-- Spaces -->
      <SidebarGroup>
        <SidebarGroupLabel class="flex items-center justify-between">
          Spaces
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                variant="ghost"
                size="sm"
                class="h-5 w-5 p-0 text-muted-foreground hover:text-foreground"
                aria-label="Create new space"
                @click="newSpaceDialogOpen = true"
              >
                <Plus class="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="right">Create new space</TooltipContent>
          </Tooltip>
        </SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem v-for="space in activeSpaces" :key="space.name" class="group/space-item">
              <Tooltip>
                <TooltipTrigger as-child>
                  <SidebarMenuButton
                    :data-active="space.name === selectedSpace"
                    :aria-current="space.name === selectedSpace ? 'true' : undefined"
                    class="flex flex-col items-start h-auto py-2 gap-0.5"
                    @click="handleSelectSpace(space.name)"
                  >
                    <span class="truncate w-full leading-tight">{{ space.name }}</span>
                    <span class="text-[10px] text-muted-foreground leading-none">{{ relativeTime(space.updated_at) }}</span>
                  </SidebarMenuButton>
                </TooltipTrigger>
                <TooltipContent side="right">
                  <div>{{ space.name }}</div>
                  <div class="text-xs text-muted-foreground">Last active: {{ relativeTime(space.updated_at) }}</div>
                </TooltipContent>
              </Tooltip>
              <Tooltip v-if="spaceAttentionCount(space) > 0">
                <TooltipTrigger as-child>
                  <SidebarMenuBadge class="flex items-center gap-1 text-amber-500 font-semibold group-hover/space-item:opacity-0 transition-opacity">
                    <AlertCircle class="size-3" aria-hidden="true" />
                    {{ spaceAttentionCount(space) }}
                  </SidebarMenuBadge>
                </TooltipTrigger>
                <TooltipContent side="right">
                  {{ spaceAttentionCount(space) }} item{{ spaceAttentionCount(space) !== 1 ? 's' : '' }} need{{ spaceAttentionCount(space) === 1 ? 's' : '' }} attention
                </TooltipContent>
              </Tooltip>
              <SidebarMenuBadge v-else :title="`${space.agent_count} agent${space.agent_count !== 1 ? 's' : ''} in this space`" class="group-hover/space-item:opacity-0 transition-opacity">
                {{ space.agent_count }}
              </SidebarMenuBadge>
              <!-- Space context menu -->
              <DropdownMenu>
                <DropdownMenuTrigger as-child>
                  <SidebarMenuAction
                    :show-on-hover="true"
                    :aria-label="`Options for space ${space.name}`"
                  >
                    <MoreHorizontal class="size-4" aria-hidden="true" />
                  </SidebarMenuAction>
                </DropdownMenuTrigger>
                <DropdownMenuContent side="right" align="start">
                  <DropdownMenuItem
                    class="cursor-pointer"
                    @click="emit('archive-space', space.name)"
                  >
                    <Archive class="size-4 mr-2" aria-hidden="true" />
                    Archive space
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    class="text-destructive focus:text-destructive focus:bg-destructive/10 cursor-pointer"
                    @click="requestDeleteSpace(space.name)"
                  >
                    <Trash2 class="size-4 mr-2" aria-hidden="true" />
                    Delete space
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </SidebarMenuItem>
            <SidebarMenuItem v-if="activeSpaces.length === 0 && archivedSpaces.length === 0">
              <div class="px-2 py-3 text-sm text-muted-foreground font-text">
                No spaces yet — agents will create spaces when they register
              </div>
            </SidebarMenuItem>
          </SidebarMenu>

          <!-- Archived spaces collapsible section -->
          <Collapsible v-if="archivedSpaces.length > 0" v-model:open="archivedOpen">
            <CollapsibleTrigger
              class="flex w-full items-center gap-1 px-3 py-1.5 text-[11px] text-muted-foreground hover:text-foreground cursor-pointer select-none"
              :aria-expanded="archivedOpen"
            >
              <ChevronRight
                :class="['size-3 transition-transform', archivedOpen && 'rotate-90']"
                aria-hidden="true"
              />
              Archived
              <span class="ml-auto rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium tabular-nums">
                {{ archivedSpaces.length }}
              </span>
            </CollapsibleTrigger>
            <CollapsibleContent>
              <SidebarMenu>
                <SidebarMenuItem v-for="space in archivedSpaces" :key="space.name" class="group/space-item">
                  <Tooltip>
                    <TooltipTrigger as-child>
                      <SidebarMenuButton
                        :data-active="space.name === selectedSpace"
                        :aria-current="space.name === selectedSpace ? 'true' : undefined"
                        class="flex flex-col items-start h-auto py-2 gap-0.5 opacity-60"
                        @click="handleSelectSpace(space.name)"
                      >
                        <div class="flex items-center gap-1.5 w-full">
                          <Archive class="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
                          <span class="truncate leading-tight">{{ space.name }}</span>
                        </div>
                        <span class="text-[10px] text-muted-foreground leading-none pl-5">{{ relativeTime(space.updated_at) }}</span>
                      </SidebarMenuButton>
                    </TooltipTrigger>
                    <TooltipContent side="right">
                      <div>{{ space.name }} (archived)</div>
                      <div class="text-xs text-muted-foreground">Last active: {{ relativeTime(space.updated_at) }}</div>
                    </TooltipContent>
                  </Tooltip>
                  <!-- Space context menu for archived spaces -->
                  <DropdownMenu>
                    <DropdownMenuTrigger as-child>
                      <SidebarMenuAction
                        :show-on-hover="true"
                        :aria-label="`Options for space ${space.name}`"
                      >
                        <MoreHorizontal class="size-4" aria-hidden="true" />
                      </SidebarMenuAction>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent side="right" align="start">
                      <DropdownMenuItem
                        class="cursor-pointer"
                        @click="emit('archive-space', space.name)"
                      >
                        <Archive class="size-4 mr-2" aria-hidden="true" />
                        Unarchive space
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        class="text-destructive focus:text-destructive focus:bg-destructive/10 cursor-pointer"
                        @click="requestDeleteSpace(space.name)"
                      >
                        <Trash2 class="size-4 mr-2" aria-hidden="true" />
                        Delete space
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </SidebarMenuItem>
              </SidebarMenu>
            </CollapsibleContent>
          </Collapsible>
        </SidebarGroupContent>
      </SidebarGroup>

      <SidebarSeparator v-if="currentSpace" />

      <!-- Space nav: Tasks board + Conversations -->
      <SidebarGroup v-if="currentSpace">
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                :data-active="route.path.includes('/kanban')"
                @click="router.push('/' + selectedSpace + '/kanban')"
              >
                <LayoutDashboard class="size-4" />
                <span>Tasks</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                :data-active="route.path.includes('/conversations')"
                @click="router.push('/' + selectedSpace + '/conversations')"
              >
                <div class="relative shrink-0">
                  <MessageSquare class="size-4" />
                  <!-- Unread boss-message dot -->
                  <span
                    v-if="bossUnreadCount > 0"
                    class="absolute -top-1.5 -right-1.5 flex items-center justify-center rounded-full bg-red-500 text-white text-[9px] font-bold leading-none min-w-[14px] h-3.5 px-0.5"
                    :title="`${bossUnreadCount} unread message${bossUnreadCount !== 1 ? 's' : ''} for boss`"
                  >{{ bossUnreadCount }}</span>
                </div>
                <span>Conversations</span>
              </SidebarMenuButton>
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
              <ChevronRight
                :class="['ml-auto h-4 w-4 transition-transform', agentsOpen && 'rotate-90']"
                aria-hidden="true"
              />
              <span class="sr-only">{{ agentsOpen ? 'Collapse' : 'Expand' }} agents list</span>
            </SidebarGroupLabel>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <!-- Compact status breakdown summary -->
            <p v-if="agentCountSummary" class="px-3 pb-1 text-[11px] text-muted-foreground leading-none">
              {{ agentCountSummary }}
            </p>

            <!-- Agent search input -->
            <div class="px-3 pb-2">
              <SidebarInput
                v-model="agentSearch"
                placeholder="Search agents…"
                aria-label="Filter agents by name or summary"
                class="h-7 text-xs"
              />
            </div>

            <SidebarGroupContent>
              <SidebarMenu>
                <!-- Active agents: error, blocked, active — sorted by priority -->
                <SidebarMenuItem
                  v-for="[name, agent] in activeAgents"
                  :key="name"
                  :class="{
                    'mention-pulse': props.mentionedAgents?.has(name),
                    'agent-spawn': props.spawnedAgents?.has(name),
                  }"
                >
                  <SidebarMenuButton
                    size="lg"
                    class="py-3 h-auto min-h-12"
                    :data-active="name === selectedAgent"
                    :aria-current="name === selectedAgent ? 'true' : undefined"
                    :aria-label="`${name} — ${statusLabel(agent.status)}`"
                    @click="handleSelectAgent(name)"
                  >
                    <div class="relative shrink-0">
                      <AgentAvatar :name="name" :size="20" />
                      <span
                        :class="['absolute -bottom-0.5 -right-0.5 block size-2.5 rounded-full ring-1 ring-sidebar', statusDotClass(agent.status)]"
                        aria-hidden="true"
                      />
                    </div>
                    <div class="flex flex-col gap-0.5 min-w-0 flex-1">
                      <div class="flex items-center gap-1.5 min-w-0">
                        <span class="truncate">{{ name }}</span>
                        <span v-if="isRecentlyActive(agent)" class="typing-dots shrink-0" aria-label="recently updated" aria-hidden="true">
                          <span /><span /><span />
                        </span>
                      </div>
                      <div v-if="agent.mood" class="text-[10px] text-muted-foreground truncate italic leading-none">{{ agent.mood }}</div>
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
                          v-if="agent.pr && prLink(agent)"
                          :href="prLink(agent)!"
                          target="_blank"
                          rel="noopener noreferrer"
                          :class="['text-[10px] hover:underline shrink-0 text-primary', { 'pr-shimmer': agent.status === 'active' }]"
                          :title="prLink(agent)!"
                          @click.stop
                        >{{ agent.pr }}</a>
                        <span
                          v-else-if="agent.pr"
                          :class="['text-[10px] shrink-0 text-muted-foreground', { 'pr-shimmer': agent.status === 'active' }]"
                        >{{ agent.pr }}</span>
                      </div>
                    </div>
                  </SidebarMenuButton>
                  <Tooltip v-if="agentAttentionCount(agent) > 0">
                    <TooltipTrigger as-child>
                      <SidebarMenuBadge class="text-amber-500 font-semibold text-[10px]">
                        {{ agentAttentionCount(agent) }}
                      </SidebarMenuBadge>
                    </TooltipTrigger>
                    <TooltipContent side="right">
                      {{ agent.questions?.length ?? 0 }} question{{ (agent.questions?.length ?? 0) !== 1 ? 's' : '' }},
                      {{ agent.blockers?.length ?? 0 }} blocker{{ (agent.blockers?.length ?? 0) !== 1 ? 's' : '' }}
                    </TooltipContent>
                  </Tooltip>
                  <SidebarMenuBadge
                    v-else-if="agent.phase"
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
                <SidebarMenuItem v-else-if="filteredAgents.length === 0">
                  <div class="px-2 py-3 text-sm text-muted-foreground font-text">
                    No agents match "{{ agentSearch }}"
                  </div>
                </SidebarMenuItem>
              </SidebarMenu>
            </SidebarGroupContent>

            <!-- Collapsible sub-group for done/idle agents -->
            <Collapsible v-if="inactiveAgents.length > 0" v-model:open="inactiveOpen">
              <CollapsibleTrigger
                class="flex w-full items-center gap-1 px-3 py-1.5 text-[11px] text-muted-foreground hover:text-foreground cursor-pointer select-none"
                :aria-expanded="inactiveOpen"
              >
                <ChevronRight
                  :class="['size-3 transition-transform', inactiveOpen && 'rotate-90']"
                  aria-hidden="true"
                />
                Done / Idle
                <span class="ml-auto rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium tabular-nums">
                  {{ inactiveAgents.length }}
                </span>
                <span class="sr-only">{{ inactiveOpen ? 'Collapse' : 'Expand' }} done and idle agents</span>
              </CollapsibleTrigger>
              <CollapsibleContent>
                <SidebarGroupContent>
                  <SidebarMenu>
                    <SidebarMenuItem
                      v-for="[name, agent] in inactiveAgents"
                      :key="name"
                      :class="{ 'mention-pulse': props.mentionedAgents?.has(name) }"
                    >
                      <SidebarMenuButton
                        class="py-1 h-8 opacity-60"
                        :data-active="name === selectedAgent"
                        :aria-current="name === selectedAgent ? 'true' : undefined"
                        :aria-label="`${name} — ${statusLabel(agent.status)}`"
                        @click="handleSelectAgent(name)"
                      >
                        <div class="relative shrink-0">
                          <AgentAvatar :name="name" :size="16" />
                          <span
                            :class="['absolute -bottom-0.5 -right-0.5 block size-2 rounded-full ring-1 ring-sidebar', statusDotClass(agent.status)]"
                            aria-hidden="true"
                          />
                        </div>
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
                              v-if="agent.pr && prLink(agent)"
                              :href="prLink(agent)!"
                              target="_blank"
                              rel="noopener noreferrer"
                              class="text-[10px] text-primary hover:underline shrink-0"
                              :title="prLink(agent)!"
                              @click.stop
                            >{{ agent.pr }}</a>
                            <span
                              v-else-if="agent.pr"
                              class="text-[10px] text-muted-foreground shrink-0"
                            >{{ agent.pr }}</span>
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
                  </SidebarMenu>
                </SidebarGroupContent>
              </CollapsibleContent>
            </Collapsible>
          </CollapsibleContent>
        </Collapsible>
      </SidebarGroup>
    </SidebarContent>

    <SidebarFooter class="p-3 gap-2">
      <Tooltip v-if="currentSpace">
        <TooltipTrigger as-child>
          <Button
            variant="outline"
            size="sm"
            class="w-full"
            :disabled="broadcasting"
            @click="emit('broadcast')"
          >
            <Radio class="size-4" /> Nudge All
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          Nudge all agents with the latest space state
        </TooltipContent>
      </Tooltip>
      <!-- Global settings row — always at bottom of sidebar -->
      <div class="flex items-center justify-end gap-1 pt-1 border-t border-border/50">
        <Tooltip>
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="sm"
              class="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
              aria-label="Personas"
              @click="emit('open-personas')"
            >
              <User class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="top">Personas</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="sm"
              class="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
              aria-label="Settings"
              @click="emit('open-settings')"
            >
              <Settings class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="top">Settings</TooltipContent>
        </Tooltip>
      </div>
    </SidebarFooter>
  </Sidebar>

  <!-- Delete space confirmation dialog -->
  <AlertDialog v-model:open="deleteDialogOpen">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Delete space "{{ spaceToDelete }}"?</AlertDialogTitle>
        <AlertDialogDescription>
          This will permanently delete the space and all its agent data. This action cannot be undone.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel @click="cancelDeleteSpace">Cancel</AlertDialogCancel>
        <AlertDialogAction
          class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          @click="confirmDeleteSpace"
        >
          Delete
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>

  <!-- New Space dialog -->
  <Dialog v-model:open="newSpaceDialogOpen">
    <DialogContent class="sm:max-w-sm">
      <DialogHeader>
        <DialogTitle>Create new space</DialogTitle>
        <DialogDescription>
          Enter a name for the new space. Agents will post updates to it using this name.
        </DialogDescription>
      </DialogHeader>
      <form @submit.prevent="submitNewSpace">
        <Input
          v-model="newSpaceName"
          placeholder="e.g. MyProject"
          class="mb-4"
          autofocus
          @keydown.escape="newSpaceDialogOpen = false"
        />
        <DialogFooter>
          <Button type="button" variant="outline" @click="newSpaceDialogOpen = false">Cancel</Button>
          <Button type="submit" :disabled="!newSpaceName.trim()">Create</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
/* @mention pulse — 3s highlight ring on agent card when @mentioned in a message */
.mention-pulse {
  animation: mention-ring 3s ease-out forwards;
  border-radius: 0.5rem;
}

@keyframes mention-ring {
  0%   { box-shadow: 0 0 0 0 hsl(var(--primary) / 0.7); }
  25%  { box-shadow: 0 0 0 4px hsl(var(--primary) / 0.4); }
  60%  { box-shadow: 0 0 0 6px hsl(var(--primary) / 0.15); }
  100% { box-shadow: 0 0 0 0 hsl(var(--primary) / 0); }
}

/* PR badge shimmer — traveling light on "in review" PR links */
.pr-shimmer {
  background: linear-gradient(
    90deg,
    transparent 0%,
    hsl(var(--primary) / 0.35) 50%,
    transparent 100%
  ) no-repeat;
  background-size: 200% 100%;
  background-clip: text;
  -webkit-background-clip: text;
  color: transparent;
  -webkit-text-fill-color: transparent;
  animation: pr-shimmer-travel 2.4s ease-in-out infinite;
}
@keyframes pr-shimmer-travel {
  0%   { background-position: 150% center; }
  100% { background-position: -50% center; }
}

/* Spawn warp — new agents warp in like a portal opening (scale + ring) */
.agent-spawn {
  animation: agent-spawn-warp 0.5s cubic-bezier(0.22, 1, 0.36, 1);
  border-radius: 0.5rem;
  transform-origin: center;
}

@keyframes agent-spawn-warp {
  0%   { opacity: 0; transform: scale(0.35); box-shadow: 0 0 0 8px hsl(var(--primary) / 0.6); }
  50%  { opacity: 1; transform: scale(1.04); box-shadow: 0 0 0 3px hsl(var(--primary) / 0.3); }
  75%  { transform: scale(0.985); box-shadow: 0 0 0 1px hsl(var(--primary) / 0.1); }
  100% { transform: scale(1); box-shadow: none; }
}

/* Status dot animations — pulse, breathe, jitter */
.dot-pulse-active {
  animation: dot-sonar-ping 2s ease-out infinite;
}
@keyframes dot-sonar-ping {
  0%   { box-shadow: 0 0 0 0 rgba(34, 197, 94, 0.7); }
  60%  { box-shadow: 0 0 0 6px rgba(34, 197, 94, 0); }
  100% { box-shadow: 0 0 0 0 rgba(34, 197, 94, 0); }
}

.dot-breathe-idle {
  animation: dot-breathe 3.5s ease-in-out infinite;
}
@keyframes dot-breathe {
  0%, 100% { opacity: 0.45; }
  50%       { opacity: 1; }
}

.dot-jitter-blocked {
  animation: dot-jitter 0.6s ease-in-out infinite alternate;
}
@keyframes dot-jitter {
  0%   { transform: translateX(0); }
  33%  { transform: translateX(-1.5px); }
  66%  { transform: translateX(1.5px); }
  100% { transform: translateX(0); }
}

/* Typing indicator — 3 dots bouncing when agent recently posted */
.typing-dots {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  height: 10px;
}
.typing-dots span {
  display: inline-block;
  width: 3px;
  height: 3px;
  border-radius: 50%;
  background: hsl(var(--muted-foreground));
  animation: typing-bounce 1.2s ease-in-out infinite;
}
.typing-dots span:nth-child(2) { animation-delay: 0.2s; }
.typing-dots span:nth-child(3) { animation-delay: 0.4s; }

@keyframes typing-bounce {
  0%, 60%, 100% { transform: translateY(0); opacity: 0.5; }
  30%            { transform: translateY(-4px); opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .mention-pulse { animation: none; }
  .agent-spawn { animation: none; }
  .typing-dots span { animation: none; opacity: 0.6; }
  .dot-pulse-active, .dot-breathe-idle, .dot-jitter-blocked { animation: none; }
}
</style>
