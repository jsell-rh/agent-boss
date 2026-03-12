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
  gain.gain.setValueAtTime(volume, startAt)
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
  gain.gain.setValueAtTime(volume, startAt)
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
      gain.gain.setValueAtTime(0.08, t)
      gain.gain.exponentialRampToValueAtTime(0.001, t + 0.4)
      osc.start(t)
      osc.stop(t + 0.45)
    }

    setTimeout(() => ctx.close(), 1000)
  } catch {
    // AudioContext not available
  }
}

// Task-done success chord
export function playSuccess(): void {
  if (!soundEnabled.value) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value

    if (theme === 'retro') {
      // Chiptune ascending arpeggio
      tone(ctx, 262, t,        0.12, 0.07, 'square') // C4
      tone(ctx, 330, t + 0.10, 0.12, 0.07, 'square') // E4
      tone(ctx, 392, t + 0.20, 0.12, 0.07, 'square') // G4
      tone(ctx, 523, t + 0.30, 0.22, 0.09, 'square') // C5 held
    } else if (theme === 'space') {
      sweep(ctx, 400,  800,  t,        0.15, 0.07, 'sine')
      sweep(ctx, 800,  1200, t + 0.18, 0.25, 0.08, 'sine')
    } else if (theme === 'nature') {
      tone(ctx, 523.25, t,        0.6,  0.05, 'triangle') // C5
      tone(ctx, 659.25, t + 0.12, 0.55, 0.05, 'triangle') // E5
      tone(ctx, 783.99, t + 0.24, 0.5,  0.05, 'triangle') // G5
    } else {
      // Classic: C major triad (C5, E5, G5)
      tone(ctx, 523.25, t,        0.5)  // C5
      tone(ctx, 659.25, t + 0.08, 0.45) // E5
      tone(ctx, 783.99, t + 0.16, 0.4)  // G5
    }

    setTimeout(() => ctx.close(), 1500)
  } catch {
    // AudioContext not available
  }
}

// All-agents-idle "sprint complete" fanfare
export function playSprintComplete(): void {
  if (!soundEnabled.value) return
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

export function playAgentSignatureChime(agentName: string): void {
  if (!soundEnabled.value) return
  if (_chimePlayed.has(agentName)) return
  _chimePlayed.add(agentName)

  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const h = hashName(agentName)
    const root = PENTATONIC_HZ[h % PENTATONIC_HZ.length]!
    // Major-third partner (ratio 5:4) — always consonant
    const partner = root * 1.25

    const waveforms: OscillatorType[] = ['sine', 'triangle']
    const wave = waveforms[(h >> 4) % waveforms.length] as OscillatorType

    tone(ctx, root,    t,        0.35, 0.055, wave)
    tone(ctx, partner, t + 0.06, 0.30, 0.045, wave)

    setTimeout(() => ctx.close(), 800)
  } catch {
    // AudioContext not available
  }
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

export function notifyBossMessage(from: string, spaceName: string): void {
  if (soundEnabled.value) playChime()

  if (!notificationsEnabled.value) return
  if (!('Notification' in window) || Notification.permission !== 'granted') return
  if (!document.hidden) return

  new Notification(`New message from ${from}`, {
    body: `Workspace: ${spaceName}`,
    icon: '/favicon.ico',
    tag: `boss-msg-${from}`,
  })
}
