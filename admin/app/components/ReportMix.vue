<script setup lang="ts">
// 构成条（DESIGN.md 简报）：一根分段横条代替饼图，下面图例写名称和占比。颜色按顺序取染发色卡色，最多五段。
const props = defineProps<{ parts: { label: string, value: number }[] }>()

const colors = ['var(--color-dye-1)', 'var(--color-dye-2)', 'var(--color-dye-3)', 'var(--color-dye-4)', 'var(--color-dye-5)']
const total = computed(() => props.parts.reduce((s, p) => s + Math.max(p.value, 0), 0))
// 占比用最大余数法取整，几段加起来正好 100%。
const shown = computed(() => {
  const parts = props.parts.filter(p => p.value > 0).slice(0, colors.length)
  const raw = parts.map(p => p.value / total.value * 100)
  const pct = raw.map(Math.floor)
  const order = raw.map((r, i) => i).sort((a, b) => (raw[b]! - pct[b]!) - (raw[a]! - pct[a]!))
  const missing = 100 - pct.reduce((s, n) => s + n, 0)
  for (let k = 0; k < Math.min(missing, order.length); k++) pct[order[k]!]!++
  return parts.map((p, i) => ({ ...p, color: colors[i]!, pct: pct[i]! }))
})
</script>

<template>
  <div v-if="total > 0" data-testid="report-mix">
    <div class="mb-2 flex h-2.5 gap-0.5 overflow-hidden rounded-md" aria-hidden="true">
      <i v-for="(p, i) in shown" :key="i" :style="{ flexGrow: p.value, background: p.color }" />
    </div>
    <ul class="flex flex-wrap gap-x-4 gap-y-1 text-meta text-muted">
      <li v-for="(p, i) in shown" :key="i" class="flex items-center gap-2">
        <i class="size-2 rounded-full" :style="{ background: p.color }" aria-hidden="true" />{{ p.label }}<span class="text-default tabular-nums">{{ p.pct }}%</span>
      </li>
    </ul>
  </div>
</template>
