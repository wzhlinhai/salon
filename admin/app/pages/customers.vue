<script setup lang="ts">
import type { components } from '~/utils/api'
import type { TableRow } from '@nuxt/ui'

definePageMeta({ permission: 'customer.view' })

type Customer = components['schemas']['Customer']
const api = useApi()
const toast = useToast()
const { can } = useMe()
const route = useRoute()
const router = useRouter()
const NONE = 'none'

// 列表、搜索和筛选
const items = ref<Customer[]>([])
const staff = ref<components['schemas']['StaffOption'][]>([])
const sources = ref<components['schemas']['CustomerSource'][]>([])
const query = ref('')
const includeUnidentified = ref(false)
const responsible = ref(NONE)
const offset = ref(0)
const selected = ref<string[]>([])
const loading = ref(false)
let request = 0
let timer: ReturnType<typeof setTimeout> | undefined
const staffItems = computed(() => staff.value.map(s => ({
  label: s.name + (s.active ? '' : '（已停用）'),
  value: s.id
})))
const activeStaffItems = computed(() => staff.value
  .filter(s => s.active)
  .map(s => ({ label: s.name, value: s.id }))
)
const labelNames: Record<string, string> = {
  allergy: '过敏',
  scalp_sensitive: '头皮敏感',
  recent_chemical: '近期烫染'
}
const columns = computed(() => [
  ...(can('customer.assign') ? [{ id: 'select', header: '选择' }] : []),
  { id: 'name', header: '姓名' },
  { accessorKey: 'phone', header: '手机号' },
  { id: 'gender', header: '性别' },
  { id: 'source', header: '来源' },
  { id: 'responsible', header: '负责人' },
  { id: 'safety', header: '安全标签' },
  { id: 'created', header: '建档时间' }
])

async function load() {
  const current = ++request
  loading.value = true
  const res = await api.POST('/api/ops/search_customers', {
    body: {
      query: query.value,
      include_unidentified: includeUnidentified.value,
      responsible_staff_id: responsible.value === NONE ? undefined : responsible.value,
      limit: 50,
      offset: offset.value
    }
  })
  if (current !== request) return
  loading.value = false
  if (res.data) {
    items.value = res.data.items
    selected.value = []
  } else {
    items.value = []
    toast.add({ title: errorText(res.error), color: 'error' })
  }
}

async function loadOptions() {
  const [s, c] = await Promise.all([
    api.POST('/api/ops/list_staff_options', { body: {} }),
    api.POST('/api/ops/list_customer_sources', { body: {} })
  ])
  if (s.data) {
    staff.value = s.data.staff
  } else {
    toast.add({ title: errorText(s.error), color: 'error' })
  }
  if (c.data) {
    sources.value = c.data.items
  } else {
    toast.add({ title: errorText(c.error), color: 'error' })
  }
}

function search() {
  clearTimeout(timer)
  offset.value = 0
  load()
}

function changePage(change: number) {
  offset.value += change * 50
  load()
}

function toggle(id: string, value: boolean | 'indeterminate') {
  selected.value = value === true ? [...selected.value, id] : selected.value.filter(v => v !== id)
}

function done(error: unknown, ok: unknown, message: string) {
  toast.add({ title: ok ? message : errorText(error), color: ok ? 'success' : 'error' })
  if (ok) {
    load()
  }
  return !!ok
}

watch(query, () => {
  clearTimeout(timer)
  timer = setTimeout(search, 300)
})

watch([includeUnidentified, responsible], search)

onBeforeUnmount(() => {
  clearTimeout(timer)
  request++
})

onMounted(async () => {
  await Promise.all([load(), loadOptions()])
})

// 新建顾客
const createOpen = ref(false)
const creating = ref(false)
const profile = reactive({
  phone: '',
  name: '',
  gender: NONE,
  source: NONE,
  note: ''
})
const sourceItems = computed(() => [
  { label: '不设来源', value: NONE },
  ...sources.value.filter(s => s.active).map(s => ({ label: s.name, value: s.id }))
])
const genderItems = [
  { label: '未填写', value: NONE },
  { label: '男', value: 'male' },
  { label: '女', value: 'female' }
]

function openCreate() {
  Object.assign(profile, {
    phone: '',
    name: '',
    gender: NONE,
    source: NONE,
    note: ''
  })
  createOpen.value = true
}

async function create() {
  if (creating.value) return
  creating.value = true
  try {
    const res = await api.POST('/api/ops/create_customer', {
      body: {
        phone: profile.phone || undefined,
        name: profile.name || undefined,
        gender: profile.gender === NONE ? undefined : profile.gender as 'male' | 'female',
        source_id: profile.source === NONE ? undefined : profile.source,
        note: profile.note || undefined
      }
    })
    if (!res.data) {
      done(res.error, null, '')
      return
    }
    toast.add({
      title: res.data.existed ? '这个手机号已有档案' : '顾客已新建',
      color: res.data.existed ? 'info' : 'success'
    })
    createOpen.value = false
    openDetail(res.data.customer.id)
    load()
  } finally {
    creating.value = false
  }
}

// 顾客详情
const detailID = computed(() => typeof route.query.id === 'string' ? route.query.id : '')

function openDetail(id: string) {
  router.replace({ query: { ...route.query, id } })
}

function closeDetail() {
  router.replace({ query: { ...route.query, id: undefined } })
}

function selectRow(_event: Event, row: TableRow<Customer>) {
  openDetail(row.original.id)
}

// 分配负责人
const assignOpen = ref(false)
const assignStaff = ref(NONE)
const assigning = ref(false)

function openAssign() {
  assignStaff.value = NONE
  assignOpen.value = true
}

async function assign() {
  if (assigning.value) return
  assigning.value = true
  try {
    const res = await api.POST('/api/ops/assign_customer_responsible', {
      body: {
        customer_ids: selected.value,
        staff_id: assignStaff.value === NONE ? undefined : assignStaff.value
      }
    })
    if (done(res.error, res.data, '负责人已分配')) {
      assignOpen.value = false
    }
  } finally {
    assigning.value = false
  }
}

// 负责人交接
const handoverOpen = ref(false)
const fromStaff = ref('')
const toStaff = ref('')
const handingOver = ref(false)

async function handover() {
  if (handingOver.value) return
  handingOver.value = true
  try {
    const res = await api.POST('/api/ops/hand_over_customers', {
      body: { from_staff_id: fromStaff.value, to_staff_id: toStaff.value }
    })
    if (done(res.error, res.data, `已移交 ${res.data?.count ?? 0} 位顾客`)) {
      handoverOpen.value = false
    }
  } finally {
    handingOver.value = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h1 class="text-xl font-semibold">顾客</h1>
      <div class="flex gap-2">
        <UButton v-if="can('customer.assign')" variant="outline" @click="handoverOpen = true">负责人交接</UButton>
        <UButton v-if="can('customer.edit')" icon="i-lucide-plus" @click="openCreate">新建顾客</UButton>
      </div>
    </div>
    <div class="flex flex-wrap items-end gap-4">
      <UFormField label="搜索顾客">
        <UInput v-model="query" placeholder="手机号、后四位或姓名" @keydown.enter="search" />
      </UFormField>
      <UFormField label="负责人筛选">
        <USelect v-model="responsible" :items="[{ label: '全部负责人', value: NONE }, ...staffItems]" class="min-w-44" />
      </UFormField>
      <USwitch v-model="includeUnidentified" label="显示未识别顾客" />
      <UButton v-if="can('customer.assign') && selected.length" @click="openAssign">分配负责人</UButton>
    </div>
    <UTable :data="items" :columns="columns" :loading="loading" @select="selectRow">
      <template #select-cell="{ row }">
        <span @click.stop>
          <UCheckbox :aria-label="`选择顾客 ${row.original.name || '未识别顾客'}`" :model-value="selected.includes(row.original.id)" @update:model-value="v => toggle(row.original.id, v)" />
        </span>
      </template>
      <template #name-cell="{ row }">
        <UButton variant="link" @click.stop="openDetail(row.original.id)">{{ row.original.unidentified ? '未识别顾客' : row.original.name || '未填写姓名' }}</UButton>
      </template>
      <template #gender-cell="{ row }">{{ row.original.gender === 'male' ? '男' : row.original.gender === 'female' ? '女' : '—' }}</template>
      <template #source-cell="{ row }">{{ row.original.source?.name || '—' }}</template>
      <template #responsible-cell="{ row }">{{ row.original.responsible?.name || '—' }}</template>
      <template #safety-cell="{ row }">
        <div class="flex flex-wrap gap-1">
          <UBadge v-for="label in row.original.active_safety_labels" :key="label.id" color="error">{{ labelNames[label.kind] }}</UBadge>
        </div>
      </template>
      <template #created-cell="{ row }">{{ new Date(row.original.created_at).toLocaleString('zh-CN') }}</template>
    </UTable>
    <div class="flex items-center gap-3">
      <UButton variant="outline" :disabled="offset === 0 || loading" @click="changePage(-1)">上一页</UButton>
      <span>第 {{ offset / 50 + 1 }} 页</span>
      <UButton variant="outline" :disabled="items.length < 50 || loading" @click="changePage(1)">下一页</UButton>
    </div>
    <CustomerDetail v-if="detailID" :id="detailID" :sources="sources" @close="closeDetail" @open="openDetail" @changed="load" />
    <UModal v-model:open="createOpen" title="新建顾客">
      <template #body>
        <div class="space-y-3">
          <UFormField label="手机号">
            <UInput v-model="profile.phone" type="tel" class="w-full" />
          </UFormField>
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
        <UButton :loading="creating" @click="create">保存</UButton>
      </template>
    </UModal>
    <UModal v-if="can('customer.assign')" v-model:open="assignOpen" title="分配负责人">
      <template #body>
        <UFormField label="负责人">
          <USelect v-model="assignStaff" :items="[{ label: '不设负责人', value: NONE }, ...activeStaffItems]" class="w-full" />
        </UFormField>
        <p class="mt-3">为选中的 {{ selected.length }} 位顾客分配负责人。</p>
      </template>
      <template #footer>
        <UButton :loading="assigning" @click="assign">确认分配</UButton>
      </template>
    </UModal>
    <UModal v-if="can('customer.assign')" v-model:open="handoverOpen" title="负责人交接">
      <template #body>
        <div class="space-y-3">
          <UFormField label="交出的员工">
            <USelect v-model="fromStaff" :items="staffItems" class="w-full" />
          </UFormField>
          <UFormField label="接手的员工">
            <USelect v-model="toStaff" :items="activeStaffItems" class="w-full" />
          </UFormField>
          <p>确认后，将交出员工负责的全部顾客移交给接手员工。</p>
        </div>
      </template>
      <template #footer>
        <UButton :disabled="!fromStaff || !toStaff || fromStaff === toStaff" :loading="handingOver" @click="handover">确认交接</UButton>
      </template>
    </UModal>
  </div>
</template>
