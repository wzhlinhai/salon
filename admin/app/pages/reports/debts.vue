<script setup lang="ts">
// 欠款对账（样稿 13）：先一句话说到今天一共欠过多少、还了多少、还差多少，一根已还/还差的条；
// 下面「还没还的」按欠的天数从久到近，一行一笔：谁欠的、哪天怎么欠的、还差多少、欠了几天。
// 按来源（单上挂账、历史欠款）的合计表收在最下面。撤单作废的挂账不计。逐笔列表要 customer.view，没有就只看合计。
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '欠款对账' })

type Summary = components['schemas']['Summary']
type Debt = components['schemas']['OutstandingDebt']

const api = useApi()
const toast = useToast()
const { can } = useMe()
const summary = ref<Summary | null>(null)
const debts = ref<Debt[] | null>(null)
const today = ref('')
const failed = ref(false)
const guard = useRequestGuard()

// 欠了这么多天以上，还差的数字写红。
const OLD_DAYS = 30

async function load() {
  const current = guard.next()
  failed.value = false
  const listed = can('customer.view')
  const [s, settings, list] = await Promise.all([
    api.POST('/api/ops/debt_summary', { body: {} }),
    api.POST('/api/ops/get_settings', { body: {} }),
    listed ? api.POST('/api/ops/list_outstanding_debts', { body: {} }) : null
  ])
  if (!current()) return
  if (!s.data) {
    failed.value = true
    toast.add({ title: errorText(s.error), color: 'error' })
    return
  }
  summary.value = s.data
  today.value = settings.data?.business_date ?? ''
  debts.value = list?.data?.items ?? null
  if (list && !list.data) toast.add({ title: errorText(list.error), color: 'error' })
}

onMounted(load)

function daysOwed(d: Debt) {
  if (!today.value) return 0
  return Math.round((Date.parse(today.value) - Date.parse(d.business_date)) / 86_400_000)
}

const customers = computed(() => new Set((debts.value ?? []).map(d => d.customer.id)).size)
const oldest = computed(() => debts.value?.[0])

const brief = computed(() => {
  const s = summary.value
  if (!s) return []
  const { parts, say, bold } = briefWriter()
  if (!Number(s.amount)) {
    say('还没有过欠款。')
    return parts
  }
  say(`到今天一共欠过 ${yuan(s.amount)}，已经还了 ${yuan(s.repaid)}，`)
  if (!Number(s.outstanding)) {
    say('都还清了。')
    return parts
  }
  say('还差 ')
  bold(yuan(s.outstanding), 'down', 'outstanding')
  say(debts.value ? `，是 ${customers.value} 位顾客的。` : '。')
  const first = oldest.value
  if (first && daysOwed(first) > 0) {
    say('最久那笔欠了 ')
    bold(`${daysOwed(first)} 天`)
    say('。')
  }
  return parts
})

// 已还占一共欠过的比例，条的两段。
const repaidShare = computed(() => {
  const s = summary.value
  if (!s || !Number(s.amount)) return 0
  return Number(s.repaid) / Number(s.amount)
})

function rowMeta(d: Debt) {
  if (d.kind === 'historical') return d.note ? `历史欠款（${d.note}）` : '历史欠款'
  return [`${monthDay(d.business_date)} 结账挂账`, d.item_names.join('、'), `${d.recorded_by.name}经手`].filter(Boolean).join(' · ')
}

function ageText(d: Debt) {
  if (d.kind === 'historical') return '导入时已有'
  const days = daysOwed(d)
  return days ? `${days} 天` : '今天'
}

const rows = computed(() => summary.value
  ? [
      { key: 'order', label: '单上挂账', totals: summary.value.order },
      { key: 'historical', label: '历史欠款', totals: summary.value.historical }
    ]
  : [])

function cell(value: string) {
  return Number(value) === 0 ? 'text-dimmed' : ''
}
</script>

<template>
  <div class="space-y-4">
    <LoadError v-if="failed && !summary" title="欠款没有读出来" hint="检查网络后再试一次" @retry="load" />

    <template v-if="summary">
      <ReportBrief :parts="brief" />

      <div v-if="Number(summary.amount)" data-testid="debt-bar">
        <div class="flex h-2 gap-1 overflow-hidden rounded-full" role="img" :aria-label="`已还 ${yuan(summary.repaid)}，还差 ${yuan(summary.outstanding)}`">
          <div v-if="Number(summary.repaid)" class="bg-dye-4" :style="{ flexGrow: repaidShare, flexBasis: 0 }" />
          <div v-if="Number(summary.outstanding)" class="bg-dye-5" :style="{ flexGrow: 1 - repaidShare, flexBasis: 0 }" />
        </div>
        <div class="mt-2 flex gap-4 text-meta text-muted">
          <span class="flex items-center gap-1"><span class="size-2 rounded-full bg-dye-4" />已还 {{ yuan(summary.repaid) }}</span>
          <span class="flex items-center gap-1"><span class="size-2 rounded-full bg-dye-5" />还差 {{ yuan(summary.outstanding) }}</span>
        </div>
      </div>

      <section v-if="debts" data-testid="debt-list">
        <h2 class="sec">还没还的 <span class="font-normal text-dimmed">按欠的天数</span></h2>
        <p v-if="!debts.length" class="text-sm text-muted">没有还没还的欠款</p>
        <div v-else class="divide-y divide-default">
          <NuxtLink
            v-for="d in debts"
            :key="d.id"
            :to="`/customers/${d.customer.id}`"
            data-testid="debt-row"
            :data-kind="d.kind"
            class="-mx-3 flex items-center gap-3 rounded-card px-3 py-3 hover:bg-hover"
          >
            <PersonAvatar :name="d.customer.name ?? '散'" />
            <div class="min-w-0 flex-1">
              <p class="font-medium">{{ d.customer.name ?? '散客' }}</p>
              <p class="truncate text-meta text-muted">{{ rowMeta(d) }}</p>
            </div>
            <div class="shrink-0 text-right">
              <p class="tabular-nums font-medium" :class="{ 'text-ink-red': d.kind === 'order' && daysOwed(d) > OLD_DAYS }" data-col="outstanding">{{ yuan(d.outstanding) }}</p>
              <p class="text-meta text-muted">{{ [ageText(d), Number(d.repaid) ? `已还 ${yuan(d.repaid)}` : ''].filter(Boolean).join(' · ') }}</p>
            </div>
          </NuxtLink>
        </div>
      </section>

      <details class="border-t border-default pt-3" data-testid="debt-details">
        <summary class="cursor-pointer text-sm text-muted">按来源合计：结账挂账 {{ yuan(summary.order.amount) }} / 已还 {{ yuan(summary.order.repaid) }}；历史欠款 {{ yuan(summary.historical.amount) }} / 已还 {{ yuan(summary.historical.repaid) }}</summary>
        <div class="mt-3 overflow-x-auto">
          <table class="w-full text-sm tabular-nums" data-testid="debt-table">
            <thead class="border-b border-default text-sm text-muted">
              <tr>
                <th scope="col" class="px-3 py-2 text-left font-normal">来源</th>
                <th scope="col" class="px-3 text-right font-normal">欠款</th>
                <th scope="col" class="px-3 text-right font-normal">已还</th>
                <th scope="col" class="px-3 text-right font-normal">未还</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.key" class="border-t border-default" :data-kind="row.key">
                <th scope="row" class="px-3 py-2 text-left font-normal">{{ row.label }}</th>
                <td class="px-3 text-right" :class="cell(row.totals.amount)" data-col="amount">{{ yuanFixed(row.totals.amount) }}</td>
                <td class="px-3 text-right" :class="cell(row.totals.repaid)" data-col="repaid">{{ yuanFixed(row.totals.repaid) }}</td>
                <td class="px-3 text-right font-medium" :class="cell(row.totals.outstanding)" data-col="outstanding">{{ yuanFixed(row.totals.outstanding) }}</td>
              </tr>
            </tbody>
            <tfoot class="font-semibold">
              <tr class="border-y border-default" data-testid="debt-totals">
                <th scope="row" class="px-3 py-2 text-left">合计</th>
                <td class="px-3 text-right" data-col="amount">{{ yuanFixed(summary.amount) }}</td>
                <td class="px-3 text-right" data-col="repaid">{{ yuanFixed(summary.repaid) }}</td>
                <td class="px-3 text-right" data-col="outstanding">{{ yuanFixed(summary.outstanding) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </details>
    </template>
  </div>
</template>
