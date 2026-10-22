<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage' })
type CardType = components['schemas']['CardType']
const api = useApi()
const toast = useToast()
const items = ref<CardType[]>([])
const upgradeRules = ref<components['schemas']['CardUpgradeRule'][]>([])
const catalog = ref<components['schemas']['Item'][]>([])
const kind = ref<'stored_value' | 'count' | 'package'>('stored_value')
const kindItems = [{ label: '储值卡', value: 'stored_value' }, { label: '次卡', value: 'count' }, { label: '套餐', value: 'package' }]
const definition = ref<components['schemas']['CardTypeItemInput'][]>([])
// 每项最多 9999 次、单次金额最多 99999999.99 元，最多 20 项的分值总和仍小于 2^53，Number 可精确累加。
const countsPrice = computed(() => definition.value.reduce((sum, x) => sum + Math.round(Number(x.unit_price) * 100) * Number(x.paid_count), 0) / 100)
const loading = ref(false)
const loadGuard = useRequestGuard()

async function load() {
  const current = loadGuard.next()
  loading.value = true
  try {
    const [types, projects, rules] = await Promise.all([
      api.POST('/api/ops/list_card_types', { body: {} }),
      api.POST('/api/ops/list_items', { body: {} }),
      api.POST('/api/ops/list_card_upgrade_rules', { body: {} })
    ])
    const { data, error } = types
    if (!current()) return
    if (rules.data) upgradeRules.value = rules.data.items
    else toast.add({ title: errorText(rules.error), color: 'error' })
    if (projects.data) catalog.value = projects.data.items
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
  kind.value = item?.kind as typeof kind.value ?? 'stored_value'
  definition.value = item?.items.map(x => ({ item_id: x.item_id, paid_count: x.paid_count, gift_count: x.gift_count, unit_price: x.unit_price })) ?? []
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
    name: name.value, kind: kind.value, validity_kind: validity.value,
    items: kind.value === 'stored_value' ? [] : definition.value.map(x => ({ ...x, paid_count: Number(x.paid_count), gift_count: Number(x.gift_count) })),
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

const ruleGuard = useRequestGuard()
const ruleOpen = ref(false)
const ruleSaving = ref(false)
const ruleEditing = ref<components['schemas']['CardUpgradeRule'] | null>(null)
const ruleFrom = ref('')
const ruleTo = ref('')
const ruleCondition = ref<'single_recharge' | 'total_recharge'>('single_recharge')
const ruleThreshold = ref('0.00')
const ruleActive = ref(true)
const ruleConditions = [{ label: '单次充值本金 ≥', value: 'single_recharge' }, { label: '累计充值本金 ≥', value: 'total_recharge' }]
const storedTypes = computed(() => items.value.filter(t => t.kind === 'stored_value'))

function editRule(rule: components['schemas']['CardUpgradeRule'] | null) {
  ruleGuard.next()
  ruleSaving.value = false
  ruleEditing.value = rule
  ruleFrom.value = rule?.from_type_id ?? ''
  ruleTo.value = rule?.to_type_id ?? ''
  ruleCondition.value = rule?.condition_kind ?? 'single_recharge'
  ruleThreshold.value = rule?.threshold ?? '0.00'
  ruleActive.value = rule?.active ?? true
  ruleOpen.value = true
}

watch(ruleOpen, () => ruleGuard.next())

async function saveRule() {
  if (ruleSaving.value) return
  const current = ruleGuard.hold()
  ruleSaving.value = true
  try {
    const { data, error } = ruleEditing.value
      ? await api.POST('/api/ops/update_card_upgrade_rule', { body: { id: ruleEditing.value.id, threshold: ruleThreshold.value, active: ruleActive.value } })
      : await api.POST('/api/ops/create_card_upgrade_rule', { body: { from_type_id: ruleFrom.value, to_type_id: ruleTo.value, condition_kind: ruleCondition.value, threshold: ruleThreshold.value } })
    if (!current()) return
    if (data) {
      ruleOpen.value = false
      await load()
    } else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current() || !ruleOpen.value) ruleSaving.value = false
  }
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
        <div>{{ item.name }} · {{ kindItems.find(k => k.value === item.kind)?.label }} · 有效期：{{ validityText(item) }} · {{ item.active ? '启用' : '停用' }}</div>
        <UButton variant="outline" @click="edit(item)">编辑</UButton>
      </div>
    </UCard>
    <section class="space-y-3" aria-label="升级规则">
      <div class="flex justify-between"><h2 class="text-lg font-semibold">升级规则</h2><UButton @click="editRule(null)">新建升级规则</UButton></div>
      <UCard v-for="rule in upgradeRules" :key="rule.id" data-testid="upgrade-rule">
        <div class="flex justify-between items-center">
          <p>{{ rule.from_type_name }} → {{ rule.to_type_name }} · {{ ruleConditions.find(c => c.value === rule.condition_kind)?.label }} {{ rule.threshold }} · {{ rule.active ? '启用' : '停用' }}</p>
          <UButton variant="outline" @click="editRule(rule)">编辑升级规则</UButton>
        </div>
      </UCard>
    </section>
    <UModal v-model:open="ruleOpen" :title="ruleEditing ? '编辑升级规则' : '新建升级规则'">
      <template #body>
        <div class="space-y-3">
          <UFormField label="原卡类型"><USelect v-model="ruleFrom" :disabled="!!ruleEditing" :items="storedTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
          <UFormField label="目标卡类型"><USelect v-model="ruleTo" :disabled="!!ruleEditing" :items="storedTypes.filter(t => t.active && t.id !== ruleFrom).map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
          <UFormField label="条件"><USelect v-model="ruleCondition" :disabled="!!ruleEditing" :items="ruleConditions" class="w-full" /></UFormField>
          <UFormField label="门槛"><UInput v-model="ruleThreshold" inputmode="decimal" class="w-full" /></UFormField>
          <USwitch v-if="ruleEditing" v-model="ruleActive" label="启用升级规则" />
        </div>
      </template>
      <template #footer><UButton :loading="ruleSaving" :disabled="!ruleFrom || !ruleTo" @click="saveRule">保存</UButton></template>
    </UModal>
    <UModal v-model:open="open" :title="editing ? '编辑卡类型' : '新建卡类型'">
      <template #body>
        <div class="space-y-3">
          <UFormField label="卡类型名称"><UInput v-model="name" class="w-full" /></UFormField>
          <UFormField label="种类"><USelect v-model="kind" :items="kindItems" class="w-full" /></UFormField>
          <div v-if="kind !== 'stored_value'" class="space-y-3">
            <div v-for="(row, index) in definition" :key="index" data-testid="card-type-item" class="space-y-2">
              <UFormField label="项目"><USelect v-model="row.item_id" :items="catalog.filter(i => i.active || definition.some(x => x.item_id === i.id)).map(i => ({ label: i.name, value: i.id }))" class="w-full" /></UFormField>
              <UFormField label="购买次数"><UInput v-model="row.paid_count" type="number" :min="1" /></UFormField>
              <UFormField label="赠送次数"><UInput v-model="row.gift_count" type="number" :min="0" /></UFormField>
              <UFormField label="单次金额"><UInput v-model="row.unit_price" inputmode="decimal" /></UFormField>
              <UButton variant="outline" @click="definition.splice(index, 1)">移除项目</UButton>
            </div>
            <UButton :disabled="definition.length >= 20" variant="outline" @click="definition.push({ item_id: '', paid_count: 1, gift_count: 0, unit_price: '0.00' })">添加项目</UButton>
            <p>次数售价 {{ countsPrice.toFixed(2) }}</p>
            <p>编辑项目只影响以后开卡，已开卡保留原次数和单次金额。</p>
          </div>
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
