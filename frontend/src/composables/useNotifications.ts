import { ref, watch } from 'vue'

const LS_NOTIF = 'boss_notifications_enabled'
const LS_SOUND = 'boss_sound_enabled'
const LS_THEME = 'boss_sound_theme'

export const notificationsEnabled = ref(
  localStorage.getItem(LS_NOTIF) !== 'false',
)

// Sounds are OFF by default — must be explicitly enabled in settings.
export const soundEnabled = ref(
  localStorage.getItem(LS_SOUND) === 'true',
)

export type SoundTheme = 'classic' | 'retro' | 'space' | 'nature'

export const soundTheme = ref<SoundTheme>(
  (localStorage.getItem(LS_THEME) as SoundTheme) || 'classic',
)

export const SOUND_THEMES: { id: SoundTheme; label: string; description: string }[] = [
  { id: 'classic', label: 'Classic',     description: 'Clean sine-wave chords' },
  { id: 'retro',   label: 'Retro 8-bit', description: 'Chiptune square waves (Game Boy vibes)' },
  { id: 'space',   label: 'Spaceship',   description: 'Sci-fi bleeps and swoops' },
  { id: 'nature',  label: 'Nature',      description: 'Soft triangle-wave tones' },
]

watch(notificationsEnabled, (v) => localStorage.setItem(LS_NOTIF, String(v)))
watch(soundEnabled,         (v) => localStorage.setItem(LS_SOUND, String(v)))
watch(soundTheme,           (v) => localStorage.setItem(LS_THEME, v))

// ── Volume ─────────────────────────────────────────────────────────────────
const LS_VOLUME = 'boss_sound_volume'
export const soundVolume = ref<number>(
  parseFloat(localStorage.getItem(LS_VOLUME) ?? '0.7'),
)
watch(soundVolume, (v) => localStorage.setItem(LS_VOLUME, String(v)))

// ── Per-category toggles ───────────────────────────────────────────────────
const LS_CATEGORIES = 'boss_sound_categories'

export type SoundCategory = 'urgent' | 'events' | 'celebrations' | 'ambient' | 'social'

export const SOUND_CATEGORY_META: { id: SoundCategory; label: string; description: string; defaultOn: boolean }[] = [
  { id: 'urgent',       label: 'Urgent',       description: 'Blocked/error alerts',                      defaultOn: true  },
  { id: 'events',       label: 'Events',       description: 'Task transitions, spawn, PR shipped',        defaultOn: true  },
  { id: 'celebrations', label: 'Celebrations', description: 'Task done, sprint complete',                 defaultOn: true  },
  { id: 'ambient',      label: 'Ambient',      description: 'Activity ticks (server-room ambience)',       defaultOn: false },
  { id: 'social',       label: 'Social',       description: 'Messages, @mention pings, collaboration',    defaultOn: true  },
]

const defaultCategories: Record<SoundCategory, boolean> = {
  urgent: true, events: true, celebrations: true, ambient: false, social: true,
}

function loadCategories(): Record<SoundCategory, boolean> {
  try {
    const stored = localStorage.getItem(LS_CATEGORIES)
    if (stored) return { ...defaultCategories, ...(JSON.parse(stored) as Partial<Record<SoundCategory, boolean>>) }
  } catch { /* ignore */ }
  return { ...defaultCategories }
}

export const soundCategories = ref<Record<SoundCategory, boolean>>(loadCategories())
watch(soundCategories, (v) => localStorage.setItem(LS_CATEGORIES, JSON.stringify(v)), { deep: true })

export function isCategoryEnabled(cat: SoundCategory): boolean {
  return soundEnabled.value && soundCategories.value[cat]
}

export async function requestNotificationPermission(): Promise<boolean> {
  if (!('Notification' in window)) return false
  if (Notification.permission === 'granted') return true
  if (Notification.permission === 'denied') return false
  const result = await Notification.requestPermission()
  return result === 'granted'
}

// ── Low-level synth helpers ────────────────────────────────────────────────

function tone(
  ctx: AudioContext,
  freq: number,
  startAt: number,
  duration: number,
  volume = 0.08,
  type: OscillatorType = 'sine',
): void {
  const osc = ctx.createOscillator()
  const gain = ctx.createGain()
  osc.connect(gain)
  gain.connect(ctx.destination)
  osc.type = type
  osc.frequency.setValueAtTime(freq, startAt)
  gain.gain.setValueAtTime(volume * soundVolume.value, startAt)
  gain.gain.exponentialRampToValueAtTime(0.001, startAt + duration)
  osc.start(startAt)
  osc.stop(startAt + duration + 0.05)
}

function sweep(
  ctx: AudioContext,
  freqStart: number,
  freqEnd: number,
  startAt: number,
  duration: number,
  volume = 0.07,
  type: OscillatorType = 'sine',
): void {
  const osc = ctx.createOscillator()
  const gain = ctx.createGain()
  osc.connect(gain)
  gain.connect(ctx.destination)
  osc.type = type
  osc.frequency.setValueAtTime(freqStart, startAt)
  osc.frequency.exponentialRampToValueAtTime(freqEnd, startAt + duration)
  gain.gain.setValueAtTime(volume * soundVolume.value, startAt)
  gain.gain.exponentialRampToValueAtTime(0.001, startAt + duration)
  osc.start(startAt)
  osc.stop(startAt + duration + 0.05)
}

// ── Theme-aware sound functions ────────────────────────────────────────────

// Message arrival chime — also used as preview (ignores soundEnabled)
export function playChime(): void {
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value

    if (theme === 'retro') {
      tone(ctx, 880, t,        0.08, 0.06, 'square')
      tone(ctx, 660, t + 0.10, 0.12, 0.06, 'square')
    } else if (theme === 'space') {
      sweep(ctx, 1200, 600, t, 0.25, 0.07, 'sine')
    } else if (theme === 'nature') {
      tone(ctx, 880, t,        0.3, 0.05, 'triangle')
      tone(ctx, 660, t + 0.15, 0.3, 0.05, 'triangle')
    } else {
      // Classic: 880→660 sine glide
      const osc = ctx.createOscillator()
      const gain = ctx.createGain()
      osc.connect(gain)
      gain.connect(ctx.destination)
      osc.type = 'sine'
      osc.frequency.setValueAtTime(880, t)
      osc.frequency.exponentialRampToValueAtTime(660, t + 0.15)
      gain.gain.setValueAtTime(0.08 * soundVolume.value, t)
      gain.gain.exponentialRampToValueAtTime(0.001, t + 0.4)
      osc.start(t)
      osc.stop(t + 0.45)
    }

    setTimeout(() => ctx.close(), 1000)
  } catch {
    // AudioContext not available
  }
}

// Task-done success chord.
// priority='critical' (#4 Boss Level): adds an ascending run before the chord for extra fanfare.
export function playSuccess(priority?: string): void {
  if (!isCategoryEnabled('celebrations')) return
  const isCritical = priority === 'critical'
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value
    // Critical-priority head-start: ascending run (C5→G5→C6) gives a "Boss Level" feeling
    const offset = isCritical ? 0.38 : 0
    if (isCritical && !prefersReducedMotion) {
      if (theme === 'retro') {
        tone(ctx, 523,  t,        0.09, effectiveVolume(0.065), 'square')
        tone(ctx, 784,  t + 0.10, 0.09, effectiveVolume(0.065), 'square')
        tone(ctx, 1047, t + 0.22, 0.1,  effectiveVolume(0.075), 'square')
      } else if (theme === 'space') {
        sweep(ctx, 300, 1400, t, 0.32, effectiveVolume(0.07), 'sine')
      } else {
        // Classic/Nature: short C5→G5→C6 arpeggio lead-in
        const wave: OscillatorType = theme === 'nature' ? 'triangle' : 'sine'
        tone(ctx, 523.25, t,        0.12, effectiveVolume(0.055), wave)
        tone(ctx, 783.99, t + 0.13, 0.12, effectiveVolume(0.055), wave)
        tone(ctx, 1046.5, t + 0.26, 0.1,  effectiveVolume(0.065), wave)
      }
    }

    if (theme === 'retro') {
      tone(ctx, 262, t + offset,        0.12, 0.07, 'square') // C4
      tone(ctx, 330, t + offset + 0.10, 0.12, 0.07, 'square') // E4
      tone(ctx, 392, t + offset + 0.20, 0.12, 0.07, 'square') // G4
      tone(ctx, 523, t + offset + 0.30, 0.22, 0.09, 'square') // C5 held
    } else if (theme === 'space') {
      sweep(ctx, 400,  800,  t + offset,        0.15, 0.07, 'sine')
      sweep(ctx, 800,  1200, t + offset + 0.18, 0.25, 0.08, 'sine')
    } else if (theme === 'nature') {
      tone(ctx, 523.25, t + offset,        0.6,  0.05, 'triangle') // C5
      tone(ctx, 659.25, t + offset + 0.12, 0.55, 0.05, 'triangle') // E5
      tone(ctx, 783.99, t + offset + 0.24, 0.5,  0.05, 'triangle') // G5
    } else {
      // Classic: C major triad (C5, E5, G5)
      tone(ctx, 523.25, t + offset,        0.5)  // C5
      tone(ctx, 659.25, t + offset + 0.08, 0.45) // E5
      tone(ctx, 783.99, t + offset + 0.16, 0.4)  // G5
    }

    setTimeout(() => ctx.close(), isCritical ? 2000 : 1500)
  } catch {
    // AudioContext not available
  }
}

// All-agents-idle "sprint complete" fanfare
export function playSprintComplete(): void {
  if (!isCategoryEnabled('celebrations')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value

    if (theme === 'retro') {
      // Classic video game victory run
      const notes = [262, 330, 392, 523, 659, 784, 1047]
      notes.forEach((freq, i) => {
        tone(ctx, freq, t + i * 0.08, i === notes.length - 1 ? 0.6 : 0.1, 0.07, 'square')
      })
    } else if (theme === 'space') {
      // Warp jump: sweep then sustained chord
      sweep(ctx, 200,  1600, t,        0.3,  0.08, 'sine')
      tone(ctx,  440,        t + 0.35, 0.7,  0.07) // A4
      tone(ctx,  554.37,     t + 0.35, 0.7,  0.06) // C#5
      tone(ctx,  659.25,     t + 0.35, 0.7,  0.06) // E5
    } else if (theme === 'nature') {
      // Soft chime cascade
      const freqs = [523.25, 659.25, 783.99, 1046.5]
      freqs.forEach((freq, i) => {
        tone(ctx, freq, t + i * 0.15, 0.7 - i * 0.1, 0.05, 'triangle')
      })
    } else {
      // Classic: ascending arpeggio A4→C5→E5→A5
      tone(ctx, 440,    t,        0.35, 0.07) // A4
      tone(ctx, 523.25, t + 0.12, 0.35, 0.07) // C5
      tone(ctx, 659.25, t + 0.24, 0.35, 0.07) // E5
      tone(ctx, 880,    t + 0.36, 0.55, 0.09) // A5 held
    }

    setTimeout(() => ctx.close(), 2000)
  } catch {
    // AudioContext not available
  }
}

// ── Agent signature chimes ─────────────────────────────────────────────────
// Each agent gets a unique 2-note "voice" from their name hash.
// Plays once per page-load per agent on their first status update.
// Uses a pentatonic scale so every chord sounds harmonious regardless of hash.

const PENTATONIC_HZ = [
  261.63, 293.66, 329.63, 392.00, 440.00,  // C4 D4 E4 G4 A4
  523.25, 587.33, 659.25, 783.99, 880.00,  // C5 D5 E5 G5 A5
]

function hashName(name: string): number {
  let h = 5381
  for (let i = 0; i < name.length; i++) h = (h * 33 + name.charCodeAt(i)) >>> 0
  return h
}

const _chimePlayed = new Set<string>()

function _playAgentVoice(agentName: string): void {
  const ctx = new AudioContext()
  const t = ctx.currentTime
  const h = hashName(agentName)
  const root = PENTATONIC_HZ[h % PENTATONIC_HZ.length]!
  // Major-third partner (ratio 5:4) — always consonant
  const partner = root * 1.25

  const waveforms: OscillatorType[] = ['sine', 'triangle']
  const wave = waveforms[(h >> 4) % waveforms.length] as OscillatorType

  // Micro-variation: ±5% pitch + ±15ms timing offset so each play feels organic
  const pitchVariation = 0.975 + Math.random() * 0.05
  const timingVariation = Math.random() * 0.015

  tone(ctx, root * pitchVariation,    t + timingVariation,        0.35, 0.055, wave)
  tone(ctx, partner * pitchVariation, t + 0.06 + timingVariation, 0.30, 0.045, wave)

  setTimeout(() => ctx.close(), 800)
}

export function playAgentSignatureChime(agentName: string): void {
  if (!isCategoryEnabled('social')) return
  if (_chimePlayed.has(agentName)) return
  _chimePlayed.add(agentName)
  try { _playAgentVoice(agentName) } catch { /* AudioContext not available */ }
}

/** Play an agent's voice on demand (for profile preview button — always plays, ignores once-per-session guard). */
export function previewAgentVoice(agentName: string): void {
  if (!soundEnabled.value) return
  try { _playAgentVoice(agentName) } catch { /* AudioContext not available */ }
}

// Reset chimes on space navigation so agents get their chime each new session
export function resetAgentChimes(): void {
  _chimePlayed.clear()
}

// ── Activity tick ──────────────────────────────────────────────────────────
// Micro white-noise burst on each SSE agent_updated event.
// Creates "busy server room" ambience. Off by default.

const LS_TICK = 'boss_activity_tick_enabled'
export const activityTickEnabled = ref(
  localStorage.getItem(LS_TICK) === 'true',
)
watch(activityTickEnabled, (v) => localStorage.setItem(LS_TICK, String(v)))

export function playActivityTick(): void {
  if (!activityTickEnabled.value) return
  try {
    const ctx = new AudioContext()
    const bufSize = ctx.sampleRate * 0.004 // 4ms
    const buffer = ctx.createBuffer(1, bufSize, ctx.sampleRate)
    const data = buffer.getChannelData(0)
    for (let i = 0; i < bufSize; i++) data[i] = (Math.random() * 2 - 1)
    const src = ctx.createBufferSource()
    src.buffer = buffer
    const gain = ctx.createGain()
    gain.gain.value = 0.012
    src.connect(gain)
    gain.connect(ctx.destination)
    src.start()
    setTimeout(() => ctx.close(), 200)
  } catch {
    // AudioContext not available
  }
}

// ── #7 Heartbeat Mode — agent-personality tick ─────────────────────────────
// Each agent's tick is a 3ms micro-tone at their pentatonic frequency instead
// of uniform white noise. Active fleets sound like a chord of working agents.
export function playAgentTick(agentName: string): void {
  if (!activityTickEnabled.value) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const freq = PENTATONIC_HZ[hashName(agentName) % PENTATONIC_HZ.length]!
    tone(ctx, freq, t, 0.003, 0.008 * soundVolume.value, 'sine') // 3ms micro-tone
    setTimeout(() => ctx.close(), 100)
  } catch { /* AudioContext not available */ }
}

// ── Reduced-motion awareness ────────────────────────────────────────────────
const prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches

function effectiveVolume(base: number): number {
  return prefersReducedMotion ? base * 0.4 : base
}

// ── #2 Dissonance Flag — blocked/error alert ───────────────────────────────
// Minor second interval (two adjacent semitones) — tense but not alarming.
export function playBlockedAlert(): void {
  if (!isCategoryEnabled('urgent')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const vol = effectiveVolume(0.12)
    tone(ctx, 440,    t,       vol,          0.07, 'triangle') // A4
    tone(ctx, 466.16, t + 0.01, vol * 1.1,  0.06, 'sine')     // A#4 — minor second
    setTimeout(() => ctx.close(), 500)
  } catch { /* AudioContext not available */ }
}

// ── #6 Warp Arrival — agent spawned ───────────────────────────────────────
export function playAgentSpawn(): void {
  if (!isCategoryEnabled('events')) return
  if (prefersReducedMotion) return // skip sweeps in reduced-motion mode
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    sweep(ctx, 200, 1200, t, 0.25, effectiveVolume(0.08), 'sine')
    tone(ctx, 1200, t + 0.28, 0.35, effectiveVolume(0.05), 'triangle')
    setTimeout(() => ctx.close(), 800)
  } catch { /* AudioContext not available */ }
}

// ── #3 The Arc — task column transitions ──────────────────────────────────
// backlog→in_progress: rising sweep ("starting")
// in_progress→review: suspended 2nd chord ("waiting")
// review→done / any→done: playSuccess() (already wired at call site)
export function playTaskTransition(toStatus: string): void {
  if (!isCategoryEnabled('events')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    if (toStatus === 'in_progress') {
      if (prefersReducedMotion) {
        tone(ctx, 523.25, t, 0.2, effectiveVolume(0.06))
      } else {
        sweep(ctx, 330, 523, t, 0.2, effectiveVolume(0.06), 'sine')
      }
    } else if (toStatus === 'review') {
      tone(ctx, 523.25, t,       0.4, effectiveVolume(0.055)) // C5
      tone(ctx, 587.33, t + 0.05, 0.4, effectiveVolume(0.045)) // D5 — suspended 2nd
    }
    setTimeout(() => ctx.close(), 800)
  } catch { /* AudioContext not available */ }
}

// ── #9 @mention ping ───────────────────────────────────────────────────────
// Distinct short ping — higher pitch than message chime, percussive attack.
export function playMentionPing(): void {
  if (!isCategoryEnabled('social')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    tone(ctx, 1046.5, t, 0.15, effectiveVolume(0.09), 'sine') // C6 — bright, short
    setTimeout(() => ctx.close(), 400)
  } catch { /* AudioContext not available */ }
}

export function notifyBossMessage(from: string, spaceName: string): void {
  if (isCategoryEnabled('social')) playChime()

  if (!notificationsEnabled.value) return
  if (!('Notification' in window) || Notification.permission !== 'granted') return
  if (!document.hidden) return

  new Notification(`New message from ${from}`, {
    body: `Workspace: ${spaceName}`,
    icon: '/favicon.ico',
    tag: `boss-msg-${from}`,
  })
}

// ── #10 PR Shipped — agent sets a PR link ──────────────────────────────────
// Descending whoosh (600→300Hz) + brief landing tone. "Code out the door."
export function playPRShipped(): void {
  if (!isCategoryEnabled('events')) return
  if (prefersReducedMotion) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    sweep(ctx, 700, 350, t,       0.2, effectiveVolume(0.07), 'sine')
    tone(ctx,  350, t + 0.22, 0.25, effectiveVolume(0.04), 'triangle')
    setTimeout(() => ctx.close(), 600)
  } catch { /* AudioContext not available */ }
}

// ── #8 Collaboration Harmony — two agents conversing ──────────────────────
// Both agents' pentatonic voices play as a chord with a slight timing offset.
export function playCollaborationChord(senderName: string, receiverName: string): void {
  if (!isCategoryEnabled('social')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const freqA = PENTATONIC_HZ[hashName(senderName)   % PENTATONIC_HZ.length]!
    const freqB = PENTATONIC_HZ[hashName(receiverName) % PENTATONIC_HZ.length]!
    tone(ctx, freqA, t,        0.3, effectiveVolume(0.04), 'sine')
    tone(ctx, freqB, t + 0.02, 0.3, effectiveVolume(0.04), 'triangle') // slight offset = conversation feel
    setTimeout(() => ctx.close(), 600)
  } catch { /* AudioContext not available */ }
}

// ── #5 Agent Moods — status transition voice variants ─────────────────────
// Each agent's pentatonic root frequency played in ascending or descending
// intervals to convey "waking up" vs "settling down" — completing the arc.
// Uses the same pentatonic hash so moods are tonally consistent with chimes.

export function playAgentMoodActive(agentName: string): void {
  if (!isCategoryEnabled('events')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const root = PENTATONIC_HZ[hashName(agentName) % PENTATONIC_HZ.length]!
    const fifth = root * 1.498 // perfect fifth (3:2 ratio) — energizing, upward
    // Ascending: root → fifth, short and punchy
    tone(ctx, root,  t,       0.18, effectiveVolume(0.038), 'sine')
    tone(ctx, fifth, t + 0.1, 0.16, effectiveVolume(0.038), 'triangle')
    setTimeout(() => ctx.close(), 500)
  } catch { /* AudioContext not available */ }
}

export function playAgentMoodIdle(agentName: string): void {
  if (!isCategoryEnabled('events')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const root = PENTATONIC_HZ[hashName(agentName) % PENTATONIC_HZ.length]!
    const fifth = root * 1.498
    // Descending: fifth → root, slower and softer — "settling down"
    tone(ctx, fifth, t,       0.22, effectiveVolume(0.028), 'triangle')
    tone(ctx, root,  t + 0.13, 0.28, effectiveVolume(0.022), 'sine')
    setTimeout(() => ctx.close(), 600)
  } catch { /* AudioContext not available */ }
}
