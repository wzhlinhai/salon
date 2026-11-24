<script setup lang="ts">
// 欠款对账（DESIGN.md 报表）：本店到现在为止全部欠款的合计，分单上的挂账和登记的历史欠款两行，各自欠了多少、还了多少、还剩多少。
// 不分时间段：欠款是一直挂着直到还清的。撤单作废的挂账不计。合计行用接口的数。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '欠款对账', description: '到现在为止全部欠款；撤单作废的挂账不计。每位顾客欠多少在顾客详情里看。' })

type Summary = components['schemas']['Summary']

const api = useApi()
const toast = useToast()
const summary = ref<Summary | null>(null)
const failed = ref(false)
const guard = useRequestGuard()

async function load() {
  const current = guard.next()
  failed.value = false
  const { data, error } = await api.POST('/api/ops/debt_summary', { body: {} })
  if (!current()) return
  if (!data) {
    failed.value = true
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  summary.value = data
}

onMounted(load)

const metrics = computed(() => {
  const s = summary.value
  if (!s) return []
  return [
    { label: '未还', value: yuan(s.outstanding), note: `${s.outstanding_count} 笔还没还清` },
    { label: '已还', value: yuan(s.repaid), note: '挂账和历史欠款的还款' },
    { label: '欠款合计', value: yuan(s.amount), note: '单上挂账 + 历史欠款' }
  ]
})

const rows = computed(() => summary.value
  ? [
      { key: 'order', label: '单上挂账', totals: summary.value.order },
      { key: 'historical', label: '历史欠款', totals: summary.value.historical }
    ]
  : [])

function cell(value: string) {
  return Number(value) === 0 ? 'text-dimmed' : ''
}
</script>

<template>
  <div class="space-y-4">
    <EmptyState v-if="failed && !summary" class="box" icon="i-lucide-circle-alert" title="欠款没有读出来" hint="检查网络后再试一次">
      <UButton color="neutral" variant="outline" @click="load">重试</UButton>
    </EmptyState>

    <template v-if="summary">
      <div data-testid="report-metrics" class="stats grid grid-cols-2 gap-3 tabular-nums md:grid-cols-3">
        <div v-for="(m, i) in metrics" :key="m.label" :data-metric="m.label" :class="i ? 'md:border-l md:border-default md:pl-4' : ''">
          <p class="text-xs text-dimmed">{{ m.label }}</p>
          <p data-testid="metric-value" class="text-2xl leading-[1.3] font-semibold" :class="zeroText(m.value) && 'text-dimmed'">{{ m.value }}</p>
          <p class="text-xs text-muted">{{ m.note }}</p>
        </div>
      </div>

      <section>
        <div class="sec">明细</div>
        <div class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="debt-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th scope="col" class="px-3 py-1.5 text-left font-normal">来源</th>
                <th scope="col" class="px-3 text-right font-normal">欠款</th>
                <th scope="col" class="px-3 text-right font-normal">已还</th>
                <th scope="col" class="px-3 text-right font-normal">未还</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.key" class="border-t border-default" :data-kind="row.key">
                <th scope="row" class="px-3 py-2 text-left font-normal">{{ row.label }}</th>
                <td class="px-3 text-right" :class="cell(row.totals.amount)" data-col="amount">{{ yuanFixed(row.totals.amount) }}</td>
                <td class="px-3 text-right" :class="cell(row.totals.repaid)" data-col="repaid">{{ yuanFixed(row.totals.repaid) }}</td>
                <td class="px-3 text-right font-medium" :class="cell(row.totals.outstanding)" data-col="outstanding">{{ yuanFixed(row.totals.outstanding) }}</td>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="debt-totals">
                <th scope="row" class="px-3 py-2 text-left">合计</th>
                <td class="px-3 text-right" data-col="amount">{{ yuanFixed(summary.amount) }}</td>
                <td class="px-3 text-right" data-col="repaid">{{ yuanFixed(summary.repaid) }}</td>
                <td class="px-3 text-right" data-col="outstanding">{{ yuanFixed(summary.outstanding) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
