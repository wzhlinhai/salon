<script setup lang="ts">
// 营业报表（样稿 11）：先一段话说结论（和上一段同长时间比），折线看走势，明细表一天或一月一行、新的在上，末行合计。
// 表格没竖线、金额不带 .00、0 写成 —；整段时间都是 0 的收款方式和附加列不出现。合计行直接用接口的 totals，不在前端再加一遍。
import type { components } from '~/utils/api'
import type { Granularity, ReportRange } from '~/utils/report'

definePageMeta({ permission: 'report.view', title: '营业报表', width: 'wide', description: '实收不含办卡充值；撤单在撤单那天冲回。' })

type BusinessReport = components['schemas']['BusinessReport']
type Row = components['schemas']['BusinessReportRow']

const api = useApi()
const route = useRoute()
const router = useRouter()
const toast = useToast()
const today = ref('')
const range = ref<ReportRange>({ granularity: 'day', from: '', to: '' })
const report = ref<BusinessReport | null>(null)
const previous = ref<BusinessReport | null>(null)
const loading = ref(false)
const failed = ref(false)
const loadGuard = useRequestGuard()

function fromQuery(): ReportRange | null {
  const { g, from, to } = route.query
  const granularity: Granularity = g === 'month' ? 'month' : 'day'
  const pattern = granularity === 'month' ? /^\d{4}-\d{2}$/ : /^\d{4}-\d{2}-\d{2}$/
  if (typeof from !== 'string' || typeof to !== 'string' || !pattern.test(from) || !pattern.test(to)) return null
  const asked = { granularity, from, to }
  return rangeProblem(asked) ? null : asked
}

async function load() {
  // 范围无效时也要作废还在路上的旧请求，免得旧范围的结果落到新范围下面。
  const current = loadGuard.next()
  if (rangeProblem(range.value)) {
    loading.value = false
    return
  }
  const asked = { ...range.value }
  loading.value = true
  failed.value = false
  try {
    const before = previousRange(asked)
    const [now, then] = await Promise.all([
      api.POST('/api/ops/get_business_report', { body: asked }),
      api.POST('/api/ops/get_business_report', { body: before })
    ])
    if (!current()) return
    if (!now.data) {
      failed.value = true
      report.value = null
      toast.add({ title: errorText(now.error), color: 'error' })
      return
    }
    report.value = now.data
    // 上一段可能超出本人能查的时间窗口，查不到就不比。
    previous.value = then.data ?? null
  } finally {
    if (current()) loading.value = false
  }
}

// 先取营业日，再定范围；范围一变 watch 就去查。
async function start() {
  failed.value = false
  const settings = await api.POST('/api/ops/get_settings', { body: {} })
  if (!settings.data) {
    failed.value = true
    toast.add({ title: errorText(settings.error), color: 'error' })
    return
  }
  today.value = settings.data.business_date
  range.value = fromQuery() ?? defaultRange('day', today.value)
}

function retry() {
  void (today.value ? load() : start())
}

onMounted(start)

watch(range, (value) => {
  void router.replace({ query: { g: value.granularity, from: value.from, to: value.to } })
  void load()
}, { deep: true })

const hasPrevious = computed(() => {
  const t = previous.value?.totals
  return !!t && [t.service_revenue, t.sales_revenue, t.guest_total, t.card_principal, t.card_bonus, t.groupbuy_deduct].some(v => Number(v) !== 0)
})

// 顶上一段话（DESIGN.md 报表页）：实收、办卡充值、客数各和上一段比，再说单数；卡扣、团购、扣次有数才说。
const brief = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  const p = hasPrevious.value ? previous.value!.totals : undefined
  const label = previousLabel(range.value)
  const { parts, say, bold, change } = briefWriter()
  const cardUse = (row: Row) => Number(row.card_principal) + Number(row.card_bonus)
  say('实收 ')
  bold(yuan(t.service_revenue), undefined, 'service_revenue')
  change(t.service_revenue, p?.service_revenue, label)
  say('；另有办卡充值 ')
  bold(yuan(t.sales_revenue), undefined, 'sales_revenue')
  change(t.sales_revenue, p?.sales_revenue, label)
  say('。来了 ')
  bold(formatGuest(t.guest_total), undefined, 'guest_total')
  say(' 位')
  change(t.guest_total, p?.guest_total, label)
  const guests = Number(t.guest_total)
  if (guests >= 1) say(`，客单 ${yuan(Math.round(Number(t.service_revenue) / guests))}`)
  say(`，结账 ${t.checkout_count} 单`)
  if (t.void_count) say(`，撤单 ${t.void_count} 单`)
  say('。')
  if (busiest.value) say(`${busiest.value.weekday}最忙，平均 ${yuan(busiest.value.average)} 一天。`)
  const paidBy = [
    cardUse(t) ? `卡扣 ${yuan(cardUse(t))}（本金 ${yuan(t.card_principal)}）` : '',
    Number(t.groupbuy_deduct) ? `团购核销 ${yuan(t.groupbuy_deduct)}` : '',
    Number(t.count_amount) ? `扣次 ${yuan(t.count_amount)}` : ''
  ].filter(Boolean)
  if (paidBy.length) say(`其中${paidBy.join('，')}。`)
  return parts
})

const rows = computed(() => report.value?.rows ?? [])
const byDay = computed(() => report.value?.granularity === 'day')
// 表里新的在上；走势图仍按时间从左到右。
const tableRows = computed(() => [...rows.value].reverse())

// 一周里哪天最忙：按日看、只算开了单的日子（今天还没营业完，不算），平均每天实收最高的那个星期几；
// 每个星期几都要有至少两天可比，才说这句。
const busiest = computed(() => {
  if (!byDay.value) return null
  const sums = new Map<string, { total: number, days: number }>()
  for (const r of rows.value) {
    if (!r.checkout_count || r.period === today.value) continue
    const w = weekdayOf(r.period)
    const entry = sums.get(w) ?? { total: 0, days: 0 }
    entry.total += Number(r.service_revenue)
    entry.days++
    sums.set(w, entry)
  }
  if (!sums.size || [...sums.values()].some(e => e.days < 2)) return null
  let best: { weekday: string, average: number } | null = null
  for (const [weekday, { total, days }] of sums) {
    const average = Math.round(total / days)
    if (!best || average > best.average) best = { weekday, average }
  }
  return best && best.average > 0 ? best : null
})

// 收款方式列只出现这段时间收过钱的；挂账还款、售后退款整段没有一笔时也不出现。
// 合计为 0 不够：撤单冲回会让整段合计归零，但表身里还有那几天的数。
function used(total: string, values: string[]) {
  return !zeroText(total) || values.some(v => !zeroText(v))
}

const methodColumns = (side: 'on_site' | 'non_site') => (report.value?.totals[side] ?? [])
  .filter(m => used(m.amount, rows.value.map(r => r[side].find(x => x.method === m.method)?.amount ?? '0')))
  .map(m => ({ method: m.method, label: m.label }))
const onSiteColumns = computed(() => methodColumns('on_site'))
const nonSiteColumns = computed(() => methodColumns('non_site'))
const extraColumns = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  return ([
    { key: 'debt_repayment', label: '挂账还款' },
    { key: 'aftersale_refund', label: '售后退款' }
  ] as const).filter(c => used(t[c.key], rows.value.map(r => r[c.key])))
})
const methodCount = computed(() => onSiteColumns.value.length + nonSiteColumns.value.length + extraColumns.value.length + 1)

// 一整天什么都没有（没收款、没办卡、没客人）：这一行只写「没开单」。
function idle(row: Row) {
  return !row.checkout_count && !row.void_count && zeroText(row.service_revenue) && zeroText(row.sales_revenue) && zeroText(row.debt_repayment) && zeroText(row.aftersale_refund) && zeroText(row.guest_total)
}

function periodText(period: string) {
  return byDay.value ? `${period.slice(5)} ${weekdayOf(period)}` : period
}

function methodAmount(list: Row['on_site'], method: string) {
  return list.find(m => m.method === method)?.amount ?? '0.00'
}

const chart = computed(() => ({
  categories: rows.value.map(r => report.value?.granularity === 'day' ? r.period.slice(5) : r.period),
  series: [
    { name: '实收', values: rows.value.map(r => Number(r.service_revenue)) },
    { name: '办卡充值', values: rows.value.map(r => Number(r.sales_revenue)) }
  ]
}))

function cell(value: string | number) {
  return zeroText(value) || Number(value) === 0 ? 'text-dimmed' : ''
}
</script>

<template>
  <div class="space-y-4">
    <ReportRangeBar v-if="today" v-model="range" :today="today" />

    <EmptyState v-if="failed && !report" class="box" icon="i-lucide-circle-alert" title="报表没有读出来" hint="检查网络后再试一次">
      <UButton color="neutral" variant="outline" @click="retry">重试</UButton>
    </EmptyState>

    <template v-if="report">
      <ReportBrief :parts="brief" :class="loading && 'opacity-60'" />

      <section v-if="rows.length > 1">
        <ReportChart :label="`${range.from} 至 ${range.to} 的实收和办卡充值走势，数字见下方明细表`" :categories="chart.categories" :series="chart.series" />
      </section>

      <section>
        <h2 class="sec">{{ byDay ? '每天' : '每月' }}<span class="font-normal text-dimmed">按收款方式</span></h2>
        <div class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="business-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th scope="col" class="sticky left-0 bg-page px-3 py-2 text-left font-normal">{{ byDay ? '日期' : '月份' }}</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">实收</th>
                <th v-for="c in [...onSiteColumns, ...nonSiteColumns]" :key="c.method" scope="col" class="px-3 text-right font-normal whitespace-nowrap">{{ c.label }}</th>
                <th v-for="c in extraColumns" :key="c.key" scope="col" class="px-3 text-right font-normal whitespace-nowrap">{{ c.label }}</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">办卡充值</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">客数</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">结账单</th>
                <th v-if="report.totals.void_count" scope="col" class="px-3 text-right font-normal whitespace-nowrap">撤单</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in tableRows" :key="row.period" class="row-hover border-t border-default" :data-period="row.period">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap">{{ periodText(row.period) }}</th>
                <template v-if="idle(row)">
                  <td class="px-3 text-right text-dimmed">—</td>
                  <td :colspan="methodCount + 2 + (report.totals.void_count ? 1 : 0)" class="px-3 text-dimmed">没开单</td>
                </template>
                <template v-else>
                  <td class="px-3 text-right font-medium whitespace-nowrap" :class="cell(row.service_revenue)" data-col="service_revenue">{{ yuanCell(row.service_revenue) }}</td>
                  <td v-for="c in onSiteColumns" :key="c.method" class="px-3 text-right whitespace-nowrap" :class="cell(methodAmount(row.on_site, c.method))" :data-col="c.method">{{ yuanCell(methodAmount(row.on_site, c.method)) }}</td>
                  <td v-for="c in nonSiteColumns" :key="c.method" class="px-3 text-right whitespace-nowrap" :class="cell(methodAmount(row.non_site, c.method))" :data-col="c.method">{{ yuanCell(methodAmount(row.non_site, c.method)) }}</td>
                  <td v-for="c in extraColumns" :key="c.key" class="px-3 text-right whitespace-nowrap" :class="cell(row[c.key])" :data-col="c.key">{{ yuanCell(row[c.key]) }}</td>
                  <td class="px-3 text-right whitespace-nowrap" :class="cell(row.sales_revenue)" data-col="sales_revenue">{{ yuanCell(row.sales_revenue) }}</td>
                  <td class="px-3 text-right" :class="cell(row.guest_total)" data-col="guest_total">{{ zeroText(row.guest_total) ? '—' : formatGuest(row.guest_total) }}</td>
                  <td class="px-3 text-right" :class="cell(row.checkout_count)" data-col="checkout_count">{{ row.checkout_count || '—' }}</td>
                  <td v-if="report.totals.void_count" class="px-3 text-right" :class="cell(row.void_count)" data-col="void_count">{{ row.void_count || '—' }}</td>
                </template>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="business-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <td class="px-3 text-right whitespace-nowrap" data-col="service_revenue">{{ yuan(report.totals.service_revenue) }}</td>
                <td v-for="c in onSiteColumns" :key="c.method" class="px-3 text-right whitespace-nowrap" :data-col="c.method">{{ yuan(methodAmount(report.totals.on_site, c.method)) }}</td>
                <td v-for="c in nonSiteColumns" :key="c.method" class="px-3 text-right whitespace-nowrap" :data-col="c.method">{{ yuan(methodAmount(report.totals.non_site, c.method)) }}</td>
                <td v-for="c in extraColumns" :key="c.key" class="px-3 text-right whitespace-nowrap" :data-col="c.key">{{ yuan(report.totals[c.key]) }}</td>
                <td class="px-3 text-right whitespace-nowrap" data-col="sales_revenue">{{ yuan(report.totals.sales_revenue) }}</td>
                <td class="px-3 text-right" data-col="guest_total">{{ formatGuest(report.totals.guest_total) }}</td>
                <td class="px-3 text-right" data-col="checkout_count">{{ report.totals.checkout_count }}</td>
                <td v-if="report.totals.void_count" class="px-3 text-right" data-col="void_count">{{ report.totals.void_count }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
