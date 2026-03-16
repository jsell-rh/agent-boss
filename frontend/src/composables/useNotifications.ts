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
  dest?: AudioNode,
): void {
  const osc = ctx.createOscillator()
  const gain = ctx.createGain()
  osc.connect(gain)
  gain.connect(dest ?? ctx.destination)
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
  dest?: AudioNode,
): void {
  const osc = ctx.createOscillator()
  const gain = ctx.createGain()
  osc.connect(gain)
  gain.connect(dest ?? ctx.destination)
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

// 4-dimension agent voice system — 4×5×3×2 = 120 distinct voices, all pentatonically consonant.
// Dimension 1: Waveform  (h % 4)       — sine, triangle, square, sawtooth
// Dimension 2: Interval  ((h>>4) % 5)  — major 3rd, P4, P5, major 6th, octave
// Dimension 3: Envelope  ((h>>8) % 3)  — pluck, sustained, staccato
// Dimension 4: Register  ((h>>12) % 2) — upper register, lower register (−1 octave)
// Idea D — Stereo position as agent identity: each agent has a consistent pan position
function _agentPan(h: number): number {
  return ((h >> 20) % 101 / 100) * 1.2 - 0.6 // -0.6 (left) to +0.6 (right)
}

function _playAgentVoice(agentName: string): void {
  const ctx = new AudioContext()
  const t = ctx.currentTime
  const h = hashName(agentName)

  // Dim 1 — Waveform
  const waveforms: OscillatorType[] = ['sine', 'triangle', 'square', 'sawtooth']
  const wave = waveforms[h % 4] as OscillatorType
  // Square/sawtooth are brighter — lower their volume so perceived loudness stays consistent
  const waveVol = (wave === 'square' || wave === 'sawtooth') ? 0.038 : 0.055

  // Dim 2 — Interval (ratio above root)
  const intervals = [1.25, 1.333, 1.498, 1.667, 2.0] // M3, P4, P5, M6, octave
  const interval = intervals[(h >> 4) % 5]!

  // Dim 3 — Envelope type
  const envelopeType = (h >> 8) % 3 // 0=pluck, 1=sustained, 2=staccato

  // Dim 4 — Register
  const registerShift = (h >> 12) % 2 // 0=upper, 1=lower (half freq)
  const baseIdx = h % PENTATONIC_HZ.length
  const root = PENTATONIC_HZ[baseIdx]! * (registerShift === 0 ? 1.0 : 0.5)
  const partner = root * interval

  // Micro-variation: ±8 cents pitch drift + up to 18ms timing humanization
  const centsDrift = Math.pow(2, (Math.random() * 16 - 8) / 1200)
  const timeHuman = Math.random() * 0.018

  // Optional 8% grace note (a semitone above root, very brief)
  const hasGrace = Math.random() < 0.08

  // Idea D — Stereo position: each agent has a consistent pan (-0.6…+0.6)
  const panner = ctx.createStereoPanner()
  panner.pan.value = _agentPan(h)
  panner.connect(ctx.destination)

  if (envelopeType === 0) {
    // Pluck: fast attack, medium decay (~350ms)
    if (hasGrace) tone(ctx, root * centsDrift * 1.059, t + timeHuman - 0.03, 0.04, waveVol * 0.5, wave, panner)
    tone(ctx, root    * centsDrift, t + timeHuman,        0.35, waveVol,        wave, panner)
    tone(ctx, partner * centsDrift, t + 0.06 + timeHuman, 0.30, waveVol * 0.82, wave, panner)
  } else if (envelopeType === 1) {
    // Sustained: slower attack, longer ring (~550ms)
    if (hasGrace) tone(ctx, root * centsDrift * 1.059, t + timeHuman - 0.03, 0.04, waveVol * 0.5, wave, panner)
    tone(ctx, root    * centsDrift, t + timeHuman,        0.55, waveVol * 0.82, wave, panner)
    tone(ctx, partner * centsDrift, t + 0.08 + timeHuman, 0.50, waveVol * 0.68, wave, panner)
  } else {
    // Staccato: very short punchy notes + brief echo
    tone(ctx, root    * centsDrift, t + timeHuman,        0.10, waveVol * 1.1,  wave, panner)
    tone(ctx, partner * centsDrift, t + 0.06 + timeHuman, 0.10, waveVol * 0.95, wave, panner)
    // Echo at half volume, offset by ~160ms
    tone(ctx, root    * centsDrift, t + 0.18 + timeHuman, 0.08, waveVol * 0.4,  wave, panner)
  }

  setTimeout(() => ctx.close(), 900)
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

// Idea K — Whisper Hour: automatically quieter between 10pm and 7am
function _whisperMultiplier(): number {
  const h = new Date().getHours()
  return (h >= 22 || h < 7) ? 0.3 : 1.0
}

function effectiveVolume(base: number): number {
  return base * (prefersReducedMotion ? 0.4 : 1.0) * _whisperMultiplier()
}

// ── Idea J — Repeating blocked pulse ──────────────────────────────────────
// Plays a soft single-note reminder every 30s while an agent stays blocked.
const _blockedPulseMap = new Map<string, ReturnType<typeof setInterval>>()
const BLOCKED_PULSE_MS = 30_000

export function startBlockedPulse(agentKey: string): void {
  if (_blockedPulseMap.has(agentKey)) return
  const id = setInterval(() => {
    if (!isCategoryEnabled('urgent')) return
    try {
      const ctx = new AudioContext()
      const t = ctx.currentTime
      // Single dissonant tone — softer than the initial alert (reminder, not alarm)
      tone(ctx, 466.16, t, 0.06, effectiveVolume(0.05), 'sine')
      setTimeout(() => ctx.close(), 200)
    } catch { /* AudioContext not available */ }
  }, BLOCKED_PULSE_MS)
  _blockedPulseMap.set(agentKey, id)
}

export function stopBlockedPulse(agentKey: string): void {
  const id = _blockedPulseMap.get(agentKey)
  if (id !== undefined) {
    clearInterval(id)
    _blockedPulseMap.delete(agentKey)
  }
}

// ── #2 Dissonance Flag — blocked/error alert ───────────────────────────────
// Minor second interval (two adjacent semitones) — tense but not alarming.
export function playBlockedAlert(): void {
  if (!isCategoryEnabled('urgent')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value
    const vol = effectiveVolume(0.12)

    // Idea I — 7Hz tremolo LFO: makes the alert feel urgent and alive
    const tremolo = ctx.createGain()
    tremolo.gain.value = 0.82
    const lfo = ctx.createOscillator()
    const lfoGain = ctx.createGain()
    lfo.frequency.value = 7
    lfoGain.gain.value = 0.18
    lfo.connect(lfoGain)
    lfoGain.connect(tremolo.gain)
    tremolo.connect(ctx.destination)
    lfo.start(t)
    lfo.stop(t + 0.55)

    if (theme === 'retro') {
      // Chiptune minor second: E4 + F4 square
      tone(ctx, 329.63, t,        vol,         0.08, 'square', tremolo)
      tone(ctx, 349.23, t + 0.01, vol * 1.1,  0.07, 'square', tremolo)
    } else if (theme === 'space') {
      // Descending alarm sweep + dissonant overlay
      sweep(ctx, 600, 200, t, 0.3, effectiveVolume(0.09), 'sine', tremolo)
      tone(ctx, 220, t + 0.05, 0.2, effectiveVolume(0.06), 'sine', tremolo)
    } else if (theme === 'nature') {
      // Softer dissonance: B4 + C5 triangle (gentler but still tense)
      tone(ctx, 493.88, t,        vol * 0.8, 0.1, 'triangle', tremolo)
      tone(ctx, 523.25, t + 0.01, vol * 0.9, 0.1, 'triangle', tremolo)
    } else {
      // Classic: A4 triangle + A#4 sine — minor second
      tone(ctx, 440,    t,        vol,         0.07, 'triangle', tremolo)
      tone(ctx, 466.16, t + 0.01, vol * 1.1,  0.06, 'sine',     tremolo)
    }
    setTimeout(() => ctx.close(), 600)
  } catch { /* AudioContext not available */ }
}

// ── #6 Warp Arrival — agent spawned ───────────────────────────────────────
export function playAgentSpawn(): void {
  if (!isCategoryEnabled('events')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value
    if (theme === 'retro') {
      // Chiptune ascending arpeggio — "new player entered"
      tone(ctx, 261.63, t,        0.08, effectiveVolume(0.07), 'square') // C4
      tone(ctx, 392.00, t + 0.09, 0.08, effectiveVolume(0.07), 'square') // G4
      tone(ctx, 523.25, t + 0.18, 0.18, effectiveVolume(0.08), 'square') // C5
    } else if (theme === 'space') {
      // Massive sci-fi warp jump
      if (!prefersReducedMotion) {
        sweep(ctx, 80, 2000, t, 0.3, effectiveVolume(0.09), 'sine')
        tone(ctx, 1400, t + 0.33, 0.35, effectiveVolume(0.05), 'triangle')
      } else {
        tone(ctx, 880, t, 0.3, effectiveVolume(0.06), 'sine')
      }
    } else if (theme === 'nature') {
      // Gentle ascending C4→G4→C5 triangle tones — no sweep
      tone(ctx, 261.63, t,        0.4,  effectiveVolume(0.04), 'triangle') // C4
      tone(ctx, 392.00, t + 0.15, 0.35, effectiveVolume(0.04), 'triangle') // G4
      tone(ctx, 523.25, t + 0.30, 0.45, effectiveVolume(0.05), 'triangle') // C5
    } else {
      // Classic: upward sine sweep + triangle landing
      if (!prefersReducedMotion) {
        sweep(ctx, 200, 1200, t, 0.25, effectiveVolume(0.08), 'sine')
        tone(ctx, 1200, t + 0.28, 0.35, effectiveVolume(0.05), 'triangle')
      } else {
        tone(ctx, 1200, t, 0.35, effectiveVolume(0.05), 'triangle')
      }
    }
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
    const theme = soundTheme.value
    if (toStatus === 'in_progress') {
      if (theme === 'retro') {
        // Octave jump — punchy "go!" signal
        tone(ctx, 261.63, t,       0.06, effectiveVolume(0.07), 'square') // C4
        tone(ctx, 523.25, t + 0.08, 0.12, effectiveVolume(0.08), 'square') // C5
      } else if (theme === 'space') {
        if (!prefersReducedMotion) {
          sweep(ctx, 200, 700, t, 0.22, effectiveVolume(0.07), 'sine')
        } else {
          tone(ctx, 659.25, t, 0.2, effectiveVolume(0.06), 'sine')
        }
      } else if (theme === 'nature') {
        tone(ctx, 392.00, t,       0.25, effectiveVolume(0.05), 'triangle') // G4
        tone(ctx, 523.25, t + 0.12, 0.3, effectiveVolume(0.05), 'triangle') // C5
      } else {
        // Classic: rising sine sweep
        if (!prefersReducedMotion) {
          sweep(ctx, 330, 523, t, 0.2, effectiveVolume(0.06), 'sine')
        } else {
          tone(ctx, 523.25, t, 0.2, effectiveVolume(0.06))
        }
      }
    } else if (toStatus === 'review') {
      if (theme === 'retro') {
        tone(ctx, 523.25, t,        0.4, effectiveVolume(0.055), 'square') // C5
        tone(ctx, 587.33, t + 0.05, 0.4, effectiveVolume(0.045), 'square') // D5
      } else if (theme === 'space') {
        // Minor third — more ambiguous "awaiting signal" feel
        tone(ctx, 523.25, t,        0.5, effectiveVolume(0.055), 'sine') // C5
        tone(ctx, 622.25, t + 0.05, 0.5, effectiveVolume(0.045), 'sine') // D#5
      } else if (theme === 'nature') {
        tone(ctx, 523.25, t,        0.5, effectiveVolume(0.05), 'triangle') // C5
        tone(ctx, 587.33, t + 0.05, 0.5, effectiveVolume(0.04), 'triangle') // D5
      } else {
        // Classic: C5 + D5 suspended second
        tone(ctx, 523.25, t,        0.4, effectiveVolume(0.055)) // C5
        tone(ctx, 587.33, t + 0.05, 0.4, effectiveVolume(0.045)) // D5
      }
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
    const theme = soundTheme.value
    if (theme === 'retro') {
      // High blip: E6 square — very chiptune
      tone(ctx, 1318.51, t, 0.1, effectiveVolume(0.08), 'square')
    } else if (theme === 'space') {
      // Rising blip: fast ascending sweep
      sweep(ctx, 800, 1600, t, 0.12, effectiveVolume(0.08), 'sine')
    } else if (theme === 'nature') {
      // Softer, slightly lower: G5 triangle — still distinct
      tone(ctx, 783.99, t, 0.2, effectiveVolume(0.07), 'triangle')
    } else {
      // Classic: C6 sine — bright, short
      tone(ctx, 1046.5, t, 0.15, effectiveVolume(0.09), 'sine')
    }
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
// Descending whoosh + brief landing tone. "Code out the door."
export function playPRShipped(): void {
  if (!isCategoryEnabled('events')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value
    if (theme === 'retro') {
      // Descending square arpeggio — "shipped" fanfare
      tone(ctx, 659.25, t,        0.06, effectiveVolume(0.07), 'square') // E5
      tone(ctx, 523.25, t + 0.08, 0.06, effectiveVolume(0.07), 'square') // C5
      tone(ctx, 392.00, t + 0.16, 0.12, effectiveVolume(0.08), 'square') // G4
    } else if (theme === 'space') {
      // Mega-whoosh: 1200→150Hz warp-out + landing blip
      if (!prefersReducedMotion) {
        sweep(ctx, 1200, 150, t, 0.28, effectiveVolume(0.08), 'sine')
        tone(ctx, 220, t + 0.30, 0.3, effectiveVolume(0.04), 'triangle')
      } else {
        tone(ctx, 440, t, 0.25, effectiveVolume(0.06), 'sine')
      }
    } else if (theme === 'nature') {
      // Gentle descending: G5→C5 triangle cascade
      tone(ctx, 783.99, t,        0.35, effectiveVolume(0.04), 'triangle') // G5
      tone(ctx, 659.25, t + 0.14, 0.35, effectiveVolume(0.04), 'triangle') // E5
      tone(ctx, 523.25, t + 0.28, 0.4,  effectiveVolume(0.05), 'triangle') // C5
    } else {
      // Classic: descending sine whoosh + landing tone
      if (!prefersReducedMotion) {
        sweep(ctx, 700, 350, t,       0.2, effectiveVolume(0.07), 'sine')
        tone(ctx,  350, t + 0.22, 0.25, effectiveVolume(0.04), 'triangle')
      } else {
        tone(ctx, 392, t, 0.3, effectiveVolume(0.05), 'triangle')
      }
    }
    setTimeout(() => ctx.close(), 700)
  } catch { /* AudioContext not available */ }
}

// ── #8 Collaboration Harmony — two agents conversing ──────────────────────
// Both agents' pentatonic voices play as a chord with a slight timing offset.
export function playCollaborationChord(senderName: string, receiverName: string): void {
  if (!isCategoryEnabled('social')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value
    const freqA = PENTATONIC_HZ[hashName(senderName)   % PENTATONIC_HZ.length]!
    const freqB = PENTATONIC_HZ[hashName(receiverName) % PENTATONIC_HZ.length]!
    if (theme === 'retro') {
      tone(ctx, freqA, t,        0.25, effectiveVolume(0.04), 'square')
      tone(ctx, freqB, t + 0.03, 0.25, effectiveVolume(0.04), 'square')
    } else if (theme === 'space') {
      // Slightly wider timing gap — signals crossing in space
      tone(ctx, freqA, t,        0.35, effectiveVolume(0.035), 'sine')
      tone(ctx, freqB, t + 0.04, 0.35, effectiveVolume(0.035), 'sine')
    } else if (theme === 'nature') {
      tone(ctx, freqA, t,        0.35, effectiveVolume(0.04), 'triangle')
      tone(ctx, freqB, t + 0.02, 0.35, effectiveVolume(0.04), 'triangle')
    } else {
      // Classic: sine sender, triangle receiver — conversation feel
      tone(ctx, freqA, t,        0.3, effectiveVolume(0.04), 'sine')
      tone(ctx, freqB, t + 0.02, 0.3, effectiveVolume(0.04), 'triangle')
    }
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
    const theme = soundTheme.value
    const root = PENTATONIC_HZ[hashName(agentName) % PENTATONIC_HZ.length]!
    const fifth = root * 1.498 // perfect fifth (3:2 ratio) — energizing, upward
    if (theme === 'retro') {
      tone(ctx, root,  t,       0.1,  effectiveVolume(0.04), 'square')
      tone(ctx, fifth, t + 0.1, 0.1,  effectiveVolume(0.04), 'square')
    } else if (theme === 'space') {
      // Rising micro-sweep to root, then fifth — "powering up"
      if (!prefersReducedMotion) {
        sweep(ctx, root * 0.7, root, t, 0.1, effectiveVolume(0.035), 'sine')
        tone(ctx, fifth, t + 0.12, 0.18, effectiveVolume(0.035), 'triangle')
      } else {
        tone(ctx, fifth, t, 0.18, effectiveVolume(0.038), 'sine')
      }
    } else if (theme === 'nature') {
      tone(ctx, root,  t,       0.22, effectiveVolume(0.036), 'triangle')
      tone(ctx, fifth, t + 0.1, 0.2,  effectiveVolume(0.036), 'triangle')
    } else {
      // Classic: ascending root→fifth, sine then triangle
      tone(ctx, root,  t,       0.18, effectiveVolume(0.038), 'sine')
      tone(ctx, fifth, t + 0.1, 0.16, effectiveVolume(0.038), 'triangle')
    }
    setTimeout(() => ctx.close(), 500)
  } catch { /* AudioContext not available */ }
}

export function playAgentMoodIdle(agentName: string): void {
  if (!isCategoryEnabled('events')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value
    const root = PENTATONIC_HZ[hashName(agentName) % PENTATONIC_HZ.length]!
    const fifth = root * 1.498
    if (theme === 'retro') {
      tone(ctx, fifth, t,        0.12, effectiveVolume(0.03), 'square')
      tone(ctx, root,  t + 0.13, 0.14, effectiveVolume(0.025), 'square')
    } else if (theme === 'space') {
      // Descending fade — "going offline"
      if (!prefersReducedMotion) {
        sweep(ctx, fifth, root * 0.7, t + 0.05, 0.25, effectiveVolume(0.025), 'sine')
      } else {
        tone(ctx, root, t, 0.28, effectiveVolume(0.022), 'sine')
      }
    } else if (theme === 'nature') {
      tone(ctx, fifth, t,        0.28, effectiveVolume(0.026), 'triangle')
      tone(ctx, root,  t + 0.13, 0.32, effectiveVolume(0.02),  'triangle')
    } else {
      // Classic: descending fifth→root, triangle then sine — "settling down"
      tone(ctx, fifth, t,        0.22, effectiveVolume(0.028), 'triangle')
      tone(ctx, root,  t + 0.13, 0.28, effectiveVolume(0.022), 'sine')
    }
    setTimeout(() => ctx.close(), 600)
  } catch { /* AudioContext not available */ }
}
