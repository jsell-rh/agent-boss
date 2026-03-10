<script setup lang="ts">
import type { Task } from '@/types'
import { TASK_PRIORITY_LABELS, TASK_PRIORITY_COLOR } from '@/types'
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import AgentAvatar from './AgentAvatar.vue'
import { MessageSquare, GitBranch, ChevronsUpDown, ListTree, Calendar, AlertTriangle } from 'lucide-vue-next'

const props = defineProps<{
  task: Task
  dragging?: boolean
}>()

const emit = defineEmits<{
  click: [task: Task]
  dragstart: [event: DragEvent, task: Task]
}>()

const priorityClass = computed(() =>
  props.task.priority ? TASK_PRIORITY_COLOR[props.task.priority] : '',
)

const priorityLabel = computed(() =>
  props.task.priority ? TASK_PRIORITY_LABELS[props.task.priority] : '',
)

const commentCount = computed(() => props.task.comments?.length ?? 0)

const dueDate = computed(() => props.task.due_at ? new Date(props.task.due_at) : null)

const isOverdue = computed(() => {
  if (!dueDate.value || props.task.status === 'done') return false
  return dueDate.value < new Date()
})

const isStale = computed(() => {
  if (props.task.status !== 'in_progress') return false
  const updatedAt = new Date(props.task.updated_at)
  return Date.now() - updatedAt.getTime() > 60 * 60 * 1000 // >1 hour
})

const isDueSoon = computed(() => {
  if (!dueDate.value || isOverdue.value || props.task.status === 'done') return false
  return (dueDate.value.getTime() - Date.now()) < 48 * 60 * 60 * 1000
})

const dueDateLabel = computed(() => {
  if (!dueDate.value) return null
  return dueDate.value.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
})

function onDragStart(e: DragEvent) {
  emit('dragstart', e, props.task)
}
</script>

<template>
  <div
    :id="task.id"
    class="group bg-card border rounded-lg p-3 cursor-pointer hover:border-primary/40 hover:shadow-sm transition-all select-none"
    :class="[
      dragging ? 'opacity-50 rotate-1 shadow-lg' : '',
      isStale ? 'border-amber-500/60 bg-amber-500/5' : 'border-border',
    ]"
    draggable="true"
    @click="emit('click', task)"
    @dragstart="onDragStart"
  >
    <!-- ID + Priority row -->
    <div class="flex items-center justify-between gap-2 mb-1.5">
      <div class="flex items-center gap-1.5">
        <span class="text-[10px] font-mono text-muted-foreground">{{ task.id }}</span>
        <span v-if="task.parent_task" class="flex items-center gap-0.5 text-[9px] text-muted-foreground/70" :title="`Subtask of ${task.parent_task}`">
          <ChevronsUpDown class="size-2.5" />
          {{ task.parent_task }}
        </span>
        <span v-if="isStale" class="flex items-center gap-0.5 text-[10px] text-amber-500 font-medium" title="No update for >1 hour">
          <AlertTriangle class="size-3" /> Stale
        </span>
      </div>
      <Badge v-if="task.priority" :class="['text-[10px] px-1.5 py-0 h-4', priorityClass]">
        {{ priorityLabel }}
      </Badge>
    </div>

    <!-- Title -->
    <p class="text-sm font-medium leading-snug line-clamp-2 mb-2">{{ task.title }}</p>

    <!-- Labels -->
    <div v-if="task.labels && task.labels.length" class="flex flex-wrap gap-1 mb-2">
      <Badge
        v-for="label in task.labels.slice(0, 3)"
        :key="label"
        variant="outline"
        class="text-[10px] px-1.5 py-0 h-4"
      >
        {{ label }}
      </Badge>
      <Badge v-if="task.labels.length > 3" variant="outline" class="text-[10px] px-1.5 py-0 h-4">
        +{{ task.labels.length - 3 }}
      </Badge>
    </div>

    <!-- Footer: assignee + branch + comments -->
    <div class="flex items-center gap-2 mt-1">
      <AgentAvatar v-if="task.assigned_to" :name="task.assigned_to" :size="16" class="shrink-0" />
      <span v-if="task.assigned_to" class="text-[10px] text-muted-foreground truncate flex-1">
        {{ task.assigned_to }}
      </span>
      <div v-else class="flex-1" />

      <div class="flex items-center gap-2 shrink-0">
        <span
          v-if="dueDateLabel"
          :class="[
            'flex items-center gap-0.5 text-[10px] font-medium',
            isOverdue ? 'text-destructive' : isDueSoon ? 'text-orange-500 dark:text-orange-400' : 'text-muted-foreground',
          ]"
          :title="isOverdue ? 'Overdue' : isDueSoon ? 'Due soon' : `Due ${dueDateLabel}`"
        >
          <Calendar class="size-3" />
          {{ dueDateLabel }}
        </span>
        <span v-if="task.subtasks && task.subtasks.length" class="flex items-center gap-0.5 text-[10px] text-muted-foreground" :title="`${task.subtasks.length} subtask(s)`">
          <ListTree class="size-3" />
          {{ task.subtasks.length }}
        </span>
        <span v-if="task.linked_branch" class="flex items-center gap-0.5 text-[10px] text-muted-foreground">
          <GitBranch class="size-3" />
        </span>
        <span v-if="commentCount > 0" class="flex items-center gap-0.5 text-[10px] text-muted-foreground">
          <MessageSquare class="size-3" />
          {{ commentCount }}
        </span>
      </div>
    </div>
  </div>
</template>
