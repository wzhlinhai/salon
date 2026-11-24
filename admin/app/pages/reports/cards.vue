<script setup lang="ts">
// 卡项报表（DESIGN.md 报表页）：先一段话说结论，再是明细表。一段营业日里每种卡一行：开卡、充值、补差价收进来多少，耗卡、扣次用掉多少，退卡退了多少，
// 以及结束那天全部卡的余额和剩余次数。升级过的卡按升级后的卡类型算。合计行用接口的 totals。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '卡项报表', description: '每种卡收进来多少、用掉多少；余额和剩余次数是结束那天营业结束时的。' })

type CardReport = components['schemas']['CardReport']
type Row = components['schemas']['CardReportRow']

const api = useApi()
const { today, range, report, loading, failed, retry } = useDayReport<CardReport>((from, to) => api.POST('/api/ops/get_card_report', { body: { from, to } }))

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
  return count ? String(value) : yuanFixed(String(value))
}

function cents(value: string) {
  return Math.round(Number(value) * 100)
}

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
    say('这段时间储值本金进账 ')
    bold(yuan(sold))
    say(`${t.open_count ? `，开卡 ${t.open_count} 张` : ''}（次卡卖价不在内）。`)
  } else say('这段时间没有开卡、充值。')
  const out = [
    used ? `耗卡 ${yuan(used)}（本金 ${yuan(t.consumed_principal)}）` : '',
    t.count_used ? `扣次 ${t.count_used} 次折 ${yuan(t.count_amount)}` : '',
    t.refund_count ? `退卡 ${t.refund_count} 张共 ${yuan(t.refund_amount)}` : ''
  ].filter(Boolean)
  if (out.length) say(`${out.join('，')}。`)
  say(`到 ${range.value.to} 营业结束，卡里还有 `)
  bold(yuan(t.ending_balance), undefined, 'ending_balance')
  say(t.ending_counts ? `和 ${t.ending_counts} 次没用。` : ' 没用。')
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

      <section>
        <div class="sec">明细</div>
        <EmptyState v-if="!rows.length" class="box" art="card" title="还没有卡类型" hint="在“卡类型”里新建后这里会有数" />
        <EmptyState v-else-if="idle" class="box" art="card" title="这段时间没有卡的进出" hint="开卡、充值、耗卡后这里会有数" />
        <div v-else class="overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="card-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th rowspan="2" scope="col" class="sticky left-0 bg-page px-3 py-1.5 text-left font-normal">卡类型</th>
                <th v-for="g in groups" :key="g.label" :colspan="g.columns.length" scope="colgroup" class="border-l border-default px-3 pt-1.5 text-center font-normal">{{ g.label }}</th>
              </tr>
              <tr>
                <template v-for="g in groups" :key="g.label">
                  <th v-for="(c, i) in g.columns" :key="c.key" scope="col" class="px-3 py-1.5 text-right font-normal whitespace-nowrap" :class="!i && 'border-l border-default'">{{ c.label }}</th>
                </template>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.card_type.id" class="border-t border-default" :data-card-type="row.card_type.name">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left font-normal whitespace-nowrap">{{ row.card_type.name }}</th>
                <template v-for="g in groups" :key="g.label">
                  <td v-for="(c, i) in g.columns" :key="c.key" class="px-3 text-right whitespace-nowrap" :class="[cell(row[c.key]), !i && 'border-l border-default']" :data-col="c.key">{{ show(row, c.key, c.count) }}</td>
                </template>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="card-totals">
                <th scope="row" class="sticky left-0 bg-page px-3 py-2 text-left">合计</th>
                <template v-for="g in groups" :key="g.label">
                  <td v-for="(c, i) in g.columns" :key="c.key" class="px-3 text-right whitespace-nowrap" :class="!i && 'border-l border-default'" :data-col="c.key">{{ show(report.totals, c.key, c.count) }}</td>
                </template>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
