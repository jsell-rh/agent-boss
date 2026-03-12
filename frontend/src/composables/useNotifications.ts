import { ref, watch } from 'vue'

const LS_NOTIF = 'boss_notifications_enabled'
const LS_SOUND = 'boss_sound_enabled'

export const notificationsEnabled = ref(
  localStorage.getItem(LS_NOTIF) !== 'false',
)

export const soundEnabled = ref(
  localStorage.getItem(LS_SOUND) !== 'false',
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
    gain.gain.setValueAtTime(0.25, ctx.currentTime)
    gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + 0.4)
    osc.start(ctx.currentTime)
    osc.stop(ctx.currentTime + 0.4)
    setTimeout(() => ctx.close(), 1000)
  } catch {
    // AudioContext not available in this environment
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
