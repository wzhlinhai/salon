<script setup lang="ts">
import type { TableColumn, TableRow } from '@nuxt/ui'
import type { components } from '~/utils/api'

definePageMeta({ permission: 'payroll.manage' })
type Schema = components['schemas']
type Line = Schema['PayrollLine']
const api = useApi()
const toast = useToast()
const months = ref<Schema['PayrollMonth'][]>([])
const month = ref('')
const payroll = ref<Schema['Payroll']>()
const salaries = ref<Schema['StaffBaseSalary'][]>([])
const guard = useRequestGuard()
const monthsGuard = useRequestGuard()
const selected = ref<Schema['Ref']>()
const detailOpen = ref(false)
const detail = useTemplateRef('detail')
const loading = ref(false)

function monthLabel(value: string) {
  const [y, m] = value.split('-')
  return `${y}年${Number(m)}月`
}

const monthItems = computed(() => months.value.map(m => ({ value: m.month, label: monthLabel(m.month) + (m.locked ? '（已锁定）' : '') })))
const status = computed(() => months.value.find(m => m.month === month.value))
const staffItems = computed(() => salaries.value.map(s => ({ value: s.staff.id, label: s.staff.name + (s.active ? '' : '（离职）') })))
const amountColumns = [
  ['applied_amount', '底薪/保底'], ['base_pay', '底薪部分'], ['item_commission', '项目'], ['consume_commission', '耗卡'],
  ['labor_commission', '手工费'], ['sale_commission', '销售'], ['commission_total', '提成合计'], ['guarantee_top_up', '保底补差'], ['adjustments_total', '加减项'], ['total', '应发']
] as const
const columns: TableColumn<Line>[] = [
  { accessorKey: 'staff', header: '员工' }, { accessorKey: 'mode', header: '计薪方式' },
  ...amountColumns.map(([accessorKey, header]) => ({ accessorKey, header, meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } }))
]
const rows = computed<Line[]>(() => payroll.value?.lines.length ? [...payroll.value.lines, { ...payroll.value.totals, staff: { id: '__total__', name: '合计' }, active: true, mode: 'base' }] : [])

function selectRow(_event: Event, row: TableRow<Line>) {
  if (row.original.staff.id === '__total__') return
  selected.value = row.original.staff
  detailOpen.value = true
}

async function reloadMonths() {
  const current = monthsGuard.next()
  const { data, error } = await api.POST('/api/ops/list_payroll_months', { body: {} })
  if (!current()) return
  if (data) {
    months.value = data.entries
    if (!month.value) month.value = data.entries[0]?.month ?? ''
  } else toast.add({ title: errorText(error), color: 'error' })
}

async function reload() {
  if (!month.value) return
  const current = guard.next()
  const targetMonth = month.value
  loading.value = true
  try {
    const [p, s] = await Promise.all([
      api.POST('/api/ops/get_payroll', { body: { month: targetMonth } }),
      api.POST('/api/ops/list_base_salaries', { body: { month: targetMonth } })
    ])
    if (!current()) return
    if (p.data) payroll.value = p.data
    else toast.add({ title: errorText(p.error), color: 'error' })
    if (s.data) salaries.value = s.data.entries
    else toast.add({ title: errorText(s.error), color: 'error' })
  } finally {
    if (current()) loading.value = false
  }
}

const editor = useModalForm()
const { open, saving } = editor
const kind = ref<'salary' | 'adjustment'>('salary')
const formMonth = ref('')
const form = reactive({ staff_id: '', mode: 'base' as 'base' | 'guarantee', amount: '', name: '', note: '' })
const inForce = computed(() => salaries.value.find(s => s.staff.id === form.staff_id)?.base_salary)

function edit(target: 'salary' | 'adjustment') {
  editor.begin(() => {
    kind.value = target
    formMonth.value = month.value
    const first = salaries.value[0]
    Object.assign(form, { staff_id: first?.staff.id ?? '', mode: first?.base_salary?.mode ?? 'base', amount: target === 'salary' ? first?.base_salary?.amount ?? '' : '', name: '', note: '' })
  })
}

watch(() => form.staff_id, () => {
  if (kind.value === 'salary') {
    form.mode = inForce.value?.mode ?? 'base'
    form.amount = inForce.value?.amount ?? ''
  }
})

async function save() {
  await editor.save(async (current) => {
    const targetMonth = formMonth.value
    const staffId = form.staff_id
    const editingKind = kind.value
    const body = { month: targetMonth, staff_id: staffId, amount: String(form.amount) }
    const { error } = editingKind === 'salary'
      ? await api.POST('/api/ops/set_base_salary', { body: { ...body, mode: form.mode } })
      : await api.POST('/api/ops/add_payroll_adjustment', { body: { ...body, name: form.name, note: form.note } })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void reload()
    if (editingKind === 'adjustment' && month.value === targetMonth && selected.value?.id === staffId) void detail.value?.reloadAdjustments()
    if (!current()) return
    toast.add({ title: '已保存' })
    open.value = false
  })
}

const lockForm = useModalForm()
const { open: lockOpen, saving: lockSaving } = lockForm
const lockMonth = ref('')
const lockTotal = ref('')

function askLock() {
  lockForm.begin(() => {
    lockMonth.value = month.value
    lockTotal.value = payroll.value?.totals.total ?? ''
  })
}

async function lock() {
  await lockForm.save(async (current) => {
    const target = lockMonth.value
    const { data, error } = await api.POST('/api/ops/lock_payroll_month', { body: { month: target } })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    if (current()) {
      guard.invalidate()
      loading.value = false
      payroll.value = data
    } else {
      void reload()
    }
    await reloadMonths()
    if (!current()) return
    toast.add({ title: '已锁定' })
    lockOpen.value = false
  })
}

watch(month, () => {
  payroll.value = undefined
  salaries.value = []
  detailOpen.value = false
  selected.value = undefined
  open.value = false
  lockOpen.value = false
  void reload()
})
onMounted(reloadMonths)
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-xl font-semibold">工资表</h1>
    <div class="flex flex-wrap items-center gap-3">
      <USelect v-model="month" aria-label="结算月" :items="monthItems" />
      <span v-if="payroll?.locked">已锁定 · {{ payroll.locked_by?.name }} · {{ payroll.locked_at ? new Date(payroll.locked_at).toLocaleString('zh-CN') : '' }}</span>
      <span v-else>未锁定</span>
      <div v-if="payroll && !payroll.locked" class="ml-auto flex flex-wrap items-center gap-2">
        <UButton @click="edit('salary')">设置底薪</UButton>
        <UButton @click="edit('adjustment')">添加加减项</UButton>
        <UButton :disabled="!status?.lockable" @click="askLock">锁定本月</UButton>
        <span v-if="!status?.lockable" class="text-sm text-muted">月份结束且之前的月份都已锁定后才能锁定</span>
      </div>
    </div>
    <UTable :data="rows" :columns="columns" :loading="loading" :aria-busy="loading" data-testid="payroll-table" @select="selectRow">
      <template #staff-cell="{ row }">{{ row.original.staff.name }}{{ !row.original.active ? '（离职）' : '' }}</template>
      <template #mode-cell="{ row }">{{ row.original.staff.id === '__total__' ? '' : row.original.mode === 'base' ? '底薪 + 提成' : '保底' }}</template>
      <template #applied_amount-cell="{ row }">{{ row.original.staff.id === '__total__' ? '' : row.original.applied_amount }}</template>
      <template #empty>本月还没有员工工资</template>
    </UTable>
    <USlideover v-model:open="detailOpen" :modal="false" :title="`${selected?.name ?? ''} · ${monthLabel(month)}`">
      <template #body>
        <PayrollDetail v-if="selected && payroll" :key="`${month}:${selected.id}`" ref="detail" :month="month" :staff-id="selected.id" :locked="payroll.locked" @changed="reload" />
      </template>
    </USlideover>
    <UModal v-model:open="open" :title="kind === 'salary' ? '设置底薪' : '添加加减项'">
      <template #body>
        <div class="space-y-3">
          <UFormField label="员工"><USelect v-model="form.staff_id" :items="staffItems" class="w-full" /></UFormField>
          <template v-if="kind === 'salary'">
            <p v-if="inForce">当前：{{ inForce.mode === 'base' ? '底薪 + 提成' : '保底' }} {{ inForce.amount }}，自 {{ monthLabel(inForce.effective_month) }}起</p>
            <p v-else>当前：未设置</p>
            <UFormField label="计薪方式"><URadioGroup v-model="form.mode" :items="[{ value: 'base', label: '底薪 + 提成' }, { value: 'guarantee', label: '保底' }]" /></UFormField>
          </template>
          <UFormField v-else label="名称"><UInput v-model="form.name" :maxlength="20" class="w-full" /></UFormField>
          <UFormField label="金额" :hint="kind === 'adjustment' ? '正数为加，负数为减' : undefined"><UInput v-model="form.amount" type="number" step="0.01" class="w-full" /></UFormField>
          <p v-if="kind === 'salary'">从 {{ monthLabel(formMonth) }} 起执行</p>
          <UFormField v-else label="备注"><UTextarea v-model="form.note" :maxlength="100" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer><UButton :loading="saving" @click="save">保存</UButton></template>
    </UModal>
    <UModal v-model:open="lockOpen" :title="`锁定 ${monthLabel(lockMonth)}`">
      <template #body>
        <p>锁定后本月的提成和工资表不再变化，也不能再设置底薪或增删加减项。锁定不能撤销，之后发现错误只能在以后的月份用加减项更正。</p>
        <p class="mt-3">全店应发 {{ lockTotal }}</p>
      </template>
      <template #footer>
        <UButton color="neutral" variant="outline" @click="lockOpen = false">取消</UButton>
        <UButton :loading="lockSaving" @click="lock">确认锁定</UButton>
      </template>
    </UModal>
  </div>
</template>
