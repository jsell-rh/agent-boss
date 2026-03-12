import { ref, watch } from 'vue'

const LS_NOTIF = 'boss_notifications_enabled'
const LS_SOUND = 'boss_sound_enabled'

export const notificationsEnabled = ref(
  localStorage.getItem(LS_NOTIF) !== 'false',
)

// Sounds are OFF by default — must be explicitly enabled in settings.
// Default is 'false' unless the user has previously set it to 'true'.
export const soundEnabled = ref(
  localStorage.getItem(LS_SOUND) === 'true',
)

watch(notificationsEnabled, (v) => localStorage.setItem(LS_NOTIF, String(v)))
watch(soundEnabled, (v) => localStorage.setItem(LS_SOUND, String(v)))

export async function requestNotificationPermission(): Promise<boolean> {
  if (!('Notification' in window)) return false
  if (Notification.permission === 'granted') return true
  if (Notification.permission === 'denied') return false
  const result = await Notification.requestPermission()
  return result === 'granted'
}

// Helper to create a short synthesized tone. All sounds are generated via
// Web Audio API — no audio files required.
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

// Message arrival chime: descending two-note 880→660 Hz sine.
export function playChime(): void {
  try {
    const ctx = new AudioContext()
    const osc = ctx.createOscillator()
    const gain = ctx.createGain()
    osc.connect(gain)
    gain.connect(ctx.destination)
    osc.type = 'sine'
    osc.frequency.setValueAtTime(880, ctx.currentTime)
    osc.frequency.exponentialRampToValueAtTime(660, ctx.currentTime + 0.15)
    gain.gain.setValueAtTime(0.08, ctx.currentTime)
    gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + 0.4)
    osc.start(ctx.currentTime)
    osc.stop(ctx.currentTime + 0.45)
    setTimeout(() => ctx.close(), 1000)
  } catch {
    // AudioContext not available
  }
}

// Task-done success chord: C major triad (C5, E5, G5) — three notes 80ms apart.
export function playSuccess(): void {
  if (!soundEnabled.value) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    tone(ctx, 523.25, t,        0.5) // C5
    tone(ctx, 659.25, t + 0.08, 0.45) // E5
    tone(ctx, 783.99, t + 0.16, 0.4)  // G5
    setTimeout(() => ctx.close(), 1500)
  } catch {
    // AudioContext not available
  }
}

// All-agents-idle "sprint complete" fanfare: ascending arpeggio A4→C5→E5→A5.
export function playSprintComplete(): void {
  if (!soundEnabled.value) return
  try {
    const ctx = new AudioContext()
    const t = ctx.currentTime
    tone(ctx, 440,    t,        0.35, 0.07) // A4
    tone(ctx, 523.25, t + 0.12, 0.35, 0.07) // C5
    tone(ctx, 659.25, t + 0.24, 0.35, 0.07) // E5
    tone(ctx, 880,    t + 0.36, 0.55, 0.09) // A5 — held longer
    setTimeout(() => ctx.close(), 2000)
  } catch {
    // AudioContext not available
  }
}

export function notifyBossMessage(from: string, spaceName: string): void {
  if (soundEnabled.value) playChime()

  if (!notificationsEnabled.value) return
  if (!('Notification' in window) || Notification.permission !== 'granted') return
  if (!document.hidden) return // only notify when tab is not focused

  new Notification(`New message from ${from}`, {
    body: `Workspace: ${spaceName}`,
    icon: '/favicon.ico',
    tag: `boss-msg-${from}`, // deduplicates rapid messages from same sender
  })
}
