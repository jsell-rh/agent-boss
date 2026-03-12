<script setup lang="ts">
import type { Task, TaskStatus } from '@/types'
import { TASK_STATUS_LABELS } from '@/types'
import { ref } from 'vue'
import TaskCard from './TaskCard.vue'
import { LayoutList, Plus } from 'lucide-vue-next'

const props = defineProps<{
  status: TaskStatus
  tasks: Task[]
  allTasks?: Task[]
  draggingTaskId?: string | null
}>()

// For each top-level task in this column, look up its subtasks from allTasks.
function subtasksOf(task: Task): Task[] {
  if (!props.allTasks || !task.subtasks?.length) return []
  return task.subtasks
    .map(id => props.allTasks!.find(t => t.id === id))
    .filter((t): t is Task => t !== undefined)
}

const emit = defineEmits<{
  'task-click': [task: Task]
  'task-drop': [taskId: string, newStatus: TaskStatus]
  'task-drag-start': [task: Task]
  'create-in-column': [status: TaskStatus]
}>()

const isDragOver = ref(false)

function onDragOver(e: DragEvent) {
  e.preventDefault()
  isDragOver.value = true
}

function onDragLeave() {
  isDragOver.value = false
}

function onDrop(e: DragEvent) {
  e.preventDefault()
  isDragOver.value = false
  const taskId = e.dataTransfer?.getData('text/plain')
  if (taskId) {
    emit('task-drop', taskId, props.status)
  }
}

function onDragStart(e: DragEvent, task: Task) {
  e.dataTransfer?.setData('text/plain', task.id)
  emit('task-drag-start', task)
}

const statusHeaderClass: Record<TaskStatus, string> = {
  backlog: 'text-muted-foreground',
  in_progress: 'text-blue-600 dark:text-blue-400',
  review: 'text-purple-600 dark:text-purple-400',
  blocked: 'text-red-600 dark:text-red-400',
  done: 'text-teal-600 dark:text-teal-400',
}
</script>

<template>
  <div
    class="flex flex-col w-56 sm:w-64 shrink-0 rounded-lg bg-muted/40 border border-border transition-colors max-h-full"
    :class="{ 'ring-2 ring-primary/50 border-primary/50 bg-primary/5': isDragOver }"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <!-- Column header -->
    <div class="flex items-center justify-between px-3 py-2.5 border-b border-border">
      <span :class="['text-xs font-semibold uppercase tracking-wide', statusHeaderClass[status]]">
        {{ TASK_STATUS_LABELS[status] }}
      </span>
      <div class="flex items-center gap-1.5">
        <span class="text-[10px] font-mono text-muted-foreground bg-muted rounded-full px-1.5 py-0.5 min-w-5 text-center">
          {{ tasks.length }}
        </span>
        <button
          class="rounded p-0.5 text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
          title="Add task to this column"
          @click.stop="emit('create-in-column', status)"
        >
          <Plus class="size-3.5" />
        </button>
      </div>
    </div>

    <!-- Cards -->
    <div class="flex flex-col gap-2 p-2 flex-1 min-h-24 overflow-y-auto">
      <TransitionGroup name="kanban-card" tag="div" class="flex flex-col gap-2">
        <div v-for="task in tasks" :key="task.id" class="flex flex-col gap-1">
          <TaskCard
            :task="task"
            :dragging="draggingTaskId === task.id"
            @click="emit('task-click', task)"
            @dragstart="onDragStart"
          />
          <!-- Nested subtasks -->
          <div v-if="subtasksOf(task).length" class="flex flex-col gap-1 pl-3 border-l-2 border-border ml-1">
            <TaskCard
              v-for="sub in subtasksOf(task)"
              :key="sub.id"
              :task="sub"
              :dragging="draggingTaskId === sub.id"
              class="opacity-90 scale-[0.98] origin-left"
              @click="emit('task-click', sub)"
              @dragstart="onDragStart"
            />
          </div>
        </div>
      </TransitionGroup>
      <div
        v-if="tasks.length === 0"
        class="flex-1 flex flex-col items-center justify-center py-8 text-center gap-2 cursor-pointer select-none rounded-md hover:bg-muted/60 transition-colors"
        title="Double-click to add a task"
        @dblclick="emit('create-in-column', status)"
      >
        <div class="rounded-full bg-muted p-2.5">
          <LayoutList class="size-4 text-muted-foreground/50" aria-hidden="true" />
        </div>
        <p class="text-[11px] text-muted-foreground">No tasks</p>
        <p class="text-[10px] text-muted-foreground/50">Double-click to add</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Spring-drop: cards enter with a slight overshoot then settle — feels physical */
.kanban-card-enter-active {
  animation: kanban-spring-drop 0.35s cubic-bezier(0.22, 1, 0.36, 1);
}
@keyframes kanban-spring-drop {
  0%   { opacity: 0; transform: translateY(-14px) scale(0.95); }
  55%  { opacity: 1; transform: translateY(5px) scale(1.01); }
  78%  { transform: translateY(-2px) scale(0.998); }
  100% { opacity: 1; transform: translateY(0) scale(1); }
}
.kanban-card-leave-active {
  transition: all 0.2s ease;
}
.kanban-card-leave-to {
  opacity: 0;
  transform: translateY(6px) scale(0.97);
}
.kanban-card-move {
  transition: transform 0.3s ease;
}
@media (prefers-reduced-motion: reduce) {
  .kanban-card-enter-active { animation: none; }
}
</style>
