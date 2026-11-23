<script setup lang="ts">
// 报表入口：照 Notion 的画廊视图，一张报表一张卡片，卡片上是这张报表本月的关键数字。
// 只列当前员工有权限看的报表。数字取不到（没权限、网络错）就只留名称和说明，不挡进入。
definePageMeta({ permission: REPORT_PERMISSIONS, title: '报表' })

const api = useApi()
const { can } = useMe()
const visible = computed(() => reportCatalog.filter(r => can(r.permission)))

// 每张报表的关键数字：标签 + 值。
const figures = ref<Record<string, { label: string, value: string }>>({})

async function loadFigures() {
  const settings = await api.POST('/api/ops/get_settings', { body: {} })
  const today = settings.data?.business_date
  if (!today) return
  const range = { from: `${today.slice(0, 7)}-01`, to: today }
  const keys = new Set(visible.value.map(r => r.key))
  const set = (key: string, label: string, value: string | undefined) => {
    if (value !== undefined) figures.value = { ...figures.value, [key]: { label, value } }
  }
  const jobs: Promise<void>[] = []
  if (keys.has('business')) jobs.push(api.POST('/api/ops/get_business_report', { body: { granularity: 'day', ...range } }).then(r => set('business', '本月实收', r.data && yuan(r.data.totals.service_revenue))))
  if (keys.has('debts')) jobs.push(api.POST('/api/ops/debt_summary', { body: {} }).then(r => set('debts', '未还', r.data && yuan(r.data.outstanding))))
  if (keys.has('staff')) jobs.push(api.POST('/api/ops/get_staff_report', { body: range }).then(r => set('staff', '本月项目业绩', r.data && yuan(r.data.totals.item_performance))))
  if (keys.has('items')) jobs.push(api.POST('/api/ops/get_item_report', { body: range }).then(r => set('items', '本月项目实收', r.data && yuan(r.data.totals.paid))))
  if (keys.has('cards')) jobs.push(api.POST('/api/ops/get_card_report', { body: range }).then(r => set('cards', '本月开卡', r.data && `${r.data.totals.open_count} 张`)))
  if (keys.has('sources')) jobs.push(api.POST('/api/ops/get_customer_source_report', { body: range }).then(r => set('sources', '本月新客', r.data && `${r.data.totals.new_customers} 人`)))
  await Promise.allSettled(jobs)
}

onMounted(() => {
  void loadFigures()
})
</script>

<template>
  <div>
    <div v-if="visible.length" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3" data-testid="report-list">
      <NuxtLink
        v-for="r in visible"
        :key="r.key"
        :to="`/reports/${r.key}`"
        class="box flex min-h-40 flex-col gap-3 transition-colors hover:bg-card-hover"
      >
        <span class="flex items-center gap-2">
          <UIcon :name="r.icon" class="size-4 shrink-0 text-muted" aria-hidden="true" />
          <span class="font-semibold">{{ r.label }}</span>
        </span>
        <span class="min-h-12">
          <template v-if="figures[r.key]">
            <span class="block text-meta text-muted">{{ figures[r.key]!.label }}</span>
            <span class="block text-2xl leading-tight font-semibold tabular-nums" :data-figure="r.key">{{ figures[r.key]!.value }}</span>
          </template>
        </span>
        <span class="mt-auto text-meta text-muted text-pretty">{{ r.description }}</span>
      </NuxtLink>
    </div>
    <EmptyState v-else title="没有可看的报表" hint="看报表要相应的权限，找老板开通" />
  </div>
</template>
