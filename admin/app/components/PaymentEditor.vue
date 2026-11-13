<script setup lang="ts">
import type { components } from '~/utils/api'

type Payment = components['schemas']['Payment']
const props = defineProps<{ expected: string }>()
const payments = defineModel<Payment[]>({ default: () => [] })
const touched = defineModel<boolean>('touched', { default: false })
const methods = [{ label: '现金', value: 'cash' }, { label: '银联', value: 'unionpay' }, { label: '微信', value: 'wechat' }, { label: '支付宝', value: 'alipay' }]
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

watch([() => props.expected, touched], ([value, custom]) => {
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
    <div class="flex items-center justify-between">
      <p class="text-sm font-medium">收款方式</p>
      <p class="text-sm text-muted">应收合计 {{ expected }}</p>
    </div>
    <div v-for="(payment, index) in payments" :key="index" class="flex items-end gap-2">
      <UFormField label="方式" class="w-32">
        <USelect v-model="payment.method" :items="methods" class="w-full" @update:model-value="change" />
      </UFormField>
      <UFormField label="金额" class="flex-1">
        <UInput v-model="payment.amount" inputmode="decimal" class="w-full" @update:model-value="change" />
      </UFormField>
      <UButton size="sm" variant="outline" color="error" @click="remove(index)">删除</UButton>
    </div>
    <div class="flex items-center gap-3">
      <UButton size="sm" variant="outline" @click="add">添加收款方式</UButton>
      <p v-if="total !== cents(expected)" class="text-sm text-error">已录入 {{ money(total) }}，还应录入 {{ money(cents(expected) - total) }}</p>
    </div>
  </div>
</template>
