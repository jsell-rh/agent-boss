<script setup lang="ts">
import type { Task, TaskStatus, KnowledgeSpace } from '@/types'
import { TASK_STATUS_COLUMNS } from '@/types'
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { api } from '@/api/client'
import { useSSE } from '@/composables/useSSE'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ChevronDown, Plus, RefreshCw, Search, AlertCircle } from 'lucide-vue-next'
import KanbanColumn from './KanbanColumn.vue'
import TaskDetailPanel from './TaskDetailPanel.vue'
import NewTaskDialog from './NewTaskDialog.vue'

const props = defineProps<{
  space: KnowledgeSpace
}>()

// ── State ──────────────────────────────────────────────────────────
const tasks = ref<Task[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const draggingTaskId = ref<string | null>(null)

// ── Filters ────────────────────────────────────────────────────────
const filterAssignee = ref('')
const filterLabel = ref('')
const filterSearch = ref('')
const filterOverdueOnly = ref(false)

// ── Panel / Dialog ─────────────────────────────────────────────────
const selectedTask = ref<Task | null>(null)
const panelOpen = ref(false)
const newTaskOpen = ref(false)
const newTaskInitialStatus = ref<TaskStatus>('backlog')

// ── Computed: tasks grouped by column ─────────────────────────────
const allLabels = computed(() => {
  const s = new Set<string>()
  for (const t of tasks.value) {
    for (const l of t.labels ?? []) s.add(l)
  }
  return [...s].sort()
})

const now = new Date()

function isTaskOverdue(task: Task): boolean {
  if (!task.due_at || task.status === 'done') return false
  return new Date(task.due_at) < now
}

function dueSortKey(task: Task): number {
  if (!task.due_at) return Infinity
  return new Date(task.due_at).getTime()
}

const filteredTasks = computed(() => {
  const search = filterSearch.value.trim().toLowerCase()
  return tasks.value.filter(t => {
    if (filterAssignee.value && t.assigned_to !== filterAssignee.value) return false
    if (filterLabel.value && !t.labels?.includes(filterLabel.value)) return false
    if (filterOverdueOnly.value && !isTaskOverdue(t)) return false
    if (search) {
      const titleMatch = t.title.toLowerCase().includes(search)
      const idMatch = t.id.toLowerCase() === search
      if (!titleMatch && !idMatch) return false
    }
    return true
  })
})

const tasksByStatus = computed(() => {
  const groups: Record<TaskStatus, Task[]> = {
    backlog: [], in_progress: [], review: [], blocked: [], done: [],
  }
  // Only show top-level tasks in columns; subtasks appear nested under their parents.
  for (const t of filteredTasks.value) {
    if (!t.parent_task) {
      groups[t.status]?.push(t)
    }
  }
  // Sort each column: overdue tasks first (by due_at asc), then tasks with due dates, then no due date
  for (const col of Object.values(groups)) {
    col.sort((a, b) => dueSortKey(a) - dueSortKey(b))
  }
  return groups
})

// ── Data loading ───────────────────────────────────────────────────
async function loadTasks() {
  loading.value = true
  error.value = null
  try {
    tasks.value = await api.fetchTasks(props.space.name)
  } catch (e) {
    // Backend may not have tasks yet — treat as empty
    if (e instanceof Error && e.message.includes('404')) {
      tasks.value = []
    } else {
      error.value = e instanceof Error ? e.message : String(e)
    }
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await loadTasks()
  // Scroll to task anchor if URL hash is present (e.g. /kanban#TASK-001)
  // After scrolling, briefly highlight the card with a ring animation.
  if (window.location.hash) {
    const id = window.location.hash.slice(1)
    const el = document.getElementById(id)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' })
      // Wait for smooth scroll to finish, then flash a highlight ring
      setTimeout(() => {
        el.classList.add('task-deep-link-highlight')
        setTimeout(() => el.classList.remove('task-deep-link-highlight'), 1800)
      }, 600)
    }
  }
})
watch(() => props.space.name, () => {
  filterAssignee.value = ''
  filterLabel.value = ''
  filterSearch.value = ''
  filterOverdueOnly.value = false
  loadTasks()
})

// ── Drag and drop ──────────────────────────────────────────────────
async function onTaskDrop(taskId: string, newStatus: TaskStatus) {
  draggingTaskId.value = null
  const task = tasks.value.find(t => t.id === taskId)
  if (!task || task.status === newStatus) return

  // Optimistic update
  const oldStatus = task.status
  task.status = newStatus
  try {
    const updated = await api.moveTask(props.space.name, taskId, newStatus)
    Object.assign(task, updated)
  } catch {
    // Revert on error
    task.status = oldStatus
  }
}

// ── Task detail panel ──────────────────────────────────────────────
function openTask(task: Task) {
  selectedTask.value = task
  panelOpen.value = true
}

function onTaskUpdated(updated: Task) {
  const idx = tasks.value.findIndex(t => t.id === updated.id)
  if (idx >= 0) tasks.value[idx] = updated
  selectedTask.value = updated
}

function onTaskDeleted(id: string) {
  tasks.value = tasks.value.filter(t => t.id !== id)
  selectedTask.value = null
}

function onTaskCreated() {
  loadTasks()
}

function openNewTaskInColumn(status: TaskStatus) {
  newTaskInitialStatus.value = status
  newTaskOpen.value = true
}

function openTaskById(id: string) {
  const task = tasks.value.find(t => t.id === id)
  if (task) {
    selectedTask.value = task
    panelOpen.value = true
  }
}

// ── SSE integration: auto-reload on task_updated events ────────────
const sse = useSSE()
let sseReloadTimer: ReturnType<typeof setTimeout> | null = null

// Debounce SSE-triggered reloads to batch rapid bursts.
function scheduleSSEReload() {
  if (sseReloadTimer !== null) return
  sseReloadTimer = setTimeout(async () => {
    sseReloadTimer = null
    const fresh = await api.fetchTasks(props.space.name).catch(() => null)
    if (fresh === null) return
    // Merge fresh data: update status in place so TransitionGroup animates moves.
    for (const t of fresh) {
      const idx = tasks.value.findIndex(x => x.id === t.id)
      if (idx >= 0) {
        Object.assign(tasks.value[idx] as object, t)
      } else {
        tasks.value.push(t)
      }
    }
    // Remove tasks that are no longer present.
    const freshIds = new Set(fresh.map(t => t.id))
    tasks.value = tasks.value.filter(t => freshIds.has(t.id))
    // Keep selected task in sync.
    if (selectedTask.value) {
      const updated = tasks.value.find(t => t.id === selectedTask.value!.id)
      if (updated) selectedTask.value = updated
    }
  }, 300)
}

const unsubTaskUpdated = sse.on('task_updated', (data) => {
  if (data.space !== props.space.name) return
  if (data.deleted) {
    tasks.value = tasks.value.filter(t => t.id !== data.id)
    if (selectedTask.value?.id === data.id) {
      selectedTask.value = null
      panelOpen.value = false
    }
    return
  }
  scheduleSSEReload()
})

onUnmounted(() => {
  unsubTaskUpdated()
  if (sseReloadTimer !== null) clearTimeout(sseReloadTimer)
})
</script>

<template>
  <div class="flex flex-col h-full overflow-hidden">
    <!-- Toolbar -->
    <div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 sm:px-6 py-2 sm:py-3 border-b border-border shrink-0">
      <h2 class="text-sm font-semibold">Kanban Board</h2>
      <span class="text-xs text-muted-foreground">{{ filteredTasks.length }} task{{ filteredTasks.length !== 1 ? 's' : '' }}</span>

      <!-- Search input -->
      <div class="relative flex items-center">
        <Search class="absolute left-2 size-3 text-muted-foreground pointer-events-none" />
        <input
          v-model="filterSearch"
          type="search"
          placeholder="Search tasks…"
          class="pl-6 pr-2 h-7 text-xs border border-border rounded bg-background outline-none focus:border-primary w-32 sm:w-40"
        />
      </div>

      <div class="flex items-center gap-2 ml-auto flex-wrap">
        <!-- Overdue filter -->
        <Button
          variant="outline"
          size="sm"
          :class="['h-7 text-xs gap-1', filterOverdueOnly ? 'border-destructive text-destructive' : '']"
          @click="filterOverdueOnly = !filterOverdueOnly"
        >
          <AlertCircle class="size-3" />
          Overdue
        </Button>

        <!-- Filter by assignee -->
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button variant="outline" size="sm" class="h-7 text-xs gap-1">
              {{ filterAssignee || 'All agents' }}
              <ChevronDown class="size-3" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuItem :class="{ 'font-semibold': !filterAssignee }" @click="filterAssignee = ''">
              All agents
            </DropdownMenuItem>
            <DropdownMenuItem
              v-for="agent in Object.keys(space.agents)"
              :key="agent"
              :class="{ 'font-semibold': filterAssignee === agent }"
              @click="filterAssignee = agent"
            >
              {{ agent }}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <!-- Filter by label -->
        <DropdownMenu v-if="allLabels.length > 0">
          <DropdownMenuTrigger as-child>
            <Button variant="outline" size="sm" class="h-7 text-xs gap-1">
              {{ filterLabel || 'All labels' }}
              <ChevronDown class="size-3" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuItem :class="{ 'font-semibold': !filterLabel }" @click="filterLabel = ''">
              All labels
            </DropdownMenuItem>
            <DropdownMenuItem
              v-for="label in allLabels"
              :key="label"
              :class="{ 'font-semibold': filterLabel === label }"
              @click="filterLabel = label"
            >
              {{ label }}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <Button variant="ghost" size="sm" class="h-7 w-7 p-0" :disabled="loading" @click="loadTasks">
          <RefreshCw :class="['size-3.5', loading && 'animate-spin']" />
        </Button>

        <Button size="sm" class="h-7 text-xs gap-1" @click="newTaskInitialStatus = 'backlog'; newTaskOpen = true">
          <Plus class="size-3.5" />
          New Task
        </Button>
      </div>
    </div>

    <!-- Error state -->
    <div v-if="error" class="px-6 py-3 bg-destructive/10 text-destructive text-sm border-b border-border">
      {{ error }}
    </div>

    <!-- Loading skeleton -->
    <div v-if="loading && tasks.length === 0" class="flex items-center justify-center flex-1 text-muted-foreground text-sm">
      Loading tasks…
    </div>

    <!-- Board -->
    <div
      v-else
      class="flex gap-3 p-3 sm:p-4 overflow-x-auto overflow-y-hidden flex-1 scroll-smooth"
      @dragend="draggingTaskId = null"
    >
      <KanbanColumn
        v-for="status in TASK_STATUS_COLUMNS"
        :key="status"
        :status="status"
        :tasks="tasksByStatus[status]"
        :all-tasks="tasks"
        :dragging-task-id="draggingTaskId"
        @task-click="openTask"
        @task-drop="onTaskDrop"
        @task-drag-start="t => draggingTaskId = t.id"
        @create-in-column="openNewTaskInColumn"
      />
    </div>

    <!-- Task detail sheet -->
    <TaskDetailPanel
      :task="selectedTask"
      :space="space"
      :open="panelOpen"
      :all-tasks="tasks"
      @update:open="panelOpen = $event"
      @task-updated="onTaskUpdated"
      @task-deleted="onTaskDeleted"
      @open-task="openTaskById"
    />

    <!-- New task dialog -->
    <NewTaskDialog
      :open="newTaskOpen"
      :space="space"
      :tasks="tasks"
      :initial-status="newTaskInitialStatus"
      @update:open="newTaskOpen = $event"
      @created="onTaskCreated"
    />
  </div>
</template>

<style scoped>
@keyframes deep-link-flash {
  0%   { box-shadow: none; outline: none; }
  15%  { box-shadow: 0 0 0 3px color-mix(in oklch, var(--color-primary) 80%, transparent); outline: 2px solid color-mix(in oklch, var(--color-primary) 50%, transparent); }
  60%  { box-shadow: 0 0 0 3px color-mix(in oklch, var(--color-primary) 40%, transparent); outline: 2px solid color-mix(in oklch, var(--color-primary) 20%, transparent); }
  100% { box-shadow: none; outline: none; }
}

:global(.task-deep-link-highlight) {
  animation: deep-link-flash 1.8s ease-out forwards;
  border-radius: 0.5rem;
}
</style>
