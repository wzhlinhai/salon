<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'staff.manage' })

type Staff = components['schemas']['Staff']
const api = useApi()
const toast = useToast()

const staff = ref<Staff[]>([])
const roles = ref<components['schemas']['RoleOption'][]>([])
const levels = ref<components['schemas']['Level'][]>([])
const positions = ref<components['schemas']['Position'][]>([])

const NONE = 'none'

async function load() {
  const [s, r, l, p] = await Promise.all([
    api.POST('/api/ops/list_staff', { body: {} }),
    api.POST('/api/ops/list_role_options', { body: {} }),
    api.POST('/api/ops/list_levels', { body: {} }),
    api.POST('/api/ops/list_positions', { body: {} })
  ])
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

const columns = [
  { accessorKey: 'number', header: '编号' },
  { accessorKey: 'name', header: '姓名' },
  { id: 'level', header: '级别' },
  { id: 'position', header: '岗位' },
  { id: 'roles', header: '角色' },
  { accessorKey: 'login_account', header: '登录账号' },
  { id: 'status', header: '状态' },
  { id: 'actions', header: '' }
]

function done(error: unknown, ok: unknown, message: string) {
  if (ok) {
    toast.add({ title: message, color: 'success' })
    load()
    return true
  }
  toast.add({ title: errorText(error), color: 'error' })
  return false
}

// 新建、修改档案
const profileOpen = ref(false)
const editing = ref<Staff | null>(null)
const profile = reactive({ name: '', number: '', level: NONE, position: NONE, role_ids: [] as string[] })

function openProfile(s: Staff | null) {
  editing.value = s
  Object.assign(profile, {
    name: s?.name ?? '',
    number: s?.number ?? '',
    level: s?.level?.id ?? NONE,
    position: s?.position?.id ?? NONE,
    role_ids: []
  })
  profileOpen.value = true
}

async function saveProfile() {
  const common = {
    name: profile.name,
    number: profile.number,
    level_id: profile.level === NONE ? undefined : profile.level,
    position_id: profile.position === NONE ? undefined : profile.position
  }
  const res = editing.value
    ? await api.POST('/api/ops/update_staff', { body: { ...common, staff_id: editing.value.id } })
    : await api.POST('/api/ops/create_staff', { body: { ...common, role_ids: profile.role_ids } })
  if (done(res.error, res.data, '已保存')) profileOpen.value = false
}

// 角色
const rolesOpen = ref(false)
const roleTarget = ref<Staff | null>(null)
const roleSelection = ref<string[]>([])

function openRoles(s: Staff) {
  roleTarget.value = s
  roleSelection.value = s.roles.map(r => r.id)
  rolesOpen.value = true
}

async function saveRoles() {
  const res = await api.POST('/api/ops/set_staff_roles', {
    body: { staff_id: roleTarget.value!.id, role_ids: roleSelection.value }
  })
  if (done(res.error, res.data, '角色已更新')) rolesOpen.value = false
}

// 登录账号
const loginOpen = ref(false)
const loginTarget = ref<Staff | null>(null)
const login = reactive({ login_account: '', password: '' })

function openLogin(s: Staff) {
  loginTarget.value = s
  Object.assign(login, { login_account: s.login_account ?? '', password: '' })
  loginOpen.value = true
}

async function saveLogin() {
  const res = await api.POST('/api/ops/set_staff_login', { body: { ...login, staff_id: loginTarget.value!.id } })
  if (done(res.error, res.data, '登录账号已设置')) loginOpen.value = false
}

async function setActive(s: Staff, active: boolean) {
  const path = active ? '/api/ops/reactivate_staff' : '/api/ops/deactivate_staff'
  const res = await api.POST(path, { body: { staff_id: s.id } })
  done(res.error, res.data, active ? '已恢复' : '已停用')
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold">员工</h1>
      <UButton icon="i-lucide-plus" @click="openProfile(null)">新建员工</UButton>
    </div>

    <UTable :data="staff" :columns="columns">
      <template #level-cell="{ row }">{{ row.original.level?.name }}</template>
      <template #position-cell="{ row }">{{ row.original.position?.name }}</template>
      <template #roles-cell="{ row }">
        <div class="flex flex-wrap gap-1">
          <UBadge v-for="r in row.original.roles" :key="r.id" variant="subtle">{{ r.name }}</UBadge>
        </div>
      </template>
      <template #status-cell="{ row }">
        <UBadge :color="row.original.active ? 'success' : 'neutral'" variant="subtle">
          {{ row.original.active ? '在职' : '已停用' }}
        </UBadge>
      </template>
      <template #actions-cell="{ row }">
        <div class="flex gap-1 justify-end">
          <UButton size="xs" variant="ghost" @click="openProfile(row.original)">档案</UButton>
          <UButton size="xs" variant="ghost" @click="openRoles(row.original)">角色</UButton>
          <UButton size="xs" variant="ghost" @click="openLogin(row.original)">账号</UButton>
          <UButton v-if="row.original.active" size="xs" variant="ghost" color="error" @click="setActive(row.original, false)">停用</UButton>
          <UButton v-else size="xs" variant="ghost" @click="setActive(row.original, true)">恢复</UButton>
        </div>
      </template>
    </UTable>

    <UModal v-model:open="profileOpen" :title="editing ? '修改档案' : '新建员工'">
      <template #body>
        <div class="space-y-3">
          <UFormField label="姓名"><UInput v-model="profile.name" class="w-full" /></UFormField>
          <UFormField label="编号"><UInput v-model="profile.number" class="w-full" /></UFormField>
          <UFormField label="级别"><USelect v-model="profile.level" :items="optionItems(levels)" class="w-full" /></UFormField>
          <UFormField label="岗位"><USelect v-model="profile.position" :items="optionItems(positions)" class="w-full" /></UFormField>
          <UFormField v-if="!editing" label="角色">
            <USelectMenu v-model="profile.role_ids" :items="roleItems" value-key="value" multiple class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <UButton @click="saveProfile">保存</UButton>
      </template>
    </UModal>

    <UModal v-model:open="rolesOpen" :title="`设置角色：${roleTarget?.name ?? ''}`">
      <template #body>
        <p class="text-sm text-muted mb-3">一个员工可以有多个角色，权限取各角色的并集。</p>
        <USelectMenu v-model="roleSelection" :items="roleItems" value-key="value" multiple class="w-full" />
      </template>
      <template #footer>
        <UButton @click="saveRoles">保存</UButton>
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
        <UButton @click="saveLogin">保存</UButton>
      </template>
    </UModal>
  </div>
</template>
