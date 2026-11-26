<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'staff.manage', title: '级别与岗位', description: '级别用来按人定价，岗位用来算提成。', art: 'dryer' })

type LevelOrPosition = components['schemas']['Level'] | components['schemas']['Position']
type ItemCategory = components['schemas']['ItemCategory']
const api = useApi()
const toast = useToast()
const { can } = useMe()
const canManageSettings = computed(() => can('settings.manage'))

const kinds = [
  { key: 'level', title: '级别', hint: '例如首席、总监，可用于按级别定价', list: '/api/ops/list_levels', create: '/api/ops/create_level', update: '/api/ops/update_level' },
  { key: 'position', title: '岗位', hint: '例如大工、助理，用于计算提成', list: '/api/ops/list_positions', create: '/api/ops/create_position', update: '/api/ops/update_position' }
] as const

const items = reactive<Record<string, LevelOrPosition[]>>({ level: [], position: [] })
const draft = reactive<Record<string, string>>({ level: '', position: '' })

const categories = ref<ItemCategory[]>([])
const categoryDraft = reactive({ name: '', performance_ratio: 100 })
const categorySaving = reactive<Record<string, boolean>>({})
const categoryAddSaving = ref(false)
const categoryActionGuard = useRequestGuard()
const categoryLoadGuard = useRequestGuard()
const categoryLoading = ref(false)

const saving = reactive<Record<string, boolean>>({})
const actionGuard = useRequestGuard()
const loadGuard = useRequestGuard()

async function load() {
  const request = loadGuard.next()
  for (const k of kinds) {
    const { data, error } = await api.POST(k.list, { body: {} })
    if (!request()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      continue
    }
    items[k.key] = data?.items ?? []
  }
}

async function loadCategories() {
  const request = categoryLoadGuard.next()
  categoryLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_item_categories', { body: {} })
    if (!request()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    categories.value = data.items
  } finally {
    if (request()) categoryLoading.value = false
  }
}

onMounted(() => {
  load()
  loadCategories()
})

function report(error: unknown, ok: unknown) {
  if (!ok) toast.add({ title: errorText(error), color: 'error' })
  load()
}

async function add(k: typeof kinds[number]) {
  if (saving[k.key]) return
  const current = actionGuard.hold()
  saving[k.key] = true
  try {
    const name = draft[k.key] ?? ''
    const { data, error } = await api.POST(k.create, { body: { name } })
    if (!current()) return
    if (data && draft[k.key] === name) draft[k.key] = ''
    report(error, data)
  } finally {
    if (current()) saving[k.key] = false
  }
}

function reportCategory(error: unknown, ok: unknown) {
  if (!ok) toast.add({ title: errorText(error), color: 'error' })
  loadCategories()
}

async function addCategory() {
  if (categoryAddSaving.value || !canManageSettings.value) return
  const current = categoryActionGuard.hold()
  categoryAddSaving.value = true
  try {
    const name = categoryDraft.name
    const ratio = Number(categoryDraft.performance_ratio)
    const { data, error } = await api.POST('/api/ops/create_item_category', { body: { name, performance_ratio: ratio } })
    if (!current()) return
    if (data && categoryDraft.name === name) {
      categoryDraft.name = ''
      categoryDraft.performance_ratio = 100
    }
    reportCategory(error, data)
  } finally {
    categoryAddSaving.value = false
  }
}

async function saveCategory(category: ItemCategory) {
  if (categorySaving[category.id] || !canManageSettings.value) return
  const current = categoryActionGuard.hold()
  categorySaving[category.id] = true
  try {
    const { data, error } = await api.POST('/api/ops/update_item_category', {
      body: { id: category.id, name: category.name, performance_ratio: Number(category.performance_ratio), active: category.active }
    })
    if (!current()) return
    reportCategory(error, data)
  } finally {
    categorySaving[category.id] = false
  }
}

async function save(k: typeof kinds[number], o: LevelOrPosition) {
  if (saving[o.id]) return
  const current = actionGuard.hold()
  saving[o.id] = true
  try {
    const { data, error } = await api.POST(k.update, { body: { id: o.id, name: o.name, active: o.active } })
    if (!current()) return
    report(error, data)
  } finally {
    if (current()) saving[o.id] = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="grid gap-6 md:grid-cols-2">
      <section v-for="k in kinds" :key="k.key">
        <div class="sec">{{ k.title }}<span class="ms-2 font-normal text-dimmed">{{ items[k.key]?.length ?? 0 }}</span></div>
        <p class="mb-2 text-xs text-muted">{{ k.hint }}</p>
        <div class="overflow-x-auto">
          <div v-for="o in items[k.key]" :key="o.id" class="flex items-center gap-2 border-b border-default px-2 py-2 hover:bg-hover">
            <UInput v-model="o.name" variant="ghost" :aria-label="`${k.title}名称`" :disabled="saving[o.id]" class="min-w-0 flex-1" :class="o.active ? '' : 'opacity-60'" @change="save(k, o)" />
            <USwitch v-model="o.active" size="sm" :loading="saving[o.id]" :label="o.active ? '启用' : '停用'" @update:model-value="save(k, o)" />
          </div>
          <form class="flex gap-2 px-2 py-2" @submit.prevent="add(k)">
            <UInput v-model="draft[k.key]" variant="ghost" :placeholder="`新${k.title}`" :aria-label="`新${k.title}`" class="min-w-0 flex-1" />
            <UButton :loading="saving[k.key]" type="submit" size="sm" color="neutral" variant="ghost">添加</UButton>
          </form>
        </div>
      </section>
    </div>
    <section data-category="item_categories">
      <div class="sec">项目类别<span class="ms-2 font-normal text-dimmed">{{ categories.length }}</span></div>
      <p class="mb-2 text-xs text-muted">门店自己维护的类别，例如剪发、烫染、护理；每个类别有一个 0–100 的业绩比例</p>
      <div class="overflow-x-auto">
        <ListSkeleton v-if="categoryLoading" :rows="3" />
        <div v-for="category in categories" :key="category.id" data-category-row :data-category-name="category.name" class="flex flex-wrap items-center gap-2 border-b border-default px-2 py-2 hover:bg-hover">
          <UInput v-model="category.name" variant="ghost" :disabled="categorySaving[category.id] || !canManageSettings" :aria-label="`类别 ${category.name}`" class="min-w-0 flex-1 basis-40" :class="category.active ? '' : 'opacity-60'" @change="saveCategory(category)" />
          <label class="flex items-center gap-2 text-xs text-muted">
            业绩比例
            <UInput v-model.number="category.performance_ratio" type="number" min="0" max="100" class="w-20" :disabled="categorySaving[category.id] || !canManageSettings" :aria-label="`${category.name}业绩比例`" @change="saveCategory(category)" />
            %
          </label>
          <USwitch v-if="canManageSettings" v-model="category.active" size="sm" :loading="categorySaving[category.id]" :label="category.active ? '启用' : '停用'" @update:model-value="saveCategory(category)" />
          <span v-else-if="!category.active" class="tag bg-tag-gray">停用</span>
        </div>
        <form v-if="canManageSettings" class="flex flex-wrap items-center gap-2 px-2 py-2" @submit.prevent="addCategory">
          <UInput v-model="categoryDraft.name" variant="ghost" placeholder="新类别" aria-label="新类别" class="min-w-0 flex-1 basis-40" />
          <label class="flex items-center gap-2 text-xs text-muted">
            业绩比例
            <UInput v-model.number="categoryDraft.performance_ratio" type="number" min="0" max="100" class="w-20" aria-label="新类别业绩比例" />
            %
          </label>
          <UButton :loading="categoryAddSaving" type="submit" size="sm" color="neutral" variant="ghost">添加</UButton>
        </form>
      </div>
    </section>
  </div>
</template>
