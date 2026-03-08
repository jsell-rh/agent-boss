<script setup lang="ts">
import type { Task, TaskStatus } from '@/types'
import { TASK_STATUS_LABELS } from '@/types'
import { ref } from 'vue'
import TaskCard from './TaskCard.vue'

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
    :class="{ 'border-primary bg-primary/5': isDragOver }"
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
      <TaskCard
        v-for="task in tasks"
        :key="task.id"
        :task="task"
        :dragging="draggingTaskId === task.id"
        @click="emit('task-click', task)"
        @dragstart="onDragStart"
      />
      <div
        v-if="tasks.length === 0"
        class="flex-1 flex items-center justify-center text-[11px] text-muted-foreground py-6"
      >
        No tasks
      </div>
    </div>
  </div>
</template>
