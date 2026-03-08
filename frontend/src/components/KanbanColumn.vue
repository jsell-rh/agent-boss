<script setup lang="ts">
import type { Task, TaskStatus } from '@/types'
import { TASK_STATUS_LABELS } from '@/types'
import { ref } from 'vue'
import TaskCard from './TaskCard.vue'
import { LayoutList } from 'lucide-vue-next'

const props = defineProps<{
  status: TaskStatus
  tasks: Task[]
  draggingTaskId?: string | null
}>()

const emit = defineEmits<{
  'task-click': [task: Task]
  'task-drop': [taskId: string, newStatus: TaskStatus]
  'task-drag-start': [task: Task]
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
    class="flex flex-col w-64 shrink-0 rounded-lg bg-muted/40 border border-border transition-colors max-h-full"
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
      <span class="text-[10px] font-mono text-muted-foreground bg-muted rounded-full px-1.5 py-0.5 min-w-5 text-center">
        {{ tasks.length }}
      </span>
    </div>

    <!-- Cards -->
    <div class="flex flex-col gap-2 p-2 flex-1 min-h-24 overflow-y-auto">
      <TransitionGroup name="kanban-card" tag="div" class="flex flex-col gap-2">
        <TaskCard
          v-for="task in tasks"
          :key="task.id"
          :task="task"
          :dragging="draggingTaskId === task.id"
          @click="emit('task-click', task)"
          @dragstart="onDragStart"
        />
      </TransitionGroup>
      <div
        v-if="tasks.length === 0"
        class="flex-1 flex flex-col items-center justify-center py-8 text-center gap-2"
      >
        <div class="rounded-full bg-muted p-2.5">
          <LayoutList class="size-4 text-muted-foreground/50" aria-hidden="true" />
        </div>
        <p class="text-[11px] text-muted-foreground">No tasks</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.kanban-card-enter-active,
.kanban-card-leave-active {
  transition: all 0.25s ease;
}
.kanban-card-enter-from {
  opacity: 0;
  transform: translateY(-8px) scale(0.97);
}
.kanban-card-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.97);
}
.kanban-card-move {
  transition: transform 0.3s ease;
}
</style>
