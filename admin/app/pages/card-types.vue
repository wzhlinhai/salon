<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage' })
type CardType = components['schemas']['CardType']
const api = useApi()
const toast = useToast()
const items = ref<CardType[]>([])
const loading = ref(false)
const loadGuard = useRequestGuard()

async function load() {
  const current = loadGuard.next()
  loading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_card_types', { body: {} })
    if (!current()) return
    if (data) items.value = data.items
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) loading.value = false
  }
}

onMounted(load)
const editorGuard = useRequestGuard()
const open = ref(false)
const saving = ref(false)
const editing = ref<CardType | null>(null)
const name = ref('')
const active = ref(true)
const validity = ref<'none' | 'fixed_date' | 'days_after_open'>('none')
const date = ref('')
const days = ref(30)
const validityItems = [
  { label: '不限', value: 'none' },
  { label: '固定日期', value: 'fixed_date' },
  { label: '开卡后 N 天', value: 'days_after_open' }
]

function edit(item: CardType | null) {
  editorGuard.next()
  saving.value = false
  editing.value = item
  name.value = item?.name ?? ''
  active.value = item?.active ?? true
  validity.value = item?.validity_kind as typeof validity.value ?? 'none'
  date.value = item?.validity_date ?? ''
  days.value = item?.validity_days ?? 30
  open.value = true
}

watch(open, () => editorGuard.next())

async function save() {
  if (saving.value) return
  const current = editorGuard.hold()
  const body = {
    name: name.value, kind: 'stored_value' as const, validity_kind: validity.value,
    validity_date: validity.value === 'fixed_date' ? date.value : undefined,
    validity_days: validity.value === 'days_after_open' ? Number(days.value) : undefined
  }
  saving.value = true
  try {
    const { data, error } = editing.value
      ? await api.POST('/api/ops/update_card_type', { body: { ...body, id: editing.value.id, active: active.value } })
      : await api.POST('/api/ops/create_card_type', { body })
    if (!current()) return
    if (data) {
      open.value = false
      await load()
    } else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current() || !open.value) saving.value = false
  }
}

function validityText(item: CardType) {
  return item.validity_kind === 'fixed_date' ? item.validity_date : item.validity_kind === 'days_after_open' ? `开卡后 ${item.validity_days} 天` : '不限'
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex justify-between">
      <h1 class="text-xl font-semibold">卡类型</h1>
      <UButton @click="edit(null)">新建卡类型</UButton>
    </div>
    <p v-if="loading">加载中</p>
    <UCard v-for="item in items" :key="item.id" :data-card-type="item.name">
      <div class="flex justify-between items-center">
        <div>{{ item.name }} · 储值 · 有效期：{{ validityText(item) }} · {{ item.active ? '启用' : '停用' }}</div>
        <UButton variant="outline" @click="edit(item)">编辑</UButton>
      </div>
    </UCard>
    <UModal v-model:open="open" :title="editing ? '编辑卡类型' : '新建卡类型'">
      <template #body>
        <div class="space-y-3">
          <UFormField label="卡类型名称"><UInput v-model="name" class="w-full" /></UFormField>
          <UFormField label="有效期"><USelect v-model="validity" :items="validityItems" class="w-full" /></UFormField>
          <UFormField v-if="validity === 'fixed_date'" label="固定日期"><UInput v-model="date" type="date" /></UFormField>
          <UFormField v-if="validity === 'days_after_open'" label="天数"><UInput v-model="days" type="number" :min="1" /></UFormField>
          <USwitch v-if="editing" v-model="active" label="启用卡类型" />
        </div>
      </template>
      <template #footer><UButton :loading="saving" @click="save">保存</UButton></template>
    </UModal>
  </div>
</template>
