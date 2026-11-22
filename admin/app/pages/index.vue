<script setup lang="ts">
import type { components } from '~/utils/api'
import { homeLabels, homeMatches, homeShortLabels, type HomeKind } from '~/utils/home'
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
  const options: HomeSwitcherOption[] = matches.value.map(kind => ({ label: homeLabels[kind], short: homeShortLabels[kind], value: kind }))
  switcherOptions.value = options
  switcherActive.value = options.length > 1
}

function initSelected() {
  syncSwitcher()
  const remembered = readRemembered()
  selectedHome.value = remembered && matches.value.includes(remembered) ? remembered : (matches.value[0] ?? 'personal')
}

// 今日排班或今日顾客看板报上来的预约数；换首页时清空，等新首页重报，用在问候下面那行。
const visits = ref<{ total: number, waiting: number } | null>(null)

watch(selectedHome, (value) => {
  visits.value = null
  if (!ready || !homeKey.value || !matches.value.includes(value)) return
  localStorage.setItem(homeKey.value, value)
  if (value === 'manager') void loadReport()
})

// 指令框里的推荐说法，点一下直接交给 AI。
const frontSuggestions = ['陈女士明天下午 3 点约剪发', '今天还有谁没到店', '张师傅下午几点有空']
const managerSuggestions = ['本周谁的业绩最高？', '今天实收比上周同日差多少？']
const personalSuggestions = ['帮我占 5 点到 6 点', '我今天做了几单？']

const { pending: pendingDrafts, drafts: allDrafts } = useDrafts()
const myDrafts = computed(() => allDrafts.value.filter(draft => draft.status === 'pending' && draft.initiator === me.value?.staff.id))

// 当前时刻，每 30 秒走一次，用在问候下面那行。
const nowStamp = ref(new Date())
let clockTimer: ReturnType<typeof setInterval> | undefined

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
  if (hour < 5) return '夜深了'
  if (hour < 12) return '早上好'
  if (hour < 18) return '下午好'
  return '晚上好'
})

const dateLine = computed(() => {
  const [year, month, day] = today.value.split('-').map(Number)
  const date = new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, day ?? 1))
  const weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
  const time = nowStamp.value.toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })
  const parts = [`${month} 月 ${day} 日 ${weekdays[date.getUTCDay()]}`, time]
  if (selectedHome.value !== 'personal' && visits.value?.waiting) parts.push(`今天还有 ${visits.value.waiting} 位顾客没到店`)
  return parts.join(' · ')
})

function formatGuest(value: string | number) {
  return Number(value || '0').toLocaleString('zh-CN', { maximumFractionDigits: 1 })
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

function percentDelta(current: string | number, previous: string | number) {
  if (!lastWeekHasData.value || !Number(previous)) return null
  return Math.round(((Number(current) - Number(previous)) / Number(previous)) * 100)
}

const metrics = computed(() => {
  const totals = report.value?.totals
  const previous = lastWeekReport.value?.totals
  if (!totals) return []
  const cardUse = Number(totals.card_principal) + Number(totals.card_bonus)
  return [
    { label: '实收', value: yuan(totals.service_revenue), change: percentDelta(totals.service_revenue, previous?.service_revenue ?? 0), note: '不含办卡充值' },
    { label: '客数', value: formatGuest(totals.guest_total), change: percentDelta(totals.guest_total, previous?.guest_total ?? 0), note: `共 ${totals.checkout_count} 单` },
    { label: '办卡充值', value: yuan(totals.sales_revenue), change: null, note: `卡扣 ${yuan(cardUse)}` },
    { label: '团购核销', value: yuan(totals.groupbuy_deduct), change: null, note: `新增欠款 ${yuan(totals.new_debt)}` }
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
  clearInterval(clockTimer)
  switcherActive.value = false
  switcherOptions.value = []
})

// 同步选好首页，第一次渲染就不会显示上一个登录者选的首页。
initSelected()

onMounted(async () => {
  clockTimer = setInterval(() => {
    nowStamp.value = new Date()
  }, 30_000)
  ready = true
  await loadSettings()
  await Promise.all([loadAttendance(), loadReport()])
})
</script>

<!-- 三种首页照原型：店长 J、前台 J 的助理视图、个人 K。 -->
<template>
  <div class="mx-auto w-full pb-16" :class="selectedHome === 'personal' ? 'max-w-[1000px]' : 'max-w-[1060px]'">
    <h1 data-testid="home-greeting" class="mt-2 mb-0.5 text-center text-greet font-bold text-balance">
      {{ greeting }}，{{ me?.staff.name }}
    </h1>
    <p class="mb-4 text-center text-muted tabular-nums" data-testid="home-date">{{ dateLine }}</p>

    <template v-if="selectedHome === 'personal'">
      <template v-if="can('attendance.clock')">
        <div v-if="!todayClockIn" class="flex items-center gap-4 rounded-xl bg-notice px-[18px] py-4">
          <UIcon name="i-lucide-clock" class="size-7 shrink-0 text-muted" aria-hidden="true" />
          <p class="text-lead font-semibold">今天还没打卡<small class="block text-meta font-normal text-muted">上班先打卡，迟到、早退按门店的班次算</small></p>
          <UButton data-testid="home-punch" size="xl" class="ms-auto min-h-12 px-7 text-base" :loading="punching" @click="punch">打卡</UButton>
        </div>
        <p v-else class="-mt-2 mb-3.5 flex items-center justify-center gap-2 text-meta text-muted">
          <span data-testid="home-punch-time" class="tag bg-tag-green tabular-nums">已打卡 {{ clockTime(todayClockIn) }}</span>
        </p>
      </template>
      <AiCommandBox :suggestions="personalSuggestions" />
      <div class="grid gap-3 md:grid-cols-[1fr_1.4fr]">
        <HomeRotation v-if="can('queue.view') && me" :staff-id="me.staff.id" />
        <HomeMyDay v-if="can('performance.view') && me && businessDate" :date="today" :staff-id="me.staff.id" />
      </div>
      <template v-if="can('booking.manage') && me && businessDate">
        <div class="sec"><UIcon name="i-lucide-calendar" class="size-4 shrink-0" aria-hidden="true" /> 我的今天<span class="ms-auto font-normal text-dimmed">时间按门店营业时间排</span></div>
        <div class="box"><HomeTimeline :date="today" :staff-id="me.staff.id" /></div>
        <HomeMyCustomers :date="today" :staff-id="me.staff.id" />
      </template>
      <template v-if="myDrafts.length">
        <div class="sec"><UIcon :name="DRAFTS_ICON" class="size-4 shrink-0" aria-hidden="true" /> 我起草的<span class="ms-auto font-normal text-dimmed">等有权限的人确认</span></div>
        <div class="space-y-2">
          <NuxtLink v-for="draft in myDrafts" :key="draft.id" to="/drafts" class="box flex items-center gap-3">
            <span class="tag shrink-0 bg-ai-soft text-ai">✦ AI 起草</span>
            <b class="truncate text-sm font-semibold">{{ draft.summary }}</b>
            <span class="flex-1 text-meta text-dimmed tabular-nums">{{ formatStamp(draft.created_at) }}</span>
            <span class="tag shrink-0 bg-tag-yellow">待确认</span>
          </NuxtLink>
        </div>
      </template>
    </template>

    <template v-else-if="selectedHome === 'front'">
      <AiCommandBox large :suggestions="frontSuggestions" placeholder="说一句要做的事，例如“陈女士到了，开剪发”" />
      <div data-testid="home-front-actions" class="mx-auto mt-3 grid w-full max-w-[720px] grid-cols-2 gap-2 sm:grid-cols-4">
        <NuxtLink v-if="can('order.create')" to="/cashier?open=walkin" class="quick"><UIcon name="i-lucide-receipt" class="size-4 text-muted" aria-hidden="true" />散客开单</NuxtLink>
        <NuxtLink v-if="can('booking.manage')" to="/bookings" class="quick"><UIcon name="i-lucide-calendar-plus" class="size-4 text-muted" aria-hidden="true" />新预约</NuxtLink>
        <NuxtLink v-if="can('customer.view')" to="/customers" class="quick"><UIcon name="i-lucide-user-search" class="size-4 text-muted" aria-hidden="true" />找顾客</NuxtLink>
        <button v-if="can('attendance.clock') && !todayClockIn" type="button" class="quick" :disabled="punching" @click="punch"><UIcon name="i-lucide-clock" class="size-4 text-muted" aria-hidden="true" />打卡</button>
        <p v-if="todayClockIn" class="quick text-muted tabular-nums">已打卡 {{ clockTime(todayClockIn) }}</p>
      </div>
      <template v-if="myDrafts.length">
        <div class="sec"><UIcon :name="DRAFTS_ICON" class="size-4 shrink-0" aria-hidden="true" /> 我起草的<span class="ms-auto font-normal text-dimmed">等店长确认</span></div>
        <div class="space-y-2">
          <NuxtLink v-for="draft in myDrafts" :key="draft.id" to="/drafts" class="box flex items-center gap-3">
            <span class="tag shrink-0 bg-ai-soft text-ai">✦ AI 起草</span>
            <b class="truncate text-sm font-semibold">{{ draft.summary }}</b>
            <span class="flex-1 text-meta text-dimmed tabular-nums">{{ formatStamp(draft.created_at) }}</span>
            <span class="tag shrink-0 bg-tag-yellow">待店长确认</span>
          </NuxtLink>
        </div>
      </template>
      <template v-if="can('booking.manage') && businessDate">
        <div class="sec"><span aria-hidden="true">▥</span> 今日顾客<span class="ms-auto font-normal text-dimmed">预约和散客</span></div>
        <HomeBoard :date="today" @summary="visits = $event" />
      </template>
    </template>

    <template v-else-if="selectedHome === 'manager'">
      <div class="sec"><UIcon :name="DRAFTS_ICON" class="size-4 shrink-0" aria-hidden="true" /> 待你确认<span class="ms-auto font-normal text-dimmed">AI 起草，你确认后才生效 · 当天有效</span></div>
      <div v-if="pendingDrafts.length" data-testid="home-drafts" class="grid gap-3 md:grid-cols-3">
        <DraftCard v-for="draft in pendingDrafts.slice(0, 3)" :key="draft.id" :draft="draft" @open="navigateTo('/drafts')" />
      </div>
      <p v-else class="box text-meta text-dimmed">没有等你确认的草稿。员工吩咐 AI 做的事会先到这里。</p>
      <p v-if="pendingDrafts.length > 3" class="mt-2 text-meta">
        <NuxtLink to="/drafts" class="text-muted underline underline-offset-3">还有 {{ pendingDrafts.length - 3 }} 条，到待确认里看</NuxtLink>
      </p>

      <AiCommandBox :suggestions="managerSuggestions" />

      <div class="sec"><UIcon name="i-lucide-chart-column" class="size-4 shrink-0" aria-hidden="true" /> 今日经营<span class="ms-auto font-normal text-dimmed">实收口径</span></div>
      <div data-testid="home-manager-metrics" class="box grid grid-cols-2 gap-3 tabular-nums md:grid-cols-4">
        <div v-for="metric in metrics" :key="metric.label" :data-metric="metric.label">
          <p class="text-xs text-dimmed">{{ metric.label }}</p>
          <p data-testid="metric-value" class="text-2xl leading-[1.3] font-semibold" :class="zeroText(metric.value) && 'text-dimmed'">{{ metric.value }}</p>
          <p data-testid="metric-compare" class="text-xs text-muted">
            <template v-if="metric.change !== null">
              <span :class="metric.change >= 0 ? 'text-ink-green' : 'text-ink-red'">{{ metric.change >= 0 ? '↑' : '↓' }} {{ Math.abs(metric.change) }}%</span>
              较上周同日{{ metric.label === '实收' ? '' : ` · ${metric.note}` }}
            </template>
            <template v-else>{{ metric.note }}</template>
          </p>
        </div>
      </div>

      <template v-if="can('booking.manage') && businessDate">
        <div class="sec"><UIcon name="i-lucide-calendar" class="size-4 shrink-0" aria-hidden="true" /> 今日排班<span class="ms-auto font-normal text-dimmed"><NuxtLink to="/bookings" class="hover:underline">到预约页改约</NuxtLink></span></div>
        <div class="box"><HomeTimeline :date="today" @summary="visits = $event" /></div>
      </template>
    </template>
  </div>
</template>

<style scoped>
.quick {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 44px;
  border-radius: 10px;
  background: var(--color-white);
  box-shadow: var(--shadow-line);
  font-weight: 500;
}
.quick:hover {
  background: var(--color-side);
}
</style>
