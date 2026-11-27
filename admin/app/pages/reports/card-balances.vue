<script setup lang="ts">
// 套餐结余和到期（DESIGN.md 报表页，照欠款对账）：不分时间段，到今天为止还有余额或次数的卡，按到期日从近到远一张一行。
// 口径取 get_card_balance_report：价值 = 剩下的购买次数 × 开卡时的单次金额 + 本金，赠送次数和赠送金不计；退了的卡不列。
// 要看顾客（customer.view）才列得出是谁的卡；可以只看一种卡、只看 30 天或 90 天内到期的（已过期的也在里面）。点一行进顾客档案。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '卡结余和到期', description: '到今天还有余额或次数的卡，到期近的在上；价值按购买的次数和本金算，赠送的不算。' })

type Report = components['schemas']['CardBalanceReport']
type Row = components['schemas']['CardBalanceRow']
type Kind = 'all' | Row['card_type']['kind']
type Within = 'all' | '30' | '90'

const api = useApi()
const { can } = useMe()
const canCustomers = computed(() => can('customer.view'))
const report = ref<Report>()
const loading = ref(false)
const failed = ref(false)
const guard = useRequestGuard()
const toast = useToast()

const kind = ref<Kind>('all')
const kinds: { key: Kind, label: string }[] = [{ key: 'all', label: '所有卡' }, { key: 'stored_value', label: '储值卡' }, { key: 'count', label: '次卡' }, { key: 'package', label: '套餐' }]
const within = ref<Within>('all')
const withins: { key: Within, label: string }[] = [{ key: 'all', label: '不限到期' }, { key: '90', label: '90 天内到期' }, { key: '30', label: '30 天内到期' }]
const kindLabels: Record<Row['card_type']['kind'], string> = { stored_value: '储值卡', count: '次卡', package: '套餐' }

// 到期 30 天内（含已过期）标红。
const SOON = 30

async function load() {
  if (!canCustomers.value) return
  const current = guard.next()
  loading.value = true
  failed.value = false
  try {
    const body: components['schemas']['CardBalanceReportInput'] = {}
    if (kind.value !== 'all') body.kind = kind.value
    if (within.value !== 'all') body.expiring_within_days = Number(within.value)
    const { data, error } = await api.POST('/api/ops/get_card_balance_report', { body })
    if (!current()) return
    if (data) {
      report.value = data
      return
    }
    // 换了筛选没读出来，不留着上一个筛选的卡冒充这一个的。
    failed.value = true
    report.value = undefined
    toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) loading.value = false
  }
}

watch([kind, within], () => void load())
onMounted(load)

const brief = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  const { parts, say, bold } = briefWriter()
  const what = kind.value === 'all' ? '卡' : kindLabels[kind.value]
  const when = within.value === 'all' ? '' : `${within.value} 天内到期（含已过期）的`
  if (!t.cards) {
    say(`现在没有${when}还剩余额或次数的${what}。`)
    return parts
  }
  say(`现在${when}还剩余额或次数的${what}有 `)
  bold(`${t.cards} 张`, undefined, 'cards')
  say('，一共值 ')
  bold(yuan(t.remaining_value), undefined, 'remaining_value')
  const bits = []
  if (t.remaining_counts) bits.push(`剩 ${t.remaining_counts} 次`)
  if (t.remaining_counts && Number(t.principal)) bits.push(`其中本金 ${yuan(t.principal)}`)
  if (Number(t.bonus)) bits.push(`另有赠送金 ${yuan(t.bonus)} 不计`)
  if (bits.length) say(`（${bits.join('，')}）`)
  say('。')
  if (t.expiring_in_30_days) {
    say(`接下来 ${SOON} 天要到期的有 `)
    bold(`${t.expiring_in_30_days} 张`, 'down')
    say('，可以提醒顾客来用。')
  }
  if (report.value?.truncated) say(`下面只列到期最近的 ${report.value.rows.length} 张。`)
  return parts
})

// 今年的日子写「12月10日」，别的年份带上年。
const thisYear = String(new Date().getFullYear())

function dateText(day: string) {
  return day.startsWith(thisYear) ? shortDate(day) : `${day.slice(0, 4)}年${shortDate(day)}`
}

function itemsText(row: Row) {
  return row.items
    .filter(i => i.paid_remaining + i.gift_remaining > 0)
    .map(i => `${i.item_name} 剩 ${i.paid_remaining + i.gift_remaining} 次${i.gift_remaining ? `（送 ${i.gift_remaining}）` : ''}`)
    .join('、')
}

function rowMeta(row: Row) {
  return [row.card_type.name, `${dateText(row.opened_on)} 开卡`, itemsText(row)].filter(Boolean).join(' · ')
}

function expiryText(row: Row) {
  if (row.days_left === null || !row.expires_on) return '不过期'
  if (row.days_left < 0) return `已过期 ${-row.days_left} 天`
  if (row.days_left === 0) return '今天到期'
  return `${dateText(row.expires_on)} 到期 · 还有 ${row.days_left} 天`
}

function soon(row: Row) {
  return row.days_left !== null && row.days_left <= SOON
}

function moneyText(row: Row) {
  const bits = []
  if (row.remaining_counts) bits.push(`剩 ${row.remaining_counts} 次`)
  if (row.remaining_counts && Number(row.principal)) bits.push(`本金 ${yuan(row.principal)}`)
  if (Number(row.bonus)) bits.push(`送 ${yuan(row.bonus)}`)
  return bits.join(' · ')
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-start justify-between gap-2">
      <ViewTabs v-model="within" :items="withins" label="到期" testid="balance-within" />
      <ViewTabs v-model="kind" :items="kinds" label="卡种" testid="balance-kind" />
    </div>

    <EmptyState v-if="!canCustomers" art="chart" title="要能看顾客才能列出是谁的卡" hint="请店长在角色里加上「看顾客」" />
    <LoadError v-else-if="failed && !report" title="报表没有读出来" hint="检查网络后再试一次" @retry="load" />

    <template v-else-if="report">
      <ReportBrief :parts="brief" :class="loading && 'opacity-60'" />

      <section v-if="report.rows.length" data-testid="balance-list" :class="loading && 'opacity-60'">
        <h2 class="sec">还有余额或次数的卡 <span class="font-normal text-dimmed">到期近的在上</span></h2>
        <div class="divide-y divide-default">
          <NuxtLink
            v-for="row in report.rows"
            :key="row.card_id"
            :to="`/customers/${row.customer.id}`"
            data-testid="balance-row"
            :data-kind="row.card_type.kind"
            class="-mx-3 flex items-center gap-3 rounded-card px-3 py-3 hover:bg-hover"
          >
            <PersonAvatar :name="row.customer.name ?? '散'" />
            <div class="min-w-0 flex-1">
              <p class="font-medium">{{ row.customer.name ?? '未识别顾客' }}</p>
              <p class="truncate text-meta text-muted">{{ rowMeta(row) }}</p>
            </div>
            <div class="shrink-0 text-right">
              <p class="tabular-nums font-medium" data-col="remaining_value">{{ yuan(row.remaining_value) }}</p>
              <p class="text-meta tabular-nums" :class="soon(row) ? 'text-ink-red' : 'text-muted'" data-col="expiry">{{ [moneyText(row), expiryText(row)].filter(Boolean).join(' · ') }}</p>
            </div>
          </NuxtLink>
        </div>
      </section>
    </template>
  </div>
</template>
