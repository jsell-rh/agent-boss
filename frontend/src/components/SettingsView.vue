<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { AlertTriangle } from 'lucide-vue-next'

const allowSkipPermissions = ref(false)
const loading = ref(true)
const saving = ref(false)
const errorMsg = ref('')

onMounted(async () => {
  try {
    const settings = await api.getSettings()
    allowSkipPermissions.value = settings.allow_skip_permissions
  } catch (e: unknown) {
    errorMsg.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
})

async function toggleSkipPermissions(value: boolean) {
  saving.value = true
  errorMsg.value = ''
  try {
    const updated = await api.updateSettings({ allow_skip_permissions: value })
    allowSkipPermissions.value = updated.allow_skip_permissions
  } catch (e: unknown) {
    errorMsg.value = e instanceof Error ? e.message : String(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-6 p-6 max-w-2xl mx-auto">
    <div>
      <h1 class="text-2xl font-semibold">Settings</h1>
      <p class="text-sm text-muted-foreground mt-1">Server-wide configuration for Agent Boss.</p>
    </div>

    <div v-if="loading" class="text-sm text-muted-foreground">Loading settings…</div>

    <template v-else>
      <!-- Warning banner when skip-permissions is enabled -->
      <div
        v-if="allowSkipPermissions"
        class="flex items-start gap-3 rounded-md border border-amber-500/50 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400"
        role="alert"
      >
        <AlertTriangle class="size-4 mt-0.5 shrink-0" />
        <span>
          <strong>Permission skip is ON</strong> — all tmux agents can run tools without confirmation.
          Disable this before running untrusted agents.
        </span>
      </div>

      <!-- Skip permissions toggle -->
      <div class="rounded-lg border p-4 flex flex-col gap-3">
        <div class="flex items-center justify-between gap-4">
          <div class="flex flex-col gap-0.5">
            <span class="font-medium text-sm">Allow Skip Permissions</span>
            <span class="text-xs text-muted-foreground">
              Appends <code class="rounded bg-muted px-1">--dangerously-skip-permissions</code> to every tmux agent launch command.
              Agents can then run tools without per-tool confirmation prompts.
            </span>
          </div>
          <button
            type="button"
            role="switch"
            :aria-checked="allowSkipPermissions"
            :disabled="saving"
            :class="[
              'relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50',
              allowSkipPermissions ? 'bg-primary' : 'bg-input',
            ]"
            @click="toggleSkipPermissions(!allowSkipPermissions)"
          >
            <span
              :class="[
                'pointer-events-none block size-4 rounded-full bg-background shadow-lg ring-0 transition-transform',
                allowSkipPermissions ? 'translate-x-5' : 'translate-x-0',
              ]"
            />
          </button>
        </div>
        <p class="text-xs text-muted-foreground">
          Current state: <strong>{{ allowSkipPermissions ? 'Enabled' : 'Disabled' }}</strong>
          <span v-if="saving" class="ml-2 text-muted-foreground">Saving…</span>
        </p>
      </div>

      <p v-if="errorMsg" class="text-xs text-destructive">{{ errorMsg }}</p>
    </template>
  </div>
</template>
