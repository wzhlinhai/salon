<script setup lang="ts">
// 欠款对账（DESIGN.md 报表页）：先一段话说还有多少没收回来，再是明细表。本店到现在为止全部欠款的合计，分单上的挂账和登记的历史欠款两行，各自欠了多少、还了多少、还剩多少。
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

// 顶上一段话（DESIGN.md 报表页）：还有几笔、多少没收回来，一共欠过多少、还了多少。
const brief = computed(() => {
  const s = summary.value
  if (!s) return []
  const { parts, say, bold } = briefWriter()
  if (!Number(s.amount)) {
    say('还没有过欠款。')
    return parts
  }
  if (s.outstanding_count) {
    say('还有 ')
    bold(String(s.outstanding_count))
    say(' 笔欠款没还清，共 ')
    bold(yuan(s.outstanding), 'down', 'outstanding')
    say('。')
  } else say('欠款都还清了。')
  say(`到现在一共欠过 ${yuan(s.amount)}，已还 ${yuan(s.repaid)}。`)
  return parts
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
      <ReportBrief :parts="brief" />

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
