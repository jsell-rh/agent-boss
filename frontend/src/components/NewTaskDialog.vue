<script setup lang="ts">
import type { KnowledgeSpace, Task, TaskPriority, TaskStatus } from '@/types'
import { TASK_PRIORITY_LABELS, TASK_STATUS_LABELS } from '@/types'
import { ref } from 'vue'
import { api } from '@/api/client'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ChevronDown } from 'lucide-vue-next'
import AgentAvatar from './AgentAvatar.vue'

const props = defineProps<{
  open: boolean
  space: KnowledgeSpace
  tasks?: Task[]
  parentTaskId?: string
  initialAssignee?: string
  initialStatus?: TaskStatus
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  created: []
}>()

const title = ref('')
const description = ref('')
const status = ref<TaskStatus>(props.initialStatus ?? 'backlog')
const priority = ref<TaskPriority>('medium')
const assignedTo = ref(props.initialAssignee ?? '')
const parentTask = ref(props.parentTaskId ?? '')
const dueDate = ref('')
const submitting = ref(false)

function reset() {
  title.value = ''
  description.value = ''
  status.value = props.initialStatus ?? 'backlog'
  priority.value = 'medium'
  assignedTo.value = props.initialAssignee ?? ''
  parentTask.value = props.parentTaskId ?? ''
  dueDate.value = ''
}

async function submit() {
  if (!title.value.trim()) return
  submitting.value = true
  try {
    await api.createTask(props.space.name, {
      title: title.value.trim(),
      description: description.value.trim() || undefined,
      status: status.value,
      priority: priority.value,
      assigned_to: assignedTo.value || undefined,
      parent_task: parentTask.value || undefined,
      due_at: dueDate.value ? new Date(dueDate.value + 'T00:00:00Z').toISOString() : undefined,
    })
    reset()
    emit('created')
    emit('update:open', false)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="sm:max-w-[480px]">
      <DialogHeader>
        <DialogTitle>New Task</DialogTitle>
        <DialogDescription>Create a task in {{ space.name }}</DialogDescription>
      </DialogHeader>

      <form class="flex flex-col gap-4 py-2" @submit.prevent="submit">
        <!-- Title -->
        <div class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Title *</label>
          <Input
            v-model="title"
            placeholder="Task title"
            autofocus
            class="text-sm"
          />
        </div>

        <!-- Description -->
        <div class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Description</label>
          <Textarea
            v-model="description"
            placeholder="Optional description (markdown supported)"
            class="text-sm min-h-[80px] resize-none"
          />
        </div>

        <!-- Status -->
        <div class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Status</label>
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button variant="outline" size="sm" class="w-full justify-between text-xs h-8">
                {{ TASK_STATUS_LABELS[status] }}
                <ChevronDown class="size-3 ml-1" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem
                v-for="(label, s) in TASK_STATUS_LABELS"
                :key="s"
                :class="{ 'font-semibold': s === status }"
                @click="status = s as TaskStatus"
              >
                {{ label }}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <!-- Priority + Assignee row -->
        <div class="flex gap-3">
          <!-- Priority -->
          <div class="flex flex-col gap-1.5 flex-1">
            <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Priority</label>
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <Button variant="outline" size="sm" class="w-full justify-between text-xs h-8">
                  {{ TASK_PRIORITY_LABELS[priority] }}
                  <ChevronDown class="size-3 ml-1" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuItem
                  v-for="(label, p) in TASK_PRIORITY_LABELS"
                  :key="p"
                  :class="{ 'font-semibold': p === priority }"
                  @click="priority = p as TaskPriority"
                >
                  {{ label }}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>

          <!-- Assignee -->
          <div class="flex flex-col gap-1.5 flex-1">
            <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Assign To</label>
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <Button variant="outline" size="sm" class="w-full justify-between text-xs h-8 gap-1">
                  <span v-if="assignedTo" class="flex items-center gap-1.5 truncate">
                    <AgentAvatar :name="assignedTo" :size="14" />
                    {{ assignedTo }}
                  </span>
                  <span v-else class="text-muted-foreground">Unassigned</span>
                  <ChevronDown class="size-3 ml-1 shrink-0" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuItem @click="assignedTo = ''">
                  <span class="text-muted-foreground">Unassigned</span>
                </DropdownMenuItem>
                <DropdownMenuItem
                  v-for="agent in Object.keys(space.agents)"
                  :key="agent"
                  @click="assignedTo = agent"
                >
                  <AgentAvatar :name="agent" :size="14" class="mr-2" />
                  {{ agent }}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>

        <!-- Due Date (optional) -->
        <div class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Due Date (optional)</label>
          <input
            v-model="dueDate"
            type="date"
            class="text-sm h-8 px-2 border border-border rounded bg-background outline-none focus:border-primary w-full"
          />
        </div>

        <!-- Parent Task (optional) -->
        <div v-if="tasks && tasks.length" class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Parent Task (optional)</label>
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button variant="outline" size="sm" class="w-full justify-between text-xs h-8 gap-1">
                <span v-if="parentTask" class="flex items-center gap-1.5 truncate">
                  <span class="font-mono text-[10px] text-muted-foreground">{{ parentTask }}</span>
                  <span class="truncate">{{ tasks.find(t => t.id === parentTask)?.title }}</span>
                </span>
                <span v-else class="text-muted-foreground">None</span>
                <ChevronDown class="size-3 ml-1 shrink-0" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent class="max-h-48 overflow-y-auto">
              <DropdownMenuItem @click="parentTask = ''">
                <span class="text-muted-foreground">None</span>
              </DropdownMenuItem>
              <DropdownMenuItem
                v-for="t in tasks"
                :key="t.id"
                :class="{ 'font-semibold': t.id === parentTask }"
                @click="parentTask = t.id"
              >
                <span class="font-mono text-[10px] text-muted-foreground mr-1.5">{{ t.id }}</span>
                <span class="truncate">{{ t.title }}</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" @click="emit('update:open', false)">Cancel</Button>
          <Button type="submit" :disabled="!title.trim() || submitting">
            {{ submitting ? 'Creating…' : 'Create Task' }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
