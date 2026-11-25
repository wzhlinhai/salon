<script setup lang="ts">
import type { components } from '~/utils/api'

// 员工的几个弹窗：新建或改档案、设置角色、登录账号，以及停用、恢复。员工名册和员工详情共用；
// 使用方经 ref 调 openProfile / openRoles / openLogin / setActive，保存成功后发 changed，由使用方重新取数。
type Staff = components['schemas']['Staff']
const emit = defineEmits<{ changed: [] }>()
const api = useApi()
const toast = useToast()
const NONE = 'none'

const roles = ref<components['schemas']['RoleOption'][]>([])
const levels = ref<components['schemas']['Level'][]>([])
const positions = ref<components['schemas']['Position'][]>([])

// 选项取失败时，下次打开弹窗再取一次，免得下拉一直是空的。
let optionsLoaded = false

async function loadOptions() {
  const [r, l, p] = await Promise.all([
    api.POST('/api/ops/list_role_options', { body: {} }),
    api.POST('/api/ops/list_levels', { body: {} }),
    api.POST('/api/ops/list_positions', { body: {} })
  ])
  if (!r.data || !l.data || !p.data) {
    toast.add({ title: errorText(r.error ?? l.error ?? p.error), color: 'error' })
    return
  }
  roles.value = r.data.roles
  levels.value = l.data.items
  positions.value = p.data.items
  optionsLoaded = true
}

onMounted(loadOptions)

const roleItems = computed(() => roles.value.map(r => ({ label: r.name, value: r.id })))
const optionItems = (list: (components['schemas']['Level'] | components['schemas']['Position'])[]) => [
  { label: '不设', value: NONE },
  ...list.filter(o => o.active).map(o => ({ label: o.name, value: o.id }))
]

function done(error: unknown, ok: unknown, message: string, current: () => boolean = () => true) {
  if (ok) {
    if (current()) toast.add({ title: message, color: 'success' })
    emit('changed')
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
  if (!optionsLoaded) loadOptions()
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
  if (!optionsLoaded) loadOptions()
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

// 停用、恢复
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

defineExpose({ openProfile, openRoles, openLogin, setActive, activeSaving })
</script>

<template>
  <UModal v-model:open="profileOpen" :title="editing ? '修改档案' : '新建员工'">
    <template #body>
      <div class="space-y-5">
        <div class="grid grid-cols-2 gap-4">
          <UFormField label="姓名"><UInput v-model="profile.name" class="w-full" /></UFormField>
          <UFormField label="编号"><UInput v-model="profile.number" class="w-full" /></UFormField>
        </div>
        <div class="grid grid-cols-2 gap-4">
          <UFormField label="级别"><USelect v-model="profile.level" :items="optionItems(levels)" class="w-full" /></UFormField>
          <UFormField label="岗位"><USelect v-model="profile.position" :items="optionItems(positions)" class="w-full" /></UFormField>
        </div>
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

  <UModal v-model:open="rolesOpen" :title="`设置角色：${roleTarget?.name ?? ''}`" description="一个员工可以有多个角色，权限取各角色的并集。">
    <template #body>
      <UFormField label="角色">
        <USelectMenu v-model="roleSelection" :items="roleItems" value-key="value" multiple class="w-full" />
      </UFormField>
    </template>
    <template #footer>
      <ModalActions @cancel="rolesOpen = false">
        <UButton :loading="rolesSaving" @click="saveRoles">保存</UButton>
      </ModalActions>
    </template>
  </UModal>

  <UModal v-model:open="loginOpen" :title="`登录账号：${loginTarget?.name ?? ''}`">
    <template #body>
      <div class="space-y-5">
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
</template>
