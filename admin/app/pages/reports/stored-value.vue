<script setup lang="ts">
// 储值余额日报（DESIGN.md 报表页）：一段营业日里卡内余额每天怎么变的。先一段话说期初到期末，走势图看余额，下面每天一行。
// 口径取 get_stored_value_report：本店所有卡的钱（本金、赠送金分开），期末 = 期初 + 充入 + 升级 − 耗卡 − 退卡 + 调整；
// 撤单、售后退回卡里的钱算负的耗卡。可以只看本金、只看赠送或两样合起来；合计行取接口的 totals，只是按选的那样加起来。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '储值余额', description: '卡里的钱每天进多少、用多少、还剩多少；撤单和售后退回卡里的钱算负的耗卡。' })

type Report = components['schemas']['StoredValueReport']
type Day = components['schemas']['StoredValueDay']
type Totals = components['schemas']['StoredValueTotals']
type Part = 'all' | 'principal' | 'bonus'
type Flow = 'opening' | 'added' | 'upgrade' | 'consumed' | 'refunded' | 'adjusted' | 'closing'

const api = useApi()
const { today, range, report, loading, failed, retry } = useDayReport<Report>((from, to) => api.POST('/api/ops/get_stored_value_report', { body: { from, to } }))

const part = ref<Part>('all')
const parts: { key: Part, label: string }[] = [{ key: 'all', label: '本金加赠送' }, { key: 'principal', label: '只看本金' }, { key: 'bonus', label: '只看赠送' }]

// 一列的数：按选的看法取本金、赠送或两样相加（按分加）。
function value(row: Day | Totals, flow: Flow) {
  const p = cents(row[`${flow}_principal`])
  const b = cents(row[`${flow}_bonus`])
  return part.value === 'principal' ? p : part.value === 'bonus' ? b : p + b
}

const flows: { key: Flow, label: string, always: boolean }[] = [
  { key: 'opening', label: '期初', always: true },
  { key: 'added', label: '充入', always: true },
  { key: 'upgrade', label: '升级补差', always: false },
  { key: 'consumed', label: '耗卡', always: true },
  { key: 'refunded', label: '退卡', always: false },
  { key: 'adjusted', label: '调整', always: false },
  { key: 'closing', label: '期末', always: true }
]
// 升级、退卡、调整这段时间哪天有数才出现（按天看，不看合计：一进一出合计为零时各天也要对得上）。
const shownFlows = computed(() => {
  const rows = report.value?.rows ?? []
  return flows.filter(f => f.always || rows.some(r => cents(r[`${f.key}_principal`]) || cents(r[`${f.key}_bonus`])))
})

// 新的一天在上。
const days = computed(() => [...(report.value?.rows ?? [])].reverse())

function idle(row: Day) {
  return ['added', 'upgrade', 'consumed', 'refunded', 'adjusted'].every(f => !cents(row[`${f as Flow}_principal`]) && !cents(row[`${f as Flow}_bonus`]))
}

const brief = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  const { parts: out, say, bold } = briefWriter()
  const open = cents(t.opening_principal) + cents(t.opening_bonus)
  const close = cents(t.closing_principal) + cents(t.closing_bonus)
  say('卡里的钱从 ')
  bold(yuan(money(open)))
  say(' 变成 ')
  bold(yuan(money(close)), undefined, 'closing')
  if (Number(t.closing_bonus)) say(`（本金 ${yuan(t.closing_principal)}，赠送 ${yuan(t.closing_bonus)}）`)
  const added = cents(t.added_principal) + cents(t.added_bonus) + cents(t.upgrade_principal) + cents(t.upgrade_bonus)
  const used = cents(t.consumed_principal) + cents(t.consumed_bonus)
  const bits = []
  if (added) bits.push(`充入 ${yuan(money(added))}`)
  if (used) bits.push(`耗卡 ${yuan(money(used))}`)
  const refunded = cents(t.refunded_principal) + cents(t.refunded_bonus)
  if (refunded) bits.push(`退卡清掉 ${yuan(money(refunded))}`)
  say(bits.length ? `；这段时间${bits.join('，')}。` : '，这段时间没有进出。')
  return out
})

// 走势图：每天的期末余额（按选的看法）。
const chart = computed(() => {
  const rows = report.value?.rows ?? []
  return {
    categories: rows.map(r => r.business_date.slice(5)),
    series: [{ name: '期末余额', values: rows.map(r => value(r, 'closing') / 100) }]
  }
})

function cellClass(amount: number) {
  return amount === 0 ? 'text-dimmed' : ''
}

function cellText(amount: number) {
  if (amount === 0) return '—'
  return amount < 0 ? `−${yuan(money(-amount))}` : yuan(money(amount))
}
</script>

<template>
  <div class="space-y-4">
    <div v-if="today" class="flex flex-wrap items-start justify-between gap-2">
      <ReportRangeBar v-model="range" :today="today" day-only />
      <ViewTabs v-model="part" :items="parts" label="看哪部分钱" testid="stored-value-part" />
    </div>

    <LoadError v-if="failed && !report" title="报表没有读出来" hint="检查网络后再试一次" @retry="retry" />

    <template v-if="report">
      <ReportBrief :parts="brief" :class="loading && 'opacity-60'" />

      <section v-if="report.rows.length > 1">
        <ReportChart :label="`${range.from} 至 ${range.to} 每天的卡内余额，数字见下方明细表`" :categories="chart.categories" :series="chart.series" />
      </section>

      <section>
        <h2 class="sec" data-testid="stored-value-heading">每天<span class="font-normal text-dimmed">{{ parts.find(p => p.key === part)?.label }}</span></h2>
        <div class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="stored-value-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th scope="col" class="sticky left-0 bg-page px-3 py-2 text-left font-normal">日期</th>
                <th v-for="f in shownFlows" :key="f.key" scope="col" class="px-3 text-right font-normal whitespace-nowrap">{{ f.label }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in days" :key="row.business_date" class="row-hover border-t border-default" :data-day="row.business_date">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap">{{ row.business_date.slice(5) }} {{ weekdayOf(row.business_date) }}</th>
                <template v-if="idle(row)">
                  <td class="px-3 text-right whitespace-nowrap" :class="cellClass(value(row, 'opening'))" data-col="opening">{{ cellText(value(row, 'opening')) }}</td>
                  <td :colspan="shownFlows.length - 2" class="px-3 text-dimmed">没有进出</td>
                  <td class="px-3 text-right font-medium whitespace-nowrap" :class="cellClass(value(row, 'closing'))" data-col="closing">{{ cellText(value(row, 'closing')) }}</td>
                </template>
                <template v-else>
                  <td v-for="f in shownFlows" :key="f.key" class="px-3 text-right whitespace-nowrap" :class="[cellClass(value(row, f.key)), f.key === 'closing' && 'font-medium']" :data-col="f.key">{{ cellText(value(row, f.key)) }}</td>
                </template>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="stored-value-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <td v-for="f in shownFlows" :key="f.key" class="px-3 text-right whitespace-nowrap" :data-col="f.key">{{ cellText(value(report.totals, f.key)) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
