<script setup lang="ts">
import type { components } from '~/utils/api'
import type { CustomerOverview } from '~/utils/customer'

type Customer = components['schemas']['Customer']
type LabelInput = components['schemas']['AddSafetyLabelInput']

const props = defineProps<{
  id: string
  sources: components['schemas']['CustomerSource'][]
  // 列表里这一行的到店和余额汇总；从合并跳到不在当前页的顾客时没有，指标小卡就不出现
  overview?: CustomerOverview
  // 右栏当前分页（overview、cards、red-packets、debts、logs），写在网址里，刷新和转发都还在
  tab?: string
}>()
const emit = defineEmits<{
  'close': []
  'open': [id: string]
  'changed': []
  'update:tab': [tab: string]
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
const { record: recordRecent } = useRecentPages()
const customerName = (c: Customer) => c.name || (c.unidentified ? '未识别顾客' : '未填写姓名')
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
    recordRecent({ to: `/customers?id=${id}`, label: customerName(res.data), icon: 'i-lucide-user' })
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
      note: profile.note || undefined,
      // 资料是整体替换，这页还不改发质、喜好、忌讳，原样带回去免得被清空。
      hair_texture: customer.value?.hair_texture ?? undefined,
      preferences: customer.value?.preferences ?? undefined,
      dislikes: customer.value?.dislikes ?? undefined
    }
  }), '资料已保存')) {
    editing.value = false
  }
}

// 左栏（drawer-01）：主操作"修改资料"，其余收进 ··· 菜单；分节导航点了滚到对应分节。
const moreItems = computed(() => {
  const c = customer.value
  if (!c || c.merged_into) return []
  return [[
    ...((!c.phone && can('customer.edit')) || (c.phone && can('customer.merge')) ? [{ label: c.phone ? '改手机号' : '补手机号', icon: 'i-lucide-phone', onSelect: openPhone }] : []),
    ...(can('customer.safety_label') && today.value ? [{ label: '添加安全标签', icon: 'i-lucide-shield-alert', onSelect: openLabel }] : []),
    ...(can('customer.merge') ? [{ label: '合并重复顾客', icon: 'i-lucide-merge', onSelect: openMerge }] : [])
  ]].filter(group => group.length)
})

// 右栏分页：一次只看一块，没权限的分页不出现；网址里的分页看不到时回到概览。
const sections = computed(() => [
  { value: 'overview', label: '概览' },
  ...(can('customer.view')
    ? [
        { value: 'cards', label: '卡' },
        { value: 'red-packets', label: '红包' }
      ]
    : []),
  ...(can('customer.view') && !customer.value?.merged_into ? [{ value: 'debts', label: '欠款' }] : []),
  ...(can('audit_log.view') ? [{ value: 'logs', label: '修改记录' }] : [])
])
const tab = computed({
  get: () => sections.value.some(s => s.value === props.tab) ? props.tab! : 'overview',
  set: value => emit('update:tab', value)
})

const createdOn = computed(() => customer.value
  ? new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai' }).format(new Date(customer.value.created_at))
  : '')

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
  <USlideover :open="true" title="顾客详情" :ui="{ content: 'sm:max-w-4xl', body: 'p-0 sm:p-0 sm:overflow-hidden' }" @update:open="setDetailOpen">
    <template #body>
      <div v-if="customer" class="sm:grid sm:h-full sm:grid-cols-[15rem_minmax(0,1fr)]">
        <aside class="side-scroll border-b border-default p-5 sm:overflow-y-auto sm:border-e sm:border-b-0" data-testid="customer-rail">
          <div class="flex flex-col items-center text-center">
            <span class="grid size-16 place-items-center rounded-full bg-tag-gray text-2xl font-semibold" aria-hidden="true">{{ customerName(customer).slice(0, 1) }}</span>
            <h2 class="mt-3 text-lg font-semibold">{{ customerName(customer) }}</h2>
            <p class="text-sm text-muted tabular-nums">{{ customer.phone || '没留手机号' }}</p>
            <div v-if="!customer.merged_into && (can('customer.edit') || moreItems.length)" class="mt-4 flex gap-2">
              <UButton v-if="can('customer.edit')" color="neutral" variant="outline" icon="i-lucide-pencil" @click="edit">修改资料</UButton>
              <UDropdownMenu v-if="moreItems.length" :items="moreItems" :content="{ align: 'end' }">
                <UButton color="neutral" variant="outline" icon="i-lucide-ellipsis" aria-label="顾客操作" />
              </UDropdownMenu>
            </div>
          </div>
          <ul class="mt-5 space-y-2.5 text-sm">
            <li class="flex gap-2.5">
              <UIcon name="i-lucide-user" class="mt-0.5 size-4 shrink-0 text-muted" aria-hidden="true" />
              <span :class="customer.gender ? '' : 'text-dimmed'"><span class="sr-only">性别：</span>{{ customer.gender ? genderName(customer.gender) : '性别未填' }}</span>
            </li>
            <li class="flex gap-2.5">
              <UIcon name="i-lucide-megaphone" class="mt-0.5 size-4 shrink-0 text-muted" aria-hidden="true" />
              <span :class="customer.source ? '' : 'text-dimmed'"><span class="sr-only">来源：</span>{{ customer.source?.name || '来源未填' }}</span>
            </li>
            <li class="flex gap-2.5">
              <UIcon name="i-lucide-user-check" class="mt-0.5 size-4 shrink-0 text-muted" aria-hidden="true" />
              <span :class="customer.responsible ? '' : 'text-dimmed'">{{ customer.responsible ? `负责人 ${customer.responsible.name}` : '不设负责人' }}</span>
            </li>
            <li class="flex gap-2.5">
              <UIcon name="i-lucide-user-plus" class="mt-0.5 size-4 shrink-0 text-muted" aria-hidden="true" />
              <span class="tabular-nums">{{ createdOn }} 建档</span>
            </li>
            <li class="flex gap-2.5">
              <UIcon name="i-lucide-sticky-note" class="mt-0.5 size-4 shrink-0 text-muted" aria-hidden="true" />
              <span class="min-w-0 whitespace-pre-wrap break-words" :class="customer.note ? '' : 'text-dimmed'"><span class="sr-only">备注：</span>{{ customer.note || '没有备注' }}</span>
            </li>
          </ul>
        </aside>
        <div class="flex min-w-0 flex-col sm:min-h-0">
          <UTabs v-model="tab" :items="sections" :content="false" variant="link" color="neutral" class="shrink-0 px-5 pt-2" data-testid="customer-tabs" />
          <div class="min-w-0 flex-1 space-y-4 p-5 sm:overflow-y-auto">
            <UAlert v-if="customer.merged_into" title="已合并到另一位顾客" color="warning" variant="subtle">
              <template #description>
                <UButton size="sm" color="neutral" variant="outline" class="mt-1" @click="emit('open', customer.merged_into)">查看保留的顾客</UButton>
              </template>
            </UAlert>
            <section v-if="tab === 'overview'" class="space-y-3">
              <div v-if="overview" class="stats grid grid-cols-2 gap-x-4 gap-y-3 border-t-0! pt-1! lg:grid-cols-4" data-testid="customer-metrics">
                <div>
                  <p class="text-meta text-muted">卡余额</p>
                  <p class="mt-1 text-2xl font-semibold tabular-nums" :class="zeroText(overview.stored_balance) && 'text-dimmed'">{{ yuan(overview.stored_balance) }}</p>
                  <p class="mt-0.5 text-meta text-muted">{{ overview.count_remaining ? `另有 ${overview.count_remaining} 次` : '本金加赠送金' }}</p>
                </div>
                <div>
                  <p class="text-meta text-muted">累计消费</p>
                  <p class="mt-1 text-2xl font-semibold tabular-nums" :class="zeroText(overview.total_spend) && 'text-dimmed'">{{ yuan(overview.total_spend) }}</p>
                  <p class="mt-0.5 text-meta text-muted">{{ overview.visit_count ? `平均每次 ${yuan(averageSpend(overview))}` : '实收，不扣售后' }}</p>
                </div>
                <div>
                  <p class="text-meta text-muted">来过</p>
                  <p class="mt-1 text-2xl font-semibold tabular-nums" :class="overview.visit_count ? '' : 'text-dimmed'">{{ overview.visit_count }} 次</p>
                  <p class="mt-0.5 text-meta text-muted">{{ overview.cycle_days ? `约 ${overview.cycle_days} 天一次` : overview.visit_count ? '只来过一天' : '还没来过' }}</p>
                </div>
                <div>
                  <p class="text-meta text-muted">上次到店</p>
                  <p class="mt-1 text-2xl font-semibold" :class="overview.days_since_last_visit == null && 'text-dimmed'">{{ overview.days_since_last_visit == null ? '没有' : sinceText(overview.days_since_last_visit) }}</p>
                  <p class="mt-0.5 truncate text-meta" :class="overview.overdue_days ? 'text-ink-orange' : 'text-muted'">{{ overview.overdue_days ? `比平时晚了 ${overview.overdue_days} 天` : overview.first_visit_date ? `首次 ${shortDate(overview.first_visit_date)}` : '' }}</p>
                </div>
              </div>
              <div v-if="overview && (overview.next_appointment || overview.last_visit_date)" class="box divide-y divide-default p-0 text-sm">
                <p v-if="overview.next_appointment" class="flex items-center gap-3 px-3 py-2.5">
                  <span class="w-16 shrink-0 text-meta text-muted">下次预约</span>
                  <span class="tag bg-tag-blue tabular-nums">{{ nextText(overview.next_appointment.starts_at) }}</span>
                  <span class="text-muted">{{ overview.next_appointment.staff.name }}</span>
                </p>
                <p v-if="overview.last_visit_date" class="flex items-center gap-3 px-3 py-2.5">
                  <span class="w-16 shrink-0 text-meta text-muted">上次做了</span>
                  <span class="min-w-0 truncate" :title="lastVisitMeta(overview)">{{ lastVisitMeta(overview) || overview.last_visit_date }}</span>
                </p>
              </div>
              <div>
                <div class="sec">
                  安全标签<span class="ms-1 font-normal text-dimmed">{{ customer.active_safety_labels.length }}</span>
                </div>
                <div class="box overflow-hidden p-0">
                  <p v-if="!customer.active_safety_labels.length" class="px-3 py-2.5 text-sm text-muted">暂无有效安全标签</p>
                  <div v-for="l in customer.active_safety_labels" :key="l.id" class="group/row flex items-start gap-3 border-t border-default px-3 py-2.5 text-sm first:border-t-0">
                    <span class="tag bg-tag-red shrink-0">{{ labelNames[l.kind] }}</span>
                    <div class="min-w-0 flex-1 space-y-0.5">
                      <p class="text-meta text-muted">{{ labelSources[l.source] }} · {{ l.observed_on }}</p>
                      <p class="whitespace-pre-wrap">{{ l.note || '无备注' }}</p>
                    </div>
                    <UButton v-if="!customer.merged_into && can('customer.safety_label')" size="xs" color="error" variant="ghost" class="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100" @click="revokeTarget = l">撤销</UButton>
                  </div>
                </div>
              </div>
            </section>
            <CustomerCards v-if="tab === 'cards'" :id="id" :merged="!!customer.merged_into" @changed="emit('changed')" />
            <CustomerRedPackets v-if="tab === 'red-packets'" :id="customer.id" :merged="!!customer.merged_into" />
            <CustomerDebts v-if="tab === 'debts'" :id="customer.id" :today="today" />
            <section v-if="tab === 'logs'">
              <div class="box overflow-hidden p-0" data-testid="customer-logs">
                <p v-if="!logs.items.value.length" class="px-3 py-2.5 text-sm text-muted">暂无修改记录</p>
                <div v-for="{ entry, changes } in logEntries" :key="entry.id" data-testid="customer-log-entry" class="space-y-1 border-t border-default px-3 py-2.5 text-sm first:border-t-0">
                  <p class="flex flex-wrap items-center gap-x-2 text-meta text-muted">
                    <span class="font-medium text-default">{{ actions[entry.action] || entry.action }}</span>
                    <span>{{ entry.actor.name }}</span>
                    <span class="ms-auto tabular-nums">{{ new Date(entry.at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }}</span>
                  </p>
                  <p v-for="change in changes" :key="change.label" data-testid="log-change">
                    <span class="me-1.5 text-meta text-muted">{{ change.label }}</span>
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
                <div v-if="logs.hasMore.value" class="border-t border-default px-3 py-1.5">
                  <UButton :loading="logs.loading.value" size="xs" color="neutral" variant="ghost" @click="logs.loadMore">加载更多</UButton>
                </div>
              </div>
            </section>
          </div>
        </div>
      </div>
      <div v-else-if="loadError" class="space-y-3 p-5">
        <p>{{ loadError }}</p>
        <UButton variant="outline" @click="load">重试</UButton>
      </div>
      <p v-else class="p-5 text-muted">正在读取顾客档案…</p>
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
      <ModalActions @cancel="setRevokeOpen(false)">
        <UButton :loading="saving" color="error" @click="revoke">确认撤销</UButton>
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
                <span v-for="l in c.active_safety_labels" :key="l.id" class="tag mr-1 bg-tag-red">{{ labelNames[l.kind] }}</span>
              </dd>
            </dl>
            <UButton class="mt-3" :variant="keepID === c.id ? 'solid' : 'outline'" :aria-pressed="keepID === c.id" @click="keepID = c.id">保留这位顾客</UButton>
          </UCard>
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
      <ModalActions @cancel="mergeConfirm = false">
        <UButton :loading="saving" color="error" @click="merge">确认合并</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
