<script setup lang="ts">
import type { Persona, PersonaVersion, PersonaAgentInfo } from '@/types'
import { ref, computed, onMounted } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Plus, Pencil, Trash2, X, Save, Loader2, User, History, Users, RotateCcw, RefreshCw, ChevronDown, ChevronRight, AlertTriangle } from 'lucide-vue-next'
import api from '@/api/client'

const personas = ref<Persona[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

// Create form
const createOpen = ref(false)
const createName = ref('')
const createDescription = ref('')
const createPrompt = ref('')
const createSaving = ref(false)
const createError = ref('')

// Edit form
const editingId = ref<string | null>(null)
const editName = ref('')
const editDescription = ref('')
const editPrompt = ref('')
const editSaving = ref(false)
const editError = ref('')

// Delete
const deleteId = ref<string | null>(null)
const deleteConfirmOpen = ref(false)
const deleting = ref(false)

// Detail panel
const selectedId = ref<string | null>(null)
const historyVersions = ref<PersonaVersion[]>([])
const historyLoading = ref(false)
const agentUsers = ref<PersonaAgentInfo[]>([])
const agentsLoading = ref(false)
const revertingVersion = ref<number | null>(null)
const revertConfirmOpen = ref(false)
const revertTargetVersion = ref<number | null>(null)
const restartingOutdated = ref(false)
const restartResult = ref<{ restarted: string[]; errors: string[] } | null>(null)

const selectedPersona = computed(() => personas.value.find(p => p.id === selectedId.value))
const outdatedAgents = computed(() => agentUsers.value.filter(a => a.outdated))

async function loadPersonas() {
  loading.value = true
  error.value = null
  try {
    personas.value = await api.fetchPersonas()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
    personas.value = []
  } finally {
    loading.value = false
  }
}

async function selectPersona(id: string) {
  if (selectedId.value === id) {
    selectedId.value = null
    return
  }
  selectedId.value = id
  restartResult.value = null
  await Promise.all([loadHistory(id), loadAgentUsers(id)])
}

async function loadHistory(id: string) {
  historyLoading.value = true
  try {
    historyVersions.value = await api.fetchPersonaHistory(id)
  } catch {
    historyVersions.value = []
  } finally {
    historyLoading.value = false
  }
}

async function loadAgentUsers(id: string) {
  agentsLoading.value = true
  try {
    agentUsers.value = await api.fetchPersonaAgents(id)
  } catch {
    agentUsers.value = []
  } finally {
    agentsLoading.value = false
  }
}

function promptRevert(version: number) {
  revertTargetVersion.value = version
  revertConfirmOpen.value = true
}

async function submitRevert() {
  if (!selectedId.value || revertTargetVersion.value == null) return
  revertingVersion.value = revertTargetVersion.value
  try {
    const updated = await api.revertPersona(selectedId.value, revertTargetVersion.value)
    const idx = personas.value.findIndex(p => p.id === selectedId.value)
    if (idx >= 0) personas.value[idx] = updated
    revertConfirmOpen.value = false
    await loadHistory(selectedId.value)
    await loadAgentUsers(selectedId.value)
  } catch {
    // error handled by UI
  } finally {
    revertingVersion.value = null
  }
}

async function restartOutdated() {
  if (!selectedId.value) return
  restartingOutdated.value = true
  restartResult.value = null
  try {
    const result = await api.restartOutdatedPersonaAgents(selectedId.value)
    restartResult.value = result
    await loadAgentUsers(selectedId.value)
  } catch {
    // handled
  } finally {
    restartingOutdated.value = false
  }
}

async function submitCreate() {
  if (!createName.value.trim() || !createPrompt.value.trim()) return
  createSaving.value = true
  createError.value = ''
  try {
    const created = await api.createPersona({
      name: createName.value.trim(),
      description: createDescription.value.trim(),
      prompt: createPrompt.value.trim(),
    })
    personas.value.push(created)
    createOpen.value = false
    createName.value = ''
    createDescription.value = ''
    createPrompt.value = ''
  } catch (e) {
    createError.value = e instanceof Error ? e.message : String(e)
  } finally {
    createSaving.value = false
  }
}

function startEdit(persona: Persona) {
  editingId.value = persona.id
  editName.value = persona.name
  editDescription.value = persona.description
  editPrompt.value = persona.prompt
  editError.value = ''
}

function cancelEdit() {
  editingId.value = null
  editError.value = ''
}

async function submitEdit() {
  if (!editingId.value || !editName.value.trim() || !editPrompt.value.trim()) return
  editSaving.value = true
  editError.value = ''
  try {
    const updated = await api.updatePersona(editingId.value, {
      name: editName.value.trim(),
      description: editDescription.value.trim(),
      prompt: editPrompt.value.trim(),
    })
    const idx = personas.value.findIndex(p => p.id === editingId.value)
    if (idx >= 0) personas.value[idx] = updated
    editingId.value = null
    // Refresh detail panel if this persona is selected
    if (selectedId.value === updated.id) {
      await Promise.all([loadHistory(updated.id), loadAgentUsers(updated.id)])
    }
  } catch (e) {
    editError.value = e instanceof Error ? e.message : String(e)
  } finally {
    editSaving.value = false
  }
}

function confirmDelete(id: string) {
  deleteId.value = id
  deleteConfirmOpen.value = true
}

async function submitDelete() {
  if (!deleteId.value) return
  deleting.value = true
  try {
    await api.deletePersona(deleteId.value)
    if (selectedId.value === deleteId.value) selectedId.value = null
    personas.value = personas.value.filter(p => p.id !== deleteId.value)
    deleteConfirmOpen.value = false
    deleteId.value = null
  } catch {
    // ignore — persona stays in list
  } finally {
    deleting.value = false
  }
}

function formatDate(dateStr: string) {
  if (!dateStr) return ''
  const d = new Date(dateStr)
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

onMounted(loadPersonas)
</script>

<template>
  <div class="flex flex-col h-full overflow-hidden">
    <!-- Header -->
    <div class="flex items-center justify-between px-6 py-4 border-b shrink-0">
      <div>
        <h1 class="text-lg font-semibold">Personas</h1>
        <p class="text-xs text-muted-foreground mt-0.5">Reusable prompt fragments injected into agents on spawn.</p>
      </div>
      <Button size="sm" class="gap-1.5" @click="createOpen = true">
        <Plus class="size-4" /> New Persona
      </Button>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="flex items-center justify-center flex-1 text-muted-foreground gap-2 text-sm">
      <Loader2 class="size-4 animate-spin" /> Loading personas…
    </div>

    <!-- Error -->
    <div v-else-if="error" class="flex flex-col items-center justify-center flex-1 gap-3 text-sm">
      <p class="text-muted-foreground">{{ error }}</p>
      <Button variant="outline" size="sm" @click="loadPersonas">Retry</Button>
    </div>

    <!-- Empty state -->
    <div v-else-if="!personas.length" class="flex flex-col items-center justify-center flex-1 gap-3 text-muted-foreground">
      <User class="size-10 opacity-30" />
      <p class="text-sm">No personas yet. Create one to inject reusable prompts into agents.</p>
      <Button size="sm" @click="createOpen = true"><Plus class="size-4 mr-1" /> New Persona</Button>
    </div>

    <!-- Persona list + detail panel -->
    <div v-else class="flex-1 overflow-hidden flex">
      <!-- Left: persona list -->
      <div class="flex-1 overflow-y-auto p-6 space-y-4 min-w-0" :class="{ 'max-w-[50%]': selectedId }">
        <div
          v-for="persona in personas"
          :key="persona.id"
          class="border rounded-lg bg-card cursor-pointer transition-colors"
          :class="{ 'ring-1 ring-primary/50': selectedId === persona.id }"
          @click="selectPersona(persona.id)"
        >
          <!-- View mode -->
          <div v-if="editingId !== persona.id" class="p-4">
            <div class="flex items-start justify-between gap-3 mb-2">
              <div class="flex items-center gap-2 min-w-0">
                <component :is="selectedId === persona.id ? ChevronDown : ChevronRight" class="size-4 text-muted-foreground shrink-0" />
                <div class="min-w-0">
                  <div class="flex items-center gap-2">
                    <h3 class="font-semibold text-sm truncate">{{ persona.name }}</h3>
                    <Badge variant="outline" class="text-[10px] px-1.5 py-0 h-4 font-mono shrink-0">v{{ persona.version }}</Badge>
                  </div>
                  <p v-if="persona.description" class="text-xs text-muted-foreground mt-0.5 truncate">{{ persona.description }}</p>
                </div>
              </div>
              <div class="flex items-center gap-1 shrink-0" @click.stop>
                <Button variant="ghost" size="sm" class="h-7 w-7 p-0" title="Edit" @click="startEdit(persona)">
                  <Pencil class="size-3.5" />
                </Button>
                <Button variant="ghost" size="sm" class="h-7 w-7 p-0 text-destructive/70 hover:text-destructive hover:bg-destructive/10" title="Delete" @click="confirmDelete(persona.id)">
                  <Trash2 class="size-3.5" />
                </Button>
              </div>
            </div>
            <pre class="text-xs bg-muted/50 rounded p-3 whitespace-pre-wrap font-mono leading-relaxed max-h-32 overflow-y-auto">{{ persona.prompt }}</pre>
            <p class="text-[10px] text-muted-foreground/60 mt-1.5">ID: {{ persona.id }} · Updated {{ formatDate(persona.updated_at) }}</p>
          </div>

          <!-- Edit mode -->
          <div v-else class="p-4 space-y-3" @click.stop>
            <div class="flex items-center justify-between mb-1">
              <h3 class="text-sm font-semibold">Editing persona</h3>
              <button class="text-muted-foreground hover:text-foreground" @click="cancelEdit">
                <X class="size-4" />
              </button>
            </div>
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Name *</label>
              <Input v-model="editName" class="h-8 text-sm" />
            </div>
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Description</label>
              <Input v-model="editDescription" class="h-8 text-sm" placeholder="Short description" />
            </div>
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Prompt *</label>
              <Textarea v-model="editPrompt" rows="6" class="text-xs font-mono" />
            </div>
            <p v-if="editError" class="text-xs text-destructive">{{ editError }}</p>
            <div class="flex gap-2">
              <Button size="sm" class="h-7 text-xs gap-1" :disabled="editSaving || !editName.trim() || !editPrompt.trim()" @click="submitEdit">
                <Loader2 v-if="editSaving" class="size-3 animate-spin" />
                <Save v-else class="size-3" />
                Save
              </Button>
              <Button size="sm" variant="ghost" class="h-7 text-xs" @click="cancelEdit">Cancel</Button>
            </div>
          </div>
        </div>
      </div>

      <!-- Right: detail panel (history + agents) -->
      <div v-if="selectedId && selectedPersona" class="w-[50%] border-l overflow-y-auto">
        <div class="p-5 space-y-6">
          <!-- Header -->
          <div>
            <h2 class="text-sm font-semibold">{{ selectedPersona.name }}</h2>
            <p class="text-xs text-muted-foreground mt-0.5">Version {{ selectedPersona.version }} · {{ formatDate(selectedPersona.updated_at) }}</p>
          </div>

          <!-- Agent Usage -->
          <div>
            <div class="flex items-center justify-between mb-3">
              <div class="flex items-center gap-2">
                <Users class="size-4 text-muted-foreground" />
                <h3 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  Agents Using This Persona
                  <span v-if="agentUsers.length" class="ml-1 text-foreground">({{ agentUsers.length }})</span>
                </h3>
              </div>
              <Button
                v-if="outdatedAgents.length > 0"
                size="sm"
                variant="outline"
                class="h-7 text-xs gap-1.5"
                :disabled="restartingOutdated"
                @click="restartOutdated"
              >
                <Loader2 v-if="restartingOutdated" class="size-3 animate-spin" />
                <RefreshCw v-else class="size-3" />
                Restart {{ outdatedAgents.length }} outdated
              </Button>
            </div>

            <div v-if="agentsLoading" class="text-xs text-muted-foreground flex items-center gap-1.5">
              <Loader2 class="size-3 animate-spin" /> Loading…
            </div>
            <div v-else-if="!agentUsers.length" class="text-xs text-muted-foreground">
              No agents are using this persona.
            </div>
            <div v-else class="space-y-1.5">
              <div
                v-for="agent in agentUsers"
                :key="agent.space + '/' + agent.agent"
                class="flex items-center justify-between px-3 py-2 rounded-md text-xs"
                :class="agent.outdated ? 'bg-warning/10 border border-warning/30' : 'bg-muted/30'"
              >
                <div class="flex items-center gap-2 min-w-0">
                  <AlertTriangle v-if="agent.outdated" class="size-3.5 text-warning shrink-0" />
                  <span class="font-mono font-medium truncate">{{ agent.space }}/{{ agent.agent }}</span>
                </div>
                <div class="flex items-center gap-2 shrink-0">
                  <span v-if="agent.outdated" class="text-warning font-mono">
                    v{{ agent.pinned_version }} → v{{ agent.current_version }}
                  </span>
                  <Badge v-else variant="outline" class="text-[10px] h-4 px-1.5 py-0 font-mono">
                    v{{ agent.pinned_version }}
                  </Badge>
                </div>
              </div>
            </div>

            <!-- Restart result -->
            <div v-if="restartResult" class="mt-3 text-xs space-y-1">
              <p v-if="restartResult.restarted.length" class="text-success">
                Restarted: {{ restartResult.restarted.join(', ') }}
              </p>
              <p v-if="restartResult.errors?.length" class="text-destructive">
                Errors: {{ restartResult.errors.join(', ') }}
              </p>
            </div>
          </div>

          <!-- Version History -->
          <div>
            <div class="flex items-center gap-2 mb-3">
              <History class="size-4 text-muted-foreground" />
              <h3 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                Version History
                <span v-if="historyVersions.length" class="ml-1 text-foreground">({{ historyVersions.length }})</span>
              </h3>
            </div>

            <div v-if="historyLoading" class="text-xs text-muted-foreground flex items-center gap-1.5">
              <Loader2 class="size-3 animate-spin" /> Loading…
            </div>
            <div v-else-if="!historyVersions.length" class="text-xs text-muted-foreground">
              No version history yet.
            </div>
            <div v-else class="space-y-2">
              <div
                v-for="ver in [...historyVersions].reverse()"
                :key="ver.version"
                class="border rounded-md p-3"
                :class="ver.version === selectedPersona.version ? 'border-primary/40 bg-primary/5' : ''"
              >
                <div class="flex items-center justify-between mb-2">
                  <div class="flex items-center gap-2">
                    <Badge variant="outline" class="text-[10px] px-1.5 py-0 h-4 font-mono">v{{ ver.version }}</Badge>
                    <span v-if="ver.version === selectedPersona.version" class="text-[10px] text-primary font-medium">current</span>
                    <span class="text-[10px] text-muted-foreground">{{ formatDate(ver.updated_at) }}</span>
                  </div>
                  <Button
                    v-if="ver.version !== selectedPersona.version"
                    size="sm"
                    variant="ghost"
                    class="h-6 text-[11px] gap-1 px-2"
                    :disabled="revertingVersion === ver.version"
                    @click.stop="promptRevert(ver.version)"
                  >
                    <Loader2 v-if="revertingVersion === ver.version" class="size-3 animate-spin" />
                    <RotateCcw v-else class="size-3" />
                    Revert
                  </Button>
                </div>
                <pre class="text-[11px] bg-muted/50 rounded p-2 whitespace-pre-wrap font-mono leading-relaxed max-h-24 overflow-y-auto">{{ ver.prompt }}</pre>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Create Dialog -->
    <AlertDialog v-model:open="createOpen">
      <AlertDialogContent class="max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>New Persona</AlertDialogTitle>
          <AlertDialogDescription>
            Create a reusable prompt fragment to inject into agent sessions.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div class="space-y-3 py-1">
          <div class="flex flex-col gap-1.5">
            <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Name *</label>
            <Input v-model="createName" placeholder="e.g. Senior Backend Engineer" class="h-8 text-sm" />
          </div>
          <div class="flex flex-col gap-1.5">
            <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Description</label>
            <Input v-model="createDescription" placeholder="Short description" class="h-8 text-sm" />
          </div>
          <div class="flex flex-col gap-1.5">
            <label class="text-xs font-medium text-muted-foreground uppercase tracking-wide">Prompt *</label>
            <Textarea v-model="createPrompt" placeholder="You are a senior backend engineer with expertise in Go and distributed systems..." rows="6" class="text-xs font-mono" />
          </div>
          <p v-if="createError" class="text-xs text-destructive">{{ createError }}</p>
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel @click="createError = ''">Cancel</AlertDialogCancel>
          <AlertDialogAction
            :disabled="!createName.trim() || !createPrompt.trim() || createSaving"
            @click.prevent="submitCreate"
          >
            <Loader2 v-if="createSaving" class="size-4 animate-spin mr-1" />
            <Plus v-else class="size-4 mr-1" />
            Create
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <!-- Delete Confirm -->
    <AlertDialog v-model:open="deleteConfirmOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete persona?</AlertDialogTitle>
          <AlertDialogDescription>
            This will permanently delete the persona. Agents already configured with it will lose the reference.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            class="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            :disabled="deleting"
            @click.prevent="submitDelete"
          >
            <Loader2 v-if="deleting" class="size-4 animate-spin mr-1" />
            <Trash2 v-else class="size-4 mr-1" />
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <!-- Revert Confirm -->
    <AlertDialog v-model:open="revertConfirmOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revert to v{{ revertTargetVersion }}?</AlertDialogTitle>
          <AlertDialogDescription>
            This will create a new version with the prompt from v{{ revertTargetVersion }}.
            Agents using this persona will show as outdated and can be restarted to pick up the change.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction @click.prevent="submitRevert">
            <RotateCcw class="size-4 mr-1" />
            Revert
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
