<script setup lang="ts">
import type { components } from '~/utils/api'

// 收银工作台：扫码框、挂单列表、今日结账在这里；当前单、团购券面板、未关联券、开单弹窗各是一个组件，
// 页面只管它们之间的联动（开了单就选中、关联了券就刷新挂单等）。
definePageMeta({ permission: 'order.create', title: '收银', workbench: true, art: 'register' })

type Order = components['schemas']['Order']
type Customer = components['schemas']['Customer']
type Item = components['schemas']['Item']
type StaffOption = components['schemas']['StaffOption']
type Deal = components['schemas']['Deal']
type Redemption = components['schemas']['Redemption']
type Setting = components['schemas']['Setting']
type Platform = components['schemas']['CreateDealInput']['platform']

const api = useApi()
const route = useRoute()
const router = useRouter()
const toast = useToast()
const { me, can, grant } = useMe()
const canCollect = computed(() => can('payment.collect'))
const canGroupbuy = computed(() => can('groupbuy.redeem'))
const canCustomer = computed(() => can('customer.view'))
const panel = useTemplateRef('panel')
const coupon = useTemplateRef('coupon')
const unlinkedList = useTemplateRef('unlinkedList')
const openOrderModal = useTemplateRef('openOrderModal')

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

// —— 设置、项目、员工 ——
const businessDate = ref('')
const settings = ref<Setting[]>([])
const items = ref<Item[]>([])
const staffOptions = ref<StaffOption[]>([])
const initGuard = useRequestGuard()
const backfillMaxDays = computed(() => {
  const value = settings.value.find(s => s.key === 'backfill_max_days')?.value
  return Number(value ?? 3)
})
const staffItems = computed(() => staffOptions.value.map(s => ({ label: s.name, value: s.id })))
const activeItems = computed(() => items.value.filter(i => i.active))

async function init() {
  const current = initGuard.next()
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
  await nextTick()
  await loadUnlinked()
  if (route.query.open === 'walkin') {
    // 去掉参数，刷新或后退回来不会再弹一次开单。
    await router.replace({ query: { ...route.query, open: undefined } })
    openOrderBegin(null)
    return
  }
  focusScan()
}

// —— 挂单列表 ——
const openOrders = ref<Order[]>([])
const ordersLoading = ref(false)
const ordersGuard = useRequestGuard()
const selectedOrderID = ref('')
const checkoutSaving = ref(false)
const customerNames = ref<Record<string, string>>({})
// 已识别顾客才能挂账，和结账规则一致；要靠 get_customer 得知，所以没有 customer.view 时不显示挂账。
const identifiedCustomers = ref<Record<string, boolean>>({})

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
    if (selectedOrderID.value && !data.items.some(o => o.id === selectedOrderID.value)) panel.value?.clear()
  } finally {
    if (current()) ordersLoading.value = false
  }
}

async function selectOrder(order: Order) {
  await panel.value?.select(order)
}

async function loadUnlinked() {
  await unlinkedList.value?.load()
}

// —— 扫码框 ——
const scanValue = ref('')
const scanKind = ref('')
const scanGuard = useRequestGuard()
const scanLoading = ref(false)
const customerMatches = ref<Customer[]>([])
const pickedCustomer = ref<Customer | null>(null)
const platform = ref<Platform>('meituan')

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
  await coupon.value?.verify(value)
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

// —— 各块之间的联动 ——
function openOrderBegin(customer: Customer | null, deal: Deal | null = null, redemption: Redemption | null = null) {
  openOrderModal.value?.begin(customer, deal, redemption)
}

async function onOpened(order: Order, viaCoupon: boolean) {
  if (viaCoupon) coupon.value?.clear()
  pickedCustomer.value = null
  scanValue.value = ''
  await loadOrders()
  await selectOrder(order)
  await loadUnlinked()
  focusScan()
}

function onCouponCleared() {
  scanValue.value = ''
  focusScan()
}

async function onCouponChanged() {
  await loadUnlinked()
  focusScan()
}

async function onCouponLinked(orderID: string) {
  await loadOrders()
  const order = openOrders.value.find(o => o.id === orderID)
  if (order) await selectOrder(order)
  await loadUnlinked()
  focusScan()
}

async function onCouponUnlinked() {
  await panel.value?.refresh()
  await loadOrders()
  await loadUnlinked()
  focusScan()
}

async function onCheckedOut(checkoutID: string | null | undefined) {
  settledKey.value++
  await loadOrders()
  await loadUnlinked()
  if (checkoutID) await showReceipt(checkoutID)
  focusScan()
}

const settledKey = ref(0)

// 撤单退回了卡余额、次数和红包：撤掉的是最近一次结账就不再能补打小票；同一顾客的当前单重新读卡并试算。
async function onVoided(order: Order) {
  if (lastReceipt.value?.checkout_id === order.checkout_id) lastReceipt.value = null
  await panel.value?.voided(order)
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
        <USelect v-if="canGroupbuy" v-model="platform" :items="platformItems" size="lg" class="w-28" aria-label="券码平台" :disabled="coupon?.locked" />
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

      <CashierCoupon
        ref="coupon"
        :platform="platform"
        :open-orders="openOrders"
        :customer-name="customerName"
        @open-order="(deal, redemption) => openOrderBegin(pickedCustomer, deal, redemption)"
        @linked="onCouponLinked"
        @unlinked="onCouponUnlinked"
        @changed="onCouponChanged"
        @cleared="onCouponCleared"
      />
    </section>

    <!-- 源码顺序就是窄屏一栏时的顺序（也是键盘顺序）：挂单 → 当前单 → 今日结账，先选挂单、紧接着就是这张单。
         宽屏时挂单、今日结账叠在左栏，当前单占右栏两行；第二行 1fr 吃掉当前单多出的高度，左栏两块之间不留空。 -->
    <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-[20rem_minmax(0,1fr)] lg:grid-rows-[auto_1fr]">
      <section class="min-w-0 lg:col-start-1 lg:row-start-1">
        <h2 class="sec">挂单<span class="ms-1 font-normal text-dimmed">{{ openOrders.length }}</span></h2>
        <div class="border-y border-default" data-testid="open-orders">
          <ListSkeleton v-if="ordersLoading && !openOrders.length" :rows="3" />
          <p v-else-if="!openOrders.length" class="px-3 py-2.5 text-sm text-muted">暂无挂单。顾客到店开单后会挂在这里，做完再结账。</p>
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
              <span class="truncate" :title="orderItemNames(order)">{{ orderItemNames(order) }}</span>
            </div>
          </button>
        </div>
        <div v-if="canCollect && lastReceipt" class="mt-2 flex items-center gap-2 px-1 text-meta text-muted" data-testid="last-receipt">
          <span>最近一次结账 {{ yuanFixed(lastReceipt.total) }}</span>
          <UButton size="xs" color="neutral" variant="ghost" class="ms-auto" @click="receipt = lastReceipt">补打小票</UButton>
        </div>
      </section>

      <div class="min-w-0 lg:col-start-2 lg:row-span-2 lg:row-start-1">
        <CashierOrderPanel
          ref="panel"
          v-model:order-id="selectedOrderID"
          v-model:busy="checkoutSaving"
          :open-orders="openOrders"
          :items="activeItems"
          :staff-items="staffItems"
          :business-date="businessDate"
          :identified-customers="identifiedCustomers"
          :customer-name="customerName"
          :staff-name="staffName"
          :resolve-names="resolveNames"
          :may-change="mayChange"
          @changed="loadOrders"
          @coupons-changed="loadUnlinked"
          @checked-out="onCheckedOut"
        />
      </div>

      <div class="min-w-0 lg:col-start-1 lg:row-start-2">
        <CashierSettled :refresh-key="settledKey" :customer-name="customerName" :resolve-names="resolveNames" :can-void="o => mayChange('order.void', o)" :can-change-staff="o => mayChange('order.edit', o)" :staff-items="staffItems" :staff-name="staffName" @voided="onVoided" />
      </div>
    </div>

    <CashierUnlinkedCoupons
      ref="unlinkedList"
      :business-date="businessDate"
      :open-orders="openOrders"
      :customer-name="customerName"
      @linked="loadOrders"
      @revoked="row => coupon?.forget(row.id)"
    />
    <CashierOpenOrderModal ref="openOrderModal" :items="activeItems" :staff-items="staffItems" :business-date="businessDate" :backfill-max-days="backfillMaxDays" @opened="onOpened" />

    <Receipt v-if="receipt" :receipt="receipt" @close="closeReceipt" />
  </div>
</template>
