/**
 * useConfetti — pure canvas confetti burst.
 * No dependencies. Fires and forgets — never blocks task state.
 * Respects prefers-reduced-motion.
 */

const COLORS = [
  '#ff6b6b', '#ffa94d', '#ffd43b', '#69db7c',
  '#74c0fc', '#da77f2', '#f783ac', '#63e6be',
  '#a9e34b', '#ff8787',
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

export function useConfetti() {
  function celebrate(originX?: number, originY?: number) {
    // Always clean up any existing confetti canvas first
    document.querySelector('.boss-confetti-canvas')?.remove()

    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    const count = reduced ? 30 : 65

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

    const particles: Particle[] = Array.from({ length: count }, () => {
      const angle = rand(-Math.PI, 0) // fan upward
      const speed = rand(4, 12)
      return {
        x: cx,
        y: cy,
        vx: Math.cos(angle) * speed,
        vy: Math.sin(angle) * speed - rand(2, 5),
        rotation: rand(0, Math.PI * 2),
        rotationSpeed: reduced ? 0 : rand(-0.2, 0.2),
        color: COLORS[Math.floor(Math.random() * COLORS.length)]!,
        width: rand(6, 12),
        height: rand(4, 8),
        opacity: 1,
      }
    })

    const DURATION = 1800
    const start = performance.now()
    let raf: number

    function frame(now: number) {
      const elapsed = now - start
      const progress = elapsed / DURATION

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

      if (elapsed < DURATION) {
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
