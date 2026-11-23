<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'order.create', title: '收银', workbench: true })

type Order = components['schemas']['Order']
type Customer = components['schemas']['Customer']
type Item = components['schemas']['Item']
type StaffOption = components['schemas']['StaffOption']
type Deal = components['schemas']['Deal']
type Redemption = components['schemas']['Redemption']
type CustomerCard = components['schemas']['Card']
type Setting = components['schemas']['Setting']

interface OrderItemRow { item_id: string, staff: Record<number, string> }
interface PaymentRow { method: string, amount: string }
interface CountPayment { order_item_id: string, card_id: string, amount: string, item_name: string, card_name: string }

const api = useApi()
const route = useRoute()
const router = useRouter()
const toast = useToast()
const { me, can, grant } = useMe()
const canCollect = computed(() => can('payment.collect'))
const canGroupbuy = computed(() => can('groupbuy.redeem'))
const canCustomer = computed(() => can('customer.view'))

// 去掉项目、换员工、改价按权限的数据范围和时间窗口判断，和后端 checkEdit 一致：
// 本人范围只能改自己开的或自己在岗位上的单；时间窗口按营业日算。
function mayChange(permission: string, order: Order | null) {
  const g = grant(permission)
  if (!g || !order) return false
  if (g.windowDays !== null && businessDate.value && order.business_date < addDays(businessDate.value, -g.windowDays)) return false
  if (g.scope === 'self') {
    const id = me.value?.staff.id
    return order.opened_by === id || order.items.some(item => item.positions.some(p => p.staff_id === id))
  }
  return true
}

const canOrderEdit = computed(() => mayChange('order.edit', selectedOrder.value))
const canReprice = computed(() => mayChange('order.reprice', selectedOrder.value))
const NONE = '__none__'
const platformItems = [{ label: '美团', value: 'meituan' }, { label: '抖音', value: 'douyin' }]

function cents(value: string) {
  const n = Number(value || '0')
  return Number.isFinite(n) ? Math.round(n * 100) : 0
}

function money(value: number) {
  const sign = value < 0 ? '-' : ''
  const absolute = Math.abs(value)
  return `${sign}${Math.trunc(absolute / 100)}.${String(absolute % 100).padStart(2, '0')}`
}

// —— 设置、项目、员工 ——
const businessDate = ref('')
const settings = ref<Setting[]>([])
const items = ref<Item[]>([])
const staffOptions = ref<StaffOption[]>([])
const initLoading = ref(false)
const initGuard = useRequestGuard()
const backfillMaxDays = computed(() => {
  const value = settings.value.find(s => s.key === 'backfill_max_days')?.value
  return Number(value ?? 3)
})
const staffItems = computed(() => staffOptions.value.map(s => ({ label: s.name, value: s.id })))
const activeItems = computed(() => items.value.filter(i => i.active))

async function init() {
  const current = initGuard.next()
  initLoading.value = true
  try {
    const [s, i, st] = await Promise.all([
      api.POST('/api/ops/get_settings', { body: {} }),
      api.POST('/api/ops/list_items', { body: {} }),
      api.POST('/api/ops/list_staff_options', { body: {} })
    ])
    if (!current()) return
    if (!s.data || !i.data || !st.data) {
      toast.add({ title: errorText(s.error ?? i.error ?? st.error), color: 'error' })
      return
    }
    businessDate.value = s.data.business_date
    settings.value = s.data.settings
    items.value = i.data.items
    staffOptions.value = st.data.staff
    await loadOrders()
    await loadUnlinked()
    await nextTick()
    if (route.query.open === 'walkin') {
      // 去掉参数，刷新或后退回来不会再弹一次开单。
      await router.replace({ query: { ...route.query, open: undefined } })
      openOrderBegin(null)
      return
    }
    focusScan()
  } finally {
    if (current()) initLoading.value = false
  }
}

// —— 挂单列表和当前单 ——
const openOrders = ref<Order[]>([])
const ordersLoading = ref(false)
const ordersGuard = useRequestGuard()
const selectedOrderID = ref('')
const selectedOrder = ref<Order | null>(null)
const orderLoading = ref(false)
const orderGuard = useRequestGuard()
const customerNames = ref<Record<string, string>>({})
// 已识别顾客才能挂账，和结账规则一致；要靠 get_customer 得知，所以没有 customer.view 时不显示挂账。
const identifiedCustomers = ref<Record<string, boolean>>({})
const actionSaving = ref(false)
const actionGuard = useRequestGuard()

function clearSelectedOrder() {
  orderGuard.invalidate()
  selectedOrderID.value = ''
  selectedOrder.value = null
  resetCheckout()
}

function customerName(id: string) {
  return customerNames.value[id] || '未识别'
}

function staffName(id: string | null | undefined) {
  return staffOptions.value.find(s => s.id === id)?.name || '未填'
}

async function resolveNames(orders: Order[]) {
  if (!can('customer.view')) return
  const ids = [...new Set(orders.map(o => o.customer_id))].filter(id => !(id in customerNames.value))
  if (!ids.length) return
  const results = await Promise.all(ids.map(id => api.POST('/api/ops/get_customer', { body: { id } })))
  for (const result of results) {
    const customer = result.data
    if (!customer) continue
    customerNames.value[customer.id] = customer.name || '未识别'
    identifiedCustomers.value[customer.id] = !customer.unidentified
  }
}

async function loadOrders() {
  const current = ordersGuard.next()
  ordersLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_open_orders', { body: {} })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    openOrders.value = data.items
    await resolveNames(data.items)
    if (!current()) return
    if (selectedOrderID.value && !data.items.some(o => o.id === selectedOrderID.value)) clearSelectedOrder()
  } finally {
    if (current()) ordersLoading.value = false
  }
}

async function selectOrder(order: Order) {
  if (checkoutSaving.value) return
  const current = orderGuard.next()
  selectedOrderID.value = order.id
  selectedOrder.value = null
  resetCheckout()
  orderLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/get_order', { body: { id: order.id } })
    if (!current() || selectedOrderID.value !== order.id) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    selectedOrder.value = data
    await resolveNames([data])
    if (!current() || selectedOrderID.value !== order.id) return
    await loadCards()
    if (!current()) return
    await previewCheckout()
  } finally {
    if (current()) orderLoading.value = false
  }
}

async function refreshSelected() {
  const id = selectedOrderID.value
  if (!id) return
  const current = orderGuard.next()
  orderLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/get_order', { body: { id } })
    if (!current() || selectedOrderID.value !== id) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    selectedOrder.value = data
    await resolveNames([data])
    if (!current() || selectedOrderID.value !== id) return
    await previewCheckout()
  } finally {
    if (current()) orderLoading.value = false
  }
}

// —— 扫码框 ——
const scanValue = ref('')
const scanKind = ref('')
const scanGuard = useRequestGuard()
const scanLoading = ref(false)
const customerMatches = ref<Customer[]>([])
const pickedCustomer = ref<Customer | null>(null)

function focusScan() {
  document.querySelector<HTMLInputElement>('input[aria-label="扫码或输入手机号、券码"]')?.focus()
}

function looksLikePhone(value: string) {
  return /^1\d{10}$/.test(value.replace(/\s/g, ''))
}

async function submitScan() {
  const value = scanValue.value.trim()
  if (!value) return
  scanGuard.next()
  scanLoading.value = false
  scanKind.value = looksLikePhone(value) ? '手机号' : '券码'
  if (scanKind.value === '手机号') {
    if (!canCustomer.value) return
    await searchCustomers(value.replace(/\s/g, ''))
    return
  }
  customerMatches.value = []
  if (!canGroupbuy.value) return
  couponCode.value = value
  await verifyCoupon()
}

async function searchCustomers(query: string) {
  if (!canCustomer.value) return
  const current = scanGuard.hold()
  scanLoading.value = true
  pickedCustomer.value = null
  customerMatches.value = []
  try {
    const { data, error } = await api.POST('/api/ops/search_customers', { body: { query, include_unidentified: false, limit: 20 } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    customerMatches.value = data.items
    if (!data.items.length) toast.add({ title: '没有找到顾客', color: 'warning' })
  } finally {
    if (current()) scanLoading.value = false
  }
}

function pickCustomer(customer: Customer) {
  pickedCustomer.value = customer
  customerMatches.value = []
  scanValue.value = ''
  focusScan()
}

const pickedOpenOrders = computed(() => pickedCustomer.value
  ? openOrders.value.filter(o => o.customer_id === pickedCustomer.value?.id)
  : [])

// —— 开单 ——
const openOrderForm = useModalForm()
const { open: openOrderOpen, saving: openOrderSaving } = openOrderForm
const orderCustomer = ref<Customer | null>(null)
const orderGender = ref<'male' | 'female'>('female')
const orderDesignated = ref(false)
const orderBackfill = ref(false)
const orderPaperDate = ref('')
const orderItemRows = ref<OrderItemRow[]>([])
const pendingRedemption = ref<Redemption | null>(null)
const pendingDeal = ref<Deal | null>(null)

function defaultPaperDate() {
  return businessDate.value ? addDays(businessDate.value, -1) : ''
}

function positionsOf(itemID: string) {
  const item = items.value.find(i => i.id === itemID)
  return [...(item?.positions ?? [])].sort((a, b) => a.slot - b.slot)
}

function openOrderBegin(customer: Customer | null, deal: Deal | null = null, redemption: Redemption | null = null) {
  openOrderForm.begin(() => {
    orderCustomer.value = customer
    orderGender.value = customer?.gender === 'male' ? 'male' : 'female'
    orderDesignated.value = false
    orderBackfill.value = false
    orderPaperDate.value = defaultPaperDate()
    pendingDeal.value = deal
    pendingRedemption.value = redemption
    orderItemRows.value = deal ? [{ item_id: deal.item_id, staff: {} }] : [{ item_id: '', staff: {} }]
  })
}

function addOrderRow() {
  orderItemRows.value.push({ item_id: '', staff: {} })
}

function removeOrderRow(index: number) {
  orderItemRows.value.splice(index, 1)
}

async function saveOpenOrder() {
  const rows = orderItemRows.value.filter(r => r.item_id)
  if (!rows.length) {
    toast.add({ title: '请选择项目', color: 'warning' })
    return
  }
  const inputItems = rows.map(row => ({
    item_id: row.item_id,
    staff: positionsOf(row.item_id)
      .filter(p => row.staff[p.slot])
      .map(p => ({ slot: p.slot, staff_id: row.staff[p.slot]! }))
  }))
  await openOrderForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/open_order', {
      body: {
        customer_id: orderCustomer.value?.id,
        gender: orderGender.value,
        designated: orderDesignated.value,
        paper_date: orderBackfill.value ? orderPaperDate.value : undefined,
        items: inputItems
      }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    let opened = data
    if (pendingRedemption.value && pendingDeal.value) {
      const linked = await api.POST('/api/ops/link_coupon_order', {
        body: { redemption_id: pendingRedemption.value.id, deal_id: pendingDeal.value.id, order_id: data.id }
      })
      if (!current()) return
      if (linked.data) {
        opened = { ...data, groupbuy: data.groupbuy }
        toast.add({ title: '已开单并关联团购券', color: 'success' })
      } else {
        toast.add({ title: `已开单，但关联团购券失败：${errorText(linked.error)}`, color: 'error' })
      }
    } else {
      toast.add({ title: '已开单', color: 'success' })
    }
    if (pendingRedemption.value && pendingDeal.value) {
      pendingRedemption.value = null
      pendingDeal.value = null
      clearCoupon()
    }
    openOrderOpen.value = false
    pickedCustomer.value = null
    scanValue.value = ''
    await loadOrders()
    await selectOrder(opened)
    await loadUnlinked()
    focusScan()
  })
}

// —— 当前单编辑 ——
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
  if (!selectedOrder.value || !addItemID.value) return
  const order = selectedOrder.value
  await addItemForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/add_order_item', {
      body: {
        order_id: order.id,
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
    invalidatePreview()
    await refreshSelected()
    await loadOrders()
    focusScan()
  })
}

async function removeItem(orderItemID: string) {
  const order = selectedOrder.value
  if (!order || !canOrderEdit.value) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/remove_order_item', { body: { order_id: order.id, order_item_id: orderItemID } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    invalidatePreview()
    await refreshSelected()
    await loadOrders()
    focusScan()
  } finally {
    if (current()) actionSaving.value = false
  }
}

// 同一顾客、同一营业日、同为补录或同为普通单，且每张都能改的其他挂单可以并进当前单（merge_orders 的条件）。
const mergeableOrders = computed(() => {
  const order = selectedOrder.value
  if (!order) return []
  return openOrders.value.filter(o => o.id !== order.id && o.customer_id === order.customer_id && o.business_date === order.business_date && o.backfill === order.backfill && mayChange('order.edit', o))
})

async function mergeOrders() {
  const order = selectedOrder.value
  const merged = mergeableOrders.value.map(o => o.id)
  if (!order || !canOrderEdit.value || !merged.length) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/merge_orders', { body: { order_id: order.id, merged_order_ids: merged } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: `已合并 ${merged.length} 张挂单`, color: 'success' })
    if (selectedOrderID.value !== order.id) {
      await loadOrders()
      return
    }
    invalidatePreview()
    await refreshSelected()
    await loadOrders()
    focusScan()
  } finally {
    if (current()) actionSaving.value = false
  }
}

async function changeStaff(item: components['schemas']['OrderItem'], slot: number, staffID: string) {
  const order = selectedOrder.value
  if (!order || !canOrderEdit.value) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const staff = item.positions
      .map(p => ({ slot: p.slot, staff_id: p.slot === slot ? staffID : (p.staff_id ?? '') }))
      .filter(p => p.staff_id)
    const { data, error } = await api.POST('/api/ops/set_order_item_staff', {
      body: { order_id: order.id, order_item_id: item.id, staff }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    invalidatePreview()
    await refreshSelected()
    focusScan()
  } finally {
    if (current()) actionSaving.value = false
  }
}

const repriceForm = useModalForm()
const { open: repriceOpen, saving: repriceSaving } = repriceForm
const repriceItem = ref<components['schemas']['OrderItem'] | null>(null)
const repricePrice = ref('0.00')
const repriceReason = ref('')

function repriceBegin(item: components['schemas']['OrderItem']) {
  repriceForm.begin(() => {
    repriceItem.value = item
    repricePrice.value = item.price
    repriceReason.value = ''
  })
}

async function saveReprice() {
  const order = selectedOrder.value
  const item = repriceItem.value
  if (!order || !item || !canReprice.value) return
  await repriceForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/reprice_order_item', {
      body: { order_id: order.id, order_item_id: item.id, price: repricePrice.value, reason: repriceReason.value.trim() }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    repriceOpen.value = false
    invalidatePreview()
    await refreshSelected()
    await loadOrders()
    focusScan()
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
    await refreshSelected()
    await loadOrders()
    await loadUnlinked()
    focusScan()
  } finally {
    if (current()) actionSaving.value = false
  }
}

// —— 结账 ——
const preview = ref<components['schemas']['PreviewCheckout'] | null>(null)
const previewLoading = ref(false)
const previewGuard = useRequestGuard()
const checkoutSaving = ref(false)
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
  ...(selectedOrder.value && identifiedCustomers.value[selectedOrder.value.customer_id] ? [{ label: '挂账', value: 'debt' }] : [])
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
  const id = selectedOrder.value?.customer_id
  if (!id || !can('customer.view')) return
  const current = cardsGuard.next()
  const { data } = await api.POST('/api/ops/list_customer_cards', { body: { customer_id: id } })
  if (!current() || selectedOrder.value?.customer_id !== id) return
  cards.value = (data?.items ?? []).filter(c => c.status === 'active')
}

function countCardsFor(itemID: string) {
  const day = selectedOrder.value?.business_date || businessDate.value
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
const countTarget = ref<components['schemas']['OrderItem'] | null>(null)
const countCardID = ref(NONE)
const countCardOptions = computed(() => {
  const item = countTarget.value
  if (!item) return []
  return countCardsFor(item.item_id).map((card) => {
    const remaining = card.items.find(count => count.item_id === item.item_id)
    return { label: `${card.card_type_name}（剩 ${(remaining?.paid_remaining ?? 0) + (remaining?.gift_remaining ?? 0)} 次）`, value: card.id }
  })
})

function countBegin(item: components['schemas']['OrderItem']) {
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
  const order = selectedOrder.value
  if (!order || !canCollect.value) return
  const current = previewGuard.next()
  previewLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/preview_checkout', {
      body: { order_id: order.id, card_id: selectedCardID.value === NONE ? undefined : selectedCardID.value }
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

function recalculationCard() {
  void previewCheckout()
}

async function checkout() {
  if (checkoutSaving.value || !canCollect.value) return
  const order = selectedOrder.value
  if (!order || !preview.value) return
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
  checkoutSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/checkout_orders', {
      body: { orders: [{ order_id: order.id, payments: [...countInputs, ...cashPayments] }] }
    })
    if (!current() || selectedOrderID.value !== order.id) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    const paidOrder = data.items[0]
    const checkoutID = paidOrder?.checkout_id
    clearSelectedOrder()
    settledKey.value++
    await loadOrders()
    await loadUnlinked()
    toast.add({ title: '结账成功', color: 'success' })
    if (checkoutID) await showReceipt(checkoutID)
    focusScan()
  } finally {
    checkoutSaving.value = false
  }
}

const settledKey = ref(0)

// 撤单退回了卡余额、次数和红包：撤掉的是最近一次结账就不再能补打小票；同一顾客的当前单重新读卡并试算。
async function onVoided(order: Order) {
  if (lastReceipt.value?.checkout_id === order.checkout_id) lastReceipt.value = null
  if (selectedOrder.value?.customer_id !== order.customer_id) return
  invalidatePreview()
  await loadCards()
  await refreshSelected()
}

// —— 小票 ——
const receipt = ref<components['schemas']['Receipt'] | null>(null)
const lastReceipt = ref<components['schemas']['Receipt'] | null>(null)
const receiptGuard = useRequestGuard()

async function showReceipt(checkoutID: string) {
  if (!canCollect.value) return
  const current = receiptGuard.next()
  const { data, error } = await api.POST('/api/ops/get_receipt', { body: { checkout_id: checkoutID } })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  receipt.value = data
  lastReceipt.value = data
  await nextTick()
  window.print()
}

function closeReceipt() {
  receiptGuard.invalidate()
  receipt.value = null
}

// —— 团购 ——
const platform = ref('meituan')
const couponPlatform = ref('')
const couponCode = ref('')
const couponKey = ref('')
const couponBusy = ref('')
const couponGuard = useRequestGuard()
const verifyResult = ref<components['schemas']['CouponResult'] | null>(null)
const redemption = ref<Redemption | null>(null)
const couponUnknown = ref(false)
const deals = ref<Deal[]>([])
const dealsGuard = useRequestGuard()
const selectedDealID = ref('')
const linkOrderID = ref(NONE)
const activeDeals = computed(() => deals.value.filter(d => d.active))
const dealItems = computed(() => activeDeals.value.map(d => ({ label: `${d.name}（购买价 ${d.purchase_price}）`, value: d.id })))
const linkOrderItems = computed(() => openOrders.value.map(o => ({ label: `${customerName(o.customer_id)} · ${o.items.filter(i => !i.removed_at).map(i => i.item_name).join('、') || '无项目'} · 应收 ${o.receivable}`, value: o.id })))
const unlinked = ref<Redemption[]>([])
const unlinkedGuard = useRequestGuard()

function platformLabel(value: string) {
  return platformItems.find(p => p.value === value)?.label ?? value
}

function startCouponAttempt(couponPlatformValue: string) {
  couponKey.value = crypto.randomUUID()
  couponPlatform.value = couponPlatformValue
  couponUnknown.value = false
}

async function verifyCoupon() {
  if (!can('groupbuy.redeem')) {
    toast.add({ title: '没有团购核销权限', color: 'error' })
    return
  }
  couponCode.value = couponCode.value.trim()
  if (!couponCode.value) return
  const current = couponGuard.next()
  startCouponAttempt(platform.value)
  redemption.value = null
  verifyResult.value = null
  couponBusy.value = 'verify'
  try {
    const { data, error } = await api.POST('/api/ops/verify_coupon', {
      body: { platform: couponPlatform.value, coupon_code: couponCode.value, key: couponKey.value }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    verifyResult.value = data
  } finally {
    if (current()) couponBusy.value = ''
  }
}

async function redeemCoupon() {
  if (!canGroupbuy.value || !verifyResult.value || couponBusy.value) return
  const current = couponGuard.hold()
  couponBusy.value = 'redeem'
  try {
    const { data, error } = await api.POST('/api/ops/redeem_coupon', {
      body: { platform: couponPlatform.value, coupon_code: couponCode.value, key: couponKey.value }
    })
    if (!current()) return
    if (!data) {
      couponUnknown.value = true
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    redemption.value = data
    verifyResult.value = { platform: data.platform, status: 'redeemed', redeemed_at: data.redeemed_at }
    await loadDeals(data.platform)
    await loadUnlinked()
    focusScan()
  } finally {
    if (current()) couponBusy.value = ''
  }
}

async function queryRedemption() {
  if (!canGroupbuy.value || !couponKey.value || couponBusy.value) return
  const current = couponGuard.hold()
  couponBusy.value = 'query'
  try {
    const { data, error } = await api.POST('/api/ops/get_redemption', { body: { key: couponKey.value } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    redemption.value = data
    couponUnknown.value = data.status === 'unknown'
    if (data.status === 'redeemed') await loadDeals(data.platform)
    await loadUnlinked()
    focusScan()
  } finally {
    if (current()) couponBusy.value = ''
  }
}

async function loadDeals(couponPlatform: string) {
  if (!canGroupbuy.value) return
  const current = dealsGuard.next()
  const { data, error } = await api.POST('/api/ops/list_groupbuy_deals', { body: {} })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  deals.value = data.items.filter(d => d.platform === couponPlatform)
  selectedDealID.value = deals.value.find(d => d.active)?.id ?? NONE
}

async function linkRedeemed() {
  if (!canGroupbuy.value || !redemption.value || selectedDealID.value === NONE || linkOrderID.value === NONE) return
  const current = couponGuard.hold()
  couponBusy.value = 'link'
  try {
    const { data, error } = await api.POST('/api/ops/link_coupon_order', {
      body: { redemption_id: redemption.value.id, deal_id: selectedDealID.value, order_id: linkOrderID.value }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已关联挂单', color: 'success' })
    const id = linkOrderID.value
    clearCoupon()
    await loadOrders()
    const order = openOrders.value.find(o => o.id === id)
    if (order) await selectOrder(order)
    await loadUnlinked()
    focusScan()
  } finally {
    if (current()) couponBusy.value = ''
  }
}

function openOrderFromCoupon() {
  const deal = activeDeals.value.find(d => d.id === selectedDealID.value)
  if (!deal || !redemption.value) return
  openOrderBegin(pickedCustomer.value, deal, redemption.value)
}

function clearCoupon() {
  couponGuard.invalidate()
  verifyResult.value = null
  redemption.value = null
  couponUnknown.value = false
  couponCode.value = ''
  couponKey.value = ''
  couponPlatform.value = ''
  selectedDealID.value = NONE
  linkOrderID.value = NONE
  scanValue.value = ''
  couponBusy.value = ''
  focusScan()
}

async function revokeCoupon(row: Redemption) {
  if (!canGroupbuy.value) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/revoke_coupon', { body: { key: row.key } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已撤销核销', color: 'success' })
    if (redemption.value?.id === row.id) clearCoupon()
    await loadUnlinked()
    focusScan()
  } finally {
    if (current()) actionSaving.value = false
  }
}

async function loadUnlinked() {
  if (!canGroupbuy.value || !businessDate.value) return
  const current = unlinkedGuard.next()
  const { data, error } = await api.POST('/api/ops/list_redemptions', {
    body: { from: addDays(businessDate.value, -90), to: businessDate.value, status: 'redeemed', unlinked_only: true }
  })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  unlinked.value = data.items
}

const linkForm = useModalForm()
const { open: linkOpen, saving: linkSaving } = linkForm
const linkTarget = ref<Redemption | null>(null)
const linkDealID = ref(NONE)
const linkTargetOrderID = ref(NONE)

async function linkBegin(row: Redemption) {
  if (!canGroupbuy.value) return
  linkForm.begin(() => {
    linkTarget.value = row
    linkDealID.value = NONE
    linkTargetOrderID.value = NONE
  })
  await linkForm.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/list_groupbuy_deals', { body: {} })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    deals.value = data.items.filter(d => d.platform === row.platform)
    linkDealID.value = deals.value.find(d => d.active)?.id ?? NONE
  })
}

async function saveLink() {
  const row = linkTarget.value
  if (!canGroupbuy.value || !row || linkDealID.value === NONE || linkTargetOrderID.value === NONE) return
  await linkForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/link_coupon_order', {
      body: { redemption_id: row.id, deal_id: linkDealID.value, order_id: linkTargetOrderID.value }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    linkOpen.value = false
    toast.add({ title: '已关联挂单', color: 'success' })
    await loadOrders()
    await loadUnlinked()
    focusScan()
  })
}

onMounted(init)
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <span v-if="businessDate" class="text-meta text-muted">营业日 {{ businessDate }}</span>
      <UButton size="sm" icon="i-lucide-plus" @click="openOrderBegin(pickedCustomer)">开单</UButton>
    </PageActions>

    <section v-if="canCustomer || canGroupbuy" class="space-y-3">
      <form class="flex flex-wrap items-center gap-2" @submit.prevent="submitScan">
        <UInput
          v-model="scanValue"
          autofocus
          autocomplete="off"
          size="lg"
          icon="i-lucide-scan-line"
          :placeholder="canGroupbuy ? '扫码枪扫券，或输入手机号、券码' : '输入手机号'"
          aria-label="扫码或输入手机号、券码"
          data-testid="scan-input"
          class="min-w-64 flex-1"
        />
        <USelect v-if="canGroupbuy" v-model="platform" :items="platformItems" size="lg" class="w-28" aria-label="券码平台" :disabled="couponBusy !== '' || !!verifyResult || !!redemption || couponUnknown" />
        <UButton type="submit" size="lg" color="neutral" variant="outline">确认</UButton>
        <span v-if="scanLoading" class="text-meta text-muted">搜索中…</span>
        <span v-if="scanKind" data-testid="scan-kind" class="tag bg-tag-gray">识别为{{ scanKind }}</span>
      </form>

      <div v-if="customerMatches.length" class="box space-y-2" data-testid="customer-matches">
        <p class="text-meta text-muted">找到 {{ customerMatches.length }} 位顾客，选择一位：</p>
        <div class="flex flex-wrap gap-2">
          <UButton v-for="customer in customerMatches" :key="customer.id" color="neutral" variant="outline" @click="pickCustomer(customer)">
            {{ customer.name || '未识别' }} · {{ customer.phone || '无手机号' }}
          </UButton>
        </div>
      </div>

      <div v-if="pickedCustomer" class="box flex flex-wrap items-center gap-2" data-testid="picked-customer">
        <span class="text-meta text-muted">已选顾客</span>
        <span class="font-medium">{{ pickedCustomer.name || '未识别' }}</span>
        <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-plus" @click="openOrderBegin(pickedCustomer)">为这位顾客开单</UButton>
        <template v-if="pickedOpenOrders.length">
          <span class="text-meta text-muted">还有 {{ pickedOpenOrders.length }} 张挂单：</span>
          <UButton v-for="order in pickedOpenOrders" :key="order.id" size="sm" color="neutral" variant="ghost" @click="selectOrder(order)">选择挂单</UButton>
        </template>
      </div>

      <div v-if="canGroupbuy && (verifyResult || redemption || couponUnknown)" class="box space-y-3" data-testid="coupon-panel">
        <div class="flex flex-wrap items-center gap-2">
          <span class="tag bg-tag-gray">{{ platformLabel(couponPlatform || platform) }}</span>
          <span class="font-medium tabular-nums">券码 {{ couponCode }}</span>
          <span v-if="redemption" data-testid="coupon-status" class="tag" :class="redemption.status === 'redeemed' ? 'bg-tag-green' : 'bg-tag-yellow'">{{ redemption.status }}</span>
          <span v-else-if="verifyResult" class="tag" :class="verifyResult.status === 'unused' ? 'bg-tag-green' : 'bg-tag-yellow'">{{ verifyResult.status }}</span>
        </div>

        <div v-if="verifyResult && !redemption && verifyResult.status === 'unused' && !couponUnknown" class="flex flex-wrap items-center gap-2">
          <span class="text-sm">券未使用，可以核销。</span>
          <UButton size="sm" color="neutral" :loading="couponBusy === 'redeem'" @click="redeemCoupon">核销</UButton>
        </div>

        <div v-if="couponUnknown || (redemption && redemption.status === 'unknown')" class="flex flex-wrap items-center gap-2">
          <span class="text-sm text-ink-orange">核销结果未知，请先查询状态，不要再次核销。</span>
          <UButton size="sm" color="neutral" variant="outline" :loading="couponBusy === 'query'" @click="queryRedemption">查询状态</UButton>
        </div>

        <template v-if="redemption && redemption.status === 'redeemed' && !redemption.order_id">
          <div class="grid gap-2 md:grid-cols-2">
            <UFormField label="团购商品">
              <USelect v-model="selectedDealID" :items="dealItems" class="w-full" aria-label="团购商品" />
            </UFormField>
            <UFormField label="关联挂单">
              <USelect v-model="linkOrderID" :items="[{ label: '选择挂单', value: NONE }, ...linkOrderItems]" class="w-full" aria-label="关联挂单" />
            </UFormField>
          </div>
          <p v-if="!activeDeals.length" class="text-sm text-ink-orange">这个平台还没有启用的团购商品，请先到“团购商品”登记。</p>
          <div class="flex flex-wrap gap-2">
            <UButton size="sm" color="neutral" :disabled="!activeDeals.length" @click="openOrderFromCoupon">立即开单</UButton>
            <UButton size="sm" color="neutral" variant="outline" :disabled="!activeDeals.length || linkOrderID === NONE" :loading="couponBusy === 'link'" @click="linkRedeemed">关联挂单</UButton>
            <UButton size="sm" color="neutral" variant="ghost" @click="clearCoupon">暂不开单</UButton>
            <UButton size="sm" color="error" variant="ghost" class="ms-auto" :loading="actionSaving" @click="revokeCoupon(redemption)">撤销核销</UButton>
          </div>
        </template>

        <div v-if="redemption && redemption.order_id" class="flex flex-wrap items-center gap-2">
          <span class="tag bg-tag-green">已关联挂单</span>
          <UButton size="sm" color="neutral" variant="outline" :loading="actionSaving" @click="unlinkCoupon(redemption.id)">解除关联</UButton>
          <UButton size="sm" color="neutral" variant="ghost" @click="clearCoupon">完成</UButton>
        </div>

        <div v-if="redemption && redemption.status === 'revoked'" class="flex items-center gap-2">
          <span class="tag bg-tag-gray">已撤销</span>
          <UButton size="sm" color="neutral" variant="ghost" @click="clearCoupon">关闭</UButton>
        </div>
      </div>
    </section>

    <div class="grid items-start gap-4 lg:grid-cols-[20rem_minmax(0,1fr)]">
      <div class="space-y-4">
        <section>
          <h2 class="sec">挂单<span class="ms-1 font-normal text-dimmed">{{ openOrders.length }}</span></h2>
          <div class="box overflow-hidden p-0" data-testid="open-orders">
            <ListSkeleton v-if="ordersLoading && !openOrders.length" :rows="3" />
            <EmptyState v-else-if="!openOrders.length" title="暂无挂单" hint="顾客到店开单后会挂在这里，做完再结账" />
            <button
              v-for="order in openOrders"
              :key="order.id"
              type="button"
              class="block w-full border-t border-default px-3 py-2.5 text-left first:border-t-0"
              :class="order.id === selectedOrderID ? 'bg-selected shadow-[inset_2px_0_0_var(--ui-primary)]' : 'hover:bg-hover'"
              :aria-current="order.id === selectedOrderID ? 'true' : undefined"
              :data-order-id="order.id"
              data-testid="open-order"
              :disabled="checkoutSaving"
              @click="selectOrder(order)"
            >
              <div class="flex items-center justify-between gap-2 text-sm">
                <span class="truncate font-medium" :title="customerName(order.customer_id)">{{ customerName(order.customer_id) }}</span>
                <span class="font-medium tabular-nums">{{ yuanFixed(order.receivable) }}</span>
              </div>
              <div class="mt-0.5 flex items-center gap-1.5 text-meta text-muted">
                <span v-if="order.backfill" data-testid="backfill-badge" class="tag bg-tag-yellow">补录 {{ order.business_date }}</span>
                <span class="truncate" :title="order.items.filter(i => !i.removed_at).map(i => i.item_name).join('、')">{{ order.items.filter(i => !i.removed_at).map(i => i.item_name).join('、') || '无项目' }}</span>
              </div>
            </button>
          </div>
          <div v-if="canCollect && lastReceipt" class="mt-2 flex items-center gap-2 px-1 text-meta text-muted" data-testid="last-receipt">
            <span>最近一次结账 {{ yuanFixed(lastReceipt.total) }}</span>
            <UButton size="xs" color="neutral" variant="ghost" class="ms-auto" @click="receipt = lastReceipt">补打小票</UButton>
          </div>
        </section>

        <CashierSettled :refresh-key="settledKey" :customer-name="customerName" :resolve-names="resolveNames" :can-void="o => mayChange('order.void', o)" @voided="onVoided" />
      </div>

      <section data-testid="selected-order">
        <h2 class="sec">
          当前单
          <template v-if="selectedOrder">
            <span class="font-normal">· {{ customerName(selectedOrder.customer_id) }} · {{ selectedOrder.gender === 'male' ? '男客' : '女客' }}</span>
            <span v-if="selectedOrder.designated" class="tag bg-tag-gray">指定</span>
            <span v-if="selectedOrder.backfill" data-testid="selected-backfill" class="tag bg-tag-yellow">补录 {{ selectedOrder.business_date }}</span>
            <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-plus" class="ms-auto" @click="addItemBegin">加项目</UButton>
          </template>
        </h2>

        <p v-if="orderLoading" class="box text-sm text-muted">加载中…</p>
        <EmptyState v-else-if="!selectedOrder" class="box" art="receipt" title="还没选单" hint="从左侧选一张挂单，或扫码开单" />
        <div v-else class="grid items-start gap-4" :class="canCollect ? 'xl:grid-cols-[minmax(0,1fr)_20rem]' : ''">
          <div class="space-y-4">
            <div v-if="canOrderEdit && mergeableOrders.length" class="box flex items-center gap-2 py-2 text-sm" role="status" data-testid="merge-hint">
              <span>这位顾客还有 {{ mergeableOrders.length }} 张同日挂单（{{ yuanFixed(money(mergeableOrders.reduce((sum, o) => sum + cents(o.receivable), 0))) }}），可以并成一张结账</span>
              <UButton size="sm" color="neutral" variant="outline" class="ms-auto" :loading="actionSaving" :disabled="checkoutSaving" @click="mergeOrders">合并到这张</UButton>
            </div>
            <div class="box overflow-hidden p-0">
              <div
                v-for="item in selectedOrder.items.filter(i => !i.removed_at)"
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
                    <UButton v-else-if="countCardsFor(item.item_id).length" size="sm" color="neutral" variant="outline" :disabled="!preview || checkoutSaving" @click="countBegin(item)">用次卡</UButton>
                  </template>
                  <div class="ms-auto flex gap-1 opacity-0 group-hover/row:opacity-100 focus-within:opacity-100 any-pointer-coarse:opacity-100">
                    <UButton v-if="canReprice" size="sm" color="neutral" variant="ghost" @click="repriceBegin(item)">改价</UButton>
                    <UButton v-if="canOrderEdit" size="sm" color="error" variant="ghost" :loading="actionSaving" @click="removeItem(item.id)">去掉</UButton>
                  </div>
                </div>
              </div>
            </div>

            <div v-if="canGroupbuy && selectedOrder.groupbuy.length" data-testid="order-groupbuy">
              <h2 class="sec">关联团购券</h2>
              <div class="box overflow-hidden p-0">
                <div v-for="coupon in selectedOrder.groupbuy" :key="coupon.redemption_id" class="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-default px-3 py-2.5 text-sm first:border-t-0">
                  <span class="font-medium">{{ coupon.deal_name }}</span>
                  <span class="tag bg-tag-gray">{{ platformLabel(coupon.platform) }}</span>
                  <span class="text-muted tabular-nums">购买价 {{ coupon.purchase_price }} · 门店标价 {{ coupon.store_price }}</span>
                  <UButton size="sm" color="error" variant="ghost" class="ms-auto" :loading="actionSaving" @click="unlinkCoupon(coupon.redemption_id)">解除关联</UButton>
                </div>
              </div>
            </div>
            <p v-if="!canCollect" class="px-1 text-meta text-muted tabular-nums">应收 {{ yuanFixed(selectedOrder.receivable) }} · 实收 {{ yuanFixed(selectedOrder.net) }}</p>
          </div>

          <div v-if="canCollect" class="box space-y-3">
            <div class="flex items-center justify-between">
              <h2 class="font-medium">结账</h2>
              <span v-if="previewLoading" class="text-meta text-muted">试算中…</span>
            </div>
            <USelect v-model="selectedCardID" :items="[{ label: '不使用卡', value: NONE }, ...cardItems]" class="w-full" aria-label="计价卡" @update:model-value="recalculationCard" />
            <dl class="space-y-1.5 border-t border-default pt-3 text-sm tabular-nums">
              <div class="flex justify-between"><dt class="text-muted">应收</dt><dd>{{ yuanFixed(selectedOrder.receivable) }}</dd></div>
              <div class="flex justify-between"><dt class="text-muted">实收</dt><dd>{{ yuanFixed(preview?.net ?? selectedOrder.net) }}</dd></div>
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
              <UButton size="lg" block :loading="checkoutSaving" :disabled="previewLoading || actionSaving || orderLoading || checkoutSaving" @click="checkout">结账</UButton>
            </template>
          </div>
        </div>
      </section>
    </div>

    <section v-if="canGroupbuy" data-testid="unlinked-coupons">
      <h2 class="sec">未关联团购券<span class="ms-1 font-normal text-dimmed">{{ unlinked.length }}</span><span class="ms-auto text-meta font-normal text-dimmed">近 90 天</span></h2>
      <div class="box overflow-hidden p-0">
        <p v-if="!unlinked.length" class="px-3 py-2.5 text-sm text-muted">没有未关联的团购券</p>
        <div v-for="row in unlinked" :key="row.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-default px-3 py-2.5 text-sm first:border-t-0 hover:bg-hover">
          <span class="tag bg-tag-gray">{{ platformLabel(row.platform) }}</span>
          <span class="font-medium tabular-nums">{{ row.coupon_code }}</span>
          <span class="text-meta text-muted">{{ row.redeemed_at ? new Date(row.redeemed_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) : '' }}</span>
          <UButton size="sm" color="neutral" variant="outline" class="ms-auto" @click="linkBegin(row)">关联挂单</UButton>
          <UButton size="sm" color="error" variant="ghost" :loading="actionSaving" @click="revokeCoupon(row)">撤销核销</UButton>
        </div>
      </div>
    </section>
    <UModal v-model:open="openOrderOpen" :title="pendingDeal ? '团购立即开单' : '开单'" description="选好项目和做的人，开单后挂在左边，做完再结账" :ui="{ content: 'sm:max-w-[600px]' }">
      <template #body>
        <div class="space-y-5">
          <!-- 顾客、性别、指定、补录收进一块浅底：都是"这单是谁、怎么来的"，和下面的项目分开。 -->
          <div class="space-y-3 rounded-lg bg-side p-3.5 ring-1 ring-default ring-inset">
            <div class="flex items-center gap-3">
              <div class="min-w-0 flex-1">
                <p class="text-meta text-muted">顾客</p>
                <p class="truncate font-medium" :class="orderCustomer ? 'text-default' : 'text-muted'">{{ orderCustomer?.name || '未识别顾客' }}</p>
              </div>
              <MethodPicker v-model="orderGender" label="顾客性别" :items="[{ label: '女客', value: 'female' }, { label: '男客', value: 'male' }]" />
            </div>
            <div class="flex items-center gap-6 border-t border-default pt-3">
              <USwitch v-model="orderDesignated" label="指定" />
              <USwitch v-model="orderBackfill" label="补录" />
            </div>
            <UFormField v-if="orderBackfill" label="纸单营业日" :help="`最多补录最近 ${backfillMaxDays} 个营业日`">
              <DateInput v-model="orderPaperDate" :max="businessDate" :aria-label="'纸单营业日'" />
            </UFormField>
          </div>
          <FieldGroup label="项目">
            <div v-for="(row, index) in orderItemRows" :key="index" class="space-y-2" data-testid="order-item-row">
              <div class="flex items-center gap-2">
                <USelect v-model="row.item_id" :items="activeItems.map(i => ({ label: `${i.name}（${i.price}）`, value: i.id }))" class="min-w-0 flex-1" :aria-label="`项目${index + 1}`" placeholder="选择项目" @update:model-value="row.staff = {}" />
                <UButton variant="ghost" color="error" icon="i-lucide-x" aria-label="去掉" :disabled="orderItemRows.length === 1" @click="removeOrderRow(index)" />
              </div>
              <div v-for="position in positionsOf(row.item_id)" :key="position.slot" class="flex items-center gap-2 ps-3">
                <span class="w-14 shrink-0 text-meta text-muted">岗位 {{ position.slot }}</span>
                <USelect v-model="row.staff[position.slot]" :items="staffItems" class="min-w-0 flex-1" :aria-label="`项目${index + 1}岗位${position.slot}员工`" placeholder="选择员工" />
                <span v-if="position.required" class="tag shrink-0 bg-tag-yellow">必填</span>
              </div>
            </div>
            <UButton color="neutral" icon="i-lucide-plus" class="-ms-2.5 self-start" variant="ghost" @click="addOrderRow">添加项目</UButton>
          </FieldGroup>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="openOrderOpen = false">
          <UButton :loading="openOrderSaving" @click="saveOpenOrder">开单</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="addItemOpen" title="加项目">
      <template #body>
        <div class="space-y-5">
          <UFormField label="项目">
            <USelect v-model="addItemID" :items="activeItems.map(i => ({ label: `${i.name}（${i.price}）`, value: i.id }))" class="w-full" aria-label="加项目" placeholder="选择项目" @update:model-value="addItemStaff = {}" />
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

    <UModal v-model:open="linkOpen" title="关联团购券到挂单" :description="`券码 ${linkTarget?.coupon_code ?? ''}（${linkTarget ? platformLabel(linkTarget.platform) : ''}）`">
      <template #body>
        <div class="space-y-5">
          <UFormField label="团购商品"><USelect v-model="linkDealID" :items="dealItems" class="w-full" aria-label="关联团购商品" /></UFormField>
          <UFormField label="挂单"><USelect v-model="linkTargetOrderID" :items="[{ label: '选择挂单', value: NONE }, ...linkOrderItems]" class="w-full" aria-label="关联到挂单" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="linkOpen = false">
          <UButton :loading="linkSaving" @click="saveLink">关联</UButton>
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

    <Receipt v-if="receipt" :receipt="receipt" @close="closeReceipt" />
  </div>
</template>
