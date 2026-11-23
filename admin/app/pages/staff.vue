<script setup lang="ts">
import type { DropdownMenuItem, TableRow } from '@nuxt/ui'
import type { components } from '~/utils/api'

definePageMeta({ permission: 'staff.manage', title: '员工' })

type Staff = components['schemas']['Staff']
const api = useApi()
const toast = useToast()

const staff = ref<Staff[]>([])
const roles = ref<components['schemas']['RoleOption'][]>([])
const levels = ref<components['schemas']['Level'][]>([])
const positions = ref<components['schemas']['Position'][]>([])

const NONE = 'none'

const loadGuard = useRequestGuard()
const loading = ref(false)

async function load() {
  const request = loadGuard.next()
  loading.value = true
  let s, r, l, p
  try {
    [s, r, l, p] = await Promise.all([
      api.POST('/api/ops/list_staff', { body: {} }),
      api.POST('/api/ops/list_role_options', { body: {} }),
      api.POST('/api/ops/list_levels', { body: {} }),
      api.POST('/api/ops/list_positions', { body: {} })
    ])
  } finally {
    // 网络断了也要停掉骨架屏；只有最新一次请求能改。
    if (request()) loading.value = false
  }
  if (!request()) return
  if (!s.data || !r.data || !l.data || !p.data) {
    toast.add({ title: errorText(s.error ?? r.error ?? l.error ?? p.error), color: 'error' })
    return
  }
  staff.value = s.data?.staff ?? []
  roles.value = r.data?.roles ?? []
  levels.value = l.data?.items ?? []
  positions.value = p.data?.items ?? []
}

onMounted(load)

const roleItems = computed(() => roles.value.map(r => ({ label: r.name, value: r.id })))
const optionItems = (list: (components['schemas']['Level'] | components['schemas']['Position'])[]) => [
  { label: '不设', value: NONE },
  ...list.filter(o => o.active).map(o => ({ label: o.name, value: o.id }))
]

// 列表照 DESIGN.md 列表页：第一列头像、姓名和编号；空值留白；状态只在不正常时出现（已停用标签）。
const columns = [
  { id: 'who', header: '员工' },
  { id: 'position', header: '岗位' },
  { id: 'level', header: '级别' },
  { id: 'roles', header: '角色' },
  { id: 'login', header: '登录账号' },
  { id: 'self_bookable', header: '小程序预约' },
  { id: 'actions', header: '' }
]

// 视图页签：在职（默认）、人数最多的两个岗位、未开通账号、已停用。
interface Filter { key: string, label: string, empty: string, test: (s: Staff) => boolean }
const filterKey = ref('active')
const search = ref('')

const filters = computed<Filter[]>(() => {
  const counts = new Map<string, { id: string, name: string, count: number }>()
  for (const s of staff.value) {
    if (!s.active || !s.position) continue
    const entry = counts.get(s.position.id) ?? { id: s.position.id, name: s.position.name, count: 0 }
    entry.count++
    counts.set(s.position.id, entry)
  }
  const top = [...counts.values()].sort((a, b) => b.count - a.count).slice(0, 2)
  return [
    { key: 'active', label: '在职', empty: '没有在职员工', test: s => s.active },
    ...top.map(p => ({ key: `position:${p.id}`, label: p.name, empty: `没有在职的${p.name}`, test: (s: Staff) => s.active && s.position?.id === p.id })),
    { key: 'no-login', label: '未开通账号', empty: '在职员工都开通了登录账号', test: s => s.active && !s.login_account },
    { key: 'inactive', label: '已停用', empty: '没有停用的员工', test: s => !s.active }
  ]
})

const filterTabs = computed(() => filters.value.map(f => ({ key: f.key, label: f.label, count: loading.value && !staff.value.length ? undefined : staff.value.filter(f.test).length })))
const filter = computed(() => filters.value.find(f => f.key === filterKey.value) ?? filters.value[0]!)
// 岗位筛选的岗位没人了（例如刚停用最后一个人），回到在职。
watch(filters, (list) => {
  if (!list.some(f => f.key === filterKey.value)) filterKey.value = 'active'
})

const shown = computed(() => {
  const term = search.value.trim()
  return staff.value.filter(s => filter.value.test(s) && (!term || s.name.includes(term) || s.number.includes(term)))
})

function rowActions(s: Staff): DropdownMenuItem[][] {
  return [
    [
      { label: '修改档案', icon: 'i-lucide-pencil', onSelect: () => openProfile(s) },
      { label: '设置角色', icon: 'i-lucide-shield', onSelect: () => openRoles(s) },
      { label: '登录账号', icon: 'i-lucide-key-round', onSelect: () => openLogin(s) }
    ],
    [s.active
      ? { label: '停用', icon: 'i-lucide-user-x', color: 'error', onSelect: () => setActive(s, false) }
      : { label: '恢复', icon: 'i-lucide-user-check', onSelect: () => setActive(s, true) }]
  ]
}

function selectRow(_event: Event, row: TableRow<Staff>) {
  openProfile(row.original)
}

function done(error: unknown, ok: unknown, message: string, current: () => boolean = () => true) {
  if (ok) {
    if (current()) toast.add({ title: message, color: 'success' })
    load()
    return current()
  }
  toast.add({ title: errorText(error), color: 'error' })
  return false
}

// 新建、修改档案
const profileEditor = useModalForm()
const { open: profileOpen, saving: profileSaving } = profileEditor
const editing = ref<Staff | null>(null)
const profile = reactive({ name: '', number: '', level: NONE, position: NONE, role_ids: [] as string[], self_bookable: false })

function openProfile(s: Staff | null) {
  profileEditor.begin(() => {
    editing.value = s
    Object.assign(profile, {
      name: s?.name ?? '',
      number: s?.number ?? '',
      level: s?.level?.id ?? NONE,
      position: s?.position?.id ?? NONE,
      role_ids: [],
      self_bookable: s?.self_bookable ?? false
    })
  })
}

async function saveProfile() {
  await profileEditor.save(async (current) => {
    const common = {
      name: profile.name,
      number: profile.number,
      level_id: profile.level === NONE ? undefined : profile.level,
      position_id: profile.position === NONE ? undefined : profile.position,
      self_bookable: profile.self_bookable
    }
    const res = editing.value
      ? await api.POST('/api/ops/update_staff', { body: { ...common, staff_id: editing.value.id } })
      : await api.POST('/api/ops/create_staff', { body: { ...common, role_ids: profile.role_ids } })
    if (done(res.error, res.data, '已保存', current)) profileOpen.value = false
  })
}

// 角色
const rolesEditor = useModalForm()
const { open: rolesOpen, saving: rolesSaving } = rolesEditor
const roleTarget = ref<Staff | null>(null)
const roleSelection = ref<string[]>([])

function openRoles(s: Staff) {
  rolesEditor.begin(() => {
    roleTarget.value = s
    roleSelection.value = s.roles.map(r => r.id)
  })
}

async function saveRoles() {
  await rolesEditor.save(async (current) => {
    const res = await api.POST('/api/ops/set_staff_roles', {
      body: { staff_id: roleTarget.value!.id, role_ids: roleSelection.value }
    })
    if (done(res.error, res.data, '角色已更新', current)) rolesOpen.value = false
  })
}

// 登录账号
const loginEditor = useModalForm()
const { open: loginOpen, saving: loginSaving } = loginEditor
const loginTarget = ref<Staff | null>(null)
const login = reactive({ login_account: '', password: '' })

function openLogin(s: Staff) {
  loginEditor.begin(() => {
    loginTarget.value = s
    Object.assign(login, { login_account: s.login_account ?? '', password: '' })
  })
}

async function saveLogin() {
  await loginEditor.save(async (current) => {
    const res = await api.POST('/api/ops/set_staff_login', { body: { ...login, staff_id: loginTarget.value!.id } })
    if (done(res.error, res.data, '登录账号已设置', current)) loginOpen.value = false
  })
}

const activeGuard = useRequestGuard()
const activeSaving = reactive<Record<string, boolean>>({})

async function setActive(s: Staff, active: boolean) {
  if (activeSaving[s.id]) return
  const current = activeGuard.hold()
  activeSaving[s.id] = true
  try {
    const path = active ? '/api/ops/reactivate_staff' : '/api/ops/deactivate_staff'
    const res = await api.POST(path, { body: { staff_id: s.id } })
    if (!current()) return
    done(res.error, res.data, active ? '已恢复' : '已停用')
  } finally {
    if (current()) activeSaving[s.id] = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton size="sm" icon="i-lucide-plus" @click="openProfile(null)">新建员工</UButton>
    </PageActions>

    <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
      <ViewTabs v-model="filterKey" :items="filterTabs" label="筛选员工" />
      <UInput v-model="search" icon="i-lucide-search" placeholder="搜索姓名或编号" aria-label="搜索姓名或编号" class="w-full sm:w-64" />
      <span class="ms-auto text-meta text-dimmed tabular-nums" data-testid="staff-count">共 {{ shown.length }} 人</span>
    </div>

    <div class="overflow-x-auto">
      <UTable :data="shown" :columns="columns" :loading="loading" :aria-busy="loading" :ui="{ tr: 'group/row' }" @select="selectRow">
        <template #who-cell="{ row }">
          <div class="flex items-center gap-2.5" :class="row.original.active ? '' : 'opacity-60'">
            <StaffAvatar :name="row.original.name" />
            <div class="min-w-0">
              <div class="flex items-center gap-1.5">
                <span class="font-medium" data-staff-name>{{ row.original.name }}</span>
                <span v-if="!row.original.active" class="tag bg-tag-gray">已停用</span>
              </div>
              <div v-if="row.original.number" class="text-xs leading-tight text-dimmed tabular-nums">{{ row.original.number }}</div>
            </div>
          </div>
        </template>
        <template #position-cell="{ row }">{{ row.original.position?.name }}</template>
        <template #level-cell="{ row }">{{ row.original.level?.name }}</template>
        <template #roles-cell="{ row }">
          <div class="flex flex-wrap gap-1">
            <span v-for="r in row.original.roles" :key="r.id" class="tag bg-tag-gray">{{ r.name }}</span>
          </div>
        </template>
        <template #login-cell="{ row }">
          <span v-if="row.original.login_account" class="text-muted">{{ row.original.login_account }}</span>
          <span v-else class="text-dimmed">未开通</span>
        </template>
        <template #self_bookable-cell="{ row }">
          <span v-if="row.original.self_bookable" class="inline-flex items-center gap-1">
            <UIcon name="i-lucide-check" class="size-3.5 text-success" aria-hidden="true" />可约
          </span>
        </template>
        <template #actions-cell="{ row }">
          <div class="flex justify-end" @click.stop>
            <UDropdownMenu :items="rowActions(row.original)" :content="{ align: 'end' }">
              <UButton
                size="sm"
                color="neutral"
                variant="ghost"
                icon="i-lucide-ellipsis"
                :aria-label="`${row.original.name} 的操作`"
                :loading="activeSaving[row.original.id]"
                :class="activeSaving[row.original.id] ? '' : 'opacity-0'"
                class="group-hover/row:opacity-100 group-focus-visible/row:opacity-100 focus-visible:opacity-100 aria-expanded:opacity-100 any-pointer-coarse:opacity-100"
              />
            </UDropdownMenu>
          </div>
        </template>
        <template #loading>
          <ListSkeleton />
        </template>
        <template #empty>
          <EmptyState v-if="!staff.length" title="还没有员工" hint="点右上角新建员工，再开登录账号和角色" />
          <EmptyState v-else :title="search.trim() ? '没有找到这位员工' : filter.empty" :hint="search.trim() ? '换个姓名或编号试试' : undefined" />
        </template>
      </UTable>
    </div>

    <UModal v-model:open="profileOpen" :title="editing ? '修改档案' : '新建员工'">
      <template #body>
        <div class="space-y-3">
          <UFormField label="姓名"><UInput v-model="profile.name" class="w-full" /></UFormField>
          <UFormField label="编号"><UInput v-model="profile.number" class="w-full" /></UFormField>
          <UFormField label="级别"><USelect v-model="profile.level" :items="optionItems(levels)" class="w-full" /></UFormField>
          <UFormField label="岗位"><USelect v-model="profile.position" :items="optionItems(positions)" class="w-full" /></UFormField>
          <UFormField label="小程序预约">
            <UCheckbox v-model="profile.self_bookable" label="顾客可在小程序预约" aria-label="顾客可在小程序预约" />
          </UFormField>
          <UFormField v-if="!editing" label="角色">
            <USelectMenu v-model="profile.role_ids" :items="roleItems" value-key="value" multiple class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="profileOpen = false">
          <UButton :loading="profileSaving" @click="saveProfile">保存</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="rolesOpen" :title="`设置角色：${roleTarget?.name ?? ''}`">
      <template #body>
        <p class="text-sm text-muted mb-3">一个员工可以有多个角色，权限取各角色的并集。</p>
        <USelectMenu v-model="roleSelection" :items="roleItems" value-key="value" multiple class="w-full" />
      </template>
      <template #footer>
        <ModalActions @cancel="rolesOpen = false">
          <UButton :loading="rolesSaving" @click="saveRoles">保存</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="loginOpen" :title="`登录账号：${loginTarget?.name ?? ''}`">
      <template #body>
        <div class="space-y-3">
          <UFormField label="账号"><UInput v-model="login.login_account" class="w-full" /></UFormField>
          <UFormField label="新密码（至少 8 位）"><UInput v-model="login.password" type="password" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="loginOpen = false">
          <UButton :loading="loginSaving" @click="saveLogin">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
