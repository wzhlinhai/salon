<script setup lang="ts">
import type { components } from '~/utils/api'

// 工资表（样稿 15）：页头写「10 月工资」，顶上一句话说一共多少、比上个月、谁缺卡；下面一张明细，点一行看提成从哪来。
definePageMeta({ permission: 'payroll.manage', title: '工资表', art: 'coins' })
type Schema = components['schemas']
type Line = Schema['PayrollLine']
const api = useApi()
const toast = useToast()
const { can } = useMe()
const crumbTitle = useState('page-crumb-title', () => '')
const months = ref<Schema['PayrollMonth'][]>([])
const month = ref('')
const payroll = ref<Schema['Payroll']>()
const loadError = ref('')
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
// 金额列（样稿 15）：底薪、项目提成、销售提成、实发总在；耗卡、手工费、保底补差、加减项这个月有人有数才出现。
const amountColumns = [
  { key: 'base_pay', label: '底薪', always: true },
  { key: 'item_commission', label: '项目提成', always: true },
  { key: 'consume_commission', label: '耗卡提成', always: false },
  { key: 'labor_commission', label: '手工费', always: false },
  { key: 'sale_commission', label: '销售提成', always: true },
  { key: 'guarantee_top_up', label: '保底补差', always: false },
  { key: 'adjustments_total', label: '加减项', always: false }
] as const
const lines = computed(() => payroll.value?.lines ?? [])
const shownColumns = computed(() => amountColumns.filter(c => c.always || lines.value.some(l => !zeroText(l[c.key]))))

// 表身金额：零写「—」，负数写「−¥50」。
function money(value: string) {
  const n = Number(value)
  return n < 0 ? `−${yuan(-n)}` : yuanCell(value)
}

// 考勤缺卡：有考勤权限才取，取不到就不标。
const attendance = ref(new Map<string, number>())

async function loadAttendance(target: string) {
  if (!can('attendance.manage')) return
  const { data } = await api.POST('/api/ops/list_attendance_summary', { body: { month: target } })
  if (data && month.value === target) attendance.value = new Map(data.entries.filter(e => e.missing_count > 0).map(e => [e.staff.id, e.missing_count]))
}

// 上个月的工资表，用来比一比：有上个月才取。
const previous = ref<Schema['Payroll']>()

function previousMonth(value: string) {
  const [y, m] = value.split('-').map(Number)
  return new Date(Date.UTC(y!, m! - 2, 1)).toISOString().slice(0, 7)
}

async function loadPrevious(target: string) {
  const before = previousMonth(target)
  if (!months.value.some(m => m.month === before)) return
  const { data } = await api.POST('/api/ops/get_payroll', { body: { month: before } })
  if (data && month.value === target) previous.value = data
}

// 顶上一句话：几个人一共多少，比上个月多少、主要是谁；有人缺卡，提醒锁定前看一下。
const brief = computed(() => {
  const p = payroll.value
  if (!p || !p.lines.length) return []
  const { parts, say, bold } = briefWriter()
  say(`${p.lines.length} 个人一共 `)
  bold(yuan(p.totals.total))
  const before = previous.value
  if (before?.lines.length) {
    const diff = (cents(p.totals.total) - cents(before.totals.total)) / 100
    if (diff) {
      say(`，比 ${Number(before.month.slice(5))} 月${diff > 0 ? '多' : '少'} `)
      bold(yuan(Math.abs(diff)))
      const last = new Map(before.lines.map(l => [l.staff.id, cents(l.total)]))
      const mover = p.lines.map(l => ({ name: l.staff.name, change: (cents(l.total) - (last.get(l.staff.id) ?? 0)) / 100 }))
        .sort((x, y) => Math.abs(y.change) - Math.abs(x.change))[0]
      if (mover && Math.sign(mover.change) === Math.sign(diff) && Math.abs(mover.change) >= Math.abs(diff) / 2) say(`，主要是${mover.name}${mover.change > 0 ? '多了' : '少了'} ${yuan(Math.abs(mover.change))}`)
    } else say(`，和 ${Number(before.month.slice(5))} 月一样`)
  }
  say('。')
  const missing = p.lines.filter(l => attendance.value.has(l.staff.id))
  if (missing.length) {
    bold(missing.map(l => `${l.staff.name}缺卡 ${attendance.value.get(l.staff.id)} 次`).join('，'))
    say(p.locked ? '。' : '，锁定前看一下。')
  }
  if (p.locked) say(`已锁定（${p.locked_by?.name ?? ''} ${p.locked_at ? formatStamp(p.locked_at) : ''}），之后有错只能在以后的月份用加减项更正。`)
  return parts
})

function openLine(line: Line) {
  selected.value = line.staff
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
    void loadAttendance(targetMonth)
    void loadPrevious(targetMonth)
    // 这个月的工资表读不出来时原地给「重试」，别只弹提示留一片空白。
    if (p.data) {
      loadError.value = ''
      payroll.value = p.data
    } else if (payroll.value) toast.add({ title: errorText(p.error), color: 'error' })
    else loadError.value = errorText(p.error)
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

const moreItems = [[
  { label: '设置底薪', icon: 'i-lucide-wallet', onSelect: () => edit('salary') },
  { label: '添加加减项', icon: 'i-lucide-plus', onSelect: () => edit('adjustment') }
]]

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
    toast.add({ title: editingKind === 'salary' ? '底薪已改，工资表已重算' : '已加上这笔调整，工资表已重算', color: 'success' })
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
    toast.add({ title: '已锁定', color: 'success' })
    lockOpen.value = false
  })
}

watch(month, (value) => {
  crumbTitle.value = value ? `${Number(value.slice(5))} 月工资` : ''
  previous.value = undefined
  attendance.value = new Map()
  payroll.value = undefined
  salaries.value = []
  detailOpen.value = false
  selected.value = undefined
  open.value = false
  lockOpen.value = false
  void reload()
})
onMounted(reloadMonths)
onBeforeUnmount(() => {
  crumbTitle.value = ''
})
</script>

<template>
  <div class="space-y-6">
    <PageActions>
      <USelect v-model="month" aria-label="结算月" :items="monthItems" size="sm" class="min-w-36" />
      <UDropdownMenu v-if="payroll && !payroll.locked" :items="moreItems" :content="{ align: 'end' }">
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-ellipsis" aria-label="更多操作" />
      </UDropdownMenu>
      <UButton v-if="payroll && !payroll.locked" size="sm" :disabled="!status?.lockable" @click="askLock">核对无误，锁定</UButton>
    </PageActions>

    <ReportBrief v-if="brief.length" :parts="brief" data-testid="payroll-brief" />
    <div class="flex flex-wrap items-center gap-2 text-meta text-muted">
      <template v-if="payroll?.locked">
        <span class="tag bg-tag-gray">已锁定</span>
        <span>{{ payroll.locked_by?.name }} · {{ payroll.locked_at ? new Date(payroll.locked_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) : '' }}</span>
      </template>
      <template v-else-if="payroll">
        <span>未锁定</span>
        <span v-if="!status?.lockable">月份结束且之前的月份都已锁定后才能锁定</span>
      </template>
    </div>

    <section data-testid="payroll-table" :aria-busy="loading">
      <h2 class="sec">明细<span class="font-normal text-dimmed">点一行看每笔提成从哪来</span></h2>
      <ListSkeleton v-if="loading && !payroll" />
      <LoadError v-else-if="loadError && !payroll" :title="`${month} 的工资表没有读出来`" :hint="loadError" @retry="reload()" />
      <EmptyState v-else-if="payroll && !payroll.lines.length" art="calendar" title="本月还没有员工工资" hint="点右上角 ··· 设置底薪或保底，开单后的提成会自动算进来" />
      <div v-else-if="payroll" class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-default text-sm text-muted">
              <th scope="col" class="px-3 py-2 text-start font-normal">员工</th>
              <th v-for="c in shownColumns" :key="c.key" scope="col" class="px-3 py-2 text-end font-normal whitespace-nowrap">{{ c.label }}</th>
              <th scope="col" class="px-3 py-2 text-end font-normal">实发</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-default">
            <tr
              v-for="line in payroll.lines"
              :key="line.staff.id"
              class="cursor-pointer row-hover"
              :class="attendance.has(line.staff.id) ? 'bg-notice' : ''"
              @click="openLine(line)"
            >
              <td class="px-3 py-2">
                <div class="flex items-center gap-2">
                  <PersonAvatar :name="line.staff.name" size="sm" />
                  <button type="button" class="font-medium" @click.stop="openLine(line)">{{ line.staff.name }}</button>
                  <span v-if="!line.active" class="tag bg-tag-gray">离职</span>
                  <span v-if="attendance.has(line.staff.id)" class="tag bg-tag-red">缺卡 {{ attendance.get(line.staff.id) }} 次</span>
                </div>
              </td>
              <td v-for="c in shownColumns" :key="c.key" class="px-3 py-2 text-end tabular-nums whitespace-nowrap" :class="Number(line[c.key]) < 0 ? 'text-ink-red' : zeroText(line[c.key]) ? 'text-dimmed' : ''" :data-col="c.key">
                <template v-if="c.key === 'base_pay' && line.mode === 'guarantee'"><span class="text-dimmed">保底 {{ yuan(line.applied_amount) }}</span></template>
                <template v-else>{{ money(line[c.key]) }}</template>
              </td>
              <td class="px-3 py-2 text-end font-semibold tabular-nums" :class="Number(line.total) < 0 ? 'text-ink-red' : ''" data-col="total">{{ money(line.total) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="border-t border-default font-semibold" data-testid="payroll-totals">
              <td class="px-3 py-2">合计</td>
              <td v-for="c in shownColumns" :key="c.key" class="px-3 py-2 text-end tabular-nums whitespace-nowrap" :data-col="c.key">{{ money(payroll.totals[c.key]) }}</td>
              <td class="px-3 py-2 text-end tabular-nums" data-col="total">{{ money(payroll.totals.total) }}</td>
            </tr>
          </tfoot>
        </table>
      </div>
    </section>

    <USlideover v-model:open="detailOpen" :modal="false" :title="`${selected?.name ?? ''} · ${monthLabel(month)}`">
      <template #title>
        <span class="flex items-center gap-3"><PersonAvatar v-if="selected" :name="selected.name" />{{ selected?.name ?? '' }} · {{ monthLabel(month) }}</span>
      </template>
      <template #body>
        <PayrollDetail v-if="selected && payroll" :key="`${month}:${selected.id}`" ref="detail" :month="month" :staff-id="selected.id" :locked="payroll.locked" @changed="reload" />
      </template>
    </USlideover>
    <UModal v-model:open="open" :title="kind === 'salary' ? '设置底薪' : '添加加减项'" :description="kind === 'salary' ? `从 ${monthLabel(formMonth)} 起执行` : undefined">
      <template #body>
        <div class="space-y-5">
          <UFormField label="员工" :help="kind === 'salary' ? (inForce ? `当前：${inForce.mode === 'base' ? '底薪 + 提成' : '保底'} ${inForce.amount}，自 ${monthLabel(inForce.effective_month)}起` : '当前：未设置') : undefined">
            <USelect v-model="form.staff_id" :items="staffItems" class="w-full" />
          </UFormField>
          <UFormField v-if="kind === 'salary'" label="计薪方式"><URadioGroup v-model="form.mode" orientation="horizontal" :ui="{ fieldset: 'gap-x-6' }" :items="[{ value: 'base', label: '底薪 + 提成' }, { value: 'guarantee', label: '保底' }]" /></UFormField>
          <UFormField v-else label="名称"><UInput v-model="form.name" :maxlength="20" class="w-full" /></UFormField>
          <UFormField label="金额" :help="kind === 'adjustment' ? '正数为加，负数为减' : undefined"><UInput v-model="form.amount" type="number" step="0.01" class="w-full" /></UFormField>
          <UFormField v-if="kind === 'adjustment'" label="备注"><UTextarea v-model="form.note" :maxlength="100" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="open = false">
          <UButton :loading="saving" @click="save">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
    <UModal v-model:open="lockOpen" :title="`锁定 ${monthLabel(lockMonth)}`">
      <template #body>
        <p>锁定后本月的提成和工资表不再变化，也不能再设置底薪或增删加减项。锁定不能撤销，之后发现错误只能在以后的月份用加减项更正。</p>
        <p class="mt-3">全店应发 {{ yuan(lockTotal) }}</p>
      </template>
      <template #footer>
        <ModalActions @cancel="lockOpen = false">
          <UButton :loading="lockSaving" @click="lock">确认锁定</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
