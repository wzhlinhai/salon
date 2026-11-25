<script setup lang="ts">
import type { components } from '~/utils/api'

type Order = components['schemas']['Order']
type OrderItem = components['schemas']['OrderItem']
const props = defineProps<{
  refreshKey: number
  customerName: (id: string) => string
  resolveNames: (orders: Order[]) => Promise<void>
  canVoid: (order: Order) => boolean
  canChangeStaff: (order: Order) => boolean
  staffItems: { label: string, value: string }[]
  staffName: (id: string | null | undefined) => string
}>()
const emit = defineEmits<{ voided: [order: Order] }>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const orders = ref<Order[]>([])
const total = ref(0)
const loading = ref(false)
const failed = ref(false)
const listGuard = useRequestGuard()
// 撤单授权窗口没开时 void_order 的拒绝原因，见 backend/internal/orders/void.go。
const NEEDS_PASSWORD = '请先输入登录密码开启撤单授权窗口'

async function load() {
  const current = listGuard.next()
  loading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_orders', { body: {} })
    if (!current()) return
    failed.value = !data
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    orders.value = data.items
    total.value = data.total
    await props.resolveNames(data.items)
  } finally {
    if (current()) loading.value = false
  }
}

watch(() => props.refreshKey, load)

const expanded = ref(false)
// 列表只取了最近一批时不知道全天合计，只写单数。
const summary = computed(() => {
  if (loading.value && !orders.value.length) return '今天结了…'
  const paid = orders.value.filter(order => order.status === 'paid')
  if (failed.value && !orders.value.length) return '取不到今天结的单'
  if (!total.value) return '今天还没结账'
  // total 含撤掉的单，所以这里说「张单」不说「结了」。
  if (total.value > orders.value.length) return `今天 ${total.value} 张单`
  return `今天结了 ${paid.length} 单 · ${yuan(money(paid.reduce((sum, order) => sum + cents(order.net), 0)))}`
})
onMounted(load)

function itemNames(order: Order) {
  return order.items.filter(i => !i.removed_at).map(i => i.item_name).join('、') || '无项目'
}

// 改人历史一行：项目名加每个变了的岗位「原员工 → 新员工」。
function changeText(order: Order, change: Order['staff_changes'][number]) {
  const item = order.items.find(i => i.id === change.order_item_id)
  const parts = change.after
    .map(a => ({ slot: a.slot, from: change.before.find(b => b.slot === a.slot)?.staff_id ?? null, to: a.staff_id ?? null }))
    .filter(p => p.from !== p.to)
    .map(p => `岗位${p.slot} ${props.staffName(p.from)} → ${props.staffName(p.to)}`)
  return `${item?.item_name ?? '项目'} ${parts.join('，')}`
}

const NONE = '__none__'
const staffForm = useModalForm()
const { open: staffOpen, saving: staffSaving } = staffForm
const staffTarget = ref<Order | null>(null)
// 每个项目行每个岗位当前选的员工（'' 表示未填），键是项目行 id。
const draft = ref<Record<string, Record<number, string>>>({})

function activeItems(order: Order) {
  return order.items.filter(i => !i.removed_at)
}

function sortedPositions(item: OrderItem) {
  return [...item.positions].sort((a, b) => a.slot - b.slot)
}

function editStaff(order: Order) {
  staffForm.begin(() => {
    staffTarget.value = order
    draft.value = Object.fromEntries(activeItems(order).map(i => [i.id, Object.fromEntries(i.positions.map(p => [p.slot, p.staff_id ?? '']))]))
  })
}

function changedItems(order: Order) {
  return activeItems(order).filter(i => i.positions.some(p => (draft.value[i.id]?.[p.slot] ?? '') !== (p.staff_id ?? '')))
}

const missingRequired = computed(() => {
  const order = staffTarget.value
  if (!order) return false
  return activeItems(order).some(i => i.positions.some(p => p.required && !draft.value[i.id]?.[p.slot]))
})

async function submitStaff() {
  const order = staffTarget.value
  if (!order) return
  const items = changedItems(order)
  if (!items.length) {
    staffOpen.value = false
    return
  }
  await staffForm.save(async (current) => {
    let changed = false
    for (const item of items) {
      const staff = item.positions
        .map(p => ({ slot: p.slot, staff_id: draft.value[item.id]?.[p.slot] ?? '' }))
        .filter(p => p.staff_id)
      const { data, error } = await api.POST('/api/ops/set_paid_item_staff', { body: { order_id: order.id, order_item_id: item.id, staff } })
      if (!current()) return
      if (!data) {
        toast.add({ title: `${item.item_name}：${errorText(error)}`, color: 'error' })
        // 前面已改成功的项目刷新出来，弹窗留着让用户看清哪一项没改成。
        if (changed) await load()
        return
      }
      changed = true
    }
    staffOpen.value = false
    toast.add({ title: '已改做的人，业绩和提成已改记', color: 'success' })
    await load()
  })
}

// 收款记录弹窗：结过账、撤过单的都能看。
const collectionsOpen = ref(false)
const collectionsOrder = ref<Order | null>(null)

function showCollections(order: Order) {
  collectionsOrder.value = order
  collectionsOpen.value = true
}

const voidForm = useModalForm()
const { open: voidOpen, saving: voiding } = voidForm
const target = ref<Order | null>(null)
const reason = ref('')
const password = ref('')
const needPassword = ref(false)

function editVoid(order: Order) {
  voidForm.begin(() => {
    target.value = order
    reason.value = ''
    password.value = ''
    needPassword.value = false
  })
}

async function submitVoid() {
  const order = target.value
  if (!order) return
  const body = { order_id: order.id, reason: reason.value.trim() }
  await voidForm.save(async (current) => {
    if (needPassword.value) {
      const opened = await api.POST('/api/ops/open_void_window', { body: { password: password.value } })
      if (!current()) return
      if (!opened.data) {
        toast.add({ title: errorText(opened.error), color: 'error' })
        return
      }
      // 窗口已开：这次撤单若被别的规则拒绝，再提交不必重开窗口、再验一次密码。
      needPassword.value = false
      password.value = ''
    }
    const { data, error } = await api.POST('/api/ops/void_order', { body })
    if (!current()) return
    if (!data) {
      const detail = errorText(error)
      if (detail === NEEDS_PASSWORD && !needPassword.value) {
        needPassword.value = true
        return
      }
      toast.add({ title: detail, color: 'error' })
      return
    }
    voidOpen.value = false
    toast.add({ title: '已撤单，卡余额、次数和红包已退回', color: 'success' })
    emit('voided', data)
    await load()
  })
}
</script>

<template>
  <section data-testid="settled-orders">
    <!-- 收成一行（样稿 02）：结了几单、收了多少；点开才列明细，撤单、改做的人、收款记录都在明细里。 -->
    <button
      type="button"
      class="flex min-h-11 w-full items-center gap-2 rounded-md px-2 text-left text-sm text-muted hover:bg-hover"
      :aria-expanded="expanded"
      aria-controls="settled-list"
      data-testid="settled-toggle"
      @click="expanded = !expanded"
    >
      <span class="tabular-nums">{{ summary }}</span>
      <UIcon name="i-lucide-chevron-right" class="ms-auto size-4 transition-transform" :class="expanded && 'rotate-90'" aria-hidden="true" />
    </button>
    <div v-show="expanded" id="settled-list" class="mt-1 border-y border-default">
      <ListSkeleton v-if="loading && !orders.length" :rows="3" />
      <p v-else-if="!orders.length" class="px-3 py-2 text-sm text-muted">今天还没有结账的单。结过账的单会列在这里，结错了可以撤单。</p>
      <div
        v-for="order in orders"
        :key="order.id"
        :data-settled-order="order.id"
        class="group/row border-t border-default px-3 py-2 first:border-t-0"
        :class="order.status === 'voided' ? 'text-muted' : ''"
      >
        <div class="flex items-center justify-between gap-2 text-sm">
          <span class="truncate font-medium" :title="customerName(order.customer_id)">{{ customerName(order.customer_id) }}</span>
          <span class="font-medium tabular-nums" :class="order.status === 'voided' ? 'line-through' : ''">{{ yuanFixed(order.net) }}</span>
        </div>
        <div class="mt-0.5 flex items-center gap-2 text-meta text-muted">
          <span v-if="order.status === 'voided'" class="tag bg-tag-gray">已撤</span>
          <span class="me-auto truncate" :title="itemNames(order)">{{ itemNames(order) }}</span>
          <UButton
            v-if="can('payment.collect')"
            size="sm"
            color="neutral"
            variant="ghost"
            class="-my-2 opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100"
            :aria-label="`收款记录 ${customerName(order.customer_id)} ${yuanFixed(order.net)}`"
            @click="showCollections(order)"
          >
            收款记录
          </UButton>
          <UButton
            v-if="order.status === 'paid' && canChangeStaff(order)"
            size="sm"
            color="neutral"
            variant="ghost"
            class="-my-2 opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100"
            :aria-label="`改做的人 ${customerName(order.customer_id)} ${yuanFixed(order.net)}`"
            @click="editStaff(order)"
          >
            改做的人
          </UButton>
          <UButton
            v-if="order.status === 'paid' && canVoid(order)"
            size="sm"
            color="error"
            variant="ghost"
            class="-my-2 opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100"
            :aria-label="`撤单 ${customerName(order.customer_id)} ${yuanFixed(order.net)}`"
            @click="editVoid(order)"
          >
            撤单
          </UButton>
        </div>
        <p v-if="order.void" class="mt-0.5 text-meta text-muted">撤单原因：{{ order.void.reason }}</p>
        <p v-for="change in order.staff_changes" :key="change.id" class="mt-0.5 truncate text-meta text-muted" :title="changeText(order, change)">
          改人：{{ changeText(order, change) }}
        </p>
      </div>
      <p v-if="total > orders.length" class="border-t border-default px-3 py-2 text-meta text-dimmed">只显示最近 {{ orders.length }} 张</p>
    </div>
  </section>
  <CashierCollections
    v-model:open="collectionsOpen"
    :order-id="collectionsOrder?.id ?? null"
    :summary="collectionsOrder ? `${customerName(collectionsOrder.customer_id)} · ${itemNames(collectionsOrder)} · 实收 ${yuanFixed(collectionsOrder.net)}` : ''"
  />
  <UModal v-model:open="staffOpen" title="改做的人" description="钱不变。原员工这一行的业绩和客数在今天冲回，改记给新员工，提成跟着重算。">
    <template #body>
      <div v-if="staffTarget" class="space-y-5">
        <p class="rounded-card bg-side px-3 py-2 text-sm">
          {{ customerName(staffTarget.customer_id) }} · {{ staffTarget.business_date }} · 实收 {{ yuanFixed(staffTarget.net) }}
        </p>
        <div v-for="item in activeItems(staffTarget)" :key="item.id" class="space-y-2">
          <p class="text-sm font-medium">{{ item.item_name }}</p>
          <div v-for="position in sortedPositions(item)" :key="position.slot" class="flex items-center gap-2">
            <span class="w-12 text-meta text-muted">岗位 {{ position.slot }}</span>
            <USelect
              :model-value="draft[item.id]?.[position.slot] || NONE"
              :items="[...(position.required ? [] : [{ label: '未填', value: NONE }]), ...staffItems]"
              size="sm"
              class="w-48"
              :aria-label="`${item.item_name} 岗位${position.slot}员工`"
              @update:model-value="value => { draft[item.id]![position.slot] = value === NONE ? '' : String(value) }"
            />
            <span v-if="position.required" class="tag bg-tag-yellow">必填</span>
          </div>
        </div>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="staffOpen = false">
        <UButton :loading="staffSaving" :disabled="missingRequired || !staffTarget || !changedItems(staffTarget).length" @click="submitStaff">保存</UButton>
      </ModalActions>
    </template>
  </UModal>
  <UModal v-model:open="voidOpen" title="撤单" description="撤单后这张单的收款、业绩全部作废，卡余额、次数和红包退回。">
    <template #body>
      <div v-if="target" class="space-y-5">
        <p class="rounded-card bg-side px-3 py-2 text-sm">
          {{ customerName(target.customer_id) }} · {{ itemNames(target) }} · 实收 {{ yuanFixed(target.net) }}
        </p>
        <UFormField label="撤单原因" required><UTextarea v-model="reason" :maxlength="200" placeholder="例如：项目开错了" /></UFormField>
        <UFormField v-if="needPassword" label="登录密码" required help="撤单要先用你的登录密码开启授权，授权期间再撤单不用重复输入。输错次数和登录共用，错太多会暂时锁定登录">
          <UInput v-model="password" type="password" autocomplete="current-password" class="w-full" />
        </UFormField>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="voidOpen = false">
        <UButton color="error" :loading="voiding" :disabled="!reason.trim() || (needPassword && !password)" @click="submitVoid">确认撤单</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
