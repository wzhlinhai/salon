<script setup lang="ts">
import type { components } from '~/utils/api'
import { RED_LINES, groupPermissions, limitText, roleSentence } from '~/utils/roles'

definePageMeta({ permission: 'role.manage', title: '角色与权限', description: '一个员工可以有几个角色，能做的事是这几个角色的合并。', art: 'key' })

type Role = components['schemas']['Role']
type Def = components['schemas']['Def']
type Grant = components['schemas']['GrantSpec']
const api = useApi()
const toast = useToast()
const { me } = useMe()

const roles = ref<Role[]>([])
const loadError = ref('')
const catalogue = ref<Def[]>([])
// 每个角色有几位在职员工；没有「员工管理」权限读不到员工，就不显示人数。
const staffCount = ref<Record<string, number> | null>(null)

const loadGuard = useRequestGuard()

async function load() {
  const request = loadGuard.next()
  const [r, p, s] = await Promise.all([
    api.POST('/api/ops/list_roles', { body: {} }),
    api.POST('/api/ops/list_permissions', { body: {} }),
    mine('staff.manage') ? api.POST('/api/ops/list_staff', { body: {} }) : Promise.resolve(null)
  ])
  if (!request()) return
  // 一次都没读到过时原地给「重试」，别只弹提示留一片空白；已经有角色（刷新失败）时弹提示就够。
  if (!r.data || !p.data) {
    if (roles.value.length) toast.add({ title: errorText(r.error ?? p.error), color: 'error' })
    else loadError.value = errorText(r.error ?? p.error)
    return
  }
  loadError.value = ''
  roles.value = r.data.roles
  catalogue.value = p.data.permissions
  if (s?.data) {
    const count: Record<string, number> = {}
    for (const st of s.data.staff) {
      if (st.deactivated_at) continue
      for (const role of st.roles) count[role.id] = (count[role.id] ?? 0) + 1
    }
    staffCount.value = count
  }
}

onMounted(load)

// 自己没有的权限不能授予别人（ADR-0009），在页面上直接禁用；后端同样会拒绝。
function mine(name: string) {
  return !!me.value && (me.value.is_owner || me.value.permissions.some(p => p.permission === name))
}

const groups = computed(() => groupPermissions(catalogue.value))
const owner = computed(() => roles.value.find(r => r.is_owner))
// 老板全开、不能改，不占一列，收成表上方一行；表里只比能改的角色。
const columns = computed(() => roles.value.filter(r => !r.is_owner))
const grantOf = (role: Role, name: string) => role.permissions.find(p => p.permission === name)

// 视图：全部权限 / 只看有差别的；点红线时只看那条红线的几项。
type View = 'all' | 'diff' | 'line'
const view = ref<View>('all')
const redLine = ref('')

function differs(name: string) {
  const cells = columns.value.map((r) => {
    const g = grantOf(r, name)
    return g ? limitText(g) || '●' : ''
  })
  return new Set(cells).size > 1
}

const diffCount = computed(() => catalogue.value.filter(d => differs(d.name)).length)
const shownGroups = computed(() => {
  const line = RED_LINES.find(l => l.key === redLine.value)
  const keep = (d: Def) => view.value === 'line' && line ? line.permissions.includes(d.name) : view.value === 'diff' ? differs(d.name) : true
  return groups.value.map(g => ({ ...g, defs: g.defs.filter(keep) })).filter(g => g.defs.length)
})
watch(view, (v) => {
  if (v !== 'line') redLine.value = ''
})
const viewItems = computed(() => {
  const items: { key: View, label: string, count: number }[] = [{ key: 'all', label: '全部权限', count: catalogue.value.length }, { key: 'diff', label: '只看有差别的', count: diffCount.value }]
  const line = RED_LINES.find(l => l.key === redLine.value)
  if (line) items.push({ key: 'line', label: `只看${line.label}`, count: line.permissions.length })
  return items
})
const redLines = computed(() => RED_LINES.map(l => ({
  ...l,
  holders: columns.value.filter(r => l.permissions.some(p => grantOf(r, p))).map(r => r.name)
})))

function toggleRedLine(key: string) {
  if (redLine.value === key) {
    view.value = 'all'
    return
  }
  redLine.value = key
  view.value = 'line'
}

interface Row { on: boolean, scope: 'self' | 'store', days: string }
const editor = useModalForm()
const { open, saving } = editor
const editing = ref<Role | null>(null)
const name = ref('')
const rows = reactive<Record<string, Row>>({})
const focused = ref('')

// focus：从格子点进来时，打开后滚到那一项并闪一下底色。
function openEditor(role: Role | null, focus = '') {
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
    focused.value = focus
  })
  if (focus) {
    // 侧滑出来有动画，等它停下再滚，不然滚动位置会被动画盖掉。
    setTimeout(() => document.querySelector(`[data-perm="${focus}"]`)?.scrollIntoView({ block: 'center', behavior: 'smooth' }), 250)
    setTimeout(() => {
      if (focused.value === focus) focused.value = ''
    }, 1800)
  }
}

const draftGrants = computed<Grant[]>(() => catalogue.value
  .filter(d => rows[d.name]?.on)
  .map((d) => {
    const r = rows[d.name]!
    return {
      permission: d.name,
      scope: d.scoped ? r.scope : undefined,
      window_days: d.windowed && /^\d+$/.test(r.days) ? Number(r.days) : undefined
    }
  }))
const onCount = (defs: Def[]) => defs.filter(d => rows[d.name]?.on).length

// 整组开关只动自己有的权限；自己没有的不能授予，保持原样。
function toggleGroup(defs: Def[]) {
  const usable = defs.filter(d => mine(d.name))
  const allOn = usable.every(d => rows[d.name]!.on)
  for (const d of usable) rows[d.name]!.on = !allOn
}

const scopeItems = [{ label: '全店', value: 'store' }, { label: '本人', value: 'self' }] as const

async function save() {
  if (catalogue.value.some(d => d.windowed && rows[d.name]?.on && rows[d.name]!.days !== '' && !/^\d+$/.test(rows[d.name]!.days))) {
    toast.add({ title: '天数须为非负整数或留空', color: 'error' })
    return
  }
  await editor.save(async (current) => {
    const permissions = draftGrants.value
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

// 删角色先确认（样稿 21 危险确认）：入口在编辑侧滑底部左边，平时页面上不放删除。
const removeTarget = ref<Role | null>(null)
const removeOpen = ref(false)

function askRemove(role: Role) {
  removeTarget.value = role
  removeOpen.value = true
}

async function confirmRemove() {
  const role = removeTarget.value
  if (!role) return
  if (await remove(role)) {
    removeOpen.value = false
    if (editing.value?.id === role.id) open.value = false
  }
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
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton @click="openEditor(null)">新建角色</UButton>
    </PageActions>

    <LoadError v-if="loadError && !roles.length" title="角色没有读出来" :hint="loadError" @retry="load()" />
    <template v-else-if="catalogue.length">
      <!-- 红线：老板最要盯的三类事，写出除老板以外谁能做；点一条，下面的表只留那几项。 -->
      <div class="grid border-y border-default sm:grid-cols-3 sm:divide-x sm:divide-default" data-testid="red-lines">
        <button
          v-for="l in redLines"
          :key="l.key"
          type="button"
          class="group/line px-4 py-3 text-start first:ps-0 max-sm:border-b max-sm:border-default max-sm:px-0 max-sm:last:border-b-0"
          :aria-pressed="redLine === l.key"
          :data-line="l.key"
          @click="toggleRedLine(l.key)"
        >
          <span class="flex items-center gap-2 text-meta text-muted"><span class="size-1.5 rounded-full bg-ink-red" aria-hidden="true" />{{ l.label }} · {{ l.hint }}</span>
          <span class="mt-1 block font-serif text-figure font-semibold underline-offset-4" :class="[l.holders.length ? '' : 'text-muted', redLine === l.key ? 'underline decoration-2' : 'group-hover/line:underline group-hover/line:decoration-dimmed']">{{ l.holders.length ? l.holders.join('、') : '只有老板' }}</span>
        </button>
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <ViewTabs v-model="view" label="看哪些权限" :items="viewItems" />
        <p v-if="owner" class="ms-auto flex items-center gap-2 text-sm text-muted">
          <PersonAvatar :name="owner.name" size="sm" />{{ owner.name }}<span class="tag bg-tag-gray">内置 · 全部权限</span>
        </p>
      </div>

      <div class="overflow-x-auto">
        <table class="w-full min-w-[560px] border-collapse text-sm" data-testid="role-matrix">
          <thead>
            <tr class="border-b border-default">
              <th class="sticky left-0 z-10 w-[36%] min-w-40 bg-page py-2 pe-3 text-start align-bottom text-meta font-normal text-muted">权限</th>
              <th v-for="r in columns" :key="r.id" class="px-1 pb-2 align-bottom font-normal">
                <button
                  type="button"
                  class="group/h flex w-full flex-col items-center gap-1 rounded-md px-2 py-2 hover:bg-hover"
                  :data-role="r.name"
                  :aria-label="`编辑角色${r.name}`"
                  @click="openEditor(r)"
                >
                  <PersonAvatar :name="r.name" size="sm" />
                  <span class="font-medium text-default">{{ r.name }}</span>
                  <span class="text-meta text-muted">
                    <span class="group-hover/h:hidden group-focus-visible/h:hidden">{{ staffCount ? `${staffCount[r.id] ?? 0} 人 · ` : '' }}{{ r.permissions.length }} 项</span>
                    <span class="hidden text-default underline underline-offset-2 group-hover/h:inline group-focus-visible/h:inline">编辑</span>
                  </span>
                </button>
              </th>
            </tr>
          </thead>
          <tbody v-for="g in shownGroups" :key="g.key">
            <tr>
              <td class="sticky left-0 bg-page pt-6 pb-2 pe-3">
                <span class="font-semibold">{{ g.label }}</span>
                <span v-if="g.hint" class="ms-2 text-meta text-muted max-sm:hidden">{{ g.hint }}</span>
              </td>
              <td v-for="r in columns" :key="r.id" class="pt-6 pb-2 text-center text-meta tabular-nums text-muted">
                {{ g.defs.filter(d => grantOf(r, d.name)).length }}/{{ g.defs.length }}
              </td>
            </tr>
            <tr v-for="d in g.defs" :key="d.name" class="row-hover border-t border-default" :data-perm-row="d.name">
              <td class="sticky left-0 bg-page py-2 pe-3">{{ d.label }}</td>
              <td v-for="r in columns" :key="r.id" class="p-0 text-center">
                <button
                  type="button"
                  class="grid h-9 w-full place-items-center hover:bg-hover"
                  :aria-label="`${r.name}：${d.label}${grantOf(r, d.name) ? (limitText(grantOf(r, d.name)!) ? `，限${limitText(grantOf(r, d.name)!)}` : '，能做') : '，不能做'}`"
                  @click="openEditor(r, d.name)"
                >
                  <template v-if="grantOf(r, d.name)">
                    <span v-if="limitText(grantOf(r, d.name)!)" class="tag bg-tag-yellow">{{ limitText(grantOf(r, d.name)!) }}</span>
                    <span v-else class="size-2 rounded-full bg-ink" aria-hidden="true" />
                  </template>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="flex flex-wrap items-center gap-x-4 gap-y-1 text-meta text-muted">
        <span class="flex items-center gap-2"><span class="size-2 rounded-full bg-ink" aria-hidden="true" />能做</span>
        <span class="flex items-center gap-2"><span class="tag bg-tag-yellow">本人·当天</span>能做，但只限本人的、当天的</span>
        <span>空着是不能做。点格子或角色名修改。</span>
      </p>
    </template>

    <USlideover
      v-model:open="open"
      :title="editing ? `编辑「${editing.name}」` : '新建角色'"
      description="打开的开关就是这个角色能做的事"
      :ui="{ content: 'sm:inset-y-4 sm:right-4 sm:h-auto sm:max-w-[540px] sm:rounded-card shadow-lift ring-0', body: 'space-y-7' }"
    >
      <template #body>
        <div>
          <input v-model="name" placeholder="角色名称" aria-label="角色名称" class="w-full bg-transparent font-serif text-title font-semibold outline-none placeholder:text-dimmed">
          <p class="mt-2 text-sm text-toned" data-testid="role-sentence">{{ roleSentence(draftGrants, groups) }}</p>
        </div>
        <section v-for="g in groups" :key="g.key">
          <div class="flex items-baseline gap-2 border-b border-default pb-2">
            <span class="font-semibold">{{ g.label }}</span>
            <span class="text-meta text-muted max-sm:hidden">{{ g.hint }}</span>
            <span class="ms-auto text-meta tabular-nums text-muted">{{ onCount(g.defs) }}/{{ g.defs.length }}</span>
            <button type="button" class="text-meta text-muted underline-offset-2 hover:text-default hover:underline" @click="toggleGroup(g.defs)">
              {{ g.defs.filter(d => mine(d.name)).every(d => rows[d.name]?.on) ? '全不选' : '全选' }}
            </button>
          </div>
          <ul class="divide-y divide-default">
            <li
              v-for="d in g.defs"
              :key="d.name"
              class="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1 py-1 transition-colors duration-700"
              :class="focused === d.name ? 'bg-tag-yellow/50 -mx-2 px-2 rounded-md' : ''"
              :data-perm="d.name"
            >
              <label :for="`perm-${d.name}`" class="flex-1 text-sm" :class="rows[d.name]?.on ? '' : 'text-muted'">{{ d.label }}</label>
              <template v-if="rows[d.name]?.on">
                <div v-if="d.scoped" class="flex rounded-md bg-lane p-0.5 text-meta" role="group" :aria-label="`${d.label}的范围`">
                  <button
                    v-for="s in scopeItems"
                    :key="s.value"
                    type="button"
                    class="rounded-md px-2 py-0.5"
                    :class="rows[d.name]!.scope === s.value ? 'bg-white shadow-line' : 'text-muted'"
                    :aria-pressed="rows[d.name]!.scope === s.value"
                    :disabled="!mine(d.name)"
                    @click="rows[d.name]!.scope = s.value"
                  >
                    {{ s.label }}
                  </button>
                </div>
                <span v-if="d.windowed" class="flex items-center gap-1 text-meta text-muted">
                  <input
                    v-model="rows[d.name]!.days"
                    inputmode="numeric"
                    placeholder="不限天数"
                    :aria-label="`${d.label}的天数`"
                    :disabled="!mine(d.name)"
                    class="w-16 rounded-md bg-lane px-1 py-0.5 text-center text-default outline-none placeholder:text-dimmed focus-visible:ring-2 focus-visible:ring-primary"
                  >天内
                </span>
              </template>
              <USwitch :id="`perm-${d.name}`" v-model="rows[d.name]!.on" size="sm" :disabled="!mine(d.name)" :aria-label="d.label" />
            </li>
          </ul>
        </section>
        <p class="text-meta text-muted">天数填 0 表示只能处理当天的单，不填表示不限。灰掉的开关是你自己没有的权限，不能授予。</p>
      </template>
      <template #footer>
        <ModalActions @cancel="open = false">
          <template v-if="editing" #leading>
            <UButton color="error" variant="ghost" icon="i-lucide-trash-2" :loading="removing[editing.id]" @click="askRemove(editing)">删除这个角色</UButton>
          </template>
          <UButton :loading="saving" @click="save">保存</UButton>
        </ModalActions>
      </template>
    </USlideover>
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
