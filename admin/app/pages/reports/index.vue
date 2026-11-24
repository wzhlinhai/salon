<script setup lang="ts">
import type { MonthFigures } from '~/utils/report'

// 报表首页（DESIGN.md 简报）：先用一段话说这个月怎么样，再一条每天实收的走势、钱从哪来、谁做得多，
// 最后是各张报表的子页面链接。每块只在当前员工有权限、数也取到时出现；取不到不挡进入下面的报表。
definePageMeta({ permission: REPORT_PERMISSIONS, title: '报表', width: 'narrow', art: 'chart' })

const api = useApi()
const { can } = useMe()
const visible = computed(() => reportCatalog.filter(r => can(r.permission)))

const figures = ref<Partial<MonthFigures>>({})
const linkFigures = ref<Record<string, string>>({})
const days = ref<{ day: string, revenue: number }[]>([])
const mix = ref<{ label: string, value: number }[]>([])
const rank = ref<{ key: string, name: string, value: number, display: string }[]>([])
const brief = computed(() => monthBrief(figures.value))
// 几个请求都回来后再一起出简报、走势和两栏，句子和版面不会边到边跳。
const ready = ref(false)

// 实收构成：实收前四的项目各一段，其余合成「其他」。
const MIX_TOP = 4

function shortDay(day: string) {
  return `${Number(day.slice(5, 7))}/${Number(day.slice(8, 10))}`
}

async function load() {
  const settings = await api.POST('/api/ops/get_settings', { body: {} })
  const today = settings.data?.business_date
  if (!today) return
  const range = { from: `${today.slice(0, 7)}-01`, to: today }
  const keys = new Set(visible.value.map(r => r.key))
  const merge = (patch: Partial<MonthFigures>) => {
    figures.value = { ...figures.value, ...patch }
  }
  const link = (key: string, value: string) => {
    linkFigures.value = { ...linkFigures.value, [key]: value }
  }
  const jobs: Promise<unknown>[] = []

  if (keys.has('business')) {
    jobs.push(api.POST('/api/ops/get_business_report', { body: { granularity: 'day', ...range } }).then((r) => {
      if (!r.data) return
      merge({ revenue: r.data.totals.service_revenue, guests: r.data.totals.guest_total })
      link('business', yuan(r.data.totals.service_revenue))
      const byDay = new Map(r.data.rows.map(row => [row.period, Number(row.service_revenue)]))
      const list: { day: string, revenue: number }[] = []
      for (let d = range.from; d <= today; d = addDays(d, 1)) list.push({ day: d, revenue: byDay.get(d) ?? 0 })
      days.value = list
    }))
    jobs.push(api.POST('/api/ops/get_business_report', { body: { granularity: 'day', ...samePeriodLastMonth(today) } }).then((r) => {
      if (r.data) merge({ lastRevenue: r.data.totals.service_revenue })
    }))
  }
  if (keys.has('sources')) {
    jobs.push(api.POST('/api/ops/get_customer_source_report', { body: range }).then((r) => {
      if (!r.data) return
      const top = r.data.rows.filter(row => row.source_name && row.new_customers > 0).sort((a, b) => b.new_customers - a.new_customers)[0]
      merge({ newCustomers: r.data.totals.new_customers, topSource: top?.source_name ?? null })
      link('sources', `${r.data.totals.new_customers} 人`)
    }))
  }
  if (keys.has('cards')) {
    jobs.push(api.POST('/api/ops/get_card_report', { body: range }).then((r) => {
      if (!r.data) return
      merge({ cardsOpened: r.data.totals.open_count })
      link('cards', `开卡 ${r.data.totals.open_count} 张`)
    }))
  }
  if (keys.has('debts')) {
    jobs.push(api.POST('/api/ops/debt_summary', { body: {} }).then((r) => {
      if (!r.data) return
      merge({ debtCount: r.data.outstanding_count, debtAmount: r.data.outstanding })
      link('debts', `未还 ${yuan(r.data.outstanding)}`)
    }))
  }
  if (keys.has('items')) {
    jobs.push(api.POST('/api/ops/get_item_report', { body: range }).then((r) => {
      if (!r.data) return
      const sorted = [...r.data.rows].sort((a, b) => Number(b.paid) - Number(a.paid))
      const paid = sorted.filter(row => Number(row.paid) > 0)
      const parts = paid.slice(0, MIX_TOP).map(row => ({ label: row.item_name, value: Number(row.paid) }))
      const rest = paid.slice(MIX_TOP).reduce((s, row) => s + Number(row.paid), 0)
      if (rest > 0) parts.push({ label: '其他', value: rest })
      mix.value = parts
      if (paid[0]) link('items', `${paid[0].item_name}最多`)
    }))
  }
  if (keys.has('staff')) {
    jobs.push(api.POST('/api/ops/get_staff_report', { body: range }).then((r) => {
      if (!r.data) return
      rank.value = r.data.rows
        .filter(row => row.staff && Number(row.item_performance) > 0)
        .map(row => ({ key: row.staff!.id, name: row.staff!.name, value: Number(row.item_performance), display: yuan(row.item_performance) }))
        .sort((a, b) => b.value - a.value)
        .slice(0, 5)
      link('staff', `项目业绩 ${yuan(r.data.totals.item_performance)}`)
    }))
  }
  await Promise.allSettled(jobs)
  ready.value = true
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div>
    <p v-if="ready && brief.length" class="text-base leading-[1.9] text-pretty" data-testid="report-brief">
      <template v-for="(part, i) in brief" :key="i">
        <b v-if="part.strong" class="font-semibold tabular-nums" :class="{ 'text-ink-green': part.tone === 'up', 'text-ink-red': part.tone === 'down' }">{{ part.text }}</b>
        <template v-else>{{ part.text }}</template>
      </template>
    </p>

    <ReportSpark
      v-if="ready && days.length > 1"
      class="mt-6"
      :values="days.map(d => d.revenue)"
      :start="shortDay(days[0]!.day)"
      end="今天"
      caption="每天实收"
      :peak-label="i => `${shortDay(days[i]!.day)} ${yuan(days[i]!.revenue)}`"
    />

    <div v-if="ready && (mix.length || rank.length > 1)" class="grid gap-x-12 sm:grid-cols-2">
      <section v-if="mix.length">
        <h2 class="sec">钱从哪来<span class="font-normal text-dimmed">实收前几的项目</span></h2>
        <ReportMix :parts="mix" />
      </section>
      <section v-if="rank.length > 1">
        <h2 class="sec">谁做得多<span class="font-normal text-dimmed">项目业绩</span></h2>
        <ReportRank :rows="rank" />
      </section>
    </div>

    <section v-if="visible.length">
      <h2 class="sec">细看</h2>
      <nav class="flex flex-col" aria-label="报表" data-testid="report-list">
        <PageLink
          v-for="r in visible"
          :key="r.key"
          :to="`/reports/${r.key}`"
          :label="r.label"
          :icon="r.icon"
          :description="r.description"
          :figure="linkFigures[r.key]"
        />
      </nav>
    </section>
    <EmptyState v-else title="没有可看的报表" hint="看报表要相应的权限，找老板开通" />
  </div>
</template>
