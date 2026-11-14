<!-- eslint-disable vue/multi-word-component-names -->
<script setup lang="ts">
import type { components } from '~/utils/api'

type Receipt = components['schemas']['Receipt']
const props = defineProps<{ receipt: Receipt }>()
const emit = defineEmits<{ close: [] }>()
const { receipt } = toRefs(props)

const methodLabels: Record<string, string> = {
  cash: '现金',
  unionpay: '银联',
  wechat: '微信',
  alipay: '支付宝',
  groupbuy: '团购',
  debt: '挂账',
  card: '卡余额',
  count: '扣次',
  red_packet: '红包'
}

const width = computed(() => receipt.value.paper_width === '58mm' ? '58mm' : '80mm')
const printStyle = computed(() => `
@page { size: ${width.value} auto; margin: 3mm; }
@media print {
  body > *:not(.receipt-print-root) { display: none !important; }
  body { width: ${width.value}; margin: 0; background: white; }
  .receipt-print-root { position: static !important; display: block !important; padding: 0 !important; background: white !important; overflow: visible !important; }
  .receipt-paper { width: ${width.value} !important; margin: 0 !important; box-shadow: none !important; }
  .no-print { display: none !important; }
}
`)

useHead(() => ({
  style: [{ key: 'receipt-print', textContent: printStyle.value }]
}))

function timeText(iso: string) {
  return new Date(iso).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
}

function print() {
  window.print()
}
</script>

<template>
  <Teleport to="body">
    <div class="receipt-print-root fixed inset-0 z-50 overflow-auto bg-black/40 p-4" data-testid="receipt">
      <div class="receipt-paper mx-auto bg-white text-black shadow-lg" :style="{ width }" data-testid="receipt-paper">
        <div class="no-print flex items-center justify-between gap-2 border-b border-gray-200 px-3 py-2" :style="{ width }">
          <span class="text-sm font-medium">小票预览</span>
          <div class="flex gap-2">
            <UButton size="sm" @click="print">打印</UButton>
            <UButton size="sm" variant="ghost" @click="emit('close')">关闭</UButton>
          </div>
        </div>
        <div class="receipt-content px-3 py-4 font-mono text-xs leading-5">
          <div v-if="receipt.backfill" data-testid="receipt-backfill" class="mb-2 text-center text-base font-bold">补录</div>
          <div class="text-center text-base font-semibold" data-testid="receipt-header">{{ receipt.header || receipt.store_name }}</div>
          <div class="mt-1 text-center">营业日 {{ receipt.business_date }}</div>
          <div class="text-center">结账时间 {{ timeText(receipt.checked_out_at) }}</div>
          <div class="my-2 border-t border-dashed border-black" />
          <div v-for="order in receipt.orders" :key="order.id" class="mb-3" data-testid="receipt-order">
            <div v-if="order.backfill" class="text-center font-bold">补录</div>
            <div v-if="receipt.show_customer_name && (order.customer_name || receipt.customer_name)" data-testid="receipt-customer">
              顾客 {{ order.customer_name || receipt.customer_name }}
            </div>
            <div v-for="(line, index) in order.items" :key="index" class="flex justify-between gap-2">
              <span>{{ line.item_name }}</span>
              <span>{{ line.net }}</span>
            </div>
            <div v-if="receipt.show_staff" data-testid="receipt-staff" class="mt-1 space-y-0.5">
              <div v-for="(line, index) in order.items.filter(x => x.staff_names.length)" :key="index">
                {{ line.item_name }}：{{ line.staff_names.join('、') }}
              </div>
            </div>
            <div v-if="receipt.show_payments" data-testid="receipt-payments" class="mt-1 border-t border-dashed border-black pt-1">
              <div v-for="(payment, index) in order.payments" :key="index" class="flex justify-between gap-2">
                <span>{{ methodLabels[payment.method] ?? payment.method }}{{ payment.store_price ? `（门店标价 ${payment.store_price}）` : '' }}</span>
                <span>{{ payment.amount }}</span>
              </div>
            </div>
            <div class="mt-1 flex justify-between border-t border-dashed border-black pt-1">
              <span>单号 {{ order.id.slice(0, 8) }}</span>
              <span>小计 {{ order.net }}</span>
            </div>
          </div>
          <div v-if="receipt.show_card_balance && receipt.cards.length" data-testid="receipt-cards" class="my-2 border-t border-dashed border-black pt-1">
            <div v-for="card in receipt.cards" :key="card.card_id" class="mb-1">
              <div>卡 {{ card.card_id.slice(0, 8) }}</div>
              <div class="flex justify-between"><span>本金</span><span>{{ card.principal }}</span></div>
              <div class="flex justify-between"><span>赠送金</span><span>{{ card.bonus }}</span></div>
              <div v-for="count in card.counts" :key="count.item_id" class="flex justify-between"><span>{{ count.name }}剩余</span><span>{{ count.paid }} 次 / 赠 {{ count.gift }} 次</span></div>
            </div>
          </div>
          <div class="my-2 border-t border-dashed border-black" />
          <div class="flex justify-between text-sm font-semibold"><span>合计</span><span data-testid="receipt-total">{{ receipt.total }}</span></div>
          <div class="mt-2 text-center" data-testid="receipt-footer">{{ receipt.footer }}</div>
        </div>
      </div>
    </div>
  </Teleport>
</template>
