<script setup lang="ts">
import type { components } from '~/utils/api'

type Customer = components['schemas']['Customer']
type LabelInput = components['schemas']['AddSafetyLabelInput']

const props = defineProps<{
  id: string
  sources: components['schemas']['CustomerSource'][]
}>()
const emit = defineEmits<{
  close: []
  open: [id: string]
  changed: []
}>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const NONE = 'none'

// 详情资料
const customer = ref<Customer | null>(null)
const loadError = ref('')
const today = ref('')
const editing = ref(false)
const profile = reactive({
  name: '',
  gender: NONE,
  source: NONE,
  note: ''
})
const customerGuard = useRequestGuard()
const savingIds = ref(new Set<string>())
const saving = computed(() => savingIds.value.has(props.id))
const genderItems = [
  { label: '未填写', value: NONE },
  { label: '男', value: 'male' },
  { label: '女', value: 'female' }
]
const genderName = (value: string | null) => value === 'male' ? '男' : value === 'female' ? '女' : '未填写'
const customerName = (c: Customer) => c.unidentified ? '未识别顾客' : c.name || '未填写姓名'
const sourceItems = computed(() => [
  { label: '不设来源', value: NONE },
  ...props.sources
    .filter(s => s.active || s.id === customer.value?.source?.id)
    .map(s => ({ label: s.name + (s.active ? '' : '（已停用）'), value: s.id }))
])
const detailGuard = useRequestGuard()

async function load() {
  const id = props.id
  const request = detailGuard.next()
  loadError.value = ''
  if (can('audit_log.view')) logs.reload()
  else logs.clear()
  const res = await api.POST('/api/ops/get_customer', { body: { id } })
  if (id !== props.id || !request()) return
  if (res.data) {
    customer.value = res.data
  } else {
    loadError.value = errorText(res.error)
    toast.add({ title: loadError.value, color: 'error' })
  }
}

// 按发起时的顾客保存：成功总是刷新列表，仅当前顾客提示并执行后续操作（默认重载详情）。
async function act<R>(
  request: (id: string) => Promise<{ data?: R, error?: unknown }>,
  message: string,
  onDone: (data: R) => void = () => {
    load()
  }
): Promise<boolean> {
  const id = props.id
  if (savingIds.value.has(id)) return false
  const current = customerGuard.hold()
  savingIds.value.add(id)
  try {
    const { data, error } = await request(id)
    if (data) emit('changed')
    if (id !== props.id || !current()) {
      if (!data) toast.add({ title: errorText(error), color: 'error' })
      return false
    }
    toast.add({ title: data ? message : errorText(error), color: data ? 'success' : 'error' })
    if (data) onDone(data)
    return !!data
  } finally {
    savingIds.value.delete(id)
  }
}

function edit() {
  const c = customer.value!
  Object.assign(profile, {
    name: c.name ?? '',
    gender: c.gender ?? NONE,
    source: c.source?.id ?? NONE,
    note: c.note ?? ''
  })
  editing.value = true
}

async function saveProfile() {
  if (await act(id => api.POST('/api/ops/update_customer_profile', {
    body: {
      id,
      name: profile.name || undefined,
      gender: profile.gender === NONE ? undefined : profile.gender as 'male' | 'female',
      source_id: profile.source === NONE ? undefined : profile.source,
      note: profile.note || undefined
    }
  }), '资料已保存')) {
    editing.value = false
  }
}

function setDetailOpen(open: boolean) {
  if (!open) {
    emit('close')
  }
}

// 手机号
const phoneOpen = ref(false)
const phone = ref('')

function openPhone() {
  phone.value = ''
  phoneOpen.value = true
}

async function savePhone() {
  const path = customer.value?.phone ? '/api/ops/change_customer_phone' : '/api/ops/add_customer_phone'
  if (await act(id => api.POST(path, { body: { id, phone: phone.value } }), '手机号已保存')) {
    phoneOpen.value = false
  }
}

// 安全标签
const labelOpen = ref(false)
const label = reactive({
  kind: 'allergy' as LabelInput['kind'],
  source: 'customer_said' as LabelInput['source'],
  observed_on: '',
  note: ''
})
const revokeTarget = ref<components['schemas']['SafetyLabel'] | null>(null)
const labelNames: Record<string, string> = {
  allergy: '过敏',
  scalp_sensitive: '头皮敏感',
  recent_chemical: '近期烫染'
}
const labelSources: Record<string, string> = {
  customer_said: '顾客自述',
  staff_observed: '员工观察'
}

function openLabel() {
  Object.assign(label, {
    kind: 'allergy',
    source: 'customer_said',
    observed_on: today.value,
    note: ''
  })
  labelOpen.value = true
}

async function addLabel() {
  if (await act(id => api.POST('/api/ops/add_safety_label', {
    body: { ...label, customer_id: id, note: label.note || undefined }
  }), '安全标签已添加')) {
    labelOpen.value = false
  }
}

function setRevokeOpen(open: boolean) {
  if (!open) {
    revokeTarget.value = null
  }
}

async function revoke() {
  const target = revokeTarget.value
  if (!target) return
  if (await act(id => api.POST('/api/ops/revoke_safety_label', {
    body: { customer_id: id, id: target.id }
  }), '安全标签已撤销')) {
    revokeTarget.value = null
  }
}

// 合并
const mergeOpen = ref(false)
const mergeQuery = ref('')
const mergeOffset = ref(0)
const mergeHasNext = ref(false)
const candidates = ref<Customer[]>([])
const other = ref<Customer | null>(null)
const keepID = ref('')
const mergeConfirm = ref(false)
const searching = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
const searchGuard = useRequestGuard()

function openMerge() {
  mergeQuery.value = ''
  mergeOffset.value = 0
  candidates.value = []
  other.value = null
  keepID.value = props.id
  mergeConfirm.value = false
  mergeOpen.value = true
  searchMerge()
}

async function searchMerge() {
  clearTimeout(timer)
  const current = searchGuard.next()
  searching.value = true
  try {
    const res = await api.POST('/api/ops/search_customers', {
      body: {
        query: mergeQuery.value,
        include_unidentified: true,
        limit: 50,
        offset: mergeOffset.value
      }
    })
    if (!current()) return
    if (res.data) {
      candidates.value = res.data.items.filter(c => c.id !== props.id)
      mergeHasNext.value = res.data.items.length === 50
    } else {
      toast.add({ title: errorText(res.error), color: 'error' })
    }
  } finally {
    if (current()) searching.value = false
  }
}

function searchCandidates() {
  mergeOffset.value = 0
  searchMerge()
}

function changeMergePage(change: number) {
  mergeOffset.value += change * 50
  searchMerge()
}

function selectCandidate(c: Customer) {
  other.value = c
  keepID.value = props.id
}

watch(mergeQuery, () => {
  searchGuard.invalidate()
  clearTimeout(timer)
  candidates.value = []
  other.value = null
  keepID.value = props.id
  timer = setTimeout(searchCandidates, 300)
})

async function merge() {
  const target = other.value
  if (!target) return
  await act(id => api.POST('/api/ops/merge_customers', {
    body: {
      keep_id: keepID.value,
      merged_id: keepID.value === id ? target.id : id
    }
  }), '顾客已合并', (data) => {
    mergeConfirm.value = false
    mergeOpen.value = false
    emit('open', data.keep.id)
    if (data.keep.id === props.id) load()
  })
}

// 操作日志
const logs = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_audit_log', {
    body: { target_id: props.id, before_id: beforeId }
  })
  return { data: data?.entries, error }
})

const actions: Record<string, string> = {
  create_customer: '新建顾客',
  update_customer_profile: '修改资料',
  add_customer_phone: '补手机号',
  change_customer_phone: '改手机号',
  add_safety_label: '添加安全标签',
  revoke_safety_label: '撤销安全标签',
  merge_customers: '合并顾客',
  assign_customer_responsible: '分配负责人',
  hand_over_customers: '负责人交接'
}

function snapshot(value: unknown): string {
  if (!value || typeof value !== 'object') return '无'
  const c = value as Customer
  const labels = c.active_safety_labels?.map(l =>
    `${labelNames[l.kind]}（${labelSources[l.source]}，${l.observed_on}，${l.note || '无备注'}）`
  ).join('、')
  return [
    c.unidentified ? '未识别顾客' : c.name || '未填写姓名',
    c.phone || '未留手机号',
    genderName(c.gender),
    `来源：${c.source?.name || '未填写'}`,
    `负责人：${c.responsible?.name || '不设'}`,
    `备注：${c.note || '无'}`,
    `安全标签：${labels || '无'}`,
    ...(c.merged_into ? ['已合并到另一位顾客'] : [])
  ].join('；')
}

// 读取档案及营业日，切换顾客时关闭编辑入口
watch(() => props.id, () => {
  customerGuard.next()
  customer.value = null
  editing.value = false
  phoneOpen.value = false
  labelOpen.value = false
  mergeOpen.value = false
  mergeConfirm.value = false
  revokeTarget.value = null
  other.value = null
  candidates.value = []
  searchGuard.invalidate()
  clearTimeout(timer)
  load()
})

const businessDateGuard = useRequestGuard()

onMounted(async () => {
  const current = businessDateGuard.next()
  load()
  const res = await api.POST('/api/ops/get_settings', { body: {} })
  if (!current()) return
  if (res.data) {
    today.value = res.data.business_date
  } else {
    toast.add({ title: errorText(res.error), color: 'error' })
  }
})

onBeforeUnmount(() => {
  clearTimeout(timer)
})
</script>

<template>
  <USlideover :open="true" title="顾客详情" :ui="{ content: 'sm:max-w-3xl' }" @update:open="setDetailOpen">
    <template #body>
      <div v-if="customer" class="space-y-6">
        <UAlert v-if="customer.merged_into" title="已合并到另一位顾客" color="warning">
          <template #description>
            <UButton @click="emit('open', customer.merged_into)">查看保留的顾客</UButton>
          </template>
        </UAlert>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-lg font-semibold">{{ customerName(customer) }}</h2>
          <div v-if="!customer.merged_into" class="flex gap-2">
            <UButton v-if="can('customer.edit')" variant="outline" @click="edit">修改资料</UButton>
            <UButton v-if="can('customer.merge')" variant="outline" @click="openMerge">合并重复顾客</UButton>
          </div>
        </div>
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          <dt>姓名</dt>
          <dd>{{ customer.name || '未填写' }}</dd>
          <dt>手机号</dt>
          <dd class="flex items-center gap-2">
            {{ customer.phone || '未填写' }}
            <UButton v-if="!customer.merged_into && ((!customer.phone && can('customer.edit')) || (customer.phone && can('customer.merge')))" size="xs" variant="link" @click="openPhone">{{ customer.phone ? '改手机号' : '补手机号' }}</UButton>
          </dd>
          <dt>性别</dt>
          <dd>{{ genderName(customer.gender) }}</dd>
          <dt>来源</dt>
          <dd>{{ customer.source?.name || '未填写' }}</dd>
          <dt>负责人</dt>
          <dd>{{ customer.responsible?.name || '不设负责人' }}</dd>
          <dt>备注</dt>
          <dd class="whitespace-pre-wrap">{{ customer.note || '无' }}</dd>
          <dt>建档时间</dt>
          <dd>{{ new Date(customer.created_at).toLocaleString('zh-CN') }}</dd>
        </dl>
        <UCard>
          <template #header>
            <div class="flex justify-between items-center">
              <h3 class="font-medium">安全标签</h3>
              <UButton v-if="!customer.merged_into && can('customer.safety_label')" :disabled="!today" size="sm" @click="openLabel">添加安全标签</UButton>
            </div>
          </template>
          <p v-if="!customer.active_safety_labels.length" class="text-sm text-muted">暂无有效安全标签</p>
          <div v-for="l in customer.active_safety_labels" :key="l.id" class="py-2 flex items-start justify-between gap-3">
            <div class="space-y-1">
              <UBadge color="error">{{ labelNames[l.kind] }}</UBadge>
              <p class="text-sm">{{ labelSources[l.source] }} · {{ l.observed_on }}</p>
              <p class="whitespace-pre-wrap text-sm">{{ l.note || '无备注' }}</p>
            </div>
            <UButton v-if="!customer.merged_into && can('customer.safety_label')" size="xs" color="error" variant="ghost" @click="revokeTarget = l">撤销</UButton>
          </div>
        </UCard>
        <CustomerCards v-if="can('customer.view')" :id="id" :merged="!!customer.merged_into" />
        <CustomerRedPackets v-if="can('customer.view')" :id="customer.id" :merged="!!customer.merged_into" />
        <UCard v-if="can('audit_log.view')">
          <template #header>
            <h3 class="font-medium">修改记录</h3>
          </template>
          <p v-if="!logs.items.value.length" class="text-sm text-muted">暂无修改记录</p>
          <div class="overflow-x-auto">
            <table v-if="logs.items.value.length" class="w-full text-sm">
              <thead class="text-left text-muted">
                <tr>
                  <th>时间</th>
                  <th>修改人</th>
                  <th>操作</th>
                  <th>改前</th>
                  <th>改后</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="entry in logs.items.value" :key="entry.id" class="border-t border-default align-top">
                  <td class="py-2">{{ new Date(entry.at).toLocaleString('zh-CN') }}</td>
                  <td>{{ entry.actor.name }}</td>
                  <td>{{ actions[entry.action] || entry.action }}</td>
                  <td class="min-w-48">{{ snapshot(entry.before) }}</td>
                  <td class="min-w-48">{{ snapshot(entry.after) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <UButton v-if="logs.hasMore.value" :loading="logs.loading.value" variant="outline" @click="logs.loadMore">加载更多</UButton>
        </UCard>
      </div>
      <div v-else-if="loadError" class="space-y-3">
        <p>{{ loadError }}</p>
        <UButton variant="outline" @click="load">重试</UButton>
      </div>
      <p v-else class="text-muted">正在读取顾客档案…</p>
    </template>
  </USlideover>
  <UModal v-model:open="editing" title="修改顾客资料">
    <template #body>
      <div class="space-y-3">
        <UFormField label="姓名">
          <UInput v-model="profile.name" class="w-full" />
        </UFormField>
        <UFormField label="性别">
          <USelect v-model="profile.gender" :items="genderItems" class="w-full" />
        </UFormField>
        <UFormField label="顾客来源">
          <USelect v-model="profile.source" :items="sourceItems" class="w-full" />
        </UFormField>
        <UFormField label="备注">
          <UTextarea v-model="profile.note" class="w-full" />
        </UFormField>
      </div>
    </template>
    <template #footer>
      <UButton :loading="saving" @click="saveProfile">保存</UButton>
    </template>
  </UModal>
  <UModal v-model:open="phoneOpen" :title="customer?.phone ? '改手机号' : '补手机号'">
    <template #body>
      <UFormField label="手机号">
        <UInput v-model="phone" type="tel" class="w-full" />
      </UFormField>
    </template>
    <template #footer>
      <UButton :loading="saving" @click="savePhone">保存</UButton>
    </template>
  </UModal>
  <UModal v-model:open="labelOpen" title="添加安全标签">
    <template #body>
      <div class="space-y-3">
        <UFormField label="类型">
          <USelect v-model="label.kind" :items="Object.entries(labelNames).map(([value, label]) => ({ value, label }))" class="w-full" />
        </UFormField>
        <UFormField label="来源">
          <USelect v-model="label.source" :items="Object.entries(labelSources).map(([value, label]) => ({ value, label }))" class="w-full" />
        </UFormField>
        <UFormField label="观察日期">
          <UInput v-model="label.observed_on" type="date" :max="today" />
        </UFormField>
        <UFormField label="备注">
          <UTextarea v-model="label.note" class="w-full" />
        </UFormField>
      </div>
    </template>
    <template #footer>
      <UButton :loading="saving" :disabled="!label.observed_on || label.observed_on > today" @click="addLabel">保存</UButton>
    </template>
  </UModal>
  <UModal :open="!!revokeTarget" title="撤销安全标签" @update:open="setRevokeOpen">
    <template #body>确认撤销“{{ labelNames[revokeTarget?.kind ?? ''] }}”安全标签？历史记录仍然保留。</template>
    <template #footer>
      <UButton :loading="saving" color="error" @click="revoke">确认撤销</UButton>
    </template>
  </UModal>
  <UModal v-model:open="mergeOpen" title="合并重复顾客" :ui="{ content: 'sm:max-w-2xl' }">
    <template #body>
      <div class="space-y-4">
        <UFormField label="搜索另一位顾客">
          <UInput v-model="mergeQuery" placeholder="手机号、后四位或姓名" :loading="searching" class="w-full" @keydown.enter="searchCandidates" />
        </UFormField>
        <div class="max-h-48 overflow-y-auto space-y-1">
          <UButton v-for="c in candidates" :key="c.id" :data-customer-id="c.id" variant="outline" class="w-full" @click="selectCandidate(c)">{{ customerName(c) }} · {{ c.phone || '未留手机号' }} · {{ new Date(c.created_at).toLocaleString('zh-CN') }}</UButton>
        </div>
        <div class="flex items-center gap-3">
          <UButton variant="outline" :disabled="mergeOffset === 0 || searching" @click="changeMergePage(-1)">上一页</UButton>
          <span>第 {{ mergeOffset / 50 + 1 }} 页</span>
          <UButton variant="outline" :disabled="!mergeHasNext || searching" @click="changeMergePage(1)">下一页</UButton>
        </div>
        <p v-if="!searching && !candidates.length" class="text-sm text-muted">未找到其他顾客</p>
        <div v-if="customer && other" class="grid grid-cols-2 gap-3">
          <UCard v-for="c in [customer, other]" :key="c.id">
            <dl class="space-y-2 text-sm">
              <dt class="font-medium">{{ customerName(c) }}</dt>
              <dd>手机号：{{ c.phone || '未填写' }}</dd>
              <dd>性别：{{ genderName(c.gender) }}</dd>
              <dd>来源：{{ c.source?.name || '未填写' }}</dd>
              <dd>负责人：{{ c.responsible?.name || '不设' }}</dd>
              <dd class="whitespace-pre-wrap">备注：{{ c.note || '无' }}</dd>
              <dd>建档时间：{{ new Date(c.created_at).toLocaleString('zh-CN') }}</dd>
              <dd>
                <UBadge v-for="l in c.active_safety_labels" :key="l.id" color="error" class="mr-1">{{ labelNames[l.kind] }}</UBadge>
              </dd>
            </dl>
            <UButton class="mt-3" :variant="keepID === c.id ? 'solid' : 'outline'" :aria-pressed="keepID === c.id" @click="keepID = c.id">保留这位顾客</UButton>
          </UCard>
        </div>
      </div>
    </template>
    <template #footer>
      <UButton :disabled="!other" color="error" @click="mergeConfirm = true">继续合并</UButton>
    </template>
  </UModal>
  <UModal v-model:open="mergeConfirm" title="确认合并顾客">
    <template #body>
      <p>合并后不可撤销：被合并顾客的安全标签等记录归到保留的顾客名下</p>
      <p v-if="customer && other" class="mt-3">保留：{{ customerName(keepID === id ? customer : other) }}；被合并：{{ customerName(keepID === id ? other : customer) }}</p>
    </template>
    <template #footer>
      <UButton :loading="saving" color="error" @click="merge">确认合并</UButton>
    </template>
  </UModal>
</template>
