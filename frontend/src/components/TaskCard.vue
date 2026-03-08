<script setup lang="ts">
import type { Task } from '@/types'
import { TASK_PRIORITY_LABELS, TASK_PRIORITY_COLOR } from '@/types'
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import AgentAvatar from './AgentAvatar.vue'
import { MessageSquare, GitBranch } from 'lucide-vue-next'

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

function onDragStart(e: DragEvent) {
  emit('dragstart', e, props.task)
}
</script>

<template>
  <div
    :id="task.id"
    class="group bg-card border border-border rounded-lg p-3 cursor-pointer hover:border-primary/40 hover:shadow-sm transition-all select-none"
    :class="{ 'opacity-50 rotate-1 shadow-lg': dragging }"
    draggable="true"
    @click="emit('click', task)"
    @dragstart="onDragStart"
  >
    <!-- ID + Priority row -->
    <div class="flex items-center justify-between gap-2 mb-1.5">
      <span class="text-[10px] font-mono text-muted-foreground">{{ task.id }}</span>
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

      <div class="flex items-center gap-2 text-muted-foreground shrink-0">
        <span v-if="task.linked_branch" class="flex items-center gap-0.5 text-[10px]">
          <GitBranch class="size-3" />
        </span>
        <span v-if="commentCount > 0" class="flex items-center gap-0.5 text-[10px]">
          <MessageSquare class="size-3" />
          {{ commentCount }}
        </span>
      </div>
    </div>
  </div>
</template>
