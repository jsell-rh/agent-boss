<script setup lang="ts">
import { ref, computed } from 'vue'
import * as yaml from 'js-yaml'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Upload, CheckCircle2, AlertCircle, Loader2, Plus, RefreshCw, Minus } from 'lucide-vue-next'
import api from '@/api/client'
import type { KnowledgeSpace } from '@/types'

// --------------- Types ---------------

interface FleetPersona {
  name: string
  description?: string
  prompt: string
}

interface FleetAgent {
  role?: string
  parent?: string
  personas?: string[]
  work_dir?: string
  backend?: string
  command?: string
  initial_prompt?: string
  repo_url?: string
  model?: string
}

interface FleetFile {
  version?: string
  space?: { name?: string; shared_contracts?: string }
  personas?: Record<string, FleetPersona>
  agents?: Record<string, FleetAgent>
}

type DiffAction = 'create' | 'update' | 'unchanged' | 'orphan'

interface AgentDiff {
  name: string
  action: DiffAction
  details: string[]
}

interface PersonaDiff {
  id: string
  action: 'create' | 'update' | 'unchanged'
  name: string
}

// --------------- Props / Emits ---------------

const props = defineProps<{
  open: boolean
  space: KnowledgeSpace
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  imported: []
}>()

// --------------- State ---------------

type Step = 'pick' | 'diff' | 'applying' | 'done'

const step = ref<Step>('pick')
const parseError = ref('')
const fleet = ref<FleetFile | null>(null)
const agentDiffs = ref<AgentDiff[]>([])
const personaDiffs = ref<PersonaDiff[]>([])
const restartChanged = ref(false)
const applyError = ref('')
const createdCount = ref(0)
const updatedCount = ref(0)

// Agents with no active session (not yet spawned — used in success message)
const dormantAgents = computed(() =>
  agentDiffs.value
    .filter(d => d.action === 'create')
    .map(d => d.name)
)

// --------------- File handling ---------------

const MAX_BYTES = 1_048_576 // 1 MB

function onDrop(e: DragEvent) {
  e.preventDefault()
  const file = e.dataTransfer?.files[0]
  if (file) processFile(file)
}

function onFileInput(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (file) processFile(file)
}

function processFile(file: File) {
  parseError.value = ''

  if (!file.name.match(/\.(yaml|yml)$/i)) {
    parseError.value = 'Only .yaml or .yml files are accepted.'
    return
  }
  if (file.size > MAX_BYTES) {
    parseError.value = `File is too large (${(file.size / 1024).toFixed(0)} KB). Maximum is 1 MB.`
    return
  }

  const reader = new FileReader()
  reader.onload = (ev) => {
    try {
      const raw = yaml.load(ev.target?.result as string)
      if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
        parseError.value = 'Invalid fleet file: expected a YAML mapping at the top level.'
        return
      }
      fleet.value = raw as FleetFile
      computeDiff()
      step.value = 'diff'
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      parseError.value = `YAML parse error: ${msg}`
    }
  }
  reader.readAsText(file)
}

// --------------- Diff computation ---------------

function computeDiff() {
  const f = fleet.value
  if (!f) return

  // Persona diffs
  const pDiffs: PersonaDiff[] = []
  for (const [id, fp] of Object.entries(f.personas ?? {})) {
    // We can't fetch current persona state here cheaply, so treat all as "create or update".
    // The backend upserts on create; a 409 on update means "already exists with same prompt" (noop).
    pDiffs.push({ id, name: fp.name, action: 'create' })
  }
  personaDiffs.value = pDiffs

  // Agent diffs
  const existingAgents = props.space.agents ?? {}
  const aDiffs: AgentDiff[] = []
  for (const [name, fa] of Object.entries(f.agents ?? {})) {
    const existing = existingAgents[name]
    if (!existing) {
      aDiffs.push({ name, action: 'create', details: ['new agent'] })
    } else {
      const changes: string[] = []
      if (fa.work_dir) changes.push('work_dir updated')
      if (fa.initial_prompt) changes.push('initial_prompt set')
      if (fa.personas?.length) changes.push(`personas: ${fa.personas.join(', ')}`)
      aDiffs.push({
        name,
        action: changes.length ? 'update' : 'unchanged',
        details: changes,
      })
    }
  }

  // Agents in space but not in YAML
  for (const name of Object.keys(existingAgents)) {
    if (!f.agents?.[name]) {
      aDiffs.push({ name, action: 'orphan', details: ['not in YAML — use CLI --prune to remove'] })
    }
  }

  agentDiffs.value = aDiffs
}

// --------------- Topological sort ---------------

function topoSort(agents: Record<string, FleetAgent>): string[] {
  const order: string[] = []
  const visited = new Set<string>()

  function visit(name: string) {
    if (visited.has(name)) return
    visited.add(name)
    const parent = agents[name]?.parent
    if (parent && agents[parent]) visit(parent)
    order.push(name)
  }

  for (const name of Object.keys(agents)) visit(name)
  return order
}

// --------------- Apply ---------------

async function applyImport() {
  if (!fleet.value) return
  step.value = 'applying'
  applyError.value = ''
  createdCount.value = 0
  updatedCount.value = 0

  const f = fleet.value
  const spaceName = props.space.name

  try {
    // 1. Upsert personas (create or update)
    for (const [id, fp] of Object.entries(f.personas ?? {})) {
      try {
        await api.createPersona({ name: fp.name, description: fp.description ?? '', prompt: fp.prompt })
      } catch {
        // Persona already exists — update it
        try {
          await api.updatePersona(id, { name: fp.name, description: fp.description, prompt: fp.prompt })
        } catch {
          // Already up to date — ignore
        }
      }
    }

    // 2. Create/update agents in topological order (parents first)
    const order = topoSort(f.agents ?? {})
    const existingAgents = props.space.agents ?? {}

    for (const name of order) {
      const fa = f.agents?.[name]
      if (!fa) continue
      const exists = !!existingAgents[name]

      if (!exists) {
        // Create agent (registers config but does not spawn)
        await api.createAgent(spaceName, {
          name,
          work_dir: fa.work_dir,
          command: fa.command,
          backend: (fa.backend as 'tmux' | 'ambient') ?? 'tmux',
          parent: fa.parent,
          role: fa.role,
        })
        // Set initial_prompt and personas via config PATCH
        if (fa.initial_prompt || fa.personas?.length) {
          await api.updateAgentConfig(spaceName, name, {
            initial_prompt: fa.initial_prompt,
            personas: fa.personas?.map(id => ({ id })),
          })
        }
        createdCount.value++
      } else {
        // Update config
        const cfg: Record<string, unknown> = {}
        if (fa.work_dir) cfg.work_dir = fa.work_dir
        if (fa.initial_prompt) cfg.initial_prompt = fa.initial_prompt
        if (fa.personas?.length) cfg.personas = fa.personas.map(id => ({ id }))
        if (Object.keys(cfg).length) {
          await api.updateAgentConfig(spaceName, name, cfg)
          updatedCount.value++
        }
      }
    }

    step.value = 'done'
    emit('imported')
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err)
    applyError.value = msg
    step.value = 'diff'
  }
}

// --------------- Export helper (used by parent via button, not modal) ---------------

function close() {
  emit('update:open', false)
  // Reset after close animation
  setTimeout(() => {
    step.value = 'pick'
    fleet.value = null
    parseError.value = ''
    applyError.value = ''
  }, 200)
}
</script>

<template>
  <Dialog :open="open" @update:open="close">
    <DialogContent class="max-w-2xl">
      <DialogHeader>
        <DialogTitle>Import fleet file</DialogTitle>
        <DialogDescription>
          Load an <code>agent-compose.yaml</code> to create or update agents and personas in this space.
        </DialogDescription>
      </DialogHeader>

      <!-- Step 1: File pick -->
      <template v-if="step === 'pick'">
        <div
          class="border-2 border-dashed border-border rounded-lg p-10 text-center cursor-pointer hover:border-primary/50 transition-colors"
          @dragover.prevent
          @drop="onDrop"
          @click="($refs.fileInput as HTMLInputElement).click()"
        >
          <Upload class="size-8 mx-auto mb-3 text-muted-foreground" />
          <p class="text-sm font-medium mb-1">Drop your fleet.yaml here</p>
          <p class="text-xs text-muted-foreground">or click to browse — .yaml / .yml only, max 1 MB</p>
          <input
            ref="fileInput"
            type="file"
            accept=".yaml,.yml"
            class="hidden"
            @change="onFileInput"
          />
        </div>
        <p v-if="parseError" class="text-sm text-destructive flex items-center gap-1.5 mt-2">
          <AlertCircle class="size-4 shrink-0" />
          {{ parseError }}
        </p>
      </template>

      <!-- Step 2: Diff preview -->
      <template v-else-if="step === 'diff'">
        <p v-if="applyError" class="text-sm text-destructive flex items-center gap-1.5 mb-2">
          <AlertCircle class="size-4 shrink-0" />
          {{ applyError }}
        </p>

        <p class="text-sm text-muted-foreground mb-2">
          Importing into <strong>{{ space.name }}</strong>
        </p>

        <ScrollArea class="max-h-72 rounded border bg-muted/30 p-3 font-mono text-xs">
          <!-- Personas -->
          <template v-if="personaDiffs.length">
            <p class="text-muted-foreground mb-1">Personas</p>
            <div v-for="pd in personaDiffs" :key="pd.id" class="flex items-center gap-2 py-0.5">
              <Plus class="size-3 text-green-500 shrink-0" />
              <span class="text-green-500">+ {{ pd.id }}</span>
              <span class="text-muted-foreground">{{ pd.name }}</span>
            </div>
          </template>

          <!-- Agents -->
          <p class="text-muted-foreground mt-2 mb-1">Agents</p>
          <div v-for="ad in agentDiffs" :key="ad.name" class="flex items-start gap-2 py-0.5">
            <Plus v-if="ad.action === 'create'" class="size-3 text-green-500 shrink-0 mt-0.5" />
            <RefreshCw v-else-if="ad.action === 'update'" class="size-3 text-blue-500 shrink-0 mt-0.5" />
            <Minus v-else-if="ad.action === 'orphan'" class="size-3 text-amber-500 shrink-0 mt-0.5" />
            <span v-else class="size-3 shrink-0 mt-0.5 inline-block text-center text-muted-foreground">=</span>

            <span
              :class="{
                'text-green-500': ad.action === 'create',
                'text-blue-500': ad.action === 'update',
                'text-amber-500': ad.action === 'orphan',
                'text-muted-foreground': ad.action === 'unchanged',
              }"
            >{{ ad.name }}</span>
            <span v-if="ad.details.length" class="text-muted-foreground">
              ({{ ad.details.join(', ') }})
            </span>
          </div>
          <p v-if="!agentDiffs.length && !personaDiffs.length" class="text-muted-foreground">
            No changes detected.
          </p>
        </ScrollArea>

        <div class="flex items-center gap-2 mt-2">
          <input id="restart-changed" v-model="restartChanged" type="checkbox" class="rounded" />
          <label for="restart-changed" class="text-sm cursor-pointer select-none">
            Restart changed agents after import
          </label>
        </div>

        <div class="flex justify-between mt-4">
          <Button variant="outline" size="sm" @click="step = 'pick'">
            Back
          </Button>
          <div class="flex gap-2">
            <Button variant="outline" size="sm" @click="close">Cancel</Button>
            <Button
              size="sm"
              :disabled="agentDiffs.every(d => d.action === 'unchanged') && personaDiffs.length === 0"
              @click="applyImport"
            >
              Apply import
            </Button>
          </div>
        </div>
      </template>

      <!-- Step 3: Applying -->
      <template v-else-if="step === 'applying'">
        <div class="flex flex-col items-center gap-3 py-8">
          <Loader2 class="size-8 animate-spin text-primary" />
          <p class="text-sm text-muted-foreground">Applying fleet configuration…</p>
        </div>
      </template>

      <!-- Step 4: Done -->
      <template v-else-if="step === 'done'">
        <div class="flex flex-col items-center gap-3 py-6 text-center">
          <CheckCircle2 class="size-10 text-green-500" />
          <div>
            <p class="font-medium">Fleet imported</p>
            <p class="text-sm text-muted-foreground">
              <span v-if="createdCount">{{ createdCount }} agent{{ createdCount !== 1 ? 's' : '' }} created</span>
              <span v-if="createdCount && updatedCount"> · </span>
              <span v-if="updatedCount">{{ updatedCount }} updated</span>
            </p>
          </div>
          <div
            v-if="dormantAgents.length"
            class="bg-amber-500/10 border border-amber-500/30 rounded-lg px-4 py-3 text-sm text-amber-700 dark:text-amber-400 max-w-sm"
          >
            <p class="font-medium mb-1">{{ dormantAgents.length }} agent{{ dormantAgents.length !== 1 ? 's' : '' }} created but not yet running</p>
            <p class="text-xs opacity-80">Spawn them from the space overview when you're ready.</p>
          </div>
          <Button @click="close">Done</Button>
        </div>
      </template>
    </DialogContent>
  </Dialog>
</template>
