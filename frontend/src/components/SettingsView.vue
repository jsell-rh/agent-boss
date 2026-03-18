<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api, getStoredToken, setStoredToken } from '@/api/client'
import { AlertTriangle } from 'lucide-vue-next'
import {
  notificationsEnabled,
  soundEnabled,
  soundTheme,
  soundVolume,
  soundCategories,
  audioLogEnabled,
  SOUND_THEMES,
  SOUND_CATEGORY_META,
  activityTickEnabled,
  requestNotificationPermission,
  playChime,
  playSuccess,
  playBlockedAlert,
  playTaskTransition,
  playAgentMessage,
  playActivityTick,
} from '@/composables/useNotifications'

defineEmits<{ 'open-audio-guide': [] }>()

const allowSkipPermissions = ref(false)
const loading = ref(true)
const saving = ref(false)
const errorMsg = ref('')
const notifPermission = ref(typeof Notification !== 'undefined' ? Notification.permission : 'denied')
const playingCategory = ref<string | null>(null)

async function toggleNotifications(value: boolean) {
  notificationsEnabled.value = value
  if (value) {
    const granted = await requestNotificationPermission()
    notifPermission.value = granted ? 'granted' : 'denied'
    if (!granted) notificationsEnabled.value = false
  }
}

// Preview a sound category
function previewCategory(catId: string) {
  if (!soundEnabled.value) return
  playingCategory.value = catId
  switch (catId) {
    case 'urgent': playBlockedAlert(); break
    case 'events': playTaskTransition('in_progress'); break
    case 'celebrations': playSuccess('medium'); break
    case 'ambient': playActivityTick(); break
    case 'social': playAgentMessage('preview-agent'); break
  }
  setTimeout(() => { if (playingCategory.value === catId) playingCategory.value = null }, 1200)
}

// API token management
const apiToken = ref(getStoredToken())
const tokenSaved = ref(false)

function saveToken() {
  setStoredToken(apiToken.value.trim())
  tokenSaved.value = true
  setTimeout(() => { tokenSaved.value = false }, 2000)
}

function clearToken() {
  apiToken.value = ''
  setStoredToken('')
}

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
      <p class="text-sm text-muted-foreground mt-1">Server-wide configuration for OpenDispatch.</p>
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

      <!-- API Token -->
      <div class="rounded-lg border p-4 flex flex-col gap-3">
        <div class="flex flex-col gap-0.5">
          <span class="font-medium text-sm">API Token</span>
          <span class="text-xs text-muted-foreground">
            Set <code class="rounded bg-muted px-1">OD_API_TOKEN</code> on the server to enable auth.
            Enter the same token here so the dashboard can make authenticated requests.
          </span>
        </div>
        <div class="flex gap-2">
          <input
            v-model="apiToken"
            type="password"
            placeholder="Paste token here…"
            class="flex-1 rounded-md border bg-background px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            @keydown.enter="saveToken"
          />
          <button
            type="button"
            class="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            @click="saveToken"
          >
            {{ tokenSaved ? 'Saved!' : 'Save' }}
          </button>
          <button
            v-if="apiToken"
            type="button"
            class="rounded-md border px-3 py-1.5 text-sm hover:bg-muted"
            @click="clearToken"
          >
            Clear
          </button>
        </div>
        <p class="text-xs text-muted-foreground">
          Token is stored in <code class="rounded bg-muted px-1">localStorage</code>.
          Leave blank for open mode (no auth).
        </p>
      </div>

      <p v-if="errorMsg" class="text-xs text-destructive">{{ errorMsg }}</p>

      <!-- Notifications section -->
      <div>
        <h2 class="text-base font-semibold mb-1">Notifications</h2>
        <p class="text-xs text-muted-foreground mb-3">Controls browser notifications and sound effects for OpenDispatch events.</p>

        <!-- Browser notifications toggle -->
        <div class="rounded-lg border p-4 flex flex-col gap-3 mb-3">
          <div class="flex items-center justify-between gap-4">
            <div class="flex flex-col gap-0.5">
              <span class="font-medium text-sm">Browser Notifications</span>
              <span class="text-xs text-muted-foreground">
                Show a desktop notification when a new message arrives and the tab is in the background.
                <span v-if="notifPermission === 'denied'" class="text-destructive ml-1">
                  Permission denied — enable notifications in browser settings.
                </span>
              </span>
            </div>
            <button
              type="button"
              role="switch"
              :aria-checked="notificationsEnabled"
              :class="[
                'relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
                notificationsEnabled ? 'bg-primary' : 'bg-input',
              ]"
              @click="toggleNotifications(!notificationsEnabled)"
            >
              <span
                :class="[
                  'pointer-events-none block size-4 rounded-full bg-background shadow-lg ring-0 transition-transform',
                  notificationsEnabled ? 'translate-x-5' : 'translate-x-0',
                ]"
              />
            </button>
          </div>
        </div>

        <!-- Sound toggle + theme picker -->
        <div class="rounded-lg border p-4 flex flex-col gap-4">
          <!-- Toggle row -->
          <div class="flex items-center justify-between gap-4">
            <div class="flex flex-col gap-0.5">
              <span class="font-medium text-sm">Sound Effects</span>
              <span class="text-xs text-muted-foreground">
                Audio cues for key events. Off by default.
              </span>
            </div>
            <div class="flex items-center gap-2">
              <button
                type="button"
                class="text-xs px-2 py-0.5 rounded border border-border hover:bg-muted transition-colors"
                @click="$emit('open-audio-guide')"
                title="Open the audio guide to learn what each sound means"
              >
                Audio Guide
              </button>
              <button
                type="button"
                role="switch"
                :aria-checked="soundEnabled"
                :class="[
                  'relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
                  soundEnabled ? 'bg-primary' : 'bg-input',
                ]"
                @click="soundEnabled = !soundEnabled"
              >
                <span
                  :class="[
                    'pointer-events-none block size-4 rounded-full bg-background shadow-lg ring-0 transition-transform',
                    soundEnabled ? 'translate-x-5' : 'translate-x-0',
                  ]"
                />
              </button>
            </div>
          </div>

          <!-- Sound theme picker -->
          <div class="flex flex-col gap-2">
            <span class="text-xs font-medium text-foreground">Sound Theme</span>
            <div class="grid grid-cols-2 gap-2">
              <button
                v-for="theme in SOUND_THEMES"
                :key="theme.id"
                type="button"
                :class="[
                  'flex flex-col items-start rounded-md border px-3 py-2 text-left transition-colors hover:bg-muted',
                  soundTheme === theme.id ? 'border-primary bg-primary/5' : 'border-border',
                ]"
                @click="soundTheme = theme.id; if (soundEnabled) playChime()"
              >
                <span class="text-xs font-medium">{{ theme.label }}</span>
                <span class="text-xs text-muted-foreground">{{ theme.description }}</span>
              </button>
            </div>
          </div>

          <!-- Volume slider -->
          <div class="flex flex-col gap-2 pt-2 border-t border-border">
            <div class="flex items-center justify-between">
              <span class="text-sm font-medium">Volume</span>
              <span class="text-xs text-muted-foreground tabular-nums">{{ Math.round(soundVolume * 100) }}%</span>
            </div>
            <input
              v-model.number="soundVolume"
              type="range"
              min="0"
              max="1"
              step="0.05"
              class="w-full accent-primary h-1.5 rounded-full cursor-pointer"
            />
          </div>

          <!-- Per-category toggles with preview buttons -->
          <div class="flex flex-col gap-2 pt-2 border-t border-border">
            <span class="text-sm font-medium">Sound Categories</span>
            <p class="text-xs text-muted-foreground -mt-1">Toggle and preview each category. Click the play button to hear a sample.</p>
            <div class="flex flex-col gap-1">
              <div
                v-for="cat in SOUND_CATEGORY_META"
                :key="cat.id"
                class="flex items-center justify-between gap-3 py-1.5"
              >
                <div class="flex items-center gap-2 min-w-0 flex-1">
                  <button
                    type="button"
                    class="shrink-0 w-6 h-6 rounded-full border border-border flex items-center justify-center text-xs hover:bg-muted transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                    :class="playingCategory === cat.id ? 'bg-primary/20 border-primary/50' : ''"
                    :disabled="!soundEnabled || !soundCategories[cat.id]"
                    :title="`Preview ${cat.label} sound`"
                    @click="previewCategory(cat.id)"
                  >
                    {{ playingCategory === cat.id ? '||' : '\u25B6' }}
                  </button>
                  <div class="flex flex-col gap-0.5 min-w-0">
                    <span class="text-xs font-medium">{{ cat.label }}</span>
                    <span class="text-xs text-muted-foreground">{{ cat.description }}</span>
                  </div>
                </div>
                <button
                  type="button"
                  role="switch"
                  :aria-checked="soundCategories[cat.id]"
                  :class="[
                    'relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors',
                    soundCategories[cat.id] ? 'bg-primary' : 'bg-input',
                  ]"
                  @click="soundCategories = { ...soundCategories, [cat.id]: !soundCategories[cat.id] }"
                >
                  <span :class="['block h-4 w-4 rounded-full bg-background shadow transition-transform', soundCategories[cat.id] ? 'translate-x-4' : 'translate-x-0']" />
                </button>
              </div>
            </div>
          </div>

          <!-- Audio event log toggle (Minecraft-style) -->
          <div class="flex items-center justify-between gap-4 pt-2 border-t border-border">
            <div class="flex flex-col gap-0.5 min-w-0">
              <span class="text-sm font-medium">Audio Event Log</span>
              <span class="text-xs text-muted-foreground">
                Shows a Minecraft-style overlay in the bottom-right describing what each sound cue means as it plays.
                Helpful for learning the audio language.
              </span>
            </div>
            <button
              role="switch"
              :aria-checked="audioLogEnabled"
              :class="['relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors', audioLogEnabled ? 'bg-primary' : 'bg-input']"
              @click="audioLogEnabled = !audioLogEnabled"
            >
              <span :class="['block h-4 w-4 rounded-full bg-background shadow transition-transform', audioLogEnabled ? 'translate-x-4' : 'translate-x-0']" />
            </button>
          </div>

          <!-- Activity tick toggle (moved here from standalone section) -->
          <div class="flex items-center justify-between gap-4 pt-2 border-t border-border">
            <div class="flex flex-col gap-0.5 min-w-0">
              <span class="text-sm font-medium">Activity Tick</span>
              <span class="text-xs text-muted-foreground">
                Soft tick on each agent update — server-room ambience. Very quiet.
              </span>
            </div>
            <button
              role="switch"
              :aria-checked="activityTickEnabled"
              :class="['relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors', activityTickEnabled ? 'bg-primary' : 'bg-input']"
              @click="activityTickEnabled = !activityTickEnabled"
            >
              <span :class="['block h-4 w-4 rounded-full bg-background shadow transition-transform', activityTickEnabled ? 'translate-x-4' : 'translate-x-0']" />
            </button>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
