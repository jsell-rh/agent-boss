<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { SpaceSummary, KnowledgeSpace, TmuxAgentStatus, AgentUpdate, HierarchyTree, HierarchyNode } from '@/types'
import { api } from '@/api/client'
import { useSSE } from '@/composables/useSSE'

import { SidebarProvider, SidebarInset, SidebarTrigger } from '@/components/ui/sidebar'
import { Separator } from '@/components/ui/separator'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import AppSidebar from '@/components/AppSidebar.vue'
import SpaceOverview from '@/components/SpaceOverview.vue'
import AgentDetail from '@/components/AgentDetail.vue'
import EventLog from '@/components/EventLog.vue'
import ConversationsView from '@/components/ConversationsView.vue'
import { Keyboard } from 'lucide-vue-next'
import { useTheme } from '@/composables/useTheme'

const { theme, toggle: toggleTheme } = useTheme()

// ── Router ─────────────────────────────────────────────────────────
const route = useRoute()
const router = useRouter()

// ── State ──────────────────────────────────────────────────────────
const spaces = ref<SpaceSummary[]>([])
const currentSpace = ref<KnowledgeSpace | null>(null)
const tmuxStatus = ref<Record<string, TmuxAgentStatus>>({})
const hierarchyTree = ref<HierarchyTree | null>(null)

const loading = ref(true)
const spaceLoading = ref(false)
const errorMessage = ref<string | null>(null)
const successMessage = ref<string | null>(null)
const statusAnnouncement = ref('')
const broadcasting = ref(false)

const sse = useSSE()
const eventLogRef = ref<InstanceType<typeof EventLog> | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

// ── Component refs ──────────────────────────────────────────────────
const spaceOverviewRef = ref<InstanceType<typeof SpaceOverview> | null>(null)

// ── Keyboard shortcut state ────────────────────────────────────────
const showHelpOverlay = ref(false)
const showMessageDialog = ref(false)
const kbMessageText = ref('')
const kbMessageSender = ref('boss')
const kbMessageSending = ref(false)
const savedFocusEl = ref<HTMLElement | null>(null)

function restoreFocus() {
  const el = savedFocusEl.value
  savedFocusEl.value = null
  if (el) nextTick(() => el.focus())
}

// ── Route-derived selection ────────────────────────────────────────
const selectedSpace = computed(() => {
  const p = route.params.space
  return typeof p === 'string' ? p : ''
})

const selectedAgent = computed(() => {
  const p = route.params.agent
  return typeof p === 'string' ? p : ''
})

// ── Computed ───────────────────────────────────────────────────────
const showConversations = computed(() => selectedAgent.value === 'conversations')

const selectedAgentData = computed<AgentUpdate | null>(() => {
  if (!currentSpace.value || !selectedAgent.value || showConversations.value) return null
  return currentSpace.value.agents[selectedAgent.value] ?? null
})

const selectedAgentTmux = computed<TmuxAgentStatus | null>(() => {
  if (!selectedAgent.value) return null
  return tmuxStatus.value[selectedAgent.value] ?? null
})

const currentAgentNames = computed<string[]>(() =>
  Object.keys(currentSpace.value?.agents ?? {}),
)

// Build hierarchy from agent parent fields so done/idle agents are included.
// Merges API hierarchy data (role) with the complete agent roster.
const effectiveHierarchy = computed<HierarchyTree | null>(() => {
  if (!currentSpace.value) return hierarchyTree.value
  const agents = currentSpace.value.agents
  const agentNames = Object.keys(agents)
  if (!agentNames.some(n => agents[n]?.parent)) return hierarchyTree.value

  // Build children map from agent.parent fields
  const childrenOf: Record<string, string[]> = {}
  for (const name of agentNames) {
    childrenOf[name] = childrenOf[name] ?? []
    const parentName = agents[name]!.parent
    if (parentName && agentNames.includes(parentName)) {
      childrenOf[parentName] = childrenOf[parentName] ?? []
      childrenOf[parentName].push(name)
    }
  }

  // Build nodes
  const nodes: Record<string, HierarchyNode> = {}
  for (const name of agentNames) {
    const agent = agents[name]!
    const apiNode = hierarchyTree.value?.nodes[name]
    nodes[name] = {
      agent: name,
      parent: agent.parent ?? apiNode?.parent,
      children: childrenOf[name] ?? [],
      depth: 0, // computed below
      role: agent.role ?? apiNode?.role,
    }
  }

  // Compute depths via BFS from roots
  const roots = agentNames.filter(n => {
    const parent = nodes[n]?.parent
    return !parent || !agentNames.includes(parent)
  })
  const queue: { name: string; depth: number }[] = roots.map(r => ({ name: r, depth: 0 }))
  const visited = new Set<string>()
  while (queue.length > 0) {
    const { name, depth } = queue.shift()!
    if (visited.has(name) || !nodes[name]) continue
    visited.add(name)
    nodes[name]!.depth = depth
    for (const child of nodes[name]!.children) {
      queue.push({ name: child, depth: depth + 1 })
    }
  }

  return { space: currentSpace.value.name, roots, nodes }
})

// ── Error feedback ────────────────────────────────────────────────
function showError(msg: string) {
  errorMessage.value = msg
  statusAnnouncement.value = `Error: ${msg}`
  setTimeout(() => {
    errorMessage.value = null
  }, 5000)
}

function showStatus(msg: string) {
  statusAnnouncement.value = msg
  successMessage.value = msg
  setTimeout(() => {
    successMessage.value = null
  }, 3000)
}

// ── Data fetching ──────────────────────────────────────────────────
async function loadSpaces() {
  try {
    const fetched = await api.fetchSpaces()
    // Sort by updated_at descending (newest first)
    fetched.sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
    spaces.value = fetched
  } catch (err) {
    console.error('Failed to load spaces:', err)
    showError('Failed to load spaces. Check server connection.')
  }
}

async function loadSpace(name: string, showLoader = false) {
  if (showLoader) spaceLoading.value = true
  try {
    currentSpace.value = await api.fetchSpace(name)
  } catch (err) {
    console.error(`Failed to load space ${name}:`, err)
    currentSpace.value = null
    showError(`Failed to load space "${name}".`)
  } finally {
    spaceLoading.value = false
  }
}

async function loadHierarchy(space: string) {
  try {
    hierarchyTree.value = await api.fetchHierarchy(space)
  } catch {
    hierarchyTree.value = null
  }
}

async function loadTmuxStatus(space: string) {
  try {
    const raw = await api.fetchTmuxStatus(space)
    // The server returns an array of {agent, ...} objects — normalize to a map
    if (Array.isArray(raw)) {
      const map: Record<string, TmuxAgentStatus> = {}
      for (const item of raw as any[]) {
        if (item.agent) {
          map[item.agent] = item
        }
      }
      tmuxStatus.value = map
    } else {
      tmuxStatus.value = raw
    }
  } catch {
    tmuxStatus.value = {}
  }
}

// ── Selection handlers (via router) ────────────────────────────────
function handleSelectSpace(name: string) {
  router.push('/' + name)
}

function handleSelectAgent(name: string) {
  router.push('/' + selectedSpace.value + '/' + name)
}

// ── Watch route params for data loading & SSE ──────────────────────
watch(
  () => selectedSpace.value,
  (space, oldSpace) => {
    if (space && space !== oldSpace) {
      currentSpace.value = null  // clear stale data immediately
      hierarchyTree.value = null
      loadSpace(space, true)
      loadTmuxStatus(space)
      loadHierarchy(space)
      // Reconnect SSE to this space
      sse.disconnect()
      sse.connect(space)
    } else if (!space) {
      currentSpace.value = null
      tmuxStatus.value = {}
      hierarchyTree.value = null
      sse.disconnect()
      sse.connect() // global SSE
    }
  },
)

// ── Action handlers ────────────────────────────────────────────────
async function handleBroadcastSpace() {
  if (!selectedSpace.value || broadcasting.value) return
  broadcasting.value = true
  try {
    await api.broadcastSpace(selectedSpace.value)
    showStatus(`Nudge sent to all agents in ${selectedSpace.value}`)
  } catch (err) {
    console.error('Broadcast failed:', err)
    showError('Nudge failed. Please try again.')
  } finally {
    broadcasting.value = false
  }
}

async function handleApproveAgent() {
  if (!selectedSpace.value || !selectedAgent.value) return
  try {
    await api.approveAgent(selectedSpace.value, selectedAgent.value)
    await loadTmuxStatus(selectedSpace.value)
    showStatus(`Approved ${selectedAgent.value}`)
  } catch (err) {
    console.error('Approve failed:', err)
    showError('Approval failed. Please try again.')
  }
}

async function handleReplyAgent(text: string) {
  if (!selectedSpace.value || !selectedAgent.value) return
  try {
    await api.replyToAgent(selectedSpace.value, selectedAgent.value, text)
    showStatus(`Reply sent to ${selectedAgent.value}`)
  } catch (err) {
    console.error('Reply failed:', err)
    showError('Reply failed. Please try again.')
  }
}

async function handleBroadcastAgent() {
  if (!selectedSpace.value || !selectedAgent.value || broadcasting.value) return
  broadcasting.value = true
  try {
    await api.broadcastAgent(selectedSpace.value, selectedAgent.value)
    showStatus(`Nudge sent to ${selectedAgent.value}`)
  } catch (err) {
    console.error('Broadcast agent failed:', err)
    showError('Nudge failed. Please try again.')
  } finally {
    broadcasting.value = false
  }
}

async function handleDismissQuestion(index: number) {
  if (!selectedSpace.value || !selectedAgent.value) return
  try {
    await api.dismissItem(selectedSpace.value, selectedAgent.value, index, 'question')
    await loadSpace(selectedSpace.value)
    spaceOverviewRef.value?.refreshInbox()
    showStatus('Question dismissed')
  } catch (err) {
    console.error('Dismiss question failed:', err)
    showError('Failed to dismiss question.')
  }
}

async function handleDismissBlocker(index: number) {
  if (!selectedSpace.value || !selectedAgent.value) return
  try {
    await api.dismissItem(selectedSpace.value, selectedAgent.value, index, 'blocker')
    await loadSpace(selectedSpace.value)
    spaceOverviewRef.value?.refreshInbox()
    showStatus('Blocker dismissed')
  } catch (err) {
    console.error('Dismiss blocker failed:', err)
    showError('Failed to dismiss blocker.')
  }
}

async function handleSendMessage(text: string, sender: string) {
  if (!selectedSpace.value || !selectedAgent.value) return
  try {
    await api.sendMessage(selectedSpace.value, selectedAgent.value, text, sender)
    await loadSpace(selectedSpace.value)
  } catch (err) {
    console.error('Send message failed:', err)
    showError('Failed to send message.')
  }
}

async function handleDeleteAgent(agentName?: string) {
  const space = selectedSpace.value
  const agent = agentName || selectedAgent.value
  if (!space || !agent) return
  try {
    await api.deleteAgent(space, agent)
    showStatus(`Deleted agent ${agent}`)
    // Navigate back to space overview if we deleted the currently selected agent
    if (agent === selectedAgent.value) {
      router.push('/' + space)
    }
    await loadSpace(space)
    await loadSpaces()
  } catch (err) {
    console.error('Delete agent failed:', err)
    showError(`Failed to delete agent "${agent}".`)
  }
}

async function handleDeleteSpace(spaceName: string) {
  try {
    await api.deleteSpace(spaceName)
    showStatus(`Deleted space "${spaceName}"`)
    if (selectedSpace.value === spaceName) {
      currentSpace.value = null
      router.push('/')
    }
    await loadSpaces()
  } catch (err) {
    console.error('Delete space failed:', err)
    showError(`Failed to delete space "${spaceName}".`)
  }
}

async function handleCreateSpace(spaceName: string) {
  try {
    await api.createSpace(spaceName)
    showStatus(`Created space "${spaceName}"`)
    await loadSpaces()
    router.push('/' + spaceName)
  } catch (err) {
    console.error('Create space failed:', err)
    showError(`Failed to create space "${spaceName}".`)
  }
}

async function handleClearDoneAgents(agentNames: string[]) {
  const space = selectedSpace.value
  if (!space || agentNames.length === 0) return
  try {
    await Promise.all(agentNames.map(name => api.deleteAgent(space, name)))
    showStatus(`Removed ${agentNames.length} done/idle agent${agentNames.length !== 1 ? 's' : ''}`)
    await loadSpace(space)
    await loadSpaces()
  } catch (err) {
    console.error('Clear done agents failed:', err)
    showError('Failed to clear done/idle agents.')
  }
}

async function handleBroadcastSingleAgent(agentName: string) {
  if (!selectedSpace.value) return
  try {
    await api.broadcastAgent(selectedSpace.value, agentName)
    showStatus(`Nudge sent to ${agentName}`)
  } catch (err) {
    console.error('Broadcast agent failed:', err)
    showError('Nudge failed. Please try again.')
  }
}

async function handleSendMessageToAgent(agentName: string, text: string) {
  if (!selectedSpace.value) return
  try {
    await api.sendMessage(selectedSpace.value, agentName, text, 'boss')
    await loadSpace(selectedSpace.value)
    showStatus(`Message sent to ${agentName}`)
  } catch (err) {
    console.error('Send message to agent failed:', err)
    showError(`Failed to send message to "${agentName}".`)
  }
}

async function handleReplyToQuestion(agentName: string, questionIndex: number, questionText: string, replyText: string) {
  if (!selectedSpace.value) return
  try {
    // 1. Send as persistent message so agent sees it on next check-in
    await api.sendMessage(selectedSpace.value, agentName, `Re: ${questionText}\n\n${replyText}`, 'Boss')
    // 2. Dismiss the question
    await api.dismissItem(selectedSpace.value, agentName, questionIndex, 'question')
    // 3. Nudge the agent to trigger a check-in so they read the message
    await api.broadcastAgent(selectedSpace.value, agentName)
    // 4. Reload space data
    await loadSpace(selectedSpace.value)
    spaceOverviewRef.value?.refreshInbox()
    showStatus(`Reply sent to ${agentName} — nudge triggered`)
  } catch (err) {
    console.error('Reply to question failed:', err)
    showError('Failed to reply to question. Please try again.')
  }
}

async function handleReplyToBlocker(agentName: string, blockerIndex: number, blockerText: string, replyText: string) {
  if (!selectedSpace.value) return
  try {
    // 1. Send as persistent message so agent sees it on next check-in
    await api.sendMessage(selectedSpace.value, agentName, `Re: [Blocker] ${blockerText}\n\n${replyText}`, 'Boss')
    // 2. Dismiss the blocker
    await api.dismissItem(selectedSpace.value, agentName, blockerIndex, 'blocker')
    // 3. Nudge the agent to trigger a check-in so they read the message
    await api.broadcastAgent(selectedSpace.value, agentName)
    // 4. Reload space data
    await loadSpace(selectedSpace.value)
    spaceOverviewRef.value?.refreshInbox()
    showStatus(`Reply sent to ${agentName} — nudge triggered`)
  } catch (err) {
    console.error('Reply to blocker failed:', err)
    showError('Failed to reply to blocker. Please try again.')
  }
}

// ── SSE event handlers ─────────────────────────────────────────────
function pushLog(type: string, msg: string) {
  eventLogRef.value?.pushSSEEvent(type, msg)
}

// Debounced full space reload — batches rapid SSE bursts into a single fetch.
// When 20+ agents all post simultaneously, this fires once after the burst settles.
let _spaceReloadTimer: ReturnType<typeof setTimeout> | null = null
function scheduleSpaceReload(space: string, delayMs = 300) {
  if (_spaceReloadTimer !== null) clearTimeout(_spaceReloadTimer)
  _spaceReloadTimer = setTimeout(() => {
    _spaceReloadTimer = null
    loadSpace(space)
  }, delayMs)
}

// Debounced spaces-list reload — the sidebar list only changes on space create/delete,
// not on every agent update. Coalesce multiple triggers into one fetch.
let _spacesReloadTimer: ReturnType<typeof setTimeout> | null = null
function scheduleSpacesReload(delayMs = 1000) {
  if (_spacesReloadTimer !== null) clearTimeout(_spacesReloadTimer)
  _spacesReloadTimer = setTimeout(() => {
    _spacesReloadTimer = null
    loadSpaces()
  }, delayMs)
}

function setupSSE() {
  sse.on('agent_updated', (data) => {
    // Patch agent in-place immediately for instant UI feedback — no HTTP round-trip.
    // SSE payload has status+summary; schedule a debounced full reload for
    // items/questions/blockers that aren't included in the SSE payload.
    if (currentSpace.value && currentSpace.value.name === data.space) {
      const agent = currentSpace.value.agents[data.agent]
      if (agent) {
        agent.status = data.status as AgentUpdate['status']
        agent.summary = data.summary
        agent.updated_at = new Date().toISOString()
        // Debounced full reload to pick up items, questions, blockers, etc.
        scheduleSpaceReload(data.space)
      } else {
        // New agent in this space — fetch immediately so it appears without delay
        loadSpace(data.space)
      }
      // Refresh hierarchy in case parent/children changed
      loadHierarchy(data.space)
    }
    // Update sidebar attention counts — debounced to avoid per-keystroke fetches
    scheduleSpacesReload()
    statusAnnouncement.value = `Agent ${data.agent} updated: ${data.status}`
    pushLog('agent_updated', `[${data.agent}] ${data.status}: ${data.summary}`)
  })

  sse.on('agent_removed', (data) => {
    // Remove agent in-place immediately for instant feedback
    if (currentSpace.value && currentSpace.value.name === data.space) {
      delete currentSpace.value.agents[data.agent]
      scheduleSpaceReload(data.space, 200)
    }
    // Agent removal changes space agent_count — refresh spaces list promptly
    scheduleSpacesReload(200)
    statusAnnouncement.value = `Agent ${data.agent} removed`
    pushLog('agent_removed', `[${data.agent}] agent removed`)
  })

  sse.on('space_deleted', (spaceName) => {
    loadSpaces()
    statusAnnouncement.value = `Space ${spaceName} deleted`
    pushLog('space_deleted', `space "${spaceName}" deleted`)
    if (selectedSpace.value === spaceName) {
      currentSpace.value = null
      router.push('/')
    }
  })

  sse.on('tmux_liveness', (data) => {
    if (Array.isArray(data)) {
      const map: Record<string, TmuxAgentStatus> = { ...tmuxStatus.value }
      for (const item of data) {
        if (item.agent) {
          map[item.agent] = item as TmuxAgentStatus
        }
      }
      tmuxStatus.value = map
    }
    // tmux_liveness is high-frequency, don't spam the log
  })

  sse.on('agent_message', (data) => {
    // Messages require a full reload since SSE doesn't carry message body content
    if (selectedSpace.value && selectedSpace.value === data.space) {
      loadSpace(selectedSpace.value)
    }
    pushLog('agent_message', `[${data.agent}] message from ${data.sender}`)
  })

  sse.on('broadcast_complete', () => {
    pushLog('broadcast_complete', 'Nudge completed')
  })

  sse.on('broadcast_progress', (data) => {
    pushLog('broadcast_progress', data.message || 'Nudge in progress...')
  })
}

// ── Polling fallback ───────────────────────────────────────────────
// SSE handles real-time updates. Polling is a reliability fallback only —
// 15s interval since SSE covers most updates. Skip polls when tab is hidden.
const POLL_INTERVAL_MS = 15000

function startPolling() {
  stopPolling()
  pollTimer = setInterval(() => {
    // No point fetching when the tab isn't visible
    if (document.hidden) return
    if (selectedSpace.value) {
      loadSpace(selectedSpace.value)
      loadTmuxStatus(selectedSpace.value)
    }
  }, POLL_INTERVAL_MS)
}

function stopPolling() {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

// ── Keyboard shortcuts ─────────────────────────────────────────────
const sortedAgentNames = computed<string[]>(() => {
  if (!currentSpace.value) return []
  return Object.keys(currentSpace.value.agents).sort((a, b) => a.localeCompare(b))
})

function isInputFocused(): boolean {
  const el = document.activeElement
  if (!el) return false
  const tag = el.tagName.toLowerCase()
  return tag === 'input' || tag === 'textarea' || (el as HTMLElement).isContentEditable
}

function handleKeydown(e: KeyboardEvent) {
  // Never intercept when typing in an input/textarea
  if (isInputFocused()) return

  // '?' — toggle help overlay
  if (e.key === '?') {
    e.preventDefault()
    if (!showHelpOverlay.value) {
      savedFocusEl.value = document.activeElement as HTMLElement | null
    }
    showHelpOverlay.value = !showHelpOverlay.value
    return
  }

  // Escape — close overlays or go back from agent detail to space overview
  if (e.key === 'Escape') {
    if (showHelpOverlay.value) {
      showHelpOverlay.value = false
      return
    }
    if (showMessageDialog.value) {
      showMessageDialog.value = false
      return
    }
    if (selectedAgent.value && selectedSpace.value) {
      router.push('/' + selectedSpace.value)
    }
    return
  }

  // '/' — focus search input if present
  if (e.key === '/') {
    const searchEl = document.querySelector<HTMLInputElement>('[data-search-focus]')
    if (searchEl) {
      e.preventDefault()
      searchEl.focus()
    }
    return
  }

  // 'i' — switch to inbox tab in space overview
  if (e.key === 'i') {
    if (!selectedSpace.value || selectedAgent.value) return
    e.preventDefault()
    spaceOverviewRef.value?.switchToInbox()
    return
  }

  // '[' / ']' — switch between spaces
  if (e.key === '[' || e.key === ']') {
    if (spaces.value.length === 0) return
    e.preventDefault()
    const currentIdx = spaces.value.findIndex(s => s.name === selectedSpace.value)
    let nextIdx: number
    if (e.key === ']') {
      nextIdx = currentIdx < spaces.value.length - 1 ? currentIdx + 1 : 0
    } else {
      nextIdx = currentIdx > 0 ? currentIdx - 1 : spaces.value.length - 1
    }
    const nextSpace = spaces.value[nextIdx]
    if (nextSpace) {
      router.push('/' + nextSpace.name)
    }
    return
  }

  // j/k — navigate between agents in the sidebar
  if (e.key === 'j' || e.key === 'k') {
    if (!selectedSpace.value || sortedAgentNames.value.length === 0) return
    e.preventDefault()
    const names = sortedAgentNames.value
    const currentIdx = selectedAgent.value ? names.indexOf(selectedAgent.value) : -1
    let nextIdx: number
    if (e.key === 'j') {
      nextIdx = currentIdx < names.length - 1 ? currentIdx + 1 : 0
    } else {
      nextIdx = currentIdx > 0 ? currentIdx - 1 : names.length - 1
    }
    const nextAgent = names[nextIdx]
    if (nextAgent) {
      router.push('/' + selectedSpace.value + '/' + nextAgent)
    }
    return
  }

  // 'n' — nudge the currently selected agent
  if (e.key === 'n') {
    if (!selectedAgent.value) return
    e.preventDefault()
    handleBroadcastAgent()
    return
  }

  // 'm' — open message dialog for current agent
  if (e.key === 'm') {
    if (!selectedAgent.value) return
    e.preventDefault()
    kbMessageText.value = ''
    kbMessageSender.value = 'boss'
    savedFocusEl.value = document.activeElement as HTMLElement | null
    showMessageDialog.value = true
    return
  }
}

async function handleKbSendMessage() {
  if (!kbMessageText.value.trim() || !selectedAgent.value || !selectedSpace.value) return
  kbMessageSending.value = true
  try {
    await handleSendMessage(kbMessageText.value.trim(), kbMessageSender.value || 'boss')
    showMessageDialog.value = false
    kbMessageText.value = ''
  } finally {
    kbMessageSending.value = false
  }
}

// ── Lifecycle ──────────────────────────────────────────────────────
onMounted(async () => {
  loading.value = true
  await loadSpaces()
  loading.value = false
  setupSSE()
  startPolling()
  document.addEventListener('keydown', handleKeydown)

  if (selectedSpace.value) {
    // Route already has a space — load its data and connect SSE
    loadSpace(selectedSpace.value, true)
    loadTmuxStatus(selectedSpace.value)
    sse.connect(selectedSpace.value)
  } else if (spaces.value.length > 0) {
    // No space in URL — redirect to first space
    router.replace('/' + spaces.value[0]!.name)
  } else {
    // No spaces at all — connect to global SSE to catch new spaces
    sse.connect()
  }
})

onUnmounted(() => {
  sse.disconnect()
  stopPolling()
  if (_spaceReloadTimer !== null) clearTimeout(_spaceReloadTimer)
  if (_spacesReloadTimer !== null) clearTimeout(_spacesReloadTimer)
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <TooltipProvider>
    <!-- Router view (hidden) — routes are used purely for URL sync -->
    <router-view v-slot="{ Component }">
      <component :is="Component" v-show="false" />
    </router-view>
    <SidebarProvider>
      <AppSidebar
        :spaces="spaces"
        :current-space="currentSpace"
        :selected-space="selectedSpace"
        :selected-agent="selectedAgent"
        :broadcasting="broadcasting"
        @select-space="handleSelectSpace"
        @select-agent="handleSelectAgent"
        @broadcast="handleBroadcastSpace"
        @delete-space="handleDeleteSpace"
        @create-space="handleCreateSpace"
      />
      <SidebarInset class="flex flex-col h-dvh">
        <!-- Header -->
        <header class="flex items-center gap-3 h-14 shrink-0 border-b px-4">
          <SidebarTrigger class="-ml-1" />
          <Separator orientation="vertical" class="h-5" />
          <nav aria-label="Breadcrumb" class="flex items-center gap-2 text-sm font-text">
            <button
              class="text-primary font-bold text-lg font-sans hover:text-primary/80 transition-colors cursor-pointer"
              aria-label="Navigate to home"
              @click="router.push('/')"
            >Agent Boss</button>
            <template v-if="selectedSpace">
              <span class="text-muted-foreground">/</span>
              <button
                :aria-label="`Navigate to ${selectedSpace} overview`"
                :aria-current="!selectedAgent ? 'page' : undefined"
                class="text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
                :class="{ 'text-foreground font-medium': !selectedAgent }"
                @click="router.push('/' + selectedSpace)"
              >
                {{ selectedSpace }}
              </button>
              <template v-if="selectedAgent && !showConversations">
                <span class="text-muted-foreground">/</span>
                <span class="text-foreground font-medium" aria-current="page">{{ selectedAgent }}</span>
              </template>
            </template>
          </nav>
          <!-- Space view tabs (Overview / Conversations) -->
          <template v-if="selectedSpace && !selectedAgentData">
            <Separator orientation="vertical" class="h-5 mx-1" />
            <nav class="flex items-center gap-1" aria-label="Space views">
              <button
                class="px-2.5 py-1 rounded text-xs font-medium transition-colors"
                :class="showConversations ? 'text-muted-foreground hover:text-foreground hover:bg-muted' : 'bg-muted text-foreground'"
                :aria-current="!showConversations ? 'page' : undefined"
                @click="router.push('/' + selectedSpace)"
              >Overview</button>
              <button
                class="px-2.5 py-1 rounded text-xs font-medium transition-colors"
                :class="showConversations ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground hover:bg-muted'"
                :aria-current="showConversations ? 'page' : undefined"
                @click="router.push('/' + selectedSpace + '/conversations')"
              >Conversations</button>
            </nav>
          </template>
          <!-- SSE connection indicator + theme toggle -->
          <div class="ml-auto flex items-center gap-3">
            <span
              :class="[
                'inline-block size-2 rounded-full',
                sse.connected.value ? 'bg-green-500' : 'bg-muted-foreground',
              ]"
              :title="sse.connected.value ? 'Live connection active' : 'Disconnected — reconnecting...'"
              :aria-label="sse.connected.value ? 'Live connection active' : 'Disconnected, reconnecting'"
              role="status"
            />
            <span class="text-xs text-muted-foreground font-text hidden sm:inline">
              {{ sse.connected.value ? 'Live' : 'Reconnecting...' }}
            </span>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Keyboard shortcuts (?)"
              title="Keyboard shortcuts (?)"
              @click="showHelpOverlay = !showHelpOverlay"
            >
              <Keyboard class="size-4" />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              :aria-label="theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'"
              :title="theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'"
              @click="toggleTheme"
            >
              <!-- Sun icon (shown in dark mode) -->
              <svg v-if="theme === 'dark'" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/></svg>
              <!-- Moon icon (shown in light mode) -->
              <svg v-else xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/></svg>
            </Button>
          </div>
        </header>

        <!-- Error toast -->
        <div
          v-if="errorMessage"
          role="alert"
          class="mx-4 mt-2 rounded-md border border-destructive/50 bg-destructive/10 px-4 py-2 text-sm text-destructive flex items-center justify-between gap-2"
        >
          <span>{{ errorMessage }}</span>
          <button
            class="text-destructive hover:text-foreground text-xs font-medium shrink-0"
            aria-label="Dismiss error"
            @click="errorMessage = null"
          >
            Dismiss
          </button>
        </div>

        <!-- Success toast -->
        <div
          v-if="successMessage"
          role="status"
          class="mx-4 mt-2 rounded-md border border-green-500/50 bg-green-500/10 px-4 py-2 text-sm text-green-600 dark:text-green-400 flex items-center justify-between gap-2"
        >
          <span>{{ successMessage }}</span>
          <button
            class="text-green-600 dark:text-green-400 hover:text-foreground text-xs font-medium shrink-0"
            aria-label="Dismiss notification"
            @click="successMessage = null"
          >
            Dismiss
          </button>
        </div>

        <!-- Screen reader announcements for live updates -->
        <div aria-live="polite" aria-atomic="true" class="sr-only">
          {{ statusAnnouncement }}
        </div>

        <!-- Main content -->
        <main class="flex-1 min-h-0 overflow-hidden" aria-label="Dashboard content">
          <!-- Initial load state -->
          <div v-if="loading" class="flex flex-col items-center justify-center h-full text-muted-foreground font-text gap-3">
            <div class="h-8 w-8 animate-spin rounded-full border-2 border-muted-foreground border-t-primary" role="status">
              <span class="sr-only">Loading...</span>
            </div>
            <p class="text-sm">Loading spaces...</p>
          </div>

          <!-- Space-switching loading state -->
          <div v-else-if="spaceLoading" class="flex flex-col items-center justify-center h-full text-muted-foreground font-text gap-3">
            <div class="h-8 w-8 animate-spin rounded-full border-2 border-muted-foreground border-t-primary" role="status">
              <span class="sr-only">Loading space...</span>
            </div>
            <p class="text-sm">Loading {{ selectedSpace }}…</p>
          </div>

          <!-- Conversations view -->
          <ConversationsView
            v-else-if="showConversations && currentSpace"
            :space="currentSpace"
          />

          <!-- Agent detail view -->
          <AgentDetail
            v-else-if="selectedAgentData && selectedAgent"
            :agent="selectedAgentData"
            :agent-name="selectedAgent"
            :space-name="selectedSpace"
            :tmux-status="selectedAgentTmux"
            @approve="handleApproveAgent"
            @reply="handleReplyAgent"
            @broadcast="handleBroadcastAgent"
            @delete="handleDeleteAgent()"
            @dismiss-question="handleDismissQuestion"
            @dismiss-blocker="handleDismissBlocker"
            @send-message="handleSendMessage"
            @reply-to-question="handleReplyToQuestion"
            @reply-to-blocker="handleReplyToBlocker"
            @select-agent="handleSelectAgent"
          />

          <!-- Space overview -->
          <SpaceOverview
            ref="spaceOverviewRef"
            v-else-if="currentSpace"
            :space="currentSpace"
            :tmux-status="tmuxStatus"
            :broadcasting="broadcasting"
            :hierarchy="effectiveHierarchy"
            @select-agent="handleSelectAgent"
            @broadcast="handleBroadcastSpace"
            @delete-agent="handleDeleteAgent"
            @broadcast-agent="handleBroadcastSingleAgent"
            @send-message-to-agent="handleSendMessageToAgent"
            @delete-space="handleDeleteSpace(selectedSpace)"
            @clear-done-agents="handleClearDoneAgents"
          />

          <!-- Empty state -->
          <div v-else class="flex flex-col items-center justify-center h-full text-muted-foreground font-text px-4 text-center">
            <div class="h-12 w-1 rounded-full bg-primary mb-4" aria-hidden="true" />
            <p class="text-lg font-sans font-semibold mb-1">Agent Boss</p>
            <p class="text-sm mb-4">Multi-agent coordination dashboard</p>
            <p v-if="spaces.length === 0" class="text-sm">
              No spaces found. Agents will create spaces automatically when they register.
            </p>
            <p v-else class="text-sm">
              Select a space from the sidebar to view agents.
            </p>
          </div>
        </main>

        <!-- Event Log panel -->
        <EventLog
          ref="eventLogRef"
          :space-name="selectedSpace"
          :agent-names="currentAgentNames"
        />
      </SidebarInset>
    </SidebarProvider>
    <!-- Keyboard shortcuts help overlay -->
    <Dialog :open="showHelpOverlay" @update:open="val => { showHelpOverlay = val; if (!val) restoreFocus() }">
      <DialogContent class="max-w-md">
        <DialogHeader>
          <DialogTitle>Keyboard Shortcuts</DialogTitle>
          <DialogDescription>Navigate the dashboard without lifting your hands from the keyboard.</DialogDescription>
        </DialogHeader>
        <div class="space-y-1 py-2 font-text text-sm">
          <div class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 items-center">
            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">?</kbd>
            <span>Show / hide this help overlay</span>

            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">j</kbd>
            <span>Select next agent in sidebar</span>

            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">k</kbd>
            <span>Select previous agent in sidebar</span>

            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">n</kbd>
            <span>Nudge currently selected agent</span>

            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">m</kbd>
            <span>Message currently selected agent</span>

            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">Esc</kbd>
            <span>Go back to space overview</span>

            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">/</kbd>
            <span>Focus search / filter input</span>

            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">i</kbd>
            <span>Switch to inbox tab (space overview)</span>

            <kbd class="px-2 py-0.5 rounded border bg-muted text-muted-foreground font-mono text-xs">[ ]</kbd>
            <span>Switch between spaces</span>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" @click="showHelpOverlay = false">Close</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Keyboard-triggered message dialog -->
    <Dialog :open="showMessageDialog" @update:open="val => { showMessageDialog = val; if (!val) restoreFocus() }">
      <DialogContent class="max-w-sm">
        <DialogHeader>
          <DialogTitle>Message {{ selectedAgent }}</DialogTitle>
          <DialogDescription>Send a message to this agent. It will appear in their Messages section on next check-in.</DialogDescription>
        </DialogHeader>
        <div class="space-y-3 py-1">
          <div class="space-y-1">
            <label for="kb-sender" class="text-xs font-medium text-muted-foreground">From</label>
            <Input
              id="kb-sender"
              v-model="kbMessageSender"
              type="text"
              placeholder="boss"
            />
          </div>
          <div class="space-y-1">
            <label for="kb-message" class="text-xs font-medium text-muted-foreground">Message</label>
            <Textarea
              id="kb-message"
              v-model="kbMessageText"
              :rows="4"
              placeholder="Type your message…"
              class="resize-none"
              @keydown.ctrl.enter.prevent="handleKbSendMessage"
            />
            <p class="text-xs text-muted-foreground">Ctrl+Enter to send</p>
          </div>
        </div>
        <DialogFooter class="gap-2">
          <Button variant="outline" size="sm" @click="showMessageDialog = false">Cancel</Button>
          <Button size="sm" :disabled="!kbMessageText.trim() || kbMessageSending" @click="handleKbSendMessage">
            {{ kbMessageSending ? 'Sending…' : 'Send' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </TooltipProvider>
</template>
