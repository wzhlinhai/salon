<script setup lang="ts">
// 业绩工资月报（DESIGN.md 报表页）：一个月里每人做了多少业绩、拿多少工资放在一张表上看，末列工资占业绩。
// 工资取工资表（get_payroll，没锁定是按现在的数试算），业绩取员工业绩（get_staff_report，这个月 1 号到月底，当月到今天）。
// 业绩合计 = 项目 + 耗卡 + 手工费 + 销售（四种来源互不重叠，见 GLOSSARY「业绩」）；没记到人的业绩不进表。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'payroll.manage', title: '业绩工资', description: '每人一个月的业绩和工资；工资表没锁定前是按现在的数试算。' })

type Schema = components['schemas']
type Perf = Schema['StaffReportRow']
type Line = Schema['PayrollLine']

const api = useApi()
const route = useRoute()
const router = useRouter()
const { grant } = useMe()
const toast = useToast()
// 要看全店、不限天数的业绩才对得上工资表；只看自己或只看近几天的，别人的业绩会是空的。
const canPerformance = computed(() => {
  const g = grant('performance.view')
  return g?.scope === 'store' && g.windowDays === null
})

const today = ref('')
const months = ref<Schema['PayrollMonth'][]>([])
const month = ref(typeof route.query.month === 'string' ? route.query.month : '')
const payroll = ref<Schema['Payroll']>()
const perf = ref<Schema['StaffReport']>()
const loading = ref(false)
const failed = ref('')
const guard = useRequestGuard()

const monthItems = computed(() => months.value.map(m => ({ value: m.month, label: `${m.month.slice(0, 4)}年${Number(m.month.slice(5))}月${m.locked ? '（已锁定）' : ''}` })))
const status = computed(() => months.value.find(m => m.month === month.value))

function monthEnd(value: string) {
  return addDays(`${addMonths(value, 1)}-01`, -1)
}

async function boot() {
  const [s, m] = await Promise.all([api.POST('/api/ops/get_settings', { body: {} }), api.POST('/api/ops/list_payroll_months', { body: {} })])
  if (!s.data || !m.data) {
    failed.value = errorText(s.error ?? m.error)
    toast.add({ title: failed.value, color: 'error' })
    return
  }
  today.value = s.data.business_date
  months.value = m.data.entries
  if (!months.value.some(e => e.month === month.value)) month.value = months.value[0]?.month ?? ''
}

async function load() {
  if (!month.value || !today.value || !canPerformance.value) return
  const target = month.value
  const current = guard.next()
  loading.value = true
  failed.value = ''
  try {
    const end = monthEnd(target)
    const [p, r] = await Promise.all([
      api.POST('/api/ops/get_payroll', { body: { month: target } }),
      api.POST('/api/ops/get_staff_report', { body: { from: `${target}-01`, to: end > today.value ? today.value : end } })
    ])
    if (!current()) return
    if (!p.data || !r.data) {
      failed.value = errorText(p.error ?? r.error)
      payroll.value = undefined
      perf.value = undefined
      toast.add({ title: failed.value, color: 'error' })
      return
    }
    payroll.value = p.data
    perf.value = r.data
  } finally {
    if (current()) loading.value = false
  }
}

watch(month, (value) => {
  void router.replace({ query: value ? { month: value } : {} })
  void load()
})

// 地址里的月份不在列表里时 boot 会改 month，由上面的 watch 去读，这里不再读一遍。
onMounted(async () => {
  const before = month.value
  await boot()
  if (month.value === before) await load()
})

function retry() {
  void (today.value ? load() : boot().then(load))
}

function performanceOf(p: Perf | undefined) {
  if (!p) return 0
  return cents(p.item_performance) + cents(p.consume_performance) + cents(p.labor_performance) + cents(p.sale_performance)
}

// 一人一行：工资表里的人都在（底薪的人没做单也要发钱），业绩表里有数却不在工资表的也补上。
interface Row { id: string, name: string, active: boolean, perf?: Perf, line?: Line, performance: number, pay: number }
const rows = computed<Row[]>(() => {
  const byStaff = new Map((perf.value?.rows ?? []).filter(r => r.staff).map(r => [r.staff!.id, r]))
  const out: Row[] = (payroll.value?.lines ?? []).map((line) => {
    const p = byStaff.get(line.staff.id)
    byStaff.delete(line.staff.id)
    return { id: line.staff.id, name: line.staff.name, active: line.active, perf: p, line, performance: performanceOf(p), pay: cents(line.total) }
  })
  for (const p of byStaff.values()) out.push({ id: p.staff!.id, name: p.staff!.name, active: true, perf: p, performance: performanceOf(p), pay: 0 })
  return out.sort((a, b) => b.performance - a.performance || b.pay - a.pay)
})

// 合计：业绩取接口合计里记到人的部分（去掉「未分配」那行），工资取工资表的合计。
const unassigned = computed(() => (perf.value?.rows ?? []).find(r => !r.staff))
const totalPerformance = computed(() => performanceOf(perf.value?.totals) - performanceOf(unassigned.value))
const totalPay = computed(() => cents(payroll.value?.totals.total ?? '0'))

function ratio(pay: number, performance: number) {
  if (performance <= 0) return ''
  return `${Math.round((pay / performance) * 100)}%`
}

// 分转成金额；售后扣业绩可能让业绩、实发是负的，写成「−¥5」。
function yuanCents(value: number) {
  return value < 0 ? `−${yuan(money(-value))}` : yuan(money(value))
}

function cellCents(value: number) {
  return value === 0 ? '—' : yuanCents(value)
}

// 工资列：底薪、提成总在；保底补差、加减项这个月有人有数才出现（照工资表）。
const payColumns = computed(() => [
  { key: 'base_pay', label: '底薪' },
  { key: 'commission_total', label: '提成' },
  { key: 'guarantee_top_up', label: '保底补差' },
  { key: 'adjustments_total', label: '加减项' }
].filter(c => c.key === 'base_pay' || c.key === 'commission_total' || rows.value.some(r => r.line && Number(r.line[c.key as keyof Line]) !== 0)) as { key: 'base_pay' | 'commission_total' | 'guarantee_top_up' | 'adjustments_total', label: string }[])

// 顶上一段话：几个人业绩多少、工资多少、工资占业绩几成；业绩最高的是谁，占比最高的是谁（业绩满 ¥1000 才比，免得新人一单就排第一）。
const brief = computed(() => {
  if (!payroll.value || !perf.value || !rows.value.length) return []
  const { parts, say, bold } = briefWriter()
  say(`${Number(month.value.slice(5))} 月 ${rows.value.length} 个人业绩 `)
  bold(yuanCents(totalPerformance.value), undefined, 'performance')
  say('，工资 ')
  bold(yuanCents(totalPay.value), undefined, 'pay')
  const r = ratio(totalPay.value, totalPerformance.value)
  if (r) {
    say('，工资占业绩 ')
    bold(r)
  }
  say('。')
  const top = rows.value[0]
  if (rows.value.length > 1 && top && top.performance > 0) say(`业绩最高的是${top.name}（${yuanCents(top.performance)}）。`)
  const heavy = rows.value.filter(x => x.performance >= 100_000).sort((a, b) => b.pay / b.performance - a.pay / a.performance)[0]
  if (rows.value.length > 1 && heavy && heavy !== top) say(`工资占业绩最高的是${heavy.name}（${ratio(heavy.pay, heavy.performance)}）。`)
  if (status.value && !status.value.locked) say('工资表还没锁定，月底锁定前数还会变。')
  return parts
})

const ranked = computed(() => rows.value.filter(r => r.performance > 0).slice(0, 10))

function cell(value: string | number | undefined) {
  return !value || Number(value) === 0 ? 'text-dimmed' : ''
}

function signed(value: string) {
  const n = Number(value)
  return n < 0 ? `−${yuan(-n)}` : yuanCell(value)
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <USelect v-if="monthItems.length" v-model="month" aria-label="月份" :items="monthItems" class="min-w-36" data-testid="wages-month" />
    </PageActions>

    <EmptyState v-if="!canPerformance" art="chart" title="要能看全店员工的业绩才能对照" hint="请店长在角色里给「看业绩」选全店、不限天数" />
    <LoadError v-else-if="failed && !payroll" title="报表没有读出来" hint="检查网络后再试一次" @retry="retry" />
    <EmptyState v-else-if="today && !months.length" art="chart" title="还没有工资表" hint="有员工结过账以后这里会有数" />

    <template v-else-if="payroll && perf">
      <ReportBrief :parts="brief" :class="loading && 'opacity-60'" />

      <section v-if="ranked.length > 1">
        <h2 class="sec">谁做得多<span class="font-normal text-muted">业绩合计，右边是工资占业绩</span></h2>
        <ReportRank :rows="ranked.map(r => ({ key: r.id, name: r.name, value: r.performance, display: `${yuanCents(r.performance)} · ${ratio(r.pay, r.performance) || '—'}` }))" />
      </section>

      <section>
        <h2 class="sec">明细</h2>
        <EmptyState v-if="!rows.length" art="chart" title="这个月还没有人记业绩、算工资" hint="结账后这里会有数" />
        <div v-else class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="wages-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th scope="col" class="sticky left-0 bg-page px-3 py-2 text-left font-normal">员工</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">客数</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">项目业绩</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">耗卡业绩</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">销售业绩</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">业绩合计</th>
                <th v-for="c in payColumns" :key="c.key" scope="col" class="px-3 text-right font-normal whitespace-nowrap">{{ c.label }}</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">实发</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">工资占业绩</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.id" class="border-t border-default" :class="!row.active && 'text-muted'" :data-staff="row.name">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap">
                  <span class="inline-flex items-center gap-2"><PersonAvatar :name="row.name" size="sm" />{{ row.name }}<span v-if="!row.active" class="tag bg-tag-gray">离职</span></span>
                </th>
                <td class="px-3 text-right" :class="cell(row.perf?.guest_total)">{{ row.perf && Number(row.perf.guest_total) ? formatGuest(row.perf.guest_total) : '—' }}</td>
                <td class="px-3 text-right whitespace-nowrap" :class="cell(row.perf?.item_performance)" data-col="item">{{ signed(row.perf?.item_performance ?? '0') }}</td>
                <td class="px-3 text-right whitespace-nowrap" :class="cell(row.perf?.consume_performance)">{{ signed(row.perf?.consume_performance ?? '0') }}</td>
                <td class="px-3 text-right whitespace-nowrap" :class="cell(row.perf?.sale_performance)">{{ signed(row.perf?.sale_performance ?? '0') }}</td>
                <td class="px-3 text-right font-medium whitespace-nowrap" :class="cell(row.performance)" data-col="performance">{{ cellCents(row.performance) }}</td>
                <td v-for="c in payColumns" :key="c.key" class="px-3 text-right whitespace-nowrap" :class="cell(row.line?.[c.key])">{{ signed(row.line?.[c.key] ?? '0') }}</td>
                <td class="px-3 text-right font-medium whitespace-nowrap" :class="cell(row.pay)" data-col="pay">{{ cellCents(row.pay) }}</td>
                <td class="px-3 text-right" :class="row.performance ? '' : 'text-dimmed'" data-col="ratio">{{ ratio(row.pay, row.performance) || '—' }}</td>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="wages-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <td class="px-3 text-right">{{ formatGuest(Number(perf.totals.guest_total) - Number(unassigned?.guest_total ?? 0)) }}</td>
                <td class="px-3 text-right whitespace-nowrap">{{ yuanCents(cents(perf.totals.item_performance) - cents(unassigned?.item_performance ?? '0')) }}</td>
                <td class="px-3 text-right whitespace-nowrap">{{ yuanCents(cents(perf.totals.consume_performance) - cents(unassigned?.consume_performance ?? '0')) }}</td>
                <td class="px-3 text-right whitespace-nowrap">{{ yuanCents(cents(perf.totals.sale_performance) - cents(unassigned?.sale_performance ?? '0')) }}</td>
                <td class="px-3 text-right whitespace-nowrap" data-col="performance">{{ yuanCents(totalPerformance) }}</td>
                <td v-for="c in payColumns" :key="c.key" class="px-3 text-right whitespace-nowrap">{{ signed(payroll.totals[c.key]) }}</td>
                <td class="px-3 text-right whitespace-nowrap" data-col="pay">{{ yuanCents(totalPay) }}</td>
                <td class="px-3 text-right" data-col="ratio">{{ ratio(totalPay, totalPerformance) || '—' }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
