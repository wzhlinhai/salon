<script setup lang="ts">
import type { components } from '~/utils/api'

type Customer = components['schemas']['Customer']
type LabelInput = components['schemas']['AddSafetyLabelInput']

const props = defineProps<{
  id: string
  sources: components['schemas']['CustomerSource'][]
}>()
const emit = defineEmits<{
  close: []
  open: [id: string]
  changed: []
}>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const NONE = 'none'

// 详情资料
const customer = ref<Customer | null>(null)
const loadError = ref('')
const today = ref('')
const editing = ref(false)
const profile = reactive({
  name: '',
  gender: NONE,
  source: NONE,
  note: ''
})
const customerGuard = useRequestGuard()
const savingIds = ref(new Set<string>())
const saving = computed(() => savingIds.value.has(props.id))
const genderItems = [
  { label: '未填写', value: NONE },
  { label: '男', value: 'male' },
  { label: '女', value: 'female' }
]
const genderName = (value: string | null) => value === 'male' ? '男' : value === 'female' ? '女' : '未填写'
const customerName = (c: Customer) => c.unidentified ? '未识别顾客' : c.name || '未填写姓名'
const sourceItems = computed(() => [
  { label: '不设来源', value: NONE },
  ...props.sources
    .filter(s => s.active || s.id === customer.value?.source?.id)
    .map(s => ({ label: s.name + (s.active ? '' : '（已停用）'), value: s.id }))
])
const detailGuard = useRequestGuard()

async function load() {
  const id = props.id
  const request = detailGuard.next()
  loadError.value = ''
  if (can('audit_log.view')) logs.reload()
  else logs.clear()
  const res = await api.POST('/api/ops/get_customer', { body: { id } })
  if (id !== props.id || !request()) return
  if (res.data) {
    customer.value = res.data
  } else {
    loadError.value = errorText(res.error)
    toast.add({ title: loadError.value, color: 'error' })
  }
}

// 按发起时的顾客保存：成功总是刷新列表，仅当前顾客提示并执行后续操作（默认重载详情）。
async function act<R>(
  request: (id: string) => Promise<{ data?: R, error?: unknown }>,
  message: string,
  onDone: (data: R) => void = () => {
    load()
  }
): Promise<boolean> {
  const id = props.id
  if (savingIds.value.has(id)) return false
  const current = customerGuard.hold()
  savingIds.value.add(id)
  try {
    const { data, error } = await request(id)
    if (data) emit('changed')
    if (id !== props.id || !current()) {
      if (!data) toast.add({ title: errorText(error), color: 'error' })
      return false
    }
    toast.add({ title: data ? message : errorText(error), color: data ? 'success' : 'error' })
    if (data) onDone(data)
    return !!data
  } finally {
    savingIds.value.delete(id)
  }
}

function edit() {
  const c = customer.value!
  Object.assign(profile, {
    name: c.name ?? '',
    gender: c.gender ?? NONE,
    source: c.source?.id ?? NONE,
    note: c.note ?? ''
  })
  editing.value = true
}

async function saveProfile() {
  if (await act(id => api.POST('/api/ops/update_customer_profile', {
    body: {
      id,
      name: profile.name || undefined,
      gender: profile.gender === NONE ? undefined : profile.gender as 'male' | 'female',
      source_id: profile.source === NONE ? undefined : profile.source,
      note: profile.note || undefined
    }
  }), '资料已保存')) {
    editing.value = false
  }
}

function setDetailOpen(open: boolean) {
  if (!open) {
    emit('close')
  }
}

// 手机号
const phoneOpen = ref(false)
const phone = ref('')

function openPhone() {
  phone.value = ''
  phoneOpen.value = true
}

async function savePhone() {
  const path = customer.value?.phone ? '/api/ops/change_customer_phone' : '/api/ops/add_customer_phone'
  if (await act(id => api.POST(path, { body: { id, phone: phone.value } }), '手机号已保存')) {
    phoneOpen.value = false
  }
}

// 安全标签
const labelOpen = ref(false)
const label = reactive({
  kind: 'allergy' as LabelInput['kind'],
  source: 'customer_said' as LabelInput['source'],
  observed_on: '',
  note: ''
})
const revokeTarget = ref<components['schemas']['SafetyLabel'] | null>(null)
const labelNames: Record<string, string> = {
  allergy: '过敏',
  scalp_sensitive: '头皮敏感',
  recent_chemical: '近期烫染'
}
const labelSources: Record<string, string> = {
  customer_said: '顾客自述',
  staff_observed: '员工观察'
}

function openLabel() {
  Object.assign(label, {
    kind: 'allergy',
    source: 'customer_said',
    observed_on: today.value,
    note: ''
  })
  labelOpen.value = true
}

async function addLabel() {
  if (await act(id => api.POST('/api/ops/add_safety_label', {
    body: { ...label, customer_id: id, note: label.note || undefined }
  }), '安全标签已添加')) {
    labelOpen.value = false
  }
}

function setRevokeOpen(open: boolean) {
  if (!open) {
    revokeTarget.value = null
  }
}

async function revoke() {
  const target = revokeTarget.value
  if (!target) return
  if (await act(id => api.POST('/api/ops/revoke_safety_label', {
    body: { customer_id: id, id: target.id }
  }), '安全标签已撤销')) {
    revokeTarget.value = null
  }
}

// 合并
const mergeOpen = ref(false)
const mergeQuery = ref('')
const mergeOffset = ref(0)
const mergeHasNext = ref(false)
const candidates = ref<Customer[]>([])
const other = ref<Customer | null>(null)
const keepID = ref('')
const mergeConfirm = ref(false)
const searching = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
const searchGuard = useRequestGuard()

function openMerge() {
  mergeQuery.value = ''
  mergeOffset.value = 0
  candidates.value = []
  other.value = null
  keepID.value = props.id
  mergeConfirm.value = false
  mergeOpen.value = true
  searchMerge()
}

async function searchMerge() {
  clearTimeout(timer)
  const current = searchGuard.next()
  searching.value = true
  try {
    const res = await api.POST('/api/ops/search_customers', {
      body: {
        query: mergeQuery.value,
        include_unidentified: true,
        limit: 50,
        offset: mergeOffset.value
      }
    })
    if (!current()) return
    if (res.data) {
      candidates.value = res.data.items.filter(c => c.id !== props.id)
      mergeHasNext.value = res.data.items.length === 50
    } else {
      toast.add({ title: errorText(res.error), color: 'error' })
    }
  } finally {
    if (current()) searching.value = false
  }
}

function searchCandidates() {
  mergeOffset.value = 0
  searchMerge()
}

function changeMergePage(change: number) {
  mergeOffset.value += change * 50
  searchMerge()
}

function selectCandidate(c: Customer) {
  other.value = c
  keepID.value = props.id
}

watch(mergeQuery, () => {
  searchGuard.invalidate()
  clearTimeout(timer)
  candidates.value = []
  other.value = null
  keepID.value = props.id
  timer = setTimeout(searchCandidates, 300)
})

async function merge() {
  const target = other.value
  if (!target) return
  await act(id => api.POST('/api/ops/merge_customers', {
    body: {
      keep_id: keepID.value,
      merged_id: keepID.value === id ? target.id : id
    }
  }), '顾客已合并', (data) => {
    mergeConfirm.value = false
    mergeOpen.value = false
    emit('open', data.keep.id)
    if (data.keep.id === props.id) load()
  })
}

type CustomerCard = components['schemas']['Card']
const cardCards = ref<CustomerCard[]>([])
const cardLoading = ref(false)
const cardListGuard = useRequestGuard()

async function cardLoad() {
  const id = props.id
  const current = cardListGuard.next()
  cardLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_customer_cards', { body: { customer_id: id } })
    if (id !== props.id || !current()) return
    if (data) cardCards.value = data.items
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) cardLoading.value = false
  }
}

const cardTypes = ref<components['schemas']['CardType'][]>([])
const cardStaff = ref<components['schemas']['StaffOption'][]>([])
const cardModalGuard = useRequestGuard()
const cardModal = ref(false)
const cardTarget = ref<CustomerCard | null>(null)
const cardCardType = ref('')
const openingType = computed(() => cardTypes.value.find(t => t.id === cardCardType.value))
const cardPrincipal = ref('0.00')
const cardBonus = ref('0.00')
const cardSales = ref<string[]>([])
const cardSaving = ref(false)
const cardOptionsLoading = ref(false)

async function cardEdit(card: CustomerCard | null) {
  const id = props.id
  const current = cardModalGuard.next()
  cardSaving.value = false
  cardTarget.value = card
  cardPrincipal.value = '0.00'
  cardBonus.value = '0.00'
  cardSales.value = []
  cardCardType.value = ''
  cardTypes.value = []
  cardStaff.value = []
  cardModal.value = true
  cardOptionsLoading.value = true
  try {
    const [t, s] = await Promise.all([
      api.POST('/api/ops/list_card_types', { body: {} }),
      api.POST('/api/ops/list_staff_options', { body: {} })
    ])
    if (id !== props.id || !current()) return
    if (t.data && s.data) {
      cardTypes.value = t.data.items.filter(t => t.active)
      cardStaff.value = s.data.staff.filter(s => s.active)
    } else toast.add({ title: errorText(t.error ?? s.error), color: 'error' })
  } finally {
    if (current()) cardOptionsLoading.value = false
  }
}

watch(cardModal, (value) => {
  if (!value) cardModalGuard.next()
})

async function cardSave() {
  if (cardSaving.value) return
  const id = props.id
  const current = cardModalGuard.hold()
  const countsOnly = !cardTarget.value && openingType.value?.kind === 'count'
  const body = { principal: countsOnly ? '0.00' : cardPrincipal.value, bonus: countsOnly ? '0.00' : cardBonus.value, sales_staff_ids: [...cardSales.value] }
  cardSaving.value = true
  try {
    const { data, error } = cardTarget.value
      ? await api.POST('/api/ops/recharge_card', { body: { ...body, card_id: cardTarget.value.id } })
      : await api.POST('/api/ops/open_card', { body: { ...body, customer_id: id, card_type_id: cardCardType.value } })
    if (id !== props.id || !current()) return
    if (data) {
      const upgradedTo = (data as components['schemas']['RechargeResult']).upgraded_to
      if (upgradedTo) toast.add({ title: `充值成功，已自动升级为 ${upgradedTo.name}`, color: 'success' })
      else toast.add({ title: cardTarget.value ? '充值成功' : '开卡成功', color: 'success' })
      cardModal.value = false
      await cardLoad()
      if (upgradeCardID.value === data.id) await upgrades.reload()
      if (pointsCard.value === data.id) await cardPoints.reload()
      if (cardLedgerCard.value === data.id) await cardLedger.reload()
    } else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current() || !cardModal.value) cardSaving.value = false
  }
}

const pointsCard = ref('')
const cardPoints = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_points', { body: { card_id: pointsCard.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function showPoints(id: string) {
  pointsCard.value = id
  cardPoints.reload()
}

const cardLedgerCard = ref('')
const cardLedger = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_ledger', { body: { card_id: cardLedgerCard.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function cardShowLedger(id: string) {
  cardLedgerCard.value = id
  cardLedger.reload()
}

const countCard = ref('')
const cardCounts = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_counts', { body: { card_id: countCard.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function showCounts(id: string) {
  countCard.value = id
  cardCounts.reload()
}

const adjustmentCard = ref('')
const adjustments = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_adjustments', { body: { card_id: adjustmentCard.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function showAdjustments(id: string) {
  adjustmentCard.value = id
  adjustments.reload()
}

const adjustModal = ref(false)
const adjustGuard = useRequestGuard()
const adjustTarget = ref<CustomerCard | null>(null)
const adjustPoints = ref(0)
const adjustPrincipal = ref('')
const adjustBonus = ref('')
const adjustExpires = ref('')
const adjustUnlimited = ref(false)
const adjustType = ref('')
const adjustReason = ref('')
const adjustCounts = ref<(components['schemas']['CardCountTarget'] & { name: string })[]>([])
const adjustTypes = ref<components['schemas']['CardType'][]>([])
const adjustSaving = ref(false)
const adjustLoading = ref(false)

async function editAdjustment(card: CustomerCard) {
  const customerID = props.id
  const current = adjustGuard.next()
  adjustTarget.value = card
  adjustCounts.value = card.items.map(x => ({ item_id: x.item_id, paid: x.paid_remaining, gift: x.gift_remaining, name: x.item_name }))
  adjustPoints.value = card.points
  adjustPrincipal.value = card.principal
  adjustBonus.value = card.bonus
  adjustExpires.value = card.expires_on || ''
  adjustUnlimited.value = !card.expires_on
  adjustType.value = card.card_type_id
  adjustReason.value = ''
  adjustTypes.value = []
  adjustSaving.value = false
  adjustModal.value = true
  adjustLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_card_types', { body: {} })
    if (customerID !== props.id || !current()) return
    if (data) {
      const kind = data.items.find(t => t.id === card.card_type_id)?.kind
      adjustTypes.value = data.items.filter(t => t.id === card.card_type_id || (t.active && t.kind === kind))
    } else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) adjustLoading.value = false
  }
}

watch(adjustModal, (value) => {
  if (!value) adjustGuard.next()
})

async function saveAdjustment() {
  const card = adjustTarget.value
  if (!card || adjustSaving.value) return
  const customerID = props.id
  const current = adjustGuard.hold()
  const body: components['schemas']['AdjustCardInput'] = { card_id: card.id, reason: adjustReason.value.trim() }
  const changedCounts = adjustCounts.value.filter((x) => {
    const old = card.items.find(i => i.item_id === x.item_id)!
    return Number(x.paid) !== old.paid_remaining || Number(x.gift) !== old.gift_remaining
  }).map(x => ({ item_id: x.item_id, paid: Number(x.paid), gift: Number(x.gift) }))
  if (changedCounts.length) body.counts = changedCounts
  if (Number(adjustPoints.value) !== card.points) body.points = Number(adjustPoints.value)
  if (adjustPrincipal.value !== card.principal) body.principal = adjustPrincipal.value
  if (adjustBonus.value !== card.bonus) body.bonus = adjustBonus.value
  const expires = adjustUnlimited.value ? null : adjustExpires.value
  if (expires !== (card.expires_on || null)) body.expiry = { expires_on: expires }
  if (adjustType.value !== card.card_type_id) body.card_type_id = adjustType.value
  adjustSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/adjust_card', { body })
    if (customerID !== props.id || !current()) return
    if (data) {
      adjustModal.value = false
      await cardLoad()
      if (pointsCard.value === card.id) await cardPoints.reload()
      if (cardLedgerCard.value === card.id) await cardLedger.reload()
      if (adjustmentCard.value === card.id) await adjustments.reload()
      if (countCard.value === card.id) await cardCounts.reload()
    } else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current() || !adjustModal.value) adjustSaving.value = false
  }
}

// 升级与转余额
const upgradeCardID = ref('')
const upgrades = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_upgrades', { body: { card_id: upgradeCardID.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function showUpgrades(id: string) {
  upgradeCardID.value = id
  upgrades.reload()
}

const balanceGuard = useRequestGuard()
const balanceOpen = ref(false)
const balanceMode = ref<'upgrade' | 'transfer'>('upgrade')
const balanceCard = ref<CustomerCard | null>(null)
const balanceTo = ref('')
const balancePrincipal = ref('0.00')
const balanceBonus = ref('0.00')
const balanceSales = ref<string[]>([])
const balanceReason = ref('')
const balanceTypes = ref<components['schemas']['CardType'][]>([])
const balanceStaff = ref<components['schemas']['StaffOption'][]>([])
const balanceLoading = ref(false)
const balanceSaving = ref(false)
const hasDifference = computed(() => Number(balancePrincipal.value) > 0 || Number(balanceBonus.value) > 0)
const transferTargets = computed(() => cardCards.value.filter(c => c.id !== balanceCard.value?.id && c.kind !== 'count'))

async function editBalance(card: CustomerCard, mode: 'upgrade' | 'transfer') {
  const id = props.id
  const current = balanceGuard.next()
  balanceCard.value = card
  balanceMode.value = mode
  balanceTo.value = ''
  balancePrincipal.value = '0.00'
  balanceBonus.value = '0.00'
  balanceSales.value = []
  balanceReason.value = ''
  balanceTypes.value = []
  balanceStaff.value = []
  balanceSaving.value = false
  balanceLoading.value = false
  balanceOpen.value = true
  if (mode === 'transfer') return
  balanceLoading.value = true
  try {
    const [types, staff] = await Promise.all([
      api.POST('/api/ops/list_card_types', { body: {} }),
      api.POST('/api/ops/list_staff_options', { body: {} })
    ])
    if (id !== props.id || !current()) return
    if (types.data && staff.data) {
      balanceTypes.value = types.data.items.filter(t => t.active && t.kind === 'stored_value' && t.id !== card.card_type_id)
      balanceStaff.value = staff.data.staff.filter(s => s.active)
    } else toast.add({ title: errorText(types.error ?? staff.error), color: 'error' })
  } finally {
    if (current()) balanceLoading.value = false
  }
}

watch(balanceOpen, (value) => {
  if (!value) balanceGuard.next()
})

async function saveBalance() {
  const card = balanceCard.value
  if (!card || balanceSaving.value) return
  const id = props.id
  const current = balanceGuard.hold()
  const amounts = { principal: balancePrincipal.value, bonus: balanceBonus.value }
  balanceSaving.value = true
  try {
    const { data, error } = balanceMode.value === 'upgrade'
      ? await api.POST('/api/ops/upgrade_card', { body: { ...amounts, card_id: card.id, to_type_id: balanceTo.value, sales_staff_ids: hasDifference.value ? [...balanceSales.value] : [] } })
      : await api.POST('/api/ops/transfer_card_balance', { body: { ...amounts, from_card_id: card.id, to_card_id: balanceTo.value, reason: balanceReason.value.trim() } })
    if (id !== props.id || !current()) return
    if (data) {
      balanceOpen.value = false
      await cardLoad()
      if (pointsCard.value === card.id) await cardPoints.reload()
      if (cardLedgerCard.value) await cardLedger.reload()
      if (upgradeCardID.value === card.id) await upgrades.reload()
    } else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current() || !balanceOpen.value) balanceSaving.value = false
  }
}

const ledgerKinds: Record<string, string> = { open: '开卡', recharge: '充值', adjust: '调整', upgrade: '升级', transfer_out: '转出', transfer_in: '转入' }

// 操作日志
const logs = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_audit_log', {
    body: { target_id: props.id, before_id: beforeId }
  })
  return { data: data?.entries, error }
})

const actions: Record<string, string> = {
  create_customer: '新建顾客',
  update_customer_profile: '修改资料',
  add_customer_phone: '补手机号',
  change_customer_phone: '改手机号',
  add_safety_label: '添加安全标签',
  revoke_safety_label: '撤销安全标签',
  merge_customers: '合并顾客',
  assign_customer_responsible: '分配负责人',
  hand_over_customers: '负责人交接'
}

function snapshot(value: unknown): string {
  if (!value || typeof value !== 'object') return '无'
  const c = value as Customer
  const labels = c.active_safety_labels?.map(l =>
    `${labelNames[l.kind]}（${labelSources[l.source]}，${l.observed_on}，${l.note || '无备注'}）`
  ).join('、')
  return [
    c.unidentified ? '未识别顾客' : c.name || '未填写姓名',
    c.phone || '未留手机号',
    genderName(c.gender),
    `来源：${c.source?.name || '未填写'}`,
    `负责人：${c.responsible?.name || '不设'}`,
    `备注：${c.note || '无'}`,
    `安全标签：${labels || '无'}`,
    ...(c.merged_into ? ['已合并到另一位顾客'] : [])
  ].join('；')
}

// 读取档案及营业日，切换顾客时关闭编辑入口
watch(() => props.id, () => {
  customerGuard.next()
  upgradeCardID.value = ''
  upgrades.clear()
  balanceGuard.next()
  balanceOpen.value = false
  balanceSaving.value = false
  balanceLoading.value = false
  adjustmentCard.value = ''
  adjustments.clear()
  countCard.value = ''
  cardCounts.clear()
  adjustGuard.next()
  adjustModal.value = false
  adjustSaving.value = false
  adjustLoading.value = false
  cardListGuard.invalidate()
  cardModalGuard.next()
  cardModal.value = false
  cardSaving.value = false
  cardOptionsLoading.value = false
  cardCards.value = []
  cardLedgerCard.value = ''
  pointsCard.value = ''
  cardPoints.clear()
  cardLedger.clear()
  if (can('customer.view')) cardLoad()
  customer.value = null
  editing.value = false
  phoneOpen.value = false
  labelOpen.value = false
  mergeOpen.value = false
  mergeConfirm.value = false
  revokeTarget.value = null
  other.value = null
  candidates.value = []
  searchGuard.invalidate()
  clearTimeout(timer)
  load()
})

const businessDateGuard = useRequestGuard()

onMounted(async () => {
  const current = businessDateGuard.next()
  load()
  if (can('customer.view')) cardLoad()
  const res = await api.POST('/api/ops/get_settings', { body: {} })
  if (!current()) return
  if (res.data) {
    today.value = res.data.business_date
  } else {
    toast.add({ title: errorText(res.error), color: 'error' })
  }
})

onBeforeUnmount(() => {
  clearTimeout(timer)
})
</script>

<template>
  <USlideover :open="true" title="顾客详情" :ui="{ content: 'sm:max-w-3xl' }" @update:open="setDetailOpen">
    <template #body>
      <div v-if="customer" class="space-y-6">
        <UAlert v-if="customer.merged_into" title="已合并到另一位顾客" color="warning">
          <template #description>
            <UButton @click="emit('open', customer.merged_into)">查看保留的顾客</UButton>
          </template>
        </UAlert>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-lg font-semibold">{{ customerName(customer) }}</h2>
          <div v-if="!customer.merged_into" class="flex gap-2">
            <UButton v-if="can('customer.edit')" variant="outline" @click="edit">修改资料</UButton>
            <UButton v-if="can('customer.merge')" variant="outline" @click="openMerge">合并重复顾客</UButton>
          </div>
        </div>
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          <dt>姓名</dt>
          <dd>{{ customer.name || '未填写' }}</dd>
          <dt>手机号</dt>
          <dd class="flex items-center gap-2">
            {{ customer.phone || '未填写' }}
            <UButton v-if="!customer.merged_into && ((!customer.phone && can('customer.edit')) || (customer.phone && can('customer.merge')))" size="xs" variant="link" @click="openPhone">{{ customer.phone ? '改手机号' : '补手机号' }}</UButton>
          </dd>
          <dt>性别</dt>
          <dd>{{ genderName(customer.gender) }}</dd>
          <dt>来源</dt>
          <dd>{{ customer.source?.name || '未填写' }}</dd>
          <dt>负责人</dt>
          <dd>{{ customer.responsible?.name || '不设负责人' }}</dd>
          <dt>备注</dt>
          <dd class="whitespace-pre-wrap">{{ customer.note || '无' }}</dd>
          <dt>建档时间</dt>
          <dd>{{ new Date(customer.created_at).toLocaleString('zh-CN') }}</dd>
        </dl>
        <UCard>
          <template #header>
            <div class="flex justify-between items-center">
              <h3 class="font-medium">安全标签</h3>
              <UButton v-if="!customer.merged_into && can('customer.safety_label')" :disabled="!today" size="sm" @click="openLabel">添加安全标签</UButton>
            </div>
          </template>
          <p v-if="!customer.active_safety_labels.length" class="text-sm text-muted">暂无有效安全标签</p>
          <div v-for="l in customer.active_safety_labels" :key="l.id" class="py-2 flex items-start justify-between gap-3">
            <div class="space-y-1">
              <UBadge color="error">{{ labelNames[l.kind] }}</UBadge>
              <p class="text-sm">{{ labelSources[l.source] }} · {{ l.observed_on }}</p>
              <p class="whitespace-pre-wrap text-sm">{{ l.note || '无备注' }}</p>
            </div>
            <UButton v-if="!customer.merged_into && can('customer.safety_label')" size="xs" color="error" variant="ghost" @click="revokeTarget = l">撤销</UButton>
          </div>
        </UCard>
        <div v-if="can('customer.view')">
          <UCard>
            <template #header>
              <div class="flex justify-between items-center">
                <h3 class="font-medium">卡</h3>
                <UButton v-if="can('card.sell') && !customer.merged_into" size="sm" @click="cardEdit(null)">开卡</UButton>
              </div>
            </template>
            <p v-if="cardLoading">正在读取卡…</p>
            <p v-else-if="!cardCards.length">暂无卡</p>
            <div v-for="card in cardCards" :key="card.id" :data-card="card.id" class="space-y-2 py-3 border-b border-default">
              <p>{{ card.card_type_name }} · 本金 {{ card.principal }} · 赠送金 {{ card.bonus }} · 积分 {{ card.points }} · 有效期 {{ card.expires_on || '不限' }}</p>
              <p v-for="item in card.items" :key="item.item_id" data-testid="remaining-counts">{{ item.item_name }} · 购买 {{ item.paid_remaining }} · 赠送 {{ item.gift_remaining }}</p>
              <p v-if="card.items.length">次数售价 {{ card.counts_price }}</p>
              <div class="flex gap-2">
                <UButton v-if="card.kind !== 'count' && can('card.sell') && !customer.merged_into" size="xs" variant="outline" @click="cardEdit(card)">充值</UButton>
                <UButton v-if="can('card.adjust')" size="xs" variant="outline" @click="editAdjustment(card)">调整</UButton>
                <UButton size="xs" variant="outline" @click="cardShowLedger(card.id)">流水</UButton>
                <UButton size="xs" variant="outline" @click="showPoints(card.id)">积分流水</UButton>
                <UButton v-if="card.items.length" size="xs" variant="outline" @click="showCounts(card.id)">次数流水</UButton>
                <UButton v-if="can('card.sell') && card.kind === 'stored_value'" size="xs" variant="outline" @click="editBalance(card, 'upgrade')">升级</UButton>
                <UButton v-if="can('card.adjust')" size="xs" variant="outline" @click="editBalance(card, 'transfer')">转余额</UButton>
                <UButton size="xs" variant="outline" @click="showUpgrades(card.id)">升级记录</UButton>
                <UButton size="xs" variant="outline" @click="showAdjustments(card.id)">调整记录</UButton>
              </div>
              <div v-if="upgradeCardID === card.id" data-testid="card-upgrades" class="space-y-2 text-sm">
                <p v-if="upgrades.loading.value">正在读取升级记录…</p>
                <p v-for="entry in upgrades.items.value" :key="entry.id" data-testid="card-upgrade-entry">{{ entry.type_before.name }} → {{ entry.type_after.name }} · {{ entry.trigger === 'auto' ? '自动升级' : '手动升级' }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }}</p>
                <p v-if="!upgrades.loading.value && !upgrades.items.value.length">暂无升级记录</p>
                <UButton v-if="upgrades.hasMore.value" :loading="upgrades.loading.value" variant="outline" @click="upgrades.loadMore">加载更多</UButton>
              </div>
              <div v-if="pointsCard === card.id" data-testid="card-points" class="space-y-2 text-sm">
                <p v-if="cardPoints.loading.value">正在读取积分流水…</p>
                <p v-for="entry in cardPoints.items.value" :key="entry.id" data-testid="card-points-entry">
                  {{ ledgerKinds[entry.kind] || entry.kind }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }}
                  积分 {{ entry.before }} + {{ entry.delta }} = {{ entry.after }} <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
                </p>
                <p v-if="!cardPoints.loading.value && !cardPoints.items.value.length">暂无积分流水</p>
                <UButton v-if="cardPoints.hasMore.value" :loading="cardPoints.loading.value" variant="outline" @click="cardPoints.loadMore">加载更多</UButton>
              </div>
              <div v-if="countCard === card.id" data-testid="card-counts" class="space-y-2 text-sm">
                <p v-if="cardCounts.loading.value">正在读取次数流水…</p>
                <p v-for="entry in cardCounts.items.value" :key="entry.id" data-testid="card-count-entry">
                  {{ entry.item.name }} · {{ entry.kind === 'open' ? '开卡' : '调整' }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }}
                  购买 {{ entry.paid_before }} + {{ entry.paid_delta }} = {{ entry.paid_after }}；赠送 {{ entry.gift_before }} + {{ entry.gift_delta }} = {{ entry.gift_after }}
                  <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
                </p>
                <UButton v-if="cardCounts.hasMore.value" :loading="cardCounts.loading.value" variant="outline" @click="cardCounts.loadMore">加载更多</UButton>
              </div>
              <div v-if="adjustmentCard === card.id" data-testid="card-adjustments" class="space-y-2 text-sm">
                <p v-if="adjustments.loading.value">正在读取调整记录…</p>
                <p v-for="entry in adjustments.items.value" :key="entry.id" data-testid="card-adjustment-entry">
                  {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }} · 原因：{{ entry.reason }}
                  <span v-for="change in entry.counts" :key="change.id">{{ change.item.name }}：购买 {{ change.paid_before }} → {{ change.paid_after }}；赠送 {{ change.gift_before }} → {{ change.gift_after }}</span>
                  <span v-if="entry.balance">本金 {{ entry.balance.principal_before }} + {{ entry.balance.principal_delta }} = {{ entry.balance.principal_after }}；赠送金 {{ entry.balance.bonus_before }} + {{ entry.balance.bonus_delta }} = {{ entry.balance.bonus_after }}</span>
                  <span v-if="entry.expiry_before && entry.expiry_after">有效期 {{ entry.expiry_before.expires_on || '不限' }} → {{ entry.expiry_after.expires_on || '不限' }}</span>
                  <span v-if="entry.card_type_before && entry.card_type_after">卡类型 {{ entry.card_type_before.name }} → {{ entry.card_type_after.name }}</span>
                </p>
                <UButton v-if="adjustments.hasMore.value" :loading="adjustments.loading.value" variant="outline" @click="adjustments.loadMore">加载更多</UButton>
              </div>
              <div v-if="cardLedgerCard === card.id" data-testid="card-ledger" class="space-y-2 text-sm">
                <p v-for="entry in cardLedger.items.value" :key="entry.id" data-testid="card-ledger-entry">
                  {{ ledgerKinds[entry.kind] || entry.kind }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }}
                  本金 {{ entry.principal_before }} + {{ entry.principal_delta }} = {{ entry.principal_after }}；赠送金 {{ entry.bonus_before }} + {{ entry.bonus_delta }} = {{ entry.bonus_after }}
                  <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
                  <span v-if="entry.other_card_id">· 对方卡：{{ entry.other_card_id }}</span>
                  <span v-if="entry.sales_staff.length">· 业绩归属人：{{ entry.sales_staff.map(s => s.name).join('、') }}</span>
                </p>
                <UButton v-if="cardLedger.hasMore.value" :loading="cardLedger.loading.value" variant="outline" @click="cardLedger.loadMore">加载更多</UButton>
              </div>
            </div>
          </UCard>
        </div>
        <UCard v-if="can('audit_log.view')">
          <template #header>
            <h3 class="font-medium">修改记录</h3>
          </template>
          <p v-if="!logs.items.value.length" class="text-sm text-muted">暂无修改记录</p>
          <div class="overflow-x-auto">
            <table v-if="logs.items.value.length" class="w-full text-sm">
              <thead class="text-left text-muted">
                <tr>
                  <th>时间</th>
                  <th>修改人</th>
                  <th>操作</th>
                  <th>改前</th>
                  <th>改后</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="entry in logs.items.value" :key="entry.id" class="border-t border-default align-top">
                  <td class="py-2">{{ new Date(entry.at).toLocaleString('zh-CN') }}</td>
                  <td>{{ entry.actor.name }}</td>
                  <td>{{ actions[entry.action] || entry.action }}</td>
                  <td class="min-w-48">{{ snapshot(entry.before) }}</td>
                  <td class="min-w-48">{{ snapshot(entry.after) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <UButton v-if="logs.hasMore.value" :loading="logs.loading.value" variant="outline" @click="logs.loadMore">加载更多</UButton>
        </UCard>
      </div>
      <div v-else-if="loadError" class="space-y-3">
        <p>{{ loadError }}</p>
        <UButton variant="outline" @click="load">重试</UButton>
      </div>
      <p v-else class="text-muted">正在读取顾客档案…</p>
    </template>
  </USlideover>
  <UModal v-model:open="balanceOpen" :title="balanceMode === 'upgrade' ? '升级' : '转余额'">
    <template #body>
      <div class="space-y-3">
        <template v-if="balanceMode === 'upgrade'">
          <UFormField label="目标卡类型"><USelect v-model="balanceTo" :items="balanceTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
          <p>补差价（可为零），有效期不变。</p>
        </template>
        <UFormField v-else label="目标卡"><USelect v-model="balanceTo" :items="transferTargets.map(c => ({ label: `${c.card_type_name} · ${c.id}`, value: c.id }))" class="w-full" /></UFormField>
        <UFormField label="本金"><UInput v-model="balancePrincipal" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField label="赠送金"><UInput v-model="balanceBonus" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField v-if="balanceMode === 'upgrade' && hasDifference" label="业绩归属人"><USelect v-model="balanceSales" multiple :items="balanceStaff.map(s => ({ label: s.name, value: s.id }))" class="w-full" /></UFormField>
        <UFormField v-if="balanceMode === 'transfer'" label="原因" required><UTextarea v-model="balanceReason" :maxlength="200" class="w-full" /></UFormField>
      </div>
    </template>
    <template #footer><UButton :loading="balanceSaving" :disabled="balanceLoading || !balanceTo || (balanceMode === 'upgrade' && hasDifference && !balanceSales.length) || (balanceMode === 'transfer' && (!hasDifference || !balanceReason.trim()))" @click="saveBalance">保存</UButton></template>
  </UModal>
  <UModal v-model:open="adjustModal" title="调整">
    <template #body>
      <div class="space-y-3">
        <UFormField label="积分"><UInput v-model="adjustPoints" type="number" :min="0" :max="99999999" :step="1" /></UFormField>
        <UFormField label="本金"><UInput v-model="adjustPrincipal" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField label="赠送金"><UInput v-model="adjustBonus" inputmode="decimal" class="w-full" /></UFormField>
        <div v-for="row in adjustCounts" :key="row.item_id" data-testid="adjust-count-item">
          <p>{{ row.name }}</p>
          <UFormField :label="`${row.name}购买次数`"><UInput v-model="row.paid" type="number" :min="0" /></UFormField>
          <UFormField :label="`${row.name}赠送次数`"><UInput v-model="row.gift" type="number" :min="0" /></UFormField>
        </div>
        <UCheckbox v-model="adjustUnlimited" label="不限" />
        <UFormField v-if="!adjustUnlimited" label="有效期"><UInput v-model="adjustExpires" type="date" class="w-full" /></UFormField>
        <UFormField label="卡类型"><USelect v-model="adjustType" :items="adjustTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
        <UFormField label="原因" required><UTextarea v-model="adjustReason" :maxlength="200" class="w-full" /></UFormField>
      </div>
    </template>
    <template #footer><UButton :loading="adjustSaving" :disabled="adjustLoading || !adjustReason.trim() || (!adjustUnlimited && !adjustExpires)" @click="saveAdjustment">保存</UButton></template>
  </UModal>
  <UModal v-model:open="cardModal" :title="cardTarget ? '充值' : '开卡'">
    <template #body>
      <div class="space-y-3">
        <UFormField v-if="!cardTarget" label="卡类型"><USelect v-model="cardCardType" :items="cardTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
        <UFormField v-if="cardTarget || openingType?.kind !== 'count'" label="本金"><UInput v-model="cardPrincipal" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField v-if="cardTarget || openingType?.kind !== 'count'" label="赠送金"><UInput v-model="cardBonus" inputmode="decimal" class="w-full" /></UFormField>
        <p v-for="item in openingType?.items ?? []" :key="item.item_id">{{ item.item_name }} · 购买 {{ item.paid_count }} · 赠送 {{ item.gift_count }} · 单次金额 {{ item.unit_price }}</p>
        <UFormField label="业绩归属人"><USelect v-model="cardSales" multiple :items="cardStaff.map(s => ({ label: s.name, value: s.id }))" class="w-full" /></UFormField>
      </div>
    </template>
    <template #footer><UButton :loading="cardSaving" :disabled="cardOptionsLoading || !cardSales.length || (!cardTarget && !cardCardType)" @click="cardSave">保存</UButton></template>
  </UModal>

  <UModal v-model:open="editing" title="修改顾客资料">
    <template #body>
      <div class="space-y-3">
        <UFormField label="姓名">
          <UInput v-model="profile.name" class="w-full" />
        </UFormField>
        <UFormField label="性别">
          <USelect v-model="profile.gender" :items="genderItems" class="w-full" />
        </UFormField>
        <UFormField label="顾客来源">
          <USelect v-model="profile.source" :items="sourceItems" class="w-full" />
        </UFormField>
        <UFormField label="备注">
          <UTextarea v-model="profile.note" class="w-full" />
        </UFormField>
      </div>
    </template>
    <template #footer>
      <UButton :loading="saving" @click="saveProfile">保存</UButton>
    </template>
  </UModal>
  <UModal v-model:open="phoneOpen" :title="customer?.phone ? '改手机号' : '补手机号'">
    <template #body>
      <UFormField label="手机号">
        <UInput v-model="phone" type="tel" class="w-full" />
      </UFormField>
    </template>
    <template #footer>
      <UButton :loading="saving" @click="savePhone">保存</UButton>
    </template>
  </UModal>
  <UModal v-model:open="labelOpen" title="添加安全标签">
    <template #body>
      <div class="space-y-3">
        <UFormField label="类型">
          <USelect v-model="label.kind" :items="Object.entries(labelNames).map(([value, label]) => ({ value, label }))" class="w-full" />
        </UFormField>
        <UFormField label="来源">
          <USelect v-model="label.source" :items="Object.entries(labelSources).map(([value, label]) => ({ value, label }))" class="w-full" />
        </UFormField>
        <UFormField label="观察日期">
          <UInput v-model="label.observed_on" type="date" :max="today" />
        </UFormField>
        <UFormField label="备注">
          <UTextarea v-model="label.note" class="w-full" />
        </UFormField>
      </div>
    </template>
    <template #footer>
      <UButton :loading="saving" :disabled="!label.observed_on || label.observed_on > today" @click="addLabel">保存</UButton>
    </template>
  </UModal>
  <UModal :open="!!revokeTarget" title="撤销安全标签" @update:open="setRevokeOpen">
    <template #body>确认撤销“{{ labelNames[revokeTarget?.kind ?? ''] }}”安全标签？历史记录仍然保留。</template>
    <template #footer>
      <UButton :loading="saving" color="error" @click="revoke">确认撤销</UButton>
    </template>
  </UModal>
  <UModal v-model:open="mergeOpen" title="合并重复顾客" :ui="{ content: 'sm:max-w-2xl' }">
    <template #body>
      <div class="space-y-4">
        <UFormField label="搜索另一位顾客">
          <UInput v-model="mergeQuery" placeholder="手机号、后四位或姓名" :loading="searching" class="w-full" @keydown.enter="searchCandidates" />
        </UFormField>
        <div class="max-h-48 overflow-y-auto space-y-1">
          <UButton v-for="c in candidates" :key="c.id" :data-customer-id="c.id" variant="outline" class="w-full" @click="selectCandidate(c)">{{ customerName(c) }} · {{ c.phone || '未留手机号' }} · {{ new Date(c.created_at).toLocaleString('zh-CN') }}</UButton>
        </div>
        <div class="flex items-center gap-3">
          <UButton variant="outline" :disabled="mergeOffset === 0 || searching" @click="changeMergePage(-1)">上一页</UButton>
          <span>第 {{ mergeOffset / 50 + 1 }} 页</span>
          <UButton variant="outline" :disabled="!mergeHasNext || searching" @click="changeMergePage(1)">下一页</UButton>
        </div>
        <p v-if="!searching && !candidates.length" class="text-sm text-muted">未找到其他顾客</p>
        <div v-if="customer && other" class="grid grid-cols-2 gap-3">
          <UCard v-for="c in [customer, other]" :key="c.id">
            <dl class="space-y-2 text-sm">
              <dt class="font-medium">{{ customerName(c) }}</dt>
              <dd>手机号：{{ c.phone || '未填写' }}</dd>
              <dd>性别：{{ genderName(c.gender) }}</dd>
              <dd>来源：{{ c.source?.name || '未填写' }}</dd>
              <dd>负责人：{{ c.responsible?.name || '不设' }}</dd>
              <dd class="whitespace-pre-wrap">备注：{{ c.note || '无' }}</dd>
              <dd>建档时间：{{ new Date(c.created_at).toLocaleString('zh-CN') }}</dd>
              <dd>
                <UBadge v-for="l in c.active_safety_labels" :key="l.id" color="error" class="mr-1">{{ labelNames[l.kind] }}</UBadge>
              </dd>
            </dl>
            <UButton class="mt-3" :variant="keepID === c.id ? 'solid' : 'outline'" :aria-pressed="keepID === c.id" @click="keepID = c.id">保留这位顾客</UButton>
          </UCard>
        </div>
      </div>
    </template>
    <template #footer>
      <UButton :disabled="!other" color="error" @click="mergeConfirm = true">继续合并</UButton>
    </template>
  </UModal>
  <UModal v-model:open="mergeConfirm" title="确认合并顾客">
    <template #body>
      <p>合并后不可撤销：被合并顾客的安全标签等记录归到保留的顾客名下</p>
      <p v-if="customer && other" class="mt-3">保留：{{ customerName(keepID === id ? customer : other) }}；被合并：{{ customerName(keepID === id ? other : customer) }}</p>
    </template>
    <template #footer>
      <UButton :loading="saving" color="error" @click="merge">确认合并</UButton>
    </template>
  </UModal>
</template>
