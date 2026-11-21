<script setup lang="ts">
import type { components } from '~/utils/api'
import { homeLabels, homeMatches, type HomeKind } from '~/utils/home'
import type { HomeSwitcherOption } from '~/composables/useHomeSwitcher'

definePageMeta({ title: '首页' })

type BusinessReport = components['schemas']['BusinessReport']
type AttendanceDay = components['schemas']['AttendanceDay']

const api = useApi()
const toast = useToast()
const { me, can } = useMe()

const matches = computed(() => homeMatches(can))
const { active: switcherActive, options: switcherOptions, selected: selectedHome } = useHomeSwitcher()
const homeKey = computed(() => me.value ? `salon_home_${me.value.staff.id}` : '')
let ready = false

function readRemembered(): HomeKind | null {
  if (!homeKey.value) return null
  const value = localStorage.getItem(homeKey.value)
  return value === 'manager' || value === 'front' || value === 'personal' ? value : null
}

function syncSwitcher() {
  const options: HomeSwitcherOption[] = matches.value.map(kind => ({ label: homeLabels[kind], value: kind }))
  switcherOptions.value = options
  switcherActive.value = options.length > 1
}

function initSelected() {
  syncSwitcher()
  const remembered = readRemembered()
  selectedHome.value = remembered && matches.value.includes(remembered) ? remembered : (matches.value[0] ?? 'personal')
}

watch(selectedHome, (value) => {
  if (!ready || !homeKey.value || !matches.value.includes(value)) return
  localStorage.setItem(homeKey.value, value)
  if (value === 'manager') void loadReport()
})

const businessDate = ref('')
const settingsGuard = useRequestGuard()
const attendanceGuard = useRequestGuard()
const reportGuard = useRequestGuard()
const { clockIn: todayClockIn, punching, punch, remember: rememberClockIn } = usePunch()
const report = ref<BusinessReport | null>(null)
const lastWeekReport = ref<BusinessReport | null>(null)

function localDate() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}

const today = computed(() => businessDate.value || localDate())

const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour < 12) return '早上好'
  if (hour < 18) return '下午好'
  return '晚上好'
})

const dateLine = computed(() => {
  const [year, month, day] = today.value.split('-').map(Number)
  const date = new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, day ?? 1))
  const weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
  return `${month} 月 ${day} 日 ${weekdays[date.getUTCDay()]}`
})

function amount(value: string | number) {
  const n = Number(value || '0')
  return `¥${n.toLocaleString('zh-CN', {
    minimumFractionDigits: Number.isInteger(n) ? 0 : 2,
    maximumFractionDigits: 2
  })}`
}

function formatGuest(value: string | number) {
  return Number(value || '0').toLocaleString('zh-CN', { maximumFractionDigits: 1 })
}

function addDays(value: string, delta: number) {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, (day ?? 1) + delta)).toISOString().slice(0, 10)
}

const lastWeekHasData = computed(() => {
  const totals = lastWeekReport.value?.totals
  if (!totals) return false
  return [
    totals.service_revenue,
    totals.sales_revenue,
    totals.checkout_count,
    totals.guest_total,
    totals.card_principal,
    totals.card_bonus,
    totals.count_amount,
    totals.groupbuy_deduct,
    totals.debt_repayment,
    totals.new_debt,
    totals.aftersale_refund,
    totals.void_count
  ].some(value => Number(value) !== 0)
})

function moneyDelta(current: string | number, previous: string | number) {
  if (!lastWeekHasData.value) return '上周同日无数据'
  const difference = Number(current) - Number(previous)
  if (difference === 0) return '比上周同日 持平'
  return `比上周同日 ${difference > 0 ? '+' : '−'}${amount(Math.abs(difference))}`
}

function countDelta(current: string | number, previous: string | number, unit: string) {
  if (!lastWeekHasData.value) return '上周同日无数据'
  const difference = Number(current) - Number(previous)
  if (difference === 0) return '比上周同日 持平'
  return `比上周同日 ${difference > 0 ? '+' : '−'}${formatGuest(Math.abs(difference))} ${unit}`
}

const metrics = computed(() => {
  const totals = report.value?.totals
  const previous = lastWeekReport.value?.totals
  if (!totals) return []
  const currentCard = Number(totals.card_principal) + Number(totals.card_bonus)
  const previousCard = Number(previous?.card_principal ?? 0) + Number(previous?.card_bonus ?? 0)
  return [
    { label: '实收', value: amount(totals.service_revenue), compare: moneyDelta(totals.service_revenue, previous?.service_revenue ?? 0) },
    { label: '单数', value: String(totals.checkout_count), compare: countDelta(totals.checkout_count, previous?.checkout_count ?? 0, '单') },
    { label: '客数', value: formatGuest(totals.guest_total), compare: countDelta(totals.guest_total, previous?.guest_total ?? 0, '人') },
    { label: '卡扣', value: amount(currentCard), compare: moneyDelta(currentCard, previousCard) },
    { label: '办卡', value: amount(totals.sales_revenue), compare: moneyDelta(totals.sales_revenue, previous?.sales_revenue ?? 0) }
  ]
})

async function loadSettings() {
  const current = settingsGuard.next()
  const { data, error } = await api.POST('/api/ops/get_settings', { body: {} })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  businessDate.value = data.business_date
}

async function loadAttendance() {
  if (!can('attendance.clock')) return
  const current = attendanceGuard.next()
  const before = todayClockIn.value
  const month = (businessDate.value || localDate()).slice(0, 7)
  const { data, error } = await api.POST('/api/ops/list_my_attendance', { body: { month } })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  const day = data.days.find((row: AttendanceDay) => row.business_date === (businessDate.value || localDate()))
  // 请求期间在个人菜单里打过卡，就以那次打卡为准。
  if (todayClockIn.value !== before) return
  rememberClockIn(day?.clock_in ?? null)
}

async function loadReport() {
  if (!can('report.view') || !today.value) return
  const current = reportGuard.next()
  const lastWeekDay = addDays(today.value, -7)
  const [thisWeek, lastWeek] = await Promise.all([
    api.POST('/api/ops/get_business_report', { body: { from: today.value, to: today.value, granularity: 'day' } }),
    api.POST('/api/ops/get_business_report', { body: { from: lastWeekDay, to: lastWeekDay, granularity: 'day' } })
  ])
  if (!current()) return
  if (!thisWeek.data) {
    toast.add({ title: errorText(thisWeek.error), color: 'error' })
    return
  }
  report.value = thisWeek.data
  lastWeekReport.value = lastWeek.data ?? null
}

watch(matches, () => {
  if (ready) syncSwitcher()
})

onBeforeUnmount(() => {
  switcherActive.value = false
  switcherOptions.value = []
})

// 同步选好首页，第一次渲染就不会显示上一个登录者选的首页。
initSelected()

onMounted(async () => {
  ready = true
  await loadSettings()
  await Promise.all([loadAttendance(), loadReport()])
})
</script>

<template>
  <div class="mx-auto w-full max-w-5xl space-y-6">
    <div>
      <h1 data-testid="home-greeting" class="text-2xl font-bold text-balance">
        {{ greeting }}，{{ me?.staff.name }}
      </h1>
      <p class="mt-1 text-sm text-muted tabular-nums" data-testid="home-date">{{ dateLine }}</p>
    </div>

    <template v-if="selectedHome === 'personal'">
      <div v-if="can('attendance.clock')">
        <UButton v-if="!todayClockIn" data-testid="home-punch" :loading="punching" @click="punch">打卡</UButton>
        <p v-else data-testid="home-punch-time" class="text-sm text-muted">已打卡 {{ clockTime(todayClockIn) }}</p>
      </div>
    </template>

    <template v-else-if="selectedHome === 'front'">
      <div data-testid="home-front-actions" class="flex flex-wrap items-center gap-2">
        <UButton v-if="can('order.create')" to="/cashier?open=walkin">散客开单</UButton>
        <UButton v-if="can('booking.manage')" to="/bookings" color="neutral" variant="outline">新预约</UButton>
        <UButton v-if="can('attendance.clock') && !todayClockIn" color="neutral" variant="outline" :loading="punching" @click="punch">打卡</UButton>
        <p v-if="todayClockIn" class="text-sm text-muted">已打卡 {{ clockTime(todayClockIn) }}</p>
      </div>
    </template>

    <template v-else-if="selectedHome === 'manager'">
      <div data-testid="home-manager-metrics" class="grid gap-4 sm:grid-cols-3 lg:grid-cols-5">
        <div v-for="metric in metrics" :key="metric.label" :data-metric="metric.label" class="rounded-xl border border-default p-4">
          <p class="text-sm text-muted">{{ metric.label }}</p>
          <p data-testid="metric-value" class="mt-1 text-2xl font-semibold tabular-nums">{{ metric.value }}</p>
          <p data-testid="metric-compare" class="mt-1 text-xs text-muted tabular-nums">{{ metric.compare }}</p>
        </div>
      </div>
    </template>
  </div>
</template>
