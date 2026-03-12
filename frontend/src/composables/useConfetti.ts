/**
 * useConfetti — pure canvas confetti burst.
 * No dependencies. Fires and forgets — never blocks task state.
 * Respects prefers-reduced-motion.
 *
 * Priority variations:
 *   critical → gold palette, 2× particles, wider blast
 *   high     → 1.4× particles, slight spread increase
 *   medium   → standard burst
 *   low      → lighter burst
 */

const COLORS_DEFAULT = [
  '#ff6b6b', '#ffa94d', '#ffd43b', '#69db7c',
  '#74c0fc', '#da77f2', '#f783ac', '#63e6be',
  '#a9e34b', '#ff8787',
]

const COLORS_CRITICAL = [
  '#ffd700', '#ffb800', '#ff9500', '#ffe066',
  '#ffec99', '#ffa94d', '#ff6b35', '#fcc419',
  '#fab005', '#f59f00',
]

interface Particle {
  x: number
  y: number
  vx: number
  vy: number
  rotation: number
  rotationSpeed: number
  color: string
  width: number
  height: number
  opacity: number
}

function rand(min: number, max: number): number {
  return min + Math.random() * (max - min)
}

export type ConfettiPriority = 'low' | 'medium' | 'high' | 'critical'

export function useConfetti() {
  function celebrate(originX?: number, originY?: number, priority: ConfettiPriority = 'medium') {
    // Always clean up any existing confetti canvas first
    document.querySelector('.boss-confetti-canvas')?.remove()

    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

    // Priority-based tuning
    const isCritical = priority === 'critical'
    const isHigh = priority === 'high'
    const isLow = priority === 'low'
    const baseCount = reduced ? 25 : isCritical ? 130 : isHigh ? 90 : isLow ? 40 : 65
    const speedMult = isCritical ? 1.5 : isHigh ? 1.2 : 1
    const colors = isCritical ? COLORS_CRITICAL : COLORS_DEFAULT
    const duration = isCritical ? 2400 : 1800

    const canvas = document.createElement('canvas')
    canvas.className = 'boss-confetti-canvas'
    canvas.style.cssText = [
      'position:fixed',
      'inset:0',
      'width:100%',
      'height:100%',
      'pointer-events:none',
      'z-index:9999',
    ].join(';')
    canvas.width = window.innerWidth
    canvas.height = window.innerHeight
    document.body.appendChild(canvas)

    const ctx = canvas.getContext('2d')!
    const cx = originX ?? canvas.width / 2
    const cy = originY ?? canvas.height * 0.4

    const particles: Particle[] = Array.from({ length: baseCount }, () => {
      const angle = rand(-Math.PI, 0) // fan upward
      const speed = rand(4, 12) * speedMult
      return {
        x: cx,
        y: cy,
        vx: Math.cos(angle) * speed,
        vy: Math.sin(angle) * speed - rand(2, 5) * speedMult,
        rotation: rand(0, Math.PI * 2),
        rotationSpeed: reduced ? 0 : rand(-0.2, 0.2),
        color: colors[Math.floor(Math.random() * colors.length)]!,
        width: rand(isCritical ? 8 : 6, isCritical ? 16 : 12),
        height: rand(isCritical ? 5 : 4, isCritical ? 10 : 8),
        opacity: 1,
      }
    })

    const start = performance.now()
    let raf: number

    function frame(now: number) {
      const elapsed = now - start
      const progress = elapsed / duration

      ctx.clearRect(0, 0, canvas.width, canvas.height)

      for (const p of particles) {
        p.vy += 0.25 // gravity
        p.vx *= 0.99 // slight drag
        p.x += p.vx
        p.y += p.vy
        p.rotation += p.rotationSpeed
        p.opacity = Math.max(0, 1 - progress * 1.3)

        ctx.save()
        ctx.globalAlpha = p.opacity
        ctx.translate(p.x, p.y)
        ctx.rotate(p.rotation)
        ctx.fillStyle = p.color
        ctx.fillRect(-p.width / 2, -p.height / 2, p.width, p.height)
        ctx.restore()
      }

      if (elapsed < duration) {
        raf = requestAnimationFrame(frame)
      } else {
        canvas.remove()
      }
    }

    raf = requestAnimationFrame(frame)

    // Safety cleanup in case component unmounts early
    return () => {
      cancelAnimationFrame(raf)
      canvas.remove()
    }
  }

  return { celebrate }
}
