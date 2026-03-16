<template>
  <Transition name="audio-log-fade">
    <div
      v-if="enabled && entries.length > 0"
      class="audio-event-log"
      role="log"
      aria-label="Audio event log"
      aria-live="polite"
    >
      <TransitionGroup name="audio-entry" tag="div" class="audio-log-entries">
        <div
          v-for="entry in visibleEntries"
          :key="entry.id"
          class="audio-log-entry"
          :class="entry.category"
        >
          <span class="audio-log-icon">{{ entry.icon }}</span>
          <span class="audio-log-text">{{ entry.text }}</span>
        </div>
      </TransitionGroup>
    </div>
  </Transition>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { audioEventLog } from '@/composables/useNotifications'

defineProps<{ enabled: boolean }>()

const entries = audioEventLog

const visibleEntries = computed(() =>
  entries.value.slice(-6),
)

// Prune expired entries every second
let _pruneTimer: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  _pruneTimer = setInterval(() => {
    const now = Date.now()
    audioEventLog.value = audioEventLog.value.filter(e => now - e.ts < 5000)
  }, 1000)
})
onUnmounted(() => {
  if (_pruneTimer) clearInterval(_pruneTimer)
})
</script>

<style scoped>
.audio-event-log {
  position: fixed;
  bottom: 1rem;
  right: 1rem;
  z-index: 40;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  pointer-events: none;
  max-width: 280px;
}

.audio-log-entries {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.audio-log-entry {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.6rem;
  border-radius: 4px;
  background: hsl(var(--background) / 0.85);
  backdrop-filter: blur(8px);
  border: 1px solid hsl(var(--border) / 0.5);
  font-size: 0.7rem;
  color: hsl(var(--muted-foreground));
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.audio-log-entry.urgent {
  color: hsl(var(--destructive));
  border-color: hsl(var(--destructive) / 0.3);
}

.audio-log-entry.celebrations {
  color: hsl(142 76% 46%);
}

.audio-log-icon {
  font-size: 0.75rem;
  flex-shrink: 0;
  width: 1rem;
  text-align: center;
}

.audio-log-text {
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Transitions */
.audio-log-fade-enter-active,
.audio-log-fade-leave-active {
  transition: opacity 0.2s;
}
.audio-log-fade-enter-from,
.audio-log-fade-leave-to {
  opacity: 0;
}

.audio-entry-enter-active {
  transition: all 0.25s ease-out;
}
.audio-entry-leave-active {
  transition: all 0.2s ease-in;
}
.audio-entry-enter-from {
  opacity: 0;
  transform: translateX(20px);
}
.audio-entry-leave-to {
  opacity: 0;
  transform: translateX(20px);
}
</style>
