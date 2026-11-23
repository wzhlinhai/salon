<script setup lang="ts">
import type { components } from '~/utils/api'

// 一张单的收款记录：结账时按收款方式自动登记，撤单时自动撤销。
// 这里的退款、撤销只改收款记录本身（对账用），不改单的实收和业绩。
type Collection = components['schemas']['Collection']
type Status = Collection['status']
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ orderId: string | null, summary: string }>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const items = ref<Collection[]>([])
const loading = ref(false)
const listGuard = useRequestGuard()

const channelNames: Record<string, string> = { cash: '现金', unionpay_register: '银联登记' }
const statusNames: Record<Status, string> = { pending: '待收', processing: '收款中', succeeded: '已收', failed: '失败', cancelled: '已撤销', refunded: '已退款' }
const statusTags: Record<Status, string> = { pending: 'bg-tag-yellow', processing: 'bg-tag-yellow', succeeded: 'bg-tag-green', failed: 'bg-tag-red', cancelled: 'bg-tag-gray', refunded: 'bg-tag-gray' }
// 与 backend/internal/collections/channels.go 的 transitions 一致。
const canCancel = (c: Collection) => ['pending', 'processing', 'succeeded'].includes(c.status)
const canRefund = (c: Collection) => c.status === 'succeeded'

async function load() {
  const current = listGuard.next()
  if (!props.orderId) return
  loading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_collections', { body: { order_id: props.orderId } })
    if (!current()) return
    if (data) items.value = data.items
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) loading.value = false
  }
}

watch(open, (value) => {
  if (!value) return
  items.value = []
  load()
})

const changeForm = useModalForm()
const { open: changeOpen, saving: changing } = changeForm
const target = ref<Collection | null>(null)
const action = ref<'refund' | 'cancel'>('refund')
const reason = ref('')

function editChange(collection: Collection, kind: 'refund' | 'cancel') {
  changeForm.begin(() => {
    target.value = collection
    action.value = kind
    reason.value = ''
  })
}

async function submitChange() {
  const collection = target.value
  if (!collection) return
  const body = { id: collection.id, reason: reason.value.trim() }
  const path = action.value === 'refund' ? '/api/ops/refund_collection' : '/api/ops/cancel_collection'
  await changeForm.save(async (current) => {
    const { data, error } = await api.POST(path, { body })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    changeOpen.value = false
    toast.add({ title: action.value === 'refund' ? '已登记退款' : '已撤销这笔收款', color: 'success' })
    await load()
  })
}
</script>

<template>
  <UModal v-model:open="open" title="收款记录" description="结账时按收款方式自动登记，撤单时自动撤销。这里的退款、撤销只改收款记录，不改单的实收和业绩；退钱给顾客请走撤单或售后。">
    <template #body>
      <div class="space-y-4">
        <p class="rounded-lg bg-side px-3 py-2.5 text-sm">{{ summary }}</p>
        <div data-testid="collections" class="box overflow-hidden p-0">
          <ListSkeleton v-if="loading && !items.length" :rows="2" />
          <p v-else-if="!items.length" class="px-3 py-2.5 text-sm text-muted">这张单没有收款记录（卡扣、团购和红包不登记收款）</p>
          <div v-for="c in items" :key="c.id" :data-collection="c.id" class="border-t border-default px-3 py-2.5 text-sm first:border-t-0">
            <p class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span>{{ channelNames[c.channel] ?? c.channel }}</span>
              <span class="font-medium tabular-nums">{{ yuanFixed(c.amount) }}</span>
              <span class="tag" :class="statusTags[c.status]">{{ statusNames[c.status] }}</span>
              <span class="ms-auto text-meta text-muted tabular-nums">{{ formatStamp(c.updated_at) }}</span>
            </p>
            <p v-if="c.external_ref" class="mt-0.5 text-meta text-muted">流水号 {{ c.external_ref }}</p>
            <div v-if="can('payment.collect') && (canRefund(c) || canCancel(c))" class="mt-1 -mx-1.5 flex gap-1">
              <UButton v-if="canRefund(c)" size="sm" color="neutral" variant="ghost" :aria-label="`登记退款 ${yuanFixed(c.amount)}`" @click="editChange(c, 'refund')">登记退款</UButton>
              <UButton v-if="canCancel(c)" size="sm" color="neutral" variant="ghost" :aria-label="`撤销收款 ${yuanFixed(c.amount)}`" @click="editChange(c, 'cancel')">撤销</UButton>
            </div>
          </div>
        </div>
      </div>
    </template>
  </UModal>
  <UModal v-model:open="changeOpen" :title="action === 'refund' ? '登记退款' : '撤销收款'" :description="target ? `${channelNames[target.channel] ?? target.channel} ${yuanFixed(target.amount)}，改了不能恢复` : undefined">
    <template #body>
      <UFormField :label="action === 'refund' ? '退款原因' : '撤销原因'" :required="action === 'refund'">
        <UTextarea v-model="reason" :maxlength="200" class="w-full" :placeholder="action === 'refund' ? '例如：通道那边已原路退回' : '可不填'" />
      </UFormField>
    </template>
    <template #footer>
      <ModalActions @cancel="changeOpen = false">
        <UButton color="error" :loading="changing" :disabled="action === 'refund' && !reason.trim()" @click="submitChange">{{ action === 'refund' ? '确认退款' : '确认撤销' }}</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
