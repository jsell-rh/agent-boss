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

// ── Ambient preemption + agent debounce ───────────────────────────────────
// Ambient ticks are silenced while any event/social/urgent cue is playing.
// Per-agent debounce drops duplicate cues within 200ms (prevents stack-up in busy fleets).

let _activeCueCount = 0
const _agentLastCueTs = new Map<string, number>()
const DEBOUNCE_MS = 200

function _agentDebounceOk(agentName: string): boolean {
  const now = Date.now()
  const last = _agentLastCueTs.get(agentName) ?? 0
  if (now - last < DEBOUNCE_MS) return false
  _agentLastCueTs.set(agentName, now)
  return true
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

// Bandpass-filtered noise burst — the structural marker for "communication event".
// Perceptually distinct from any melodic/pentatonic identity voice (arch requirement).
function _noiseBurst(
  ctx: AudioContext,
  t: number,
  cutoffHz = 800,
  vol = 0.06,
  durationS = 0.025,
  dest?: AudioNode,
): void {
  const bufSize = Math.ceil(ctx.sampleRate * durationS)
  const buffer = ctx.createBuffer(1, bufSize, ctx.sampleRate)
  const data = buffer.getChannelData(0)
  for (let i = 0; i < bufSize; i++) data[i] = Math.random() * 2 - 1
  const src = ctx.createBufferSource()
  src.buffer = buffer
  const filt = ctx.createBiquadFilter()
  filt.type = 'bandpass'
  filt.frequency.value = cutoffHz
  filt.Q.value = 2.5
  const gain = ctx.createGain()
  gain.gain.setValueAtTime(vol * soundVolume.value, t)
  gain.gain.exponentialRampToValueAtTime(0.001, t + durationS)
  src.connect(filt)
  filt.connect(gain)
  gain.connect(dest ?? ctx.destination)
  src.start(t)
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

// ── Idea C — Palette guarantee for first 16 agents ─────────────────────────
// Hash can map two agents to identical (wave, interval) combos. For the first
// 16 agents seen in a space we assign from this curated palette of 16 unique
// (waveform × interval) pairs — guaranteeing no two visible agents share the
// same timbre. Agents beyond slot 16 fall back to hash-derived dims.
const VOICE_PALETTE: Array<{ wave: OscillatorType; interval: number }> = [
  { wave: 'sine',     interval: 1.498 }, // P5  — open, stable
  { wave: 'triangle', interval: 1.25  }, // M3  — warm, gentle
  { wave: 'square',   interval: 2.0   }, // 8ve — bold, retro
  { wave: 'sawtooth', interval: 1.333 }, // P4  — edgy, bright
  { wave: 'sine',     interval: 1.25  }, // M3  — sweet
  { wave: 'triangle', interval: 1.667 }, // M6  — expansive
  { wave: 'square',   interval: 1.333 }, // P4  — punchy
  { wave: 'sawtooth', interval: 1.498 }, // P5  — aggressive
  { wave: 'sine',     interval: 1.667 }, // M6  — warm, full
  { wave: 'triangle', interval: 2.0   }, // 8ve — airy
  { wave: 'square',   interval: 1.25  }, // M3  — crisp
  { wave: 'sawtooth', interval: 2.0   }, // 8ve — rich, buzzy
  { wave: 'sine',     interval: 1.333 }, // P4  — round
  { wave: 'triangle', interval: 1.498 }, // P5  — ethereal
  { wave: 'square',   interval: 1.667 }, // M6  — clear
  { wave: 'sawtooth', interval: 1.25  }, // M3  — raw
]

const _seenAgentsOrdered: string[] = []

function _getPaletteEntry(agentName: string): { wave: OscillatorType; interval: number } | null {
  if (!_seenAgentsOrdered.includes(agentName) && _seenAgentsOrdered.length < 16) {
    _seenAgentsOrdered.push(agentName)
  }
  const slot = _seenAgentsOrdered.indexOf(agentName)
  return slot >= 0 ? VOICE_PALETTE[slot]! : null
}

// 5-dimension agent voice system — 4×5×3×2×4 = 480 distinct voices.
// Dimension 1: Waveform  (palette slot or h % 4)   — sine, triangle, square, sawtooth
// Dimension 2: Interval  (palette slot or (h>>4)%5) — M3, P4, P5, M6, octave
// Dimension 3: Envelope  ((h>>8) % 3)              — pluck, sustained, staccato
// Dimension 4: Register  ((h>>12) % 2)             — upper or lower register
// Dimension 5: Rhythm    ((h>>16) % 4)             — single, double-tap, triplet, call+response
// Idea D — Stereo position as agent identity: each agent has a consistent pan position
function _agentPan(h: number): number {
  return ((h >> 20) % 101 / 100) * 1.2 - 0.6 // -0.6 (left) to +0.6 (right)
}

// Play one envelope phrase (root + partner at a given start time).
function _playPhrase(
  ctx: AudioContext,
  t: number,
  root: number,
  partner: number,
  centsDrift: number,
  wave: OscillatorType,
  waveVol: number,
  envelopeType: number,
  hasGrace: boolean,
  panner: AudioNode,
): void {
  const th = Math.random() * 0.018 // per-phrase timing humanization
  if (envelopeType === 0) {
    // Pluck: fast attack, medium decay (~350ms)
    if (hasGrace) tone(ctx, root * centsDrift * 1.059, t + th - 0.03, 0.04, waveVol * 0.5, wave, panner)
    tone(ctx, root    * centsDrift, t + th,        0.35, waveVol,        wave, panner)
    tone(ctx, partner * centsDrift, t + 0.06 + th, 0.30, waveVol * 0.82, wave, panner)
  } else if (envelopeType === 1) {
    // Sustained: slower attack, longer ring (~550ms)
    if (hasGrace) tone(ctx, root * centsDrift * 1.059, t + th - 0.03, 0.04, waveVol * 0.5, wave, panner)
    tone(ctx, root    * centsDrift, t + th,        0.55, waveVol * 0.82, wave, panner)
    tone(ctx, partner * centsDrift, t + 0.08 + th, 0.50, waveVol * 0.68, wave, panner)
  } else {
    // Staccato: very short punchy notes + echo
    tone(ctx, root    * centsDrift, t + th,        0.10, waveVol * 1.1,  wave, panner)
    tone(ctx, partner * centsDrift, t + 0.06 + th, 0.10, waveVol * 0.95, wave, panner)
    tone(ctx, root    * centsDrift, t + 0.18 + th, 0.08, waveVol * 0.4,  wave, panner)
  }
}

// Play an agent's 5D voice inside an existing AudioContext at a specified start time.
// Accepts volumeMult for scaling (e.g. recipient = 0.5) and invertPan for recipient side.
// Used by both standalone playback and grammar-wired sequences.
function _voiceInCtx(
  ctx: AudioContext,
  startAt: number,
  agentName: string,
  volumeMult = 1.0,
  invertPan = false,
): void {
  const h = hashName(agentName)
  const palette = _getPaletteEntry(agentName)
  const waveforms: OscillatorType[] = ['sine', 'triangle', 'square', 'sawtooth']
  const wave: OscillatorType = palette ? palette.wave : (waveforms[h % 4] as OscillatorType)
  const waveVol = ((wave === 'square' || wave === 'sawtooth') ? 0.038 : 0.055) * volumeMult
  const intervals = [1.25, 1.333, 1.498, 1.667, 2.0]
  const interval: number = palette ? palette.interval : intervals[(h >> 4) % 5]!
  const envelopeType = (h >> 8) % 3
  const registerShift = (h >> 12) % 2
  const root = PENTATONIC_HZ[h % PENTATONIC_HZ.length]! * (registerShift === 0 ? 1.0 : 0.5)
  const partner = root * interval
  const centsDrift = Math.pow(2, (Math.random() * 16 - 8) / 1200)
  const hasGrace = Math.random() < 0.08

  const panner = ctx.createStereoPanner()
  panner.pan.value = invertPan ? -_agentPan(h) : _agentPan(h)
  panner.connect(ctx.destination)

  const rhythmType = (h >> 16) % 4
  if (rhythmType === 0) {
    _playPhrase(ctx, startAt,        root, partner, centsDrift,         wave, waveVol,        envelopeType, hasGrace, panner)
  } else if (rhythmType === 1) {
    _playPhrase(ctx, startAt,        root, partner, centsDrift,         wave, waveVol,        envelopeType, hasGrace, panner)
    _playPhrase(ctx, startAt + 0.13, root, partner, centsDrift * 1.002, wave, waveVol * 0.72, envelopeType, false,    panner)
  } else if (rhythmType === 2) {
    tone(ctx, root    * centsDrift, startAt,        0.14, waveVol,        wave, panner)
    tone(ctx, partner * centsDrift, startAt + 0.08, 0.14, waveVol * 0.88, wave, panner)
    tone(ctx, root    * centsDrift, startAt + 0.16, 0.12, waveVol * 0.68, wave, panner)
  } else {
    tone(ctx, root    * centsDrift, startAt,        0.22, waveVol * 1.1, wave, panner)
    tone(ctx, partner * centsDrift, startAt + 0.30, 0.20, waveVol * 0.7, wave, panner)
  }
}

function _playAgentVoice(agentName: string): void {
  const ctx = new AudioContext()
  _voiceInCtx(ctx, ctx.currentTime, agentName)
  setTimeout(() => ctx.close(), 1100)
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

// Reset chimes on space navigation so agents get their chime each new session.
// Also resets the palette registry so the first 16 agents in the new space
// get fresh, collision-free timbre assignments.
export function resetAgentChimes(): void {
  _chimePlayed.clear()
  _seenAgentsOrdered.length = 0
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
  if (_activeCueCount > 0) return // ambient preempted while event/social/urgent cues are active
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
  if (_activeCueCount > 0) return // ambient preempted
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
    _activeCueCount++
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
    setTimeout(() => { ctx.close(); _activeCueCount-- }, 650)
  } catch { _activeCueCount-- /* AudioContext not available */ }
}

// ── #6 Warp Arrival — agent spawned ───────────────────────────────────────
// agentName: when provided, appends the agent's identity voice after the warp cue (grammar).
export function playAgentSpawn(agentName?: string): void {
  if (!isCategoryEnabled('events')) return
  try {
    _activeCueCount++
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value
    let warpDur = 0.3 // duration of warp action cue

    if (theme === 'retro') {
      tone(ctx, 261.63, t,        0.08, effectiveVolume(0.07), 'square') // C4
      tone(ctx, 392.00, t + 0.09, 0.08, effectiveVolume(0.07), 'square') // G4
      tone(ctx, 523.25, t + 0.18, 0.18, effectiveVolume(0.08), 'square') // C5
      warpDur = 0.36
    } else if (theme === 'space') {
      if (!prefersReducedMotion) {
        sweep(ctx, 80, 2000, t, 0.3, effectiveVolume(0.09), 'sine')
        tone(ctx, 1400, t + 0.33, 0.35, effectiveVolume(0.05), 'triangle')
        warpDur = 0.68
      } else {
        tone(ctx, 880, t, 0.3, effectiveVolume(0.06), 'sine')
        warpDur = 0.3
      }
    } else if (theme === 'nature') {
      tone(ctx, 261.63, t,        0.4,  effectiveVolume(0.04), 'triangle') // C4
      tone(ctx, 392.00, t + 0.15, 0.35, effectiveVolume(0.04), 'triangle') // G4
      tone(ctx, 523.25, t + 0.30, 0.45, effectiveVolume(0.05), 'triangle') // C5
      warpDur = 0.75
    } else {
      if (!prefersReducedMotion) {
        sweep(ctx, 200, 1200, t, 0.25, effectiveVolume(0.08), 'sine')
        tone(ctx, 1200, t + 0.28, 0.35, effectiveVolume(0.05), 'triangle')
        warpDur = 0.63
      } else {
        tone(ctx, 1200, t, 0.35, effectiveVolume(0.05), 'triangle')
        warpDur = 0.35
      }
    }

    // Grammar: append agent's identity voice after 100ms gap
    if (agentName) _voiceInCtx(ctx, t + warpDur + 0.1, agentName)

    const totalDur = agentName ? warpDur + 0.1 + 1.1 : warpDur
    setTimeout(() => { ctx.close(); _activeCueCount-- }, (totalDur + 0.3) * 1000)
  } catch { _activeCueCount-- /* AudioContext not available */ }
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
      // Noise-burst attack before the held chord — distinguishes action cue from identity voice
      _noiseBurst(ctx, t, 600, effectiveVolume(0.04), 0.02)
      const chord = t + 0.025 // chord starts after noise burst
      if (theme === 'retro') {
        tone(ctx, 523.25, chord,        0.4, effectiveVolume(0.055), 'square') // C5
        tone(ctx, 587.33, chord + 0.05, 0.4, effectiveVolume(0.045), 'square') // D5
      } else if (theme === 'space') {
        tone(ctx, 523.25, chord,        0.5, effectiveVolume(0.055), 'sine') // C5
        tone(ctx, 622.25, chord + 0.05, 0.5, effectiveVolume(0.045), 'sine') // D#5
      } else if (theme === 'nature') {
        tone(ctx, 523.25, chord,        0.5, effectiveVolume(0.05), 'triangle') // C5
        tone(ctx, 587.33, chord + 0.05, 0.5, effectiveVolume(0.04), 'triangle') // D5
      } else {
        // Classic: C5 + D5 suspended second
        tone(ctx, 523.25, chord,        0.4, effectiveVolume(0.055)) // C5
        tone(ctx, 587.33, chord + 0.05, 0.4, effectiveVolume(0.045)) // D5
      }
    }
    setTimeout(() => ctx.close(), 800)
  } catch { /* AudioContext not available */ }
}

// ── #9 @mention ping — grammar-wired ──────────────────────────────────────
// Action ping (percussive/sweep) + sender identity voice + optional recipient voice.
// Urgent gap = 50ms (not 100ms) so the identity lands sooner, reinforcing urgency.
// senderName: who sent the @mention. recipientName: who was mentioned (50% softer, inverted pan).
export function playMentionPing(senderName?: string, recipientName?: string): void {
  if (!isCategoryEnabled('social')) return
  try {
    _activeCueCount++
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const theme = soundTheme.value
    let pingDur = 0.12 // action cue duration

    // Action cue: must use sweep or percussive texture (structural distinguishability rule)
    if (theme === 'retro') {
      tone(ctx, 1318.51, t, 0.1, effectiveVolume(0.08), 'square') // high blip
      pingDur = 0.1
    } else if (theme === 'space') {
      sweep(ctx, 800, 1600, t, 0.12, effectiveVolume(0.08), 'sine')
      pingDur = 0.12
    } else if (theme === 'nature') {
      tone(ctx, 783.99, t, 0.15, effectiveVolume(0.07), 'triangle')
      pingDur = 0.15
    } else {
      // Classic: ascending sweep (fixes static C6 — now structurally distinct from identity voices)
      sweep(ctx, 600, 1200, t, 0.08, effectiveVolume(0.09), 'sine')
      pingDur = 0.08
    }

    // Grammar: sender voice at t + pingDur + 0.05 (50ms urgent gap)
    const voiceStart = t + pingDur + 0.05
    if (senderName) _voiceInCtx(ctx, voiceStart, senderName, 1.0, false)
    // Recipient voice: 40ms after sender, 50% softer, opposite pan (@mentions only)
    if (recipientName) _voiceInCtx(ctx, voiceStart + 0.04, recipientName, 0.5, true)

    const totalDur = (senderName || recipientName) ? pingDur + 0.05 + 1.1 + 0.04 : pingDur
    setTimeout(() => { ctx.close(); _activeCueCount-- }, (totalDur + 0.3) * 1000)
  } catch { _activeCueCount-- /* AudioContext not available */ }
}

// ── Agent message — action cue + sender identity voice (grammar) ───────────
// Replaces playCollaborationChord for agent→agent messages.
// Uses bandpass noise-burst as action cue (communication = percussive texture, not tones).
export function playAgentMessage(senderName: string): void {
  if (!isCategoryEnabled('social')) return
  if (!_agentDebounceOk(senderName)) return
  try {
    _activeCueCount++
    const ctx = new AudioContext()
    const t = ctx.currentTime
    // Action cue: noise-burst (structurally distinct from any melodic identity voice)
    _noiseBurst(ctx, t, 900, effectiveVolume(0.055), 0.025)
    // Grammar: sender's identity voice after 100ms gap
    _voiceInCtx(ctx, t + 0.025 + 0.1, senderName, 1.0, false)
    setTimeout(() => { ctx.close(); _activeCueCount-- }, 1500)
  } catch { _activeCueCount-- /* AudioContext not available */ }
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

// Idle cue — single soft fade with no directional sweep (audio-sme requirement).
// Descending patterns were confused with identity voices; this is ambient-tier volume only.
export function playAgentMoodIdle(agentName: string): void {
  if (!isCategoryEnabled('events')) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    const root = PENTATONIC_HZ[hashName(agentName) % PENTATONIC_HZ.length]!
    tone(ctx, root, t, 0.07, effectiveVolume(0.018), 'sine')
    setTimeout(() => ctx.close(), 300)
  } catch { /* AudioContext not available */ }
}
