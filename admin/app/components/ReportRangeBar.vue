<script setup lang="ts">
// 报表顶栏（DESIGN.md 报表）：按日 / 按月、起止、快捷范围在一行；范围超限或填错时在后面写一句为什么不查。
// dayOnly：接口只按营业日查的报表（员工业绩）不出按日 / 按月切换。
import type { Granularity, ReportRange } from '~/utils/report'

const props = defineProps<{ today: string, dayOnly?: boolean }>()
const range = defineModel<ReportRange>({ required: true })

const granularityItems: { label: string, key: Granularity }[] = [{ label: '按日', key: 'day' }, { label: '按月', key: 'month' }]
const granularity = computed({
  get: () => range.value.granularity,
  set: value => (range.value = switchGranularity(range.value, value, props.today))
})
const presets = computed(() => rangePresets(range.value.granularity, props.today))
// 快捷范围和按日 / 按月同一种视图页签；当前范围不是哪一个快捷范围时都不选中。
const presetItems = computed(() => presets.value.map(p => ({ key: p.label, label: p.label })))
const preset = computed({
  get: () => presets.value.find(p => same(p.range, range.value))?.label ?? '',
  set: (label: string) => {
    const p = presets.value.find(x => x.label === label)
    if (p) range.value = p.range
  }
})
const problem = computed(() => rangeProblem(range.value, props.dayOnly))
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
  <div class="space-y-2" data-testid="report-range">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
      <ViewTabs v-if="!dayOnly" v-model="granularity" :items="granularityItems" label="按日或按月" />
      <div class="flex items-center gap-2">
        <DateInput :key="`from-${range.granularity}`" :model-value="range.from" :month="month" :max="today" class="w-36" aria-label="开始" @update:model-value="setFrom" />
        <span class="text-meta text-muted">至</span>
        <DateInput :key="`to-${range.granularity}`" :model-value="range.to" :month="month" :max="today" class="w-36" aria-label="结束" @update:model-value="setTo" />
      </div>
      <ViewTabs v-model="preset" :items="presetItems" label="快捷范围" />
    </div>
    <p v-if="problem" class="text-meta text-ink-orange" data-testid="range-problem">{{ problem }}</p>
  </div>
</template>
