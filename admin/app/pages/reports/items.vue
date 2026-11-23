<script setup lang="ts">
// 项目报表（DESIGN.md 报表）：一段营业日里每个服务、卖品一行，按实收从高到低；撤单在撤单那天冲回。
// 可以只看服务或只看卖品。看全部时合计行用接口的 totals；只看一类时合计是这一类各行相加（按分算）。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '项目报表', description: '每个服务、卖品做了几次、收了多少；实收是折后价，撤单在撤单那天冲回。' })

type ItemReport = components['schemas']['ItemReport']
type Row = components['schemas']['ItemReportRow']
type Kind = 'all' | Row['item_kind']

const api = useApi()
const { today, range, report, loading, failed, retry } = useDayReport<ItemReport>((from, to) => api.POST('/api/ops/get_item_report', { body: { from, to } }))

const kind = ref<Kind>('all')
const kinds: { label: string, value: Kind }[] = [{ label: '全部', value: 'all' }, { label: '服务', value: 'service' }, { label: '卖品', value: 'goods' }]
const rows = computed(() => (report.value?.rows ?? []).filter(r => kind.value === 'all' || r.item_kind === kind.value))

type MoneyKey = 'line_price' | 'paid' | 'card_paid' | 'count_paid' | 'groupbuy_paid' | 'performance'
const moneyColumns: { key: MoneyKey, label: string }[] = [
  { key: 'line_price', label: '原价' },
  { key: 'paid', label: '实收' },
  { key: 'card_paid', label: '其中卡付' },
  { key: 'groupbuy_paid', label: '其中团购' },
  { key: 'count_paid', label: '其中扣次' },
  { key: 'performance', label: '业绩' }
]

function cents(value: string) {
  return Math.round(Number(value) * 100)
}

// 合计：全部时用接口的；只看一类时把这一类的行按分加起来。
const totals = computed(() => {
  const t = report.value?.totals
  if (!t) return null
  if (kind.value === 'all') return t
  const sum = (key: MoneyKey) => (rows.value.reduce((s, r) => s + cents(r[key]), 0) / 100).toFixed(2)
  return {
    ...t,
    times: rows.value.reduce((s, r) => s + r.times, 0),
    designated: rows.value.reduce((s, r) => s + r.designated, 0),
    line_price: sum('line_price'), paid: sum('paid'), card_paid: sum('card_paid'),
    count_paid: sum('count_paid'), groupbuy_paid: sum('groupbuy_paid'), performance: sum('performance')
  }
})

const metrics = computed(() => {
  const t = totals.value
  if (!t) return []
  const discount = cents(t.line_price) - cents(t.paid)
  return [
    { label: '实收', value: yuan(t.paid), note: `原价 ${yuan(t.line_price)}` },
    { label: '折扣', value: yuan(discount / 100), note: '原价减实收' },
    { label: '次数', value: String(t.times), note: `指定 ${t.designated} 次` },
    { label: '业绩', value: yuan(t.performance), note: '项目业绩' }
  ]
})

// 柱状图只比实收前 10 名（接口已按实收从高到低排），全部的数以下面的表为准。
const CHART_TOP = 10
const ranked = computed(() => rows.value.filter(r => Number(r.paid) > 0).slice(0, CHART_TOP))

function cell(value: string | number) {
  return Number(value) === 0 ? 'text-dimmed' : ''
}
</script>

<template>
  <div class="space-y-4">
    <div v-if="today" class="flex flex-wrap items-start justify-between gap-2">
      <ReportRangeBar v-model="range" :today="today" day-only />
      <MethodPicker v-model="kind" :items="kinds" label="项目类型" />
    </div>

    <EmptyState v-if="failed && !report" class="box" icon="i-lucide-circle-alert" title="报表没有读出来" hint="检查网络后再试一次">
      <UButton color="neutral" variant="outline" @click="retry">重试</UButton>
    </EmptyState>

    <template v-if="report && totals">
      <div data-testid="report-metrics" class="stats grid grid-cols-2 gap-3 tabular-nums md:grid-cols-4" :class="loading && 'opacity-60'">
        <div v-for="(m, i) in metrics" :key="m.label" :data-metric="m.label" :class="i ? 'md:border-l md:border-default md:pl-4' : ''">
          <p class="text-xs text-dimmed">{{ m.label }}</p>
          <p data-testid="metric-value" class="text-2xl leading-[1.3] font-semibold" :class="zeroText(m.value) && 'text-dimmed'">{{ m.value }}</p>
          <p class="text-xs text-muted">{{ m.note }}</p>
        </div>
      </div>

      <section v-if="ranked.length > 1">
        <div class="sec">实收前 {{ CHART_TOP }} 名</div>
        <div class="box">
          <ReportChart
            type="bar"
            horizontal
            :label="`${range.from} 至 ${range.to} 实收最高的项目，数字见下方明细表`"
            :categories="ranked.map(r => r.item_name)"
            :series="[{ name: '实收', values: ranked.map(r => Number(r.paid)) }]"
          />
        </div>
      </section>

      <section>
        <div class="sec">明细</div>
        <EmptyState v-if="!rows.length" class="box" title="这段时间没有做过项目" hint="结账后这里会有数" />
        <div v-else class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="item-table">
            <thead class="border-b border-default text-xs text-dimmed">
              <tr>
                <th scope="col" class="sticky left-0 bg-page px-3 py-1.5 text-left font-normal">项目</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">次数</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">指定</th>
                <th v-for="c in moneyColumns" :key="c.key" scope="col" class="px-3 text-right font-normal whitespace-nowrap">{{ c.label }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.item_id" class="border-t border-default" :data-item="row.item_name">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap">
                  {{ row.item_name }}<span v-if="row.item_kind === 'goods'" class="tag ms-1.5 bg-tag-gray">卖品</span>
                </th>
                <td class="px-3 text-right" :class="cell(row.times)" data-col="times">{{ row.times }}</td>
                <td class="px-3 text-right" :class="cell(row.designated)" data-col="designated">{{ row.designated }}</td>
                <td v-for="c in moneyColumns" :key="c.key" class="px-3 text-right whitespace-nowrap" :class="[cell(row[c.key]), c.key === 'paid' && 'font-medium']" :data-col="c.key">{{ yuanFixed(row[c.key]) }}</td>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="item-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <td class="px-3 text-right" data-col="times">{{ totals.times }}</td>
                <td class="px-3 text-right" data-col="designated">{{ totals.designated }}</td>
                <td v-for="c in moneyColumns" :key="c.key" class="px-3 text-right whitespace-nowrap" :data-col="c.key">{{ yuanFixed(totals[c.key]) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
