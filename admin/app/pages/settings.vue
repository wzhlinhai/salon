<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ title: '门店设置', width: 'narrow', description: '修改从选定的日期开始执行，之前的业务和已经下的单不受影响。' })

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

function show(setting: Setting | undefined, value: unknown): string {
  if (setting?.kind === 'time_range') {
    const r = value as TimeRange
    return `${r.open}–${r.close}`
  }
  if (setting?.kind === 'choice') {
    return setting.options?.find(o => o.value === value)?.label ?? String(value)
  }
  return String(value)
}

const settingOf = (key: string) => settings.value.find(s => s.key === key)

// 设置页分组（DESIGN.md 设置页）：按键名归到几组，组内保持后端目录的顺序；认不出的放最后"其他"。
const SETTING_GROUPS: { label: string, match: (key: string) => boolean }[] = [
  { label: '营业', match: k => ['business_hours', 'business_day_end', 'backfill_max_days', 'attendance_grace_minutes', 'draft_dual_confirm'].includes(k) },
  { label: '预约', match: k => k.startsWith('booking_') },
  { label: '轮牌', match: k => k.startsWith('rotation_') },
  { label: '收银与卡', match: k => k.startsWith('card_') || k.startsWith('collection_channel_') || ['member_price_rule', 'others_card_payment', 'points_per_recharge_yuan', 'void_window_minutes'].includes(k) },
  { label: '售后', match: k => k.startsWith('aftersale_') },
  { label: '业绩与提成', match: k => k.startsWith('performance_') || ['guest_count_split', 'commission_split'].includes(k) },
  { label: '小票', match: k => k.startsWith('receipt_') }
]

const settingGroups = computed(() => {
  const groups = [...SETTING_GROUPS, { label: '其他', match: () => true }]
    .map(g => ({ label: g.label, match: g.match, items: [] as Setting[] }))
  for (const setting of settings.value) groups.find(g => g.match(setting.key))!.items.push(setting)
  return groups.filter(g => g.items.length)
})

// 编辑中的设置。值按形式分开存，保存时再组装。
const editorGuard = useRequestGuard()
const editing = ref<Setting | null>(null)
const form = reactive({ choice: '', integer: 0, time: '', text: '', open: '', close: '', from: '' })

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
  } else if (s.kind === 'text') {
    form.text = String(s.value ?? '')
  } else {
    form.time = s.value as string
  }
  form.from = today.value
}

async function save(reset: boolean) {
  const current = editorGuard.hold()
  const s = editing.value!
  const value = reset ? null : s.kind === 'time_range' ? { open: form.open, close: form.close } : s.kind === 'choice' ? form.choice : s.kind === 'integer' ? Number(form.integer) : s.kind === 'text' ? form.text : form.time
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

// 门店名：侧栏顶上显示的就是它（app.vue 读的同一份状态）。
const storeName = useState('store-name', () => '')
const renameForm = useModalForm()
const { open: renameOpen, saving: renaming } = renameForm
const newName = ref('')

function editName() {
  renameForm.begin(() => {
    newName.value = storeName.value
  })
}

async function rename() {
  const name = newName.value.trim()
  await renameForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/rename_store', { body: { name } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    storeName.value = data.name
    renameOpen.value = false
    toast.add({ title: '门店名已改', color: 'success' })
  })
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
    <PageDescriptionTail v-if="today">今天是营业日 {{ today }}。</PageDescriptionTail>

    <section data-setting="store_name">
      <div class="sec">门店</div>
      <div class="box grid gap-x-6 gap-y-2 px-4 py-3.5 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] md:items-center">
        <div>
          <div class="text-sm font-medium">门店名</div>
          <div class="mt-0.5 text-xs text-muted">显示在侧栏顶上和小票上，改了立即生效</div>
        </div>
        <div class="text-sm">{{ storeName }}</div>
        <UButton v-if="can('settings.manage') && storeName" size="sm" color="neutral" variant="ghost" class="justify-self-start md:justify-self-end" aria-label="修改门店名" @click="editName">修改</UButton>
      </div>
    </section>

    <section v-for="group in settingGroups" :key="group.label" :data-setting-group="group.label">
      <div class="sec">{{ group.label }}</div>
      <div class="box overflow-hidden p-0">
        <div
          v-for="s in group.items"
          :key="s.key"
          :data-setting="s.key"
          class="grid gap-x-6 gap-y-2 border-t border-default px-4 py-3.5 first:border-t-0 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] md:items-center"
        >
          <div>
            <div class="text-sm font-medium">{{ s.label }}</div>
            <div class="mt-0.5 text-xs text-muted">{{ s.hint }}</div>
          </div>
          <div class="text-sm">
            <div class="flex flex-wrap items-center gap-2">
              <span>{{ show(s, s.value) }}</span>
              <span v-if="s.is_default" class="text-xs text-dimmed">系统默认</span>
              <span v-else class="text-xs text-dimmed">{{ s.effective_from }} 起</span>
            </div>
            <div v-for="u in s.upcoming" :key="u.effective_from" class="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted">
              <span class="tag bg-tag-yellow">已安排</span>
              {{ u.effective_from }} 起改为 {{ show(s, u.value) }}{{ u.is_default ? '（系统默认）' : '' }}
            </div>
          </div>
          <UButton v-if="can('settings.manage')" size="sm" color="neutral" variant="ghost" class="justify-self-start md:justify-self-end" @click="openEditor(s)">修改</UButton>
        </div>
      </div>
    </section>

    <section data-setting="customer_sources">
      <div class="sec">顾客来源</div>
      <div class="box space-y-2">
        <div v-for="source in sources" :key="source.id" class="flex items-center gap-2">
          <template v-if="can('settings.manage')">
            <UInput v-model="source.name" :aria-label="`顾客来源 ${source.name}`" class="flex-1" @change="saveSource(source)" />
            <USwitch v-model="source.active" :label="source.active ? '启用' : '停用'" @update:model-value="saveSource(source)" />
          </template>
          <template v-else>
            <span>{{ source.name }}</span>
            <span v-if="!source.active" class="tag bg-tag-gray">停用</span>
          </template>
        </div>
        <form v-if="can('settings.manage')" class="flex gap-2 pt-2" @submit.prevent="addSource">
          <UInput v-model="sourceName" aria-label="新顾客来源" placeholder="新顾客来源" class="flex-1" />
          <UButton type="submit" size="sm" color="neutral" variant="outline" icon="i-lucide-plus">添加</UButton>
        </form>
      </div>
    </section>

    <section v-if="can('audit_log.view')">
      <div class="sec">修改记录</div>
      <div class="box">
        <div v-if="!changes.items.value.length" class="text-sm text-muted">还没有修改过设置</div>
        <table v-else class="w-full text-sm">
          <thead class="text-left text-xs text-dimmed">
            <tr><th class="py-1.5 font-normal">时间</th><th class="font-normal">修改人</th><th class="font-normal">设置</th><th class="font-normal">改前</th><th class="font-normal">改后</th><th class="font-normal">生效日期</th></tr>
          </thead>
          <tbody>
            <tr v-for="c in changes.items.value" :key="c.id" class="border-t border-default">
              <td class="py-2 tabular-nums">{{ new Date(c.changed_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }}</td>
              <td>{{ c.changed_by.name }}</td>
              <td>{{ c.label }}</td>
              <td>{{ show(settingOf(c.key), c.old_value) }}{{ c.old_is_default ? '（默认）' : '' }}</td>
              <td>{{ show(settingOf(c.key), c.value) }}{{ c.is_default ? '（恢复默认）' : '' }}</td>
              <td>{{ c.effective_from }}</td>
            </tr>
          </tbody>
        </table>
        <UButton v-if="changes.hasMore.value" :loading="changes.loading.value" size="sm" color="neutral" variant="ghost" class="mt-2" @click="changes.loadMore">加载更多</UButton>
      </div>
    </section>

    <UModal v-model:open="renameOpen" title="修改门店名">
      <template #body>
        <UFormField label="门店名" required><UInput v-model="newName" :maxlength="100" class="w-full" aria-label="门店名" /></UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="renameOpen = false">
          <UButton :loading="renaming" :disabled="!newName.trim() || newName.trim() === storeName" @click="rename">保存</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal :open="!!editing" :title="`修改${editing?.label ?? ''}`" :description="editing ? `系统默认：${show(editing, editing.default)}` : undefined" @update:open="v => { if (!v) editing = null }">
      <template #body>
        <div v-if="editing" class="space-y-5">
          <div v-if="editing.kind === 'time_range'" class="grid grid-cols-2 gap-4">
            <UFormField label="开门"><UInput v-model="form.open" type="time" /></UFormField>
            <UFormField label="关门"><UInput v-model="form.close" type="time" /></UFormField>
          </div>
          <UFormField v-else-if="editing.kind === 'choice'" label="做法"><USelect v-model="form.choice" aria-label="做法" :items="editing.options" value-key="value" label-key="label" /></UFormField>
          <UFormField v-else-if="editing.kind === 'integer'" :label="editing.label"><UInput v-model="form.integer" type="number" :min="editing.min" :max="editing.max" :step="1" /></UFormField>
          <UFormField v-else-if="editing.kind === 'text'" :label="editing.label"><UInput v-model="form.text" :maxlength="editing.max" :aria-label="editing.label" /></UFormField>
          <UFormField v-else label="时间"><UInput v-model="form.time" type="time" /></UFormField>
          <UFormField label="生效日期" help="不能早于今天"><DateInput v-model="form.from" :min="today" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="editing = null">
          <template #leading>
            <UButton color="neutral" variant="ghost" @click="save(true)">恢复系统默认</UButton>
          </template>
          <UButton @click="save(false)">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
