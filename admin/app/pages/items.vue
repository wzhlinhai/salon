<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage' })
type Item = components['schemas']['Item']
type Level = components['schemas']['Level']
type Position = components['schemas']['Position']
const api = useApi()
const toast = useToast()
const items = ref<Item[]>([])
const levels = ref<Level[]>([])
const positions = ref<Position[]>([])
const loading = ref(false)
const loadGuard = useRequestGuard()

async function load() {
  const current = loadGuard.next()
  loading.value = true
  try {
    const [i, l, p] = await Promise.all([
      api.POST('/api/ops/list_items', { body: {} }),
      api.POST('/api/ops/list_levels', { body: {} }),
      api.POST('/api/ops/list_positions', { body: {} })
    ])
    if (!current()) return
    if (!i.data || !l.data || !p.data) {
      toast.add({ title: errorText(i.error ?? l.error ?? p.error), color: 'error' })
      return
    }
    items.value = i.data.items
    levels.value = l.data.items
    positions.value = p.data.items
  } finally {
    if (current()) loading.value = false
  }
}

onMounted(load)

const editorGuard = useRequestGuard()
const open = ref(false)
const editing = ref<Item | null>(null)
const saving = ref(false)
const name = ref('')
const price = ref('0.00')
const discountable = ref(true)
const active = ref(true)
const feeKind = ref('none')
const feeValue = ref('0.00')
const positionRows = ref<{ position_id: string, required: boolean }[]>([])
const levelRows = ref<{ level_id: string, price: string }[]>([])
const levelOptions = computed(() => levels.value.filter(l => l.active || editing.value?.level_prices.some(p => p.level_id === l.id)).map(l => ({ label: l.name + (l.active ? '' : '（停用）'), value: l.id })))

function positionOptions(index: number) {
  return positions.value.filter(p => p.active || editing.value?.positions[index]?.position_id === p.id).map(p => ({ label: p.name + (p.active ? '' : '（停用）'), value: p.id }))
}

function openEditor(item: Item | null) {
  editorGuard.next()
  saving.value = false
  editing.value = item
  name.value = item?.name ?? ''
  price.value = item?.price ?? '0.00'
  discountable.value = item?.discountable ?? true
  active.value = item?.active ?? true
  feeKind.value = item?.labor_fee_amount != null ? 'amount' : item?.labor_fee_percent != null ? 'percent' : 'none'
  feeValue.value = item?.labor_fee_amount ?? item?.labor_fee_percent ?? '0.00'
  positionRows.value = item?.positions.map(p => ({ position_id: p.position_id, required: p.required })) ?? []
  levelRows.value = item?.level_prices.map(l => ({ ...l })) ?? []
  open.value = true
}

watch(open, (value) => {
  if (!value) editorGuard.next()
})

function move(index: number, offset: number) {
  const rows = positionRows.value
  const target = index + offset
  if (target < 0 || target >= rows.length) return
  ;[rows[index], rows[target]] = [rows[target]!, rows[index]!]
}

async function save() {
  if (saving.value) return
  const current = editorGuard.hold()
  saving.value = true
  const body = {
    name: name.value, price: price.value, discountable: discountable.value,
    labor_fee_amount: feeKind.value === 'amount' ? feeValue.value : null,
    labor_fee_percent: feeKind.value === 'percent' ? feeValue.value : null,
    positions: positionRows.value.map(p => ({ ...p })),
    level_prices: levelRows.value.map(l => ({ ...l }))
  }
  try {
    const result = editing.value
      ? await api.POST('/api/ops/update_item', { body: { ...body, id: editing.value.id, active: active.value } })
      : await api.POST('/api/ops/create_item', { body })
    if (!current()) return
    if (result.data) {
      open.value = false
      toast.add({ title: '已保存', color: 'success' })
      await load()
    } else toast.add({ title: errorText(result.error), color: 'error' })
  } finally {
    if (current() || !open.value) saving.value = false
  }
}

function laborFee(item: Item) {
  if (item.labor_fee_amount != null) return `固定 ${item.labor_fee_amount} 元`
  if (item.labor_fee_percent != null) return `售价的 ${item.labor_fee_percent}%`
  return '无'
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold">项目与价格</h1>
      <UButton @click="openEditor(null)">新建项目</UButton>
    </div>
    <p v-if="loading">加载中</p>
    <UCard v-for="item in items" :key="item.id" :data-item="item.name">
      <div class="flex items-center justify-between gap-3">
        <div>
          <span class="font-medium">{{ item.name }}</span>
          <UBadge v-if="!item.discountable" class="ml-2">不打折项目</UBadge>
          <UBadge class="ml-2" color="neutral">{{ item.active ? '启用' : '停用' }}</UBadge>
          <p>标准价 {{ item.price }} 元 · 手工费 {{ laborFee(item) }}</p>
        </div>
        <UButton variant="ghost" @click="openEditor(item)">编辑</UButton>
      </div>
    </UCard>
    <UModal v-model:open="open" :title="editing ? '编辑项目' : '新建项目'">
      <template #body>
        <div class="space-y-4">
          <UFormField label="项目名称"><UInput v-model="name" aria-label="项目名称" :maxlength="40" /></UFormField>
          <UFormField label="标准价"><UInput v-model="price" aria-label="标准价" inputmode="decimal" /></UFormField>
          <USwitch v-model="discountable" label="可以打折" />
          <UFormField label="手工费">
            <USelect v-model="feeKind" aria-label="手工费类型" :items="[{ label: '无', value: 'none' }, { label: '固定金额', value: 'amount' }, { label: '售价百分比', value: 'percent' }]" />
            <UInput v-if="feeKind !== 'none'" v-model="feeValue" aria-label="手工费数值" inputmode="decimal" class="ml-2" />
          </UFormField>
          <div class="space-y-2">
            <h2 class="font-medium">项目岗位</h2>
            <div v-for="(row, index) in positionRows" :key="index" class="flex flex-wrap gap-2" data-testid="position-row">
              <USelect v-model="row.position_id" :aria-label="`岗位${index + 1}`" :items="positionOptions(index)" placeholder="选择岗位" />
              <UCheckbox v-model="row.required" label="必填" />
              <UButton :disabled="index === 0" aria-label="上移" @click="move(index, -1)">↑</UButton>
              <UButton :disabled="index === positionRows.length - 1" aria-label="下移" @click="move(index, 1)">↓</UButton>
              <UButton color="error" @click="positionRows.splice(index, 1)">移除岗位</UButton>
            </div>
            <UButton variant="outline" @click="positionRows.push({ position_id: positions.find(p => p.active)?.id ?? '', required: false })">添加岗位</UButton>
          </div>
          <div class="space-y-2">
            <h2 class="font-medium">级别价</h2>
            <div v-for="(row, index) in levelRows" :key="index" class="flex gap-2" data-testid="level-row">
              <USelect v-model="row.level_id" :aria-label="`级别${index + 1}`" :items="levelOptions" placeholder="选择级别" />
              <UInput v-model="row.price" :aria-label="`级别价${index + 1}`" inputmode="decimal" />
              <UButton color="error" @click="levelRows.splice(index, 1)">移除级别价</UButton>
            </div>
            <UButton variant="outline" @click="levelRows.push({ level_id: levels.find(l => l.active)?.id ?? '', price: '0.00' })">添加级别价</UButton>
          </div>
          <USwitch v-if="editing" v-model="active" label="启用项目" />
        </div>
      </template>
      <template #footer><UButton :loading="saving" @click="save">保存</UButton></template>
    </UModal>
  </div>
</template>
