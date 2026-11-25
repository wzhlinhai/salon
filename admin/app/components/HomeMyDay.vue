<script setup lang="ts">
import type { components } from '~/utils/api'

// 个人首页的"我今天"：本人今天的项目业绩、售卡业绩、提成和单数，业绩和上周同日比。
// 点提成从右侧滑出今天每一笔提成怎么算出来的。
type PerformanceList = components['schemas']['PerformanceList']
type CommissionList = components['schemas']['CommissionList']

const props = defineProps<{ date: string, staffId: string }>()

const api = useApi()
const today = ref<PerformanceList | null>(null)
const lastWeek = ref<PerformanceList | null>(null)
const commissions = ref<CommissionList | null>(null)
const showCommissions = ref(false)
const guard = useRequestGuard()

async function load() {
  if (!props.date) return
  const current = guard.next()
  const previous = addDays(props.date, -7)
  const [now, before, earned] = await Promise.all([
    api.POST('/api/ops/list_performance', { body: { date_from: props.date, date_to: props.date, staff_id: props.staffId, limit: 200 } }),
    api.POST('/api/ops/list_performance', { body: { date_from: previous, date_to: previous, staff_id: props.staffId, limit: 1 } }),
    api.POST('/api/ops/list_commissions', { body: { date_from: props.date, date_to: props.date, staff_id: props.staffId, limit: 200 } })
  ])
  if (!current()) return
  today.value = now.data ?? null
  lastWeek.value = before.data ?? null
  commissions.value = earned.data ?? null
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
const tiered = computed(() => commissions.value?.entries.some(c => c.explanation.tier) ?? false)
const orders = computed(() => {
  const ids = new Set<string>()
  for (const entry of today.value?.entries ?? []) {
    if (entry.kind === 'item' && !entry.reversal && entry.order_id) ids.add(entry.order_id)
  }
  return ids.size
})
</script>

<template>
  <HomeWidget v-if="today" title="我今天" data-testid="home-my-day" class="@container">
    <div class="grid grid-cols-2 gap-x-4 gap-y-3 tabular-nums @2xl:grid-cols-4">
      <div>
        <p class="text-xs text-dimmed">业绩</p>
        <p class="figure" :class="zeroText(yuan(itemAmount)) && 'text-dimmed'">{{ yuan(itemAmount) }}</p>
        <p class="text-xs text-muted">
          <template v-if="change === null">我做的项目</template>
          <template v-else><span :class="change >= 0 ? 'text-ink-green' : 'text-ink-red'">{{ change >= 0 ? '↑' : '↓' }} {{ Math.abs(change) }}%</span> 较上周同日</template>
        </p>
      </div>
      <button
        v-if="commissions"
        type="button"
        data-testid="home-my-commission"
        class="-m-2 rounded-md p-2 text-start hover:bg-side"
        @click="showCommissions = true"
      >
        <p class="flex items-center gap-1 text-xs text-dimmed">提成<UIcon name="i-lucide-chevron-right" class="size-3.5" aria-hidden="true" /></p>
        <p class="figure" :class="zeroText(yuan(commissions.total)) && 'text-dimmed'">{{ yuan(commissions.total) }}</p>
        <p class="text-xs text-muted">{{ tiered ? '含阶梯提成，月底才定' : '点开看每笔怎么算' }}</p>
      </button>
      <div>
        <p class="text-xs text-dimmed">售卡</p>
        <p class="figure" :class="zeroText(yuan(total(today, 'sale'))) && 'text-dimmed'">{{ yuan(total(today, 'sale')) }}</p>
        <p class="text-xs text-muted">办卡、充值和升级</p>
      </div>
      <div>
        <p class="text-xs text-dimmed">单数</p>
        <p class="figure" :class="zeroText(orders) && 'text-dimmed'">{{ orders }}</p>
        <p class="text-xs text-muted">有我参与的单</p>
      </div>
    </div>

    <USlideover v-model:open="showCommissions" :title="`我今天的提成 ${commissions ? yuan(commissions.total) : ''}`" description="每笔按做单那天生效的提成方案算；阶梯提成随当月累计变化，月底锁定后才固定。">
      <template #body>
        <EmptyState v-if="!commissions?.entries.length" title="今天还没有提成" hint="结账后，你参与的项目和售卡会算出提成" />
        <ul v-else class="divide-y divide-default text-sm" data-testid="home-commission-list">
          <li v-for="c in commissions.entries" :key="c.id" class="py-2">
            <p class="flex items-center gap-2">
              <span class="font-medium">{{ commissionSourceNames[c.source] }}</span>
              <span v-if="c.explanation.origin.item" class="truncate text-muted">{{ c.explanation.origin.item.name }}</span>
              <span v-if="c.reversal" class="tag bg-tag-gray">冲回</span>
              <span class="ms-auto tabular-nums" :class="c.reversal && 'text-ink-red'">{{ yuanFixed(c.amount) }}</span>
            </p>
            <p class="text-meta text-muted">{{ commissionExplanation(c) }}</p>
          </li>
        </ul>
      </template>
    </USlideover>
  </HomeWidget>
</template>
