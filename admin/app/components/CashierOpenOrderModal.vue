<script setup lang="ts">
import type { components } from '~/utils/api'
import type { OrderItemRow } from '~/components/OrderItemRows.vue'

// 收银开单弹窗：普通开单（可补录纸单），或核销团购券后立即开单（开单和关联券同一事务）。
type Order = components['schemas']['Order']
type Customer = components['schemas']['Customer']
type Item = components['schemas']['Item']
type Deal = components['schemas']['Deal']
type Redemption = components['schemas']['Redemption']

const props = defineProps<{
  items: Item[]
  staffItems: { label: string, value: string }[]
  businessDate: string
  backfillMaxDays: number
}>()
const emit = defineEmits<{ opened: [order: Order, viaCoupon: boolean] }>()
const api = useApi()
const toast = useToast()

const form = useModalForm()
const { open, saving } = form
const customer = ref<Customer | null>(null)
const gender = ref<'male' | 'female'>('female')
const designated = ref(false)
const backfill = ref(false)
const paperDate = ref('')
const rows = ref<OrderItemRow[]>([])
const pendingRedemption = ref<Redemption | null>(null)
const pendingDeal = ref<Deal | null>(null)

// itemIDs：从顾客档案「照这个开单」带来的上次项目，只预填项目，做的人照常选。
function begin(who: Customer | null, deal: Deal | null = null, redemption: Redemption | null = null, itemIDs: string[] = []) {
  form.begin(() => {
    customer.value = who
    gender.value = who?.gender === 'male' ? 'male' : 'female'
    designated.value = false
    backfill.value = false
    paperDate.value = props.businessDate ? addDays(props.businessDate, -1) : ''
    pendingDeal.value = deal
    pendingRedemption.value = redemption
    const preset = itemIDs.filter(id => props.items.some(item => item.id === id))
    rows.value = deal
      ? [{ item_id: deal.item_id, staff: {} }]
      : preset.map(id => ({ item_id: id, staff: {} }))
  })
}

async function save() {
  const inputItems = orderItemsInput(rows.value, props.items)
  if (!inputItems.length) {
    toast.add({ title: '请选择项目', color: 'warning' })
    return
  }
  await form.save(async (current) => {
    const redemption = pendingRedemption.value
    const deal = pendingDeal.value
    if (redemption && deal) {
      // 开单和关联团购券在同一个事务里，不会出现单开了券没关上。团购开单不支持补录。
      const { data, error } = await api.POST('/api/ops/open_groupbuy_order', {
        body: {
          customer_id: customer.value?.id,
          gender: gender.value,
          designated: designated.value,
          redemption_id: redemption.id,
          deal_id: deal.id,
          items: inputItems
        }
      })
      if (!current()) return
      if (!data) {
        toast.add({ title: errorText(error), color: 'error' })
        return
      }
      toast.add({ title: '已开单并关联团购券', color: 'success' })
      open.value = false
      emit('opened', data.order, true)
      return
    }
    const { data, error } = await api.POST('/api/ops/open_order', {
      body: {
        customer_id: customer.value?.id,
        gender: gender.value,
        designated: designated.value,
        paper_date: backfill.value ? paperDate.value : undefined,
        items: inputItems
      }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已开单', color: 'success' })
    open.value = false
    emit('opened', data, false)
  })
}

// 底栏左边一句：几项、大约多久、做到几点（按项目服务时长相加，没设时长的不算）；右边按标价合计，实收以结账为准。
const picked = computed(() => rows.value.flatMap(r => props.items.find(i => i.id === r.item_id) ?? []))
const listTotal = computed(() => picked.value.reduce((sum, i) => sum + Number(i.price), 0))
const summary = computed(() => {
  const list = picked.value
  if (!list.length) return '还没挑项目'
  const minutes = list.reduce((sum, i) => sum + i.duration_minutes, 0)
  if (!minutes || backfill.value) return `${list.length} 项`
  const end = new Date(Date.now() + minutes * 60_000)
  const hours = minutes >= 60 ? `${Math.round(minutes / 30) / 2} 小时` : `${minutes} 分钟`
  return `${list.length} 项 · 约 ${hours} · 到 ${end.getHours()}:${String(end.getMinutes()).padStart(2, '0')}`
})

const customerLine = computed(() => {
  const c = customer.value
  if (!c) return ''
  return [c.phone, c.preferences].filter(Boolean).join(' · ')
})

defineExpose({ begin })
</script>

<!-- 大表单弹窗（样稿 18）：顶上浅底一条写顾客，中间挑项目、选做的人，底栏左边几项多久、右边合计和开单。 -->
<template>
  <UModal v-model:open="open" :title="pendingDeal ? '团购立即开单' : '开单'" description="选好项目和做的人，开单后挂在左边「在做」里，做完再结账" :ui="{ content: 'sm:max-w-[600px]' }">
    <template #body>
      <div class="space-y-4">
        <div>
          <p class="mb-2 text-meta text-muted">顾客</p>
          <div class="flex items-center gap-3 rounded-card bg-side px-4 py-3" data-testid="open-order-customer">
            <PersonAvatar :name="customer?.name || '散'" />
            <div class="min-w-0 flex-1">
              <p class="truncate font-medium" :class="customer ? '' : 'text-muted'">{{ customer?.name || '散客（没认出是谁）' }}</p>
              <p v-if="customerLine" class="truncate text-meta text-muted">{{ customerLine }}</p>
            </div>
            <span v-if="customer?.active_safety_labels.length" class="tag shrink-0 bg-tag-red">有安全提醒</span>
            <MethodPicker v-model="gender" label="顾客性别" :items="[{ label: '女客', value: 'female' }, { label: '男客', value: 'male' }]" />
          </div>
        </div>
        <OrderItemRows v-model="rows" :items="items" :staff-items="staffItems" />
        <div class="flex flex-wrap items-center gap-x-6 gap-y-2">
          <UCheckbox v-model="designated" label="指定（顾客点名要谁做）" />
          <UCheckbox v-if="!pendingDeal" v-model="backfill" label="补录纸单" />
        </div>
        <UFormField v-if="backfill && !pendingDeal" label="纸单营业日" :help="`最多补录最近 ${backfillMaxDays} 个营业日`">
          <DateInput v-model="paperDate" :max="businessDate" :aria-label="'纸单营业日'" />
        </UFormField>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="open = false">
        <template #before>
          <span v-if="listTotal" class="me-2 font-semibold tabular-nums" data-testid="open-order-total">{{ yuan(listTotal) }}</span>
        </template>
        <template #leading>
          <span class="text-meta text-muted tabular-nums" data-testid="open-order-summary">{{ summary }}</span>
        </template>
        <UButton :loading="saving" @click="save">开单</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
