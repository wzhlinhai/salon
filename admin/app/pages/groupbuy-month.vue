<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '团购对账' })

type GroupbuyMonth = components['schemas']['GroupbuyMonth']
type Platform = components['schemas']['GroupbuyMonthInput']['platform']

const api = useApi()
const toast = useToast()
const { can } = useMe()
const platformItems: { label: string, value: Platform }[] = [{ label: '美团', value: 'meituan' }, { label: '抖音', value: 'douyin' }]
const platform = ref<Platform>('meituan')
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
  <div class="space-y-6">
    <PageActions v-if="can('closing.daily')">
      <UButton size="sm" icon="i-lucide-plus" @click="openSettlement">录入结算</UButton>
    </PageActions>
    <form class="flex flex-wrap items-center gap-2" @submit.prevent="load">
      <USelect v-model="platform" :items="platformItems" class="w-28" aria-label="平台" />
      <UInput v-model="month" type="month" aria-label="结算月" />
      <UButton type="submit" color="neutral" variant="outline" :loading="loading">查询</UButton>
    </form>

    <EmptyState v-if="!data && !loading" class="box" title="选择平台和结算月查看对账。" hint="平台月底打款后，把结算额录进来，和核销的券对一对" />
    <template v-if="data">
      <div class="box grid grid-cols-2 gap-y-4 tabular-nums md:grid-cols-4">
        <div>
          <p class="text-xs text-dimmed">已核销券</p>
          <p class="text-2xl leading-[1.3] font-semibold"><span data-testid="redeemed-count">{{ data.redeemed_count }}</span> <span class="text-sm font-normal text-muted">张</span></p>
        </div>
        <div class="border-l border-default pl-4">
          <p class="text-xs text-dimmed">平台购买价合计</p>
          <p class="text-2xl leading-[1.3] font-semibold" data-testid="purchase-total">{{ yuan(data.purchase_total) }}</p>
        </div>
        <div class="md:border-l md:border-default md:pl-4">
          <p class="text-xs text-dimmed">当前结算额</p>
          <p class="text-2xl leading-[1.3] font-semibold" data-testid="settlement-amount" :class="data.settlement ? '' : 'text-dimmed'">{{ data.settlement ? yuan(data.settlement.amount) : '未录入' }}</p>
        </div>
        <div class="border-l border-default pl-4">
          <p class="text-xs text-dimmed">差额</p>
          <p class="text-2xl leading-[1.3] font-semibold" data-testid="difference" :class="data.difference && Number(data.difference) !== 0 ? 'text-ink-orange' : data.difference ? '' : 'text-dimmed'">{{ data.difference ? yuan(data.difference) : '—' }}</p>
        </div>
      </div>

      <section>
        <div class="sec">核销的券</div>
        <div class="box overflow-hidden p-0 text-sm">
          <div class="flex items-center gap-4 px-4 py-2.5">
            <span class="w-28 font-medium">已关联已结账</span>
            <span data-testid="linked-paid" class="tabular-nums">{{ data.linked_paid.count }} 张 · 购买价 {{ yuan(data.linked_paid.purchase_total) }}</span>
          </div>
          <div class="flex items-center gap-4 border-t border-default px-4 py-2.5">
            <span class="w-28 font-medium">已关联挂单</span>
            <span data-testid="linked-open" class="tabular-nums">{{ data.linked_open.count }} 张 · 购买价 {{ yuan(data.linked_open.purchase_total) }}</span>
          </div>
          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-default px-4 py-2.5">
            <span class="w-28 font-medium">未关联</span>
            <span data-testid="unlinked" class="tabular-nums">{{ data.unlinked.count }} 张 · 购买价 {{ yuan(data.unlinked.purchase_total) }}</span>
            <span class="text-xs text-muted" data-testid="unlinked-no-deal">其中没有团购商品快照 {{ data.unlinked_no_deal }} 张</span>
          </div>
        </div>
      </section>

      <section>
        <div class="sec">结算录入历史</div>
        <div class="box">
          <p v-if="!data.history.length" class="text-sm text-muted">还没有录入结算</p>
          <table v-else class="w-full text-sm" data-testid="settlement-history">
            <thead class="text-left text-xs text-dimmed"><tr><th class="py-1.5 font-normal">时间</th><th class="font-normal">金额</th><th class="font-normal">录入人</th><th class="font-normal">备注</th></tr></thead>
            <tbody>
              <tr v-for="row in data.history" :key="row.id" class="border-t border-default">
                <td class="py-2 text-muted tabular-nums">{{ new Date(row.created_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }}</td>
                <td class="font-medium tabular-nums">{{ yuanFixed(row.amount) }}</td>
                <td>{{ row.entered_by_name }}</td>
                <td :class="row.note ? '' : 'text-dimmed'">{{ row.note || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
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
