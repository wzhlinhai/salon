<script setup lang="ts">
import type { components } from '~/utils/api'
import { LOG_PAGE_SIZE } from '~/utils/paging'

type Schema = components['schemas']
const props = defineProps<{ month: string, staffId: string, locked: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const api = useApi()
const toast = useToast()
const totals = ref<Schema['CommissionSourceTotal'][]>([])
const sourceNames = { item: '项目业绩', consume: '耗卡业绩', labor: '手工费', sale: '销售业绩' }
const bases = { line_price: '项目行价格（原价）', paid: '实收（折后价）', performance: '业绩', cash_performance: '现金业绩', card_performance: '卡金业绩' }
const commissions = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_payroll_commissions', { body: { month: props.month, staff_id: props.staffId, limit: LOG_PAGE_SIZE, before_id: beforeId } })
  if (data) totals.value = data.totals
  return { data: data?.entries, error }
})
const adjustments = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_payroll_adjustments', { body: { month: props.month, staff_id: props.staffId, limit: LOG_PAGE_SIZE, before_id: beforeId } })
  return { data: data?.entries, error }
})

function explanation(c: Schema['Commission']) {
  const e = c.explanation
  const match = e.match.override ?? e.match.cell
  const value = e.tier ? `${e.tier.mode === 'whole' ? '整笔跳档' : '超额累进'}，从 ${e.tier.level.from} 元起 ${e.tier.level.percent}%` : match?.percent !== undefined ? `${match.percent}%` : match?.fixed_amount !== undefined ? `固定 ${match.fixed_amount} 元` : '不计提成'
  return `${bases[e.base.kind]} ${e.base.amount} · ${value} · 方案第 ${e.version.version} 版`
}

const deletion = useModalForm()
const { open, saving } = deletion
const target = ref<Schema['PayrollAdjustment']>()

function askDelete(a: Schema['PayrollAdjustment']) {
  deletion.begin(() => {
    target.value = a
  })
}

async function remove() {
  await deletion.save(async (current) => {
    const { error } = await api.POST('/api/ops/delete_payroll_adjustment', { body: { id: target.value!.id } })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    emit('changed')
    void adjustments.reload()
    if (!current()) return
    open.value = false
    toast.add({ title: '已删除' })
  })
}

function signed(amount: string) {
  return amount.startsWith('-') ? amount : `+${amount}`
}

defineExpose({ reloadAdjustments: adjustments.reload })
onMounted(() => Promise.all([commissions.reload(), adjustments.reload()]))
</script>

<template>
  <div class="space-y-6" data-testid="payroll-detail">
    <section class="space-y-3">
      <h2 class="font-semibold">提成明细</h2>
      <div data-testid="commission-totals" class="text-sm space-y-1">
        <p v-for="t in totals" :key="t.source">{{ sourceNames[t.source] }} {{ t.amount }}</p>
      </div>
      <div v-for="c in commissions.items.value" :key="c.id" class="space-y-1" data-testid="payroll-commission">
        <p>{{ c.business_date }} · {{ sourceNames[c.source] }} · {{ c.amount }} <UBadge v-if="c.reversal" color="neutral">冲回</UBadge></p>
        <p class="text-sm text-muted">{{ explanation(c) }}</p>
      </div>
      <UButton v-if="commissions.hasMore.value" :loading="commissions.loading.value" variant="ghost" @click="commissions.loadMore">加载更多</UButton>
    </section>
    <section class="space-y-3">
      <h2 class="font-semibold">加减项</h2>
      <div v-for="a in adjustments.items.value" :key="a.id" data-testid="payroll-adjustment" class="space-y-1">
        <div class="flex justify-between gap-2">
          <p>{{ a.name }} {{ signed(a.amount) }}</p>
          <UButton v-if="!locked" size="xs" color="error" variant="ghost" @click="askDelete(a)">删除</UButton>
        </div>
        <p class="text-sm text-muted"><template v-if="a.note">{{ a.note }} · </template>{{ a.created_by.name }} · {{ new Date(a.created_at).toLocaleString('zh-CN') }}</p>
      </div>
      <UButton v-if="adjustments.hasMore.value" :loading="adjustments.loading.value" variant="ghost" @click="adjustments.loadMore">加载更多</UButton>
    </section>
    <UModal v-model:open="open" :title="`删除加减项「${target?.name ?? ''}」？`">
      <template #footer>
        <UButton color="neutral" variant="outline" @click="open = false">取消</UButton>
        <UButton color="error" :loading="saving" @click="remove">确认删除</UButton>
      </template>
    </UModal>
  </div>
</template>
