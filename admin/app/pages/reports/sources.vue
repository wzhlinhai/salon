<script setup lang="ts">
// 新客来源（DESIGN.md 报表页）：先一段话说结论，排名条看新客从哪来；一段营业日里第一次来结账的新客按来源分行，几个后来又来了（回头客），这段时间一共花了多少。
// 只有人数和金额，不列顾客。未识别顾客认不出是不是新客，单独写一句有几单。合计行用接口的 totals。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '新客来源', description: '新客是第一次在本店结账的顾客；导入的老顾客不算。' })

type SourceReport = components['schemas']['CustomerSourceReport']
type Row = components['schemas']['CustomerSourceReportRow']

const api = useApi()
const { today, range, report, loading, failed, retry } = useDayReport<SourceReport>((from, to) => api.POST('/api/ops/get_customer_source_report', { body: { from, to } }))

const rows = computed(() => report.value?.rows ?? [])
const nameOf = (row: Row) => row.source_name ?? '未填来源'

function percent(part: number, whole: number) {
  return whole ? `${Math.round((part / whole) * 100)}%` : '—'
}

const ranked = computed(() => [...rows.value].filter(r => r.new_customers > 0).sort((a, b) => b.new_customers - a.new_customers))

// 顶上一段话（DESIGN.md 报表页）：几位新客、最多从哪来、几个回头、花了多少。
const brief = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  const { parts, say, bold } = briefWriter()
  say('这段时间来了 ')
  bold(String(t.new_customers), undefined, 'new_customers')
  say(' 位新客')
  // 人数最多的来源没填时不说「最多从哪来」，免得把第二名说成第一。
  const top = ranked.value[0]?.source_name
  if (top) say(`，最多从「${top}」来`)
  if (!t.new_customers) {
    say('。')
    return parts
  }
  if (t.returning) {
    say('；其中 ')
    bold(String(t.returning))
    say(` 位后来又来了（回头率 ${percent(t.returning, t.new_customers)}）`)
  } else say('，还没有人回头')
  say('。新客一共花了 ')
  bold(yuan(t.revenue))
  say(`，人均 ${yuan(Number(t.revenue) / t.new_customers)}。`)
  return parts
})

function cell(value: string | number) {
  return Number(value) === 0 ? 'text-dimmed' : ''
}
</script>

<template>
  <div class="space-y-4">
    <ReportRangeBar v-if="today" v-model="range" :today="today" day-only />

    <EmptyState v-if="failed && !report" class="box" icon="i-lucide-circle-alert" title="报表没有读出来" hint="检查网络后再试一次">
      <UButton color="neutral" variant="outline" @click="retry">重试</UButton>
    </EmptyState>

    <template v-if="report">
      <ReportBrief :parts="brief" :class="loading && 'opacity-60'" />
      <p v-if="report.unidentified_orders" class="text-meta text-muted" data-testid="unidentified-orders">
        另有 {{ report.unidentified_orders }} 单是未识别顾客（档案里没有手机号），认不出是不是新客，不在上面。
      </p>

      <section v-if="ranked.length > 1">
        <div class="sec">新客从哪来</div>
        <ReportRank :avatar="false" :rows="ranked.map(r => ({ key: r.source_id ?? '', name: nameOf(r), value: r.new_customers, display: `${r.new_customers} 人` }))" />
      </section>

      <section>
        <div class="sec">明细</div>
        <EmptyState v-if="!rows.length" art="chart" title="这段时间没有新客" hint="新顾客第一次结账后这里会有数" />
        <div v-else class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="source-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th scope="col" class="sticky left-0 bg-page px-3 py-2 text-left font-normal">来源</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">新客</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">占比</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">回头客</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">回头率</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">新客消费</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.source_id ?? ''" class="border-t border-default" :data-source="nameOf(row)">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap" :class="!row.source_name && 'text-muted'">{{ nameOf(row) }}</th>
                <td class="px-3 text-right" :class="cell(row.new_customers)" data-col="new_customers">{{ row.new_customers }}</td>
                <td class="px-3 text-right text-muted">{{ percent(row.new_customers, report.totals.new_customers) }}</td>
                <td class="px-3 text-right" :class="cell(row.returning)" data-col="returning">{{ row.returning }}</td>
                <td class="px-3 text-right text-muted">{{ percent(row.returning, row.new_customers) }}</td>
                <td class="px-3 text-right whitespace-nowrap" :class="cell(row.revenue)" data-col="revenue">{{ yuanFixed(row.revenue) }}</td>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="source-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <td class="px-3 text-right" data-col="new_customers">{{ report.totals.new_customers }}</td>
                <td class="px-3 text-right text-muted">{{ report.totals.new_customers ? '100%' : '—' }}</td>
                <td class="px-3 text-right" data-col="returning">{{ report.totals.returning }}</td>
                <td class="px-3 text-right text-muted">{{ percent(report.totals.returning, report.totals.new_customers) }}</td>
                <td class="px-3 text-right whitespace-nowrap" data-col="revenue">{{ yuanFixed(report.totals.revenue) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
