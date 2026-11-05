<script setup lang="ts">
import type { TableColumn, TableRow } from '@nuxt/ui'
import type { components } from '~/utils/api'
import { attendanceCalendarDate, attendancePunchAt } from '~/utils/attendance'

definePageMeta({ permission: 'attendance.manage' })
type Schema = components['schemas']

const api = useApi()
const toast = useToast()
const tab = ref('monthly')
const tabs = [
  { label: '月度考勤', value: 'monthly' },
  { label: '班次', value: 'shifts' }
]

const today = ref('')
const calendarToday = ref(attendanceCalendarDate())
const currentMonth = ref('')
const month = ref('')
const summary = ref<Schema['AttendanceSummary'][]>([])
const summaryLoading = ref(false)
const summaryGuard = useRequestGuard()
const selected = ref<Schema['AttendanceSummary']>()
const detailOpen = ref(false)
const detail = useTemplateRef('detail')

function monthLabel(value: string) {
  if (!value) return ''
  const [year, monthNumber] = value.split('-')
  return `${year}年${Number(monthNumber)}月`
}

function shortDate(value: string) {
  const [, monthNumber, day] = value.split('-')
  return `${Number(monthNumber)}月${Number(day)}日`
}

function shiftText(shift: Schema['Shift'] | null | undefined) {
  return shift ? `${shift.name} ${shift.start_time}–${shift.end_time}` : '不按班次'
}

const summaryColumns: TableColumn<Schema['AttendanceSummary']>[] = [
  { accessorKey: 'staff', header: '员工' },
  { accessorKey: 'attendance_days', header: '出勤天数', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } },
  { accessorKey: 'late_count', header: '迟到', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } },
  { accessorKey: 'early_count', header: '早退', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } },
  { accessorKey: 'missing_count', header: '缺卡', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } },
  { accessorKey: 'approved_count', header: '已核准', meta: { class: { th: 'text-right', td: 'text-right tabular-nums' } } }
]

const shiftColumns: TableColumn<Schema['Shift']>[] = [
  { accessorKey: 'name', header: '名称' },
  { accessorKey: 'start_time', header: '上班' },
  { accessorKey: 'end_time', header: '下班' },
  { accessorKey: 'active', header: '状态' },
  { id: 'actions', header: '' }
]

const staffShiftColumns: TableColumn<Schema['StaffShiftEntry']>[] = [
  { accessorKey: 'staff', header: '员工' },
  { id: 'current', header: '当前班次' },
  { id: 'upcoming', header: '即将生效' },
  { id: 'actions', header: '' }
]

async function reloadSummary() {
  const current = summaryGuard.next()
  if (!month.value) {
    summary.value = []
    summaryLoading.value = false
    return
  }
  summaryLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_attendance_summary', { body: { month: month.value } })
    if (!current()) return
    if (data) summary.value = data.entries
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) summaryLoading.value = false
  }
}

function selectSummary(_event: Event, row: TableRow<Schema['AttendanceSummary']>) {
  selected.value = row.original
  detailOpen.value = true
}

const addPunchForm = useModalForm()
const { open: addPunchOpen, saving: addPunchSaving } = addPunchForm
const addPunchTarget = ref<Schema['AttendanceSummary'] | null>(null)
const punchForm = reactive({ staff_id: '', date: '', time: '', reason: '' })
const addPunchStaffItems = computed(() => addPunchTarget.value
  ? [{ label: addPunchTarget.value.staff.name, value: addPunchTarget.value.staff.id }]
  : summary.value.map(entry => ({ label: entry.staff.name + (entry.active ? '' : '（离职）'), value: entry.staff.id })))

function openAddPunch(staff?: Schema['AttendanceSummary']) {
  addPunchForm.begin(() => {
    addPunchTarget.value = staff ?? null
    Object.assign(punchForm, { staff_id: staff?.staff.id ?? '', date: calendarToday.value, time: '', reason: '' })
  })
}

async function savePunch() {
  await addPunchForm.save(async (current) => {
    const staffId = punchForm.staff_id
    const { error } = await api.POST('/api/ops/add_punch', {
      body: { staff_id: staffId, at: attendancePunchAt(punchForm.date, punchForm.time), reason: punchForm.reason }
    })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void reloadSummary()
    if (selected.value?.staff.id === staffId) void detail.value?.reload()
    if (!current()) return
    addPunchOpen.value = false
    toast.add({ title: '已补卡', color: 'success' })
  })
}

const shifts = ref<Schema['Shift'][]>([])
const shiftsLoading = ref(false)
const shiftsGuard = useRequestGuard()
const staffShifts = ref<Schema['StaffShiftEntry'][]>([])
const staffShiftsLoading = ref(false)
const staffShiftsGuard = useRequestGuard()

async function reloadShifts() {
  const current = shiftsGuard.next()
  shiftsLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_shifts', { body: {} })
    if (!current()) return
    if (data) shifts.value = data.entries
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) shiftsLoading.value = false
  }
}

async function reloadStaffShifts() {
  if (!today.value) return
  const current = staffShiftsGuard.next()
  staffShiftsLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_staff_shifts', { body: { date: today.value } })
    if (!current()) return
    if (data) staffShifts.value = data.entries
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) staffShiftsLoading.value = false
  }
}

const createShiftForm = useModalForm()
const { open: createShiftOpen, saving: createShiftSaving } = createShiftForm
const newShift = reactive({ name: '', start_time: '', end_time: '' })

function openCreateShift() {
  createShiftForm.begin(() => {
    Object.assign(newShift, { name: '', start_time: '', end_time: '' })
  })
}

async function saveShift() {
  await createShiftForm.save(async (current) => {
    const { error } = await api.POST('/api/ops/create_shift', { body: { ...newShift } })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void reloadShifts()
    if (!current()) return
    createShiftOpen.value = false
    toast.add({ title: '已保存', color: 'success' })
  })
}

const deactivateForm = useModalForm()
const { open: deactivateOpen, saving: deactivateSaving } = deactivateForm
const deactivateTarget = ref<Schema['Shift']>()

function askDeactivate(shift: Schema['Shift']) {
  deactivateForm.begin(() => {
    deactivateTarget.value = shift
  })
}

async function deactivateShift() {
  await deactivateForm.save(async (current) => {
    const { error } = await api.POST('/api/ops/deactivate_shift', { body: { id: deactivateTarget.value!.id } })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void reloadShifts()
    void reloadStaffShifts()
    if (!current()) return
    deactivateOpen.value = false
    toast.add({ title: '已停用', color: 'success' })
  })
}

const assignForm = useModalForm()
const { open: assignOpen, saving: assignSaving } = assignForm
const assignTarget = ref<Schema['StaffShiftEntry']>()
const assign = reactive<{ shift_id?: string, effective_from: string }>({ shift_id: undefined, effective_from: '' })
const activeShiftItems = computed(() => [
  { label: '不按班次', value: '__none__' },
  ...shifts.value.filter(shift => shift.active).map(shift => ({
    label: `${shift.name} ${shift.start_time}–${shift.end_time}`,
    value: shift.id
  }))
])
const activeShiftIds = computed(() => new Set(activeShiftItems.value.map(item => item.value)))
const currentShiftText = computed(() => {
  if (!assignTarget.value?.current) return '未指定'
  return shiftText(assignTarget.value.current.shift)
})

function askAssign(entry: Schema['StaffShiftEntry']) {
  assignForm.begin(() => {
    assignTarget.value = entry
    const currentShiftID = entry.current?.shift?.id
    if (!entry.current) assign.shift_id = undefined
    else if (!currentShiftID) assign.shift_id = '__none__'
    else assign.shift_id = activeShiftIds.value.has(currentShiftID) ? currentShiftID : undefined
    assign.effective_from = today.value
  })
}

async function saveAssign() {
  await assignForm.save(async (current) => {
    const { error } = await api.POST('/api/ops/set_staff_shift', {
      body: {
        staff_id: assignTarget.value!.staff.id,
        shift_id: assign.shift_id === '__none__' ? null : assign.shift_id,
        effective_from: assign.effective_from
      }
    })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void reloadStaffShifts()
    void reloadSummary()
    if (!current()) return
    assignOpen.value = false
    toast.add({ title: '已保存', color: 'success' })
  })
}

watch(month, () => {
  summary.value = []
  detailOpen.value = false
  selected.value = undefined
  addPunchOpen.value = false
  void reloadSummary()
})

onMounted(async () => {
  const { data, error } = await api.POST('/api/ops/get_settings', { body: {} })
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  today.value = data.business_date
  currentMonth.value = data.business_date.slice(0, 7)
  month.value = currentMonth.value
  await Promise.all([reloadShifts(), reloadStaffShifts()])
})
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-xl font-semibold">考勤</h1>
    <UTabs v-model="tab" :items="tabs" :content="false" />

    <section v-if="tab === 'monthly'" class="space-y-4">
      <div class="flex flex-wrap items-center gap-3">
        <UInput v-model="month" type="month" :max="currentMonth" aria-label="月份" />
        <UButton class="ml-auto" icon="i-lucide-plus" @click="openAddPunch()">补卡</UButton>
      </div>
      <UTable
        :data="summary"
        :columns="summaryColumns"
        :loading="summaryLoading"
        :aria-busy="summaryLoading"
        data-testid="attendance-table"
        @select="selectSummary"
      >
        <template #staff-cell="{ row }">{{ row.original.staff.name }}{{ row.original.active ? '' : '（离职）' }}</template>
        <template #late_count-cell="{ row }">{{ row.original.late_count }} 次 / {{ row.original.late_minutes }} 分钟</template>
        <template #early_count-cell="{ row }">{{ row.original.early_count }} 次 / {{ row.original.early_minutes }} 分钟</template>
        <template #missing_count-cell="{ row }">{{ row.original.missing_count }} 次</template>
        <template #approved_count-cell="{ row }">{{ row.original.approved_count }} 次</template>
        <template #empty>本月还没有考勤记录</template>
      </UTable>
      <USlideover v-model:open="detailOpen" :modal="false" :title="`${selected?.staff.name ?? ''} · ${monthLabel(month)}`">
        <template #body>
          <AttendanceDetail
            v-if="selected"
            :key="`${month}:${selected.staff.id}`"
            ref="detail"
            :month="month"
            :staff-id="selected.staff.id"
            @changed="reloadSummary"
            @add-punch="openAddPunch"
          />
        </template>
      </USlideover>
    </section>

    <section v-else class="space-y-8">
      <div class="space-y-3">
        <div class="flex items-center justify-between">
          <h2 class="font-semibold">班次</h2>
          <UButton icon="i-lucide-plus" @click="openCreateShift">新建班次</UButton>
        </div>
        <UTable :data="shifts" :columns="shiftColumns" :loading="shiftsLoading" :aria-busy="shiftsLoading" data-testid="shifts-table">
          <template #active-cell="{ row }">{{ row.original.active ? '在用' : '已停用' }}</template>
          <template #actions-cell="{ row }">
            <UButton v-if="row.original.active" size="xs" color="error" variant="ghost" @click="askDeactivate(row.original)">停用</UButton>
          </template>
          <template #empty>还没有班次</template>
        </UTable>
      </div>

      <div class="space-y-3">
        <h2 class="font-semibold">员工班次</h2>
        <UTable :data="staffShifts" :columns="staffShiftColumns" :loading="staffShiftsLoading" :aria-busy="staffShiftsLoading" data-testid="staff-shifts-table">
          <template #staff-cell="{ row }">{{ row.original.staff.name }}{{ row.original.active ? '' : '（离职）' }}</template>
          <template #current-cell="{ row }">{{ row.original.current ? shiftText(row.original.current.shift) : '未指定' }}</template>
          <template #upcoming-cell="{ row }">
            <div v-for="upcoming in row.original.upcoming" :key="`${upcoming.effective_from}:${upcoming.shift?.id ?? 'none'}`" class="text-sm">
              {{ shortDate(upcoming.effective_from) }}起 {{ shiftText(upcoming.shift) }}
            </div>
          </template>
          <template #actions-cell="{ row }">
            <UButton v-if="row.original.active" size="xs" variant="ghost" @click="askAssign(row.original)">指定班次</UButton>
          </template>
          <template #empty>还没有员工</template>
        </UTable>
      </div>
    </section>

    <UModal v-model:open="addPunchOpen" title="补卡">
      <template #body>
        <div class="space-y-3">
          <UFormField label="员工"><USelect v-model="punchForm.staff_id" :items="addPunchStaffItems" :disabled="!!addPunchTarget" aria-label="员工" class="w-full" /></UFormField>
          <UFormField label="日期" help="按门店时间填写，不能晚于现在"><UInput v-model="punchForm.date" type="date" :max="calendarToday" class="w-full" /></UFormField>
          <UFormField label="时间"><UInput v-model="punchForm.time" type="time" class="w-full" /></UFormField>
          <UFormField label="原因"><UInput v-model="punchForm.reason" :maxlength="100" class="w-full" /></UFormField>
          <p class="text-sm text-muted">在营业日结束时间前补卡，会算到前一个营业日。</p>
        </div>
      </template>
      <template #footer><UButton :loading="addPunchSaving" @click="savePunch">保存</UButton></template>
    </UModal>

    <UModal v-model:open="createShiftOpen" title="新建班次">
      <template #body>
        <div class="space-y-3">
          <UFormField label="名称"><UInput v-model="newShift.name" :maxlength="20" class="w-full" /></UFormField>
          <UFormField label="上班时间"><UInput v-model="newShift.start_time" type="time" class="w-full" /></UFormField>
          <UFormField label="下班时间"><UInput v-model="newShift.end_time" type="time" class="w-full" /></UFormField>
          <p class="text-sm text-muted">上班时间要早于下班时间，不跨午夜。班次建好后不能改时间，要改就新建一个再改指定。</p>
        </div>
      </template>
      <template #footer><UButton :loading="createShiftSaving" @click="saveShift">保存</UButton></template>
    </UModal>

    <UModal v-model:open="deactivateOpen" :title="`停用班次「${deactivateTarget?.name ?? ''}」？`">
      <template #body>
        <p>已经指定的员工照常执行，直到改指定；停用后不能再指定给员工。</p>
      </template>
      <template #footer>
        <UButton color="neutral" variant="outline" @click="deactivateOpen = false">取消</UButton>
        <UButton color="error" :loading="deactivateSaving" @click="deactivateShift">确认停用</UButton>
      </template>
    </UModal>

    <UModal v-model:open="assignOpen" :title="`指定班次：${assignTarget?.staff.name ?? ''}`">
      <template #body>
        <div class="space-y-3">
          <p>当前班次：{{ currentShiftText }}</p>
          <UFormField label="班次"><USelect v-model="assign.shift_id" :items="activeShiftItems" placeholder="选择班次" aria-label="班次" class="w-full" /></UFormField>
          <UFormField label="生效日期" :help="`不能早于今天（${today}）`"><UInput v-model="assign.effective_from" type="date" :min="today" class="w-full" /></UFormField>
          <p class="text-sm text-muted">从这一天起执行，之前的日子仍按原来的班次计算</p>
        </div>
      </template>
      <template #footer><UButton :loading="assignSaving" :disabled="!assign.shift_id" @click="saveAssign">保存</UButton></template>
    </UModal>
  </div>
</template>
