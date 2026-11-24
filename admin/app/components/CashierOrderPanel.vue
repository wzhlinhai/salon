<script setup lang="ts">
import type { components } from '~/utils/api'

// 收银「当前单」：项目和做的人、加项目、改价、去掉、合并同日挂单、解除团购券，右边试算和结账。
// 选哪张单由页面调 select()；这里改了挂单（金额、项目）就发 changed，页面重读挂单列表。
type Order = components['schemas']['Order']
type OrderItem = components['schemas']['OrderItem']
type Item = components['schemas']['Item']
type CustomerCard = components['schemas']['Card']

interface PaymentRow { method: string, amount: string }
interface CountPayment { order_item_id: string, card_id: string, amount: string, item_name: string, card_name: string }

const NONE = '__none__'
const props = defineProps<{
  openOrders: Order[]
  items: Item[]
  staffItems: { label: string, value: string }[]
  businessDate: string
  identifiedCustomers: Record<string, boolean>
  customerName: (id: string) => string
  staffName: (id: string | null | undefined) => string
  resolveNames: (orders: Order[]) => Promise<void>
  mayChange: (permission: string, order: Order | null) => boolean
}>()
// orderId：选中的挂单，页面拿来高亮列表；busy：结账进行中，页面这时不让换单。
const orderId = defineModel<string>('orderId', { default: '' })
const busy = defineModel<boolean>('busy', { default: false })
const emit = defineEmits<{
  'changed': []
  'coupons-changed': []
  'checked-out': [checkoutId: string | null | undefined]
}>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const canCollect = computed(() => can('payment.collect'))
const canGroupbuy = computed(() => can('groupbuy.redeem'))

const order = ref<Order | null>(null)
const orderLoading = ref(false)
const orderGuard = useRequestGuard()
const actionSaving = ref(false)
const actionGuard = useRequestGuard()
const canOrderEdit = computed(() => props.mayChange('order.edit', order.value))
const canReprice = computed(() => props.mayChange('order.reprice', order.value))
const itemOptions = computed(() => props.items.map(i => ({ label: `${i.name}（${i.price}）`, value: i.id })))

function positionsOf(itemID: string) {
  const item = props.items.find(i => i.id === itemID)
  return [...(item?.positions ?? [])].sort((a, b) => a.slot - b.slot)
}

function clear() {
  orderGuard.invalidate()
  orderId.value = ''
  order.value = null
  resetCheckout()
}

async function select(target: Order) {
  if (busy.value) return
  const current = orderGuard.next()
  orderId.value = target.id
  order.value = null
  resetCheckout()
  orderLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/get_order', { body: { id: target.id } })
    if (!current() || orderId.value !== target.id) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    order.value = data
    await props.resolveNames([data])
    if (!current() || orderId.value !== target.id) return
    await loadCards()
    if (!current()) return
    await previewCheckout()
  } finally {
    if (current()) orderLoading.value = false
  }
}

async function refresh() {
  const id = orderId.value
  if (!id) return
  const current = orderGuard.next()
  orderLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/get_order', { body: { id } })
    if (!current() || orderId.value !== id) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    order.value = data
    await props.resolveNames([data])
    if (!current() || orderId.value !== id) return
    await previewCheckout()
  } finally {
    if (current()) orderLoading.value = false
  }
}

// 撤单退回了卡余额、次数和红包：同一顾客的当前单重新读卡并试算。
async function voided(target: Order) {
  if (order.value?.customer_id !== target.customer_id) return
  invalidatePreview()
  await loadCards()
  await refresh()
}

// —— 编辑项目 ——
async function afterEdit() {
  invalidatePreview()
  await refresh()
  emit('changed')
  focusScan()
}

const addItemForm = useModalForm()
const { open: addItemOpen, saving: addItemSaving } = addItemForm
const addItemID = ref('')
const addItemStaff = ref<Record<number, string>>({})

function addItemBegin() {
  addItemForm.begin(() => {
    addItemID.value = ''
    addItemStaff.value = {}
  })
}

async function saveAddItem() {
  if (!order.value || !addItemID.value) return
  const target = order.value
  await addItemForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/add_order_item', {
      body: {
        order_id: target.id,
        item: {
          item_id: addItemID.value,
          staff: positionsOf(addItemID.value).filter(p => addItemStaff.value[p.slot]).map(p => ({ slot: p.slot, staff_id: addItemStaff.value[p.slot]! }))
        }
      }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    addItemOpen.value = false
    await afterEdit()
  })
}

async function removeItem(orderItemID: string) {
  const target = order.value
  if (!target || !canOrderEdit.value) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/remove_order_item', { body: { order_id: target.id, order_item_id: orderItemID } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    await afterEdit()
  } finally {
    if (current()) actionSaving.value = false
  }
}

// 同一顾客、同一营业日、同为补录或同为普通单，且每张都能改的其他挂单可以并进当前单（merge_orders 的条件）。
const mergeableOrders = computed(() => {
  const target = order.value
  if (!target) return []
  return props.openOrders.filter(o => o.id !== target.id && o.customer_id === target.customer_id && o.business_date === target.business_date && o.backfill === target.backfill && props.mayChange('order.edit', o))
})

async function mergeOrders() {
  const target = order.value
  const merged = mergeableOrders.value.map(o => o.id)
  if (!target || !canOrderEdit.value || !merged.length) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/merge_orders', { body: { order_id: target.id, merged_order_ids: merged } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: `已合并 ${merged.length} 张挂单`, color: 'success' })
    if (orderId.value !== target.id) {
      emit('changed')
      return
    }
    await afterEdit()
  } finally {
    if (current()) actionSaving.value = false
  }
}

async function changeStaff(item: OrderItem, slot: number, staffID: string) {
  const target = order.value
  if (!target || !canOrderEdit.value) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const staff = item.positions
      .map(p => ({ slot: p.slot, staff_id: p.slot === slot ? staffID : (p.staff_id ?? '') }))
      .filter(p => p.staff_id)
    const { data, error } = await api.POST('/api/ops/set_order_item_staff', {
      body: { order_id: target.id, order_item_id: item.id, staff }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    invalidatePreview()
    await refresh()
    focusScan()
  } finally {
    if (current()) actionSaving.value = false
  }
}

const repriceForm = useModalForm()
const { open: repriceOpen, saving: repriceSaving } = repriceForm
const repriceItem = ref<OrderItem | null>(null)
const repricePrice = ref('0.00')
const repriceReason = ref('')

function repriceBegin(item: OrderItem) {
  repriceForm.begin(() => {
    repriceItem.value = item
    repricePrice.value = item.price
    repriceReason.value = ''
  })
}

async function saveReprice() {
  const target = order.value
  const item = repriceItem.value
  if (!target || !item || !canReprice.value) return
  await repriceForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/reprice_order_item', {
      body: { order_id: target.id, order_item_id: item.id, price: repricePrice.value, reason: repriceReason.value.trim() }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    repriceOpen.value = false
    await afterEdit()
  })
}

async function unlinkCoupon(redemptionID: string) {
  if (!canGroupbuy.value) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/unlink_coupon_order', { body: { redemption_id: redemptionID } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已解除关联', color: 'success' })
    await refresh()
    emit('changed')
    emit('coupons-changed')
    focusScan()
  } finally {
    if (current()) actionSaving.value = false
  }
}

// —— 结账 ——
const preview = ref<components['schemas']['PreviewCheckout'] | null>(null)
const previewLoading = ref(false)
const previewGuard = useRequestGuard()
const payments = ref<PaymentRow[]>([])
const paymentsTouched = ref(false)
const paymentResetKey = ref(0)
const countPayments = ref<CountPayment[]>([])
const selectedCardID = ref(NONE)
const cards = ref<CustomerCard[]>([])
const cardsGuard = useRequestGuard()
const cardItems = computed(() => cards.value.filter(c => cents(c.principal) + cents(c.bonus) > 0).map(c => ({
  label: `${c.card_type_name}（余额 ${c.principal} + ${c.bonus}）`,
  value: c.id
})))
const paymentMethods = computed(() => [
  { label: '现金', value: 'cash' },
  { label: '银联', value: 'unionpay' },
  { label: '微信', value: 'wechat' },
  { label: '支付宝', value: 'alipay' },
  ...(can('customer.view') && cardItems.value.length ? [{ label: '卡余额', value: 'card' }] : []),
  ...(order.value && props.identifiedCustomers[order.value.customer_id] ? [{ label: '挂账', value: 'debt' }] : [])
])
const groupbuyTotal = computed(() => (preview.value?.payments ?? []).filter(p => p.method === 'groupbuy').reduce((sum, p) => sum + cents(p.amount), 0))
const countTotal = computed(() => countPayments.value.reduce((sum, p) => sum + cents(p.amount), 0))
const expectedCash = computed(() => {
  if (!preview.value) return '0.00'
  return money(Math.max(0, cents(preview.value.net) - groupbuyTotal.value - countTotal.value))
})
const paidTotal = computed(() => payments.value.reduce((sum, p) => sum + cents(p.amount), 0))

function resetCheckout() {
  previewGuard.invalidate()
  cardsGuard.invalidate()
  preview.value = null
  payments.value = []
  paymentsTouched.value = false
  paymentResetKey.value++
  countPayments.value = []
  selectedCardID.value = NONE
  cards.value = []
}

function invalidatePreview() {
  previewGuard.invalidate()
  preview.value = null
  payments.value = []
  paymentsTouched.value = false
  paymentResetKey.value++
  countPayments.value = []
}

async function loadCards() {
  const id = order.value?.customer_id
  if (!id || !can('customer.view')) return
  const current = cardsGuard.next()
  const { data } = await api.POST('/api/ops/list_customer_cards', { body: { customer_id: id } })
  if (!current() || order.value?.customer_id !== id) return
  cards.value = (data?.items ?? []).filter(c => c.status === 'active')
}

function countCardsFor(itemID: string) {
  const day = order.value?.business_date || props.businessDate
  return cards.value.filter(card => card.status === 'active'
    && (!card.expires_on || card.expires_on >= day)
    && card.items.some(item => item.item_id === itemID && item.paid_remaining + item.gift_remaining > 0))
}

function countPaymentFor(orderItemID: string) {
  return countPayments.value.find(payment => payment.order_item_id === orderItemID)
}

function previewLineNet(orderItemID: string) {
  const line = preview.value?.items.find(item => item.id === orderItemID && !item.removed_at)
  if (!line) return ''
  return money(Math.max(0, cents(line.price) - cents(line.discount)))
}

const countOpen = ref(false)
const countTarget = ref<OrderItem | null>(null)
const countCardID = ref(NONE)
const countCardOptions = computed(() => {
  const item = countTarget.value
  if (!item) return []
  return countCardsFor(item.item_id).map((card) => {
    const remaining = card.items.find(count => count.item_id === item.item_id)
    return { label: `${card.card_type_name}（剩 ${(remaining?.paid_remaining ?? 0) + (remaining?.gift_remaining ?? 0)} 次）`, value: card.id }
  })
})

function countBegin(item: OrderItem) {
  const matching = countCardsFor(item.item_id)
  if (!matching.length) return
  countTarget.value = item
  countCardID.value = matching[0]!.id
  countOpen.value = true
}

function addCountPayment() {
  const item = countTarget.value
  const card = cards.value.find(c => c.id === countCardID.value)
  if (!item || !card) return
  const amount = previewLineNet(item.id)
  if (!amount || cents(amount) <= 0) {
    toast.add({ title: '试算还没完成', color: 'error' })
    return
  }
  countPayments.value = [
    ...countPayments.value.filter(payment => payment.order_item_id !== item.id),
    { order_item_id: item.id, card_id: card.id, amount, item_name: item.item_name, card_name: card.card_type_name }
  ]
  countOpen.value = false
  countTarget.value = null
}

function removeCountPayment(orderItemID: string) {
  countPayments.value = countPayments.value.filter(payment => payment.order_item_id !== orderItemID)
}

async function previewCheckout() {
  const target = order.value
  if (!target || !canCollect.value) return
  const current = previewGuard.next()
  previewLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/preview_checkout', {
      body: { order_id: target.id, card_id: selectedCardID.value === NONE ? undefined : selectedCardID.value }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    preview.value = data
    paymentsTouched.value = false
    payments.value = []
    paymentResetKey.value++
    countPayments.value = countPayments.value.flatMap((payment) => {
      const line = data.items.find(item => item.id === payment.order_item_id && !item.removed_at)
      if (!line) return []
      const amount = money(Math.max(0, cents(line.price) - cents(line.discount)))
      if (cents(amount) === 0) return []
      return [{ ...payment, amount }]
    })
  } finally {
    if (current()) previewLoading.value = false
  }
}

async function checkout() {
  if (busy.value || !canCollect.value) return
  const target = order.value
  if (!target || !preview.value) return
  if (paidTotal.value !== cents(expectedCash.value)) {
    toast.add({ title: '收款合计必须等于现场补差', color: 'error' })
    return
  }
  if (payments.value.some(p => p.method === 'card') && selectedCardID.value === NONE) {
    toast.add({ title: '卡余额支付请先选择计价卡', color: 'error' })
    return
  }
  const cashPayments: components['schemas']['PaymentInput'][] = payments.value.map(p => p.method === 'card'
    ? { method: 'card' as const, card_id: selectedCardID.value, amount: p.amount }
    : { method: p.method as components['schemas']['PaymentInput']['method'], amount: p.amount })
  const countInputs: components['schemas']['PaymentInput'][] = countPayments.value.map(payment => ({
    method: 'count', card_id: payment.card_id, order_item_id: payment.order_item_id, amount: payment.amount
  }))
  const current = orderGuard.hold()
  busy.value = true
  try {
    const { data, error } = await api.POST('/api/ops/checkout_orders', {
      body: { orders: [{ order_id: target.id, payments: [...countInputs, ...cashPayments] }] }
    })
    if (!current() || orderId.value !== target.id) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    clear()
    toast.add({ title: '结账成功', color: 'success' })
    emit('checked-out', data.items[0]?.checkout_id)
  } finally {
    busy.value = false
  }
}

defineExpose({ select, clear, refresh, voided })
</script>

<template>
  <section data-testid="selected-order">
    <h2 class="sec">
      当前单
      <template v-if="order">
        <span class="font-normal">· {{ customerName(order.customer_id) }} · {{ order.gender === 'male' ? '男客' : '女客' }}</span>
        <span v-if="order.designated" class="tag bg-tag-gray">指定</span>
        <span v-if="order.backfill" data-testid="selected-backfill" class="tag bg-tag-yellow">补录 {{ order.business_date }}</span>
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-plus" class="ms-auto" @click="addItemBegin">加项目</UButton>
      </template>
    </h2>

    <p v-if="orderLoading" class="box text-sm text-muted">加载中…</p>
    <EmptyState v-else-if="!order" class="box" art="receipt" title="还没选单" hint="从左侧选一张挂单，或扫码开单" />
    <div v-else class="grid items-start gap-4" :class="canCollect ? 'xl:grid-cols-[minmax(0,1fr)_20rem]' : ''">
      <div class="space-y-4">
        <div v-if="canOrderEdit && mergeableOrders.length" class="box flex items-center gap-2 py-2 text-sm" role="status" data-testid="merge-hint">
          <span>这位顾客还有 {{ mergeableOrders.length }} 张同日挂单（{{ yuanFixed(money(mergeableOrders.reduce((sum, o) => sum + cents(o.receivable), 0))) }}），可以并成一张结账</span>
          <UButton size="sm" color="neutral" variant="outline" class="ms-auto" :loading="actionSaving" :disabled="busy" @click="mergeOrders">合并到这张</UButton>
        </div>
        <div class="box overflow-hidden p-0">
          <div
            v-for="item in order.items.filter(i => !i.removed_at)"
            :key="item.id"
            class="group/row space-y-2 border-t border-default px-3 py-2.5 text-sm first:border-t-0"
            :data-order-item="item.item_name"
          >
            <div class="flex items-center gap-2">
              <span class="font-medium">{{ item.item_name }}</span>
              <span v-if="item.price_source === 'manual'" class="text-meta text-ink-orange">改价</span>
              <span class="ms-auto font-medium tabular-nums">{{ yuanFixed(item.price) }}</span>
            </div>
            <div v-for="position in [...item.positions].sort((a, b) => a.slot - b.slot)" :key="position.slot" class="flex items-center gap-2">
              <span class="w-12 text-meta text-muted">岗位 {{ position.slot }}</span>
              <USelect
                v-if="canOrderEdit"
                :model-value="position.staff_id ?? undefined"
                :items="[{ label: '未填', value: NONE }, ...staffItems]"
                size="sm"
                class="w-48"
                :aria-label="`${item.item_name} 岗位${position.slot}员工`"
                @update:model-value="value => changeStaff(item, position.slot, String(value === NONE ? '' : value))"
              />
              <span v-else>{{ staffName(position.staff_id) }}</span>
              <span v-if="position.required" class="tag bg-tag-yellow">必填</span>
            </div>
            <div class="flex flex-wrap items-center gap-1">
              <template v-if="canCollect">
                <template v-if="countPaymentFor(item.id)">
                  <span class="tag bg-tag-green">已用次卡 {{ countPaymentFor(item.id)?.card_name }}</span>
                  <UButton size="sm" color="neutral" variant="ghost" @click="removeCountPayment(item.id)">取消用次卡</UButton>
                </template>
                <UButton v-else-if="countCardsFor(item.item_id).length" size="sm" color="neutral" variant="outline" :disabled="!preview || busy" @click="countBegin(item)">用次卡</UButton>
              </template>
              <div class="ms-auto flex gap-1 opacity-0 group-hover/row:opacity-100 focus-within:opacity-100 any-pointer-coarse:opacity-100">
                <UButton v-if="canReprice" size="sm" color="neutral" variant="ghost" @click="repriceBegin(item)">改价</UButton>
                <UButton v-if="canOrderEdit" size="sm" color="error" variant="ghost" :loading="actionSaving" @click="removeItem(item.id)">去掉</UButton>
              </div>
            </div>
          </div>
        </div>

        <div v-if="canGroupbuy && order.groupbuy.length" data-testid="order-groupbuy">
          <h2 class="sec">关联团购券</h2>
          <div class="box overflow-hidden p-0">
            <div v-for="coupon in order.groupbuy" :key="coupon.redemption_id" class="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-default px-3 py-2.5 text-sm first:border-t-0">
              <span class="font-medium">{{ coupon.deal_name }}</span>
              <span class="tag bg-tag-gray">{{ platformLabel(coupon.platform) }}</span>
              <span class="text-muted tabular-nums">购买价 {{ coupon.purchase_price }} · 门店标价 {{ coupon.store_price }}</span>
              <UButton size="sm" color="error" variant="ghost" class="ms-auto" :loading="actionSaving" @click="unlinkCoupon(coupon.redemption_id)">解除关联</UButton>
            </div>
          </div>
        </div>
        <p v-if="!canCollect" class="px-1 text-meta text-muted tabular-nums">应收 {{ yuanFixed(order.receivable) }} · 实收 {{ yuanFixed(order.net) }}</p>
      </div>

      <div v-if="canCollect" class="box space-y-3">
        <div class="flex items-center justify-between">
          <h2 class="font-medium">结账</h2>
          <span v-if="previewLoading" class="text-meta text-muted">试算中…</span>
        </div>
        <USelect v-model="selectedCardID" :items="[{ label: '不使用卡', value: NONE }, ...cardItems]" class="w-full" aria-label="计价卡" @update:model-value="previewCheckout" />
        <dl class="space-y-1.5 border-t border-default pt-3 text-sm tabular-nums">
          <div class="flex justify-between"><dt class="text-muted">应收</dt><dd>{{ yuanFixed(order.receivable) }}</dd></div>
          <div class="flex justify-between"><dt class="text-muted">实收</dt><dd>{{ yuanFixed(preview?.net ?? order.net) }}</dd></div>
          <template v-if="preview">
            <div v-if="groupbuyTotal" class="flex justify-between"><dt class="text-muted">团购抵扣</dt><dd>-{{ yuanFixed(groupbuyTotal / 100) }}</dd></div>
            <div v-if="countTotal" class="flex justify-between"><dt class="text-muted">次卡抵扣</dt><dd>-{{ yuanFixed(countTotal / 100) }}</dd></div>
            <div class="flex items-baseline justify-between border-t border-default pt-2 font-semibold">
              <dt>现场补差</dt>
              <dd class="text-xl">¥<span data-testid="cash-expected">{{ expectedCash }}</span></dd>
            </div>
          </template>
        </dl>
        <template v-if="preview">
          <p v-if="groupbuyTotal" class="text-meta text-muted">团购支付由系统按关联的团购券生成，不需要在这里填写。</p>
          <div v-if="countPayments.length" class="space-y-1" data-testid="count-payments">
            <div v-for="payment in countPayments" :key="payment.order_item_id" class="flex items-center justify-between gap-2 text-meta">
              <span>次卡 {{ payment.card_name }} · {{ payment.item_name }} · {{ payment.amount }}</span>
              <UButton size="xs" color="neutral" variant="ghost" @click="removeCountPayment(payment.order_item_id)">取消</UButton>
            </div>
          </div>
          <PaymentEditor v-model="payments" v-model:touched="paymentsTouched" :expected="expectedCash" :methods="paymentMethods" :reset-key="paymentResetKey" />
          <UButton size="lg" block :loading="busy" :disabled="previewLoading || actionSaving || orderLoading || busy" @click="checkout">结账</UButton>
        </template>
      </div>
    </div>
  </section>

  <UModal v-model:open="addItemOpen" title="加项目">
    <template #body>
      <div class="space-y-5">
        <UFormField label="项目">
          <USelect v-model="addItemID" :items="itemOptions" class="w-full" aria-label="加项目" placeholder="选择项目" @update:model-value="addItemStaff = {}" />
        </UFormField>
        <div v-if="positionsOf(addItemID).length" class="grid grid-cols-2 gap-4">
          <UFormField v-for="position in positionsOf(addItemID)" :key="position.slot" :label="`岗位 ${position.slot}`">
            <USelect v-model="addItemStaff[position.slot]" :items="staffItems" class="w-full" :aria-label="`加项目岗位${position.slot}员工`" placeholder="选择员工" />
          </UFormField>
        </div>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="addItemOpen = false">
        <UButton :loading="addItemSaving" @click="saveAddItem">添加</UButton>
      </ModalActions>
    </template>
  </UModal>

  <UModal v-model:open="repriceOpen" title="改价" :description="repriceItem?.item_name">
    <template #body>
      <div class="space-y-5">
        <UFormField label="新价格"><UInput v-model="repricePrice" inputmode="decimal" aria-label="新价格" /></UFormField>
        <UFormField label="原因"><UInput v-model="repriceReason" aria-label="改价原因" /></UFormField>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="repriceOpen = false">
        <UButton :loading="repriceSaving" @click="saveReprice">保存</UButton>
      </ModalActions>
    </template>
  </UModal>

  <UModal v-model:open="countOpen" title="用次卡" :description="`${countTarget?.item_name ?? ''} · 次卡抵扣 ${countTarget ? previewLineNet(countTarget.id) : ''}`">
    <template #body>
      <div class="space-y-5">
        <UFormField label="次卡">
          <USelect v-model="countCardID" :items="countCardOptions" class="w-full" aria-label="次卡" />
        </UFormField>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="countOpen = false">
        <UButton @click="addCountPayment">确定</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
