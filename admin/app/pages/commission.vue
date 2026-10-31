<script setup lang="ts">
import type { components } from '~/utils/api'
import type { CommissionValue } from '~/components/CommissionValueEditor.vue'
import { LOG_PAGE_SIZE } from '~/utils/paging'

definePageMeta({ permission: 'settings.manage' })
type Schema = components['schemas']
type Source = Schema['Rule']['source']
type Cell = Schema['Cell']
type View = Schema['CommissionSchemeVersionView']
interface GridCell { key: string, dimensions: Cell, value: CommissionValue }
interface RuleForm extends Omit<Schema['Rule'], 'cells'> { enabled: boolean, cells: GridCell[] }
interface OverrideForm { source: Source, staff_id: string, item_id: string, value: CommissionValue }
const sources: { value: Source, label: string }[] = [
  { value: 'item', label: '项目业绩' }, { value: 'consume', label: '耗卡业绩' },
  { value: 'labor', label: '手工费' }, { value: 'sale', label: '销售业绩' }
]
const bases = [
  { value: 'line_price', label: '项目行价格（原价）' }, { value: 'paid', label: '实收（折后价）' },
  { value: 'performance', label: '业绩' }, { value: 'cash_performance', label: '现金业绩' }, { value: 'card_performance', label: '卡金业绩' }
]
const api = useApi()
const toast = useToast()
const today = ref('')
const inForce = ref<number>()
const selected = ref<number>()
const detail = ref<View>()
const detailLoading = ref(false)
let selectionRevision = 0
const detailGuard = useRequestGuard()
const todayGuard = useRequestGuard()
const versions = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_commission_scheme_versions', { body: { before_version: beforeId ? Number(beforeId) : undefined, limit: LOG_PAGE_SIZE } })
  return { data: data?.versions.map(v => ({ ...v, id: String(v.version) })), error }
})

async function selectVersion(version: number) {
  selectionRevision++
  selected.value = version
  detail.value = undefined
  const current = detailGuard.next()
  detailLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/get_commission_scheme', { body: { version } })
    if (!current()) return
    if (data) detail.value = data
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) detailLoading.value = false
  }
}

async function reload(version?: number) {
  const selectionAtStart = selectionRevision
  const current = todayGuard.next()
  const [settings] = await Promise.all([
    api.POST('/api/ops/get_settings', { body: {} }), versions.reload()
  ])
  if (!current()) return
  if (!settings.data) {
    toast.add({ title: errorText(settings.error), color: 'error' })
    return
  }
  today.value = settings.data.business_date
  const active = await api.POST('/api/ops/get_commission_scheme', { body: { date: today.value } })
  if (!current()) return
  if (active.error && active.response.status !== 404) toast.add({ title: errorText(active.error), color: 'error' })
  inForce.value = active.data?.version
  if (version === undefined && selectionAtStart !== selectionRevision) return
  const target = version ?? inForce.value ?? versions.items.value[0]?.version
  if (target !== undefined) await selectVersion(target)
}

onMounted(() => reload())

function valueText(value: Cell) {
  if (value.percent !== undefined) return `${Number(value.percent).toFixed(2)}%`
  if (value.fixed_amount !== undefined) return `固定 ${Number(value.fixed_amount).toFixed(2)} 元`
  if (value.tiers) return `${value.tiers.mode === 'whole' ? '整笔跳档' : '超额累进'}：${value.tiers.levels.map(l => `从 ${Number(l.from).toFixed(2)} 元起 ${Number(l.percent).toFixed(2)}%`).join('；')}`
  return '不计提成'
}

const editor = useModalForm()
const { open, loading, saving } = editor
const ready = ref(false)
const effectiveFrom = ref('')
const minDate = ref('')
const positions = ref<Schema['Ref'][]>([])
const staff = ref<Schema['StaffOption'][]>([])
const items = ref<Schema['Item'][]>([])
const rules = ref<RuleForm[]>([])
const overrides = ref<OverrideForm[]>([])
const excludedStaff = ref<string[]>([])
const enabledSources = computed(() => sources.filter(s => rules.value.some(r => r.source === s.value && r.enabled)))
const staffOptions = computed(() => staff.value.map(s => ({ value: s.id, label: `${s.name}${s.active ? '' : '（离职）'}` })))

function formValue(cell?: Cell): CommissionValue {
  return {
    kind: cell?.tiers ? 'tiers' : cell?.percent !== undefined ? 'percent' : cell?.fixed_amount !== undefined ? 'fixed_amount' : 'none',
    amount: cell?.percent ?? cell?.fixed_amount ?? '', mode: cell?.tiers?.mode ?? 'whole',
    levels: cell?.tiers?.levels.map(l => ({ ...l })) ?? [{ from: '0.00', percent: '' }, { from: '', percent: '' }]
  }
}

function key(cell: Cell) {
  return JSON.stringify([cell.position_id, cell.designated, cell.member])
}

function rebuild(rule: RuleForm) {
  const previous = new Map(rule.cells.map(c => [c.key, c.value]))
  let dimensions: Cell[] = [{}]
  if (rule.by_position) dimensions = dimensions.flatMap(c => positions.value.map(p => ({ ...c, position_id: p.id })))
  if (rule.by_designated) dimensions = dimensions.flatMap(c => [true, false].map(designated => ({ ...c, designated })))
  if (rule.by_member) dimensions = dimensions.flatMap(c => [true, false].map(member => ({ ...c, member })))
  rule.cells = dimensions.map(d => ({ key: key(d), dimensions: d, value: previous.get(key(d)) ?? formValue() }))
}

function toggleRule(rule: RuleForm) {
  if (!rule.enabled) overrides.value = overrides.value.filter(o => o.source !== rule.source)
}

async function edit() {
  editor.begin(() => {
    ready.value = false
    rules.value = []
    overrides.value = []
    excludedStaff.value = []
  })
  await editor.load(async (current) => {
    const [p, s, i, latest] = await Promise.all([
      api.POST('/api/ops/list_positions', { body: {} }), api.POST('/api/ops/list_staff_options', { body: {} }),
      api.POST('/api/ops/list_items', { body: {} }), api.POST('/api/ops/list_commission_scheme_versions', { body: { limit: 1 } })
    ])
    if (!current()) return
    for (const result of [p, s, i, latest]) {
      if (result.error) {
        toast.add({ title: errorText(result.error), color: 'error' })
        return
      }
    }
    const last = latest.data?.versions[0]
    const response = last ? await api.POST('/api/ops/get_commission_scheme', { body: { version: last.version } }) : undefined
    if (!current()) return
    if (response?.error) {
      toast.add({ title: errorText(response.error), color: 'error' })
      return
    }
    const scheme = response?.data?.scheme
    positions.value = [...(p.data?.items ?? [])]
    for (const cell of scheme?.rules.flatMap(r => r.cells) ?? []) {
      if (cell.position && !positions.value.some(p => p.id === cell.position!.id)) positions.value.push(cell.position)
    }
    staff.value = s.data?.staff ?? []
    items.value = i.data?.items ?? []
    minDate.value = last && last.effective_from > today.value ? last.effective_from : today.value
    effectiveFrom.value = minDate.value
    rules.value = sources.map(({ value: source }) => {
      const existing = scheme?.rules.find(r => r.source === source)
      const rule: RuleForm = {
        source, enabled: !!existing, base: existing?.base ?? 'performance',
        by_position: existing?.by_position ?? false, by_designated: existing?.by_designated ?? false, by_member: existing?.by_member ?? false,
        cells: existing?.cells.map(c => ({ key: key(c), dimensions: { position_id: c.position_id, designated: c.designated, member: c.member }, value: formValue(c) })) ?? []
      }
      rebuild(rule)
      return rule
    })
    overrides.value = scheme?.overrides.map(o => ({ source: o.source, staff_id: o.staff_id ?? '__all__', item_id: o.item_id ?? '__all__', value: formValue(o) })) ?? []
    excludedStaff.value = scheme?.excluded_staff.map(s => s.staff_id) ?? []
    ready.value = true
  })
}

function payloadValue(value: CommissionValue): Cell {
  if (value.kind === 'tiers') return { tiers: { mode: value.mode, levels: value.levels.map(l => ({ from: String(l.from), percent: String(l.percent) })) } }
  if (value.kind === 'none') return {}
  return { [value.kind]: String(value.amount) }
}

async function save() {
  await editor.save(async (current) => {
    const scheme: Schema['CommissionScheme'] = {
      rules: rules.value.filter(r => r.enabled).map(r => ({
        source: r.source, base: r.source === 'item' ? r.base : 'performance',
        by_position: r.by_position, by_designated: r.by_designated, by_member: r.by_member,
        cells: r.cells.filter(c => c.value.kind !== 'none').map(c => ({ ...c.dimensions, ...payloadValue(c.value) }))
      })),
      overrides: overrides.value.map(o => ({ source: o.source, staff_id: o.staff_id === '__all__' ? undefined : o.staff_id, item_id: o.source === 'sale' || o.item_id === '__all__' ? undefined : o.item_id, ...payloadValue(o.value) })),
      excluded_staff: excludedStaff.value
    }
    const { data, error } = await api.POST('/api/ops/save_commission_scheme', { body: { effective_from: effectiveFrom.value, scheme } })
    if (!current()) return
    if (data) {
      toast.add({ title: `已保存版本 ${data.version}，${data.effective_from} 起执行`, color: 'success' })
      open.value = false
      await reload(data.version)
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-xl font-semibold">提成规则</h1>
    <p class="text-sm text-muted">保存后从生效日期起执行，之前写入的业绩仍按当时的方案版本算；阶梯提成随当月累计变化，月底锁定后才固定。今天是营业日 {{ today }}。</p>
    <p v-if="!versions.loading.value && !versions.items.value.length">还没有提成方案，没有方案时不计提成。</p>
    <UCard>
      <template #header><h2 class="font-semibold">方案版本</h2></template>
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="text-left text-muted"><tr><th>版本</th><th>生效日期</th><th>修改人</th><th>保存时间</th><th>状态</th></tr></thead>
          <tbody>
            <tr v-for="v in versions.items.value" :key="v.version" :data-version="v.version" class="border-t border-default" :class="selected === v.version ? 'bg-elevated' : ''">
              <td class="py-2"><UButton variant="link" @click="selectVersion(v.version)">版本 {{ v.version }}</UButton></td>
              <td>{{ v.effective_from }}</td><td>{{ v.saved_by.name }}</td><td>{{ new Date(v.saved_at).toLocaleString('zh-CN') }}</td>
              <td><UBadge v-if="inForce === v.version">执行中</UBadge><UBadge v-else-if="v.effective_from > today" color="warning">已安排</UBadge></td>
            </tr>
          </tbody>
        </table>
      </div>
      <UButton v-if="versions.hasMore.value" :loading="versions.loading.value" variant="outline" @click="versions.loadMore">加载更多</UButton>
    </UCard>
    <p v-if="detailLoading">加载中</p>
    <UCard v-if="detail" data-testid="scheme-detail">
      <template #header><h2 class="font-semibold">版本 {{ detail.version }} · {{ detail.effective_from }} 起执行</h2></template>
      <section v-for="source in sources" :key="source.value" class="mb-4 space-y-2" :data-source="source.value">
        <h3 class="font-medium">{{ source.label }}</h3>
        <template v-for="rule in detail.scheme.rules.filter(r => r.source === source.value)" :key="rule.source">
          <p>计提基数：{{ bases.find(b => b.value === rule.base)?.label }}</p>
          <p>区分：{{ [rule.by_position ? '岗位' : '', rule.by_designated ? '指定或非指定' : '', rule.by_member ? '会员或散客' : ''].filter(Boolean).join('、') || '不区分' }}</p>
          <table class="w-full text-sm">
            <thead class="text-left text-muted"><tr><th v-if="rule.by_position">岗位</th><th v-if="rule.by_designated">指定或非指定</th><th v-if="rule.by_member">会员或散客</th><th>提成规则</th></tr></thead>
            <tbody><tr v-for="(cell, index) in rule.cells" :key="index" class="border-t border-default"><td v-if="rule.by_position">{{ cell.position?.name }}</td><td v-if="rule.by_designated">{{ cell.designated ? '指定' : '非指定' }}</td><td v-if="rule.by_member">{{ cell.member ? '会员' : '散客' }}</td><td class="py-2">{{ valueText(cell) }}</td></tr></tbody>
          </table>
          <p class="text-sm text-muted">未填的格子不计提成。</p>
        </template>
        <p v-if="!detail.scheme.rules.some(r => r.source === source.value)">不计提成</p>
      </section>
      <h3 class="font-medium">覆盖比例</h3>
      <p v-if="!detail.scheme.overrides.length">无</p>
      <p v-for="(o, index) in detail.scheme.overrides" :key="index">{{ sources.find(s => s.value === o.source)?.label }} · {{ o.staff?.name ?? '全部员工' }} · {{ o.item?.name ?? '全部项目' }} · {{ valueText(o) }}</p>
      <h3 class="mt-4 font-medium">不计提成的员工</h3>
      <p>{{ detail.scheme.excluded_staff.map(s => s.staff.name).join('、') || '无' }}</p>
    </UCard>
    <UButton :disabled="!today" @click="edit">修改方案</UButton>
    <UModal v-model:open="open" title="修改提成方案" :ui="{ content: 'sm:max-w-5xl' }">
      <template #body>
        <p v-if="loading">加载中</p>
        <div v-if="ready" class="space-y-5">
          <UFormField label="生效日期" help="不能早于今天，也不能早于最新版本；同一天保存多次，取最后保存的。"><UInput v-model="effectiveFrom" type="date" :min="minDate" /></UFormField>
          <section v-for="rule in rules" :key="rule.source" :data-rule="rule.source" class="space-y-3 rounded border border-default p-4">
            <USwitch v-model="rule.enabled" :label="`按${sources.find(s => s.value === rule.source)?.label}计提成`" @update:model-value="toggleRule(rule)" />
            <template v-if="rule.enabled">
              <UFormField label="计提基数"><USelect v-if="rule.source === 'item'" v-model="rule.base" :items="bases" /><span v-else>业绩</span></UFormField>
              <div v-if="rule.source !== 'sale'" class="flex flex-wrap gap-4">
                <USwitch v-model="rule.by_position" label="按岗位" @update:model-value="rebuild(rule)" />
                <USwitch v-model="rule.by_designated" label="按指定或非指定" @update:model-value="rebuild(rule)" />
                <USwitch v-model="rule.by_member" label="按会员或散客" @update:model-value="rebuild(rule)" />
              </div>
              <p class="text-sm text-muted">每个格子填比例、固定金额或阶梯，未填的格子不计提成。</p>
              <div v-for="cell in rule.cells" :key="cell.key" data-testid="rule-cell" :data-position="cell.dimensions.position_id" class="space-y-2 border-t border-default py-3">
                <p>{{ [positions.find(p => p.id === cell.dimensions.position_id)?.name, rule.by_designated ? (cell.dimensions.designated ? '指定' : '非指定') : '', rule.by_member ? (cell.dimensions.member ? '会员' : '散客') : ''].filter(Boolean).join(' · ') || '全部' }}</p>
                <CommissionValueEditor v-model="cell.value" tiers />
              </div>
            </template>
          </section>
          <section class="space-y-3">
            <h3 class="font-medium">覆盖比例</h3>
            <p class="text-sm text-muted">覆盖比例是比例或固定金额，不带阶梯；项目覆盖只作用于项目业绩、耗卡业绩和手工费。</p>
            <div v-for="(o, index) in overrides" :key="index" data-testid="override-row" class="space-y-2 rounded border border-default p-3">
              <UFormField label="来源"><USelect v-model="o.source" :items="enabledSources" @update:model-value="o.item_id = o.source === 'sale' ? '__all__' : o.item_id" /></UFormField>
              <UFormField label="员工"><USelect v-model="o.staff_id" :items="[{ label: '全部员工', value: '__all__' }, ...staffOptions]" /></UFormField>
              <UFormField v-if="o.source !== 'sale'" label="项目"><USelect v-model="o.item_id" :items="[{ label: '全部项目', value: '__all__' }, ...items.map(i => ({ label: i.name, value: i.id }))]" /></UFormField>
              <CommissionValueEditor v-model="o.value" />
              <UButton variant="ghost" @click="overrides.splice(index, 1)">删除覆盖</UButton>
            </div>
            <UButton :disabled="!enabledSources.length" variant="outline" @click="overrides.push({ source: enabledSources[0]!.value, staff_id: '__all__', item_id: '__all__', value: formValue({ percent: '' }) })">添加覆盖</UButton>
          </section>
          <UFormField label="不计提成的员工"><USelect v-model="excludedStaff" multiple :items="staffOptions" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer><UButton :loading="saving" :disabled="loading || !ready" @click="save">保存</UButton></template>
    </UModal>
  </div>
</template>
