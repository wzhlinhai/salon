<script setup lang="ts">
// 营业报表（DESIGN.md 报表，照 report-01）：指标卡和上一段同长时间比，折线看走势，明细表一天或一月一行，末行合计。
// 合计行直接用接口的 totals，不在前端再加一遍。
import type { components } from '~/utils/api'
import type { Granularity, ReportRange } from '~/utils/report'

definePageMeta({ permission: 'report.view', title: '营业报表', description: '实收不含办卡充值；撤单在撤单那天冲回。' })

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

const metrics = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  const p = hasPrevious.value ? previous.value!.totals : undefined
  const cardUse = (row: Row) => Number(row.card_principal) + Number(row.card_bonus)
  return [
    { label: '实收', value: yuan(t.service_revenue), change: percentChange(t.service_revenue, p?.service_revenue), note: '不含办卡充值' },
    { label: '办卡充值', value: yuan(t.sales_revenue), change: percentChange(t.sales_revenue, p?.sales_revenue), note: '开卡充值、补差价、红包' },
    { label: '客数', value: formatGuest(t.guest_total), change: percentChange(t.guest_total, p?.guest_total), note: `共 ${t.checkout_count} 单` },
    { label: '卡扣', value: yuan(cardUse(t)), change: percentChange(cardUse(t), p && cardUse(p)), note: `本金 ${yuan(t.card_principal)}` },
    { label: '团购核销', value: yuan(t.groupbuy_deduct), change: percentChange(t.groupbuy_deduct, p?.groupbuy_deduct), note: `扣次 ${yuan(t.count_amount)}` }
  ]
})
const compareLabel = computed(() => report.value ? previousLabel(range.value) : '')

const rows = computed(() => report.value?.rows ?? [])
const onSiteColumns = computed(() => (report.value?.totals.on_site ?? []).map(m => ({ method: m.method, label: m.label })))
const nonSiteColumns = computed(() => (report.value?.totals.non_site ?? []).map(m => ({ method: m.method, label: m.label })))

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
      <div data-testid="report-metrics" class="stats grid grid-cols-2 gap-3 tabular-nums md:grid-cols-5" :class="loading && 'opacity-60'">
        <div v-for="(m, i) in metrics" :key="m.label" :data-metric="m.label" :class="i ? 'md:border-l md:border-default md:pl-4' : ''">
          <p class="text-xs text-dimmed">{{ m.label }}</p>
          <p data-testid="metric-value" class="text-2xl leading-[1.3] font-semibold" :class="zeroText(m.value) && 'text-dimmed'">{{ m.value }}</p>
          <p data-testid="metric-compare" class="text-xs text-muted">
            <template v-if="m.change !== null">
              <span :class="m.change >= 0 ? 'text-ink-green' : 'text-ink-red'">{{ m.change >= 0 ? '↑' : '↓' }} {{ Math.abs(m.change) }}%</span>
              {{ compareLabel }}
            </template>
            <template v-else>{{ m.note }}</template>
          </p>
        </div>
      </div>

      <section v-if="rows.length > 1">
        <div class="sec">走势</div>
        <div class="box">
          <ReportChart :label="`${range.from} 至 ${range.to} 的实收和办卡充值走势，数字见下方明细表`" :categories="chart.categories" :series="chart.series" />
        </div>
      </section>

      <section>
        <div class="sec">明细</div>
        <div class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="business-table">
            <thead class="border-b border-default text-xs text-dimmed">
              <tr>
                <th rowspan="2" scope="col" class="sticky left-0 bg-page px-3 py-1.5 text-left font-normal">{{ report.granularity === 'day' ? '日期' : '月份' }}</th>
                <th rowspan="2" scope="col" class="px-3 text-right font-normal whitespace-nowrap">实收</th>
                <th rowspan="2" scope="col" class="px-3 text-right font-normal whitespace-nowrap">办卡充值</th>
                <th :colspan="onSiteColumns.length" scope="colgroup" class="border-l border-default px-3 pt-1.5 text-center font-normal">现场收款</th>
                <th :colspan="nonSiteColumns.length" scope="colgroup" class="border-l border-default px-3 pt-1.5 text-center font-normal">非现场</th>
                <th rowspan="2" scope="col" class="border-l border-default px-3 text-right font-normal whitespace-nowrap">挂账还款</th>
                <th rowspan="2" scope="col" class="px-3 text-right font-normal whitespace-nowrap">售后退款</th>
                <th rowspan="2" scope="col" class="px-3 text-right font-normal whitespace-nowrap">客数</th>
                <th rowspan="2" scope="col" class="px-3 text-right font-normal whitespace-nowrap">结账单</th>
                <th rowspan="2" scope="col" class="px-3 text-right font-normal whitespace-nowrap">撤单</th>
              </tr>
              <tr>
                <th v-for="(c, i) in onSiteColumns" :key="c.method" scope="col" class="px-3 py-1.5 text-right font-normal whitespace-nowrap" :class="!i && 'border-l border-default'">{{ c.label }}</th>
                <th v-for="(c, i) in nonSiteColumns" :key="c.method" scope="col" class="px-3 py-1.5 text-right font-normal whitespace-nowrap" :class="!i && 'border-l border-default'">{{ c.label }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.period" class="border-t border-default" :data-period="row.period">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap">
                  {{ row.period }}<span v-if="report.granularity === 'day'" class="ms-1.5 text-xs text-dimmed">{{ weekdayOf(row.period) }}</span>
                </th>
                <td class="px-3 text-right font-medium whitespace-nowrap" :class="cell(row.service_revenue)" data-col="service_revenue">{{ yuanFixed(row.service_revenue) }}</td>
                <td class="px-3 text-right whitespace-nowrap" :class="cell(row.sales_revenue)" data-col="sales_revenue">{{ yuanFixed(row.sales_revenue) }}</td>
                <td v-for="(c, i) in onSiteColumns" :key="c.method" class="px-3 text-right whitespace-nowrap" :class="[cell(methodAmount(row.on_site, c.method)), !i && 'border-l border-default']" :data-col="c.method">{{ yuanFixed(methodAmount(row.on_site, c.method)) }}</td>
                <td v-for="(c, i) in nonSiteColumns" :key="c.method" class="px-3 text-right whitespace-nowrap" :class="[cell(methodAmount(row.non_site, c.method)), !i && 'border-l border-default']" :data-col="c.method">{{ yuanFixed(methodAmount(row.non_site, c.method)) }}</td>
                <td class="border-l border-default px-3 text-right whitespace-nowrap" :class="cell(row.debt_repayment)" data-col="debt_repayment">{{ yuanFixed(row.debt_repayment) }}</td>
                <td class="px-3 text-right whitespace-nowrap" :class="cell(row.aftersale_refund)" data-col="aftersale_refund">{{ yuanFixed(row.aftersale_refund) }}</td>
                <td class="px-3 text-right" :class="cell(row.guest_total)" data-col="guest_total">{{ formatGuest(row.guest_total) }}</td>
                <td class="px-3 text-right" :class="cell(row.checkout_count)" data-col="checkout_count">{{ row.checkout_count }}</td>
                <td class="px-3 text-right" :class="cell(row.void_count)" data-col="void_count">{{ row.void_count }}</td>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="business-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <td class="px-3 text-right whitespace-nowrap" data-col="service_revenue">{{ yuanFixed(report.totals.service_revenue) }}</td>
                <td class="px-3 text-right whitespace-nowrap" data-col="sales_revenue">{{ yuanFixed(report.totals.sales_revenue) }}</td>
                <td v-for="(c, i) in onSiteColumns" :key="c.method" class="px-3 text-right whitespace-nowrap" :class="!i && 'border-l border-default'" :data-col="c.method">{{ yuanFixed(methodAmount(report.totals.on_site, c.method)) }}</td>
                <td v-for="(c, i) in nonSiteColumns" :key="c.method" class="px-3 text-right whitespace-nowrap" :class="!i && 'border-l border-default'" :data-col="c.method">{{ yuanFixed(methodAmount(report.totals.non_site, c.method)) }}</td>
                <td class="border-l border-default px-3 text-right whitespace-nowrap" data-col="debt_repayment">{{ yuanFixed(report.totals.debt_repayment) }}</td>
                <td class="px-3 text-right whitespace-nowrap" data-col="aftersale_refund">{{ yuanFixed(report.totals.aftersale_refund) }}</td>
                <td class="px-3 text-right" data-col="guest_total">{{ formatGuest(report.totals.guest_total) }}</td>
                <td class="px-3 text-right" data-col="checkout_count">{{ report.totals.checkout_count }}</td>
                <td class="px-3 text-right" data-col="void_count">{{ report.totals.void_count }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
