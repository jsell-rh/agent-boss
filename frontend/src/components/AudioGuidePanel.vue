<template>
  <Transition name="audio-guide-slide">
    <aside
      v-if="open"
      class="audio-guide-panel"
      role="complementary"
      aria-label="Audio Guide"
    >
      <header class="audio-guide-header">
        <h2>Audio Guide</h2>
        <p class="audio-guide-subtitle">
          Learn what each sound means. Click <span class="play-hint">&#9654;</span> to hear it.
        </p>
        <button class="audio-guide-close" @click="$emit('close')" aria-label="Close audio guide">
          &#x2715;
        </button>
      </header>

      <div class="audio-guide-rule">
        <span class="rule-icon">&#8593;</span> Ascending = progress &nbsp;&bull;&nbsp;
        <span class="rule-icon">&#8595;</span> Descending = shipping &nbsp;&bull;&nbsp;
        <span class="rule-icon">&#126;</span> Dissonant = needs attention &nbsp;&bull;&nbsp;
        <span class="rule-icon">&#9632;</span> Noise burst = message / communication
      </div>

      <section class="audio-guide-section">
        <h3>Actions</h3>
        <div class="audio-guide-note">These are structural sounds — sweeps &amp; textures — never agent voices.</div>
        <ul class="audio-guide-list">
          <li v-for="item in actionSamples" :key="item.id">
            <button class="play-btn" :class="{ playing: playing === item.id }" @click="playSample(item)">
              {{ playing === item.id ? '&#9646;&#9646;' : '&#9654;' }}
            </button>
            <div class="sample-meta">
              <span class="sample-label">{{ item.label }}</span>
              <span class="sample-desc">{{ item.desc }}</span>
            </div>
          </li>
        </ul>
      </section>

      <section class="audio-guide-section">
        <h3>Agent Voices</h3>
        <div class="audio-guide-note">Every agent has a unique voice — waveform, interval, rhythm, and stereo position.</div>
        <ul class="audio-guide-list">
          <li v-for="agent in agentList" :key="agent">
            <button class="play-btn" :class="{ playing: playing === 'agent-' + agent }" @click="playAgent(agent)">
              {{ playing === 'agent-' + agent ? '&#9646;&#9646;' : '&#9654;' }}
            </button>
            <div class="sample-meta">
              <span class="sample-label">{{ agent }}</span>
              <span class="sample-desc">Unique voice signature</span>
            </div>
          </li>
          <li v-if="!agentList.length">
            <span class="sample-desc empty">No agents in this space yet.</span>
          </li>
        </ul>
      </section>

      <section class="audio-guide-section">
        <h3>Alerts</h3>
        <ul class="audio-guide-list">
          <li>
            <button class="play-btn" :class="{ playing: playing === 'blocked' }" @click="playBlockedSample">
              {{ playing === 'blocked' ? '&#9646;&#9646;' : '&#9654;' }}
            </button>
            <div class="sample-meta">
              <span class="sample-label">Agent blocked / error</span>
              <span class="sample-desc">Dissonant minor second + tremolo — demands attention</span>
            </div>
          </li>
          <li>
            <button class="play-btn" :class="{ playing: playing === 'mention' }" @click="playMentionSample">
              {{ playing === 'mention' ? '&#9646;&#9646;' : '&#9654;' }}
            </button>
            <div class="sample-meta">
              <span class="sample-label">@mention received</span>
              <span class="sample-desc">Ascending ping (action) — urgent, 50ms gap to voice</span>
            </div>
          </li>
        </ul>
      </section>

      <section class="audio-guide-section">
        <h3>Full Grammar Demo</h3>
        <div class="audio-guide-note">
          Grammar: <code>[action cue] + [sender voice] + [recipient voice]</code>
        </div>
        <ul class="audio-guide-list">
          <li>
            <button class="play-btn wide" :class="{ playing: playing === 'demo' }" @click="playGrammarDemo">
              {{ playing === 'demo' ? '&#9646;&#9646;' : '&#9654;' }} Two agents talking
            </button>
          </li>
          <li>
            <button class="play-btn wide" :class="{ playing: playing === 'demo-spawn' }" @click="playSpawnDemo">
              {{ playing === 'demo-spawn' ? '&#9646;&#9646;' : '&#9654;' }} Agent spawned
            </button>
          </li>
        </ul>
      </section>

      <footer class="audio-guide-footer">
        <button class="dismiss-btn" @click="$emit('close')">Got it</button>
      </footer>
    </aside>
  </Transition>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import {
  playBlockedAlert,
  playMentionPing,
  playAgentSpawn,
  playAgentMessage,
  playTaskTransition,
  playSuccess,
  playPRShipped,
  previewAgentVoice,
  soundEnabled,
} from '@/composables/useNotifications'

const props = defineProps<{
  open: boolean
  agents: string[]
}>()

defineEmits<{ close: [] }>()

const playing = ref<string | null>(null)
const agentList = computed(() => props.agents.slice(0, 8)) // show up to 8 agents

function markPlaying(id: string, durationMs: number) {
  playing.value = id
  setTimeout(() => { if (playing.value === id) playing.value = null }, durationMs)
}

const actionSamples = [
  {
    id: 'task-start',
    label: 'Task started',
    desc: 'Rising sweep — ascending = progress beginning',
    fn: () => playTaskTransition('in_progress'),
    dur: 500,
  },
  {
    id: 'task-review',
    label: 'Task in review',
    desc: 'Noise burst + suspended chord — "awaiting"',
    fn: () => playTaskTransition('review'),
    dur: 700,
  },
  {
    id: 'task-done',
    label: 'Task completed',
    desc: 'Ascending 3-note resolution — ascending = success',
    fn: () => playSuccess('medium'),
    dur: 900,
  },
  {
    id: 'spawn',
    label: 'Agent spawned',
    desc: 'Warp sweep — textural, not melodic',
    fn: () => playAgentSpawn(),
    dur: 800,
  },
  {
    id: 'pr-shipped',
    label: 'PR link set',
    desc: 'Descending whoosh — descending = shipping',
    fn: () => playPRShipped(),
    dur: 700,
  },
  {
    id: 'message',
    label: 'Agent message',
    desc: 'Noise-burst — percussive texture marks communication',
    fn: () => playAgentMessage('demo-agent'),
    dur: 1300,
  },
]

function playSample(item: { id: string; fn: () => void; dur?: number }) {
  if (!soundEnabled.value) return
  item.fn()
  markPlaying(item.id, item.dur ?? 800)
}

function playAgent(agentName: string) {
  if (!soundEnabled.value) return
  previewAgentVoice(agentName)
  markPlaying('agent-' + agentName, 1200)
}

function playBlockedSample() {
  if (!soundEnabled.value) return
  playBlockedAlert()
  markPlaying('blocked', 700)
}

function playMentionSample() {
  if (!soundEnabled.value) return
  playMentionPing()
  markPlaying('mention', 400)
}

// Full grammar demo: noise-burst message action + sender + recipient
function playGrammarDemo() {
  if (!soundEnabled.value) return
  const agents = props.agents
  const sender = agents[0] ?? 'arch'
  const recipient = agents[1] ?? 'ux'
  playMentionPing(sender, recipient)
  markPlaying('demo', 2000)
}

function playSpawnDemo() {
  if (!soundEnabled.value) return
  const agentName = props.agents[0] ?? 'arch'
  playAgentSpawn(agentName)
  markPlaying('demo-spawn', 2000)
}
</script>

<style scoped>
.audio-guide-panel {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: 340px;
  background: hsl(var(--background));
  border-left: 1px solid hsl(var(--border));
  box-shadow: -4px 0 24px rgba(0,0,0,0.15);
  z-index: 50;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  padding: 1.25rem;
  gap: 0;
}

.audio-guide-header {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  margin-bottom: 0.75rem;
  position: relative;
}

.audio-guide-header h2 {
  font-size: 1.1rem;
  font-weight: 600;
  margin: 0;
  padding-right: 2rem;
}

.audio-guide-subtitle {
  font-size: 0.8rem;
  color: hsl(var(--muted-foreground));
  margin: 0;
}

.play-hint {
  font-size: 0.7rem;
  background: hsl(var(--muted));
  padding: 0 0.3em;
  border-radius: 3px;
}

.audio-guide-close {
  position: absolute;
  top: 0;
  right: 0;
  background: none;
  border: none;
  cursor: pointer;
  font-size: 1rem;
  color: hsl(var(--muted-foreground));
  padding: 0.2rem;
  line-height: 1;
}
.audio-guide-close:hover { color: hsl(var(--foreground)); }

.audio-guide-rule {
  font-size: 0.72rem;
  color: hsl(var(--muted-foreground));
  background: hsl(var(--muted) / 0.5);
  border-radius: 6px;
  padding: 0.45rem 0.75rem;
  margin-bottom: 1rem;
  line-height: 1.6;
}
.rule-icon {
  font-weight: 700;
  color: hsl(var(--foreground));
}

.audio-guide-section {
  margin-bottom: 1.25rem;
}
.audio-guide-section h3 {
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: hsl(var(--muted-foreground));
  margin: 0 0 0.4rem;
}
.audio-guide-note {
  font-size: 0.72rem;
  color: hsl(var(--muted-foreground));
  margin-bottom: 0.5rem;
  font-style: italic;
}
.audio-guide-note code {
  font-style: normal;
  background: hsl(var(--muted));
  padding: 0.1em 0.3em;
  border-radius: 3px;
  font-size: 0.68rem;
}

.audio-guide-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.audio-guide-list li {
  display: flex;
  align-items: center;
  gap: 0.65rem;
}

.play-btn {
  flex-shrink: 0;
  width: 2rem;
  height: 2rem;
  border-radius: 50%;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--muted) / 0.4);
  cursor: pointer;
  font-size: 0.75rem;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s, transform 0.1s;
  color: hsl(var(--foreground));
}
.play-btn:hover { background: hsl(var(--muted)); transform: scale(1.08); }
.play-btn.playing { background: hsl(var(--primary) / 0.2); border-color: hsl(var(--primary) / 0.5); }
.play-btn.wide {
  width: auto;
  border-radius: 6px;
  padding: 0.4rem 0.8rem;
  font-size: 0.8rem;
  gap: 0.4rem;
}

.sample-meta {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  min-width: 0;
}
.sample-label {
  font-size: 0.82rem;
  font-weight: 500;
  truncate: true;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sample-desc {
  font-size: 0.7rem;
  color: hsl(var(--muted-foreground));
}
.empty { font-style: italic; }

.audio-guide-footer {
  margin-top: auto;
  padding-top: 1rem;
}
.dismiss-btn {
  width: 100%;
  padding: 0.5rem;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--muted) / 0.5);
  border-radius: 6px;
  cursor: pointer;
  font-size: 0.85rem;
  font-weight: 500;
}
.dismiss-btn:hover { background: hsl(var(--muted)); }

/* Slide transition */
.audio-guide-slide-enter-active,
.audio-guide-slide-leave-active {
  transition: transform 0.25s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.2s;
}
.audio-guide-slide-enter-from,
.audio-guide-slide-leave-to {
  transform: translateX(100%);
  opacity: 0;
}
</style>
