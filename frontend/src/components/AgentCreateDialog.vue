<script setup lang="ts">
import type { Persona } from '@/types'
import { ref, computed, onMounted } from 'vue'
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

const props = defineProps<{
  open: boolean
  space: string
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  created: [agentName: string]
  'open-personas': []
}>()

const agentName = ref('')
const workDir = ref('')
const model = ref('')
const reposText = ref('')
const taskPrompt = ref('')
const initialMessage = ref('')
const backend = ref<'tmux' | 'ambient'>('tmux')
const submitting = ref(false)
const errorMsg = ref('')

// Persona selection
const personas = ref<Persona[]>([])
const selectedPersonaIds = ref<string[]>([])

const isTmux = computed(() => backend.value === 'tmux')
const isAmbient = computed(() => backend.value === 'ambient')

function togglePersona(id: string) {
  const idx = selectedPersonaIds.value.indexOf(id)
  if (idx >= 0) {
    selectedPersonaIds.value.splice(idx, 1)
  } else {
    selectedPersonaIds.value.push(id)
  }
}

function reset() {
  agentName.value = ''
  workDir.value = ''
  model.value = ''
  reposText.value = ''
  taskPrompt.value = ''
  initialMessage.value = ''
  backend.value = 'tmux'
  selectedPersonaIds.value = []
  errorMsg.value = ''
}

onMounted(async () => {
  try {
    personas.value = await api.fetchPersonas()
  } catch {
    // personas unavailable — selector simply hidden
  }
})

function parseRepos(text: string): { url: string; branch?: string }[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .map((line) => {
      const parts = line.split(/\s+/)
      const repo: { url: string; branch?: string } = { url: parts[0]! }
      if (parts.length > 1) repo.branch = parts[1]!
      return repo
    })
}

async function submit() {
  const name = agentName.value.trim()
  if (!name) return
  submitting.value = true
  errorMsg.value = ''
  try {
    const repos = isAmbient.value ? parseRepos(reposText.value) : undefined
    const task = isAmbient.value ? (taskPrompt.value.trim() || undefined) : undefined
    await api.createAgent(props.space, {
      name,
      work_dir: isTmux.value ? (workDir.value.trim() || undefined) : undefined,
      model: model.value.trim() || undefined,
      backend: backend.value,
      repos: repos && repos.length > 0 ? repos : undefined,
      task,
      initial_message: initialMessage.value.trim() || undefined,
    })
    if (selectedPersonaIds.value.length > 0) {
      await api.updateAgentConfig(props.space, name, { personas: selectedPersonaIds.value.map(id => ({ id })) })
    }
    const created = name
    reset()
    emit('created', created)
    emit('update:open', false)
  } catch (e: unknown) {
    errorMsg.value = e instanceof Error ? e.message : String(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(v) => { if (!v) reset(); emit('update:open', v) }">
    <DialogContent class="sm:max-w-[440px]">
      <DialogHeader>
        <DialogTitle>Add Agent</DialogTitle>
        <DialogDescription>
          Spawn a new agent in <span class="font-medium">{{ space }}</span>.
        </DialogDescription>
      </DialogHeader>

      <form class="flex flex-col gap-4 py-2" @submit.prevent="submit">
        <!-- Agent Name -->
        <div class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Agent Name <span class="text-destructive">*</span>
          </label>
          <Input
            v-model="agentName"
            placeholder="e.g. MyAgent"
            autocomplete="off"
            required
          />
        </div>

        <!-- Backend selector -->
        <div class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Backend</label>
          <div class="flex gap-2">
            <button
              type="button"
              :class="[
                'flex-1 rounded border px-3 py-1.5 text-sm transition-colors',
                backend === 'tmux'
                  ? 'border-primary bg-primary/10 text-primary font-medium'
                  : 'border-border bg-background hover:bg-muted/50',
              ]"
              @click="backend = 'tmux'"
            >
              tmux
            </button>
            <button
              type="button"
              :class="[
                'flex-1 rounded border px-3 py-1.5 text-sm transition-colors',
                backend === 'ambient'
                  ? 'border-primary bg-primary/10 text-primary font-medium'
                  : 'border-border bg-background hover:bg-muted/50',
              ]"
              @click="backend = 'ambient'"
            >
              ambient
            </button>
          </div>
          <p class="text-xs text-muted-foreground">
            <template v-if="isTmux">Local tmux session on the coordinator host.</template>
            <template v-else>Remote Kubernetes pod via the Ambient Code Platform.</template>
          </p>
        </div>

        <!-- Working Directory (tmux only) -->
        <div v-if="isTmux" class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Working Directory (optional)
          </label>
          <Input
            v-model="workDir"
            placeholder="e.g. /home/user/my-project"
            autocomplete="off"
            class="font-mono text-sm"
          />
          <p class="text-xs text-muted-foreground">
            The agent's tmux session will <code>cd</code> to this directory before starting.
          </p>
        </div>

        <!-- Model override (optional) -->
        <div class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Model (optional)
          </label>
          <Input
            v-model="model"
            list="agent-model-options"
            placeholder="Default (leave blank to use server default)"
            autocomplete="off"
            class="font-mono text-sm"
          />
          <datalist id="agent-model-options">
            <option value="sonnet" />
            <option value="opus" />
            <option value="haiku" />
            <option value="claude-sonnet-4-6" />
            <option value="claude-opus-4-6" />
            <option value="claude-haiku-4-5-20251001" />
          </datalist>
        </div>

        <!-- Initial Prompt (ambient only) -->
        <div v-if="isAmbient" class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Initial Prompt (optional)
          </label>
          <Textarea
            v-model="taskPrompt"
            placeholder="e.g. You are an agent. Implement the login page."
            rows="3"
            class="text-sm"
          />
          <p class="text-xs text-muted-foreground">
            The task prompt sent to the ACP session on creation.
          </p>
        </div>

        <!-- Initial Message (all backends) -->
        <div class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Initial Mission (optional)
          </label>
          <Textarea
            v-model="initialMessage"
            placeholder="e.g. Your first task is to implement the login page."
            rows="3"
            class="text-sm"
          />
          <p class="text-xs text-muted-foreground">
            A message queued to the agent immediately after ignite.
          </p>
        </div>

        <!-- Repos (ambient only) -->
        <div v-if="isAmbient" class="flex flex-col gap-1.5">
          <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Repos (optional)
          </label>
          <Textarea
            v-model="reposText"
            placeholder="https://github.com/org/repo&#10;https://github.com/org/other-repo feat/branch"
            rows="3"
            class="font-mono text-sm"
          />
          <p class="text-xs text-muted-foreground">
            One repo per line. Optionally append a branch after a space.
          </p>
        </div>

        <!-- Persona selector — shown when personas exist; quick-link shown when none -->
        <div class="flex flex-col gap-1.5">
          <div class="flex items-center justify-between">
            <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">
              Personas (optional)
            </label>
            <button
              v-if="personas.length === 0"
              type="button"
              class="text-xs text-primary hover:underline"
              @click="emit('open-personas')"
            >
              + Create persona
            </button>
          </div>
          <div v-if="personas.length > 0" class="flex flex-wrap gap-1.5">
            <button
              v-for="persona in personas"
              :key="persona.id"
              type="button"
              :title="persona.description || persona.name"
              :class="[
                'rounded border px-2 py-1 text-xs transition-colors',
                selectedPersonaIds.includes(persona.id)
                  ? 'border-primary bg-primary/10 text-primary font-medium'
                  : 'border-border bg-background hover:bg-muted/50',
              ]"
              @click="togglePersona(persona.id)"
            >
              {{ persona.name }}
            </button>
            <p class="text-xs text-muted-foreground w-full">Prompt fragments injected into the agent on spawn.</p>
          </div>
          <p v-else class="text-xs text-muted-foreground">No personas yet. Create one to inject reusable prompts into agents.</p>
        </div>

        <p v-if="errorMsg" class="text-xs text-destructive">{{ errorMsg }}</p>

        <DialogFooter>
          <Button type="button" variant="outline" @click="emit('update:open', false)">Cancel</Button>
          <Button type="submit" :disabled="!agentName.trim() || submitting">
            {{ submitting ? 'Creating…' : 'Add Agent' }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
