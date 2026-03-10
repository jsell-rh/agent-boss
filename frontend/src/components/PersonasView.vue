<script setup lang="ts">
import type { Persona } from '@/types'
import { ref, onMounted } from 'vue'
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
import { Plus, Pencil, Trash2, X, Save, Loader2, User } from 'lucide-vue-next'
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
    personas.value = personas.value.filter(p => p.id !== deleteId.value)
    deleteConfirmOpen.value = false
    deleteId.value = null
  } catch {
    // ignore — persona stays in list
  } finally {
    deleting.value = false
  }
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
      <p class="text-xs text-muted-foreground/60">Personas backend may not be available yet (requires PR #89).</p>
      <Button variant="outline" size="sm" @click="loadPersonas">Retry</Button>
    </div>

    <!-- Empty state -->
    <div v-else-if="!personas.length" class="flex flex-col items-center justify-center flex-1 gap-3 text-muted-foreground">
      <User class="size-10 opacity-30" />
      <p class="text-sm">No personas yet. Create one to inject reusable prompts into agents.</p>
      <Button size="sm" @click="createOpen = true"><Plus class="size-4 mr-1" /> New Persona</Button>
    </div>

    <!-- Persona list -->
    <div v-else class="flex-1 overflow-y-auto p-6 space-y-4">
      <div
        v-for="persona in personas"
        :key="persona.id"
        class="border rounded-lg bg-card"
      >
        <!-- View mode -->
        <div v-if="editingId !== persona.id" class="p-4">
          <div class="flex items-start justify-between gap-3 mb-2">
            <div>
              <div class="flex items-center gap-2">
                <h3 class="font-semibold text-sm">{{ persona.name }}</h3>
                <Badge variant="outline" class="text-[10px] px-1.5 py-0 h-4 font-mono">v{{ persona.version }}</Badge>
              </div>
              <p v-if="persona.description" class="text-xs text-muted-foreground mt-0.5">{{ persona.description }}</p>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <Button variant="ghost" size="sm" class="h-7 w-7 p-0" title="Edit" @click="startEdit(persona)">
                <Pencil class="size-3.5" />
              </Button>
              <Button variant="ghost" size="sm" class="h-7 w-7 p-0 text-destructive/70 hover:text-destructive hover:bg-destructive/10" title="Delete" @click="confirmDelete(persona.id)">
                <Trash2 class="size-3.5" />
              </Button>
            </div>
          </div>
          <pre class="text-xs bg-muted/50 rounded p-3 whitespace-pre-wrap font-mono leading-relaxed max-h-40 overflow-y-auto">{{ persona.prompt }}</pre>
          <p class="text-[10px] text-muted-foreground/60 mt-1.5">ID: {{ persona.id }}</p>
        </div>

        <!-- Edit mode -->
        <div v-else class="p-4 space-y-3">
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
  </div>
</template>
