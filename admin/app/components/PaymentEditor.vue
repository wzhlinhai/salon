<script setup lang="ts">
interface Payment { method: string, amount: string }
const props = defineProps<{
  expected: string
  methods?: { label: string, value: string }[]
  resetKey?: number
  // 收银台用：方式按钮和金额框都放大到 44 高，应收已经在上面用大字写了，这里不再重复。
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

function defaults(expected: string): Payment[] {
  let left = cents(expected)
  const out: Payment[] = []
  while (left > 0) {
    const part = Math.min(left, 9_999_999_999)
    out.push({ method: 'cash', amount: money(part) })
    left -= part
  }
  return out
}

watch([() => props.expected, touched, () => props.resetKey], ([value, custom]) => {
  if (!custom) payments.value = defaults(value)
}, { immediate: true })

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
  payments.value = payments.value.filter((_, i) => i !== index)
}
</script>

<template>
  <div class="space-y-2" data-testid="sale-payments">
    <div v-if="!counter" class="flex items-baseline justify-between">
      <p class="text-sm font-semibold text-highlighted">收款方式</p>
      <p class="text-meta text-muted tabular-nums">应收合计 {{ yuanFixed(expected) }}</p>
    </div>
    <div v-for="(payment, index) in payments" :key="index" class="flex flex-wrap items-center gap-2" :class="index ? 'border-t border-default pt-2' : ''" data-testid="payment-row">
      <MethodPicker v-model="payment.method" :items="methods" :counter="counter" @pick="change" />
      <UInput v-model="payment.amount" aria-label="金额" inputmode="decimal" :size="counter ? 'lg' : 'md'" class="min-w-32 flex-1 tabular-nums" @update:model-value="change">
        <template #leading><span class="text-meta text-muted">¥</span></template>
      </UInput>
      <UButton color="neutral" variant="ghost" icon="i-lucide-x" aria-label="删除" @click="remove(index)" />
    </div>
    <UButton color="neutral" variant="ghost" icon="i-lucide-plus" class="-ms-2" @click="add">添加收款方式</UButton>
    <p v-if="total !== cents(expected)" class="text-meta text-ink-orange">已录入 {{ yuanFixed(money(total)) }}，{{ total < cents(expected) ? '还应录入' : '多录入' }} {{ yuanFixed(money(Math.abs(cents(expected) - total))) }}</p>
  </div>
</template>
