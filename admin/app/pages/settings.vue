<script setup lang="ts">
import type { components } from '~/utils/api'

// 所有员工都能查看设置和系统默认做法；修改需要门店设置权限，以后端检查为准。
type Setting = components['schemas']['Setting']
interface TimeRange { open: string, close: string }

const api = useApi()
const toast = useToast()
const { can } = useMe()

const today = ref('')
const settings = ref<Setting[]>([])
const changes = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_setting_changes', { body: { before_id: beforeId } })
  return { data: data?.changes, error }
})
const settingsGuard = useRequestGuard()

async function load() {
  const request = settingsGuard.next()
  if (can('audit_log.view')) changes.reload()
  const { data } = await api.POST('/api/ops/get_settings', { body: {} })
  if (!request()) return
  today.value = data?.business_date ?? ''
  settings.value = data?.settings ?? []
}

onMounted(load)

function show(kind: Setting['kind'] | undefined, value: unknown): string {
  if (kind === 'time_range') {
    const r = value as TimeRange
    return `${r.open}–${r.close}`
  }
  if (kind === 'choice') {
    return settings.value.flatMap(s => s.options ?? []).find(o => o.value === value)?.label ?? String(value)
  }
  return String(value)
}

const kindOf = (key: string) => settings.value.find(s => s.key === key)?.kind

// 编辑中的设置。值按形式分开存，保存时再组装。
const editorGuard = useRequestGuard()
const editing = ref<Setting | null>(null)
const form = reactive({ choice: '', integer: 0, time: '', open: '', close: '', from: '' })

function openEditor(s: Setting) {
  editorGuard.next()
  editing.value = s
  if (s.kind === 'time_range') {
    const r = s.value as TimeRange
    form.open = r.open
    form.close = r.close
  } else if (s.kind === 'choice') {
    form.choice = s.value as string
  } else if (s.kind === 'integer') {
    form.integer = Number(s.value)
  } else {
    form.time = s.value as string
  }
  form.from = today.value
}

async function save(reset: boolean) {
  const current = editorGuard.hold()
  const s = editing.value!
  const value = reset ? null : s.kind === 'time_range' ? { open: form.open, close: form.close } : s.kind === 'choice' ? form.choice : s.kind === 'integer' ? Number(form.integer) : form.time
  const { data, error } = await api.POST('/api/ops/change_setting', { body: { key: s.key, value, effective_from: form.from } })
  if (!current()) {
    if (data) load()
    else toast.add({ title: errorText(error), color: 'error' })
    return
  }
  if (data) {
    toast.add({ title: `已保存，${data.effective_from} 起执行`, color: 'success' })
    editing.value = null
    load()
  } else {
    toast.add({ title: errorText(error), color: 'error' })
  }
}

// 顾客来源
const sources = ref<components['schemas']['CustomerSource'][]>([])
const sourceName = ref('')
const sourcesGuard = useRequestGuard()

async function loadSources() {
  const request = sourcesGuard.next()
  const { data, error } = await api.POST('/api/ops/list_customer_sources', { body: {} })
  if (!request()) return
  if (data) {
    sources.value = data.items
  } else {
    toast.add({ title: errorText(error), color: 'error' })
  }
}

async function addSource() {
  const { data, error } = await api.POST('/api/ops/create_customer_source', {
    body: { name: sourceName.value }
  })
  if (data) {
    sourceName.value = ''
  } else {
    toast.add({ title: errorText(error), color: 'error' })
  }
  loadSources()
}

async function saveSource(source: components['schemas']['CustomerSource']) {
  const { data, error } = await api.POST('/api/ops/update_customer_source', { body: source })
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
  }
  loadSources()
}

onMounted(loadSources)
</script>

<template>
  <div class="space-y-4">
    <div>
      <h1 class="text-xl font-semibold">门店设置</h1>
      <p class="text-sm text-muted">修改从选定的日期开始执行，之前的业务和已经下的单不受影响。今天是营业日 {{ today }}。</p>
    </div>

    <UCard v-for="s in settings" :key="s.key" :data-setting="s.key">
      <div class="flex items-start justify-between gap-4">
        <div class="space-y-1">
          <div class="font-medium">{{ s.label }}</div>
          <div class="text-sm text-muted">{{ s.hint }}</div>
          <div class="flex items-center gap-2">
            <span class="text-lg">{{ show(s.kind, s.value) }}</span>
            <UBadge v-if="s.is_default" color="neutral" variant="subtle">系统默认</UBadge>
            <span v-else class="text-xs text-muted">{{ s.effective_from }} 起</span>
          </div>
          <div v-for="u in s.upcoming" :key="u.effective_from" class="text-sm">
            <UBadge color="warning" variant="subtle">已安排</UBadge>
            {{ u.effective_from }} 起改为 {{ show(s.kind, u.value) }}{{ u.is_default ? '（系统默认）' : '' }}
          </div>
        </div>
        <UButton v-if="can('settings.manage')" variant="outline" @click="openEditor(s)">修改</UButton>
      </div>
    </UCard>

    <UCard data-setting="customer_sources">
      <template #header>
        <div class="font-medium">顾客来源</div>
      </template>
      <div class="space-y-2">
        <div v-for="source in sources" :key="source.id" class="flex items-center gap-2">
          <template v-if="can('settings.manage')">
            <UInput v-model="source.name" :aria-label="`顾客来源 ${source.name}`" class="flex-1" @change="saveSource(source)" />
            <USwitch v-model="source.active" :label="source.active ? '启用' : '停用'" @update:model-value="saveSource(source)" />
          </template>
          <template v-else>
            <span>{{ source.name }}</span>
            <UBadge :color="source.active ? 'success' : 'neutral'" variant="subtle">{{ source.active ? '启用' : '停用' }}</UBadge>
          </template>
        </div>
        <form v-if="can('settings.manage')" class="flex gap-2 pt-2" @submit.prevent="addSource">
          <UInput v-model="sourceName" aria-label="新顾客来源" placeholder="新顾客来源" class="flex-1" />
          <UButton type="submit" icon="i-lucide-plus">添加</UButton>
        </form>
      </div>
    </UCard>

    <UCard v-if="can('audit_log.view')">
      <template #header>
        <div class="font-medium">修改记录</div>
      </template>
      <div v-if="!changes.items.value.length" class="text-sm text-muted">还没有修改过设置</div>
      <table v-else class="w-full text-sm">
        <thead class="text-left text-muted">
          <tr><th class="py-1">时间</th><th>修改人</th><th>设置</th><th>改前</th><th>改后</th><th>生效日期</th></tr>
        </thead>
        <tbody>
          <tr v-for="c in changes.items.value" :key="c.id" class="border-t border-default">
            <td class="py-1">{{ new Date(c.changed_at).toLocaleString('zh-CN') }}</td>
            <td>{{ c.changed_by.name }}</td>
            <td>{{ c.label }}</td>
            <td>{{ show(kindOf(c.key), c.old_value) }}{{ c.old_is_default ? '（默认）' : '' }}</td>
            <td>{{ show(kindOf(c.key), c.value) }}{{ c.is_default ? '（恢复默认）' : '' }}</td>
            <td>{{ c.effective_from }}</td>
          </tr>
        </tbody>
      </table>
      <UButton v-if="changes.hasMore.value" :loading="changes.loading.value" variant="outline" @click="changes.loadMore">加载更多</UButton>
    </UCard>

    <UModal :open="!!editing" :title="`修改${editing?.label ?? ''}`" @update:open="v => { if (!v) editing = null }">
      <template #body>
        <div v-if="editing" class="space-y-4">
          <div v-if="editing.kind === 'time_range'" class="flex gap-2">
            <UFormField label="开门"><UInput v-model="form.open" type="time" /></UFormField>
            <UFormField label="关门"><UInput v-model="form.close" type="time" /></UFormField>
          </div>
          <UFormField v-else-if="editing.kind === 'choice'" label="做法"><USelect v-model="form.choice" aria-label="做法" :items="editing.options" value-key="value" label-key="label" /></UFormField>
          <UFormField v-else-if="editing.kind === 'integer'" label="每积一分的本金元数"><UInput v-model="form.integer" type="number" :min="editing.min" :max="editing.max" :step="1" /></UFormField>
          <UFormField v-else label="时间"><UInput v-model="form.time" type="time" /></UFormField>
          <UFormField label="生效日期" help="不能早于今天"><UInput v-model="form.from" type="date" :min="today" /></UFormField>
          <p class="text-xs text-muted">系统默认：{{ show(editing.kind, editing.default) }}</p>
        </div>
      </template>
      <template #footer>
        <div class="flex gap-2">
          <UButton @click="save(false)">保存</UButton>
          <UButton variant="ghost" @click="save(true)">恢复系统默认</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>
