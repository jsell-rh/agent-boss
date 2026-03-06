<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    name: string
    size?: number
  }>(),
  { size: 32 },
)

const PALETTE = [
  ['#c80000', '#ff6b6b'], // red
  ['#b07800', '#ffc53d'], // amber
  ['#007070', '#00bfbf'], // teal
  ['#3d1fa0', '#7c5ccf'], // purple
  ['#b04800', '#f07830'], // orange
  ['#1260cc', '#4ba8e8'], // blue
  ['#1e6b14', '#52b84a'], // green
  ['#800000', '#cc3030'], // dark red
  ['#5a28c8', '#a878f0'], // violet
  ['#007a9a', '#00cce0'], // cyan
]

// Produce a good 32-bit hash from a string
function hashCode(str: string): number {
  let h = 0x811c9dc5 // FNV offset basis
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i)
    h = Math.imul(h, 0x01000193) // FNV prime
  }
  return h >>> 0 // unsigned
}

// Get multiple independent hash values from one seed
function multiHash(name: string, count: number): number[] {
  const hashes: number[] = []
  for (let i = 0; i < count; i++) {
    hashes.push(hashCode(name + String.fromCharCode(65 + i)))
  }
  return hashes
}

const SHAPE_TYPES = ['circle', 'square', 'diamond', 'triangle', 'hexagon', 'semicircle']

function hexagonPoints(cx: number, cy: number, r: number): string {
  const pts: string[] = []
  for (let i = 0; i < 6; i++) {
    const angle = (Math.PI / 3) * i - Math.PI / 6
    pts.push(`${cx + r * Math.cos(angle)},${cy + r * Math.sin(angle)}`)
  }
  return pts.join(' ')
}

function trianglePoints(cx: number, cy: number, r: number): string {
  return [
    `${cx},${cy - r}`,
    `${cx - r * 0.866},${cy + r * 0.5}`,
    `${cx + r * 0.866},${cy + r * 0.5}`,
  ].join(' ')
}

function semicirclePath(cx: number, cy: number, r: number): string {
  return `M ${cx - r},${cy} A ${r},${r} 0 1,1 ${cx + r},${cy} Z`
}

const avatar = computed(() => {
  const h = multiHash(props.name, 9)
  const s = props.size

  // Pick 3 distinct color pairs
  const c1 = PALETTE[h[0]! % PALETTE.length]!
  const c2 = PALETTE[(h[1]! % (PALETTE.length - 1) + 1 + (h[0]! % PALETTE.length)) % PALETTE.length]!
  const c3 = PALETTE[(h[2]! % (PALETTE.length - 2) + 2 + (h[0]! % PALETTE.length)) % PALETTE.length]!

  // Background gradient angle
  const bgAngle = h[3]! % 360

  type ShapeData = {
    type: string
    cx: number
    cy: number
    r: number
    fill: string
    rotation: number
    hexPoints: string
    triPoints: string
    semiPath: string
  }

  const shapes: ShapeData[] = []

  // Shape 1: Large background shape
  const s1Type = SHAPE_TYPES[h[4]! % SHAPE_TYPES.length]!
  const cx1 = s * 0.5
  const cy1 = s * 0.5
  const r1 = s * (0.38 + (h[4]! % 10) / 100)
  shapes.push({
    type: s1Type,
    cx: cx1,
    cy: cy1,
    r: r1,
    fill: c1[0]!,
    rotation: h[5]! % 60,
    hexPoints: hexagonPoints(cx1, cy1, r1),
    triPoints: trianglePoints(cx1, cy1, r1),
    semiPath: semicirclePath(cx1, cy1, r1),
  })

  // Shape 2: Medium accent shape, offset
  const s2Type = SHAPE_TYPES[(h[5]! + 2) % SHAPE_TYPES.length]!
  const s2Angle = (h[5]! % 628) / 100
  const s2Dist = s * (0.08 + (h[6]! % 15) / 100)
  const cx2 = s * 0.5 + Math.cos(s2Angle) * s2Dist
  const cy2 = s * 0.5 + Math.sin(s2Angle) * s2Dist
  const r2 = s * (0.22 + (h[6]! % 8) / 100)
  shapes.push({
    type: s2Type,
    cx: cx2,
    cy: cy2,
    r: r2,
    fill: c2[0]!,
    rotation: h[6]! % 90,
    hexPoints: hexagonPoints(cx2, cy2, r2),
    triPoints: trianglePoints(cx2, cy2, r2),
    semiPath: semicirclePath(cx2, cy2, r2),
  })

  // Shape 3: Small detail shape
  const s3Type = SHAPE_TYPES[(h[7]! + 4) % SHAPE_TYPES.length]!
  const s3Angle = ((h[7]! + 314) % 628) / 100
  const s3Dist = s * (0.12 + (h[7]! % 12) / 100)
  const cx3 = s * 0.5 + Math.cos(s3Angle) * s3Dist
  const cy3 = s * 0.5 + Math.sin(s3Angle) * s3Dist
  const r3 = s * (0.12 + (h[7]! % 6) / 100)
  shapes.push({
    type: s3Type,
    cx: cx3,
    cy: cy3,
    r: r3,
    fill: c3[1]!,
    rotation: h[7]! % 180,
    hexPoints: hexagonPoints(cx3, cy3, r3),
    triPoints: trianglePoints(cx3, cy3, r3),
    semiPath: semicirclePath(cx3, cy3, r3),
  })

  return {
    bgColor1: c1[1]!,
    bgColor2: c2[1]!,
    bgAngle,
    shapes,
  }
})

const gradientId = computed(() => `ag-${hashCode(props.name)}-${props.size}`)
const vignetteId = computed(() => `vg-${hashCode(props.name)}-${props.size}`)
const radius = computed(() => props.size * 0.15)
</script>

<template>
  <svg
    :width="size"
    :height="size"
    :viewBox="`0 0 ${size} ${size}`"
    :aria-label="`Avatar for ${name}`"
    role="img"
    class="inline-block shrink-0 rounded-md"
  >
    <defs>
      <!-- Background gradient — higher opacity for clearer contrast -->
      <linearGradient
        :id="gradientId"
        gradientUnits="objectBoundingBox"
        :gradientTransform="`rotate(${avatar.bgAngle}, 0.5, 0.5)`"
      >
        <stop offset="0%" :stop-color="avatar.bgColor1" stop-opacity="0.55" />
        <stop offset="100%" :stop-color="avatar.bgColor2" stop-opacity="0.40" />
      </linearGradient>
      <!-- Vignette overlay for depth -->
      <radialGradient :id="vignetteId" cx="50%" cy="40%" r="70%">
        <stop offset="0%" stop-color="white" stop-opacity="0.10" />
        <stop offset="100%" stop-color="black" stop-opacity="0.25" />
      </radialGradient>
      <clipPath :id="`clip-${gradientId}`">
        <rect x="0" y="0" :width="size" :height="size" :rx="radius" :ry="radius" />
      </clipPath>
    </defs>

    <g :clip-path="`url(#clip-${gradientId})`">
      <!-- Background gradient -->
      <rect x="0" y="0" :width="size" :height="size" :fill="`url(#${gradientId})`" />

      <!-- Geometric shapes -->
      <template v-for="(shape, i) in avatar.shapes" :key="i">
        <circle
          v-if="shape.type === 'circle'"
          :cx="shape.cx"
          :cy="shape.cy"
          :r="shape.r"
          :fill="shape.fill"
          opacity="0.92"
        />
        <rect
          v-else-if="shape.type === 'square'"
          :x="shape.cx - shape.r"
          :y="shape.cy - shape.r"
          :width="shape.r * 2"
          :height="shape.r * 2"
          :fill="shape.fill"
          opacity="0.92"
          :transform="`rotate(${shape.rotation} ${shape.cx} ${shape.cy})`"
        />
        <rect
          v-else-if="shape.type === 'diamond'"
          :x="shape.cx - shape.r"
          :y="shape.cy - shape.r"
          :width="shape.r * 2"
          :height="shape.r * 2"
          :fill="shape.fill"
          opacity="0.92"
          :transform="`rotate(${45 + shape.rotation} ${shape.cx} ${shape.cy})`"
        />
        <polygon
          v-else-if="shape.type === 'triangle'"
          :points="shape.triPoints"
          :fill="shape.fill"
          opacity="0.92"
          :transform="`rotate(${shape.rotation} ${shape.cx} ${shape.cy})`"
        />
        <polygon
          v-else-if="shape.type === 'hexagon'"
          :points="shape.hexPoints"
          :fill="shape.fill"
          opacity="0.92"
          :transform="`rotate(${shape.rotation} ${shape.cx} ${shape.cy})`"
        />
        <path
          v-else-if="shape.type === 'semicircle'"
          :d="shape.semiPath"
          :fill="shape.fill"
          opacity="0.92"
          :transform="`rotate(${shape.rotation} ${shape.cx} ${shape.cy})`"
        />
      </template>

      <!-- Depth vignette overlay -->
      <rect x="0" y="0" :width="size" :height="size" :fill="`url(#${vignetteId})`" />
    </g>

    <!-- Thin border for definition against card backgrounds -->
    <rect
      x="0.5"
      y="0.5"
      :width="size - 1"
      :height="size - 1"
      :rx="radius"
      :ry="radius"
      fill="none"
      stroke="rgba(255,255,255,0.30)"
      stroke-width="1"
    />
  </svg>
</template>
