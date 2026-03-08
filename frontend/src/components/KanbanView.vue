<script setup lang="ts">
import type { Task, TaskStatus, KnowledgeSpace } from '@/types'
import { TASK_STATUS_COLUMNS } from '@/types'
import { ref, computed, onMounted, watch } from 'vue'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ChevronDown, Plus, RefreshCw } from 'lucide-vue-next'
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

// ── Panel / Dialog ─────────────────────────────────────────────────
const selectedTask = ref<Task | null>(null)
const panelOpen = ref(false)
const newTaskOpen = ref(false)

// ── Computed: tasks grouped by column ─────────────────────────────
const allLabels = computed(() => {
  const s = new Set<string>()
  for (const t of tasks.value) {
    for (const l of t.labels ?? []) s.add(l)
  }
  return [...s].sort()
})

const filteredTasks = computed(() => {
  return tasks.value.filter(t => {
    if (filterAssignee.value && t.assigned_to !== filterAssignee.value) return false
    if (filterLabel.value && !t.labels?.includes(filterLabel.value)) return false
    return true
  })
})

const tasksByStatus = computed(() => {
  const groups: Record<TaskStatus, Task[]> = {
    backlog: [], in_progress: [], review: [], blocked: [], done: [],
  }
  for (const t of filteredTasks.value) {
    groups[t.status]?.push(t)
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
  if (window.location.hash) {
    const id = window.location.hash.slice(1)
    const el = document.getElementById(id)
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }
})
watch(() => props.space.name, () => {
  filterAssignee.value = ''
  filterLabel.value = ''
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

// ── SSE integration (listen for task_updated events) ───────────────
// The parent App.vue manages the SSE stream; we watch for space changes
// and refresh. Full SSE integration would be wired through a composable.
</script>

<template>
  <div class="flex flex-col h-full overflow-hidden">
    <!-- Toolbar -->
    <div class="flex items-center gap-3 px-6 py-3 border-b border-border shrink-0">
      <h2 class="text-sm font-semibold">Kanban Board</h2>
      <span class="text-xs text-muted-foreground">{{ filteredTasks.length }} task{{ filteredTasks.length !== 1 ? 's' : '' }}</span>

      <div class="flex items-center gap-2 ml-auto">
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

        <Button size="sm" class="h-7 text-xs gap-1" @click="newTaskOpen = true">
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
      class="flex gap-3 p-4 overflow-x-auto overflow-y-hidden flex-1"
      @dragend="draggingTaskId = null"
    >
      <KanbanColumn
        v-for="status in TASK_STATUS_COLUMNS"
        :key="status"
        :status="status"
        :tasks="tasksByStatus[status]"
        :dragging-task-id="draggingTaskId"
        @task-click="openTask"
        @task-drop="onTaskDrop"
        @task-drag-start="t => draggingTaskId = t.id"
      />
    </div>

    <!-- Task detail sheet -->
    <TaskDetailPanel
      :task="selectedTask"
      :space="space"
      :open="panelOpen"
      @update:open="panelOpen = $event"
      @task-updated="onTaskUpdated"
      @task-deleted="onTaskDeleted"
    />

    <!-- New task dialog -->
    <NewTaskDialog
      :open="newTaskOpen"
      :space="space"
      @update:open="newTaskOpen = $event"
      @created="onTaskCreated"
    />
  </div>
</template>
