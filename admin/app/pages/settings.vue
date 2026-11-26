<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ title: '门店设置', width: 'narrow', description: '修改从选定的日期开始执行，之前的业务和已经下的单不受影响。', art: 'sign' })

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

const loadError = ref('')

async function load() {
  const request = settingsGuard.next()
  if (can('audit_log.view')) changes.reload()
  const { data, error } = await api.POST('/api/ops/get_settings', { body: {} })
  if (!request()) return
  // 读失败时留着上次读到的设置；一次都没读到过才原地给「重试」，别把设置清空成一张白纸。
  if (!data) {
    if (settings.value.length) toast.add({ title: errorText(error), color: 'error' })
    else loadError.value = errorText(error)
    return
  }
  loadError.value = ''
  today.value = data.business_date
  settings.value = data.settings
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
  { label: '业绩与提成', match: k => k.startsWith('performance_') || ['guest_count_split', 'commission_split', 'payroll_auto_lock_day'].includes(k) },
  { label: '小票', match: k => k.startsWith('receipt_') }
]

const settingGroups = computed(() => {
  const groups = [...SETTING_GROUPS, { label: '其他', match: () => true }]
    .map(g => ({ label: g.label, match: g.match, items: [] as Setting[] }))
  for (const setting of settings.value) groups.find(g => g.match(setting.key))!.items.push(setting)
  return groups.filter(g => g.items.length)
})

// 编辑中的设置。改的时候要挑生效日期，所以在弹窗里改，行上只写现在的值和「修改」。值按形式分开存，保存时再组装。
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

// 保存中按钮转圈、不能再点：网慢时连点会发两次修改，修改记录里多出一版。
const settingSaving = ref(false)

async function save(reset: boolean) {
  if (settingSaving.value) return
  settingSaving.value = true
  try {
    await saveSetting(reset)
  } finally {
    settingSaving.value = false
  }
}

async function saveSetting(reset: boolean) {
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

// 新建等上一次回来再发，回车连按不会建出两个同名来源。
const sourceSaving = ref(false)

async function addSource() {
  if (sourceSaving.value) return
  sourceSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/create_customer_source', {
      body: { name: sourceName.value }
    })
    if (data) {
      sourceName.value = ''
    } else {
      toast.add({ title: errorText(error), color: 'error' })
    }
  } finally {
    sourceSaving.value = false
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

<!-- 窄栏文档（样稿 16）：一组一个小标题，一行左边名字和一句说明，右边现在的值和「修改」，行间一道细线；窄屏值掉到下一行。 -->
<template>
  <div>
    <PageDescriptionTail v-if="today">今天是营业日 {{ today }}。</PageDescriptionTail>

    <section data-setting="store_name">
      <h2 class="sec">门店</h2>
      <div class="border-y border-default">
        <div class="flex flex-wrap items-start gap-x-6 gap-y-1 py-3 text-sm">
          <div class="min-w-0 flex-1 basis-full md:basis-0">
            <p class="font-medium">门店名</p>
            <p class="text-meta text-muted">显示在侧栏顶上和小票上，改了立即生效</p>
          </div>
          <p class="flex-1 md:w-64 md:flex-none md:text-right">{{ storeName }}</p>
          <div v-if="can('settings.manage')" class="flex w-14 justify-end">
            <UButton v-if="storeName" size="sm" color="neutral" variant="ghost" aria-label="修改门店名" @click="editName">修改</UButton>
          </div>
        </div>
      </div>
    </section>

    <LoadError v-if="loadError && !settings.length" title="门店设置没有读出来" :hint="loadError" @retry="load()" />

    <section v-for="group in settingGroups" :key="group.label" :data-setting-group="group.label">
      <h2 class="sec">{{ group.label }}</h2>
      <div class="divide-y divide-default border-y border-default">
        <div v-for="s in group.items" :key="s.key" :data-setting="s.key" class="flex flex-wrap items-start gap-x-6 gap-y-1 py-3 text-sm">
          <div class="min-w-0 flex-1 basis-full md:basis-0">
            <p class="font-medium">{{ s.label }}</p>
            <p class="text-meta text-pretty text-muted">{{ s.hint }}</p>
          </div>
          <div class="flex-1 md:w-64 md:flex-none md:text-right">
            <p>{{ show(s, s.value) }}</p>
            <p class="text-meta text-muted">{{ s.is_default ? '系统默认' : `${s.effective_from} 起` }}</p>
            <p v-for="u in s.upcoming" :key="u.effective_from" class="mt-1 text-meta text-muted">
              <span class="tag bg-tag-yellow">已安排</span>
              {{ u.effective_from }} 起改为 {{ show(s, u.value) }}{{ u.is_default ? '（系统默认）' : '' }}
            </p>
          </div>
          <div v-if="can('settings.manage')" class="flex w-14 justify-end">
            <UButton size="sm" color="neutral" variant="ghost" @click="openEditor(s)">修改</UButton>
          </div>
        </div>
      </div>
    </section>

    <section data-setting="customer_sources">
      <h2 class="sec">顾客来源</h2>
      <p class="-mt-1 mb-2 text-meta text-muted">新建顾客时选的「从哪儿来」</p>
      <div class="divide-y divide-default border-y border-default">
        <div v-for="source in sources" :key="source.id" class="flex min-h-12 items-center gap-2 px-2 py-2 hover:bg-hover">
          <template v-if="can('settings.manage')">
            <UInput v-model="source.name" variant="ghost" :aria-label="`顾客来源 ${source.name}`" class="min-w-0 flex-1" :ui="{ base: source.active ? '' : 'text-muted' }" @change="saveSource(source)" />
            <USwitch v-model="source.active" size="sm" :label="source.active ? '启用' : '停用'" @update:model-value="saveSource(source)" />
          </template>
          <template v-else>
            <span class="px-3">{{ source.name }}</span>
            <span v-if="!source.active" class="tag bg-tag-gray">停用</span>
          </template>
        </div>
        <form v-if="can('settings.manage')" class="flex min-h-12 items-center gap-2 px-2 py-2" @submit.prevent="addSource">
          <UInput v-model="sourceName" variant="ghost" icon="i-lucide-plus" aria-label="新顾客来源" placeholder="新顾客来源" class="min-w-0 flex-1" />
          <UButton v-if="sourceName || sourceSaving" type="submit" size="sm" color="neutral" variant="ghost" :loading="sourceSaving">添加</UButton>
        </form>
      </div>
    </section>

    <section v-if="can('audit_log.view')">
      <h2 class="sec">修改记录</h2>
      <div class="overflow-x-auto">
        <p v-if="!changes.items.value.length" class="text-sm text-muted">还没有修改过设置</p>
        <table v-else class="data-table">
          <thead class="text-left">
            <tr><th>时间</th><th>修改人</th><th>设置</th><th>改前</th><th>改后</th><th>生效日期</th></tr>
          </thead>
          <tbody>
            <tr v-for="c in changes.items.value" :key="c.id">
              <td class="tabular-nums whitespace-nowrap">{{ new Date(c.changed_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }}</td>
              <td>{{ c.changed_by.name }}</td>
              <td>{{ c.label }}</td>
              <td>{{ show(settingOf(c.key), c.old_value) }}{{ c.old_is_default ? '（默认）' : '' }}</td>
              <td>{{ show(settingOf(c.key), c.value) }}{{ c.is_default ? '（恢复默认）' : '' }}</td>
              <td class="tabular-nums whitespace-nowrap">{{ c.effective_from }}</td>
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
          <template #leading>
            <span v-if="!newName.trim()" class="text-meta text-muted">填写新门店名后再保存</span>
            <span v-else-if="newName.trim() === storeName" class="text-meta text-muted">门店名未改动</span>
          </template>
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
            <UButton color="neutral" variant="ghost" :disabled="settingSaving" @click="save(true)">恢复系统默认</UButton>
          </template>
          <UButton :loading="settingSaving" @click="save(false)">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
