<script setup lang="ts">
import type { components } from '~/utils/api'

// 售后（DESIGN.md 看板，kanban-01）：待审批 / 已批准 / 已拒绝三列，列按内容高，近 90 天开的售后单一张一张卡。
// 点卡从右侧滑出详情（样稿 22：先一句结论，再分节写怎么退、原单、经过），在里面批准或拒绝；
// 顶上「办售后」开新单，网址带 ?order=<原单 ID> 时直接带上那一单。
definePageMeta({ permission: 'aftersales.manage', title: '售后', art: 'bowl', description: '近 90 天开的售后单，批准了才退钱。' })
type Schema = components['schemas']
type Aftersale = Schema['Aftersale']
type Status = Aftersale['status']
type Order = Schema['Order']

const api = useApi()
const toast = useToast()
const route = useRoute()
const router = useRouter()
const { me, can } = useMe()
const businessDate = ref('')
const settings = ref<Schema['Setting'][]>([])
const rows = ref<Aftersale[]>([])
const loadError = ref('')
const loading = ref(true)
const guard = useRequestGuard()

// 看板只看近 90 天开的（接口一次最多查 92 天）；待审批一般不会久放，已结的每列取最近 50 张，取满了列头写「50+」。
// 办售后要挑原单（list_orders、get_order 要开单权限），没有开单权限的只审批，不显示「办售后」。
const DAYS = 90
const lanes: { status: Status, limit: number, tag: string }[] = [
  { status: 'pending', limit: 200, tag: 'bg-tag-yellow' },
  { status: 'approved', limit: 50, tag: 'bg-tag-gray' },
  { status: 'rejected', limit: 50, tag: 'bg-tag-gray' }
]
const laneRows = computed(() => Object.fromEntries(lanes.map(l => [l.status, rows.value.filter(r => r.status === l.status)])) as Record<Status, Aftersale[]>)

function settingValue(key: string) {
  const row = settings.value.find(s => s.key === key)
  return row ? String(row.value) : ''
}

const windowDays = computed(() => Number(settingValue('aftersale_window_days') || 30))
const approvalByOther = computed(() => settingValue('aftersale_approval_mode') === 'other')

// 顾客名：有看顾客的权限才逐个取，取不到写「顾客」。
const customerNames = ref<Record<string, string>>({})

function customerName(id: string) {
  return customerNames.value[id] || '顾客'
}

async function resolveNames(ids: string[]) {
  if (!can('customer.view')) return
  const missing = [...new Set(ids)].filter(id => !(id in customerNames.value))
  if (!missing.length) return
  const results = await Promise.all(missing.map(id => api.POST('/api/ops/get_customer', { body: { id } })))
  for (const result of results) {
    if (result.data) customerNames.value[result.data.id] = result.data.name || '未识别顾客'
  }
}

async function load() {
  const current = guard.next()
  loading.value = true
  loadError.value = ''
  try {
    const s = await api.POST('/api/ops/get_settings', { body: {} })
    if (!current()) return
    if (!s.data) {
      loadError.value = errorText(s.error)
      return
    }
    businessDate.value = s.data.business_date
    settings.value = s.data.settings
    const range = { date_from: addDays(s.data.business_date, -DAYS), date_to: s.data.business_date }
    const results = await Promise.all(lanes.map(l => api.POST('/api/ops/list_aftersales', { body: { ...range, status: l.status, limit: l.limit } })))
    if (!current()) return
    const failed = results.find(r => !r.data)
    if (failed) {
      loadError.value = errorText(failed.error)
      return
    }
    const all = results.flatMap(r => r.data?.items ?? [])
    await resolveNames(all.map(r => r.order.customer_id))
    if (!current()) return
    rows.value = all
  } finally {
    if (current()) loading.value = false
  }
}

function replaceRow(row: Aftersale) {
  rows.value = [row, ...rows.value.filter(r => r.id !== row.id)]
}

// 卡片最下一行：原单 11月25日 · 谁开的 · 什么时候开的。
function cardMeta(row: Aftersale) {
  return `原单 ${shortDate(row.order.business_date)} · ${row.created_by.name} · ${formatShortStamp(row.created_at)}`
}

// 详情第一句（先说结论）：谁开的、要怎么处理、现在到哪一步。退多少下面「怎么退」写着，这里不再说一遍。
function leadText(row: Aftersale) {
  const refund = isRefundType(row.type)
  if (row.status === 'pending') {
    const what = `要${aftersaleTypeLabels[row.type]}`
    return `${row.created_by.name} ${formatStamp(row.created_at)} 开的，${what}，等审批。`
  }
  const decided = [...row.events].reverse().find(e => e.kind !== 'created')
  const who = decided ? `${decided.actor.name} ` : ''
  if (row.status === 'rejected') return `${who}拒绝了，${refund ? '没退钱' : `不${aftersaleTypeLabels[row.type]}`}。`
  if (!refund) return `${who}批准了${aftersaleTypeLabels[row.type]}。`
  const deduct = row.deduct_performance ? (row.deduct_commission ? '扣了原单业绩和提成' : '扣了原单业绩，提成没扣') : '原单业绩和提成没扣'
  return `${who}批准了，钱已退回，${deduct}。`
}

// ---- 详情（右侧滑出）----
const detailOpen = ref(false)
const selected = ref<Aftersale>()
const order = ref<Order>()
const orderError = ref('')
const orderGuard = useRequestGuard()
const deductPerformance = ref(false)
const deductCommission = ref(false)
const deciding = ref(false)
const decideGuard = useRequestGuard()

async function openDetail(row: Aftersale) {
  // 换一张单时，上一张还没回来的批准、拒绝作废，免得回来把详情换回去。
  decideGuard.invalidate()
  deciding.value = false
  selected.value = row
  detailOpen.value = true
  deductPerformance.value = settingValue('aftersale_deduct_performance') === 'on'
  deductCommission.value = deductPerformance.value && settingValue('aftersale_deduct_commission') === 'on'
  order.value = undefined
  orderError.value = ''
  const current = orderGuard.next()
  const { data, error } = await api.POST('/api/ops/get_order', { body: { id: row.order_id } })
  if (!current() || selected.value?.id !== row.id) return
  if (data) order.value = data
  else orderError.value = errorText(error)
}

// 批准、拒绝没成（别人刚批过、日结已封账……）时重读这一张，详情和看板跟上它现在的样子。
async function refreshRow(id: string) {
  const { data } = await api.POST('/api/ops/get_aftersale', { body: { id } })
  if (!data) return
  replaceRow(data)
  if (selected.value?.id === id) selected.value = data
}

watch(deductPerformance, (value) => {
  if (!value) deductCommission.value = false
})

// 审批方式是「须另一名员工审批」时，开单的人自己不能批也不能拒。
const ownPending = computed(() => !!selected.value && approvalByOther.value && selected.value.created_by.id === me.value?.staff.id)

const eventLabels: Record<Schema['AftersaleEvent']['kind'], string> = { created: '开单', approved: '批准', rejected: '拒绝' }

function paymentText(o: Order) {
  return o.payments.map(p => `${orderPaymentLabels[p.method] ?? p.method} ${yuan(p.amount)}`).join(' · ')
}

function orderItems(o: Order) {
  return o.items.filter(i => !i.removed_at)
}

async function approve() {
  const row = selected.value
  if (!row || ownPending.value) return
  const current = decideGuard.next()
  deciding.value = true
  try {
    const refund = isRefundType(row.type)
    const { data, error } = await api.POST('/api/ops/approve_aftersale', {
      body: refund ? { id: row.id, deduct_performance: deductPerformance.value, deduct_commission: deductCommission.value } : { id: row.id }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      void refreshRow(row.id)
      return
    }
    replaceRow(data)
    if (selected.value?.id === row.id) selected.value = data
    toast.add({ title: refund ? `已批准，退 ${yuan(refundTotal(data))}` : '已批准', color: 'success' })
  } finally {
    if (current()) deciding.value = false
  }
}

// 拒绝（样稿 21）：先列拒了以后会怎样，再写一句为什么（选填）。
const rejectOpen = ref(false)
const rejectNote = ref('')

function askReject() {
  rejectNote.value = ''
  rejectOpen.value = true
}

async function reject() {
  const row = selected.value
  if (!row || ownPending.value) return
  const current = decideGuard.next()
  deciding.value = true
  try {
    const note = rejectNote.value.trim()
    const { data, error } = await api.POST('/api/ops/reject_aftersale', { body: note ? { id: row.id, note } : { id: row.id } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      void refreshRow(row.id)
      return
    }
    rejectOpen.value = false
    replaceRow(data)
    if (selected.value?.id === row.id) selected.value = data
    toast.add({ title: '已拒绝', color: 'success' })
  } finally {
    if (current()) deciding.value = false
  }
}

// ---- 办售后 ----
const createOpen = ref(false)
const createOrderID = ref('')

function openCreate(orderID = '') {
  createOrderID.value = orderID
  createOpen.value = true
}

async function created(row: Aftersale) {
  await resolveNames([row.order.customer_id])
  replaceRow(row)
  void openDetail(row)
}

// 带着 ?order= 进来（或页面开着时又从别处跳来）就开办售后弹窗，开了把网址清掉。
function openFromQuery() {
  const fromQuery = typeof route.query.order === 'string' ? route.query.order : ''
  if (!fromQuery || !businessDate.value || !can('order.create')) return
  openCreate(fromQuery)
  void router.replace({ query: {} })
}

watch(() => route.query.order, openFromQuery)

onMounted(async () => {
  await load()
  openFromQuery()
})
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton v-if="can('order.create')" :disabled="!businessDate" data-testid="aftersale-new" @click="openCreate()">办售后</UButton>
    </PageActions>
    <PageDescriptionTail v-if="businessDate">原单结账后 {{ windowDays }} 天内能办{{ approvalByOther ? '，开单和审批要是两个人' : '' }}。</PageDescriptionTail>

    <LoadError v-if="loadError" title="售后单没读出来" :hint="loadError" @retry="load" />
    <ListSkeleton v-else-if="loading && !rows.length" :rows="4" />
    <div v-else class="grid items-start gap-3 md:grid-cols-3 xl:grid-cols-[repeat(3,minmax(0,20rem))]" data-testid="aftersale-board">
      <section
        v-for="lane in lanes"
        :key="lane.status"
        class="min-w-0 rounded-card bg-lane p-2"
        :aria-label="aftersaleStatusLabels[lane.status]"
        :data-testid="`aftersale-lane-${lane.status}`"
      >
        <h2 class="flex items-center gap-2 px-1 py-1 text-sm">
          <span class="tag" :class="lane.tag">{{ aftersaleStatusLabels[lane.status] }}</span>
          <span class="text-dimmed tabular-nums" data-testid="lane-count">{{ laneRows[lane.status].length }}{{ laneRows[lane.status].length >= lane.limit ? '+' : '' }}</span>
        </h2>
        <ul v-if="laneRows[lane.status].length" class="mt-1 space-y-2">
          <li v-for="row in laneRows[lane.status]" :key="row.id">
            <button
              type="button"
              class="block w-full rounded-md bg-white px-3 py-2 text-start transition-shadow hover:bg-card-hover focus-visible:outline-2 focus-visible:outline-primary"
              :class="selected?.id === row.id && detailOpen ? 'shadow-lift' : 'shadow-paper'"
              :aria-current="selected?.id === row.id && detailOpen ? 'true' : undefined"
              data-testid="aftersale-card"
              :data-aftersale-id="row.id"
              @click="openDetail(row)"
            >
              <span class="flex items-baseline gap-2">
                <span class="min-w-0 truncate font-semibold" :title="customerName(row.order.customer_id)">{{ customerName(row.order.customer_id) }}</span>
                <span v-if="isRefundType(row.type)" class="ms-auto shrink-0 tabular-nums">退 {{ yuan(refundTotal(row)) }}</span>
              </span>
              <span class="mt-1 flex flex-wrap gap-1">
                <span class="tag bg-tag-gray">{{ aftersaleTypeLabels[row.type] }}</span>
              </span>
              <span class="said mt-2 line-clamp-2 block text-sm">{{ row.reason }}</span>
              <span class="mt-1 block text-meta text-muted tabular-nums">{{ cardMeta(row) }}</span>
            </button>
          </li>
        </ul>
      </section>
    </div>

    <!-- 焦点移出时不关：办完售后弹窗关上会把焦点还给顶栏「办售后」，非模态侧滑默认会跟着关掉。 -->
    <USlideover
      v-model:open="detailOpen"
      :modal="false"
      :content="{ onFocusOutside: (e: Event) => e.preventDefault() }"
      :title="selected ? `${customerName(selected.order.customer_id)} · ${aftersaleTypeLabels[selected.type]}` : '售后单'"
      :description="selected ? `原单 ${shortDate(selected.order.business_date)} · 应收 ${yuan(selected.order.receivable)}` : ''"
    >
      <template #title>
        <span v-if="selected" class="flex items-center gap-3">
          <PersonAvatar :name="customerName(selected.order.customer_id)" />
          <span class="min-w-0 truncate">{{ customerName(selected.order.customer_id) }} · {{ aftersaleTypeLabels[selected.type] }}</span>
        </span>
      </template>
      <template #body>
        <div v-if="selected" class="space-y-2" data-testid="aftersale-detail" :data-status="selected.status">
          <p class="flex flex-wrap items-center gap-2 text-sm" data-testid="detail-lead">
            <span class="tag" :class="selected.status === 'pending' ? 'bg-tag-yellow' : 'bg-tag-gray'">{{ aftersaleStatusLabels[selected.status] }}</span>
            <span>{{ leadText(selected) }}</span>
          </p>
          <p class="said text-base">{{ selected.reason }}</p>

          <section v-if="selected.refund_lines.length">
            <h2 class="sec">怎么退</h2>
            <div class="divide-y divide-default border-b border-default text-sm">
              <p v-for="(line, i) in selected.refund_lines" :key="i" class="flex items-baseline justify-between gap-4 py-2 tabular-nums" data-testid="detail-refund-line">
                <span>{{ refundMethodLabels[line.method] }}</span><span>{{ yuan(line.amount) }}</span>
              </p>
              <p v-if="selected.refund_lines.length > 1" class="flex items-baseline justify-between gap-4 py-2 font-semibold tabular-nums">
                <span>合计</span><span data-testid="detail-refund-total">{{ yuan(refundTotal(selected)) }}</span>
              </p>
            </div>
          </section>

          <section v-if="selected.status === 'pending' && isRefundType(selected.type) && !ownPending">
            <h2 class="sec">批准时</h2>
            <div class="space-y-2">
              <UCheckbox v-model="deductPerformance" label="扣原单业绩" data-testid="deduct-performance" />
              <UCheckbox v-model="deductCommission" :disabled="!deductPerformance" label="同时扣提成" description="扣提成要先扣业绩" data-testid="deduct-commission" />
            </div>
          </section>

          <section>
            <h2 class="sec">原单<span class="ms-auto font-normal text-dimmed tabular-nums">{{ shortDate(selected.order.business_date) }}</span></h2>
            <p v-if="orderError" class="text-sm text-muted" data-testid="detail-order-error">{{ orderError }}</p>
            <ListSkeleton v-else-if="!order" :rows="2" />
            <div v-else class="divide-y divide-default border-b border-default text-sm">
              <p v-for="item in orderItems(order)" :key="item.id" class="flex items-baseline justify-between gap-4 py-2 tabular-nums">
                <span class="min-w-0 truncate">{{ item.item_name }}</span><span>{{ yuan(item.price) }}</span>
              </p>
              <p class="flex items-baseline justify-between gap-4 py-2 tabular-nums">
                <span class="text-muted">{{ paymentText(order) }}</span><span class="font-semibold">应收 {{ yuan(selected.order.receivable) }}</span>
              </p>
            </div>
          </section>

          <section>
            <h2 class="sec">经过</h2>
            <ol class="relative">
              <span class="absolute inset-y-2 start-[3px] w-px bg-line" aria-hidden="true" />
              <li v-for="event in selected.events" :key="event.id" class="relative py-1 ps-5 text-sm" data-testid="aftersale-event" :data-kind="event.kind">
                <span class="absolute start-0 top-[10px] size-2 rounded-full" :class="event.kind === 'created' ? 'bg-white ring-1 ring-(--ui-border-accented)' : 'bg-inverted'" aria-hidden="true" />
                <span class="font-medium">{{ eventLabels[event.kind] }}</span>
                <span class="text-muted"> · {{ event.actor.name }} · </span><span class="text-muted tabular-nums">{{ formatStamp(event.at) }}</span>
                <span v-if="event.note" class="said block text-muted">{{ event.note }}</span>
              </li>
            </ol>
          </section>
        </div>
      </template>
      <template v-if="selected && selected.status === 'pending'" #footer>
        <p v-if="ownPending" class="me-auto text-meta text-muted" data-testid="own-pending">这张是你开的，要另一位同事来批</p>
        <template v-else>
          <UButton color="error" variant="ghost" class="me-auto" :disabled="deciding" data-testid="aftersale-reject" @click="askReject">拒绝</UButton>
          <UButton :loading="deciding" data-testid="aftersale-approve" @click="approve">
            {{ isRefundType(selected.type) ? `批准，退 ${yuan(refundTotal(selected))}` : '批准' }}
          </UButton>
        </template>
      </template>
    </USlideover>

    <UModal
      v-model:open="rejectOpen"
      :title="selected ? `拒绝${customerName(selected.order.customer_id)}这张售后单？` : '拒绝售后单？'"
      :description="selected ? `${aftersaleTypeLabels[selected.type]}${isRefundType(selected.type) ? ` · 退 ${yuan(refundTotal(selected))}` : ''} · 原单 ${shortDate(selected.order.business_date)}` : undefined"
    >
      <template #body>
        <div v-if="selected" class="space-y-5">
          <div>
            <p class="mb-1 text-meta text-muted">拒了以后</p>
            <div class="divide-y divide-default border-b border-default text-sm">
              <p class="flex items-baseline justify-between gap-4 py-2">
                <span>{{ isRefundType(selected.type) ? '退款' : aftersaleTypeLabels[selected.type] }}</span>
                <span class="tabular-nums">{{ isRefundType(selected.type) ? `不退，${yuan(refundTotal(selected))} 不动` : '不做' }}</span>
              </p>
              <p class="flex items-baseline justify-between gap-4 py-2"><span>原单业绩和提成</span><span>不扣</span></p>
            </div>
          </div>
          <UFormField label="为什么拒绝" help="选填，会写进这张单的经过">
            <UTextarea v-model="rejectNote" :maxlength="200" :rows="2" placeholder="比如：已经重做过了" class="w-full" data-testid="reject-note" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions cancel-label="不拒绝了" @cancel="rejectOpen = false">
          <template #leading>
            <span class="text-meta text-muted">拒了不能撤回，经过里会记下是你拒的</span>
          </template>
          <UButton color="error" :loading="deciding" data-testid="reject-confirm" @click="reject">拒绝</UButton>
        </ModalActions>
      </template>
    </UModal>

    <AftersaleCreateModal
      v-if="businessDate"
      v-model:open="createOpen"
      :business-date="businessDate"
      :window-days="windowDays"
      :initial-order-id="createOrderID"
      :customer-name="customerName"
      :resolve-names="resolveNames"
      @created="created"
    />
  </div>
</template>
