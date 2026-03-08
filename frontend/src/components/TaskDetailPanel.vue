<script setup lang="ts">
import type { Task, TaskStatus, TaskPriority, KnowledgeSpace } from '@/types'
import {
  TASK_STATUS_LABELS,
  TASK_PRIORITY_LABELS,
  TASK_PRIORITY_COLOR,
  TASK_STATUS_COLUMNS,
} from '@/types'
import { ref, watch, computed } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Textarea } from '@/components/ui/textarea'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Separator } from '@/components/ui/separator'
import AgentAvatar from './AgentAvatar.vue'
import { GitBranch, ExternalLink, ChevronDown, Trash2, Send } from 'lucide-vue-next'
import { relativeTime } from '@/composables/useTime'

const props = defineProps<{
  task: Task | null
  space: KnowledgeSpace
  open: boolean
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  'task-updated': [task: Task]
  'task-deleted': [id: string]
}>()

const router = useRouter()
const commentText = ref('')
const submittingComment = ref(false)
const saving = ref(false)
const editingTitle = ref(false)
const localTitle = ref('')

watch(() => props.task, (t) => {
  if (t) localTitle.value = t.title
  editingTitle.value = false
})

const agentNames = computed(() => Object.keys(props.space.agents))

function buildPrUrl(pr: string): string | null {
  if (pr.startsWith('http')) return pr
  return null
}

async function moveTask(status: TaskStatus) {
  if (!props.task) return
  saving.value = true
  try {
    const updated = await api.moveTask(props.space.name, props.task.id, status)
    emit('task-updated', updated)
  } finally {
    saving.value = false
  }
}

async function setPriority(priority: TaskPriority) {
  if (!props.task) return
  saving.value = true
  try {
    const updated = await api.updateTask(props.space.name, props.task.id, { priority })
    emit('task-updated', updated)
  } finally {
    saving.value = false
  }
}

async function assignTask(agent: string) {
  if (!props.task) return
  saving.value = true
  try {
    const updated = await api.assignTask(props.space.name, props.task.id, agent)
    emit('task-updated', updated)
  } finally {
    saving.value = false
  }
}

async function saveTitle() {
  if (!props.task || !localTitle.value.trim()) return
  editingTitle.value = false
  if (localTitle.value.trim() === props.task.title) return
  saving.value = true
  try {
    const updated = await api.updateTask(props.space.name, props.task.id, { title: localTitle.value.trim() })
    emit('task-updated', updated)
  } finally {
    saving.value = false
  }
}

async function submitComment() {
  if (!props.task || !commentText.value.trim()) return
  submittingComment.value = true
  try {
    const updated = await api.addTaskComment(props.space.name, props.task.id, commentText.value.trim())
    commentText.value = ''
    emit('task-updated', updated)
  } finally {
    submittingComment.value = false
  }
}

async function deleteTask() {
  if (!props.task) return
  if (!confirm(`Delete task ${props.task.id}?`)) return
  await api.deleteTask(props.space.name, props.task.id)
  emit('task-deleted', props.task.id)
  emit('update:open', false)
}
</script>

<template>
  <Sheet :open="open" @update:open="emit('update:open', $event)">
    <SheetContent class="w-[480px] sm:w-[540px] overflow-y-auto flex flex-col gap-0 p-0">
      <div v-if="task" class="flex flex-col h-full">
        <!-- Header -->
        <SheetHeader class="px-6 pt-6 pb-4 border-b border-border">
          <div class="flex items-start justify-between gap-3">
            <div class="flex-1 min-w-0">
              <span class="text-[11px] font-mono text-muted-foreground">{{ task.id }}</span>
              <!-- Editable title -->
              <input
                v-if="editingTitle"
                v-model="localTitle"
                class="w-full text-base font-semibold bg-transparent border-b border-primary outline-none py-0.5 mt-0.5"
                autofocus
                @blur="saveTitle"
                @keydown.enter="saveTitle"
                @keydown.escape="editingTitle = false"
              />
              <SheetTitle
                v-else
                class="text-base font-semibold leading-snug cursor-text mt-0.5"
                @click="editingTitle = true"
              >
                {{ task.title }}
              </SheetTitle>
            </div>
            <Button variant="ghost" size="sm" class="text-destructive hover:text-destructive shrink-0" @click="deleteTask">
              <Trash2 class="size-4" />
            </Button>
          </div>
        </SheetHeader>

        <!-- Body -->
        <div class="flex-1 overflow-y-auto px-6 py-4 flex flex-col gap-5">
          <!-- Status + Priority row -->
          <div class="flex flex-wrap gap-3">
            <!-- Status -->
            <div class="flex flex-col gap-1">
              <span class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">Status</span>
              <DropdownMenu>
                <DropdownMenuTrigger as-child>
                  <Button variant="outline" size="sm" class="h-7 gap-1 text-xs" :disabled="saving">
                    {{ TASK_STATUS_LABELS[task.status] }}
                    <ChevronDown class="size-3" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent>
                  <DropdownMenuItem
                    v-for="s in TASK_STATUS_COLUMNS"
                    :key="s"
                    :class="{ 'font-semibold': s === task.status }"
                    @click="moveTask(s)"
                  >
                    {{ TASK_STATUS_LABELS[s] }}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>

            <!-- Priority -->
            <div class="flex flex-col gap-1">
              <span class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">Priority</span>
              <DropdownMenu>
                <DropdownMenuTrigger as-child>
                  <Button variant="outline" size="sm" class="h-7 gap-1 text-xs" :disabled="saving">
                    <span v-if="task.priority">{{ TASK_PRIORITY_LABELS[task.priority] }}</span>
                    <span v-else class="text-muted-foreground">None</span>
                    <ChevronDown class="size-3" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent>
                  <DropdownMenuItem
                    v-for="(label, p) in TASK_PRIORITY_LABELS"
                    :key="p"
                    :class="{ 'font-semibold': p === task.priority }"
                    @click="setPriority(p as TaskPriority)"
                  >
                    <Badge :class="['text-[10px] mr-2', TASK_PRIORITY_COLOR[p as TaskPriority]]">{{ label }}</Badge>
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>

            <!-- Assignee -->
            <div class="flex flex-col gap-1">
              <span class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">Assignee</span>
              <div class="flex items-center gap-1">
                <button
                  v-if="task.assigned_to"
                  class="flex items-center gap-1 text-xs text-primary hover:underline"
                  :title="`View ${task.assigned_to} details`"
                  @click="router.push(`/${encodeURIComponent(space.name)}/${encodeURIComponent(task.assigned_to)}`)"
                >
                  <AgentAvatar :name="task.assigned_to" :size="14" />
                  {{ task.assigned_to }}
                </button>
              <DropdownMenu>
                <DropdownMenuTrigger as-child>
                  <Button variant="outline" size="sm" class="h-7 gap-1 text-xs" :disabled="saving">
                    <span v-if="!task.assigned_to" class="text-muted-foreground">Unassigned</span>
                    <ChevronDown class="size-3" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent>
                  <DropdownMenuItem @click="assignTask('')">
                    <span class="text-muted-foreground">Unassigned</span>
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    v-for="agent in agentNames"
                    :key="agent"
                    :class="{ 'font-semibold': agent === task.assigned_to }"
                    @click="assignTask(agent)"
                  >
                    <AgentAvatar :name="agent" :size="14" class="mr-2" />
                    {{ agent }}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
              </div>
            </div>
          </div>

          <Separator />

          <!-- Description -->
          <div v-if="task.description" class="flex flex-col gap-1.5">
            <span class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">Description</span>
            <p class="text-sm text-foreground/80 whitespace-pre-wrap">{{ task.description }}</p>
          </div>

          <!-- Labels -->
          <div v-if="task.labels && task.labels.length" class="flex flex-col gap-1.5">
            <span class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">Labels</span>
            <div class="flex flex-wrap gap-1.5">
              <Badge v-for="label in task.labels" :key="label" variant="outline" class="text-xs">
                {{ label }}
              </Badge>
            </div>
          </div>

          <!-- Links -->
          <div v-if="task.linked_branch || task.linked_pr" class="flex flex-col gap-1.5">
            <span class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">Links</span>
            <div class="flex flex-col gap-1">
              <div v-if="task.linked_branch" class="flex items-center gap-1.5 text-sm">
                <GitBranch class="size-3.5 text-muted-foreground" />
                <code class="text-xs bg-muted px-1.5 py-0.5 rounded">{{ task.linked_branch }}</code>
              </div>
              <a
                v-if="task.linked_pr"
                :href="buildPrUrl(task.linked_pr) ?? '#'"
                target="_blank"
                rel="noopener noreferrer"
                class="flex items-center gap-1.5 text-sm text-primary hover:underline"
              >
                <ExternalLink class="size-3.5" />
                {{ task.linked_pr }}
              </a>
            </div>
          </div>

          <Separator />

          <!-- Comments -->
          <div class="flex flex-col gap-3">
            <span class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">
              Comments ({{ task.comments?.length ?? 0 }})
            </span>
            <div v-if="task.comments && task.comments.length" class="flex flex-col gap-3">
              <div
                v-for="comment in task.comments"
                :key="comment.id"
                class="flex gap-2.5"
              >
                <AgentAvatar :name="comment.author" :size="22" class="shrink-0 mt-0.5" />
                <div class="flex flex-col gap-0.5 min-w-0">
                  <div class="flex items-baseline gap-2">
                    <span class="text-xs font-medium">{{ comment.author }}</span>
                    <span class="text-[10px] text-muted-foreground">{{ relativeTime(comment.created_at) }}</span>
                  </div>
                  <p class="text-sm text-foreground/80 whitespace-pre-wrap">{{ comment.body }}</p>
                </div>
              </div>
            </div>
            <div v-else class="text-xs text-muted-foreground">No comments yet</div>

            <!-- Add comment -->
            <div class="flex gap-2 mt-1">
              <Textarea
                v-model="commentText"
                placeholder="Add a comment…"
                class="text-sm min-h-[60px] resize-none flex-1"
                @keydown.ctrl.enter="submitComment"
              />
              <Button
                size="sm"
                :disabled="!commentText.trim() || submittingComment"
                class="self-end shrink-0"
                @click="submitComment"
              >
                <Send class="size-3.5" />
              </Button>
            </div>
          </div>

          <!-- Metadata footer -->
          <Separator />
          <div class="text-[10px] text-muted-foreground flex flex-col gap-0.5 pb-2">
            <div>Created by <span class="font-medium">{{ task.created_by }}</span> · {{ relativeTime(task.created_at) }}</div>
            <div>Updated {{ relativeTime(task.updated_at) }}</div>
          </div>
        </div>
      </div>
    </SheetContent>
  </Sheet>
</template>
