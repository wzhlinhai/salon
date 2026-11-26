<script setup lang="ts">
// 员工业绩（DESIGN.md 报表页）：先一段话说结论，排名条看谁做得多；一段营业日里每人一行，项目、耗卡、销售业绩（手工费本店不用，有数时才显示），客数和提成，末行合计。
// 店长看全店（没记到人的业绩单列「未分配」）；发型师、助理只看自己，后端按本人范围过滤。
// 点一个人从右侧滑出这段时间的每笔业绩和每笔提成。合计行直接用接口的 totals。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'performance.view', title: '员工业绩', description: '业绩按做单那天计；撤单在撤单那天冲回。' })

type StaffReport = components['schemas']['StaffReport']
type Row = components['schemas']['StaffReportRow']
type PerformanceEntry = components['schemas']['PerformanceEntry']
type Commission = components['schemas']['Commission']

const api = useApi()
const toast = useToast()
const { grant } = useMe()
const { today, range, report, loading, failed, retry } = useDayReport<StaffReport>((from, to) => api.POST('/api/ops/get_staff_report', { body: { from, to } }))
const self = computed(() => grant('performance.view')?.scope === 'self')

const rows = computed(() => report.value?.rows ?? [])
const nameOf = (row: Row) => row.staff?.name ?? '未分配'

// 顶上一段话（DESIGN.md 报表页）：三种业绩、客数、提成，店长看全店时再说谁最高。本人范围的员工句子从「你」说起。
const brief = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  const { parts, say, bold } = briefWriter()
  say(self.value ? '你这段时间项目业绩 ' : '这段时间项目业绩 ')
  bold(yuan(t.item_performance), undefined, 'item_performance')
  // 每句只在有数时说，不写「耗卡业绩 ¥0」这种零。
  if (Number(t.card_performance)) say(`（现金 ${yuan(t.cash_performance)}，卡金 ${yuan(t.card_performance)}）`)
  if (Number(t.consume_performance)) say(`，耗卡业绩 ${yuan(t.consume_performance)}`)
  if (Number(t.sale_performance)) say(`，销售业绩 ${yuan(t.sale_performance)}`)
  say('。客数 ')
  bold(formatGuest(t.guest_total))
  if (Number(t.guest_designated)) say(`，其中指定 ${formatGuest(t.guest_designated)}`)
  say('。提成 ')
  bold(yuan(t.commission_total))
  say('（阶梯提成月底才定）。')
  if (!self.value && ranked.value.length > 1) say(`员工里项目业绩最高的是${nameOf(ranked.value[0]!)}。`)
  return parts
})

// 排名条只比项目业绩前 10 名，从高到低；没有项目业绩的、没记到人的不进。不到两个人没有可比的，不画。全部人的数以明细表为准。
const RANK_TOP = 10
const ranked = computed(() => rows.value
  .filter(r => r.staff && Number(r.item_performance) > 0)
  .sort((a, b) => Number(b.item_performance) - Number(a.item_performance))
  .slice(0, RANK_TOP))

type MoneyKey = 'item_performance' | 'cash_performance' | 'card_performance' | 'consume_performance' | 'labor_performance' | 'sale_performance'
type GuestKey = 'guest_total' | 'guest_designated' | 'guest_not_designated' | 'guest_male' | 'guest_female'
const moneyColumns: { key: MoneyKey, label: string }[] = [
  { key: 'item_performance', label: '项目业绩' },
  { key: 'cash_performance', label: '其中现金' },
  { key: 'card_performance', label: '其中卡金' },
  { key: 'consume_performance', label: '耗卡业绩' },
  { key: 'labor_performance', label: '手工费' },
  { key: 'sale_performance', label: '销售业绩' }
]
const shownMoneyColumns = computed(() => moneyColumns.filter(c => c.key !== 'labor_performance' || rows.value.some(r => Number(r.labor_performance) !== 0)))
const guestColumns: { key: GuestKey, label: string }[] = [
  { key: 'guest_total', label: '客数' },
  { key: 'guest_designated', label: '指定' },
  { key: 'guest_not_designated', label: '非指定' },
  { key: 'guest_male', label: '男客' },
  { key: 'guest_female', label: '女客' }
]

function cell(value: string | number) {
  return Number(value) === 0 ? 'text-dimmed' : ''
}

// 明细抽屉：一个人这段时间的每笔业绩、每笔提成，一次 50 笔，往下点"再看 50 笔"。
const PAGE = 50
const picked = ref<Row | null>(null)
const open = ref(false)
const tab = ref<'performance' | 'commission'>('performance')
const tabs: { label: string, value: 'performance' | 'commission' }[] = [{ label: '业绩', value: 'performance' }, { label: '提成', value: 'commission' }]
const entries = ref<PerformanceEntry[]>([])
const commissions = ref<Commission[]>([])
const moreEntries = ref(false)
const moreCommissions = ref(false)
const detailLoading = ref(false)
const detailFailed = ref(false)
const detailGuard = useRequestGuard()

async function fetchEntries(staffId: string, before?: string) {
  return api.POST('/api/ops/list_performance', { body: { date_from: range.value.from, date_to: range.value.to, staff_id: staffId, limit: PAGE, before_id: before } })
}

async function fetchCommissions(staffId: string, before?: string) {
  return api.POST('/api/ops/list_commissions', { body: { date_from: range.value.from, date_to: range.value.to, staff_id: staffId, limit: PAGE, before_id: before } })
}

async function openDetail(row: Row) {
  if (!row.staff) return
  const staffId = row.staff.id
  picked.value = row
  tab.value = 'performance'
  entries.value = []
  commissions.value = []
  moreEntries.value = false
  moreCommissions.value = false
  detailFailed.value = false
  open.value = true
  const current = detailGuard.next()
  detailLoading.value = true
  try {
    const [perf, earned] = await Promise.all([fetchEntries(staffId), fetchCommissions(staffId)])
    if (!current()) return
    if (!perf.data || !earned.data) {
      detailFailed.value = true
      toast.add({ title: errorText(perf.error ?? earned.error), color: 'error' })
      return
    }
    entries.value = perf.data.entries
    moreEntries.value = perf.data.entries.length === PAGE
    commissions.value = earned.data.entries
    moreCommissions.value = earned.data.entries.length === PAGE
  } finally {
    if (current()) detailLoading.value = false
  }
}

async function loadMore() {
  const staffId = picked.value?.staff?.id
  if (!staffId) return
  const current = detailGuard.next()
  detailLoading.value = true
  try {
    if (tab.value === 'performance') {
      const { data, error } = await fetchEntries(staffId, entries.value.at(-1)?.id)
      if (!current()) return
      if (!data) return void toast.add({ title: errorText(error), color: 'error' })
      entries.value = [...entries.value, ...data.entries]
      moreEntries.value = data.entries.length === PAGE
    } else {
      const { data, error } = await fetchCommissions(staffId, commissions.value.at(-1)?.id)
      if (!current()) return
      if (!data) return void toast.add({ title: errorText(error), color: 'error' })
      commissions.value = [...commissions.value, ...data.entries]
      moreCommissions.value = data.entries.length === PAGE
    }
  } finally {
    if (current()) detailLoading.value = false
  }
}

const purchaseNames = { first: '首购', repeat: '复购' } as const

function signed(amount: string, reversal: boolean) {
  return reversal ? `-${yuanFixed(amount)}` : yuanFixed(amount)
}
</script>

<template>
  <div class="space-y-4">
    <ReportRangeBar v-if="today" v-model="range" :today="today" day-only />

    <LoadError v-if="failed && !report" title="报表没有读出来" hint="检查网络后再试一次" @retry="retry" />

    <template v-if="report">
      <p v-if="self" class="text-meta text-muted">只显示你自己的业绩。</p>

      <ReportBrief :parts="brief" :class="loading && 'opacity-60'" />

      <section v-if="ranked.length > 1">
        <div class="sec">谁做得多<span class="font-normal text-dimmed">项目业绩前 {{ RANK_TOP }} 名</span></div>
        <ReportRank :rows="ranked.map(r => ({ key: r.staff!.id, name: r.staff!.name, value: Number(r.item_performance), display: yuan(r.item_performance) }))" />
      </section>

      <section>
        <div class="sec">明细<span class="ms-auto font-normal text-dimmed">点一行看每笔业绩和提成</span></div>
        <EmptyState v-if="!rows.length" art="chart" title="这段时间没有业绩" hint="结账、开卡充值后这里会有数" />
        <div v-else class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="staff-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th scope="col" class="sticky left-0 bg-page px-3 py-2 text-left font-normal">员工</th>
                <th v-for="c in shownMoneyColumns" :key="c.key" scope="col" class="px-3 text-right font-normal whitespace-nowrap">{{ c.label }}</th>
                <th v-for="(c, i) in guestColumns" :key="c.key" scope="col" class="px-3 text-right font-normal whitespace-nowrap" :class="!i && 'border-l border-default'">{{ c.label }}</th>
                <th scope="col" class="px-3 text-right font-normal whitespace-nowrap">服务项次</th>
                <th scope="col" class="border-l border-default px-3 text-right font-normal whitespace-nowrap">提成</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="row in rows"
                :key="row.staff?.id ?? ''"
                class="border-t border-default"
                :class="row.staff && 'cursor-pointer hover:bg-hover'"
                :data-staff="nameOf(row)"
                @click="openDetail(row)"
              >
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap">
                  <button v-if="row.staff" type="button" class="font-medium hover:underline" @click.stop="openDetail(row)">{{ row.staff.name }}</button>
                  <span v-else class="text-muted" title="结账时没记到员工的业绩，没有个人明细">未分配</span>
                </th>
                <td v-for="c in shownMoneyColumns" :key="c.key" class="px-3 text-right whitespace-nowrap" :class="cell(row[c.key])" :data-col="c.key">{{ yuanCell(row[c.key]) }}</td>
                <td v-for="(c, i) in guestColumns" :key="c.key" class="px-3 text-right" :class="[cell(row[c.key]), !i && 'border-l border-default']" :data-col="c.key">{{ formatGuest(row[c.key]) }}</td>
                <td class="px-3 text-right" :class="cell(row.service_item_lines)" data-col="service_item_lines">{{ row.service_item_lines }}</td>
                <td class="border-l border-default px-3 text-right whitespace-nowrap" :class="cell(row.commission_total)" data-col="commission_total">{{ yuanCell(row.commission_total) }}</td>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="staff-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <td v-for="c in shownMoneyColumns" :key="c.key" class="px-3 text-right whitespace-nowrap" :data-col="c.key">{{ yuan(report.totals[c.key]) }}</td>
                <td v-for="(c, i) in guestColumns" :key="c.key" class="px-3 text-right" :class="!i && 'border-l border-default'" :data-col="c.key">{{ formatGuest(report.totals[c.key]) }}</td>
                <td class="px-3 text-right" data-col="service_item_lines">{{ report.totals.service_item_lines }}</td>
                <td class="border-l border-default px-3 text-right whitespace-nowrap" data-col="commission_total">{{ yuan(report.totals.commission_total) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>

    <USlideover
      v-model:open="open"
      :title="picked?.staff ? `${picked.staff.name} 的业绩和提成` : ''"
      :description="`${range.from} 至 ${range.to}；冲回是撤单或售后在那天写的负数。`"
    >
      <template #title>
        <span class="flex items-center gap-3"><PersonAvatar v-if="picked?.staff" :name="picked.staff.name" />{{ picked?.staff ? `${picked.staff.name} 的业绩和提成` : '' }}</span>
      </template>
      <template #body>
        <div v-if="picked" class="space-y-5" data-testid="staff-detail">
          <MethodPicker v-model="tab" :items="tabs" label="明细类别" />
          <p class="text-meta text-muted tabular-nums">
            <template v-if="tab === 'performance'">项目 {{ yuan(picked.item_performance) }} · 耗卡 {{ yuan(picked.consume_performance) }}<template v-if="Number(picked.labor_performance) !== 0"> · 手工费 {{ yuan(picked.labor_performance) }}</template> · 销售 {{ yuan(picked.sale_performance) }}</template>
            <template v-else>提成合计 {{ yuan(picked.commission_total) }}</template>
          </p>

          <LoadError v-if="detailFailed" title="明细没有读出来" hint="检查网络后再试一次" @retry="openDetail(picked)" />
          <template v-else-if="tab === 'performance'">
            <EmptyState v-if="!entries.length && !detailLoading" title="这段时间没有业绩" />
            <ul v-else class="divide-y divide-default text-sm" data-testid="staff-performance-list">
              <li v-for="e in entries" :key="e.id" class="py-2">
                <p class="flex items-center gap-2">
                  <span class="font-medium">{{ commissionSourceNames[e.kind] }}</span>
                  <span class="truncate text-muted">{{ e.item_name ?? performanceMethodNames[e.method] }}</span>
                  <span v-if="e.purchase" class="tag bg-tag-gray">{{ purchaseNames[e.purchase] }}</span>
                  <span v-if="e.reversal" class="tag bg-tag-gray">冲回</span>
                  <span class="ms-auto tabular-nums" :class="e.reversal && 'text-ink-red'">{{ signed(e.amount, e.reversal) }}</span>
                </p>
                <p class="text-meta text-muted">
                  {{ e.business_date }} {{ weekdayOf(e.business_date) }}
                  <template v-if="e.kind !== 'sale'"> · {{ performanceMethodNames[e.method] }} {{ signed(e.paid_amount, e.reversal) }} × {{ e.ratio }}%<template v-if="e.category_ratio !== 100"> × 类别 {{ e.category_ratio }}%</template></template>
                </p>
              </li>
            </ul>
          </template>

          <template v-else>
            <EmptyState v-if="!commissions.length && !detailLoading" title="这段时间没有提成" />
            <ul v-else class="divide-y divide-default text-sm" data-testid="staff-commission-list">
              <li v-for="c in commissions" :key="c.id" class="py-2">
                <p class="flex items-center gap-2">
                  <span class="font-medium">{{ commissionSourceNames[c.source] }}</span>
                  <span v-if="c.explanation.origin.item" class="truncate text-muted">{{ c.explanation.origin.item.name }}</span>
                  <span v-if="c.reversal" class="tag bg-tag-gray">冲回</span>
                  <span class="ms-auto tabular-nums" :class="c.reversal && 'text-ink-red'">{{ yuanFixed(c.amount) }}</span>
                </p>
                <p class="text-meta text-muted">{{ c.business_date }} · {{ commissionExplanation(c) }}</p>
              </li>
            </ul>
          </template>

          <p v-if="detailLoading" class="text-meta text-muted">正在读取…</p>
          <UButton
            v-else-if="tab === 'performance' ? moreEntries : moreCommissions"
            block
            color="neutral"
            variant="ghost"
            @click="loadMore"
          >
            再看 {{ PAGE }} 笔
          </UButton>
        </div>
      </template>
    </USlideover>
  </div>
</template>
