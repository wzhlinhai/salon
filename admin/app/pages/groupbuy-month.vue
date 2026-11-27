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

function currentMonth() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

// 读失败时丢掉上一个平台或月份的对账，原地给「重试」：不然看着的是别的月份的数，却以为是这个月的（hasData 恒为否）。
const { loading, error: loadError, load } = usePageLoad({
  fetch: () => [api.POST('/api/ops/get_groupbuy_month', { body: { platform: platform.value, month: month.value } })],
  apply: ([r]) => {
    data.value = r.data!
  },
  hasData: () => false,
  onFail: () => {
    data.value = null
  }
})

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
    <PageActions>
      <DateInput v-model="month" month aria-label="结算月" class="w-36" />
      <UButton v-if="can('closing.daily')" @click="openSettlement">录入结算</UButton>
    </PageActions>
    <ViewTabs v-model="platform" :items="platformTabs" label="平台" />

    <LoadError v-if="loadError" :title="`${month} 的对账没有读出来`" :hint="loadError" @retry="load()" />
    <EmptyState v-else-if="empty" :title="`这个月${platformTabs.find(p => p.key === platform)?.label}没有核销的券`" hint="也还没录入结算" />
    <template v-else-if="data">
      <!-- 照 Notion 的页面属性：左边灰字属性名，右边值，一行 34px。 -->
      <dl class="props tabular-nums" :class="loading ? 'opacity-60' : ''">
        <dt>已核销券</dt>
        <dd><span data-testid="redeemed-count">{{ data.redeemed_count }}</span> 张</dd>
        <dt>平台购买价合计</dt>
        <dd data-testid="purchase-total">{{ yuan(data.purchase_total) }}</dd>
        <dt>当前结算额</dt>
        <dd data-testid="settlement-amount" :class="data.settlement ? '' : 'text-muted'">{{ data.settlement ? yuan(data.settlement.amount) : '未录入' }}</dd>
        <dt>差额</dt>
        <dd data-testid="difference" :class="data.difference && Number(data.difference) !== 0 ? 'text-ink-orange' : data.difference ? '' : 'text-dimmed'">{{ data.difference ? yuan(data.difference) : '—' }}</dd>
      </dl>

      <section>
        <h2 class="sec">核销的券</h2>
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
        <h2 class="sec">结算录入历史</h2>
        <p v-if="!data.history.length" class="text-muted">还没有录入结算</p>
        <div v-else class="overflow-x-auto">
          <UTable :data="data.history" :columns="historyColumns" data-testid="settlement-history">
            <template #created_at-cell="{ row }">
              <span class="text-muted tabular-nums">{{ storeDateTime(row.original.created_at) }}</span>
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
