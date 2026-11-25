<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'booking.manage', title: '预约', workbench: true, art: 'calendar' })

type Appointment = components['schemas']['Appointment']
type AppointmentEvent = components['schemas']['AppointmentEvent']
type Actor = components['schemas']['AppointmentActor']
type Customer = components['schemas']['Customer']
type Item = components['schemas']['Item']
type StaffOption = components['schemas']['StaffOption']

const api = useApi()
const toast = useToast()

const SLOT_MINUTES = 15
const SLOT_HEIGHT = 24
const TIME_ZONE = 'Asia/Shanghai'

const statusLabels: Record<Appointment['status'], string> = {
  pending: '待确认',
  confirmed: '已确认',
  completed: '完成',
  cancelled: '已取消',
  no_show: '爽约'
}
const kindLabels: Record<Appointment['kind'], string> = { appointment: '预约', block: '私约占位' }
const eventLabels: Record<AppointmentEvent['kind'], string> = {
  create: '创建',
  reschedule: '改约',
  confirm: '确认',
  complete: '完成',
  cancel: '取消',
  no_show: '爽约'
}

// 预约块（样稿 03）：平时全白；正在做的灰底、左边一道墨线；过了开始时间还没开单的黄底，写「打个电话」；做完的淡下去。
// 状态同时写在块里的文字上，不只靠颜色（DESIGN.md「颜色只留给有事的东西」）。
function entryClass(entry: Appointment) {
  if (entry.kind === 'block') return 'hatch text-muted shadow-line'
  if (entry.order_id && entry.status !== 'completed') return 'entry entry-serving'
  if (lateMinutes(entry)) return 'entry entry-late'
  if (entry.status === 'pending') return 'entry bg-white ring-pending'
  if (entry.status === 'no_show') return 'entry bg-white ring-alert'
  if (entry.status === 'cancelled') return 'entry bg-white text-dimmed line-through'
  if (entry.status === 'completed') return 'entry bg-white text-muted'
  return 'entry bg-white'
}

// 过了开始时间 5 分钟还没开单的（只看今天），和首页「接下来」同一个口径。
function lateMinutes(entry: Appointment) {
  if (entry.kind !== 'appointment' || entry.order_id || day.value !== businessDate.value) return 0
  if (entry.status !== 'pending' && entry.status !== 'confirmed') return 0
  const late = Math.floor((nowClock.value - new Date(entry.starts_at).getTime()) / 60_000)
  return late > 5 ? late : 0
}

// 块里第二行：项目 · 现在怎样（确认过、还没到点的不写状态）；私约占位写备注。
function entryLine(entry: Appointment) {
  if (entry.kind === 'block') return entry.note ?? ''
  let state = ''
  if (entry.order_id && entry.status !== 'completed') state = '正在做'
  else if (lateMinutes(entry)) state = '打个电话'
  else if (entry.status === 'completed') state = entry.order_id ? '已结账' : '完成'
  else if (entry.status !== 'confirmed') state = statusLabels[entry.status]
  return [entryItems(entry), state].filter(Boolean).join(' · ')
}

const cancelReasonItems = [
  { label: '顾客取消', value: '顾客取消' },
  { label: '门店取消', value: '门店取消' },
  { label: '改约', value: '改约' },
  { label: '其他', value: '其他' }
]

const businessDate = ref('')
const day = ref('')
const hours = ref({ open: '09:00', close: '22:00' })
const staff = ref<StaffOption[]>([])
const items = ref<Item[]>([])
const entries = ref<Appointment[]>([])
const loading = ref(false)
const showCancelled = ref(true)
const dayGuard = useRequestGuard()
const initGuard = useRequestGuard()
const todayGuard = useRequestGuard()

function clockText(total: number) {
  const hour = Math.floor(total / 60) % 24
  const minute = total % 60
  return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`
}

function clockMinutes(value: string) {
  const [hour, minute] = value.split(':').map(Number)
  return (hour ?? 0) * 60 + (minute ?? 0)
}

function shanghai(iso: string) {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: TIME_ZONE,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23'
  }).formatToParts(new Date(iso))
  const part = (type: string) => parts.find(p => p.type === type)?.value ?? ''
  return { date: `${part('year')}-${part('month')}-${part('day')}`, time: `${part('hour')}:${part('minute')}` }
}

function clockOf(iso: string) {
  return shanghai(iso).time
}

function dateTimeText(iso: string) {
  const value = shanghai(iso)
  return `${value.date} ${value.time}`
}

function isoAt(date: string, time: string) {
  return `${date}T${time}:00+08:00`
}

const openMinutes = computed(() => clockMinutes(hours.value.open))
const closeMinutes = computed(() => clockMinutes(hours.value.close))
const slots = computed(() => {
  const out: { minutes: number, label: string, top: number }[] = []
  for (let minutes = openMinutes.value; minutes < closeMinutes.value; minutes += SLOT_MINUTES) {
    out.push({ minutes, label: clockText(minutes), top: ((minutes - openMinutes.value) / SLOT_MINUTES) * SLOT_HEIGHT })
  }
  return out
})
const hourSlots = computed(() => slots.value.filter(slot => slot.minutes % 60 === 0))
const gridHeight = computed(() => slots.value.length * SLOT_HEIGHT)
const staffColumns = computed(() => {
  const byID = new Map<string, StaffOption>()
  for (const person of staff.value) {
    if (person.active) byID.set(person.id, person)
  }
  for (const entry of entries.value) {
    if (!byID.has(entry.staff.id)) byID.set(entry.staff.id, { id: entry.staff.id, name: entry.staff.name, active: false })
  }
  return [...byID.values()]
})
const bookableItems = computed(() => items.value.filter(item => item.active && item.kind === 'service' && item.bookable))
const activeStaffItems = computed(() => staff.value.filter(person => person.active).map(person => ({ label: person.name, value: person.id })))

// 页头的「约了 N 位 · N 位没到」：没取消的预约，私约占位不算。
const dayBooked = computed(() => [...bookedCount.value.values()].reduce((sum, n) => sum + n, 0))
const dayLate = computed(() => entries.value.filter(entry => lateMinutes(entry)).length)

// 列头的「N 约」：这位员工当天没取消的预约数，私约占位不算。
const bookedCount = computed(() => {
  const counts = new Map<string, number>()
  for (const entry of entries.value) {
    if (entry.kind === 'appointment' && entry.status !== 'cancelled') counts.set(entry.staff.id, (counts.get(entry.staff.id) ?? 0) + 1)
  }
  return counts
})

function visibleEntries(staffID: string) {
  return entries.value.filter(entry => entry.staff.id === staffID && (showCancelled.value || entry.status !== 'cancelled'))
}

function entryStyle(entry: Appointment) {
  const start = Math.max(openMinutes.value, clockMinutes(clockOf(entry.starts_at)))
  const end = Math.max(start + 1, Math.min(closeMinutes.value, clockMinutes(clockOf(entry.ends_at))))
  return {
    top: `${((start - openMinutes.value) / SLOT_MINUTES) * SLOT_HEIGHT}px`,
    height: `${Math.max(18, ((end - start) / SLOT_MINUTES) * SLOT_HEIGHT - 2)}px`
  }
}

function entryTitle(entry: Appointment) {
  if (entry.kind === 'block') return '私约占位'
  const late = lateMinutes(entry)
  const name = entry.customer?.name ?? '未识别顾客'
  return late ? `${name} · 过了 ${late} 分钟没到` : name
}

function entryItems(entry: Appointment) {
  return entry.items.map(item => item.name).join(' + ')
}

async function loadOptions() {
  const current = initGuard.next()
  const [settingsResult, staffResult, itemsResult] = await Promise.all([
    api.POST('/api/ops/get_settings', { body: {} }),
    api.POST('/api/ops/list_staff_options', { body: {} }),
    api.POST('/api/ops/list_items', { body: {} })
  ])
  if (!current()) return
  if (!settingsResult.data || !staffResult.data || !itemsResult.data) {
    toast.add({ title: errorText(settingsResult.error ?? staffResult.error ?? itemsResult.error), color: 'error' })
    return
  }
  businessDate.value = settingsResult.data.business_date
  staff.value = staffResult.data.staff
  items.value = itemsResult.data.items
  day.value = businessDate.value
}

async function loadDay() {
  if (!day.value) return
  const current = dayGuard.next()
  loading.value = true
  try {
    const [appointmentResult, settingsResult] = await Promise.all([
      api.POST('/api/ops/list_appointments', { body: { from: day.value, to: day.value } }),
      api.POST('/api/ops/get_settings', { body: { date: day.value } })
    ])
    if (!current()) return
    if (!appointmentResult.data || !settingsResult.data) {
      toast.add({ title: errorText(appointmentResult.error ?? settingsResult.error), color: 'error' })
      return
    }
    entries.value = appointmentResult.data.entries
    const hoursSetting = settingsResult.data.settings.find(setting => setting.key === 'business_hours')
    if (hoursSetting?.value) hours.value = hoursSetting.value as { open: string, close: string }
  } finally {
    if (current()) loading.value = false
  }
}

function moveDay(delta: number) {
  day.value = addDays(day.value, delta)
}

async function goToday() {
  const current = todayGuard.next()
  const { data, error } = await api.POST('/api/ops/get_settings', { body: {} })
  if (!current()) return
  if (data) day.value = data.business_date
  else toast.add({ title: errorText(error), color: 'error' })
}

watch(day, () => {
  entries.value = []
  void loadDay()
})

const dayTitle = computed(() => {
  if (!day.value) return ''
  const [year, month, date] = day.value.split('-').map(Number)
  const sameYear = businessDate.value.slice(0, 4) === day.value.slice(0, 4)
  return `${sameYear ? '' : `${year}年`}${month}月${date}日 ${weekdayOf(day.value)}`
})

// 当前时间线和打开时的滚动位置：今天滚到现在，别的日子滚到第一条预约。
const nowClock = ref(Date.now())
const nowTop = computed(() => {
  if (!day.value || day.value !== businessDate.value) return null
  const minutes = clockMinutes(shanghai(new Date(nowClock.value).toISOString()).time)
  if (minutes < openMinutes.value || minutes >= closeMinutes.value) return null
  return ((minutes - openMinutes.value) / SLOT_MINUTES) * SLOT_HEIGHT
})
const nowText = computed(() => clockOf(new Date(nowClock.value).toISOString()))
const grid = useTemplateRef<HTMLElement>('grid')
let scrolledFor = ''
watch(loading, async (busy) => {
  if (busy || scrolledFor === day.value || !grid.value) return
  scrolledFor = day.value
  await nextTick()
  const first = entries.value.filter(entry => entry.status !== 'cancelled').map(entry => clockMinutes(clockOf(entry.starts_at))).sort((a, b) => a - b)[0]
  const target = nowTop.value ?? (first === undefined ? 0 : ((first - openMinutes.value) / SLOT_MINUTES) * SLOT_HEIGHT)
  grid.value.scrollTop = Math.max(0, target - SLOT_HEIGHT * 4)
})
let clockTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  clockTimer = setInterval(() => {
    nowClock.value = Date.now()
  }, 60_000)
})
onBeforeUnmount(() => clearInterval(clockTimer))

onMounted(loadOptions)

const chooserOpen = ref(false)
const chooser = reactive({ staff_id: '', time: '' })
const chooserStaffName = computed(() => staff.value.find(person => person.id === chooser.staff_id)?.name ?? '')

function chooseSlot(staffID: string, time: string) {
  chooser.staff_id = staffID
  chooser.time = time
  chooserOpen.value = true
}

const bookingEditor = useModalForm()
const { open: bookingOpen, saving: bookingSaving } = bookingEditor
const bookingDay = ref('')
const booking = reactive({
  staff_id: '',
  start_time: '',
  end_time: '',
  item_ids: [] as string[],
  customer_name: '',
  customer_phone: '',
  note: ''
})
const selectedCustomer = ref<Customer | null>(null)
const customerQuery = ref('')
const customerResults = ref<Customer[]>([])
const customerSearching = ref(false)
const customerGuard = useRequestGuard()

function openBooking(staffID: string, time: string) {
  bookingEditor.begin(() => {
    bookingDay.value = day.value
    Object.assign(booking, {
      staff_id: staffID,
      start_time: time,
      end_time: '',
      item_ids: [],
      customer_name: '',
      customer_phone: '',
      note: ''
    })
    customerGuard.next()
    customerSearching.value = false
    selectedCustomer.value = null
    customerQuery.value = ''
    customerResults.value = []
  })
}

// 顶栏「新建预约」：默认第一位在职发型师、下一个整刻钟，弹窗里都能改。
// 今天已经打烊，或已过午夜（营业日还没切换）时，默认约到下一天开门。
function newBooking() {
  const first = activeStaffItems.value[0]
  if (!first) {
    toast.add({ title: '还没有在职的发型师', description: '先到「员工」页添加员工，再来约', color: 'warning' })
    return
  }
  let minutes = openMinutes.value
  if (day.value === businessDate.value) {
    const now = shanghai(new Date(nowClock.value).toISOString())
    const next = Math.max(minutes, Math.ceil(clockMinutes(now.time) / SLOT_MINUTES) * SLOT_MINUTES)
    if (now.date !== day.value || next > closeMinutes.value - SLOT_MINUTES) day.value = addDays(day.value, 1)
    else minutes = next
  }
  openBooking(first.value, clockText(minutes))
}

// 这天一个有效预约都没有时，网格上浮一句提示，告诉人从哪儿开始。
const dayEmpty = computed(() => !loading.value && !entries.value.some(entry => entry.status !== 'cancelled'))

function chooseBooking() {
  chooserOpen.value = false
  openBooking(chooser.staff_id, chooser.time)
}

function itemChecked(itemID: string) {
  return booking.item_ids.includes(itemID)
}

function toggleItem(itemID: string, value: boolean | 'indeterminate') {
  booking.item_ids = value === true ? [...booking.item_ids, itemID] : booking.item_ids.filter(id => id !== itemID)
}

async function searchCustomers() {
  const current = customerGuard.next()
  customerSearching.value = true
  try {
    const { data, error } = await api.POST('/api/ops/search_customers', { body: { query: customerQuery.value, limit: 10 } })
    if (!current()) return
    if (data) customerResults.value = data.items
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) customerSearching.value = false
  }
}

function pickCustomer(customer: Customer) {
  selectedCustomer.value = customer
  customerResults.value = []
  booking.customer_name = ''
  booking.customer_phone = ''
}

function clearCustomer() {
  selectedCustomer.value = null
}

async function saveBooking() {
  await bookingEditor.save(async (current) => {
    const body: components['schemas']['CreateAppointmentInput'] = {
      staff_id: booking.staff_id,
      starts_at: isoAt(bookingDay.value, booking.start_time),
      items: booking.item_ids.map(itemID => ({ item_id: itemID }))
    }
    if (booking.end_time) body.ends_at = isoAt(bookingDay.value, booking.end_time)
    if (booking.note.trim()) body.note = booking.note.trim()
    if (selectedCustomer.value) {
      body.customer_id = selectedCustomer.value.id
    } else {
      if (booking.customer_name.trim()) body.customer_name = booking.customer_name.trim()
      if (booking.customer_phone.trim()) body.customer_phone = booking.customer_phone.trim()
    }
    const { data, error } = await api.POST('/api/ops/create_appointment', { body })
    if (!data) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void loadDay()
    if (!current()) return
    bookingOpen.value = false
    toast.add({ title: '已保存', color: 'success' })
  })
}

const blockEditor = useModalForm()
const { open: blockOpen, saving: blockSaving } = blockEditor
const blockForm = reactive({ staff_id: '', start_time: '', end_time: '', note: '' })
const blockDay = ref('')

function openBlock(staffID: string, time: string) {
  blockEditor.begin(() => {
    blockDay.value = day.value
    const start = clockMinutes(time)
    const end = Math.min(closeMinutes.value, start + 60)
    Object.assign(blockForm, { staff_id: staffID, start_time: time, end_time: clockText(end > start ? end : start + 30), note: '' })
  })
}

function chooseBlock() {
  chooserOpen.value = false
  openBlock(chooser.staff_id, chooser.time)
}

async function saveBlock() {
  await blockEditor.save(async (current) => {
    const body: components['schemas']['CreateBlockInput'] = {
      staff_id: blockForm.staff_id,
      starts_at: isoAt(blockDay.value, blockForm.start_time),
      ends_at: isoAt(blockDay.value, blockForm.end_time)
    }
    if (blockForm.note.trim()) body.note = blockForm.note.trim()
    const { data, error } = await api.POST('/api/ops/create_block', { body })
    if (!data) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void loadDay()
    if (!current()) return
    blockOpen.value = false
    toast.add({ title: '已保存', color: 'success' })
  })
}

const detailEditor = useModalForm()
const { open: detailOpen, loading: detailLoading, saving: detailSaving } = detailEditor
const detail = ref<Appointment>()
const detailID = ref('')
const now = ref(Date.now())
let nowTimer: ReturnType<typeof setInterval> | undefined

function openDetail(entry: Appointment) {
  detailEditor.begin(() => {
    detailID.value = entry.id
    detail.value = undefined
  })
  void loadDetail(entry.id)
}

async function loadDetail(id: string) {
  await detailEditor.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/get_appointment', { body: { id } })
    if (!current()) return
    if (data) detail.value = data
    else toast.add({ title: errorText(error), color: 'error' })
  })
}

watch(detailOpen, (value) => {
  if (nowTimer) clearInterval(nowTimer)
  nowTimer = undefined
  if (value) {
    now.value = Date.now()
    nowTimer = setInterval(() => {
      now.value = Date.now()
    }, 30_000)
  }
})

onBeforeUnmount(() => {
  if (nowTimer) clearInterval(nowTimer)
})

const noShowAllowed = computed(() => {
  if (detail.value?.kind !== 'appointment' || detail.value.status !== 'confirmed') return false
  return now.value >= new Date(detail.value.starts_at).getTime()
})

function refreshDetail(id: string) {
  if (detailOpen.value && detailID.value === id) void loadDetail(id)
}

async function confirmAppointment() {
  const id = detail.value?.id
  if (!id) return
  await detailEditor.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/confirm_appointment', { body: { id } })
    if (!data) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void loadDay()
    refreshDetail(id)
    if (!current()) return
    toast.add({ title: '已确认', color: 'success' })
  })
}

async function markNoShow() {
  const id = detail.value?.id
  if (!id) return
  await detailEditor.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/mark_no_show', { body: { id } })
    if (!data) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void loadDay()
    refreshDetail(id)
    if (!current()) return
    toast.add({ title: '已记爽约', color: 'success' })
  })
}

async function cancelBlock() {
  const id = detail.value?.id
  if (!id) return
  await detailEditor.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/cancel_block', { body: { id } })
    if (!data) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void loadDay()
    refreshDetail(id)
    if (!current()) return
    toast.add({ title: '已取消占位', color: 'success' })
  })
}

const rescheduleEditor = useModalForm()
const { open: rescheduleOpen, saving: rescheduleSaving } = rescheduleEditor
const reschedule = reactive({ date: '', start_time: '', end_time: '', staff_id: '', reason: '' })

function askReschedule() {
  if (!detail.value) return
  rescheduleEditor.begin(() => {
    Object.assign(reschedule, {
      date: shanghai(detail.value!.starts_at).date,
      start_time: clockOf(detail.value!.starts_at),
      end_time: '',
      staff_id: '',
      reason: ''
    })
  })
}

async function saveReschedule() {
  const id = detail.value?.id
  if (!id) return
  await rescheduleEditor.save(async (current) => {
    const body: components['schemas']['RescheduleAppointmentInput'] = {
      id,
      starts_at: isoAt(reschedule.date, reschedule.start_time)
    }
    if (reschedule.end_time) body.ends_at = isoAt(reschedule.date, reschedule.end_time)
    if (reschedule.staff_id) body.staff_id = reschedule.staff_id
    if (reschedule.reason.trim()) body.reason = reschedule.reason.trim()
    const { data, error } = await api.POST('/api/ops/reschedule_appointment', { body })
    if (!data) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void loadDay()
    refreshDetail(id)
    if (!current()) return
    rescheduleOpen.value = false
    toast.add({ title: '已改约', color: 'success' })
  })
}

const cancelEditor = useModalForm()
const { open: cancelOpen, saving: cancelSaving } = cancelEditor
const cancel = reactive({ reason: '顾客取消', note: '' })
const cancelDisabled = computed(() => cancel.reason === '其他' && !cancel.note.trim())

function askCancel() {
  cancelEditor.begin(() => {
    Object.assign(cancel, { reason: '顾客取消', note: '' })
  })
}

async function saveCancel() {
  const id = detail.value?.id
  if (!id) return
  await cancelEditor.save(async (current) => {
    const body: components['schemas']['CancelAppointmentInput'] = { id, reason: cancel.reason as components['schemas']['CancelAppointmentInput']['reason'] }
    if (cancel.note.trim()) body.note = cancel.note.trim()
    const { data, error } = await api.POST('/api/ops/cancel_appointment', { body })
    if (!data) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void loadDay()
    refreshDetail(id)
    if (!current()) return
    cancelOpen.value = false
    toast.add({ title: '已取消', color: 'success' })
  })
}

const completeEditor = useModalForm()
const { open: completeOpen, saving: completeSaving } = completeEditor
const complete = reactive({ order_id: '' })

function askComplete() {
  completeEditor.begin(() => {
    complete.order_id = ''
  })
}

async function saveComplete() {
  const id = detail.value?.id
  if (!id) return
  await completeEditor.save(async (current) => {
    const body: components['schemas']['CompleteAppointmentInput'] = { id }
    if (complete.order_id.trim()) body.order_id = complete.order_id.trim()
    const { data, error } = await api.POST('/api/ops/complete_appointment', { body })
    if (!data) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    void loadDay()
    refreshDetail(id)
    if (!current()) return
    completeOpen.value = false
    toast.add({ title: '已完成', color: 'success' })
  })
}

function eventRange(event: AppointmentEvent) {
  if (!event.from_starts_at || !event.from_ends_at) return ''
  return `${dateTimeText(event.from_starts_at)}–${dateTimeText(event.from_ends_at)}`
}

function eventToRange(event: AppointmentEvent) {
  return `${dateTimeText(event.to_starts_at)}–${dateTimeText(event.to_ends_at)}`
}

function eventStaff(event: AppointmentEvent) {
  if (!event.from_staff) return event.to_staff.name
  return `${event.from_staff.name} → ${event.to_staff.name}`
}

function actorLabel(actor: Actor) {
  if (actor.kind !== 'customer') return actor.name
  return actor.name === '顾客本人' ? '顾客本人' : `顾客 ${actor.name}`
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton size="sm" icon="i-lucide-plus" @click="newBooking">新建预约</UButton>
    </PageActions>
    <div class="flex flex-wrap items-center gap-2">
      <div class="flex items-center">
        <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" aria-label="前一天" @click="moveDay(-1)" />
        <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" aria-label="后一天" @click="moveDay(1)" />
      </div>
      <h2 class="figure" data-testid="booking-day-title">{{ dayTitle }}</h2>
      <p class="flex items-center gap-2 text-meta text-muted tabular-nums" data-testid="booking-day-summary">
        {{ dayBooked ? `约了 ${dayBooked} 位` : '还没有人约' }}
        <span v-if="dayLate" class="tag bg-tag-yellow">{{ dayLate }} 位没到</span>
      </p>
      <span v-if="day && day === businessDate" class="tag bg-tag-gray">今天</span>
      <UButton v-else color="neutral" variant="outline" size="sm" @click="goToday">今天</UButton>
      <DateInput v-model="day" size="sm" aria-label="营业日" class="ms-2 w-36" />
      <USwitch v-model="showCancelled" size="sm" label="显示已取消" class="ms-auto" />
      <p v-if="loading" class="text-sm text-muted" aria-live="polite">加载中</p>
    </div>

    <div v-if="businessDate" ref="grid" class="box h-[calc(100dvh-19.5rem)] min-h-96 overflow-auto p-0" data-booking-grid>
      <div v-if="dayEmpty" class="pointer-events-none sticky top-16 left-0 z-30 flex h-0 justify-center">
        <p class="h-fit rounded-md bg-white px-3 py-2 text-meta text-muted shadow-paper" data-testid="booking-day-empty">这天还没有预约。点空白时段，或右上角「新建预约」</p>
      </div>
      <div class="flex min-w-max">
        <div class="w-14 shrink-0">
          <div class="sticky top-0 z-20 h-11 border-b border-default bg-white" />
          <div class="relative" :style="{ height: `${gridHeight}px` }">
            <div v-if="nowTop !== null" class="absolute right-2 z-10 -translate-y-1/2 bg-white text-xs font-medium text-ink-red tabular-nums" :style="{ top: `${nowTop}px` }" data-testid="booking-now">{{ nowText }}</div>
            <div
              v-for="slot in hourSlots"
              :key="slot.minutes"
              class="absolute right-2 text-xs text-dimmed tabular-nums"
              :class="slot.top === 0 ? 'translate-y-0.5' : '-translate-y-1/2'"
              :style="{ top: `${slot.top}px` }"
            >
              {{ slot.label }}
            </div>
          </div>
        </div>
        <div v-for="person in staffColumns" :key="person.id" class="min-w-40 flex-1 border-l border-default" :data-staff-column="person.id">
          <div class="sticky top-0 z-20 flex h-11 items-center gap-2 border-b border-default bg-white px-2" :title="person.name" data-testid="booking-column-head">
            <PersonAvatar :name="person.name" size="sm" />
            <span class="min-w-0 truncate text-sm font-medium">{{ person.name }}{{ person.active ? '' : '（已停用）' }}</span>
            <span class="ms-auto shrink-0 text-meta tabular-nums" :class="bookedCount.get(person.id) ? 'text-muted' : 'text-dimmed'">{{ bookedCount.get(person.id) ?? 0 }} 约</span>
          </div>
          <div class="relative" :style="{ height: `${gridHeight}px` }">
            <template v-if="person.active">
              <button
                v-for="slot in slots"
                :key="slot.minutes"
                type="button"
                class="absolute inset-x-0 border-t hover:bg-hover"
                :class="slot.minutes % 60 === 0 ? 'border-default' : 'border-default/40'"
                :style="{ top: `${slot.top}px`, height: `${SLOT_HEIGHT}px` }"
                :data-slot-staff="person.id"
                :data-slot-time="slot.label"
                :aria-label="`${person.name} ${slot.label} 新增`"
                @click="chooseSlot(person.id, slot.label)"
              />
            </template>
            <div v-if="nowTop !== null" class="pointer-events-none absolute inset-x-0 z-10 border-t border-ink-red" :style="{ top: `${nowTop}px` }" aria-hidden="true" />
            <button
              v-for="entry in visibleEntries(person.id)"
              :key="entry.id"
              type="button"
              class="absolute inset-x-1 z-10 flex flex-col justify-start overflow-hidden rounded-md px-2 py-1 text-left text-xs text-ink"
              :title="`${clockOf(entry.starts_at)}–${clockOf(entry.ends_at)}`"
              :class="entryClass(entry)"
              :style="entryStyle(entry)"
              :data-entry="entry.id"
              :data-entry-status="entry.status"
              :data-entry-kind="entry.kind"
              @click.stop="openDetail(entry)"
            >
              <span class="block truncate text-sm font-medium">{{ entryTitle(entry) }}</span>
              <span v-if="entryLine(entry)" class="block truncate" :class="entry.status === 'no_show' ? 'text-ink-red' : 'text-muted'">{{ entryLine(entry) }}</span>
              <span class="sr-only">{{ clockOf(entry.starts_at) }}–{{ clockOf(entry.ends_at) }} · {{ statusLabels[entry.status] }}</span>
            </button>
          </div>
        </div>
        <div v-if="!staffColumns.length" class="p-6 text-sm text-muted">还没有可在职员工</div>
      </div>
    </div>

    <UModal v-model:open="chooserOpen" title="新增预约" :description="`${chooserStaffName} · ${day} ${chooser.time}`">
      <template #body>
        <!-- 两个去向做成整行选项（照 Fresha 的新增菜单）：图标、名称、一句说明。 -->
        <div class="-my-2 space-y-2">
          <button
            v-for="choice in [
              { label: '代约', hint: '替顾客约这个时段，到店后开单', icon: 'i-lucide-calendar-plus', pick: chooseBooking },
              { label: '私约占位', hint: '员工自己约的客或私事，先占住时段', icon: 'i-lucide-lock', pick: chooseBlock }
            ]"
            :key="choice.label"
            type="button"
            :aria-label="choice.label"
            :aria-describedby="`chooser-${choice.label}`"
            class="flex w-full items-center gap-4 rounded-md px-4 py-4 text-left ring-1 ring-accented ring-inset hover:bg-hover focus-visible:outline-0 focus-visible:ring-inverted"
            @click="choice.pick"
          >
            <UIcon :name="choice.icon" class="size-5 shrink-0 text-muted" aria-hidden="true" />
            <span class="min-w-0 flex-1">
              <span class="block text-sm font-semibold text-highlighted">{{ choice.label }}</span>
              <span :id="`chooser-${choice.label}`" class="mt-0.5 block text-sm text-muted">{{ choice.hint }}</span>
            </span>
            <UIcon name="i-lucide-chevron-right" class="size-4 shrink-0 text-dimmed" aria-hidden="true" />
          </button>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="bookingOpen" title="代约" :description="`日期 ${bookingDay}`">
      <template #body>
        <div class="space-y-5">
          <UFormField label="发型师"><USelect v-model="booking.staff_id" :items="activeStaffItems" aria-label="发型师" class="w-full" /></UFormField>
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="开始时间"><UInput v-model="booking.start_time" type="time" aria-label="开始时间" class="w-full" /></UFormField>
            <UFormField label="结束时间" help="不填按项目时长或门店默认时长"><UInput v-model="booking.end_time" type="time" aria-label="结束时间" class="w-full" /></UFormField>
          </div>
          <UFormField label="项目">
            <div v-if="bookableItems.length" class="space-y-1">
              <UCheckbox
                v-for="item in bookableItems"
                :key="item.id"
                :model-value="itemChecked(item.id)"
                :label="`${item.name}（${item.duration_minutes || '未设置时长'}${item.duration_minutes ? ' 分钟' : ''}）`"
                :aria-label="item.name"
                @update:model-value="value => toggleItem(item.id, value)"
              />
            </div>
            <p v-else class="text-sm text-muted">没有可预约的服务项目</p>
          </UFormField>
          <UFormField label="顾客">
            <div v-if="selectedCustomer" class="flex items-center gap-2 rounded-card bg-side p-2">
              <span class="flex-1 text-sm">已选顾客：{{ selectedCustomer.name ?? '未识别顾客' }}{{ selectedCustomer.phone ? ` · ${selectedCustomer.phone}` : '' }}</span>
              <UButton size="xs" variant="ghost" @click="clearCustomer">更换</UButton>
            </div>
            <div v-else class="space-y-2">
              <div class="flex gap-2">
                <UInput v-model="customerQuery" aria-label="搜索顾客" placeholder="姓名或手机号" class="flex-1" @keyup.enter="searchCustomers" />
                <UButton :loading="customerSearching" variant="outline" @click="searchCustomers">搜索</UButton>
              </div>
              <div v-if="customerResults.length" class="space-y-1 rounded-card bg-side p-2" data-customer-results>
                <UButton v-for="customer in customerResults" :key="customer.id" size="xs" variant="ghost" class="w-full justify-start" @click="pickCustomer(customer)">
                  {{ customer.name ?? '未识别顾客' }}{{ customer.phone ? ` · ${customer.phone}` : '' }}
                </UButton>
              </div>
              <div class="grid grid-cols-2 gap-2">
                <UInput v-model="booking.customer_name" aria-label="姓名" placeholder="姓名" />
                <UInput v-model="booking.customer_phone" aria-label="手机号" placeholder="手机号" />
              </div>
            </div>
          </UFormField>
          <UFormField label="备注"><UInput v-model="booking.note" :maxlength="200" aria-label="备注" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="bookingOpen = false">
          <UButton :loading="bookingSaving" @click="saveBooking">保存</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="blockOpen" title="私约占位" :description="`日期 ${blockDay}`">
      <template #body>
        <div class="space-y-5">
          <UFormField label="员工"><USelect v-model="blockForm.staff_id" :items="activeStaffItems" aria-label="员工" class="w-full" /></UFormField>
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="开始时间"><UInput v-model="blockForm.start_time" type="time" aria-label="开始时间" class="w-full" /></UFormField>
            <UFormField label="结束时间"><UInput v-model="blockForm.end_time" type="time" aria-label="结束时间" class="w-full" /></UFormField>
          </div>
          <UFormField label="备注"><UInput v-model="blockForm.note" :maxlength="200" aria-label="备注" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="blockOpen = false">
          <UButton :loading="blockSaving" @click="saveBlock">保存</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="detailOpen" title="预约详情">
      <template #body>
        <p v-if="detailLoading" class="text-sm text-muted">加载中</p>
        <div v-else-if="detail" class="max-h-[70vh] space-y-4 overflow-auto" data-appointment-detail :data-detail-id="detail.id" :data-detail-status="detail.status">
          <div class="grid grid-cols-2 gap-2 text-sm">
            <p>类型：{{ kindLabels[detail.kind] }}</p>
            <p>状态：{{ statusLabels[detail.status] }}</p>
            <p class="col-span-2">顾客：<span data-detail-customer>{{ detail.kind === 'block' ? '私约占位' : (detail.customer?.name ?? '未识别顾客') }}</span><span v-if="detail.customer?.phone"> · {{ detail.customer.phone }}</span></p>
            <p>发型师：{{ detail.staff.name }}</p>
            <p data-detail-created-by :data-actor-kind="detail.created_by.kind">创建人：{{ actorLabel(detail.created_by) }}</p>
            <p>开始：{{ dateTimeText(detail.starts_at) }}</p>
            <p>结束：{{ dateTimeText(detail.ends_at) }}</p>
            <p>创建时间：{{ dateTimeText(detail.created_at) }}</p>
            <p v-if="detail.order_id">关联单：{{ detail.order_id }}</p>
            <p v-if="detail.note" class="col-span-2">备注：{{ detail.note }}</p>
          </div>

          <div v-if="detail.items.length" class="space-y-1">
            <h2 class="font-medium">项目</h2>
            <p v-for="item in detail.items" :key="item.item_id" class="text-sm">{{ item.position }}. {{ item.name }} · {{ item.duration_minutes || '未设置' }}{{ item.duration_minutes ? ' 分钟' : '' }}</p>
          </div>

          <div class="flex flex-wrap gap-2">
            <UButton v-if="detail.kind === 'appointment' && detail.status === 'pending'" :loading="detailSaving" @click="confirmAppointment">确认</UButton>
            <UButton v-if="detail.kind === 'appointment' && (detail.status === 'pending' || detail.status === 'confirmed')" variant="outline" @click="askReschedule">改约</UButton>
            <UButton v-if="detail.kind === 'appointment' && detail.status === 'confirmed'" :loading="detailSaving" @click="askComplete">完成</UButton>
            <UButton v-if="detail.kind === 'appointment' && detail.status === 'confirmed' && noShowAllowed" color="error" variant="outline" :loading="detailSaving" @click="markNoShow">记爽约</UButton>
            <UButton v-if="detail.kind === 'appointment' && (detail.status === 'pending' || detail.status === 'confirmed')" color="error" variant="outline" @click="askCancel">取消</UButton>
            <UButton v-if="detail.kind === 'block' && detail.status === 'confirmed'" color="error" variant="outline" :loading="detailSaving" @click="cancelBlock">取消占位</UButton>
          </div>

          <div class="space-y-2">
            <h2 class="font-medium">状态变化</h2>
            <div v-for="event in detail.events" :key="event.id" class="rounded-card bg-side p-2 text-sm" data-event-row :data-event-kind="event.kind">
              <p class="font-medium" data-event-actor :data-actor-kind="event.actor.kind">{{ eventLabels[event.kind] }} · {{ dateTimeText(event.created_at) }} · {{ actorLabel(event.actor) }}</p>
              <p v-if="event.from_starts_at" class="text-muted">时间：{{ eventRange(event) }} → {{ eventToRange(event) }}</p>
              <p v-else class="text-muted">时间：{{ eventToRange(event) }}</p>
              <p v-if="event.from_staff" class="text-muted">发型师：{{ eventStaff(event) }}</p>
              <p v-if="event.reason" class="text-muted">原因：{{ event.reason }}</p>
              <p v-if="event.note" class="text-muted">说明：{{ event.note }}</p>
            </div>
          </div>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="rescheduleOpen" title="改约预约">
      <template #body>
        <div class="space-y-5">
          <UFormField label="开始日期"><DateInput v-model="reschedule.date" aria-label="改约日期" class="w-full" /></UFormField>
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="开始时间"><UInput v-model="reschedule.start_time" type="time" aria-label="开始时间" class="w-full" /></UFormField>
            <UFormField label="结束时间" help="不填保持原时长"><UInput v-model="reschedule.end_time" type="time" aria-label="结束时间" class="w-full" /></UFormField>
          </div>
          <UFormField label="发型师" help="不填保持原发型师"><USelect v-model="reschedule.staff_id" :items="activeStaffItems" placeholder="保持原发型师" aria-label="发型师" class="w-full" /></UFormField>
          <UFormField label="改约说明"><UInput v-model="reschedule.reason" :maxlength="200" aria-label="改约说明" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="rescheduleOpen = false">
          <UButton :loading="rescheduleSaving" @click="saveReschedule">保存</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="cancelOpen" title="取消预约">
      <template #body>
        <div class="space-y-5">
          <UFormField label="取消原因"><USelect v-model="cancel.reason" :items="cancelReasonItems" aria-label="取消原因" class="w-full" /></UFormField>
          <UFormField label="说明" :help="cancel.reason === '其他' ? '原因选其他时必须填写' : ''"><UInput v-model="cancel.note" :maxlength="200" aria-label="说明" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="cancelOpen = false">
          <UButton :disabled="cancelDisabled" :loading="cancelSaving" @click="saveCancel">确定</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="completeOpen" title="完成预约">
      <template #body>
        <UFormField label="关联单号" help="可填这张顾客的已结账单号；不填只完成预约">
          <UInput v-model="complete.order_id" placeholder="单号" aria-label="关联单号" class="w-full" />
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="completeOpen = false">
          <UButton :loading="completeSaving" @click="saveComplete">确定</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>

<style scoped>
/* 预约块：细边，左边一道竖线标状态（平时浅灰，正在做墨色，没到黄色）。 */
.entry {
  box-shadow: var(--shadow-line), inset 2px 0 0 var(--ui-border-accented);
}

.entry-serving {
  background: var(--ui-bg-elevated);
  box-shadow: var(--shadow-line), inset 3px 0 0 var(--color-ink);
}

.entry-late {
  background: var(--color-tag-yellow);
  box-shadow: inset 3px 0 0 var(--color-ink-yellow);
}
</style>
