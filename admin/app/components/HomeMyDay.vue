<script setup lang="ts">
import type { components } from '~/utils/api'

// 个人首页的"我今天"（照原型 K）：本人今天的项目业绩、售卡业绩和单数，业绩和上周同日比。
type PerformanceList = components['schemas']['PerformanceList']

const props = defineProps<{ date: string, staffId: string }>()

const api = useApi()
const today = ref<PerformanceList | null>(null)
const lastWeek = ref<PerformanceList | null>(null)
const guard = useRequestGuard()

function addDays(value: string, delta: number) {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, (day ?? 1) + delta)).toISOString().slice(0, 10)
}

async function load() {
  if (!props.date) return
  const current = guard.next()
  const previous = addDays(props.date, -7)
  const [now, before] = await Promise.all([
    api.POST('/api/ops/list_performance', { body: { date_from: props.date, date_to: props.date, staff_id: props.staffId, limit: 200 } }),
    api.POST('/api/ops/list_performance', { body: { date_from: previous, date_to: previous, staff_id: props.staffId, limit: 1 } })
  ])
  if (!current()) return
  today.value = now.data ?? null
  lastWeek.value = before.data ?? null
}

watch(() => [props.date, props.staffId], load, { immediate: true })

function total(list: PerformanceList | null, kind: string) {
  return Number(list?.totals.find(row => row.kind === kind)?.amount ?? 0)
}

const itemAmount = computed(() => total(today.value, 'item'))
const change = computed(() => {
  const before = total(lastWeek.value, 'item')
  if (!before) return null
  return Math.round(((itemAmount.value - before) / before) * 100)
})
const orders = computed(() => {
  const ids = new Set<string>()
  for (const entry of today.value?.entries ?? []) {
    if (entry.kind === 'item' && !entry.reversal && entry.order_id) ids.add(entry.order_id)
  }
  return ids.size
})
</script>

<template>
  <div v-if="today" data-testid="home-my-day">
    <div class="sec"><UIcon name="i-lucide-wallet" class="size-4 shrink-0" aria-hidden="true" /> 我今天<span class="ms-auto font-normal text-dimmed">本人数据</span></div>
    <div class="box grid grid-cols-3 tabular-nums">
      <div>
        <p class="text-xs text-dimmed">业绩</p>
        <p class="text-2xl leading-[1.3] font-semibold">{{ yuan(itemAmount) }}</p>
        <p class="text-xs text-muted">
          <template v-if="change === null">我做的项目</template>
          <template v-else><span :class="change >= 0 ? 'text-ink-green' : 'text-ink-red'">{{ change >= 0 ? '↑' : '↓' }} {{ Math.abs(change) }}%</span> 较上周同日</template>
        </p>
      </div>
      <div class="border-l border-default pl-4">
        <p class="text-xs text-dimmed">售卡</p>
        <p class="text-2xl leading-[1.3] font-semibold">{{ yuan(total(today, 'sale')) }}</p>
        <p class="text-xs text-muted">办卡、充值和升级</p>
      </div>
      <div class="border-l border-default pl-4">
        <p class="text-xs text-dimmed">单数</p>
        <p class="text-2xl leading-[1.3] font-semibold">{{ orders }}</p>
        <p class="text-xs text-muted">有我参与的单</p>
      </div>
    </div>
  </div>
</template>
