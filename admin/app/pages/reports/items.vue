<script setup lang="ts">
// 项目报表（DESIGN.md 报表页）：先一段话说结论，构成条看钱从哪来；一段营业日里每个服务、卖品一行，按实收从高到低；撤单在撤单那天冲回。
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

// 顶上一段话（DESIGN.md 报表页）：实收、原价和折扣，做了几次，业绩，实收最多的项目。
const brief = computed(() => {
  const t = totals.value
  if (!t) return []
  const { parts, say, bold } = briefWriter()
  const what = { all: '', service: '服务', goods: '卖品' }[kind.value]
  say(`这段时间${what}实收 `)
  bold(yuan(t.paid), undefined, 'paid')
  const discount = cents(t.line_price) - cents(t.paid)
  say(`（原价 ${yuan(t.line_price)}${discount ? `，折扣 ${yuan(discount / 100)}` : ''}），${{ all: '共', service: '做了', goods: '卖了' }[kind.value]} `)
  bold(String(t.times))
  say(`${t.designated ? ` 次，其中指定 ${t.designated} 次` : ' 次'}；业绩 `)
  bold(yuan(t.performance))
  say('。')
  const top = rows.value.find(r => Number(r.paid) > 0)
  if (top) say(`实收最多的是「${top.item_name}」。`)
  return parts
})

// 构成条（DESIGN.md 报表页）：实收前四的项目各一段，其余合成「其他」（接口已按实收从高到低排）。全部的数以下面的表为准。
const MIX_TOP = 4
const mix = computed(() => {
  const paid = rows.value.filter(r => Number(r.paid) > 0)
  const parts = paid.slice(0, MIX_TOP).map(r => ({ label: r.item_name, value: Number(r.paid) }))
  const rest = paid.slice(MIX_TOP).reduce((s, r) => s + Number(r.paid), 0)
  if (rest > 0) parts.push({ label: '其他', value: rest })
  return parts
})

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
      <ReportBrief :parts="brief" :class="loading && 'opacity-60'" />

      <section v-if="mix.length > 1">
        <div class="sec">钱从哪来<span class="font-normal text-dimmed">实收前 {{ MIX_TOP }} 的项目</span></div>
        <ReportMix :parts="mix" />
      </section>

      <section>
        <div class="sec">明细</div>
        <EmptyState v-if="!rows.length" class="box" art="chart" title="这段时间没有做过项目" hint="结账后这里会有数" />
        <div v-else class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="item-table">
            <thead class="border-b border-default text-sm text-muted">
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
