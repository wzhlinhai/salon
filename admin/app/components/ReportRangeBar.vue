<script setup lang="ts">
// 报表顶栏（DESIGN.md 报表）：按日 / 按月、起止、快捷范围在一行；范围超限或填错时在后面写一句为什么不查。
import type { Granularity, ReportRange } from '~/utils/report'

const props = defineProps<{ today: string }>()
const range = defineModel<ReportRange>({ required: true })

const granularityItems: { label: string, value: Granularity }[] = [{ label: '按日', value: 'day' }, { label: '按月', value: 'month' }]
const granularity = computed({
  get: () => range.value.granularity,
  set: value => (range.value = switchGranularity(range.value, value ?? 'day', props.today))
})
const presets = computed(() => rangePresets(range.value.granularity, props.today))
const problem = computed(() => rangeProblem(range.value))
const month = computed(() => range.value.granularity === 'month')

function same(a: ReportRange, b: ReportRange) {
  return a.granularity === b.granularity && a.from === b.from && a.to === b.to
}

function setFrom(value: string) {
  range.value = { ...range.value, from: value }
}

function setTo(value: string) {
  range.value = { ...range.value, to: value }
}
</script>

<template>
  <div class="space-y-1.5" data-testid="report-range">
    <div class="flex flex-wrap items-center gap-2">
      <MethodPicker v-model="granularity" :items="granularityItems" label="按日或按月" />
      <div class="flex items-center gap-1.5">
        <DateInput :key="`from-${range.granularity}`" :model-value="range.from" :month="month" :max="today" size="sm" class="w-36" aria-label="开始" @update:model-value="setFrom" />
        <span class="text-meta text-muted">至</span>
        <DateInput :key="`to-${range.granularity}`" :model-value="range.to" :month="month" :max="today" size="sm" class="w-36" aria-label="结束" @update:model-value="setTo" />
      </div>
      <div class="flex flex-wrap items-center gap-1" role="group" aria-label="快捷范围">
        <UButton
          v-for="p in presets"
          :key="p.label"
          size="xs"
          :color="same(p.range, range) ? 'primary' : 'neutral'"
          :variant="same(p.range, range) ? 'soft' : 'ghost'"
          :aria-pressed="same(p.range, range)"
          @click="range = p.range"
        >
          {{ p.label }}
        </UButton>
      </div>
    </div>
    <p v-if="problem" class="text-meta text-ink-orange" data-testid="range-problem">{{ problem }}</p>
  </div>
</template>
