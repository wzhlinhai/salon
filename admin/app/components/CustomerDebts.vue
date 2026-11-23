<script setup lang="ts">
import type { components } from '~/utils/api'

const props = defineProps<{ id: string }>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
type Debt = components['schemas']['Debt']
type Method = components['schemas']['RepayDebtInput']['method']
const debts = ref<Debt[]>([])
const totals = ref<components['schemas']['Totals'] | null>(null)
const loading = ref(false)
const listGuard = useRequestGuard()
const statusNames = { outstanding: '未还清', repaid: '已还清', voided: '已撤单' }
const methodNames: Record<Method, string> = { cash: '现金', unionpay: '银联', wechat: '微信', alipay: '支付宝' }
const methodItems = (Object.keys(methodNames) as Method[]).map(value => ({ label: methodNames[value], value }))

async function load() {
  const current = listGuard.next()
  loading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_customer_debts', { body: { customer_id: props.id } })
    if (!current()) return
    if (data) {
      debts.value = data.items
      totals.value = data.totals
    } else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) loading.value = false
  }
}

const repayForm = useModalForm()
const { open: repayOpen, saving: repaying } = repayForm
const target = ref<Debt | null>(null)
const amount = ref('')
const method = ref<Method>('cash')

function editRepay(debt: Debt) {
  repayForm.begin(() => {
    target.value = debt
    amount.value = debt.outstanding
    method.value = 'cash'
  })
}

async function repay() {
  if (!target.value) return
  const body = { debt_id: target.value.id, amount: amount.value.trim(), method: method.value }
  await repayForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/repay_debt', { body })
    if (!current()) return
    if (data) {
      repayOpen.value = false
      toast.add({ title: `已收还款 ${yuanFixed(body.amount)}`, color: 'success' })
      await load()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

onMounted(load)
</script>

<template>
  <section data-testid="debts">
    <div v-if="totals && Number(totals.outstanding) > 0" class="sec mt-0!">
      <span class="font-normal text-ink-orange tabular-nums">未还 {{ yuanFixed(totals.outstanding) }}</span>
    </div>
    <div class="box overflow-hidden p-0">
      <p v-if="loading && !debts.length" class="px-3 py-2.5 text-sm text-muted">正在读取欠款…</p>
      <p v-else-if="!debts.length" class="px-3 py-2.5 text-sm text-muted">没有欠款</p>
      <div v-for="debt in debts" :key="debt.id" :data-debt="debt.id" class="space-y-1 border-t border-default px-3 py-2.5 text-sm first:border-t-0" :class="debt.status === 'outstanding' ? '' : 'text-muted'">
        <p class="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span class="tag bg-tag-gray">{{ debt.kind === 'order' ? '挂账' : '历史欠款' }}</span>
          <span class="font-medium tabular-nums">{{ yuanFixed(debt.amount) }}</span>
          <span v-if="debt.status === 'outstanding'" class="tabular-nums text-ink-orange">未还 {{ yuanFixed(debt.outstanding) }}</span>
          <span v-else class="tag" :class="debt.status === 'repaid' ? 'bg-tag-green' : 'bg-tag-gray'">{{ statusNames[debt.status] }}</span>
          <span class="ms-auto text-meta text-muted">{{ debt.business_date }}</span>
        </p>
        <p v-if="debt.note" class="text-meta text-muted">{{ debt.note }}</p>
        <p v-for="r in debt.repayments" :key="r.id" class="text-meta text-muted tabular-nums">{{ r.business_date }} 还 {{ yuanFixed(r.amount) }} · {{ methodNames[r.method] }}</p>
        <UButton v-if="can('payment.collect') && debt.status === 'outstanding'" size="sm" color="neutral" variant="ghost" class="-mx-1.5" :aria-label="`收还款 ${debt.business_date} 未还 ${yuanFixed(debt.outstanding)}`" @click="editRepay(debt)">收还款</UButton>
      </div>
    </div>
  </section>
  <UModal v-model:open="repayOpen" title="收还款" :description="target ? `这笔还欠 ${yuanFixed(target.outstanding)}，可以只还一部分` : undefined">
    <template #body>
      <div class="space-y-5">
        <UFormField label="还款金额" required><UInput v-model="amount" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField label="收款方式"><MethodPicker v-model="method" :items="methodItems" label="收款方式" /></UFormField>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="repayOpen = false">
        <UButton :loading="repaying" :disabled="!amount.trim()" @click="repay">确认收款</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
