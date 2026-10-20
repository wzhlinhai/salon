<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage' })

type Setting = components['schemas']['Setting']
type SettingChange = components['schemas']['SettingChange']
interface TimeRange { open: string; close: string }

const api = useApi()
const toast = useToast()
const { can } = useMe()

const today = ref('')
const settings = ref<Setting[]>([])
const changes = ref<SettingChange[]>([])

async function load() {
  const { data } = await api.POST('/api/ops/get_settings', { body: {} })
  today.value = data?.business_date ?? ''
  settings.value = data?.settings ?? []
  if (can('audit_log.view')) {
    const log = await api.POST('/api/ops/list_setting_changes', { body: {} })
    changes.value = log.data?.changes ?? []
  }
}
onMounted(load)

function show(kind: Setting['kind'] | undefined, value: unknown): string {
  if (kind === 'time_range') {
    const r = value as TimeRange
    return `${r.open}–${r.close}`
  }
  return String(value)
}
const kindOf = (key: string) => settings.value.find(s => s.key === key)?.kind

// 编辑中的设置。值按形式分开存，保存时再组装。
const editing = ref<Setting | null>(null)
const form = reactive({ time: '', open: '', close: '', from: '' })

function openEditor(s: Setting) {
  editing.value = s
  if (s.kind === 'time_range') {
    const r = s.value as TimeRange
    form.open = r.open
    form.close = r.close
  } else {
    form.time = s.value as string
  }
  form.from = today.value
}

async function save(reset: boolean) {
  const s = editing.value!
  const value = reset ? null : s.kind === 'time_range' ? { open: form.open, close: form.close } : form.time
  const { data, error } = await api.POST('/api/ops/change_setting', { body: { key: s.key, value, effective_from: form.from } })
  if (data) {
    toast.add({ title: `已保存，${data.effective_from} 起执行`, color: 'success' })
    editing.value = null
    load()
  } else {
    toast.add({ title: errorText(error), color: 'error' })
  }
}
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
            <UBadge v-if="!s.configured" color="neutral" variant="subtle">系统默认</UBadge>
            <span v-else class="text-xs text-muted">{{ s.effective_from }} 起</span>
          </div>
          <div v-for="u in s.upcoming" :key="u.effective_from" class="text-sm">
            <UBadge color="warning" variant="subtle">已安排</UBadge>
            {{ u.effective_from }} 起改为 {{ show(s.kind, u.value) }}{{ u.configured ? '' : '（系统默认）' }}
          </div>
        </div>
        <UButton variant="outline" @click="openEditor(s)">修改</UButton>
      </div>
    </UCard>

    <UCard v-if="can('audit_log.view')">
      <template #header>
        <div class="font-medium">修改记录</div>
      </template>
      <div v-if="!changes.length" class="text-sm text-muted">还没有修改过设置</div>
      <table v-else class="w-full text-sm">
        <thead class="text-left text-muted">
          <tr><th class="py-1">时间</th><th>修改人</th><th>设置</th><th>改前</th><th>改后</th><th>生效日期</th></tr>
        </thead>
        <tbody>
          <tr v-for="c in changes" :key="c.id" class="border-t border-default">
            <td class="py-1">{{ new Date(c.changed_at).toLocaleString('zh-CN') }}</td>
            <td>{{ c.changed_by.name }}</td>
            <td>{{ c.label }}</td>
            <td>{{ show(kindOf(c.key), c.old_value) }}{{ c.old_is_default ? '（默认）' : '' }}</td>
            <td>{{ show(kindOf(c.key), c.value) }}{{ c.is_default ? '（恢复默认）' : '' }}</td>
            <td>{{ c.effective_from }}</td>
          </tr>
        </tbody>
      </table>
    </UCard>

    <UModal :open="!!editing" :title="`修改${editing?.label ?? ''}`" @update:open="v => { if (!v) editing = null }">
      <template #body>
        <div v-if="editing" class="space-y-4">
          <div v-if="editing.kind === 'time_range'" class="flex gap-2">
            <UFormField label="开门"><UInput v-model="form.open" type="time" /></UFormField>
            <UFormField label="关门"><UInput v-model="form.close" type="time" /></UFormField>
          </div>
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
