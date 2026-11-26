<script setup lang="ts">
interface Payment { method: string, amount: string }
const props = defineProps<{
  expected: string
  methods?: { label: string, value: string }[]
  resetKey?: number
  // 收银台用：方式按钮和金额框都放大到 44 高，应收已经在上面用大字写了，这里不再重复；平时收起金额框（见下面 collapsed）。
  counter?: boolean
}>()
const payments = defineModel<Payment[]>({ default: () => [] })
const touched = defineModel<boolean>('touched', { default: false })
const defaultMethods = [{ label: '现金', value: 'cash' }, { label: '银联', value: 'unionpay' }, { label: '微信', value: 'wechat' }, { label: '支付宝', value: 'alipay' }]
const methods = computed(() => props.methods ?? defaultMethods)
const total = computed(() => payments.value.reduce((sum, p) => sum + cents(p.amount), 0))

function cents(value: string) {
  const n = Number(value || '0')
  return Number.isFinite(n) ? Math.round(n * 100) : 0
}

function money(value: number) {
  return `${Math.trunc(value / 100)}.${String(Math.abs(value % 100)).padStart(2, '0')}`
}

// 收银台平时只露一排付款方式（绝大多数单是一种方式付清）：点哪种就按应收全额收，金额框收起来；
// 点「分开付」才展开成一行一种方式、各填金额。换单、改项目时收银台先清掉试算，本组件重建，回到收起、现金；
// 同一张单重新试算（换计价卡，resetKey 变了）只收起，不动已经点好的付款方式。
const splitting = ref(false)
const single = ref('cash')
const collapsed = computed(() => props.counter && !splitting.value && payments.value.length <= 1)

function defaults(expected: string): Payment[] {
  let left = cents(expected)
  const out: Payment[] = []
  while (left > 0) {
    const part = Math.min(left, 9_999_999_999)
    out.push({ method: single.value, amount: money(part) })
    left -= part
  }
  return out
}

watch([() => props.expected, touched, () => props.resetKey], ([value, custom, key], old) => {
  if (old && key !== old[2]) splitting.value = false
  if (!custom) payments.value = defaults(value)
}, { immediate: true })

// 点好的方式不能用了（顾客没认出来，卡余额、挂账不出现），退回现金，免得按钮一个没选、钱却记在看不见的方式上。
watch(methods, (list) => {
  if (list.some(m => m.value === single.value)) return
  single.value = 'cash'
  if (!touched.value) payments.value = defaults(props.expected)
})

function pickSingle(method: string) {
  single.value = method
  if (!touched.value) payments.value = defaults(props.expected)
}

function split() {
  splitting.value = true
  add()
}

function change() {
  touched.value = true
}

function add() {
  touched.value = true
  const left = cents(props.expected) - total.value
  payments.value = [...payments.value, { method: 'cash', amount: left > 0 ? money(left) : '0.00' }]
}

function remove(index: number) {
  touched.value = true
  // payments、touched 是父组件的 v-model，赋值后要等父组件更新才读得到，这里用本地的 next 判断。
  const next = payments.value.filter((_, i) => i !== index)
  payments.value = next
  // 分开付删到只剩一行：收回一排，按剩下那种方式收全额。
  const last = next[0]
  if (props.counter && splitting.value && next.length === 1 && last) {
    splitting.value = false
    touched.value = false
    single.value = last.method
    payments.value = defaults(props.expected)
  }
}
</script>

<template>
  <div class="space-y-2" data-testid="sale-payments">
    <div v-if="!counter" class="flex items-baseline justify-between">
      <p class="text-sm font-semibold text-highlighted">收款方式</p>
      <p class="text-meta text-muted tabular-nums">应收合计 {{ yuanFixed(expected) }}</p>
    </div>
    <div v-if="collapsed" data-testid="payment-single">
      <MethodPicker v-if="cents(expected) > 0" v-model="single" :items="methods" counter @pick="pickSingle" />
    </div>
    <template v-else>
      <div v-for="(payment, index) in payments" :key="index" class="flex flex-wrap items-center gap-2" :class="index ? 'border-t border-default pt-2' : ''" data-testid="payment-row">
        <MethodPicker v-model="payment.method" :items="methods" :counter="counter" @pick="change" />
        <UInput v-model="payment.amount" aria-label="金额" inputmode="decimal" :size="counter ? 'lg' : 'md'" class="min-w-32 flex-1 tabular-nums" @update:model-value="change">
          <template #leading><span class="text-meta text-muted">¥</span></template>
        </UInput>
        <UButton v-if="!counter || payments.length > 1" color="neutral" variant="ghost" icon="i-lucide-x" aria-label="删除" @click="remove(index)" />
      </div>
      <UButton color="neutral" variant="ghost" class="-ms-2" @click="add">添加收款方式</UButton>
    </template>
    <!-- 付没付清一句话（样稿 19）：正好付清写绿勾，差多少、多了多少写橙字。 -->
    <!-- 「分开付」跟在付清那句话的行尾：平时一排付款方式就够，一单分两种付时才点开。 -->
    <div class="flex items-center justify-between gap-2">
      <p v-if="total === cents(expected)" class="text-meta text-ink-green" data-testid="payment-status">✓ 正好付清</p>
      <p v-else class="text-meta text-ink-orange tabular-nums" data-testid="payment-status">已录入 {{ yuanFixed(money(total)) }}，{{ total < cents(expected) ? '还差' : '多了' }} {{ yuanFixed(money(Math.abs(cents(expected) - total))) }}</p>
      <UButton v-if="collapsed && cents(expected) > 0" color="neutral" variant="ghost" size="sm" class="-me-2 shrink-0 text-muted" @click="split">分开付</UButton>
    </div>
  </div>
</template>
