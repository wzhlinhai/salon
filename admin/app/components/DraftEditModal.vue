<script setup lang="ts">
import type { components } from '~/utils/api'
import type { Draft } from '~/composables/useDrafts'
import type { OrderItemRow } from '~/components/OrderItemRows.vue'

// 人工修改草稿：开单、新建顾客两类最常用的草稿，直接在表单里改参数和摘要，不经 AI。
// 只改表单管得到的字段，草稿里其他参数原样保留；摘要按改后的内容重写，卡片标题和确认执行的内容一致。
type Item = components['schemas']['Item']
type StaffOption = components['schemas']['StaffOption']
type Gender = 'male' | 'female'
interface OrderParams { customer_id?: string, gender?: Gender, designated?: boolean, items?: { item_id: string, staff?: { slot: number, staff_id: string }[] }[] }
interface CustomerParams { name?: string, phone?: string, gender?: Gender, note?: string }

const emit = defineEmits<{ saved: [] }>()
const api = useApi()
const toast = useToast()
const form = useModalForm()
const { open, loading, saving } = form

const draft = ref<Draft | null>(null)
const gender = ref<Gender>('female')
// 开单
const customerName = ref('')
// 草稿里的顾客：ok 读到了名字；missing 查不到（不存在，或不在自己能看的范围）；unread 没读成（没权限、网络）。
// 查不到时确认多半会失败，要说清楚；没读成时摘要里不能把有顾客的单写成「未识别顾客」。
const customerState = ref<'none' | 'ok' | 'missing' | 'unread'>('none')
const designated = ref(false)
const rows = ref<OrderItemRow[]>([])
const items = ref<Item[]>([])
const staffOptions = ref<StaffOption[]>([])
const staffItems = computed(() => staffOptions.value.map(s => ({ label: s.name, value: s.id })))
// 草稿里已选的停用项目也要能显示出来，其余只列在用的项目。
const itemChoices = computed(() => items.value.filter(i => i.active || rows.value.some(r => r.item_id === i.id)))
// 新建顾客
const name = ref('')
const phone = ref('')
const note = ref('')
// 没有看完整号码权限时，草稿里的号码是打了码的；原样提交时后端保留原号码。
const phoneMasked = computed(() => phone.value.includes('*'))

const genderItems = [{ label: '女客', value: 'female' }, { label: '男客', value: 'male' }]
const isOrder = computed(() => draft.value?.operation === 'open_order')

function begin(target: Draft) {
  form.begin(() => {
    draft.value = target
    if (target.operation === 'open_order') {
      const p = target.params as OrderParams
      gender.value = p.gender === 'male' ? 'male' : 'female'
      designated.value = !!p.designated
      customerName.value = ''
      customerState.value = p.customer_id ? 'unread' : 'none'
      rows.value = (p.items ?? []).map(i => ({ item_id: i.item_id, staff: Object.fromEntries((i.staff ?? []).map(s => [s.slot, s.staff_id])) }))
    } else {
      const p = target.params as CustomerParams
      name.value = p.name ?? ''
      phone.value = p.phone ?? ''
      gender.value = p.gender === 'male' ? 'male' : 'female'
      note.value = p.note ?? ''
    }
  })
  if (target.operation !== 'open_order') return
  const customerID = (target.params as OrderParams).customer_id
  void form.load(async (current) => {
    const [i, st, c] = await Promise.all([
      api.POST('/api/ops/list_items', { body: {} }),
      api.POST('/api/ops/list_staff_options', { body: {} }),
      customerID ? api.POST('/api/ops/get_customer', { body: { id: customerID } }) : Promise.resolve(null)
    ])
    if (!current()) return
    if (!i.data || !st.data) {
      toast.add({ title: errorText(i.error ?? st.error), color: 'error' })
      open.value = false
      return
    }
    items.value = i.data.items
    staffOptions.value = st.data.staff
    if (!c) return
    if (c.data) {
      customerName.value = c.data.name ?? ''
      customerState.value = 'ok'
    } else if (c.response.status === 404) {
      customerState.value = 'missing'
    }
  })
}

function orderSummary(lines: { item_id: string }[]) {
  const names = lines.map(l => items.value.find(i => i.id === l.item_id)?.name ?? '项目').join('、')
  const who = { none: '未识别顾客', ok: customerName.value || '未留姓名的顾客', missing: '顾客找不到', unread: '草稿里的顾客' }[customerState.value]
  return `开单：${who}，${names}${designated.value ? '，指定' : ''}`
}

async function save() {
  const target = draft.value
  if (!target) return
  let params: Record<string, unknown>
  let summary: string
  if (isOrder.value) {
    const lines = orderItemsInput(rows.value, items.value)
    if (!lines.length) {
      toast.add({ title: '请选择项目', color: 'warning' })
      return
    }
    params = { ...(target.params as object), gender: gender.value, designated: designated.value, items: lines }
    summary = orderSummary(lines)
  } else {
    const n = name.value.trim()
    if (!n && !phone.value.trim()) {
      toast.add({ title: '姓名和手机号至少填一个', color: 'warning' })
      return
    }
    params = { ...(target.params as object), name: n || undefined, phone: phone.value.trim() || undefined, gender: gender.value, note: note.value.trim() || undefined }
    summary = `新建顾客：${n || '未填姓名'}，${gender.value === 'male' ? '男' : '女'}`
  }
  await form.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/update_draft_params', { body: { id: target.id, params, summary } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    open.value = false
    toast.add({ title: '已修改，请核对后确认', color: 'success' })
    emit('saved')
  })
}

defineExpose({ begin })
</script>

<template>
  <UModal v-model:open="open" :title="isOrder ? '修改开单草稿' : '修改新建顾客草稿'" description="改完回到卡片上核对，再点确认才会执行" :ui="{ content: isOrder ? 'sm:max-w-[600px]' : undefined }">
    <template #body>
      <div v-if="isOrder" class="space-y-5">
        <div class="space-y-3 rounded-card bg-side p-4 ring-1 ring-default ring-inset">
          <div class="flex items-center gap-3">
            <div class="min-w-0 flex-1">
              <p class="text-meta text-muted">顾客</p>
              <p v-if="customerState === 'missing'" class="font-medium text-ink-orange" data-testid="draft-edit-customer-missing">草稿里的顾客找不到，确认可能会失败</p>
              <p v-else-if="customerState === 'unread'" class="font-medium text-muted">草稿里的顾客（没能读取姓名）</p>
              <p v-else class="truncate font-medium" :class="customerName ? 'text-default' : 'text-muted'" data-testid="draft-edit-customer">{{ customerState === 'ok' ? customerName || '未留姓名的顾客' : '未识别顾客' }}</p>
            </div>
            <MethodPicker v-model="gender" label="顾客性别" :items="genderItems" />
          </div>
          <div class="border-t border-default pt-3">
            <USwitch v-model="designated" label="指定" />
          </div>
        </div>
        <ListSkeleton v-if="loading" :rows="2" />
        <OrderItemRows v-else v-model="rows" :items="itemChoices" :staff-items="staffItems" />
      </div>
      <div v-else class="space-y-5">
        <UFormField label="姓名"><UInput v-model="name" class="w-full" aria-label="姓名" /></UFormField>
        <UFormField label="手机号" :help="phoneMasked ? '号码已打码，不改就保留原号码' : undefined">
          <UInput v-model="phone" class="w-full" inputmode="tel" aria-label="手机号" />
        </UFormField>
        <UFormField label="性别"><MethodPicker v-model="gender" label="性别" :items="genderItems" /></UFormField>
        <UFormField label="备注"><UTextarea v-model="note" :rows="2" class="w-full" aria-label="备注" /></UFormField>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="open = false">
        <UButton :loading="saving" :disabled="loading" data-testid="draft-edit-save" @click="save">保存修改</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
