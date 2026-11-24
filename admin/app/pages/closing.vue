<script setup lang="ts">
import type { TableColumn, TableRow } from '@nuxt/ui'
import type { components } from '~/utils/api'

definePageMeta({ permission: 'closing.daily', title: '日结', art: 'receipt' })

type Item = components['schemas']['DayCloseListItem']
// 列表一行是一个营业日：日结过的带上最新一版快照，没日结的只有日期。
// business 是这天有没有营业（后端和封账用同一个判断）；没营业的日子不用日结，也不标「待日结」。
interface Day { date: string, close: Item | null, sealed: boolean, business: boolean }

const DAYS = 31
const api = useApi()
const toast = useToast()
const { can } = useMe()
const guard = useRequestGuard()
const loading = ref(false)
const failed = ref(false)
const today = ref('')
const sealedThrough = ref<string | null>(null)
const closes = ref<Item[]>([])
const businessDays = ref<string[]>([])

async function load() {
  const current = guard.next()
  loading.value = true
  failed.value = false
  try {
    const settings = await api.POST('/api/ops/get_settings', { body: {} })
    if (!current()) return
    if (!settings.data) {
      failed.value = true
      toast.add({ title: errorText(settings.error), color: 'error' })
      return
    }
    today.value = settings.data.business_date
    const { data, error } = await api.POST('/api/ops/list_day_closes', {
      body: { date_from: addDays(today.value, -DAYS), date_to: addDays(today.value, -1) }
    })
    if (!current()) return
    if (!data) {
      failed.value = true
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    sealedThrough.value = data.sealed_through
    closes.value = data.items
    businessDays.value = data.business_days
  } finally {
    if (current()) loading.value = false
  }
}

onMounted(load)

const days = computed<Day[]>(() => {
  if (!today.value) return []
  const byDate = new Map(closes.value.map(c => [c.business_date, c]))
  const business = new Set(businessDays.value)
  return Array.from({ length: DAYS }, (_, i) => {
    const date = addDays(today.value, -1 - i)
    return { date, close: byDate.get(date) ?? null, sealed: !!sealedThrough.value && date <= sealedThrough.value, business: business.has(date) }
  })
})

// 视图页签，照 Notion 只列有记录的行：默认「营业日」只列营业过或日结过的日子，
// 没营业的日子在「所有日子」里（封账要从那里点更早的日子）。数字是这一类的天数。
interface Filter { key: string, label: string, empty: string, test: (d: Day) => boolean }
const pending = (d: Day) => d.business && !d.close && !d.sealed
const filters: Filter[] = [
  { key: 'business', label: '营业日', empty: `最近 ${DAYS} 天没有营业`, test: d => d.business || !!d.close },
  { key: 'open', label: '待日结', empty: '营业的日子都日结了', test: pending },
  { key: 'closed', label: '已日结', empty: '还没有日结过', test: d => !!d.close && !d.sealed },
  { key: 'sealed', label: '已封账', empty: '还没有封账', test: d => d.sealed },
  { key: 'days', label: '所有日子', empty: '', test: () => true }
]
const filterKey = ref('business')
const filterTabs = computed(() => filters.map(f => ({ key: f.key, label: f.label, count: f.key === 'days' || (loading.value && !days.value.length) ? undefined : days.value.filter(f.test).length })))
const filter = computed(() => filters.find(f => f.key === filterKey.value)!)
const shown = computed(() => days.value.filter(filter.value.test))

const columns: TableColumn<Day>[] = [
  { id: 'date', header: '日期' },
  { id: 'cash', header: '现金清点', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } },
  { id: 'unionpay', header: '银联', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } },
  { id: 'wechat', header: '微信', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } },
  { id: 'alipay', header: '支付宝', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } },
  { id: 'differences', header: '差异' },
  { id: 'note', header: '备注' }
]
const AMOUNTS = { cash: 'cash_counted', unionpay: 'unionpay_actual', wechat: 'wechat_actual', alipay: 'alipay_actual' } as const

function signed(value: string) {
  const n = Number(value)
  return `${n > 0 ? '多' : '少'} ${yuanFixed(Math.abs(n))}`
}

function offLines(close: Item) {
  return close.differences.filter(d => Number(d.difference) !== 0)
}

// 封账只能接着上次封到的那天往后一天；从没封过时，昨天及以前哪天都行。
function canSeal(date: string) {
  if (!can('closing.seal') || !today.value || date >= today.value) return false
  if (!sealedThrough.value) return true
  return date === addDays(sealedThrough.value, 1)
}

function canUnseal(date: string) {
  return can('closing.unseal') && date === sealedThrough.value
}

const yesterday = computed(() => today.value ? addDays(today.value, -1) : '')
const yesterdayOpen = computed(() => days.value.some(d => d.date === yesterday.value && pending(d)))
// 下一个可封账的日子。按日期顺序封，可能早于列表的 31 天，所以入口放在列表上方，不靠某一行。
const nextSeal = computed(() => sealedThrough.value ? addDays(sealedThrough.value, 1) : '')

// 详情抽屉。
const selected = ref<Day | null>(null)
const detailOpen = ref(false)

function selectRow(_event: Event, row: TableRow<Day>) {
  selected.value = row.original
  detailOpen.value = true
}

// 列表刷新后，抽屉里的那天跟着换成新数据；那天已经滑出 31 天（例如营业日翻了）就关掉抽屉。
watch(days, (list) => {
  if (!selected.value) return
  selected.value = list.find(d => d.date === selected.value!.date) ?? null
  if (!selected.value) detailOpen.value = false
})

const closeModal = useTemplateRef('closeModal')

function openClose(day: Day) {
  const c = day.close
  closeModal.value?.start(day.date, c ? { cash_counted: c.cash_counted, unionpay_actual: c.unionpay_actual, wechat_actual: c.wechat_actual, alipay_actual: c.alipay_actual, note: c.note } : undefined)
}

function openCloseDate(date: string) {
  const day = days.value.find(d => d.date === date)
  if (day) openClose(day)
}

// 日结后列表里这天换成新的快照 id，抽屉内容按 key 重新挂载并自己加载。
function onClosed() {
  void load()
}

// 封账确认。
const sealForm = useModalForm()
const sealDate = ref('')

function openSeal(date: string) {
  sealForm.begin(() => {
    sealDate.value = date
  })
}

function saveSeal() {
  sealForm.save(async (current) => {
    const { error } = await api.POST('/api/ops/seal_day', { body: { date: sealDate.value } })
    if (!current()) return
    if (error) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    sealForm.open.value = false
    toast.add({ title: `封好了，${sealDate.value} 及以前的账不能再改`, color: 'success' })
    await load()
  })
}

// 撤销封账：只撤最后封的那天，要写原因。
const unsealForm = useModalForm()
const unsealReason = ref('')

function openUnseal() {
  unsealForm.begin(() => {
    unsealReason.value = ''
  })
}

function saveUnseal() {
  const reason = unsealReason.value.trim()
  if (!reason) {
    toast.add({ title: '写一下为什么要撤销封账', color: 'error' })
    return
  }
  unsealForm.save(async (current) => {
    const date = sealedThrough.value
    const { error } = await api.POST('/api/ops/unseal_day', { body: { reason } })
    if (!current()) return
    if (error) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    unsealForm.open.value = false
    toast.add({ title: `已撤销 ${date} 的封账`, color: 'success' })
    await load()
  })
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton v-if="yesterdayOpen" size="sm" icon="i-lucide-calculator" @click="openCloseDate(yesterday)">日结昨天</UButton>
    </PageActions>

    <ViewTabs v-model="filterKey" :items="filterTabs" label="筛选营业日" />

    <div class="flex flex-wrap items-center gap-2 text-meta text-muted">
      <UIcon name="i-lucide-lock" class="size-3.5" aria-hidden="true" />
      <span data-testid="sealed-through">{{ sealedThrough ? `已封账到 ${sealedThrough}` : '还没有封账' }}</span>
      <UButton v-if="nextSeal && canSeal(nextSeal)" size="xs" color="neutral" variant="outline" @click="openSeal(nextSeal)">封账到 {{ nextSeal }}…</UButton>
      <UButton v-if="sealedThrough && canUnseal(sealedThrough)" size="xs" color="neutral" variant="ghost" @click="openUnseal">撤销封账…</UButton>
      <span class="ms-auto text-dimmed">最近 {{ DAYS }} 天</span>
    </div>

    <div class="overflow-x-auto">
      <UTable :data="shown" :columns="columns" :loading="loading" :aria-busy="loading" @select="selectRow">
        <template #date-cell="{ row }">
          <div class="flex items-center gap-2" :data-date="row.original.date">
            <span class="font-medium tabular-nums">{{ row.original.date }}</span>
            <span class="text-dimmed">{{ weekdayOf(row.original.date) }}</span>
            <span v-if="row.original.sealed" class="inline-flex items-center gap-1 text-meta text-dimmed"><UIcon name="i-lucide-lock" class="size-3" aria-hidden="true" />已封账</span>
            <span v-else-if="pending(row.original)" class="tag bg-tag-yellow">待日结</span>
            <span v-if="row.original.close && row.original.close.version > 1" class="text-meta text-dimmed">第 {{ row.original.close.version }} 版</span>
          </div>
        </template>
        <template v-for="(field, key) in AMOUNTS" :key="key" #[`${key}-cell`]="{ row }">
          <span v-if="row.original.close" :class="zeroText(row.original.close[field]) ? 'text-dimmed' : ''">{{ yuanFixed(row.original.close[field]) }}</span>
        </template>
        <template #differences-cell="{ row }">
          <template v-if="row.original.close">
            <span v-if="offLines(row.original.close).length" class="text-ink-red">
              <template v-for="(d, i) in offLines(row.original.close)" :key="d.method">{{ i ? '，' : '' }}{{ d.label }} {{ signed(d.difference) }}</template>
            </span>
            <span v-else class="text-dimmed">对上了</span>
          </template>
        </template>
        <template #note-cell="{ row }">
          <span class="line-clamp-1 max-w-56 text-muted">{{ row.original.close?.note }}</span>
        </template>
        <template #loading>
          <ListSkeleton />
        </template>
        <template #empty>
          <EmptyState v-if="failed" title="日结列表没加载出来" hint="检查网络后再试一次">
            <UButton color="neutral" variant="outline" size="sm" @click="load">重试</UButton>
          </EmptyState>
          <EmptyState v-else-if="filterKey !== 'days'" :title="filter.empty">
            <UButton color="neutral" variant="outline" size="sm" @click="filterKey = 'days'">看所有日子</UButton>
          </EmptyState>
        </template>
      </UTable>
    </div>

    <USlideover v-model:open="detailOpen" :modal="false" :title="selected ? `${selected.date} ${weekdayOf(selected.date)}` : ''" :description="selected?.sealed ? '已封账，账目不能再改' : selected?.close ? `已日结 · 第 ${selected.close.version} 版` : '还没日结'">
      <template #body>
        <DayCloseDetail v-if="selected" :key="`${selected.date}:${selected.close?.id ?? ''}`" :date="selected.date" :closed="!!selected.close" />
      </template>
      <template v-if="selected" #footer>
        <div class="flex w-full flex-wrap justify-end gap-2">
          <UButton v-if="canUnseal(selected.date)" color="neutral" variant="ghost" icon="i-lucide-lock-open" @click="openUnseal">撤销封账…</UButton>
          <UButton v-if="canSeal(selected.date)" color="neutral" variant="outline" icon="i-lucide-lock" @click="openSeal(selected.date)">封账到这天…</UButton>
          <UButton v-if="!selected.sealed" @click="openClose(selected)">{{ selected.close ? '重新日结' : '日结' }}</UButton>
        </div>
      </template>
    </USlideover>

    <DayCloseModal ref="closeModal" :can-seal="canSeal" @closed="onClosed" @seal="openSeal" />

    <UModal v-model:open="sealForm.open.value" :title="`封账到 ${sealDate}`">
      <template #body>
        <div class="space-y-2 text-sm">
          <p>封账后 {{ sealDate }} 及以前的单不能再改、撤单、补录，要退钱只能走售后。</p>
          <p v-if="!sealedThrough" class="text-muted">这是第一次封账，{{ sealDate }} 以前的日子也一起封上。</p>
          <p class="text-muted">封错了老板可以撤销，撤销要写原因。</p>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="sealForm.open.value = false">
          <UButton :loading="sealForm.saving.value" icon="i-lucide-lock" @click="saveSeal">封账</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="unsealForm.open.value" :title="`撤销 ${sealedThrough ?? ''} 的封账`" description="撤销后这天的账又能改了，改完记得重新日结、再封账。">
      <template #body>
        <UFormField label="原因">
          <UTextarea v-model="unsealReason" :maxlength="200" :rows="2" class="w-full" placeholder="例如：漏录了一单现金" />
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="unsealForm.open.value = false">
          <UButton :loading="unsealForm.saving.value" color="error" @click="saveUnseal">撤销封账</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
