<script setup lang="ts">
import type { components } from '~/utils/api'

// 办售后（DESIGN.md 新建、短表单；样稿 18）：挑原单 → 选修补、重做还是退款 → 写原因；退款再一行一种方式填退多少。
// 挑好的原单收成一条灰底条（头像、顾客、项目、怎么付的），右边「换一单」再回到挑单。
// 提交后是待审批，批准了才真的退钱；可退金额、卡本金能退多少取 get_order_refundable，和后端校验同一个数。
type Schema = components['schemas']
type Aftersale = Schema['Aftersale']
type Order = Schema['Order']
type Method = Schema['RefundLine']['method']
type Line = { method: Method, card_id: string, amount: string }

const props = defineProps<{
  businessDate: string
  windowDays: number
  initialOrderId: string
  customerName: (id: string) => string
  resolveNames: (ids: string[]) => Promise<void>
}>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ created: [row: Aftersale] }>()
const api = useApi()
const toast = useToast()
const form = useModalForm()
const { open: formOpen, saving } = form

const NONE = '__none__'
const date = ref('')
const orders = ref<Order[]>([])
const ordersLoading = ref(false)
const ordersGuard = useRequestGuard()
const orderID = ref(NONE)
// 从别处带进来的那一单（?order=）：当天超过 100 单时不一定在列表里，单独留着好显示灰条。
const pinned = ref<Order>()
const refundable = ref<Schema['OrderRefundable']>()
const refundableGuard = useRequestGuard()
const type = ref<Aftersale['type']>('repair')
const reason = ref('')
const lines = ref<Line[]>([])

const minDate = computed(() => addDays(props.businessDate, -props.windowDays))
const orderItems = computed(() => [
  { label: ordersLoading.value ? '读取中…' : (orders.value.length ? '选一单' : '这天没有结了账的单'), value: NONE },
  ...orders.value.map(o => ({ label: `${props.customerName(o.customer_id)} · ${orderItemNames(o)} · 应收 ${o.receivable}`, value: o.id }))
])
const typeItems = (Object.keys(aftersaleTypeLabels) as Aftersale['type'][]).map(value => ({ value, label: aftersaleTypeLabels[value] }))
const refund = computed(() => isRefundType(type.value))
const cards = computed(() => (refundable.value?.cards ?? []).filter(c => cents(c.remaining) > 0))
const methodItems = computed(() => (Object.keys(refundMethodLabels) as Method[])
  .filter(m => m !== 'card_principal' || cards.value.length)
  .map(value => ({ value, label: refundMethodLabels[value] })))
const cardItems = computed(() => cards.value.map(c => ({ value: c.card_id, label: `${c.card_type_name} · 可退本金 ${yuan(c.remaining)}` })))
const picked = computed(() => orders.value.find(o => o.id === orderID.value) ?? (pinned.value?.id === orderID.value ? pinned.value : undefined))
const total = computed(() => lines.value.reduce((sum, l) => sum + cents(l.amount), 0))
const limit = computed(() => cents(refundable.value?.refundable ?? '0'))

// 按钮栏左边一句：退款时写可退多少，超了或全额退款没对上时说差在哪、标红；合计写在「取消」前面（样稿 18）。
const status = computed(() => {
  if (!refund.value || !refundable.value) return { text: '', tone: '' }
  const can = `可退 ${yuan(refundable.value.refundable)}`
  if (total.value > limit.value) return { text: `${can}，多填了 ${yuan(money(total.value - limit.value))}`, tone: 'text-ink-red' }
  if (type.value === 'full_refund' && total.value !== limit.value) return { text: `${can}，全额退款还差 ${yuan(money(limit.value - total.value))}`, tone: 'text-ink-red' }
  return { text: can, tone: 'text-muted' }
})

const blocked = computed(() => {
  if (orderID.value === NONE || !refundable.value) return '先选原单'
  if (!refundable.value.within_window) return `已过售后期限（到 ${shortDate(refundable.value.window_ends_on)}）`
  if (!reason.value.trim()) return '写一句原因'
  if (refund.value) {
    if (!limit.value) return '这单没有可退的钱'
    if (!lines.value.length || lines.value.some(l => cents(l.amount) <= 0 || (l.method === 'card_principal' && !l.card_id))) return '把每一笔退款填完整'
    if (total.value > limit.value) return '退的比可退的多'
    if (type.value === 'full_refund' && total.value !== limit.value) return '全额退款要正好退完可退的钱'
  }
  return ''
})

async function loadOrders(day: string) {
  const current = ordersGuard.next()
  ordersLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_orders', { body: { business_date: day, status: 'paid', limit: 100 } })
    if (!current()) return
    if (!data) {
      orders.value = []
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    await props.resolveNames(data.items.map(o => o.customer_id))
    if (!current()) return
    orders.value = data.items
  } finally {
    if (current()) ordersLoading.value = false
  }
}

async function loadRefundable(id: string) {
  const current = refundableGuard.next()
  refundable.value = undefined
  if (id === NONE) return
  const { data, error } = await api.POST('/api/ops/get_order_refundable', { body: { order_id: id } })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  refundable.value = data
  fillLines()
}

// 换成退款时先替店长填一份：每张付过本金的卡退回剩下的本金，其余退现金；全额退款加起来正好是可退金额。
function fillLines() {
  if (!refund.value || !refundable.value) {
    lines.value = []
    return
  }
  const cardLines = cards.value.map(c => ({ method: 'card_principal' as const, card_id: c.card_id, amount: c.remaining }))
  const rest = limit.value - cardLines.reduce((sum, l) => sum + cents(l.amount), 0)
  const next: Line[] = type.value === 'full_refund' ? cardLines : []
  if (type.value === 'partial_refund' || rest > 0) next.push({ method: 'cash', card_id: '', amount: type.value === 'full_refund' ? money(rest) : '' })
  lines.value = next
}

function addLine() {
  lines.value.push({ method: 'cash', card_id: '', amount: '' })
}

// 换营业日：同一天再选一次时值没变、监听不触发，这里直接读一遍。
function setDate(day: string) {
  if (date.value === day) void loadOrders(day)
  else date.value = day
}

function changeOrder() {
  orderID.value = NONE
}

function pickMethod(line: Line, method: Method) {
  line.method = method
  line.card_id = method === 'card_principal' ? (cards.value[0]?.card_id ?? '') : ''
}

watch(date, (day) => {
  if (!formOpen.value || !day) return
  if (orderID.value !== NONE && orders.value.every(o => o.id !== orderID.value || o.business_date !== day)) orderID.value = NONE
  void loadOrders(day)
})
watch(orderID, id => void loadRefundable(id))
watch(type, fillLines)

// 弹窗开关由父页面控制；关掉时让这一次打开里的请求全部作废（useModalForm 的 open 跟着关）。
watch(open, async (value) => {
  if (!value) {
    formOpen.value = false
    return
  }
  form.begin(() => {
    orders.value = []
    pinned.value = undefined
    orderID.value = NONE
    refundable.value = undefined
    type.value = 'repair'
    reason.value = ''
    lines.value = []
  })
  if (!props.initialOrderId) {
    setDate(props.businessDate)
    return
  }
  // 从别处带着原单进来（?order=）：先读那一单，定下营业日再选中它。
  await form.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/get_order', { body: { id: props.initialOrderId } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      setDate(props.businessDate)
      return
    }
    pinned.value = data
    setDate(data.business_date)
    await nextTick()
    orderID.value = data.id
  })
}, { immediate: true })

async function save() {
  if (blocked.value) return
  await form.save(async (current) => {
    const body: Schema['CreateAftersaleInput'] = { order_id: orderID.value, type: type.value, reason: reason.value.trim() }
    if (refund.value) body.refund_lines = lines.value.map(l => ({ method: l.method, amount: money(cents(l.amount)), ...(l.method === 'card_principal' ? { card_id: l.card_id } : {}) }))
    const { data, error } = await api.POST('/api/ops/create_aftersale', { body })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    open.value = false
    toast.add({ title: '售后单已提交，等审批', color: 'success' })
    emit('created', data)
  })
}
</script>

<template>
  <UModal
    v-model:open="open"
    title="办售后"
    description="提交后等审批，批准了才退钱。"
    :ui="{ content: 'sm:max-w-[600px]' }"
  >
    <template #body>
      <div class="space-y-5" data-testid="aftersale-form">
        <div v-if="picked" class="flex items-center gap-3 rounded-card bg-elevated px-3 py-2" data-testid="aftersale-order-strip">
          <PersonAvatar :name="customerName(picked.customer_id)" />
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm">
              <span class="font-semibold">{{ customerName(picked.customer_id) }}</span>
              <span class="text-muted tabular-nums"> · {{ shortDate(picked.business_date) }} · 应收 {{ yuan(picked.receivable) }}</span>
            </p>
            <p class="truncate text-meta text-muted tabular-nums">{{ orderItemNames(picked) }} · {{ picked.payments.map(p => `${orderPaymentLabels[p.method] ?? p.method} ${yuan(p.amount)}`).join(' · ') }}</p>
          </div>
          <UButton color="neutral" variant="ghost" size="sm" class="text-muted hover:text-default" data-testid="aftersale-change-order" @click="changeOrder">换一单</UButton>
        </div>
        <div v-else class="grid grid-cols-[10rem_minmax(0,1fr)] gap-4">
          <UFormField label="原单营业日"><DateInput v-model="date" :min="minDate" :max="businessDate" class="w-full" aria-label="原单营业日" /></UFormField>
          <UFormField label="原单">
            <USelect v-model="orderID" :items="orderItems" class="w-full" aria-label="原单" data-testid="aftersale-order" />
          </UFormField>
        </div>
        <p v-if="refundable && !refundable.within_window" class="text-sm text-ink-red" data-testid="aftersale-expired">
          这单的售后期限到 {{ shortDate(refundable.window_ends_on) }}，已经过了。
        </p>
        <p v-else-if="refundable && refundable.pending_count" class="text-meta text-muted">这单已有 {{ refundable.pending_count }} 张售后单在等审批。</p>
        <UFormField label="怎么处理">
          <MethodPicker v-model="type" :items="typeItems" label="售后类型" />
        </UFormField>
        <UFormField label="原因">
          <UTextarea v-model="reason" :maxlength="200" :rows="2" placeholder="比如：烫后卷度不满意" class="w-full" aria-label="原因" data-testid="aftersale-reason" />
        </UFormField>
        <FieldGroup v-if="refund" label="怎么退">
          <div v-for="(line, i) in lines" :key="i" class="flex items-center gap-2" data-testid="refund-line">
            <USelect :model-value="line.method" :items="methodItems" class="w-32" aria-label="退款方式" @update:model-value="pickMethod(line, $event as Method)" />
            <USelect v-if="line.method === 'card_principal'" v-model="line.card_id" :items="cardItems" class="min-w-0 flex-1" aria-label="退回哪张卡" />
            <UInput v-model="line.amount" inputmode="decimal" placeholder="金额" class="w-32" :class="line.method === 'card_principal' ? '' : 'ms-auto'" aria-label="退款金额" />
            <UButton color="error" variant="ghost" icon="i-lucide-x" :aria-label="`去掉第 ${i + 1} 笔`" @click="lines.splice(i, 1)" />
          </div>
          <UButton color="neutral" variant="ghost" class="-ms-2 self-start text-muted hover:text-default" @click="addLine">添加一笔</UButton>
        </FieldGroup>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="open = false">
        <template #leading>
          <span class="text-meta tabular-nums" :class="status.tone" data-testid="refund-status">{{ status.text }}</span>
        </template>
        <template v-if="refund && refundable" #before>
          <span class="me-2 font-semibold tabular-nums" data-testid="refund-total">退 {{ yuan(money(total)) }}</span>
        </template>
        <UButton :loading="saving" :disabled="!!blocked" :title="blocked || undefined" data-testid="aftersale-submit" @click="save">提交审批</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
