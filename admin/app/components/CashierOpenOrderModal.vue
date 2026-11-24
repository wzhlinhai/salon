<script setup lang="ts">
import type { components } from '~/utils/api'

// 收银开单弹窗：普通开单（可补录纸单），或核销团购券后立即开单（开单和关联券同一事务）。
type Order = components['schemas']['Order']
type Customer = components['schemas']['Customer']
type Item = components['schemas']['Item']
type Deal = components['schemas']['Deal']
type Redemption = components['schemas']['Redemption']

interface OrderItemRow { item_id: string, staff: Record<number, string> }

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
const itemOptions = computed(() => props.items.map(i => ({ label: `${i.name}（${i.price}）`, value: i.id })))

function positionsOf(itemID: string) {
  const item = props.items.find(i => i.id === itemID)
  return [...(item?.positions ?? [])].sort((a, b) => a.slot - b.slot)
}

function begin(who: Customer | null, deal: Deal | null = null, redemption: Redemption | null = null) {
  form.begin(() => {
    customer.value = who
    gender.value = who?.gender === 'male' ? 'male' : 'female'
    designated.value = false
    backfill.value = false
    paperDate.value = props.businessDate ? addDays(props.businessDate, -1) : ''
    pendingDeal.value = deal
    pendingRedemption.value = redemption
    rows.value = deal ? [{ item_id: deal.item_id, staff: {} }] : [{ item_id: '', staff: {} }]
  })
}

async function save() {
  const picked = rows.value.filter(r => r.item_id)
  if (!picked.length) {
    toast.add({ title: '请选择项目', color: 'warning' })
    return
  }
  const inputItems = picked.map(row => ({
    item_id: row.item_id,
    staff: positionsOf(row.item_id)
      .filter(p => row.staff[p.slot])
      .map(p => ({ slot: p.slot, staff_id: row.staff[p.slot]! }))
  }))
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

defineExpose({ begin })
</script>

<template>
  <UModal v-model:open="open" :title="pendingDeal ? '团购立即开单' : '开单'" description="选好项目和做的人，开单后挂在左边，做完再结账" :ui="{ content: 'sm:max-w-[600px]' }">
    <template #body>
      <div class="space-y-5">
        <!-- 顾客、性别、指定、补录收进一块浅底：都是"这单是谁、怎么来的"，和下面的项目分开。 -->
        <div class="space-y-3 rounded-lg bg-side p-3.5 ring-1 ring-default ring-inset">
          <div class="flex items-center gap-3">
            <div class="min-w-0 flex-1">
              <p class="text-meta text-muted">顾客</p>
              <p class="truncate font-medium" :class="customer ? 'text-default' : 'text-muted'">{{ customer?.name || '未识别顾客' }}</p>
            </div>
            <MethodPicker v-model="gender" label="顾客性别" :items="[{ label: '女客', value: 'female' }, { label: '男客', value: 'male' }]" />
          </div>
          <div class="flex items-center gap-6 border-t border-default pt-3">
            <USwitch v-model="designated" label="指定" />
            <USwitch v-if="!pendingDeal" v-model="backfill" label="补录" />
          </div>
          <UFormField v-if="backfill && !pendingDeal" label="纸单营业日" :help="`最多补录最近 ${backfillMaxDays} 个营业日`">
            <DateInput v-model="paperDate" :max="businessDate" :aria-label="'纸单营业日'" />
          </UFormField>
        </div>
        <FieldGroup label="项目">
          <div v-for="(row, index) in rows" :key="index" class="space-y-2" data-testid="order-item-row">
            <div class="flex items-center gap-2">
              <USelect v-model="row.item_id" :items="itemOptions" class="min-w-0 flex-1" :aria-label="`项目${index + 1}`" placeholder="选择项目" @update:model-value="row.staff = {}" />
              <UButton variant="ghost" color="error" icon="i-lucide-x" aria-label="去掉" :disabled="rows.length === 1" @click="rows.splice(index, 1)" />
            </div>
            <div v-for="position in positionsOf(row.item_id)" :key="position.slot" class="flex items-center gap-2 ps-3">
              <span class="w-14 shrink-0 text-meta text-muted">岗位 {{ position.slot }}</span>
              <USelect v-model="row.staff[position.slot]" :items="staffItems" class="min-w-0 flex-1" :aria-label="`项目${index + 1}岗位${position.slot}员工`" placeholder="选择员工" />
              <span v-if="position.required" class="tag shrink-0 bg-tag-yellow">必填</span>
            </div>
          </div>
          <UButton color="neutral" icon="i-lucide-plus" class="-ms-2.5 self-start" variant="ghost" @click="rows.push({ item_id: '', staff: {} })">添加项目</UButton>
        </FieldGroup>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="open = false">
        <UButton :loading="saving" @click="save">开单</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
