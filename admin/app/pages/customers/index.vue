<script setup lang="ts">
import type { components, paths } from '~/utils/api'

definePageMeta({ permission: 'customer.view', title: '顾客', art: 'mirror' })

type Overview = paths['/api/ops/list_customer_overview']['post']['responses']['200']['content']['application/json']['items'][number]
type Segments = paths['/api/ops/get_customer_segments']['post']['responses']['200']['content']['application/json']
type SegmentKey = 'all' | 'today' | 'due' | 'balance' | 'new' | 'sleeping'
type SortKey = 'last_visit' | 'overdue' | 'spend' | 'created'
const api = useApi()
const toast = useToast()
const { can } = useMe()
const route = useRoute()
const NONE = 'none'
const PAGE = 50

// 列表、分组、搜索和筛选
const items = ref<Overview[]>([])
const total = ref(0)
const segments = ref<Segments | null>(null)
const staff = ref<components['schemas']['StaffOption'][]>([])
const sources = ref<components['schemas']['CustomerSource'][]>([])
const query = ref('')
const segment = ref<SegmentKey>('all')
const sort = ref<SortKey>('last_visit')
const responsible = ref(NONE)
const offset = ref(0)
const selected = ref<string[]>([])
const loading = ref(false)
const searchGuard = useRequestGuard()
const segmentGuard = useRequestGuard()
const searchFocused = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
const staffItems = computed(() => staff.value.map(s => ({
  label: s.name + (s.active ? '' : '（已停用）'),
  value: s.id
})))
const activeStaffItems = computed(() => staff.value
  .filter(s => s.active)
  .map(s => ({ label: s.name, value: s.id }))
)
const sortItems = [
  { label: '按上次到店', value: 'last_visit' },
  { label: '按晚了几天', value: 'overdue' },
  { label: '按累计消费', value: 'spend' },
  { label: '按建档时间', value: 'created' }
]
const labelNames: Record<string, string> = {
  allergy: '过敏',
  scalp_sensitive: '头皮敏感',
  recent_chemical: '近期烫染'
}
const responsibleID = computed(() => responsible.value === NONE ? undefined : responsible.value)

// 六个分组：数字和下面一句说明都来自 get_customer_segments，口径见 backend/README.md。
const tiles = computed(() => {
  const s = segments.value
  return [
    { key: 'all' as const, label: '全部', count: s?.all.count, hint: s ? `本月到店 ${s.all.visited_this_month} 位` : '' },
    { key: 'today' as const, label: '今天要来', count: s?.today.count, hint: s?.today.count ? `${s.today.arrived} 位已到店` : '' },
    { key: 'due' as const, label: '该回来了', count: s?.due.count, hint: s?.due.count ? `${s.due.with_balance} 位卡里有余额` : '' },
    { key: 'balance' as const, label: '卡里有余额', count: s?.balance.count, hint: s?.balance.count ? `合计 ${yuanFixed(s.balance.stored_total)}` : '' },
    { key: 'new' as const, label: '30 天新客', count: s?.new.count, hint: s?.new.count ? `${s.new.single_visit} 位还没来第二次` : '' },
    { key: 'sleeping' as const, label: '沉睡', count: s?.sleeping.count, hint: '超过 90 天没来' }
  ]
})
// 分组的补充数字（本月到店、余额合计……）跟在「共 N 位」后面。
const activeHint = computed(() => tiles.value.find(t => t.key === segment.value)?.hint ?? '')

const emptyTexts: Record<SegmentKey, { title: string, hint: string }> = {
  all: { title: '还没有顾客', hint: '顾客第一次到店开单时会自动建档，也可以点右上角新建' },
  today: { title: '今天没有预约的顾客', hint: '' },
  due: { title: '没有该回来的顾客', hint: '按每位顾客自己的到店间隔算，过了间隔还没约的会出现在这里' },
  balance: { title: '没有卡里有余额的顾客', hint: '' },
  new: { title: '最近 30 天没有新客', hint: '' },
  sleeping: { title: '没有沉睡顾客', hint: '超过 90 天没来的顾客会出现在这里' }
}

async function load() {
  const current = searchGuard.next()
  loading.value = true
  try {
    const res = await api.POST('/api/ops/list_customer_overview', {
      body: {
        query: query.value,
        segment: segment.value,
        sort: sort.value,
        responsible_staff_id: responsibleID.value,
        limit: PAGE,
        offset: offset.value
      }
    })
    if (!current()) return
    if (res.data) {
      // 合并、交接后人数变少，当前页可能已经超过最后一页：退回最后一页重新取
      const last = Math.floor(Math.max(res.data.total - 1, 0) / PAGE) * PAGE
      if (!res.data.items.length && offset.value > last) {
        offset.value = last
        void load()
        return
      }
      items.value = res.data.items
      total.value = res.data.total
      selected.value = []
    } else {
      items.value = []
      total.value = 0
      toast.add({ title: errorText(res.error), color: 'error' })
    }
  } finally {
    if (current()) loading.value = false
  }
}

async function loadSegments() {
  const current = segmentGuard.next()
  const res = await api.POST('/api/ops/get_customer_segments', { body: { responsible_staff_id: responsibleID.value } })
  if (!current()) return
  if (res.data) segments.value = res.data
  else toast.add({ title: errorText(res.error), color: 'error' })
}

async function loadOptions() {
  const [s, c] = await Promise.all([
    api.POST('/api/ops/list_staff_options', { body: {} }),
    api.POST('/api/ops/list_customer_sources', { body: {} })
  ])
  if (s.data) {
    staff.value = s.data.staff
  } else {
    toast.add({ title: errorText(s.error), color: 'error' })
  }
  if (c.data) {
    sources.value = c.data.items
  } else {
    toast.add({ title: errorText(c.error), color: 'error' })
  }
}

function search() {
  clearTimeout(timer)
  offset.value = 0
  load()
}

function reload() {
  load()
  loadSegments()
}

function clearFilters() {
  query.value = ''
  responsible.value = NONE
}

// 排序跟着分组：该回来了默认按晚了几天，其余按上次到店；换分组就回到那个分组的默认。
const defaultSort = computed<SortKey>(() => segment.value === 'due' ? 'overdue' : 'last_visit')
const sortName = computed(() => sortItems.find(s => s.value === sort.value)?.label.replace(/^按/, '') ?? '')

function changePage(change: number) {
  offset.value += change * PAGE
  load()
}

function toggle(id: string, value: boolean | 'indeterminate') {
  selected.value = value === true ? [...selected.value, id] : selected.value.filter(v => v !== id)
}

function done(error: unknown, ok: unknown, message: string) {
  toast.add({ title: ok ? message : errorText(error), color: ok ? 'success' : 'error' })
  if (ok) {
    reload()
  }
  return !!ok
}

watch(query, () => {
  searchGuard.invalidate()
  clearTimeout(timer)
  timer = setTimeout(search, 300)
})

watch(segment, () => {
  if (sort.value === defaultSort.value) search()
  else sort.value = defaultSort.value
})
watch(sort, search)
watch(responsible, () => {
  search()
  loadSegments()
})

onBeforeUnmount(() => {
  clearTimeout(timer)
})

onMounted(async () => {
  await Promise.all([load(), loadSegments(), loadOptions()])
})

// 行内显示（样稿 05）：名字下面一句话写平时多久一次、上次做了什么、谁负责；右边大字写多久没来，小字写卡里还有什么。
function displayName(c: Overview['customer']) {
  return c.name || '未填写姓名'
}

function span(days: number, round = Math.floor) {
  return days >= 14 ? `${round(days / 7)} 周` : `${days} 天`
}

function rowMeta(row: Overview) {
  const last = row.last_visit_date
    ? ['上次', monthDay(row.last_visit_date), row.last_visit_items.join('、'), row.last_visit_staff.map(s => s.name).join('、')].filter(Boolean).join(' ')
    : ''
  return [
    row.cycle_days ? `平时 ${span(row.cycle_days, Math.round)}` : '',
    last,
    row.customer.responsible ? `${row.customer.responsible.name}负责` : ''
  ].filter(Boolean).join(' · ')
}

function rowSince(row: Overview) {
  if (row.next_appointment) return [`下次 ${nextText(row.next_appointment.starts_at)}`, row.next_appointment.staff.name].filter(Boolean).join(' ')
  const days = row.days_since_last_visit
  if (days === null || days === undefined) return '还没来过'
  if (row.overdue_days) return `${span(days)}没来`
  return days <= 1 ? `${sinceText(days)}来过` : `${span(days)}前来过`
}

function rowCards(row: Overview) {
  const parts = [
    sort.value === 'spend' ? `累计 ${yuan(row.total_spend)}` : '',
    Number(row.stored_balance) > 0 ? `卡余 ${yuan(row.stored_balance)}` : '',
    row.count_remaining ? `次卡剩 ${row.count_remaining} 次` : ''
  ]
  return parts.filter(Boolean).join(' · ') || '没有卡'
}

// 该回来了按晚了几天排时分两段：晚了一周以上的先打。其余情况不分段。
const groups = computed(() => segment.value === 'due' && sort.value === 'overdue'
  ? [
      { title: '晚了 1 周以上', rows: items.value.filter(r => (r.overdue_days ?? 0) >= 7) },
      { title: '晚了不到 1 周', rows: items.value.filter(r => (r.overdue_days ?? 0) < 7) }
    ].filter(g => g.rows.length)
  : [{ title: '', rows: items.value }])

// 右边三个字：搜索（平时收窄）、筛选（用了写成小签）、···（排序、负责人交接）。
const responsibleName = computed(() => staff.value.find(s => s.id === responsible.value)?.name ?? '')
const filterItems = computed(() => [[{
  label: '负责人',
  icon: 'i-lucide-user-round',
  children: [staff.value.map(s => ({
    label: s.name + (s.active ? '' : '（已停用）'),
    type: 'checkbox' as const,
    checked: responsible.value === s.id,
    onUpdateChecked: (checked: boolean) => {
      responsible.value = checked ? s.id : NONE
    }
  }))]
}]])
const moreItems = computed(() => [
  [
    { type: 'label' as const, label: '排序' },
    ...sortItems.map(s => ({
      label: s.label,
      type: 'checkbox' as const,
      checked: sort.value === s.value,
      onUpdateChecked: () => {
        sort.value = s.value as SortKey
      }
    }))
  ],
  ...(can('customer.assign')
    ? [[{ label: '负责人交接', icon: 'i-lucide-arrow-left-right', onSelect: () => {
        handoverOpen.value = true
      } }]]
    : [])
])

// Esc 取消批量选择；弹窗开着时 Esc 只关弹窗。
defineShortcuts({
  escape: () => {
    if (selected.value.length && !assignOpen.value && !handoverOpen.value) selected.value = []
  }
})

// 新建顾客
const createOpen = ref(false)
const creating = ref(false)
const profile = reactive({
  phone: '',
  name: '',
  gender: NONE,
  source: NONE,
  note: ''
})
const sourceItems = computed(() => [
  { label: '不设来源', value: NONE },
  ...sources.value.filter(s => s.active).map(s => ({ label: s.name, value: s.id }))
])
const genderItems = [
  { label: '未填写', value: NONE },
  { label: '男', value: 'male' },
  { label: '女', value: 'female' }
]

function openCreate() {
  Object.assign(profile, {
    phone: '',
    name: '',
    gender: NONE,
    source: NONE,
    note: ''
  })
  createOpen.value = true
}

async function create() {
  if (creating.value) return
  creating.value = true
  try {
    const res = await api.POST('/api/ops/create_customer', {
      body: {
        phone: profile.phone || undefined,
        name: profile.name || undefined,
        gender: profile.gender === NONE ? undefined : profile.gender as 'male' | 'female',
        source_id: profile.source === NONE ? undefined : profile.source,
        note: profile.note || undefined
      }
    })
    if (!res.data) {
      done(res.error, null, '')
      return
    }
    toast.add({
      title: res.data.existed ? '这个手机号已有档案' : '顾客已新建',
      color: res.data.existed ? 'info' : 'success'
    })
    createOpen.value = false
    openDetail(res.data.customer.id)
    reload()
  } finally {
    creating.value = false
  }
}

// 顾客档案是整页 /customers/<id>；旧网址 /customers?id=<id> 转过去，修改记录分页保留。
function openDetail(id: string) {
  navigateTo(`/customers/${id}`)
}

if (typeof route.query.id === 'string') {
  await navigateTo({ path: `/customers/${route.query.id}`, query: route.query.tab === 'logs' ? { tab: 'logs' } : {} }, { replace: true })
}

// 分配负责人
const assignOpen = ref(false)
const assignStaff = ref(NONE)
const assigning = ref(false)

function openAssign() {
  assignStaff.value = NONE
  assignOpen.value = true
}

async function assign() {
  if (assigning.value) return
  assigning.value = true
  try {
    const res = await api.POST('/api/ops/assign_customer_responsible', {
      body: {
        customer_ids: selected.value,
        staff_id: assignStaff.value === NONE ? undefined : assignStaff.value
      }
    })
    if (done(res.error, res.data, '负责人已分配')) {
      assignOpen.value = false
    }
  } finally {
    assigning.value = false
  }
}

// 负责人交接
const handoverOpen = ref(false)
const fromStaff = ref('')
const toStaff = ref('')
const handingOver = ref(false)

async function handover() {
  if (handingOver.value) return
  handingOver.value = true
  try {
    const res = await api.POST('/api/ops/hand_over_customers', {
      body: { from_staff_id: fromStaff.value, to_staff_id: toStaff.value }
    })
    if (done(res.error, res.data, `已移交 ${res.data?.count ?? 0} 位顾客`)) {
      handoverOpen.value = false
    }
  } finally {
    handingOver.value = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton v-if="can('customer.edit')" size="sm" icon="i-lucide-plus" @click="openCreate">新建顾客</UButton>
    </PageActions>

    <!-- 样稿 05、L3：页签就是常用分组；右边只放「搜索 筛选 ···」；用了的筛选写成一行能删的小签，人数跟在后面。 -->
    <div class="space-y-2">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <ViewTabs v-model="segment" :items="tiles" label="顾客分组" testid="segment" />
        <div class="ms-auto flex items-center gap-1">
          <UInput
            v-model="query"
            icon="i-lucide-search"
            variant="ghost"
            aria-label="搜索顾客"
            :placeholder="query || searchFocused ? '手机号、后四位或姓名' : '搜索'"
            class="transition-[width]"
            :class="query ? 'w-56' : 'w-24 focus-within:w-56'"
            @focus="searchFocused = true"
            @blur="searchFocused = false"
            @keydown.enter="search"
          />
          <UDropdownMenu :items="filterItems" :content="{ align: 'end' }">
            <UButton color="neutral" variant="ghost" class="text-muted">筛选</UButton>
          </UDropdownMenu>
          <UDropdownMenu :items="moreItems" :content="{ align: 'end' }">
            <UButton color="neutral" variant="ghost" icon="i-lucide-ellipsis" class="text-muted" aria-label="更多操作" />
          </UDropdownMenu>
        </div>
      </div>
      <div class="flex min-h-7 flex-wrap items-center gap-2">
        <span v-if="responsibleName" class="inline-flex h-7 items-center gap-1 rounded-md bg-selected ps-2 text-sm" data-testid="filter-chip">
          <span class="text-muted">负责人 是</span>{{ responsibleName }}
          <button type="button" class="grid size-6 place-items-center rounded-md text-muted hover:text-default" aria-label="去掉负责人筛选" @click="responsible = NONE">
            <UIcon name="i-lucide-x" class="size-3.5" />
          </button>
        </span>
        <span v-if="sort !== defaultSort" class="inline-flex h-7 items-center gap-1 rounded-md bg-selected ps-2 text-sm" data-testid="sort-chip">
          <span class="text-muted">排序</span>{{ sortName }}
          <button type="button" class="grid size-6 place-items-center rounded-md text-muted hover:text-default" aria-label="恢复默认排序" @click="sort = defaultSort">
            <UIcon name="i-lucide-x" class="size-3.5" />
          </button>
        </span>
        <span class="ms-auto text-meta text-muted tabular-nums" data-testid="customer-total">{{ total }} 位<template v-if="activeHint"> · {{ activeHint }}</template></span>
      </div>
    </div>

    <div data-testid="customer-list" :aria-busy="loading" :class="{ 'pb-20': selected.length }">
      <section v-for="group in groups" :key="group.title" class="mb-4">
        <h2 v-if="group.title" class="mb-1 text-meta text-muted">{{ group.title }}</h2>
        <div class="divide-y divide-default">
          <div
            v-for="row in group.rows"
            :key="row.customer.id"
            data-testid="customer-row"
            class="group/row -mx-3 flex cursor-pointer items-center gap-3 rounded-card px-3 py-3 hover:bg-hover"
            :class="selected.includes(row.customer.id) && 'bg-hover'"
            @click="openDetail(row.customer.id)"
          >
            <span v-if="can('customer.assign')" class="relative size-8 shrink-0" @click.stop>
              <span class="absolute inset-0 grid place-items-center rounded-full bg-tag-gray text-sm transition-opacity" :class="[selected.length || selected.includes(row.customer.id) ? 'opacity-0' : 'group-hover/row:opacity-0 group-focus-within/row:opacity-0 any-pointer-coarse:opacity-0']" aria-hidden="true">{{ displayName(row.customer).slice(0, 1) }}</span>
              <UCheckbox
                class="absolute inset-0 grid place-items-center transition-opacity"
                :class="selected.length ? '' : 'opacity-0 group-hover/row:opacity-100 focus-within:opacity-100 any-pointer-coarse:opacity-100'"
                :aria-label="`选择顾客 ${displayName(row.customer)}`"
                :model-value="selected.includes(row.customer.id)"
                @update:model-value="v => toggle(row.customer.id, v)"
              />
            </span>
            <span v-else class="grid size-8 shrink-0 place-items-center rounded-full bg-tag-gray text-sm" aria-hidden="true">{{ displayName(row.customer).slice(0, 1) }}</span>
            <div class="min-w-0 flex-1">
              <p class="flex flex-wrap items-center gap-2">
                <button type="button" class="min-w-0 text-start font-medium break-all hover:underline" @click.stop="openDetail(row.customer.id)">{{ displayName(row.customer) }}</button>
                <span v-for="label in row.customer.active_safety_labels" :key="label.id" class="tag bg-tag-red">{{ labelNames[label.kind] || '安全标签' }}</span>
              </p>
              <p class="truncate text-meta text-muted" :title="rowMeta(row)">
                <span class="tabular-nums" data-testid="customer-phone">{{ row.customer.phone }}</span><template v-if="rowMeta(row)"> · {{ rowMeta(row) }}</template>
              </p>
            </div>
            <div class="shrink-0 text-right">
              <p class="flex items-center justify-end gap-2 tabular-nums">
                <span v-if="row.overdue_days" class="tag bg-tag-yellow">晚了 {{ span(row.overdue_days) }}</span>
                {{ rowSince(row) }}
              </p>
              <p class="text-meta text-muted tabular-nums">{{ rowCards(row) }}</p>
            </div>
          </div>
        </div>
      </section>
      <template v-if="!items.length && !loading">
        <EmptyState v-if="query || responsible !== NONE" title="没有找到顾客" hint="换个手机号或姓名试试">
          <UButton color="neutral" variant="outline" @click="clearFilters">清除筛选</UButton>
        </EmptyState>
        <EmptyState v-else v-bind="emptyTexts[segment]" art="chair" />
      </template>
      <ListSkeleton v-if="!items.length && loading" />
    </div>
    <div v-if="total > PAGE" class="flex items-center justify-end gap-2 text-meta text-muted">
      <UButton color="neutral" variant="ghost" icon="i-lucide-chevron-left" :disabled="offset === 0 || loading" @click="changePage(-1)">上一页</UButton>
      <span class="tabular-nums">第 {{ offset / PAGE + 1 }} 页，共 {{ Math.ceil(total / PAGE) }} 页</span>
      <UButton color="neutral" variant="ghost" trailing-icon="i-lucide-chevron-right" :disabled="offset + PAGE >= total || loading" @click="changePage(1)">下一页</UButton>
    </div>

    <!-- 批量（样稿 L4）：勾了才从底下浮出一条墨色操作栏，平时不放「批量」按钮。 -->
    <div v-if="can('customer.assign') && selected.length" class="pointer-events-none fixed inset-x-0 bottom-6 z-40 flex justify-center">
      <div class="pointer-events-auto flex items-center gap-1 rounded-card bg-inverted p-2 ps-4 text-sm text-white shadow-lift" role="toolbar" aria-label="批量操作" data-testid="bulk-bar">
        <span class="me-2 tabular-nums" aria-live="polite">选了 {{ selected.length }} 位</span>
        <button type="button" class="h-8 rounded-md bg-white/10 px-3 hover:bg-white/20" @click="openAssign">交给别人负责</button>
        <button type="button" class="grid size-8 place-items-center rounded-md text-white/70 hover:bg-white/10 hover:text-white" aria-label="取消选择" @click="selected = []">
          <UIcon name="i-lucide-x" class="size-4" />
        </button>
      </div>
    </div>
    <UModal v-model:open="createOpen" title="新建顾客">
      <template #body>
        <div class="space-y-5">
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="手机号">
              <UInput v-model="profile.phone" type="tel" class="w-full" />
            </UFormField>
            <UFormField label="姓名">
              <UInput v-model="profile.name" class="w-full" />
            </UFormField>
          </div>
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
        <ModalActions @cancel="createOpen = false">
          <UButton :loading="creating" @click="create">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-if="can('customer.assign')" v-model:open="assignOpen" title="交给别人负责" :description="`选中的 ${selected.length} 位顾客交给谁负责。`">
      <template #body>
        <UFormField label="负责人">
          <USelect v-model="assignStaff" :items="[{ label: '不设负责人', value: NONE }, ...activeStaffItems]" class="w-full" />
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="assignOpen = false">
          <UButton :loading="assigning" @click="assign">确认分配</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-if="can('customer.assign')" v-model:open="handoverOpen" title="负责人交接" description="确认后，将交出员工负责的全部顾客移交给接手员工。">
      <template #body>
        <div class="space-y-5">
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="交出的员工">
              <USelect v-model="fromStaff" :items="staffItems" class="w-full" />
            </UFormField>
            <UFormField label="接手的员工">
              <USelect v-model="toStaff" :items="activeStaffItems" class="w-full" />
            </UFormField>
          </div>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="handoverOpen = false">
          <UButton :disabled="!fromStaff || !toStaff || fromStaff === toStaff" :loading="handingOver" @click="handover">确认交接</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
