<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view' })

type GroupbuyMonth = components['schemas']['GroupbuyMonth']

const api = useApi()
const toast = useToast()
const { can } = useMe()
const platformItems = [{ label: '美团', value: 'meituan' }, { label: '抖音', value: 'douyin' }]
const platform = ref('meituan')
const month = ref('')
const data = ref<GroupbuyMonth | null>(null)
const loading = ref(false)
const loadGuard = useRequestGuard()

function currentMonth() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

async function load() {
  const current = loadGuard.next()
  loading.value = true
  try {
    const { data: result, error } = await api.POST('/api/ops/get_groupbuy_month', {
      body: { platform: platform.value, month: month.value }
    })
    if (!current()) return
    if (!result) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    data.value = result
  } finally {
    if (current()) loading.value = false
  }
}

onMounted(() => {
  month.value = currentMonth()
  void load()
})

const settlementForm = useModalForm()
const { open: settlementOpen, saving: settlementSaving } = settlementForm
const settlementAmount = ref('0.00')
const settlementNote = ref('')

function openSettlement() {
  settlementForm.begin(() => {
    settlementAmount.value = '0.00'
    settlementNote.value = ''
  })
}

async function saveSettlement() {
  await settlementForm.save(async (current) => {
    const { data: result, error } = await api.POST('/api/ops/record_groupbuy_settlement', {
      body: { platform: platform.value, month: month.value, amount: settlementAmount.value, note: settlementNote.value.trim() }
    })
    if (!current()) return
    if (!result) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    settlementOpen.value = false
    toast.add({ title: '已录入结算额', color: 'success' })
    await load()
  })
}
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-xl font-semibold">团购对账</h1>
    <form class="flex flex-wrap items-end gap-2" @submit.prevent="load">
      <UFormField label="平台"><USelect v-model="platform" :items="platformItems" class="w-32" aria-label="平台" /></UFormField>
      <UFormField label="结算月"><UInput v-model="month" type="month" aria-label="结算月" /></UFormField>
      <UButton type="submit" :loading="loading">查询</UButton>
      <UButton v-if="can('closing.daily')" variant="outline" @click="openSettlement">录入结算</UButton>
    </form>

    <p v-if="!data && !loading" class="text-sm text-muted">选择平台和结算月查看对账。</p>
    <template v-if="data">
      <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <UCard><div class="text-sm text-muted">已核销券</div><div class="text-2xl font-semibold" data-testid="redeemed-count">{{ data.redeemed_count }}</div></UCard>
        <UCard><div class="text-sm text-muted">平台购买价合计</div><div class="text-2xl font-semibold" data-testid="purchase-total">{{ data.purchase_total }}</div></UCard>
        <UCard><div class="text-sm text-muted">当前结算额</div><div class="text-2xl font-semibold" data-testid="settlement-amount">{{ data.settlement?.amount ?? '未录入' }}</div></UCard>
        <UCard><div class="text-sm text-muted">差额</div><div class="text-2xl font-semibold" data-testid="difference" :class="data.difference && data.difference !== '0.00' ? 'text-warning' : ''">{{ data.difference ?? '—' }}</div></UCard>
      </div>

      <div class="grid gap-3 md:grid-cols-3">
        <UCard>
          <div class="font-medium">已关联已结账</div>
          <p data-testid="linked-paid">{{ data.linked_paid.count }} 张 · 购买价 {{ data.linked_paid.purchase_total }}</p>
        </UCard>
        <UCard>
          <div class="font-medium">已关联挂单</div>
          <p data-testid="linked-open">{{ data.linked_open.count }} 张 · 购买价 {{ data.linked_open.purchase_total }}</p>
        </UCard>
        <UCard>
          <div class="font-medium">未关联</div>
          <p data-testid="unlinked">{{ data.unlinked.count }} 张 · 购买价 {{ data.unlinked.purchase_total }}</p>
          <p class="text-sm text-muted" data-testid="unlinked-no-deal">其中没有团购商品快照 {{ data.unlinked_no_deal }} 张</p>
        </UCard>
      </div>

      <UCard>
        <template #header><div class="font-medium">结算录入历史</div></template>
        <p v-if="!data.history.length" class="text-sm text-muted">还没有录入结算</p>
        <table v-else class="w-full text-sm" data-testid="settlement-history">
          <thead class="text-left text-muted"><tr><th class="py-1">时间</th><th>金额</th><th>录入人</th><th>备注</th></tr></thead>
          <tbody>
            <tr v-for="row in data.history" :key="row.id" class="border-t border-default">
              <td class="py-1">{{ new Date(row.created_at).toLocaleString('zh-CN') }}</td>
              <td>{{ row.amount }}</td>
              <td>{{ row.entered_by_name }}</td>
              <td>{{ row.note || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </UCard>
    </template>

    <UModal v-model:open="settlementOpen" title="录入团购平台结算额">
      <template #body>
        <div class="space-y-3">
          <UFormField label="结算额"><UInput v-model="settlementAmount" inputmode="decimal" aria-label="结算额" /></UFormField>
          <UFormField label="备注"><UInput v-model="settlementNote" :maxlength="500" aria-label="结算备注" /></UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex gap-2">
          <UButton :loading="settlementSaving" @click="saveSettlement">保存</UButton>
          <UButton variant="ghost" @click="settlementOpen = false">取消</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>
