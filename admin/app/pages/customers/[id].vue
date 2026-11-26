<script setup lang="ts">
import type { components } from '~/utils/api'

// 顾客档案（DESIGN.md 顾客档案）：整页，左边是做过的头发（上次那一次是主角，可以照着开单），右边是卡、欠款、红包、记住的事。
// 页头自己画（头像、名字、一句话），外壳只在顶栏写「顾客 / 名字」。
definePageMeta({ permission: 'customer.view', title: '顾客', bare: true })

type Customer = components['schemas']['Customer']
type LabelInput = components['schemas']['AddSafetyLabelInput']
type Overview = components['schemas']['CustomerOverview']
type HistoryItem = components['schemas']['ServiceHistoryItem']
type MemoryField = 'hair_texture' | 'preferences' | 'dislikes' | 'note'

const route = useRoute()
const router = useRouter()
const crumbTitle = useState('page-crumb-title', () => '')
const id = computed(() => String(route.params.id))
const props = reactive({ id })
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
const sources = ref<components['schemas']['CustomerSource'][]>([])
const staffOptions = ref<components['schemas']['StaffOption'][]>([])
const genderItems = [
  { label: '未填写', value: NONE },
  { label: '男', value: 'male' },
  { label: '女', value: 'female' }
]
const genderName = (value: string | null) => value === 'male' ? '男' : value === 'female' ? '女' : '未填写'
const customerName = (c: Customer) => c.name || (c.unidentified ? '未识别顾客' : '未填写姓名')
const sourceItems = computed(() => [
  { label: '不设来源', value: NONE },
  ...sources.value
    .filter(s => s.active || s.id === customer.value?.source?.id)
    .map(s => ({ label: s.name + (s.active ? '' : '（已停用）'), value: s.id }))
])
const detailGuard = useRequestGuard()

// withHistory=false：只改了顾客资料（记住的事、负责人），做过的头发不用重取，免得「上次」闪一下、「更早」退回第一页。
// 做过的头发等读到顾客再取：已合并的顾客，记录都转到了保留的那位，后端按不存在回 404。
// 清了记录就欠一次重取：被后一次 load(false) 顶掉或断网，由当前这次在 finally 里还上，不会一直停在加载骨架。
let historyOwed = false

async function load(withHistory = true) {
  const id = props.id
  const request = detailGuard.next()
  loadError.value = ''
  if (can('audit_log.view')) logs.reload()
  else logs.clear()
  loadOverview()
  if (withHistory) {
    history.clear()
    historyOwed = true
  }
  let found: Customer | undefined
  try {
    const res = await api.POST('/api/ops/get_customer', { body: { id } })
    if (id !== props.id || !request()) return
    found = res.data
    if (res.data) {
      customer.value = res.data
      crumbTitle.value = customerName(res.data)
    } else {
      loadError.value = errorText(res.error)
      toast.add({ title: loadError.value, color: 'error' })
    }
  } finally {
    if (historyOwed && id === props.id && request()) {
      historyOwed = false
      if (found && !found.merged_into) history.reload()
      else history.loading.value = false
    }
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

// 资料是整体替换：没在改的字段原样带回去，免得被清空。
function profileBody(id: string, patch: Partial<Record<'name' | 'gender' | 'source' | MemoryField, string>>) {
  const c = customer.value!
  const value = (key: 'name' | MemoryField) => (key in patch ? patch[key] : c[key]) || undefined
  const gender = 'gender' in patch ? patch.gender : c.gender ?? NONE
  const source = 'source' in patch ? patch.source : c.source?.id ?? NONE
  return {
    id,
    name: value('name'),
    gender: gender === NONE ? undefined : gender as 'male' | 'female',
    source_id: source === NONE ? undefined : source,
    note: value('note'),
    hair_texture: value('hair_texture'),
    preferences: value('preferences'),
    dislikes: value('dislikes')
  }
}

async function saveProfile() {
  if (await act(id => api.POST('/api/ops/update_customer_profile', {
    body: profileBody(id, { name: profile.name, gender: profile.gender, source: profile.source, note: profile.note })
  }), '资料已保存', () => load(false))) {
    editing.value = false
  }
}

// 记住的事：点一条就地改，保存只改这一条。
const memoryFields: { key: MemoryField, label: string, empty: string }[] = [
  { key: 'hair_texture', label: '发质', empty: '还没记发质' },
  { key: 'preferences', label: '喜欢', empty: '还没记喜好' },
  { key: 'dislikes', label: '不要', empty: '还没记忌讳' },
  { key: 'note', label: '备注', empty: '没有备注' }
]
const memoryEditing = ref<MemoryField | null>(null)
const memoryDraft = ref('')

function editMemory(key: MemoryField) {
  memoryDraft.value = customer.value?.[key] ?? ''
  memoryEditing.value = key
}

async function saveMemory() {
  const key = memoryEditing.value
  if (!key) return
  if (await act(id => api.POST('/api/ops/update_customer_profile', {
    body: profileBody(id, { [key]: memoryDraft.value.trim() })
  }), '已记下', () => load(false))) {
    memoryEditing.value = null
  }
}

// 负责人：一句话里点名字就换，只有能分配负责人的人能点。
const responsibleItems = computed(() => [[
  { label: '不设负责人', onSelect: () => assignResponsible(null) },
  ...staffOptions.value.filter(s => s.active).map(s => ({ label: s.name, onSelect: () => assignResponsible(s.id) }))
]])

async function assignResponsible(staffID: string | null) {
  await act(id => api.POST('/api/ops/assign_customer_responsible', {
    body: { customer_ids: [id], staff_id: staffID ?? undefined }
  }), '负责人已改', () => load(false))
}

// 到店概况（列表同一份数据，按这一位取）：来过几次、多久一次、多久没来、下次预约。
const overview = ref<Overview | null>(null)
const overviewGuard = useRequestGuard()

async function loadOverview() {
  const current = overviewGuard.next()
  overview.value = null
  const { data } = await api.POST('/api/ops/list_customer_overview', { body: { customer_id: props.id, limit: 1 } })
  if (current() && data) overview.value = data.items.find(row => row.customer.id === props.id) ?? null
}

// 做过的头发：按单分组，第一张单是「上次」，其余是「更早」。
const HISTORY_PAGE = 20
const history = (() => {
  const items = ref<HistoryItem[]>([])
  const loading = ref(false)
  const hasMore = ref(false)
  const guard = useRequestGuard()

  async function fetch(offset: number) {
    const current = guard.next()
    loading.value = true
    try {
      const { data, error } = await api.POST('/api/ops/list_service_history', { body: { customer_id: props.id, limit: HISTORY_PAGE, offset } })
      if (!current()) return
      if (data) {
        items.value = offset ? [...items.value, ...data.items] : data.items
        hasMore.value = items.value.length < data.total
      } else toast.add({ title: errorText(error), color: 'error' })
    } finally {
      if (current()) loading.value = false
    }
  }

  return {
    items,
    loading,
    hasMore,
    clear: () => {
      guard.invalidate()
      items.value = []
      hasMore.value = false
      loading.value = true
    },
    reload: () => {
      items.value = []
      hasMore.value = false
      return fetch(0)
    },
    loadMore: () => fetch(items.value.length)
  }
})()

interface Visit { orderID: string, date: string, items: HistoryItem[], staff: string }
const visits = computed(() => {
  const list: Visit[] = []
  for (const item of history.items.value) {
    let visit = list.find(v => v.orderID === item.order_id)
    if (!visit) list.push(visit = { orderID: item.order_id, date: item.business_date, items: [], staff: '' })
    visit.items.push(item)
  }
  for (const visit of list) visit.staff = [...new Set(visit.items.flatMap(item => item.staff.map(s => s.name)))].join('、')
  return list
})
const lastVisit = computed(() => visits.value[0] ?? null)
const feedbackNames: Record<string, string> = { satisfied: '满意', neutral: '一般', unsatisfied: '不满意' }
const weekdays = ['日', '一', '二', '三', '四', '五', '六']
const weekday = (date: string) => `星期${weekdays[new Date(`${date}T12:00:00Z`).getUTCDay()]}`
const againLink = computed(() => lastVisit.value
  ? `/cashier?open=customer&customer=${props.id}&items=${[...new Set(lastVisit.value.items.map(item => item.item.id))].join(',')}`
  : '')

// 一句话里的到店节奏：来过几次、平时多久一次；比平时晚了就加一张黄签。
const rhythm = computed(() => {
  const o = overview.value
  if (!o) return ''
  if (!o.visit_count) return '还没来过'
  const cycle = o.cycle_days ? (o.cycle_days >= 14 ? `平时约 ${Math.round(o.cycle_days / 7)} 周一次` : `平时约 ${o.cycle_days} 天一次`) : ''
  return [`来过 ${o.visit_count} 次`, cycle].filter(Boolean).join('、')
})
const overdueText = computed(() => {
  const days = overview.value?.overdue_days ? overview.value.days_since_last_visit : null
  if (!days) return ''
  return days >= 14 ? `已经 ${Math.floor(days / 7)} 周没来` : `已经 ${days} 天没来`
})

// 页头 ··· 菜单：发红包、登记历史欠款、手机号、安全标签、合并。
const redPackets = ref<{ editIssue: () => void } | null>(null)
const debts = ref<{ editHistory: () => void } | null>(null)
const moreItems = computed(() => {
  const c = customer.value
  if (!c || c.merged_into) return []
  return [[
    ...(can('card.sell') ? [{ label: '发放红包', icon: 'i-lucide-gift', onSelect: () => redPackets.value?.editIssue() }] : []),
    ...(can('data.import_export') && today.value ? [{ label: '登记历史欠款', icon: 'i-lucide-notebook-pen', onSelect: () => debts.value?.editHistory() }] : [])
  ], [
    ...((!c.phone && can('customer.edit')) || (c.phone && can('customer.merge')) ? [{ label: c.phone ? '改手机号' : '补手机号', icon: 'i-lucide-phone', onSelect: openPhone }] : []),
    ...(can('customer.safety_label') && today.value ? [{ label: '添加安全标签', icon: 'i-lucide-shield-alert', onSelect: openLabel }] : []),
    ...(can('customer.merge') ? [{ label: '合并重复顾客', icon: 'i-lucide-merge', onSelect: openMerge }] : [])
  ]].filter(group => group.length)
})

// 修改记录从右侧滑出，开着时网址带 ?tab=logs，刷新和转发都还在。
const logsOpen = computed({
  get: () => route.query.tab === 'logs' && can('audit_log.view'),
  set: open => router.replace({ query: { ...route.query, tab: open ? 'logs' : undefined } })
})

const createdOn = computed(() => customer.value
  ? new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai' }).format(new Date(customer.value.created_at))
  : '')

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
    if (data.keep.id === props.id) load()
    else navigateTo(`/customers/${data.keep.id}`)
  })
}

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

// 一条修改记录只列出改动的字段；新建等没有改前的记录列出填了的字段。
interface LogField { label: string, value: string, empty: boolean }
interface LogChange { label: string, before: string | null, after: string }

function logFields(value: unknown): LogField[] | null {
  if (!value || typeof value !== 'object') return null
  const c = value as Customer
  const labels = c.active_safety_labels?.map(l =>
    `${labelNames[l.kind]}（${labelSources[l.source]}，${l.observed_on}，${l.note || '无备注'}）`
  ).join('、')
  const field = (label: string, text: string | null | undefined, fallback: string) => ({ label, value: text || fallback, empty: !text })
  return [
    field('姓名', c.unidentified ? '未识别顾客' : c.name, '未填写'),
    field('手机号', c.phone, '未留'),
    field('性别', c.gender ? genderName(c.gender) : '', '未填写'),
    field('来源', c.source?.name, '未填写'),
    field('负责人', c.responsible?.name, '不设'),
    field('备注', c.note, '无'),
    field('安全标签', labels, '无'),
    field('状态', c.merged_into ? '已合并' : '', '正常')
  ]
}

const phoneActions = new Set(['add_customer_phone', 'change_customer_phone'])

function logChanges(entry: { action: string, before?: unknown, after?: unknown }): LogChange[] {
  const before = logFields(entry.before)
  const after = logFields(entry.after) ?? []
  if (!before) return after.filter(f => !f.empty).map(f => ({ label: f.label, before: null, after: f.value }))
  const changes: LogChange[] = after
    .map((f, i) => ({ label: f.label, before: before[i]!.value, after: f.value }))
    .filter(change => change.before !== change.after)
  // 看不到完整号码时，只改中间四位的前后打码相同，仍要说明号码改过
  if (phoneActions.has(entry.action) && !changes.some(c => c.label === '手机号')) {
    changes.unshift({ label: '手机号', before: null, after: '已修改' })
  }
  return changes
}

const logEntries = computed(() => logs.items.value.map(entry => ({ entry, changes: logChanges(entry) })))

// 读取档案及营业日，切换顾客时关闭编辑入口
watch(() => props.id, () => {
  customerGuard.next()
  memoryEditing.value = null
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
  api.POST('/api/ops/list_customer_sources', { body: {} }).then(({ data }) => {
    if (data) sources.value = data.items
  })
  if (can('customer.assign')) {
    api.POST('/api/ops/list_staff_options', { body: {} }).then(({ data }) => {
      if (data) staffOptions.value = data.staff
    })
  }
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
  crumbTitle.value = ''
})
</script>

<template>
  <div>
    <div class="mx-auto max-w-[1100px] pb-10" data-testid="customer-profile">
      <template v-if="customer">
        <header class="flex flex-wrap items-start gap-4">
          <span class="grid size-14 shrink-0 place-items-center rounded-full bg-tag-gray text-figure" aria-hidden="true">{{ customerName(customer).slice(0, 1) }}</span>
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-1">
              <h1 class="truncate text-title">{{ customerName(customer) }}</h1>
              <UButton v-if="!customer.merged_into && can('customer.edit')" size="sm" color="neutral" variant="ghost" icon="i-lucide-pencil" aria-label="修改资料" @click="edit" />
            </div>
            <p class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted" data-testid="customer-summary">
              <span class="tabular-nums">{{ customer.phone || '没留手机号' }}</span>
              <span aria-hidden="true">·</span>
              <UDropdownMenu v-if="can('customer.assign') && !customer.merged_into" :items="responsibleItems">
                <button type="button" class="hover:underline focus-visible:underline" data-testid="customer-responsible">{{ customer.responsible ? `负责人 ${customer.responsible.name}` : '不设负责人' }}</button>
              </UDropdownMenu>
              <span v-else>{{ customer.responsible ? `负责人 ${customer.responsible.name}` : '不设负责人' }}</span>
              <template v-if="rhythm">
                <span aria-hidden="true">·</span>
                <span>{{ rhythm }}</span>
              </template>
              <span v-if="overdueText" class="tag bg-tag-yellow">{{ overdueText }}</span>
              <span v-for="l in customer.active_safety_labels" :key="l.id" class="tag bg-tag-red">{{ labelNames[l.kind] }}</span>
              <template v-if="overview?.next_appointment">
                <span aria-hidden="true">·</span>
                <span class="tabular-nums">下次 {{ nextText(overview.next_appointment.starts_at) }} {{ overview.next_appointment.staff.name }}</span>
              </template>
            </p>
          </div>
          <div v-if="!customer.merged_into" class="flex items-center gap-2">
            <UButton v-if="can('order.create')" :to="`/cashier?open=customer&customer=${customer.id}`" color="neutral" variant="outline" icon="i-lucide-receipt">开单</UButton>
            <UDropdownMenu v-if="moreItems.length" :items="moreItems" :content="{ align: 'end' }">
              <UButton color="neutral" variant="outline" icon="i-lucide-ellipsis" aria-label="顾客操作" />
            </UDropdownMenu>
          </div>
        </header>

        <UAlert v-if="customer.merged_into" class="mt-6" title="已合并到另一位顾客" color="warning" variant="subtle">
          <template #description>
            <UButton size="sm" color="neutral" variant="outline" class="mt-1" :to="`/customers/${customer.merged_into}`">查看保留的顾客</UButton>
          </template>
        </UAlert>

        <div class="mt-8 grid gap-10 lg:grid-cols-[minmax(0,1fr)_20rem]">
          <div class="min-w-0 space-y-8">
            <template v-if="!customer.merged_into">
              <section data-testid="customer-last">
                <h2 class="sec mt-0!">上次</h2>
                <ListSkeleton v-if="history.loading.value && !visits.length" :rows="2" />
                <div v-else-if="!lastVisit" class="box text-sm text-muted">还没在店里做过头发</div>
                <div v-else class="box space-y-3">
                  <p class="text-meta text-muted tabular-nums">{{ shortDate(lastVisit.date) }} · {{ weekday(lastVisit.date) }}<template v-if="lastVisit.staff"> · {{ lastVisit.staff }}</template></p>
                  <p class="figure">{{ lastVisit.items.map(item => item.item.name).join(' · ') }}</p>
                  <template v-for="item in lastVisit.items" :key="item.order_item_id">
                    <div v-if="item.record?.formula" class="rounded-card bg-tag-gray px-3 py-2 text-sm">
                      <p class="text-meta text-muted">{{ item.item.name }} · 配方</p>
                      <p class="whitespace-pre-wrap tabular-nums">{{ item.record.formula }}</p>
                    </div>
                    <p v-if="item.record?.result" class="text-sm"><span class="me-2 text-meta text-muted">做成</span>{{ item.record.result }}</p>
                    <p v-if="item.record?.feedback" class="text-sm"><span class="me-2 text-meta text-muted">顾客觉得</span>{{ feedbackNames[item.record.feedback] }}</p>
                  </template>
                  <div v-if="can('order.create')" class="flex justify-end">
                    <UButton :to="againLink" icon="i-lucide-repeat">照这个开单</UButton>
                  </div>
                </div>
              </section>
              <section v-if="visits.length > 1" data-testid="customer-earlier">
                <h2 class="sec">更早</h2>
                <div class="divide-y divide-default">
                  <div v-for="visit in visits.slice(1)" :key="visit.orderID" data-testid="customer-visit" class="grid grid-cols-[5rem_minmax(0,1fr)] gap-3 py-2 text-sm">
                    <span class="text-meta text-muted tabular-nums">{{ shortDate(visit.date) }}</span>
                    <div class="min-w-0">
                      <p>{{ visit.items.map(item => item.item.name).join(' · ') }}<span v-if="visit.staff" class="text-muted"> · {{ visit.staff }}</span></p>
                      <p v-for="item in visit.items.filter(i => i.record?.formula)" :key="item.order_item_id" class="truncate text-meta text-muted" :title="item.record!.formula!">配方 {{ item.record!.formula }}</p>
                    </div>
                  </div>
                </div>
                <UButton v-if="history.hasMore.value" :loading="history.loading.value" size="sm" color="neutral" variant="ghost" class="mt-2" @click="history.loadMore">加载更多</UButton>
              </section>
            </template>
          </div>

          <aside class="space-y-8" data-testid="customer-rail">
            <!-- 已合并的顾客，卡、欠款、红包都随合并转到保留的那位，后端对这里按不存在处理：不挂载，免得读一次弹一次错。 -->
            <template v-if="!customer.merged_into">
              <CustomerCards :id="customer.id" @changed="loadOverview" />
              <CustomerDebts :id="customer.id" ref="debts" :today="today" />
              <CustomerRedPackets :id="customer.id" ref="redPackets" />
            </template>
            <section data-testid="customer-memory">
              <h2 class="flex h-7 items-center text-meta text-muted">记住的事</h2>
              <div class="divide-y divide-default text-sm">
                <div v-for="field in memoryFields" :key="field.key" :data-memory="field.key" class="py-2">
                  <template v-if="memoryEditing === field.key">
                    <UTextarea v-model="memoryDraft" :aria-label="field.label" autoresize :rows="2" autofocus class="w-full" @keydown.esc.stop="memoryEditing = null" />
                    <div class="mt-2 flex justify-end gap-2">
                      <UButton size="sm" color="neutral" variant="ghost" @click="memoryEditing = null">取消</UButton>
                      <UButton size="sm" :loading="saving" @click="saveMemory">保存</UButton>
                    </div>
                  </template>
                  <button v-else type="button" class="flex w-full gap-3 rounded-card text-start enabled:hover:bg-elevated disabled:cursor-default" :disabled="!can('customer.edit') || !!customer.merged_into" :aria-label="`改${field.label}：${customer[field.key] || field.empty}`" @click="editMemory(field.key)">
                    <span class="w-8 shrink-0 text-meta text-muted">{{ field.label }}</span>
                    <span class="min-w-0 flex-1 whitespace-pre-wrap break-words" :class="customer[field.key] ? 'said' : 'text-dimmed'">{{ customer[field.key] || field.empty }}</span>
                  </button>
                </div>
              </div>
            </section>
            <section v-if="customer.active_safety_labels.length" data-testid="customer-labels">
              <h2 class="flex h-7 items-center text-meta text-muted">安全标签</h2>
              <div v-for="l in customer.active_safety_labels" :key="l.id" class="group/row flex items-start gap-3 border-t border-default py-2 text-sm first-of-type:border-t-0">
                <span class="tag bg-tag-red shrink-0">{{ labelNames[l.kind] }}</span>
                <div class="min-w-0 flex-1 space-y-0.5">
                  <p class="text-meta text-muted">{{ labelSources[l.source] }} · {{ l.observed_on }}</p>
                  <p class="whitespace-pre-wrap" :class="l.note && 'said'">{{ l.note || '无备注' }}</p>
                </div>
                <UButton v-if="!customer.merged_into && can('customer.safety_label')" size="xs" color="error" variant="ghost" class="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100" :aria-label="`撤销${labelNames[l.kind]}`" @click="revokeTarget = l">撤销</UButton>
              </div>
            </section>
            <footer class="space-y-1 border-t border-default pt-3 text-meta text-muted">
              <p>{{ customer.source?.name ? `来源 ${customer.source.name}` : '来源未填' }} · {{ customer.gender ? genderName(customer.gender) : '性别未填' }} · <span class="tabular-nums">{{ createdOn }} 建档</span></p>
              <UButton v-if="can('audit_log.view')" size="sm" color="neutral" variant="ghost" class="-mx-2" trailing-icon="i-lucide-chevron-right" @click="logsOpen = true">修改记录</UButton>
            </footer>
          </aside>
        </div>
      </template>
      <LoadError v-else-if="loadError" title="顾客档案没有读出来" :hint="loadError" @retry="load()" />
      <ListSkeleton v-else :rows="6" />
    </div>
    <USlideover v-model:open="logsOpen" title="修改记录">
      <template #body>
        <div data-testid="customer-logs" class="text-sm">
          <p v-if="!logs.items.value.length" class="text-muted">暂无修改记录</p>
          <div v-for="{ entry, changes } in logEntries" :key="entry.id" data-testid="customer-log-entry" class="space-y-1 border-t border-default py-2 first:border-t-0">
            <p class="flex flex-wrap items-center gap-x-2 text-meta text-muted">
              <span class="font-medium text-default">{{ actions[entry.action] || entry.action }}</span>
              <span>{{ entry.actor.name }}</span>
              <span class="ms-auto tabular-nums">{{ new Date(entry.at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }}</span>
            </p>
            <p v-for="change in changes" :key="change.label" data-testid="log-change">
              <span class="me-2 text-meta text-muted">{{ change.label }}</span>
              <template v-if="change.before !== null">
                <span class="sr-only">原为</span>
                <span data-testid="log-before" class="text-muted line-through">{{ change.before }}</span>
                <span class="mx-1 text-dimmed" aria-hidden="true">→</span>
                <span class="sr-only">改为</span>
              </template>
              <span data-testid="log-after">{{ change.after }}</span>
            </p>
            <p v-if="!changes.length" class="text-muted">资料没有变化</p>
          </div>
          <UButton v-if="logs.hasMore.value" :loading="logs.loading.value" size="xs" color="neutral" variant="ghost" class="mt-2" @click="logs.loadMore">加载更多</UButton>
        </div>
      </template>
    </USlideover>
    <UModal v-model:open="editing" title="修改顾客资料">
      <template #body>
        <div class="space-y-5">
          <UFormField label="姓名">
            <UInput v-model="profile.name" class="w-full" />
          </UFormField>
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="性别">
              <USelect v-model="profile.gender" :items="genderItems" class="w-full" />
            </UFormField>
            <UFormField label="顾客来源">
              <USelect v-model="profile.source" :items="sourceItems" class="w-full" />
            </UFormField>
          </div>
          <UFormField label="备注">
            <UTextarea v-model="profile.note" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="editing = false">
          <UButton :loading="saving" @click="saveProfile">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-model:open="phoneOpen" :title="customer?.phone ? '改手机号' : '补手机号'">
      <template #body>
        <UFormField label="手机号">
          <UInput v-model="phone" type="tel" class="w-full" />
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="phoneOpen = false">
          <UButton :loading="saving" @click="savePhone">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-model:open="labelOpen" title="添加安全标签">
      <template #body>
        <div class="space-y-5">
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="类型">
              <USelect v-model="label.kind" :items="Object.entries(labelNames).map(([value, label]) => ({ value, label }))" class="w-full" />
            </UFormField>
            <UFormField label="来源">
              <USelect v-model="label.source" :items="Object.entries(labelSources).map(([value, label]) => ({ value, label }))" class="w-full" />
            </UFormField>
          </div>
          <UFormField label="观察日期">
            <DateInput v-model="label.observed_on" :max="today" />
          </UFormField>
          <UFormField label="备注">
            <UTextarea v-model="label.note" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="labelOpen = false">
          <UButton :loading="saving" :disabled="!label.observed_on || label.observed_on > today" @click="addLabel">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal :open="!!revokeTarget" title="撤销安全标签" @update:open="setRevokeOpen">
      <template #body>确认撤销“{{ labelNames[revokeTarget?.kind ?? ''] }}”安全标签？历史记录仍然保留。</template>
      <template #footer>
        <ModalActions cancel-label="不撤了" @cancel="setRevokeOpen(false)">
          <UButton :loading="saving" color="error" @click="revoke">撤销</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-model:open="mergeOpen" title="合并重复顾客" :ui="{ content: 'sm:max-w-2xl' }">
      <template #body>
        <div class="space-y-5">
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
            <div v-for="c in [customer, other]" :key="c.id" class="rounded-md p-4 shadow-line">
              <dl class="space-y-2 text-sm">
                <dt class="font-medium">{{ customerName(c) }}</dt>
                <dd>手机号：{{ c.phone || '未填写' }}</dd>
                <dd>性别：{{ genderName(c.gender) }}</dd>
                <dd>来源：{{ c.source?.name || '未填写' }}</dd>
                <dd>负责人：{{ c.responsible?.name || '不设' }}</dd>
                <dd class="whitespace-pre-wrap">备注：{{ c.note || '无' }}</dd>
                <dd>建档时间：{{ new Date(c.created_at).toLocaleString('zh-CN') }}</dd>
                <dd>
                  <span v-for="l in c.active_safety_labels" :key="l.id" class="tag mr-1 bg-tag-red">{{ labelNames[l.kind] }}</span>
                </dd>
              </dl>
              <UButton class="mt-3" :variant="keepID === c.id ? 'solid' : 'outline'" :aria-pressed="keepID === c.id" @click="keepID = c.id">保留这位顾客</UButton>
            </div>
          </div>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="mergeOpen = false">
          <UButton :disabled="!other" color="error" @click="mergeConfirm = true">继续合并</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-model:open="mergeConfirm" title="确认合并顾客">
      <template #body>
        <p>合并后不可撤销：被合并顾客的安全标签等记录归到保留的顾客名下</p>
        <p v-if="customer && other" class="mt-3">保留：{{ customerName(keepID === id ? customer : other) }}；被合并：{{ customerName(keepID === id ? other : customer) }}</p>
      </template>
      <template #footer>
        <ModalActions cancel-label="不合并了" @cancel="mergeConfirm = false">
          <UButton :loading="saving" color="error" @click="merge">合并</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
