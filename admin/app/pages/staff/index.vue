<script setup lang="ts">
import type { components } from '~/utils/api'

// 员工名册（样稿 06）：按岗位分组的两行式行，名字下一句话写级别、角色、编号；右边大字写本月项目业绩，小字写今天的班次。
// 点一行进员工详情 /staff/<id>，改档案、角色、登录账号、停用都在详情页。
definePageMeta({ permission: 'staff.manage', title: '员工', art: 'comb' })

type Staff = components['schemas']['Staff']
const api = useApi()
const toast = useToast()
const { can } = useMe()

const staff = ref<Staff[]>([])
const positions = ref<components['schemas']['Position'][]>([])
const performance = ref<Map<string, string> | null>(null)
const shifts = ref(new Map<string, components['schemas']['Shift'] | null>())
const loadGuard = useRequestGuard()
const loading = ref(false)
const loadError = ref('')
const editors = useTemplateRef('editors')

async function load() {
  const request = loadGuard.next()
  loading.value = true
  try {
    const [s, p] = await Promise.all([
      api.POST('/api/ops/list_staff', { body: {} }),
      api.POST('/api/ops/list_positions', { body: {} })
    ])
    if (!request()) return
    if (!s.data || !p.data) {
      // 名册还没读到过时原地给一块「重试」，别只弹提示留一片空白；已经有名册时（刷新失败）弹提示就够。
      if (staff.value.length) toast.add({ title: errorText(s.error ?? p.error), color: 'error' })
      else loadError.value = errorText(s.error ?? p.error)
      return
    }
    loadError.value = ''
    staff.value = s.data.staff
    positions.value = p.data.items
  } finally {
    // 网络断了也要停掉骨架屏；只有最新一次请求能改。
    if (request()) loading.value = false
  }
}

// 本月业绩和今天的班次是补充信息：没权限或取不到就不写，不挡名册。
const storeToday = useStoreToday()

async function loadFacts() {
  const today = (await storeToday.load())?.business_date
  if (!today) return
  await Promise.all([
    can('performance.view') && api.POST('/api/ops/get_staff_report', { body: { from: monthStart(today), to: today } }).then((r) => {
      if (r.data) performance.value = new Map(r.data.rows.filter(row => row.staff).map(row => [row.staff!.id, row.item_performance]))
    }),
    can('attendance.manage') && api.POST('/api/ops/list_staff_shifts', { body: { date: today } }).then((r) => {
      if (r.data) shifts.value = new Map(r.data.entries.map(e => [e.staff.id, e.current?.shift ?? null]))
    })
  ])
}

onMounted(() => {
  load()
  loadFacts()
})

// 视图页签：在职（默认）、已停用。
const filterKey = ref<'active' | 'inactive'>('active')
const search = ref('')
const filterTabs = computed(() => [
  { key: 'active' as const, label: '在职', count: loading.value && !staff.value.length ? undefined : staff.value.filter(s => s.active).length },
  { key: 'inactive' as const, label: '已停用', count: loading.value && !staff.value.length ? undefined : staff.value.filter(s => !s.active).length }
])

const shown = computed(() => {
  const term = search.value.trim()
  return staff.value.filter(s => s.active === (filterKey.value === 'active') && (!term || s.name.includes(term) || s.number.includes(term)))
})

// 按岗位分组，岗位按设置里的顺序，没设岗位的放最后。
const groups = computed(() => {
  const order = new Map(positions.value.map((p, index) => [p.id, index]))
  const map = new Map<string, { key: string, title: string, rank: number, rows: Staff[] }>()
  for (const s of shown.value) {
    const key = s.position?.id ?? 'none'
    if (!map.has(key)) map.set(key, { key, title: s.position?.name ?? '没设岗位', rank: order.get(key) ?? Number.MAX_SAFE_INTEGER, rows: [] })
    map.get(key)!.rows.push(s)
  }
  return [...map.values()].sort((a, b) => a.rank - b.rank)
})

function rowMeta(s: Staff) {
  return [
    s.level?.name,
    s.roles.map(r => r.name).join('、'),
    s.number ? `编号 ${s.number}` : '',
    s.login_account ? '' : '没开登录账号',
    s.self_bookable ? '小程序可约' : ''
  ].filter(Boolean).join(' · ')
}

// 「本月业绩」只在第一行的数字下写一次，下面各行同一列不再重复（样稿 06）。
const labelledId = computed(() => groups.value[0]?.rows[0]?.id)

function shiftText(s: Staff) {
  if (!s.active || !shifts.value.has(s.id)) return ''
  const shift = shifts.value.get(s.id)
  return shift ? `今天${shift.name}` : '没排班'
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton @click="editors?.openProfile(null)">新建员工</UButton>
    </PageActions>

    <div class="space-y-2">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <ViewTabs v-model="filterKey" :items="filterTabs" label="筛选员工" />
        <UInput v-model="search" icon="i-lucide-search" variant="ghost" :placeholder="search ? '' : '姓名或编号'" aria-label="搜索姓名或编号" class="ms-auto w-40" />
      </div>
    </div>

    <div data-testid="staff-list" :aria-busy="loading">
      <section v-for="group in groups" :key="group.key" class="mb-4" :data-position="group.title">
        <h2 class="mb-1 text-meta text-muted">{{ group.title }}</h2>
        <div class="divide-y divide-default">
          <NuxtLink
            v-for="s in group.rows"
            :key="s.id"
            :to="`/staff/${s.id}`"
            data-testid="staff-row"
            class="flex items-center gap-3 rounded-card py-3 hover:bg-hover sm:-mx-3 sm:px-3"
          >
            <PersonAvatar :name="s.name" />
            <div class="min-w-0 flex-1">
              <p class="flex items-center gap-2">
                <span class="font-medium" data-staff-name>{{ s.name }}</span>
                <span v-if="!s.active" class="tag bg-tag-gray">已停用</span>
              </p>
              <p class="truncate text-meta text-muted">{{ rowMeta(s) || '没设级别和角色' }}</p>
            </div>
            <div class="shrink-0 text-right">
              <p v-if="performance && s.active" class="tabular-nums" :class="zeroText(performance.get(s.id) ?? 0) && 'text-dimmed'" data-testid="staff-performance">{{ yuan(performance.get(s.id) ?? 0) }}</p>
              <p class="text-meta text-muted">{{ [performance && s.active && s.id === labelledId ? '本月业绩' : '', shiftText(s)].filter(Boolean).join(' · ') }}</p>
            </div>
          </NuxtLink>
        </div>
      </section>
      <ListSkeleton v-if="loading && !staff.length" />
      <LoadError v-else-if="loadError && !staff.length" title="员工名册没有读出来" :hint="loadError" @retry="load()" />
      <template v-else-if="!shown.length">
        <EmptyState v-if="!staff.length" art="chair" title="还没有员工" hint="点右上角新建员工，再开登录账号和角色" />
        <EmptyState v-else :title="search.trim() ? '没有找到这位员工' : filterKey === 'active' ? '没有在职员工' : '没有停用的员工'" :hint="search.trim() ? '换个姓名或编号试试' : undefined" />
      </template>
    </div>

    <StaffEditors ref="editors" @changed="load" />
  </div>
</template>
