<script setup lang="ts">
import type { SpotArtName } from '~/utils/art'

// 店里物件的小插画（DESIGN.md 插画）：照 Notion 插画（Roman Muradov）的手绘墨线——线条带一点手的抖动，
// 背后一块错开放的不规则标签色块，叠一层网点；物件本身白底。只做装饰，读屏跳过。
const props = defineProps<{ name: SpotArtName }>()

// 每幅的色块：标签色和色块中心（画在 120×96 里，色块比物件往左下错开一点）。
const tones: Record<SpotArtName, { tone: string, x: number, y: number }> = {
  scissors: { tone: 'pink', x: 56, y: 54 },
  chair: { tone: 'orange', x: 52, y: 52 },
  receipt: { tone: 'green', x: 50, y: 54 },
  chart: { tone: 'blue', x: 50, y: 56 },
  card: { tone: 'purple', x: 50, y: 54 },
  calendar: { tone: 'orange', x: 50, y: 54 },
  comb: { tone: 'yellow', x: 50, y: 56 },
  dryer: { tone: 'blue', x: 52, y: 54 },
  bowl: { tone: 'brown', x: 48, y: 60 },
  roller: { tone: 'purple', x: 50, y: 54 },
  mirror: { tone: 'green', x: 50, y: 50 },
  curl: { tone: 'pink', x: 50, y: 54 },
  register: { tone: 'green', x: 50, y: 58 },
  clock: { tone: 'yellow', x: 52, y: 54 },
  coins: { tone: 'yellow', x: 50, y: 58 },
  ticket: { tone: 'red', x: 52, y: 56 },
  phone: { tone: 'pink', x: 54, y: 54 },
  tag: { tone: 'purple', x: 50, y: 56 },
  key: { tone: 'blue', x: 50, y: 52 },
  sign: { tone: 'orange', x: 52, y: 58 }
}

// 不规则色块：绕中心一圈 9 个点，半径随种子起伏，再用二次曲线连圆。种子取名字，每次画出来一样。
function blob(cx: number, cy: number, r: number, seed: number) {
  let s = seed
  const rnd = () => (s = (s * 9301 + 49297) % 233280) / 233280
  const n = 9
  const pts: [number, number][] = []
  for (let i = 0; i < n; i++) {
    const a = i / n * Math.PI * 2
    const rr = r * (0.82 + rnd() * 0.3)
    pts.push([cx + Math.cos(a) * rr * 1.15, cy + Math.sin(a) * rr * 0.9])
  }
  const f = (v: number) => v.toFixed(1)
  let d = `M${f(pts[0]![0])} ${f(pts[0]![1])}`
  for (let i = 0; i < n; i++) {
    const p = pts[(i + 1) % n]!
    const q = pts[(i + 2) % n]!
    d += ` Q${f(p[0])} ${f(p[1])} ${f((p[0] + q[0]) / 2)} ${f((p[1] + q[1]) / 2)}`
  }
  return `${d}Z`
}

const back = computed(() => {
  const t = tones[props.name]
  return { fill: `var(--color-tag-${t.tone})`, d: blob(t.x, t.y, 26, props.name.length * 37 + props.name.charCodeAt(0)) }
})

// 一页可能有几张插画，滤镜和网点的 id 各自独立。
const id = useId()
</script>

<template>
  <svg
    viewBox="0 0 120 96"
    fill="none"
    stroke="var(--color-ink)"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
    data-testid="spot-art"
    :data-art="name"
  >
    <defs>
      <filter :id="`${id}-ink`" x="-10%" y="-10%" width="120%" height="120%">
        <feTurbulence type="fractalNoise" baseFrequency="0.035" numOctaves="2" seed="7" />
        <feDisplacementMap in="SourceGraphic" scale="2.4" />
      </filter>
      <pattern :id="`${id}-dots`" width="4" height="4" patternUnits="userSpaceOnUse">
        <circle cx="2" cy="2" r="0.9" fill="var(--color-ink)" opacity=".28" stroke="none" />
      </pattern>
    </defs>
    <path :d="back.d" :fill="back.fill" stroke="none" />
    <path :d="back.d" :fill="`url(#${id}-dots)`" opacity=".55" stroke="none" />

    <g :filter="`url(#${id}-ink)`">
      <template v-if="name === 'scissors'">
        <path d="M54.3 69.7 L58.2 50.9 L73 13 L65.8 53.1 L59.1 71.1 Z" fill="#fff" />
        <path d="M42.9 54.9 L60.9 48.2 L101 41 L63.1 55.8 L44.3 59.7 Z" fill="#fff" />
        <circle cx="34" cy="60" r="10" fill="#fff" />
        <circle cx="54" cy="80" r="10" fill="#fff" />
        <circle cx="62" cy="52" r="1.8" fill="var(--color-ink)" stroke="none" />
        <path d="M74 86 h34 M78 86 v-5 M83 86 v-5 M88 86 v-5 M93 86 v-5 M98 86 v-5 M103 86 v-5" />
      </template>

      <template v-else-if="name === 'chair'">
        <rect x="74" y="12" width="12" height="8" rx="3" fill="#fff" />
        <rect x="73" y="22" width="14" height="36" rx="4" fill="#fff" />
        <rect x="36" y="50" width="50" height="10" rx="4" fill="#fff" />
        <ellipse cx="62" cy="80" rx="18" ry="4" fill="#fff" />
        <path d="M44 50 v-8 h26 M40 60 L32 74 h14 M62 60 v16" />
      </template>

      <template v-else-if="name === 'receipt'">
        <path d="M38 12 H82 V78 l-5.5 5 -5.5 -5 -5.5 5 -5.5 -5 -5.5 5 -5.5 -5 -5.5 5 -5.5 -5 Z" fill="#fff" />
        <path d="M46 26 h28 M46 36 h18 M46 46 h22" />
        <path d="M46 60 h28" stroke-dasharray="2 3" />
        <path d="M62 68 h12" stroke-width="2.6" />
      </template>

      <template v-else-if="name === 'chart'">
        <rect x="32" y="58" width="10" height="20" rx="2" fill="#fff" />
        <rect x="48" y="44" width="10" height="34" rx="2" fill="#fff" />
        <rect x="64" y="52" width="10" height="26" rx="2" fill="#fff" />
        <rect x="80" y="34" width="10" height="44" rx="2" fill="#fff" />
        <path d="M24 78 h74 M90 14 v8 M86 18 h8" />
      </template>

      <template v-else-if="name === 'card'">
        <rect x="34" y="22" width="56" height="36" rx="6" fill="#fff" transform="rotate(-10 62 40)" />
        <rect x="28" y="36" width="56" height="36" rx="6" fill="#fff" />
        <path d="M28 46 h56" stroke-width="5" />
        <path d="M36 62 h18 M72 62 h4" />
      </template>

      <template v-else-if="name === 'calendar'">
        <rect x="32" y="20" width="56" height="58" rx="6" fill="#fff" />
        <path d="M32 34 h56 M46 14 v12 M74 14 v12" />
        <path d="M42 46 h.01 M53 46 h.01 M64 46 h.01 M75 46 h.01 M42 60 h.01 M53 60 h.01" stroke-width="3" />
        <rect x="64" y="56" width="10" height="9" rx="2" fill="var(--color-tag-orange)" />
      </template>

      <template v-else-if="name === 'comb'">
        <g transform="rotate(-14 60 50)">
          <path d="M30 34 H90 a4 4 0 0 1 4 4 V48 H26 V38 a4 4 0 0 1 4 -4 Z" fill="#fff" />
          <path d="M31 48 v18 M36 48 v18 M41 48 v18 M46 48 v18 M51 48 v18 M56 48 v18 M61 48 v18 M66 48 v14 M71 48 v14 M76 48 v14 M81 48 v14 M86 48 v14 M91 48 v14" />
        </g>
      </template>

      <template v-else-if="name === 'dryer'">
        <path d="M32 32 H70 A16 16 0 0 1 70 64 H32 Z" fill="#fff" />
        <path d="M56 64 L62 86 H72 L68 64 Z" fill="#fff" />
        <path d="M32 37 H22 V59 H32 Z" fill="#fff" />
        <circle cx="72" cy="48" r="8" fill="#fff" />
        <path d="M8 42 h8 M5 48 h11 M8 54 h8 M67 86 C67 93 82 93 90 88" />
      </template>

      <template v-else-if="name === 'bowl'">
        <path d="M28 50 H88 C88 68 76 80 58 80 C40 80 28 68 28 50 Z" fill="#fff" />
        <path d="M33 50 C42 43 74 43 83 50 Z" fill="var(--color-tag-brown)" />
        <path d="M26 50 H90" />
        <g transform="rotate(38 84 28)">
          <rect x="80" y="2" width="8" height="38" rx="3" fill="#fff" />
          <path d="M80.5 40 v9 M84 40 v10 M87.5 40 v9" />
        </g>
      </template>

      <template v-else-if="name === 'roller'">
        <path d="M42 22 H78 a4 4 0 0 1 4 4 V66 a4 4 0 0 1 -4 4 H42 a4 4 0 0 1 -4 -4 V26 a4 4 0 0 1 4 -4 Z" fill="#fff" />
        <path d="M49 22 v48 M56 22 v48 M64 22 v48 M71 22 v48" />
        <path d="M24 82 C34 66 46 90 58 76 C70 62 82 90 98 72" />
      </template>

      <template v-else-if="name === 'mirror'">
        <ellipse cx="60" cy="40" rx="22" ry="28" fill="#fff" />
        <path d="M48 28 l8 -7 M49 38 l15 -13 M60 68 V82 M46 84 H74" />
      </template>

      <template v-else-if="name === 'register'">
        <rect x="46" y="12" width="28" height="14" rx="3" fill="#fff" />
        <path d="M54 26 v8 M66 26 v8" />
        <path d="M34 34 H86 L92 64 H28 Z" fill="#fff" />
        <rect x="26" y="64" width="68" height="16" rx="3" fill="#fff" />
        <path d="M42 42 h.01 M52 42 h.01 M62 42 h.01 M72 42 h.01 M40 52 h.01 M50 52 h.01 M60 52 h.01 M70 52 h.01 M80 52 h.01" stroke-width="3.4" />
        <path d="M54 72 h12" />
        <path d="M51 19 h10" stroke-width="1.6" />
      </template>

      <template v-else-if="name === 'clock'">
        <path d="M34 26 A9 9 0 0 1 46 16 M86 26 A9 9 0 0 0 74 16" stroke-width="3" />
        <circle cx="60" cy="50" r="28" fill="#fff" />
        <path d="M60 50 V32 M60 50 L72 58" />
        <path d="M60 24 v3 M86 50 h-3 M60 76 v-3 M34 50 h3" />
        <path d="M42 76 L36 86 M78 76 L84 86" />
      </template>

      <template v-else-if="name === 'coins'">
        <path d="M26 72 v8 c0 5 30 5 30 0 v-8" fill="#fff" />
        <ellipse cx="41" cy="72" rx="15" ry="5" fill="#fff" />
        <path d="M26 62 v10 c0 5 30 5 30 0 v-10" fill="#fff" />
        <ellipse cx="41" cy="62" rx="15" ry="5" fill="#fff" />
        <path d="M28 52 v10 c0 5 30 5 30 0 v-10" fill="#fff" />
        <ellipse cx="43" cy="52" rx="15" ry="5" fill="#fff" />
        <circle cx="78" cy="56" r="20" fill="#fff" />
        <circle cx="78" cy="56" r="14" />
        <path d="M73 50 l5 6 l5 -6 M78 56 v10 M73 59 h10" stroke-width="1.8" />
      </template>

      <template v-else-if="name === 'ticket'">
        <g transform="rotate(-8 60 50)">
          <path d="M24 30 H96 V44 A6 6 0 0 0 96 56 V70 H24 V56 A6 6 0 0 0 24 44 Z" fill="#fff" />
          <path d="M44 32 V68" stroke-dasharray="3 4" />
          <path d="M56 50 l8 8 l16 -16" stroke-width="2.6" />
        </g>
      </template>

      <template v-else-if="name === 'phone'">
        <rect x="40" y="10" width="40" height="76" rx="7" fill="#fff" />
        <path d="M54 16 h12 M56 80 h8" />
        <path d="M46 34 H74 V42 A4 4 0 0 0 74 50 V58 H46 V50 A4 4 0 0 0 46 42 Z" fill="var(--color-tag-pink)" />
        <path d="M57 36 V56" stroke-dasharray="2 3" stroke-width="1.6" />
      </template>

      <template v-else-if="name === 'tag'">
        <path d="M58 18 H88 V48 L56 80 L26 50 Z" fill="#fff" />
        <circle cx="78" cy="28" r="4" />
        <path d="M78 28 C88 18 98 14 104 18" />
        <circle cx="50" cy="44" r="3" />
        <circle cx="64" cy="58" r="3" />
        <path d="M66 42 L48 60" />
      </template>

      <template v-else-if="name === 'key'">
        <circle cx="38" cy="46" r="16" fill="#fff" />
        <circle cx="34" cy="46" r="5" />
        <path d="M54 42 H94 V50 H54 Z" fill="#fff" />
        <path d="M82 50 v10 M90 50 v6 M74 50 v7" stroke-width="2.6" />
      </template>

      <template v-else-if="name === 'sign'">
        <circle cx="60" cy="14" r="2.4" fill="var(--color-ink)" stroke="none" />
        <path d="M60 14 L36 40 M60 14 L84 40" />
        <rect x="26" y="40" width="68" height="34" rx="4" fill="#fff" />
        <path d="M38 52 h28 M38 62 h18" />
        <path d="M76 56 l4 4 l8 -9" stroke-width="2.6" />
      </template>

      <template v-else>
        <path d="M28 76 C26 44 54 18 80 26 C98 32 96 58 78 58 C64 58 64 42 76 42 C82 42 84 48 80 52" stroke-width="3.2" />
        <path d="M36 80 C36 54 58 34 78 36" stroke-width="3.2" />
      </template>
    </g>
  </svg>
</template>
