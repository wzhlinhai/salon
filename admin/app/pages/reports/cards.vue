<script setup lang="ts">
// 卡项报表（样稿 12）：先一段话说结论；每种卡一行一根条，收进、用掉、还剩三段，右边写还剩多少；逐列明细表折叠在最下面，对账时展开。
// 一段营业日里：开卡、充值、补差价收进来多少，耗卡、扣次用掉多少，退卡退了多少，以及结束那天的余额和剩余次数。升级过的卡按升级后的卡类型算。合计行用接口的 totals。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '卡项报表', description: '每种卡收进来多少、用掉多少；余额和剩余次数是结束那天营业结束时的。' })

type CardReport = components['schemas']['CardReport']
type Row = components['schemas']['CardReportRow']

const api = useApi()
const { today, range, report, loading, failed, retry } = useDayReport<CardReport>((from, to) => api.POST('/api/ops/get_card_report', { body: { from, to } }))

// 卡类型的种类、折扣、停售：只用来写每行名字下面那句，取不到就只写名字。
const types = ref(new Map<string, components['schemas']['CardType']>())
onMounted(async () => {
  const res = await api.POST('/api/ops/list_card_types', { body: {} })
  if (res.data) types.value = new Map(res.data.items.map(t => [t.id, t]))
})

const rows = computed(() => report.value?.rows ?? [])
// 接口把每种卡类型都列出来，全是 0 也列；这段时间没有任何进出、结束时也没有余额才算空。
const idle = computed(() => rows.value.every(r => Object.entries(r).every(([k, v]) => k === 'card_type' || Number(v) === 0)))

// 表头分组：每组若干列，key 是行上的字段；count 为整数列（张数、次数），其余为金额。
type Key = Exclude<keyof Row, 'card_type'>
const groups: { label: string, columns: { key: Key, label: string, count?: boolean }[] }[] = [
  { label: '开卡', columns: [{ key: 'open_count', label: '张数', count: true }, { key: 'open_principal', label: '本金' }, { key: 'open_bonus', label: '赠送' }] },
  { label: '充值', columns: [{ key: 'recharge_principal', label: '本金' }, { key: 'recharge_bonus', label: '赠送' }] },
  { label: '升级', columns: [{ key: 'upgrade', label: '补差价' }] },
  { label: '耗卡', columns: [{ key: 'consumed_principal', label: '本金' }, { key: 'consumed_bonus', label: '赠送' }] },
  { label: '扣次', columns: [{ key: 'count_used', label: '次数', count: true }, { key: 'count_amount', label: '金额' }] },
  { label: '退卡', columns: [{ key: 'refund_count', label: '张数', count: true }, { key: 'refund_amount', label: '金额' }] },
  { label: '结束时', columns: [{ key: 'ending_balance', label: '余额' }, { key: 'ending_counts', label: '剩余次数', count: true }] }
]

function show(row: Row, key: Key, count?: boolean) {
  const value = row[key]
  return count ? (value ? String(value) : '—') : yuanCell(String(value))
}

function total(row: Row, key: Key, count?: boolean) {
  return count ? String(row[key]) : yuan(String(row[key]))
}

function cents(value: string) {
  return Math.round(Number(value) * 100)
}

function sum(...values: string[]) {
  return values.reduce((s, v) => s + cents(v), 0) / 100
}

// 每种卡一行：储值卡的条是收进、用掉、还剩的钱；次卡开卡流水本金是 0，条改用扣掉的次数和还剩的次数。
// 这段时间没有进出、结束时也空了的卡不列。
const cardRows = computed(() => rows.value.filter(r => Object.entries(r).some(([k, v]) => k !== 'card_type' && Number(v) !== 0)).map((r) => {
  const type = types.value.get(r.card_type.id)
  const counted = type ? type.kind !== 'stored_value' : r.ending_counts > 0 || r.count_used > 0
  const sold = sum(r.open_principal, r.recharge_principal, r.upgrade)
  const used = sum(r.consumed_principal, r.consumed_bonus)
  const discount = Number(type?.discount_percent ?? 100)
  const meta = [
    type ? ({ stored_value: '储值', count: '次卡', package: '套餐' } as Record<string, string>)[type.kind] ?? '' : '',
    type && !counted && discount < 100 ? `${discount / 10} 折` : '',
    type && !type.active ? '已停售' : ''
  ].filter(Boolean).join(' · ')
  const activity = [
    r.open_count ? `开 ${r.open_count} 张` : '',
    Number(r.recharge_principal) ? `充值 ${yuan(r.recharge_principal)}` : '',
    Number(r.upgrade) ? `升级补差 ${yuan(r.upgrade)}` : '',
    r.count_used ? `扣 ${r.count_used} 次` : '',
    r.refund_count ? `退 ${r.refund_count} 张` : ''
  ].filter(Boolean).join(' · ')
  return {
    id: r.card_type.id,
    name: r.card_type.name,
    meta,
    activity: activity || '这段时间没有开卡和充值',
    bar: counted
      ? [{ key: 'used', value: r.count_used }, { key: 'left', value: r.ending_counts }]
      : [{ key: 'in', value: sold }, { key: 'used', value: used }, { key: 'left', value: Number(r.ending_balance) }],
    left: counted ? `剩 ${r.ending_counts} 次` : `剩 ${yuan(r.ending_balance)}`,
    sub: counted
      ? (r.count_used ? `扣 ${r.count_used} 次 · 折 ${yuan(r.count_amount)}` : '这段时间没扣次')
      : `收 ${yuan(sold)} · 用 ${yuan(used)}`
  }
}))

const barColor: Record<string, string> = { in: 'bg-dye-1', used: 'bg-dye-2', left: 'bg-tag-gray' }

// 顶上一段话（DESIGN.md 报表页）：收进来多少、用掉多少、退了多少，结束那天卡里还剩多少。
const brief = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  const { parts, say, bold } = briefWriter()
  // 次卡开卡的流水本金是 0（卖价记在次数上），所以这里只算得到储值部分的进账。
  const sold = (cents(t.open_principal) + cents(t.recharge_principal) + cents(t.upgrade)) / 100
  const used = (cents(t.consumed_principal) + cents(t.consumed_bonus)) / 100
  // 每句只在有数时说，不写「扣次 0 次折 ¥0」这种零。
  if (sold || t.open_count) {
    say(t.open_count ? `这段时间开卡 ${t.open_count} 张，储值收进 ` : '这段时间储值收进 ')
    bold(yuan(sold))
  } else say('这段时间没有开卡、充值')
  const out = [
    used ? `用掉 ${yuan(used)}` : '',
    t.count_used ? `扣次 ${t.count_used} 次` : '',
    t.refund_count ? `退卡 ${t.refund_count} 张共 ${yuan(t.refund_amount)}` : ''
  ].filter(Boolean)
  say(out.length ? `，${out.join('，')}。` : '。')
  say('卡里还有 ')
  bold(yuan(t.ending_balance), undefined, 'ending_balance')
  say(' 没用')
  if (t.ending_counts) say(`，另有 ${t.ending_counts} 次没扣`)
  // 卡里剩的钱是收进的几倍：收进太少时倍数没意义，不说。
  const ratio = sold >= 100 ? Number(t.ending_balance) / sold : 0
  say(ratio >= 1.5 ? `，是这段时间收进的 ${ratio.toFixed(1)} 倍。` : '。')
  say('次卡卖价不算在收进里。')
  return parts
})

function cell(value: string | number) {
  return Number(value) === 0 ? 'text-dimmed' : ''
}
</script>

<template>
  <div class="space-y-4">
    <ReportRangeBar v-if="today" v-model="range" :today="today" day-only />

    <LoadError v-if="failed && !report" title="报表没有读出来" hint="检查网络后再试一次" @retry="retry" />

    <template v-if="report">
      <ReportBrief :parts="brief" :class="loading && 'opacity-60'" />

      <section>
        <h2 class="sec">每种卡<span class="font-normal text-dimmed">收进 · 用掉 · 还剩</span></h2>
        <EmptyState v-if="!rows.length" art="card" title="还没有卡类型" hint="在“卡类型”里新建后这里会有数" />
        <EmptyState v-else-if="idle" art="card" title="这段时间没有卡的进出" hint="开卡、充值、耗卡后这里会有数" />
        <template v-else>
          <ul class="mb-2 flex gap-4 text-meta text-muted" aria-hidden="true">
            <li v-for="l in [{ key: 'in', label: '收进' }, { key: 'used', label: '用掉' }, { key: 'left', label: '还剩' }]" :key="l.key" class="flex items-center gap-2">
              <i class="size-2 rounded-full" :class="barColor[l.key]" />{{ l.label }}
            </li>
          </ul>
          <div class="divide-y divide-default border-b border-default" data-testid="card-rows">
            <div v-for="c in cardRows" :key="c.id" class="grid grid-cols-[9rem_minmax(0,1fr)_10rem] items-start gap-6 py-3" :data-card-row="c.name">
              <div class="min-w-0">
                <p class="truncate">{{ c.name }}</p>
                <p v-if="c.meta" class="text-meta text-muted">{{ c.meta }}</p>
              </div>
              <div class="pt-2">
                <div class="flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-tag-gray" aria-hidden="true">
                  <i v-for="seg in c.bar" :key="seg.key" :class="barColor[seg.key]" :style="{ flexGrow: Math.max(seg.value, 0), flexBasis: 0 }" />
                </div>
                <p class="mt-1 text-meta text-muted">{{ c.activity }}</p>
              </div>
              <div class="text-right tabular-nums">
                <p class="font-medium" data-col="left">{{ c.left }}</p>
                <p class="text-meta text-muted">{{ c.sub }}</p>
              </div>
            </div>
          </div>
        </template>
      </section>

      <details v-if="rows.length && !idle" class="border-t border-default pt-3" data-testid="card-details">
        <summary class="cursor-pointer text-sm text-muted">逐列明细（开卡、充值、升级、耗卡、扣次、退卡，本金和赠送分开）· 对账时展开</summary>
        <div class="mt-3 overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="card-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th rowspan="2" scope="col" class="sticky left-0 bg-page px-3 py-2 text-left font-normal">卡类型</th>
                <th v-for="g in groups" :key="g.label" :colspan="g.columns.length" scope="colgroup" class="px-3 pt-2 text-center font-normal">{{ g.label }}</th>
              </tr>
              <tr>
                <template v-for="g in groups" :key="g.label">
                  <th v-for="c in g.columns" :key="c.key" scope="col" class="px-3 py-2 text-right font-normal whitespace-nowrap">{{ c.label }}</th>
                </template>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.card_type.id" class="border-t border-default" :data-card-type="row.card_type.name">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap">{{ row.card_type.name }}</th>
                <template v-for="g in groups" :key="g.label">
                  <td v-for="c in g.columns" :key="c.key" class="px-3 text-right whitespace-nowrap" :class="cell(row[c.key])" :data-col="c.key">{{ show(row, c.key, c.count) }}</td>
                </template>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="card-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <template v-for="g in groups" :key="g.label">
                  <td v-for="c in g.columns" :key="c.key" class="px-3 text-right whitespace-nowrap" :data-col="c.key">{{ total(report.totals, c.key, c.count) }}</td>
                </template>
              </tr>
            </tfoot>
          </table>
        </div>
      </details>
    </template>
  </div>
</template>
