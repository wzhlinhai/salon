<script setup lang="ts">
import type { components } from '~/utils/api'

type Order = components['schemas']['Order']
const props = defineProps<{
  refreshKey: number
  customerName: (id: string) => string
  resolveNames: (orders: Order[]) => Promise<void>
  canVoid: (order: Order) => boolean
}>()
const emit = defineEmits<{ voided: [order: Order] }>()
const api = useApi()
const toast = useToast()
const orders = ref<Order[]>([])
const total = ref(0)
const loading = ref(false)
const listGuard = useRequestGuard()
// 撤单授权窗口没开时 void_order 的拒绝原因，见 backend/internal/orders/void.go。
const NEEDS_PASSWORD = '请先输入登录密码开启撤单授权窗口'

async function load() {
  const current = listGuard.next()
  loading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_orders', { body: {} })
    if (!current()) return
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
onMounted(load)

function itemNames(order: Order) {
  return order.items.filter(i => !i.removed_at).map(i => i.item_name).join('、') || '无项目'
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
    <h2 class="sec">今日结账<span class="ms-1 font-normal text-dimmed">{{ total }}</span></h2>
    <div class="box overflow-hidden p-0">
      <ListSkeleton v-if="loading && !orders.length" :rows="3" />
      <EmptyState v-else-if="!orders.length" title="今天还没有结账的单" hint="结过账的单会列在这里，结错了可以撤单" />
      <div
        v-for="order in orders"
        :key="order.id"
        :data-settled-order="order.id"
        class="group/row border-t border-default px-3 py-2.5 first:border-t-0"
        :class="order.status === 'voided' ? 'text-muted' : ''"
      >
        <div class="flex items-center justify-between gap-2 text-sm">
          <span class="truncate font-medium" :title="customerName(order.customer_id)">{{ customerName(order.customer_id) }}</span>
          <span class="font-medium tabular-nums" :class="order.status === 'voided' ? 'line-through' : ''">{{ yuanFixed(order.net) }}</span>
        </div>
        <div class="mt-0.5 flex items-center gap-1.5 text-meta text-muted">
          <span v-if="order.status === 'voided'" class="tag bg-tag-gray">已撤</span>
          <span class="truncate" :title="itemNames(order)">{{ itemNames(order) }}</span>
          <UButton
            v-if="order.status === 'paid' && canVoid(order)"
            size="sm"
            color="error"
            variant="ghost"
            class="ms-auto -my-1.5 opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100"
            :aria-label="`撤单 ${customerName(order.customer_id)} ${yuanFixed(order.net)}`"
            @click="editVoid(order)"
          >
            撤单
          </UButton>
        </div>
        <p v-if="order.void" class="mt-0.5 text-meta text-muted">撤单原因：{{ order.void.reason }}</p>
      </div>
      <p v-if="total > orders.length" class="border-t border-default px-3 py-2 text-meta text-dimmed">只显示最近 {{ orders.length }} 张</p>
    </div>
  </section>
  <UModal v-model:open="voidOpen" title="撤单" description="撤单后这张单的收款、业绩全部作废，卡余额、次数和红包退回。">
    <template #body>
      <div v-if="target" class="space-y-5">
        <p class="rounded-lg bg-side px-3 py-2.5 text-sm">
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
