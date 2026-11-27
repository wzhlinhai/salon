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
// 在做列表每一行：顾客小卡片贴着整行右边弹，不盖住这一行的名字。
const rowEls = shallowReactive<Record<string, HTMLElement | null>>({})
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
// 营业日等项目、员工一起读到了才填（填了才认网址里的开单请求），设置值随读随用。
const businessDate = ref('')
const storeToday = useStoreToday()
const items = ref<Item[]>([])
const staffOptions = ref<StaffOption[]>([])
const initGuard = useRequestGuard()
const backfillMaxDays = computed(() => Number(storeToday.value('backfill_max_days')))
const staffItems = computed(() => staffOptions.value.map(s => ({ label: s.name, value: s.id })))
const activeItems = computed(() => items.value.filter(i => i.active))

// 读失败原地给「重试」：不然页面空着，在做列表写「还没有在做的单」，前台以为没人在做。
const initError = ref('')
const ordersError = ref('')

async function init() {
  const current = initGuard.next()
  initError.value = ''
  const [s, i, st] = await Promise.all([
    storeToday.load(),
    api.POST('/api/ops/list_items', { body: {} }),
    api.POST('/api/ops/list_staff_options', { body: {} })
  ])
  if (!current()) return
  if (!s || !i.data || !st.data) {
    initError.value = s ? errorText(i.error ?? st.error) : storeToday.error.value
    return
  }
  initError.value = ''
  businessDate.value = s.business_date
  items.value = i.data.items
  staffOptions.value = st.data.staff
  await loadOrders()
  await nextTick()
  await loadUnlinked()
  if (!await openFromQuery()) focusScan()
}

// 网址带 ?open= 时直接开单：walkin 是散客；customer 是从顾客档案或全局搜索来，为这位顾客开单，「照这个开单」还带着上次的项目。
// 已经在收银页时从全局搜索再来一次，页面不重新挂载，所以也盯着网址。开了单返回 true。
async function openFromQuery() {
  const current = initGuard.hold()
  if (route.query.open === 'walkin') {
    // 去掉参数，刷新或后退回来不会再弹一次开单。
    await router.replace({ query: { ...route.query, open: undefined } })
    openOrderBegin(null)
    return true
  }
  if (route.query.open === 'customer' && typeof route.query.customer === 'string') {
    // 手上这张还没开完时不盖掉它，说一声。
    if (openOrderModal.value?.open) {
      await router.replace({ query: { ...route.query, open: undefined, customer: undefined, items: undefined } })
      toast.add({ title: '手上这张还没开完', description: '先开好或取消，再给下一位开', color: 'warning' })
      return true
    }
    const customerID = route.query.customer
    const itemIDs = typeof route.query.items === 'string' ? route.query.items.split(',').filter(Boolean) : []
    await router.replace({ query: { ...route.query, open: undefined, customer: undefined, items: undefined } })
    const res = await api.POST('/api/ops/get_customer', { body: { id: customerID } })
    if (!current()) return true
    if (!res.data) {
      toast.add({ title: errorText(res.error), color: 'error' })
      return true
    }
    openOrderModal.value?.begin(res.data, null, null, itemIDs)
    return true
  }
  return false
}

watch(() => route.query.open, (open) => {
  if (open && businessDate.value) void openFromQuery()
})

// —— 挂单列表 ——
const openOrders = ref<Order[]>([])
const ordersLoading = ref(false)
const ordersGuard = useRequestGuard()
const selectedOrderID = ref('')
const checkoutSaving = ref(false)
// 顾客名在名册里批量取（没有 customer.view 就不取，只写「顾客」）。
const roster = useCustomerRoster()
// 已识别顾客才能挂账，和结账规则一致；要靠名册得知，所以没有 customer.view 时不显示挂账。
const identifiedCustomers = computed(() => Object.fromEntries(Object.entries(roster.entries.value).map(([id, entry]) => [id, entry.identified])))

function customerName(id: string) {
  return roster.label(id, '顾客')
}

// 在做列表的第二行：项目 · 做的人（去重，没填的不写）。
function orderLine(order: Order) {
  const staff = [...new Set(order.items.filter(i => !i.removed_at).flatMap(i => i.positions.map(p => p.staff_id)).filter(Boolean).map(id => staffName(id)))]
  return [orderItemNames(order), ...staff].join(' · ')
}

function staffName(id: string | null | undefined) {
  return staffOptions.value.find(s => s.id === id)?.name || '未填'
}

async function resolveNames(orders: Order[]) {
  await roster.resolve(orders.map(o => o.customer_id))
}

async function loadOrders() {
  const current = ordersGuard.next()
  ordersLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_open_orders', { body: {} })
    if (!current()) return
    if (!data) {
      if (openOrders.value.length) toast.add({ title: errorText(error), color: 'error' })
      else ordersError.value = errorText(error)
      return
    }
    ordersError.value = ''
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
// 框清空了（选好顾客、开好单，或手动删掉），「识别为…」也跟着去掉。
watch(scanValue, (value) => {
  if (!value.trim()) scanKind.value = ''
})
const scanGuard = useRequestGuard()
const scanLoading = ref(false)
const customerMatches = ref<Customer[]>([])
const pickedCustomer = ref<Customer | null>(null)
const platform = ref<Platform>('meituan')
// 扫码框旁的下拉单看「美团」不知道在选什么，写成「美团券」。
const couponPlatformItems = platformItems.map(p => ({ ...p, label: `${p.label}券` }))

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
      <!-- 没选单时「开单」在右栏空白小票正中；选着一张单时主角是小票上的「结账」，「开单」才回到顶栏、退成描边。 -->
      <UButton v-if="selectedOrderID" color="neutral" variant="outline" @click="openOrderBegin(pickedCustomer)">开单</UButton>
    </PageActions>

    <LoadError v-if="initError" title="收银台没有读出来" :hint="initError" @retry="init()" />

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
        <USelect v-if="canGroupbuy" v-model="platform" :items="couponPlatformItems" size="lg" class="w-32" aria-label="券码平台" :disabled="coupon?.locked" />
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
        <UButton size="sm" color="neutral" variant="outline" @click="openOrderBegin(pickedCustomer)">为这位顾客开单</UButton>
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
        <h2 class="sec">在做<span class="ms-1 font-normal text-dimmed">{{ openOrders.length }}</span></h2>
        <div class="space-y-1" data-testid="open-orders">
          <ListSkeleton v-if="ordersLoading && !openOrders.length" :rows="3" />
          <LoadError v-else-if="ordersError && !openOrders.length" title="在做的单没有读出来" :hint="ordersError" @retry="loadOrders()" />
          <p v-else-if="!openOrders.length && !initError" class="py-2 text-sm text-muted">还没有在做的单</p>
          <!-- 一行一位：头像字点开顾客小卡片（样稿 26），其余整行点了选这张单；两个按钮并排，不嵌套。 -->
          <div
            v-for="order in openOrders"
            :key="order.id"
            :ref="el => { rowEls[order.id] = el as HTMLElement | null }"
            class="flex items-center gap-1 rounded-md ps-2"
            :class="order.id === selectedOrderID ? 'bg-selected' : 'hover:bg-hover'"
          >
            <CustomerPeek v-if="canCustomer && identifiedCustomers[order.customer_id]" :customer-id="order.customer_id" :reference="rowEls[order.id]">
              <button type="button" class="grid size-7 shrink-0 place-items-center rounded-full bg-tag-gray text-xs hover:ring-1 hover:ring-(--ui-border-accented)" :aria-label="`看看${customerName(order.customer_id)}`">
                {{ customerName(order.customer_id).slice(0, 1) }}
              </button>
            </CustomerPeek>
            <span v-else class="grid size-7 shrink-0 place-items-center rounded-full bg-tag-gray text-xs" aria-hidden="true">{{ identifiedCustomers[order.customer_id] === false ? '未' : customerName(order.customer_id).slice(0, 1) }}</span>
            <button
              type="button"
              class="flex min-h-14 min-w-0 flex-1 items-center gap-2 py-2 pe-3 ps-1 text-left"
              :aria-current="order.id === selectedOrderID ? 'true' : undefined"
              :data-order-id="order.id"
              data-testid="open-order"
              :disabled="checkoutSaving"
              @click="selectOrder(order)"
            >
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm" :title="customerName(order.customer_id)">{{ customerName(order.customer_id) }}</span>
                <span class="mt-0.5 flex items-center gap-2 text-meta text-muted">
                  <span v-if="order.backfill" data-testid="backfill-badge" class="tag bg-tag-yellow">补录 {{ order.business_date }}</span>
                  <span class="truncate" :title="orderItemNames(order)">{{ orderLine(order) }}</span>
                </span>
              </span>
              <span class="shrink-0 text-right">
                <span class="block text-sm tabular-nums">{{ yuan(order.receivable) }}</span>
                <span class="mt-0.5 block text-xs text-muted tabular-nums">{{ clockTime(order.opened_at) }} 开单</span>
              </span>
            </button>
          </div>
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
          @open="openOrderBegin(pickedCustomer)"
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
