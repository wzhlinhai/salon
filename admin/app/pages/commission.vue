<script setup lang="ts">
import type { components } from '~/utils/api'
import type { CommissionValue } from '~/components/CommissionValueEditor.vue'
import { LOG_PAGE_SIZE } from '~/utils/paging'

definePageMeta({ permission: 'settings.manage', title: '提成规则', description: '保存后从生效日期起执行，之前写入的业绩仍按当时的方案版本算；阶梯提成随当月累计变化，月底锁定后才固定。', art: 'tag' })
type Schema = components['schemas']
type Source = Schema['Rule']['source']
type Cell = Schema['Cell']
type View = Schema['CommissionSchemeVersionView']
interface GridCell { key: string, dimensions: Cell, value: CommissionValue }
interface RuleForm extends Omit<Schema['Rule'], 'cells'> { enabled: boolean, cells: GridCell[] }
type OverrideTarget = 'staff_item' | 'position_category'
interface OverrideForm { source: Source, target: OverrideTarget, staff_id: string, item_id: string, position_id: string, category_id: string, value: CommissionValue }
const sources: { value: Source, label: string }[] = [
  { value: 'item', label: '项目业绩' }, { value: 'consume', label: '耗卡业绩' },
  { value: 'labor', label: '手工费' }, { value: 'sale', label: '销售业绩' }
]
const bases = [
  { value: 'line_price', label: '项目行价格（原价）' }, { value: 'paid', label: '实收（折后价）' },
  { value: 'performance', label: '业绩' }, { value: 'cash_performance', label: '现金业绩' }, { value: 'card_performance', label: '卡金业绩' }
]
const targets: { value: OverrideTarget, label: string }[] = [
  { value: 'staff_item', label: '员工 / 项目' }, { value: 'position_category', label: '岗位 × 类别' }
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
}, { inlineError: true })
// 读失败原地给「重试」：写成「还没有提成方案」，店长会以为方案丢了要重建。
const settingsError = ref('')
const loadError = computed(() => settingsError.value || (versions.items.value.length ? '' : versions.error.value))

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
    settingsError.value = errorText(settings.error)
    return
  }
  settingsError.value = ''
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
const categories = ref<Schema['ItemCategoryRef'][]>([])
const categoryLoading = ref(false)
const staff = ref<Schema['StaffOption'][]>([])
const items = ref<Schema['Item'][]>([])
const rules = ref<RuleForm[]>([])
const overrides = ref<OverrideForm[]>([])
const excludedStaff = ref<string[]>([])
// 手工费本店不用，来源默认不显示；已有方案开着手工费时照常显示，保存时原样带回。
const laborInUse = ref(false)
const shownSources = (labor: boolean) => sources.filter(s => s.value !== 'labor' || labor)
const editRules = computed(() => rules.value.filter(r => r.source !== 'labor' || laborInUse.value))
const enabledSources = computed(() => sources.filter(s => rules.value.some(r => r.source === s.value && r.enabled)))
const staffOptions = computed(() => staff.value.map(s => ({ value: s.id, label: `${s.name}${s.active ? '' : '（离职）'}` })))
const categoryOptions = computed(() => categories.value.map(c => ({ value: c.id, label: c.name })))

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
    categoryLoading.value = false
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
    if (!(await loadCategories(current))) return
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
    for (const override of scheme?.overrides ?? []) {
      if (override.position && !positions.value.some(p => p.id === override.position!.id)) positions.value.push(override.position)
      if (override.category && !categories.value.some(c => c.id === override.category!.id)) categories.value.push(override.category)
    }
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
    laborInUse.value = rules.value.some(r => r.source === 'labor' && r.enabled)
    overrides.value = scheme?.overrides.map(o => ({
      source: o.source,
      target: o.position_id ? 'position_category' : 'staff_item',
      staff_id: o.staff_id ?? '__all__', item_id: o.item_id ?? '__all__',
      position_id: o.position_id ?? '__all__', category_id: o.category_id ?? '__all__',
      value: formValue(o)
    })) ?? []
    excludedStaff.value = scheme?.excluded_staff.map(s => s.staff_id) ?? []
    ready.value = true
  })
}

async function loadCategories(current: () => boolean): Promise<boolean> {
  categoryLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_item_categories', { body: {} })
    if (!current()) return false
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return false
    }
    categories.value = [...data.items]
    return true
  } finally {
    if (current()) categoryLoading.value = false
  }
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
      overrides: overrides.value.map(o => o.target === 'position_category'
        ? { source: o.source, position_id: o.position_id === '__all__' ? undefined : o.position_id, category_id: o.category_id === '__all__' ? undefined : o.category_id, ...payloadValue(o.value) }
        : { source: o.source, staff_id: o.staff_id === '__all__' ? undefined : o.staff_id, item_id: o.source === 'sale' || o.item_id === '__all__' ? undefined : o.item_id, ...payloadValue(o.value) }),
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

function changeTarget(o: OverrideForm) {
  if (o.target === 'position_category') {
    o.staff_id = '__all__'
    o.item_id = '__all__'
    if (o.source === 'sale') o.source = enabledSources.value.find(s => s.value !== 'sale')?.value ?? 'item'
  } else {
    o.position_id = '__all__'
    o.category_id = '__all__'
  }
}

function changeSource(o: OverrideForm) {
  if (o.target === 'position_category' && o.source === 'sale') o.source = enabledSources.value.find(s => s.value !== 'sale')?.value ?? 'item'
  if (o.source === 'sale') o.item_id = '__all__'
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton size="sm" icon="i-lucide-pencil" :disabled="!today" @click="edit">修改方案</UButton>
    </PageActions>
    <PageDescriptionTail v-if="today">今天是营业日 {{ today }}。</PageDescriptionTail>
    <LoadError v-if="loadError" title="提成方案没有读出来" :hint="loadError" @retry="reload()" />
    <EmptyState v-else-if="!versions.loading.value && !versions.items.value.length" art="chart" title="还没有提成方案，没有方案时不计提成。" hint="点右上角修改方案，按项目、耗卡或销售设置提成" />
    <section v-else>
      <div class="sec">方案版本</div>
      <div class="overflow-x-auto">
        <table class="data-table">
          <thead class="text-left">
            <tr><th>版本</th><th>生效日期</th><th>修改人</th><th>保存时间</th><th>状态</th></tr>
          </thead>
          <tbody>
            <tr
              v-for="v in versions.items.value"
              :key="v.version"
              :data-version="v.version"
              class="cursor-pointer"
              :class="selected === v.version ? 'bg-selected' : 'hover:bg-hover'"
              @click="selectVersion(v.version)"
            >
              <td><button type="button" class="text-start font-medium whitespace-nowrap hover:underline" @click.stop="selectVersion(v.version)">版本 {{ v.version }}</button></td>
              <td class="tabular-nums">{{ v.effective_from }}</td>
              <td>{{ v.saved_by.name }}</td>
              <td class="text-muted tabular-nums">{{ new Date(v.saved_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }}</td>
              <td><span v-if="inForce === v.version" class="tag bg-tag-gray">执行中</span><span v-else-if="v.effective_from > today" class="tag bg-tag-yellow">已安排</span></td>
            </tr>
          </tbody>
        </table>
        <UButton v-if="versions.hasMore.value" :loading="versions.loading.value" size="sm" color="neutral" variant="ghost" class="m-2" @click="versions.loadMore">加载更多</UButton>
      </div>
    </section>
    <ListSkeleton v-if="detailLoading" :rows="3" />
    <section v-if="detail" data-testid="scheme-detail">
      <div class="sec">版本 {{ detail.version }} · {{ detail.effective_from }} 起执行</div>
      <div class="overflow-x-auto">
        <div v-for="source in shownSources(detail.scheme.rules.some(r => r.source === 'labor'))" :key="source.value" class="grid gap-x-6 gap-y-2 border-t border-default px-4 py-4 first:border-t-0 md:grid-cols-[10rem_minmax(0,1fr)]" :data-source="source.value">
          <div class="text-sm font-medium">{{ source.label }}</div>
          <div class="text-sm">
            <template v-for="rule in detail.scheme.rules.filter(r => r.source === source.value)" :key="rule.source">
              <p class="text-muted">计提基数：{{ bases.find(b => b.value === rule.base)?.label }} · 区分：{{ [rule.by_position ? '岗位' : '', rule.by_designated ? '指定或非指定' : '', rule.by_member ? '会员或散客' : ''].filter(Boolean).join('、') || '不区分' }}</p>
              <table class="mt-2 w-full max-w-xl">
                <thead class="text-left text-sm text-muted"><tr><th v-if="rule.by_position" class="py-1 font-normal">岗位</th><th v-if="rule.by_designated" class="font-normal">指定或非指定</th><th v-if="rule.by_member" class="font-normal">会员或散客</th><th class="font-normal">提成规则</th></tr></thead>
                <tbody><tr v-for="(cell, index) in rule.cells" :key="index" class="border-t border-default"><td v-if="rule.by_position" class="py-2">{{ cell.position?.name }}</td><td v-if="rule.by_designated">{{ cell.designated ? '指定' : '非指定' }}</td><td v-if="rule.by_member">{{ cell.member ? '会员' : '散客' }}</td><td class="py-2 font-medium tabular-nums">{{ valueText(cell) }}</td></tr></tbody>
              </table>
              <p class="mt-1 text-xs text-muted">未填的格子不计提成。</p>
            </template>
            <p v-if="!detail.scheme.rules.some(r => r.source === source.value)" class="text-dimmed">不计提成</p>
          </div>
        </div>
        <div class="grid gap-x-6 gap-y-2 border-t border-default px-4 py-4 md:grid-cols-[10rem_minmax(0,1fr)]">
          <div class="text-sm font-medium">覆盖比例</div>
          <div class="space-y-1 text-sm">
            <p v-if="!detail.scheme.overrides.length" class="text-dimmed">无</p>
            <p v-for="(o, index) in detail.scheme.overrides" :key="index">
              {{ sources.find(s => s.value === o.source)?.label }} ·
              <template v-if="o.position_id">{{ o.position?.name }} × {{ o.category?.name }}</template>
              <template v-else>{{ o.staff?.name ?? '全部员工' }} · {{ o.item?.name ?? '全部项目' }}</template>
              · <span class="font-medium tabular-nums">{{ valueText(o) }}</span>
            </p>
          </div>
        </div>
        <div class="grid gap-x-6 gap-y-2 border-t border-default px-4 py-4 md:grid-cols-[10rem_minmax(0,1fr)]">
          <div class="text-sm font-medium">不计提成的员工</div>
          <p class="text-sm" :class="detail.scheme.excluded_staff.length ? '' : 'text-dimmed'">{{ detail.scheme.excluded_staff.map(s => s.staff.name).join('、') || '无' }}</p>
        </div>
      </div>
    </section>
    <UModal v-model:open="open" title="修改提成方案" :ui="{ content: 'sm:max-w-5xl' }">
      <template #body>
        <ListSkeleton v-if="loading || categoryLoading" :rows="2" />
        <div v-if="ready" class="space-y-5">
          <UFormField label="生效日期" help="不能早于今天，也不能早于最新版本；同一天保存多次，取最后保存的。" class="max-w-xs"><DateInput v-model="effectiveFrom" :min="minDate" /></UFormField>
          <section v-for="rule in editRules" :key="rule.source" :data-rule="rule.source" class="space-y-3 rounded-card bg-side p-4">
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
            <p class="text-sm text-muted">覆盖比例是比例或固定金额，不带阶梯；项目覆盖和岗位 × 类别覆盖只作用于项目业绩和耗卡业绩。</p>
            <div v-for="(o, index) in overrides" :key="index" data-testid="override-row" class="space-y-2 rounded-card bg-side p-3">
              <UFormField label="目标"><USelect v-model="o.target" :items="targets" @update:model-value="changeTarget(o)" /></UFormField>
              <UFormField label="来源"><USelect v-model="o.source" :items="o.target === 'position_category' ? enabledSources.filter(s => s.value !== 'sale') : enabledSources" @update:model-value="changeSource(o)" /></UFormField>
              <template v-if="o.target === 'position_category'">
                <UFormField label="岗位"><USelect v-model="o.position_id" :items="[{ label: '请选择岗位', value: '__all__' }, ...positions.map(p => ({ label: p.name, value: p.id }))]" /></UFormField>
                <UFormField label="类别"><USelect v-model="o.category_id" :items="[{ label: '请选择类别', value: '__all__' }, ...categoryOptions]" /></UFormField>
              </template>
              <template v-else>
                <UFormField label="员工"><USelect v-model="o.staff_id" :items="[{ label: '全部员工', value: '__all__' }, ...staffOptions]" /></UFormField>
                <UFormField v-if="o.source !== 'sale'" label="项目"><USelect v-model="o.item_id" :items="[{ label: '全部项目', value: '__all__' }, ...items.map(i => ({ label: i.name, value: i.id }))]" /></UFormField>
              </template>
              <CommissionValueEditor v-model="o.value" />
              <UButton variant="ghost" @click="overrides.splice(index, 1)">删除覆盖</UButton>
            </div>
            <UButton color="neutral" class="-ms-2 self-start text-muted hover:text-default" :disabled="!enabledSources.length" variant="ghost" @click="overrides.push({ source: enabledSources[0]!.value, target: 'staff_item', staff_id: '__all__', item_id: '__all__', position_id: '__all__', category_id: '__all__', value: formValue({ percent: '' }) })">添加覆盖</UButton>
          </section>
          <UFormField label="不计提成的员工"><USelect v-model="excludedStaff" multiple :items="staffOptions" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="open = false">
          <UButton :loading="saving" :disabled="loading || !ready" @click="save">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
