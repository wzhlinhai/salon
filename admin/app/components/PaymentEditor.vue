<script setup lang="ts">
interface Payment { method: string, amount: string }
const props = defineProps<{
  expected: string
  methods?: { label: string, value: string }[]
  resetKey?: number
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

function pick(payment: Payment, method: string) {
  if (payment.method === method) return
  payment.method = method
  change()
}

// 单选组的键盘约定：整组只占一个 Tab 位，方向键在方式间移动并选中。
function move(event: KeyboardEvent, payment: Payment, step: number) {
  const list = methods.value
  const index = list.findIndex(m => m.value === payment.method)
  const next = list[(index + step + list.length) % list.length]
  if (!next) return
  pick(payment, next.value)
  const group = (event.currentTarget as HTMLElement).parentElement
  void nextTick(() => group?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus())
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
    <div class="flex items-center justify-between text-meta text-muted">
      <p>收款方式</p>
      <p class="tabular-nums">应收合计 {{ yuanFixed(expected) }}</p>
    </div>
    <!-- 收款方式一键选（DESIGN.md 表单）：方式不多，平铺成一排按钮，不用下拉。 -->
    <div v-for="(payment, index) in payments" :key="index" class="flex flex-wrap items-center gap-2" :class="index ? 'border-t border-default pt-2' : ''" data-testid="payment-row">
      <div role="radiogroup" aria-label="方式" class="flex flex-wrap gap-1.5">
        <UButton
          v-for="m in methods"
          :key="m.value"
          role="radio"
          :aria-checked="payment.method === m.value"
          :tabindex="payment.method === m.value || !methods.some(x => x.value === payment.method) ? 0 : -1"
          size="sm"
          :color="payment.method === m.value ? 'primary' : 'neutral'"
          :variant="payment.method === m.value ? 'soft' : 'outline'"
          :class="payment.method === m.value ? 'font-medium ring-1 ring-primary/40 ring-inset' : ''"
          @click="pick(payment, m.value)"
          @keydown.left.prevent="move($event, payment, -1)"
          @keydown.right.prevent="move($event, payment, 1)"
          @keydown.up.prevent="move($event, payment, -1)"
          @keydown.down.prevent="move($event, payment, 1)"
        >
          {{ m.label }}
        </UButton>
      </div>
      <UInput v-model="payment.amount" aria-label="金额" inputmode="decimal" class="min-w-32 flex-1 tabular-nums" @update:model-value="change">
        <template #leading><span class="text-meta text-muted">¥</span></template>
      </UInput>
      <UButton color="neutral" variant="ghost" icon="i-lucide-x" aria-label="删除" @click="remove(index)" />
    </div>
    <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-plus" @click="add">添加收款方式</UButton>
    <p v-if="total !== cents(expected)" class="text-meta text-ink-orange">已录入 {{ yuanFixed(money(total)) }}，{{ total < cents(expected) ? '还应录入' : '多录入' }} {{ yuanFixed(money(Math.abs(cents(expected) - total))) }}</p>
  </div>
</template>
