<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage', title: '项目与价格', description: '店里做的项目，各级别的价格和服务时长。开单和预约都从这里选。', art: 'scissors' })
type Item = components['schemas']['Item']
type Level = components['schemas']['Level']
type Position = components['schemas']['Position']
type ItemCategory = components['schemas']['ItemCategory']
const api = useApi()
const toast = useToast()
const items = ref<Item[]>([])
const levels = ref<Level[]>([])
const cardTypes = ref<components['schemas']['CardType'][]>([])
const positions = ref<Position[]>([])
const categories = ref<ItemCategory[]>([])
const loading = ref(false)
const loadGuard = useRequestGuard()

async function load() {
  const current = loadGuard.next()
  loading.value = true
  try {
    const [i, l, p, t, c] = await Promise.all([
      api.POST('/api/ops/list_items', { body: {} }),
      api.POST('/api/ops/list_levels', { body: {} }),
      api.POST('/api/ops/list_positions', { body: {} }),
      api.POST('/api/ops/list_card_types', { body: {} }),
      api.POST('/api/ops/list_item_categories', { body: {} })
    ])
    if (!current()) return
    if (!i.data || !l.data || !p.data || !t.data || !c.data) {
      toast.add({ title: errorText(i.error ?? l.error ?? p.error ?? t.error ?? c.error), color: 'error' })
      return
    }
    items.value = i.data.items
    levels.value = l.data.items
    positions.value = p.data.items
    cardTypes.value = t.data.items
    categories.value = c.data.items
  } finally {
    if (current()) loading.value = false
  }
}

onMounted(load)

const editor = useModalForm()
const { open, saving } = editor
const editing = ref<Item | null>(null)
const name = ref('')
const kind = ref<Item['kind']>('service')
const price = ref('0.00')
const discountable = ref(true)
const durationMinutes = ref('0')
const bookable = ref(true)
const active = ref(true)
const categoryId = ref('none')
const positionRows = ref<{ position_id: string, required: boolean, share_percent: string }[]>([])
const memberRows = ref<{ card_type_id: string, price: string }[]>([])
const memberOptions = computed(() => cardTypes.value.filter(t => t.active || editing.value?.member_prices.some(p => p.card_type_id === t.id)).map(t => ({ label: t.name + (t.active ? '' : '（停用）'), value: t.id })))
const levelRows = ref<{ level_id: string, price: string }[]>([])
const levelOptions = computed(() => levels.value.filter(l => l.active || editing.value?.level_prices.some(p => p.level_id === l.id)).map(l => ({ label: l.name + (l.active ? '' : '（停用）'), value: l.id })))
const categoryOptions = computed(() => [
  { label: '不归类', value: 'none' },
  ...categories.value.filter(c => c.active || c.id === categoryId.value).map(c => ({ label: c.name + (c.active ? '' : '（停用）'), value: c.id }))
])

function positionOptions(index: number) {
  return positions.value.filter(p => p.active || editing.value?.positions[index]?.position_id === p.id).map(p => ({ label: p.name + (p.active ? '' : '（停用）'), value: p.id }))
}

function openEditor(item: Item | null) {
  editor.begin(() => {
    editing.value = item
    name.value = item?.name ?? ''
    kind.value = item?.kind ?? 'service'
    price.value = item?.price ?? '0.00'
    discountable.value = item?.discountable ?? true
    durationMinutes.value = String(item?.duration_minutes ?? 0)
    bookable.value = item?.bookable ?? true
    active.value = item?.active ?? true
    categoryId.value = item?.category?.id ?? 'none'
    positionRows.value = item?.positions.map(p => ({ position_id: p.position_id, required: p.required, share_percent: p.share_percent == null ? '' : String(p.share_percent) })) ?? []
    memberRows.value = item?.member_prices.map(m => ({ ...m })) ?? []
    levelRows.value = item?.level_prices.map(l => ({ ...l })) ?? []
  })
}

function move(index: number, offset: number) {
  const rows = positionRows.value
  const target = index + offset
  if (target < 0 || target >= rows.length) return
  ;[rows[index], rows[target]] = [rows[target]!, rows[index]!]
}

async function save() {
  const body = {
    name: name.value, kind: kind.value, price: price.value, discountable: discountable.value,
    duration_minutes: Number(durationMinutes.value), bookable: bookable.value,
    category_id: categoryId.value === 'none' ? null : categoryId.value,
    // 手工费本店不用（美管加里也没有），界面不显示；编辑时原样带回已有的值，新建为无。
    labor_fee_amount: editing.value?.labor_fee_amount ?? null,
    labor_fee_percent: editing.value?.labor_fee_percent ?? null,
    positions: positionRows.value.map(p => ({ ...p, share_percent: p.share_percent === '' ? null : Number(p.share_percent) })),
    member_prices: memberRows.value.map(m => ({ ...m })),
    level_prices: levelRows.value.map(l => ({ ...l }))
  }
  await editor.save(async (current) => {
    const result = editing.value
      ? await api.POST('/api/ops/update_item', { body: { ...body, id: editing.value.id, active: active.value } })
      : await api.POST('/api/ops/create_item', { body })
    if (!current()) return
    if (result.data) {
      open.value = false
      toast.add({ title: '已保存', color: 'success' })
      await load()
    } else toast.add({ title: errorText(result.error), color: 'error' })
  })
}

// 照参考图 fresha 服务菜单：按类别分组，类别按设置里的顺序，未归类放最后。
const groups = computed(() => {
  const order = new Map(categories.value.map((c, index) => [c.id, index]))
  const map = new Map<string, { key: string, name: string, rank: number, items: Item[] }>()
  for (const item of items.value) {
    const key = item.category?.id ?? 'none'
    if (!map.has(key)) map.set(key, { key, name: item.category?.name ?? '未归类', rank: order.get(key) ?? Number.MAX_SAFE_INTEGER, items: [] })
    map.get(key)!.items.push(item)
  }
  return [...map.values()].sort((a, b) => a.rank - b.rank)
})
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton size="sm" icon="i-lucide-plus" @click="openEditor(null)">新建项目</UButton>
    </PageActions>
    <ListSkeleton v-if="loading && !items.length" />
    <EmptyState v-else-if="!items.length" art="scissors" title="还没有项目" hint="点右上角新建项目，填上价格和服务时长，开单和预约就能选它" />
    <section v-for="group in groups" :key="group.key" :data-category="group.name">
      <div class="sec">{{ group.name }}<span class="ms-2 font-normal text-dimmed">{{ group.items.length }}</span></div>
      <div class="overflow-x-auto">
        <div
          v-for="item in group.items"
          :key="item.id"
          :data-item="item.name"
          class="group/row flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-default px-3 py-2 text-sm first:border-t-0 hover:bg-hover"
          :class="item.active ? '' : 'text-dimmed'"
        >
          <div class="flex min-w-40 flex-1 items-center gap-2">
            <span class="font-medium">{{ item.name }}</span>
            <span v-if="item.kind === 'goods'" class="tag bg-tag-gray">卖品</span>
            <span v-if="!item.discountable" class="tag bg-tag-gray">不打折项目</span>
            <span v-if="!item.active" class="tag bg-tag-gray">停用</span>
          </div>
          <span class="w-36 text-muted">{{ item.duration_minutes ? `${item.duration_minutes} 分钟` : '时长未设置' }} · {{ item.bookable ? '可预约' : '不可预约' }}</span>
          <span class="w-24 text-right font-medium tabular-nums">{{ yuanFixed(item.price) }}</span>
          <UButton size="sm" color="neutral" variant="ghost" class="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100" @click="openEditor(item)">编辑</UButton>
        </div>
      </div>
    </section>
    <UModal v-model:open="open" :title="editing ? '编辑项目' : '新建项目'" description="开单和预约按这里的价格、时长和岗位来" :ui="{ content: 'sm:max-w-[600px]' }">
      <template #body>
        <div class="space-y-5">
          <UFormField label="项目名称"><UInput v-model="name" aria-label="项目名称" :maxlength="40" /></UFormField>
          <div class="grid grid-cols-2 gap-3">
            <UFormField label="种类"><USelect v-model="kind" aria-label="种类" :items="[{ label: '服务', value: 'service' }, { label: '卖品', value: 'goods' }]" /></UFormField>
            <UFormField label="类别"><USelect v-model="categoryId" aria-label="类别" :items="categoryOptions" /></UFormField>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <UFormField label="标准价"><UInput v-model="price" aria-label="标准价" inputmode="decimal" /></UFormField>
            <UFormField label="服务时长（分钟）" help="0 表示未设置，最多 600"><UInput v-model="durationMinutes" type="number" min="0" max="600" aria-label="服务时长（分钟）" /></UFormField>
          </div>
          <div class="flex items-center gap-6">
            <USwitch v-model="bookable" label="可以预约" />
            <USwitch v-model="discountable" label="可以打折" />
          </div>
          <FieldGroup label="项目岗位">
            <div v-for="(row, index) in positionRows" :key="index" class="flex items-center gap-2" data-testid="position-row">
              <USelect v-model="row.position_id" :aria-label="`岗位${index + 1}`" :items="positionOptions(index)" placeholder="选择岗位" class="min-w-0 flex-1" />
              <UCheckbox v-model="row.required" label="必填" />
              <UInput v-model="row.share_percent" type="number" min="0" max="100" placeholder="比例 %" class="w-24" :aria-label="`岗位${index + 1}比例`" />
              <UButton :disabled="index === 0" icon="i-lucide-arrow-up" color="neutral" variant="ghost" aria-label="上移" @click="move(index, -1)" />
              <UButton :disabled="index === positionRows.length - 1" icon="i-lucide-arrow-down" color="neutral" variant="ghost" aria-label="下移" @click="move(index, 1)" />
              <UButton color="error" variant="ghost" icon="i-lucide-x" aria-label="移除岗位" @click="positionRows.splice(index, 1)" />
            </div>
            <UButton color="neutral" icon="i-lucide-plus" class="-ms-2 self-start" variant="ghost" @click="positionRows.push({ position_id: positions.find(p => p.active)?.id ?? '', required: false, share_percent: '' })">添加岗位</UButton>
          </FieldGroup>
          <FieldGroup label="级别价">
            <div v-for="(row, index) in levelRows" :key="index" class="flex items-center gap-2" data-testid="level-row">
              <USelect v-model="row.level_id" :aria-label="`级别${index + 1}`" :items="levelOptions" placeholder="选择级别" class="min-w-0 flex-1" />
              <UInput v-model="row.price" :aria-label="`级别价${index + 1}`" inputmode="decimal" class="w-32" />
              <UButton color="error" variant="ghost" icon="i-lucide-x" aria-label="移除级别价" @click="levelRows.splice(index, 1)" />
            </div>
            <UButton color="neutral" icon="i-lucide-plus" class="-ms-2 self-start" variant="ghost" @click="levelRows.push({ level_id: levels.find(l => l.active)?.id ?? '', price: '0.00' })">添加级别价</UButton>
          </FieldGroup>
          <FieldGroup label="会员价">
            <div v-for="(row, index) in memberRows" :key="index" class="flex items-center gap-2" data-testid="member-row">
              <USelect v-model="row.card_type_id" :aria-label="`卡类型${index + 1}`" :items="memberOptions" placeholder="选择卡类型" class="min-w-0 flex-1" />
              <UInput v-model="row.price" :aria-label="`会员价${index + 1}`" inputmode="decimal" class="w-32" />
              <UButton color="error" variant="ghost" icon="i-lucide-x" aria-label="移除会员价" @click="memberRows.splice(index, 1)" />
            </div>
            <UButton color="neutral" icon="i-lucide-plus" class="-ms-2 self-start" variant="ghost" @click="memberRows.push({ card_type_id: cardTypes.find(t => t.active)?.id ?? '', price: '0.00' })">添加会员价</UButton>
          </FieldGroup>
          <USwitch v-if="editing" v-model="active" label="启用项目" />
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="open = false">
          <UButton :loading="saving" @click="save">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
