<script setup lang="ts">
import type { components, paths } from '~/utils/api'

definePageMeta({ permission: 'customer.view', title: '顾客' })

type Overview = paths['/api/ops/list_customer_overview']['post']['responses']['200']['content']['application/json']['items'][number]
type Segments = paths['/api/ops/get_customer_segments']['post']['responses']['200']['content']['application/json']
type SegmentKey = 'all' | 'today' | 'due' | 'balance' | 'new' | 'sleeping'
type SortKey = 'last_visit' | 'overdue' | 'spend' | 'created'
const api = useApi()
const toast = useToast()
const { can } = useMe()
const route = useRoute()
const router = useRouter()
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

watch([segment, sort], search)
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

// 行内显示
function displayName(c: Overview['customer']) {
  return c.name || '未填写姓名'
}

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

// 顾客详情
const detailID = computed(() => typeof route.query.id === 'string' ? route.query.id : '')

// 详情右栏的分页也写在网址里（?tab=cards），换顾客时回到概览。
const detailTab = computed({
  get: () => typeof route.query.tab === 'string' ? route.query.tab : 'overview',
  set: tab => router.replace({ query: { ...route.query, tab: tab === 'overview' ? undefined : tab } })
})

function openDetail(id: string) {
  router.replace({ query: { ...route.query, id, tab: undefined } })
}

function closeDetail() {
  router.replace({ query: { ...route.query, id: undefined, tab: undefined } })
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
      <UDropdownMenu v-if="can('customer.assign')" :items="[[{ label: '负责人交接', icon: 'i-lucide-arrow-left-right', onSelect: () => { handoverOpen = true } }]]" :content="{ align: 'end' }">
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-ellipsis" aria-label="更多操作" />
      </UDropdownMenu>
      <UButton v-if="can('customer.edit')" size="sm" icon="i-lucide-plus" @click="openCreate">新建顾客</UButton>
    </PageActions>

    <!-- 照 Notion 数据库的工具条：第一行视图页签和无框搜索，第二行筛选、排序和人数。 -->
    <div class="space-y-1">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <ViewTabs v-model="segment" :items="tiles" label="顾客分组" testid="segment" />
        <UInput v-model="query" icon="i-lucide-search" variant="ghost" aria-label="搜索顾客" placeholder="手机号、后四位或姓名" class="ms-auto w-56" @keydown.enter="search" />
      </div>
      <div class="flex flex-wrap items-center gap-1">
        <USelect v-model="responsible" variant="ghost" aria-label="负责人筛选" :items="[{ label: '全部负责人', value: NONE }, ...staffItems]" class="min-w-32 text-muted" />
        <USelect v-model="sort" variant="ghost" aria-label="排序" :items="sortItems" class="min-w-32 text-muted" />
        <div v-if="can('customer.assign') && selected.length" class="ms-2 flex items-center gap-2">
          <span class="text-meta text-muted">已选 {{ selected.length }} 位</span>
          <UButton size="sm" color="neutral" variant="outline" @click="openAssign">分配负责人</UButton>
        </div>
        <span class="ms-auto text-meta text-muted tabular-nums" data-testid="customer-total">共 {{ total }} 位<template v-if="activeHint"> · {{ activeHint }}</template></span>
      </div>
    </div>

    <div class="border-b border-default" data-testid="customer-list" :aria-busy="loading">
      <div class="grid gap-4 grid-cols-[minmax(0,2.2fr)_minmax(0,2fr)_8rem_8rem] xl:grid-cols-[minmax(0,2.2fr)_minmax(0,2fr)_6rem_8rem_8rem_8rem] border-b border-default px-3 py-2 text-sm text-muted">
        <span>顾客</span><span>上次到店</span><span class="max-xl:hidden">来过</span><span class="text-right max-xl:hidden">累计消费</span><span class="text-right">卡余额</span><span class="ps-4">下次</span>
      </div>
      <div
        v-for="row in items"
        :key="row.customer.id"
        data-testid="customer-row"
        class="group/row grid gap-4 grid-cols-[minmax(0,2.2fr)_minmax(0,2fr)_8rem_8rem] xl:grid-cols-[minmax(0,2.2fr)_minmax(0,2fr)_6rem_8rem_8rem_8rem] cursor-pointer items-start border-t border-default px-3 py-3 text-sm first-of-type:border-t-0 hover:bg-hover"
        @click="openDetail(row.customer.id)"
      >
        <div class="flex min-w-0 gap-3">
          <span v-if="can('customer.assign')" class="relative size-9 shrink-0" @click.stop>
            <span class="absolute inset-0 grid place-items-center rounded-full bg-tag-gray font-semibold transition-opacity" :class="[selected.includes(row.customer.id) ? 'opacity-0' : 'group-hover/row:opacity-0 group-focus-within/row:opacity-0 any-pointer-coarse:opacity-0']" aria-hidden="true">{{ displayName(row.customer).slice(0, 1) }}</span>
            <UCheckbox
              class="absolute inset-0 grid place-items-center transition-opacity"
              :class="selected.includes(row.customer.id) ? '' : 'opacity-0 group-hover/row:opacity-100 focus-within:opacity-100 any-pointer-coarse:opacity-100'"
              :aria-label="`选择顾客 ${displayName(row.customer)}`"
              :model-value="selected.includes(row.customer.id)"
              @update:model-value="v => toggle(row.customer.id, v)"
            />
          </span>
          <span v-else class="grid size-9 shrink-0 place-items-center rounded-full bg-tag-gray font-semibold" aria-hidden="true">{{ displayName(row.customer).slice(0, 1) }}</span>
          <div class="min-w-0">
            <p class="flex flex-wrap items-center gap-1.5">
              <button type="button" class="font-semibold hover:underline" @click.stop="openDetail(row.customer.id)">{{ displayName(row.customer) }}</button>
              <span v-for="label in row.customer.active_safety_labels" :key="label.id" class="tag bg-tag-red">{{ labelNames[label.kind] || '安全标签' }}</span>
            </p>
            <p class="truncate text-meta text-muted" :title="row.customer.responsible ? `负责人 ${row.customer.responsible.name}` : undefined">
              <span class="tabular-nums" data-testid="customer-phone">{{ row.customer.phone }}</span>
              <template v-if="row.customer.responsible"> · 负责人 {{ row.customer.responsible.name }}</template>
            </p>
          </div>
        </div>
        <div class="min-w-0">
          <template v-if="row.days_since_last_visit !== null && row.days_since_last_visit !== undefined">
            <p class="flex flex-wrap items-center gap-1.5">
              <span class="tabular-nums">{{ sinceText(row.days_since_last_visit) }}</span>
              <span v-if="row.overdue_days" class="tag bg-tag-orange">晚了 {{ row.overdue_days }} 天</span>
            </p>
            <p class="truncate text-meta text-muted" :title="lastVisitMeta(row)">{{ lastVisitMeta(row) }}</p>
          </template>
          <p v-else class="text-muted">还没来过</p>
        </div>
        <div class="max-xl:hidden">
          <template v-if="row.visit_count">
            <p class="tabular-nums">{{ row.visit_count }} 次</p>
            <p class="text-meta text-muted">{{ row.cycle_days ? `约 ${row.cycle_days} 天一次` : '只来过一天' }}</p>
          </template>
        </div>
        <div class="text-right max-xl:hidden">
          <template v-if="row.visit_count">
            <p class="tabular-nums">{{ yuanFixed(row.total_spend) }}</p>
            <p class="text-meta text-muted tabular-nums">平均每次 {{ yuanFixed(averageSpend(row)) }}</p>
          </template>
        </div>
        <div class="text-right">
          <template v-if="Number(row.stored_balance) > 0">
            <p class="tabular-nums">{{ yuanFixed(row.stored_balance) }}</p>
            <p class="text-meta text-muted">{{ row.count_remaining ? `另有 ${row.count_remaining} 次` : '储值' }}</p>
          </template>
          <template v-else-if="row.count_remaining">
            <p class="tabular-nums">{{ row.count_remaining }} 次</p>
            <p class="text-meta text-muted">次卡</p>
          </template>
          <p v-else class="text-dimmed">没有</p>
        </div>
        <div class="min-w-0 ps-4">
          <span v-if="row.next_appointment" class="tag bg-tag-blue tabular-nums">{{ nextText(row.next_appointment.starts_at) }}</span>
          <p v-if="row.next_appointment" class="mt-0.5 truncate text-meta text-muted" :title="row.next_appointment.staff.name">{{ row.next_appointment.staff.name }}</p>
        </div>
      </div>
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
    <CustomerDetail v-if="detailID" :id="detailID" v-model:tab="detailTab" :sources="sources" :overview="items.find(row => row.customer.id === detailID)" @close="closeDetail" @open="openDetail" @changed="reload" />
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
    <UModal v-if="can('customer.assign')" v-model:open="assignOpen" title="分配负责人" :description="`为选中的 ${selected.length} 位顾客分配负责人。`">
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
