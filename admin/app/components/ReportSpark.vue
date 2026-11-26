<script setup lang="ts">
// 走势细线（DESIGN.md 简报）：不画坐标轴和网格，只标最高的那一天，下面一行写起止和说明。
// 数据点少于两个时不画；负数（退款多于收入的那天）按 0 画。颜色取染发色卡第一色。
const props = defineProps<{ values: number[], start: string, end: string, caption: string, peakLabel?: (index: number) => string }>()

const W = 720
const H = 64
const path = computed(() => {
  const v = props.values
  if (v.length < 2) return ''
  const max = Math.max(...v, 1)
  return v.map((n, i) => `${i ? 'L' : 'M'}${(i / (v.length - 1) * W).toFixed(1)} ${(H - 4 - Math.max(n, 0) / max * (H - 12)).toFixed(1)}`).join(' ')
})
const peak = computed(() => {
  const v = props.values
  if (v.length < 2 || Math.max(...v) <= 0) return null
  const max = Math.max(...v)
  const i = v.indexOf(max)
  // 圆点用 HTML 叠在线上：SVG 横向拉伸（preserveAspectRatio none），在里面画圆会变成椭圆。
  return { i, left: `${i / (v.length - 1) * 100}%`, top: `${(H - 4 - max / Math.max(max, 1) * (H - 12)) / H * 100}%` }
})
</script>

<template>
  <figure v-if="path" class="m-0" data-testid="report-spark">
    <div class="relative">
      <svg :viewBox="`0 0 ${W} ${H}`" class="block h-16 w-full overflow-visible" preserveAspectRatio="none" aria-hidden="true">
        <path :d="path" fill="none" stroke="var(--color-dye-1)" stroke-width="1.6" vector-effect="non-scaling-stroke" stroke-linejoin="round" />
      </svg>
      <!-- 最高那天：一个色卡色的点，外面一圈白边压在线上。 -->
      <span v-if="peak" data-testid="spark-peak" class="absolute size-2 -translate-1/2 rounded-full bg-dye-1 ring-2 ring-white" :style="{ left: peak.left, top: peak.top }" aria-hidden="true" />
    </div>
    <figcaption class="mt-1 flex justify-between text-xs text-muted tabular-nums">
      <span>{{ start }}</span>
      <span>{{ caption }}<template v-if="peak && peakLabel"> · 最高 {{ peakLabel(peak.i) }}</template></span>
      <span>{{ end }}</span>
    </figcaption>
  </figure>
</template>
