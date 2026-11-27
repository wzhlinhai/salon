<script setup lang="ts">
// 报表顶栏（DESIGN.md 报表）：左边按日 / 按月，右边一个范围按钮写着现在查的是哪段，点开弹层里挑快捷范围或自己填起止；
// 范围超限或填错时在下面写一句为什么不查。dayOnly：接口只按营业日查的报表（员工业绩）不出按日 / 按月切换。
import type { Granularity, ReportRange } from '~/utils/report'

const props = defineProps<{ today: string, dayOnly?: boolean }>()
const range = defineModel<ReportRange>({ required: true })

const open = ref(false)
const granularityItems: { label: string, key: Granularity }[] = [{ label: '按日', key: 'day' }, { label: '按月', key: 'month' }]
const granularity = computed({
  get: () => range.value.granularity,
  set: value => (range.value = switchGranularity(range.value, value, props.today))
})
const presets = computed(() => rangePresets(range.value.granularity, props.today))
// 当前范围是哪一个快捷范围；自己填的起止不对应任何一个时为空。
const preset = computed(() => presets.value.find(p => same(p.range, range.value))?.label ?? '')
const problem = computed(() => rangeProblem(range.value, props.dayOnly))
const month = computed(() => range.value.granularity === 'month')

// 按钮上的起止：今年的日子省掉年份，月份照写。
const span = computed(() => {
  const { from, to } = range.value
  const short = (v: string) => (!month.value && v.slice(0, 4) === props.today.slice(0, 4) ? v.slice(5) : v)
  if (!from || !to) return '自己填起止'
  return from === to ? short(from) : `${short(from)} 至 ${short(to)}`
})

function same(a: ReportRange, b: ReportRange) {
  return a.granularity === b.granularity && a.from === b.from && a.to === b.to
}

const earlier = computed(() => stepRange(range.value, -1, props.today))
const later = computed(() => stepRange(range.value, 1, props.today))

function choose(value: ReportRange) {
  range.value = value
  open.value = false
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
      <!-- 范围按钮两边「‹ ›」：本月、上月这类整月一下翻一个月，不用点开弹层。 -->
      <div class="flex min-w-0 items-center gap-1">
        <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-chevron-left" class="size-8 justify-center" aria-label="往前一段" data-testid="range-earlier" :disabled="!earlier" @click="earlier && choose(earlier)" />
        <UPopover v-model:open="open" :content="{ align: 'start', sideOffset: 4 }">
          <button
            type="button"
            class="inline-flex h-8 max-w-full items-center gap-2 rounded-full border border-default px-3 text-sm transition-colors hover:bg-hover focus-visible:outline-2 focus-visible:outline-primary"
            aria-label="查哪段时间"
            data-testid="range-trigger"
          >
            <UIcon name="i-lucide-calendar" class="size-4 shrink-0 text-muted" aria-hidden="true" />
            <span v-if="preset" class="font-medium">{{ preset }}</span>
            <span class="truncate tabular-nums" :class="preset ? 'text-muted' : 'font-medium'">{{ span }}</span>
            <UIcon name="i-lucide-chevron-down" class="size-4 shrink-0 text-muted" aria-hidden="true" />
          </button>
          <template #content>
            <div class="w-72 p-2 text-sm" data-testid="range-popover">
              <ul role="group" aria-label="快捷范围">
                <li v-for="p in presets" :key="p.label">
                  <button
                    type="button"
                    class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-start transition-colors focus-visible:outline-2 focus-visible:outline-primary"
                    :class="preset === p.label ? 'bg-selected' : 'hover:bg-hover'"
                    :aria-pressed="preset === p.label"
                    @click="choose(p.range)"
                  >
                    <span class="flex-1">{{ p.label }}</span>
                    <UIcon v-if="preset === p.label" name="i-lucide-check" class="size-4 shrink-0" aria-hidden="true" />
                  </button>
                </li>
              </ul>
              <div class="mt-2 border-t border-default px-2 pt-3 pb-1">
                <p class="mb-2 text-meta text-muted">自己填</p>
                <div class="flex items-center gap-2">
                  <DateInput :key="`from-${range.granularity}`" :model-value="range.from" :month="month" :max="today" size="sm" class="min-w-0 flex-1" aria-label="开始" @update:model-value="setFrom" />
                  <span class="text-meta text-muted">至</span>
                  <DateInput :key="`to-${range.granularity}`" :model-value="range.to" :month="month" :max="today" size="sm" class="min-w-0 flex-1" aria-label="结束" @update:model-value="setTo" />
                </div>
              </div>
            </div>
          </template>
        </UPopover>
        <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-chevron-right" class="size-8 justify-center" aria-label="往后一段" data-testid="range-later" :disabled="!later" @click="later && choose(later)" />
      </div>
    </div>
    <p v-if="problem" class="text-meta text-ink-orange" data-testid="range-problem">{{ problem }}</p>
  </div>
</template>
