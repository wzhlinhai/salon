<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'role.manage', title: '角色与权限', description: '一个员工可以有几个角色，能做的事是这几个角色的合并。', art: 'key' })

type Role = components['schemas']['Role']
type Def = components['schemas']['Def']
const api = useApi()
const toast = useToast()
const { me } = useMe()

const roles = ref<Role[]>([])
const loadError = ref('')
const catalogue = ref<Def[]>([])

const loadGuard = useRequestGuard()

async function load() {
  const request = loadGuard.next()
  const [r, p] = await Promise.all([
    api.POST('/api/ops/list_roles', { body: {} }),
    api.POST('/api/ops/list_permissions', { body: {} })
  ])
  if (!request()) return
  // 一次都没读到过时原地给「重试」，别只弹提示留一片空白；已经有角色（刷新失败）时弹提示就够。
  if (!r.data || !p.data) {
    if (roles.value.length) toast.add({ title: errorText(r.error ?? p.error), color: 'error' })
    else loadError.value = errorText(r.error ?? p.error)
    return
  }
  loadError.value = ''
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
const editor = useModalForm()
const { open, saving } = editor
const editing = ref<Role | null>(null)
const name = ref('')
const rows = reactive<Record<string, Row>>({})

function openEditor(role: Role | null) {
  editor.begin(() => {
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
  })
}

async function save() {
  if (catalogue.value.some(d => d.windowed && rows[d.name]?.on && rows[d.name]!.days !== '' && !/^\d+$/.test(rows[d.name]!.days))) {
    toast.add({ title: '天数须为非负整数或留空', color: 'error' })
    return
  }
  await editor.save(async (current) => {
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
    const verb = editing.value ? '已改好' : '已新建'
    const saved = name.value
    const res = editing.value
      ? await api.POST('/api/ops/update_role', { body: { role_id: editing.value.id, name: saved, permissions } })
      : await api.POST('/api/ops/create_role', { body: { name: saved, permissions } })
    if (!current()) {
      if (res.data) load()
      else toast.add({ title: errorText(res.error), color: 'error' })
      return
    }
    if (res.data) {
      toast.add({ title: `${verb}角色「${saved}」`, color: 'success' })
      open.value = false
      load()
    } else {
      toast.add({ title: errorText(res.error), color: 'error' })
    }
  })
}

const removeGuard = useRequestGuard()
const removing = reactive<Record<string, boolean>>({})

// 删角色先确认（样稿 21 危险确认）：标题问一句，正文写删了以后会怎样，底栏「不删了」在左、红色「删除角色」在右。
const removeTarget = ref<Role | null>(null)
const removeOpen = ref(false)

function askRemove(role: Role) {
  removeTarget.value = role
  removeOpen.value = true
}

async function confirmRemove() {
  const role = removeTarget.value
  if (!role) return
  if (await remove(role)) removeOpen.value = false
}

// 删成了返回 true；没删成（出错、换了人）弹窗留着，可以直接再点一次。
async function remove(role: Role) {
  if (removing[role.id]) return false
  const current = removeGuard.hold()
  removing[role.id] = true
  try {
    const res = await api.POST('/api/ops/delete_role', { body: { role_id: role.id } })
    if (!current()) return false
    if (!res.data) toast.add({ title: errorText(res.error), color: 'error' })
    load()
    return !!res.data
  } finally {
    if (current()) removing[role.id] = false
  }
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
    <PageActions>
      <UButton size="sm" @click="openEditor(null)">新建角色</UButton>
    </PageActions>

    <LoadError v-if="loadError && !roles.length" title="角色没有读出来" :hint="loadError" @retry="load()" />
    <section v-for="role in roles" :key="role.id" :data-role="role.name" class="box group/row">
      <div class="flex items-center gap-2">
        <span class="font-medium">{{ role.name }}</span>
        <span v-if="role.is_owner" class="tag bg-tag-gray">内置 · 全部权限</span>
        <span v-else class="text-xs text-dimmed">{{ role.permissions.length }} 项权限</span>
        <div v-if="!role.is_owner" class="ms-auto flex gap-0.5 opacity-0 group-hover/row:opacity-100 focus-within:opacity-100 any-pointer-coarse:opacity-100">
          <UButton size="sm" color="neutral" variant="ghost" @click="openEditor(role)">编辑</UButton>
          <UButton size="sm" variant="ghost" color="error" :loading="removing[role.id]" @click="askRemove(role)">删除</UButton>
        </div>
      </div>
      <p v-if="role.is_owner" class="mt-2 text-sm text-muted">老板拥有全部权限，包括以后新增的；不能修改。</p>
      <div v-else class="mt-2 flex flex-wrap gap-1">
        <span v-for="g in role.permissions" :key="g.permission" class="tag bg-tag-gray">{{ summary(g) }}</span>
        <span v-if="!role.permissions.length" class="text-sm text-muted">没有权限</span>
      </div>
    </section>

    <UModal v-model:open="open" :title="editing ? '编辑角色' : '新建角色'" :ui="{ content: 'sm:max-w-[600px]' }">
      <template #body>
        <UFormField label="角色名称" class="mb-4"><UInput v-model="name" class="w-full" /></UFormField>
        <div class="divide-y divide-default">
          <div v-for="d in catalogue" :key="d.name" class="flex items-center gap-3 py-2">
            <UCheckbox v-model="rows[d.name]!.on" :label="d.label" :disabled="!mine(d.name)" class="flex-1" />
            <USelect
              v-if="d.scoped && rows[d.name]!.on"
              v-model="rows[d.name]!.scope"
              :items="[{ label: '全店', value: 'store' }, { label: '本人', value: 'self' }]"
              :aria-label="`${d.label}的范围`"
              class="w-24"
            />
            <UInput
              v-if="d.windowed && rows[d.name]!.on"
              v-model="rows[d.name]!.days"
              type="text"
              inputmode="numeric"
              placeholder="不限天数"
              :aria-label="`${d.label}的天数`"
              class="w-28"
            />
          </div>
        </div>
        <p class="text-xs text-muted mt-3">天数填 0 表示只能处理当天的单，不填表示不限。灰色的权限你自己没有，不能授予。</p>
      </template>
      <template #footer>
        <ModalActions @cancel="open = false">
          <UButton :loading="saving" @click="save">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-model:open="removeOpen" :title="`删除角色「${removeTarget?.name ?? ''}」？`">
      <template #body>
        <p class="text-sm">删了以后：</p>
        <ul class="mt-2 list-disc space-y-1 ps-5 text-sm text-muted">
          <li>有这个角色的员工失去它带的 {{ removeTarget?.permissions.length ?? 0 }} 项权限，别的角色不受影响</li>
          <li>员工本人、账号和以前的记录都还在</li>
        </ul>
      </template>
      <template #footer>
        <ModalActions cancel-label="不删了" @cancel="removeOpen = false">
          <template #leading>
            <span class="text-meta text-muted">删了不能恢复</span>
          </template>
          <UButton color="error" :loading="removeTarget ? removing[removeTarget.id] : false" @click="confirmRemove">删除角色</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
