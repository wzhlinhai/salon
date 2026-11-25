<script setup lang="ts">
import type { components } from '~/utils/api'
import { LOG_PAGE_SIZE } from '~/utils/paging'

type Schema = components['schemas']
const props = defineProps<{ month: string, staffId: string, locked: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const api = useApi()
const toast = useToast()
const totals = ref<Schema['CommissionSourceTotal'][]>([])
const commissions = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_payroll_commissions', { body: { month: props.month, staff_id: props.staffId, limit: LOG_PAGE_SIZE, before_id: beforeId } })
  if (data) totals.value = data.totals
  return { data: data?.entries, error }
})
const adjustments = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_payroll_adjustments', { body: { month: props.month, staff_id: props.staffId, limit: LOG_PAGE_SIZE, before_id: beforeId } })
  return { data: data?.entries, error }
})

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
    <section>
      <h2 class="sec">提成明细</h2>
      <div data-testid="commission-totals" class="mb-3 grid grid-cols-2 gap-2 text-sm">
        <p v-for="t in totals" :key="t.source" class="rounded-md bg-elevated px-3 py-2"><span class="text-muted">{{ commissionSourceNames[t.source] }}</span> <span class="font-medium tabular-nums">{{ t.amount }}</span></p>
      </div>
      <div class="box overflow-hidden p-0">
        <div v-for="c in commissions.items.value" :key="c.id" class="border-t border-default px-3 py-2 text-sm first:border-t-0" data-testid="payroll-commission">
          <p class="flex items-center gap-2"><span class="tabular-nums">{{ c.business_date }} · {{ commissionSourceNames[c.source] }} · {{ c.amount }}</span><span v-if="c.reversal" class="tag bg-tag-gray">冲回</span></p>
          <p class="text-meta text-muted">{{ commissionExplanation(c) }}</p>
        </div>
        <p v-if="!commissions.loading.value && !commissions.items.value.length" class="px-3 py-2 text-sm text-muted">本月还没有提成</p>
      </div>
      <UButton v-if="commissions.hasMore.value" :loading="commissions.loading.value" size="sm" color="neutral" variant="ghost" class="mt-1" @click="commissions.loadMore">加载更多</UButton>
    </section>
    <section>
      <h2 class="sec">加减项</h2>
      <div class="box overflow-hidden p-0">
        <div v-for="a in adjustments.items.value" :key="a.id" data-testid="payroll-adjustment" class="group/row border-t border-default px-3 py-2 text-sm first:border-t-0">
          <div class="flex items-center justify-between gap-2">
            <p>{{ a.name }} <span class="tabular-nums" :class="a.amount.startsWith('-') ? 'text-ink-red' : ''">{{ signed(a.amount) }}</span></p>
            <UButton v-if="!locked" size="sm" color="error" variant="ghost" class="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100" @click="askDelete(a)">删除</UButton>
          </div>
          <p data-testid="adjustment-meta" class="text-meta text-muted"><template v-if="a.note">{{ a.note }} · </template>{{ a.created_by.name }} · {{ new Date(a.created_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }}</p>
        </div>
        <p v-if="!adjustments.loading.value && !adjustments.items.value.length" class="px-3 py-2 text-sm text-muted">本月没有加减项</p>
      </div>
      <UButton v-if="adjustments.hasMore.value" :loading="adjustments.loading.value" size="sm" color="neutral" variant="ghost" class="mt-1" @click="adjustments.loadMore">加载更多</UButton>
    </section>
    <UModal v-model:open="open" :title="`删除加减项「${target?.name ?? ''}」？`">
      <template #footer>
        <ModalActions @cancel="open = false">
          <UButton color="error" :loading="saving" @click="remove">确认删除</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
