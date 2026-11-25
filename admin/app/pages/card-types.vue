<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage', title: '卡类型', art: 'card' })
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
const editor = useModalForm()
const { open, saving } = editor
const editing = ref<CardType | null>(null)
const name = ref('')
const active = ref(true)
const discount = ref('')
const validity = ref<'none' | 'fixed_date' | 'days_after_open'>('none')
const date = ref('')
const days = ref(30)
const validityItems = [
  { label: '不限', value: 'none' },
  { label: '固定日期', value: 'fixed_date' },
  { label: '开卡后 N 天', value: 'days_after_open' }
]

function edit(item: CardType | null) {
  editor.begin(() => {
    editing.value = item
    name.value = item?.name ?? ''
    discount.value = item?.discount_percent ? String(Number(item.discount_percent) / 10) : ''
    kind.value = item?.kind as typeof kind.value ?? 'stored_value'
    definition.value = item?.items.map(x => ({ item_id: x.item_id, paid_count: x.paid_count, gift_count: x.gift_count, unit_price: x.unit_price })) ?? []
    active.value = item?.active ?? true
    validity.value = item?.validity_kind as typeof validity.value ?? 'none'
    date.value = item?.validity_date ?? ''
    days.value = item?.validity_days ?? 30
  })
}

async function save() {
  if (kind.value !== 'count' && discount.value !== '' && (!/^[0-9](\.[0-9])?$/.test(String(discount.value)) || Number(discount.value) <= 0)) {
    toast.add({ title: '折扣率须大于 0 折、小于 10 折，最多一位小数', color: 'error' })
    return
  }
  const body = {
    name: name.value, kind: kind.value, validity_kind: validity.value,
    discount_percent: kind.value === 'count' || discount.value === '' ? null : (Number(discount.value) * 10).toFixed(2),
    items: kind.value === 'stored_value' ? [] : definition.value.map(x => ({ ...x, paid_count: Number(x.paid_count), gift_count: Number(x.gift_count) })),
    validity_date: validity.value === 'fixed_date' ? date.value : undefined,
    validity_days: validity.value === 'days_after_open' ? Number(days.value) : undefined
  }
  await editor.save(async (current) => {
    const { data, error } = editing.value
      ? await api.POST('/api/ops/update_card_type', { body: { ...body, id: editing.value.id, active: active.value } })
      : await api.POST('/api/ops/create_card_type', { body })
    if (!current()) return
    if (data) {
      open.value = false
      await load()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

function validityText(item: CardType) {
  return item.validity_kind === 'fixed_date' ? item.validity_date : item.validity_kind === 'days_after_open' ? `开卡后 ${item.validity_days} 天` : '不限'
}

const ruleEditor = useModalForm()
const { open: ruleOpen, saving: ruleSaving } = ruleEditor
const ruleEditing = ref<components['schemas']['CardUpgradeRule'] | null>(null)
const ruleFrom = ref('')
const ruleTo = ref('')
const ruleCondition = ref<'single_recharge' | 'total_recharge'>('single_recharge')
const ruleThreshold = ref('0.00')
const ruleActive = ref(true)
const ruleConditions = [{ label: '单次充值本金 ≥', value: 'single_recharge' }, { label: '累计充值本金 ≥', value: 'total_recharge' }]
const storedTypes = computed(() => items.value.filter(t => t.kind === 'stored_value'))

function editRule(rule: components['schemas']['CardUpgradeRule'] | null) {
  ruleEditor.begin(() => {
    ruleEditing.value = rule
    ruleFrom.value = rule?.from_type_id ?? ''
    ruleTo.value = rule?.to_type_id ?? ''
    ruleCondition.value = rule?.condition_kind ?? 'single_recharge'
    ruleThreshold.value = rule?.threshold ?? '0.00'
    ruleActive.value = rule?.active ?? true
  })
}

async function saveRule() {
  await ruleEditor.save(async (current) => {
    const { data, error } = ruleEditing.value
      ? await api.POST('/api/ops/update_card_upgrade_rule', { body: { id: ruleEditing.value.id, threshold: ruleThreshold.value, active: ruleActive.value } })
      : await api.POST('/api/ops/create_card_upgrade_rule', { body: { from_type_id: ruleFrom.value, to_type_id: ruleTo.value, condition_kind: ruleCondition.value, threshold: ruleThreshold.value } })
    if (!current()) return
    if (data) {
      ruleOpen.value = false
      await load()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton size="sm" icon="i-lucide-plus" @click="edit(null)">新建卡类型</UButton>
    </PageActions>
    <section aria-label="卡类型">
      <div v-if="items.length" class="sec mt-0!"><span class="font-normal text-dimmed">{{ items.length }} 种</span></div>
      <div class="overflow-x-auto">
        <ListSkeleton v-if="loading && !items.length" :rows="3" />
        <EmptyState v-else-if="!items.length" art="card" title="还没有卡类型" hint="点右上角新建储值卡、次卡或套餐" />
        <div
          v-for="item in items"
          :key="item.id"
          :data-card-type="item.name"
          class="group/row flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-default px-3 py-2 text-sm first:border-t-0 hover:bg-hover"
          :class="item.active ? '' : 'text-dimmed'"
        >
          <div class="flex min-w-40 flex-1 items-center gap-2">
            <span class="font-medium">{{ item.name }}</span>
            <span class="tag bg-tag-gray">{{ kindItems.find(k => k.value === item.kind)?.label }}</span>
            <span v-if="!item.active" class="tag bg-tag-gray">停用</span>
          </div>
          <span class="w-24 text-muted">{{ item.discount_percent ? `${Number(item.discount_percent) / 10} 折` : '不打折' }}</span>
          <span class="w-44 text-muted">有效期：{{ validityText(item) }}</span>
          <UButton size="sm" color="neutral" variant="ghost" class="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100" @click="edit(item)">编辑</UButton>
        </div>
      </div>
    </section>
    <section aria-label="升级规则">
      <div class="sec">
        升级规则<span class="ms-2 font-normal text-dimmed">{{ upgradeRules.length }}</span>
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-plus" class="ms-auto" @click="editRule(null)">新建升级规则</UButton>
      </div>
      <div class="overflow-x-auto">
        <p v-if="!loading && !upgradeRules.length" class="px-3 py-2 text-sm text-muted">还没有升级规则。储值卡累计充值或消费到门槛后，可以自动升到更高的卡。</p>
        <div
          v-for="rule in upgradeRules"
          :key="rule.id"
          data-testid="upgrade-rule"
          class="group/row flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-default px-3 py-2 text-sm first:border-t-0 hover:bg-hover"
          :class="rule.active ? '' : 'text-dimmed'"
        >
          <span class="flex-1 font-medium">{{ rule.from_type_name }} → {{ rule.to_type_name }}</span>
          <span class="text-muted tabular-nums">{{ ruleConditions.find(c => c.value === rule.condition_kind)?.label }} {{ rule.threshold }}</span>
          <span v-if="!rule.active" class="tag bg-tag-gray">停用</span>
          <UButton size="sm" color="neutral" variant="ghost" class="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100" @click="editRule(rule)">编辑升级规则</UButton>
        </div>
      </div>
    </section>
    <UModal v-model:open="ruleOpen" :title="ruleEditing ? '编辑升级规则' : '新建升级规则'">
      <template #body>
        <div class="space-y-5">
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="原卡类型"><USelect v-model="ruleFrom" :disabled="!!ruleEditing" :items="storedTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
            <UFormField label="目标卡类型"><USelect v-model="ruleTo" :disabled="!!ruleEditing" :items="storedTypes.filter(t => t.active && t.id !== ruleFrom).map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
          </div>
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="条件"><USelect v-model="ruleCondition" :disabled="!!ruleEditing" :items="ruleConditions" class="w-full" /></UFormField>
            <UFormField label="门槛"><UInput v-model="ruleThreshold" inputmode="decimal" class="w-full" /></UFormField>
          </div>
          <USwitch v-if="ruleEditing" v-model="ruleActive" label="启用升级规则" />
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="ruleOpen = false">
          <UButton :loading="ruleSaving" :disabled="!ruleFrom || !ruleTo" @click="saveRule">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-model:open="open" :title="editing ? '编辑卡类型' : '新建卡类型'">
      <template #body>
        <div class="space-y-5">
          <UFormField label="卡类型名称"><UInput v-model="name" class="w-full" /></UFormField>
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="种类"><USelect v-model="kind" :items="kindItems" class="w-full" /></UFormField>
            <UFormField v-if="kind !== 'count'" label="折扣率" help="以折填写，留空为不打折"><UInput v-model="discount" type="number" :min="0.1" :max="9.9" :step="0.1" placeholder="不打折" class="w-full" /></UFormField>
          </div>
          <div v-if="kind !== 'stored_value'" class="space-y-3">
            <div v-for="(row, index) in definition" :key="index" data-testid="card-type-item" class="space-y-2 rounded-card bg-side p-3">
              <UFormField label="项目"><USelect v-model="row.item_id" :items="catalog.filter(i => i.active || definition.some(x => x.item_id === i.id)).map(i => ({ label: i.name, value: i.id }))" class="w-full" /></UFormField>
              <div class="grid grid-cols-3 gap-3">
                <UFormField label="购买次数"><UInput v-model="row.paid_count" type="number" :min="1" /></UFormField>
                <UFormField label="赠送次数"><UInput v-model="row.gift_count" type="number" :min="0" /></UFormField>
                <UFormField label="单次金额"><UInput v-model="row.unit_price" inputmode="decimal" /></UFormField>
              </div>
              <UButton color="error" variant="ghost" class="-ms-2" @click="definition.splice(index, 1)">移除项目</UButton>
            </div>
            <UButton color="neutral" icon="i-lucide-plus" class="-ms-2 self-start" :disabled="definition.length >= 20" variant="ghost" @click="definition.push({ item_id: '', paid_count: 1, gift_count: 0, unit_price: '0.00' })">添加项目</UButton>
            <p class="text-meta text-muted">次数售价 {{ countsPrice.toFixed(2) }}。编辑项目只影响以后开卡，已开卡保留原次数和单次金额。</p>
          </div>
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="有效期"><USelect v-model="validity" :items="validityItems" class="w-full" /></UFormField>
            <UFormField v-if="validity === 'fixed_date'" label="固定日期"><DateInput v-model="date" /></UFormField>
            <UFormField v-if="validity === 'days_after_open'" label="天数"><UInput v-model="days" type="number" :min="1" /></UFormField>
          </div>
          <USwitch v-if="editing" v-model="active" label="启用卡类型" />
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
