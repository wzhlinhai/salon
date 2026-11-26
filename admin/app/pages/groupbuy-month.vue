<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { components } from '~/utils/api'

definePageMeta({ permission: 'report.view', title: '团购对账', art: 'ticket' })

type GroupbuyMonth = components['schemas']['GroupbuyMonth']
type Platform = components['schemas']['GroupbuyMonthInput']['platform']

const api = useApi()
const toast = useToast()
const { can } = useMe()
const platformTabs: { key: Platform, label: string }[] = [{ key: 'meituan', label: '美团' }, { key: 'douyin', label: '抖音' }]
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
})
// 换平台或月份就重新对账，不用再点查询。
watch([platform, month], () => {
  if (/^\d{4}-\d{2}$/.test(month.value)) void load()
})

const historyColumns: TableColumn<GroupbuyMonth['history'][number]>[] = [
  { id: 'created_at', header: '时间' },
  { id: 'amount', header: '金额' },
  { id: 'entered_by_name', accessorKey: 'entered_by_name', header: '录入人' },
  { id: 'note', header: '备注' }
]
const empty = computed(() => !!data.value && !data.value.redeemed_count && !data.value.history.length)

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
    <PageActions v-if="can('closing.daily')">
      <UButton size="sm" @click="openSettlement">录入结算</UButton>
    </PageActions>
    <div class="flex flex-wrap items-center gap-2">
      <ViewTabs v-model="platform" :items="platformTabs" label="平台" />
      <DateInput v-model="month" month aria-label="结算月" class="ms-auto w-36" />
    </div>

    <p v-if="empty" class="py-6 text-muted">{{ month }} {{ platformTabs.find(p => p.key === platform)?.label }}没有核销的券，也没录入结算。</p>
    <template v-else-if="data">
      <!-- 照 Notion 的页面属性：左边灰字属性名，右边值，一行 34px。 -->
      <dl class="props tabular-nums" :class="loading ? 'opacity-60' : ''">
        <dt>已核销券</dt>
        <dd><span data-testid="redeemed-count">{{ data.redeemed_count }}</span> 张</dd>
        <dt>平台购买价合计</dt>
        <dd data-testid="purchase-total">{{ yuan(data.purchase_total) }}</dd>
        <dt>当前结算额</dt>
        <dd data-testid="settlement-amount" :class="data.settlement ? '' : 'text-dimmed'">{{ data.settlement ? yuan(data.settlement.amount) : '未录入' }}</dd>
        <dt>差额</dt>
        <dd data-testid="difference" :class="data.difference && Number(data.difference) !== 0 ? 'text-ink-orange' : data.difference ? '' : 'text-dimmed'">{{ data.difference ? yuan(data.difference) : '—' }}</dd>
      </dl>

      <section>
        <div class="sec">核销的券</div>
        <dl class="props tabular-nums">
          <dt>已关联已结账</dt>
          <dd data-testid="linked-paid">{{ data.linked_paid.count }} 张 · 购买价 {{ yuan(data.linked_paid.purchase_total) }}</dd>
          <dt>已关联挂单</dt>
          <dd data-testid="linked-open">{{ data.linked_open.count }} 张 · 购买价 {{ yuan(data.linked_open.purchase_total) }}</dd>
          <dt>未关联</dt>
          <dd>
            <span data-testid="unlinked">{{ data.unlinked.count }} 张 · 购买价 {{ yuan(data.unlinked.purchase_total) }}</span>
            <span class="ms-3 text-muted" data-testid="unlinked-no-deal">其中没有团购商品快照 {{ data.unlinked_no_deal }} 张</span>
          </dd>
        </dl>
      </section>

      <section>
        <div class="sec">结算录入历史</div>
        <p v-if="!data.history.length" class="text-muted">还没有录入结算</p>
        <div v-else class="overflow-x-auto">
          <UTable :data="data.history" :columns="historyColumns" data-testid="settlement-history">
            <template #created_at-cell="{ row }">
              <span class="text-muted tabular-nums">{{ new Date(row.original.created_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }}</span>
            </template>
            <template #amount-cell="{ row }">
              <span class="font-medium tabular-nums">{{ yuanFixed(row.original.amount) }}</span>
            </template>
            <template #note-cell="{ row }">
              <span class="text-muted">{{ row.original.note }}</span>
            </template>
          </UTable>
        </div>
      </section>
    </template>

    <UModal v-model:open="settlementOpen" title="录入团购平台结算额">
      <template #body>
        <div class="space-y-5">
          <UFormField label="结算额"><UInput v-model="settlementAmount" inputmode="decimal" aria-label="结算额" /></UFormField>
          <UFormField label="备注"><UInput v-model="settlementNote" :maxlength="500" aria-label="结算备注" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="settlementOpen = false">
          <UButton :loading="settlementSaving" @click="saveSettlement">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
