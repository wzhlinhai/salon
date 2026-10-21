<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'role.manage' })

type Role = components['schemas']['Role']
type Def = components['schemas']['Def']
const api = useApi()
const toast = useToast()
const { me } = useMe()

const roles = ref<Role[]>([])
const catalogue = ref<Def[]>([])

const loadGuard = useRequestGuard()

async function load() {
  const request = loadGuard.next()
  const [r, p] = await Promise.all([
    api.POST('/api/ops/list_roles', { body: {} }),
    api.POST('/api/ops/list_permissions', { body: {} })
  ])
  if (!request()) return
  roles.value = r.data?.roles ?? []
  catalogue.value = p.data?.permissions ?? []
}

onMounted(load)

// 自己没有的权限不能授予别人（ADR-0009），在页面上直接禁用；后端同样会拒绝。
function mine(name: string) {
  return !!me.value && (me.value.is_owner || me.value.permissions.some(p => p.permission === name))
}

const label = (name: string) => catalogue.value.find(d => d.name === name)?.label ?? name

interface Row { on: boolean, scope: 'self' | 'store', days: string }
const editorGuard = useRequestGuard()
const open = ref(false)
const editing = ref<Role | null>(null)
const name = ref('')
const rows = reactive<Record<string, Row>>({})

function openEditor(role: Role | null) {
  editorGuard.next()
  editing.value = role
  name.value = role?.name ?? ''
  for (const d of catalogue.value) {
    const g = role?.permissions.find(p => p.permission === d.name)
    rows[d.name] = {
      on: !!g,
      scope: (g?.scope as 'self' | 'store') ?? 'store',
      days: g?.window_days == null ? '' : String(g.window_days)
    }
  }
  open.value = true
}

async function save() {
  const current = editorGuard.hold()
  const permissions = catalogue.value
    .filter(d => rows[d.name]?.on)
    .map((d) => {
      const r = rows[d.name]!
      return {
        permission: d.name,
        scope: d.scoped ? r.scope : undefined,
        window_days: d.windowed && r.days !== '' ? Number(r.days) : undefined
      }
    })
  const res = editing.value
    ? await api.POST('/api/ops/update_role', { body: { role_id: editing.value.id, name: name.value, permissions } })
    : await api.POST('/api/ops/create_role', { body: { name: name.value, permissions } })
  if (!current()) {
    if (res.data) load()
    else toast.add({ title: errorText(res.error), color: 'error' })
    return
  }
  if (res.data) {
    toast.add({ title: '已保存', color: 'success' })
    open.value = false
    load()
  } else {
    toast.add({ title: errorText(res.error), color: 'error' })
  }
}

async function remove(role: Role) {
  if (!confirm(`删除角色「${role.name}」？拥有该角色的员工会失去它。`)) return
  const res = await api.POST('/api/ops/delete_role', { body: { role_id: role.id } })
  if (!res.data) toast.add({ title: errorText(res.error), color: 'error' })
  load()
}

function summary(g: components['schemas']['GrantSpec']) {
  const parts = [label(g.permission)]
  if (g.scope === 'self') parts.push('本人')
  if (g.window_days === 0) parts.push('当天')
  else if (g.window_days != null) parts.push(`${g.window_days} 天内`)
  return parts.join(' · ')
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold">角色与权限</h1>
      <UButton icon="i-lucide-plus" @click="openEditor(null)">新建角色</UButton>
    </div>

    <UCard v-for="role in roles" :key="role.id">
      <template #header>
        <div class="flex items-center justify-between">
          <div class="font-medium">
            {{ role.name }}
            <UBadge v-if="role.is_owner" class="ml-2" variant="subtle">内置 · 全部权限</UBadge>
          </div>
          <div v-if="!role.is_owner" class="flex gap-1">
            <UButton size="xs" variant="ghost" @click="openEditor(role)">编辑</UButton>
            <UButton size="xs" variant="ghost" color="error" @click="remove(role)">删除</UButton>
          </div>
        </div>
      </template>
      <div v-if="role.is_owner" class="text-sm text-muted">老板拥有全部权限，包括以后新增的；不能修改。</div>
      <div v-else class="flex flex-wrap gap-1">
        <UBadge v-for="g in role.permissions" :key="g.permission" color="neutral" variant="outline">{{ summary(g) }}</UBadge>
        <span v-if="!role.permissions.length" class="text-sm text-muted">没有权限</span>
      </div>
    </UCard>

    <UModal v-model:open="open" :title="editing ? '编辑角色' : '新建角色'" class="max-w-2xl">
      <template #body>
        <UFormField label="角色名称" class="mb-4"><UInput v-model="name" class="w-full" /></UFormField>
        <div class="divide-y divide-default">
          <div v-for="d in catalogue" :key="d.name" class="flex items-center gap-3 py-2">
            <UCheckbox v-model="rows[d.name]!.on" :label="d.label" :disabled="!mine(d.name)" class="flex-1" />
            <USelect
              v-if="d.scoped && rows[d.name]!.on"
              v-model="rows[d.name]!.scope"
              :items="[{ label: '全店', value: 'store' }, { label: '本人', value: 'self' }]"
              class="w-24"
            />
            <UInput
              v-if="d.windowed && rows[d.name]!.on"
              v-model="rows[d.name]!.days"
              type="number"
              min="0"
              placeholder="不限天数"
              class="w-28"
            />
          </div>
        </div>
        <p class="text-xs text-muted mt-3">天数填 0 表示只能处理当天的单，不填表示不限。灰色的权限你自己没有，不能授予。</p>
      </template>
      <template #footer>
        <UButton @click="save">保存</UButton>
      </template>
    </UModal>
  </div>
</template>
