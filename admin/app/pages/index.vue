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

// 前台首页指令框里的推荐说法，点一下直接交给 AI。店长和个人首页不放指令框，用右下角的 AI 按钮。
const frontSuggestions = ['陈女士明天下午 3 点约剪发', '今天还有谁没到店', '张师傅下午几点有空']

// 店长首页的「常用」（照 Notion 首页 Suggested for you）：有权限的前 4 个。
const shortcutCatalog = [
  { to: '/cashier?open=walkin', label: '散客开单', icon: 'i-lucide-receipt', permission: 'order.create' },
  { to: '/bookings', label: '新建预约', icon: 'i-lucide-calendar-plus', permission: 'booking.manage' },
  { to: '/closing', label: '做今天的日结', icon: 'i-lucide-calculator', permission: 'closing.daily' },
  { to: '/reports/business', label: '看营业报表', icon: 'i-lucide-chart-column', permission: 'report.view' },
  { to: '/payroll', label: '工资表', icon: 'i-lucide-wallet', permission: 'payroll.manage' },
  { to: '/groupbuy-month', label: '团购对账', icon: 'i-lucide-file-check', permission: 'report.view' }
]
const shortcuts = computed(() => shortcutCatalog.filter(item => can(item.permission)).slice(0, 4))

// 最近访问（照 Notion 首页）：本人最近打开的页面；不够 6 个，用常用页面补上，没去过的不写时间。
const { read: readRecent } = useRecentPages()
const fallbackPages = ['/cashier', '/bookings', '/customers', '/closing', '/reports', '/staff']
const recentPages = ref<{ to: string, label: string, icon: string, when: string }[]>([])

function visitedWhen(at: number) {
  const date = new Date(at)
  const minutes = Math.round((Date.now() - at) / 60_000)
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  const time = date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
  if (date.toDateString() === new Date().toDateString()) return `今天 ${time}`
  return `${date.getMonth() + 1}月${date.getDate()}日`
}

function loadRecent() {
  const allowed = navItems.filter(item => item.to !== '/' && (!item.permission || can(item.permission)))
  const find = (to: string) => allowed.find(item => item.to === to)
  const rows: { to: string, label: string, icon: string, when: string }[] = []
  for (const visit of readRecent()) {
    const item = find(visit.to)
    if (item) rows.push({ to: item.to, label: item.label, icon: item.icon, when: visitedWhen(visit.at) })
  }
  for (const to of fallbackPages) {
    const item = find(to)
    if (item && rows.length < 6 && !rows.some(row => row.to === to)) rows.push({ to, label: item.label, icon: item.icon, when: '' })
  }
  recentPages.value = rows
}

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
  const line = `${month}月${day}日 ${weekdays[date.getUTCDay()]} ${time}`
  if (selectedHome.value !== 'personal' && visits.value?.waiting) return `${line}，还有 ${visits.value.waiting} 位顾客没到店`
  return line
})

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
  loadRecent()
  await loadSettings()
  await Promise.all([loadAttendance(), loadReport()])
})
</script>

<!-- 三种首页照 Notion 首页：问候居中，下面是一张张组件卡（卡内灰色小图标加标题），窄的两张并排。 -->
<template>
  <div class="mx-auto w-full max-w-[960px]">
    <header class="mt-4 mb-8 text-center">
      <h1 data-testid="home-greeting" class="text-title font-bold text-balance">
        {{ greeting }}，{{ me?.staff.name }}
      </h1>
      <p class="mt-1 text-muted tabular-nums" data-testid="home-date">{{ dateLine }}</p>
    </header>

    <div class="flex flex-col gap-4">
      <template v-if="selectedHome === 'personal'">
        <template v-if="can('attendance.clock')">
          <div v-if="!todayClockIn" class="flex items-center gap-4 rounded-card bg-notice px-[18px] py-4">
            <UIcon name="i-lucide-clock" class="size-7 shrink-0 text-muted" aria-hidden="true" />
            <p class="text-lead font-semibold">今天还没打卡<small class="block text-meta font-normal text-muted">上班先打卡，迟到、早退按门店的班次算</small></p>
            <UButton data-testid="home-punch" size="xl" class="ms-auto min-h-12 px-7 text-base" :loading="punching" @click="punch">打卡</UButton>
          </div>
          <p v-else class="-mt-4 flex items-center justify-center gap-2 text-meta text-muted">
            <span data-testid="home-punch-time" class="tag bg-tag-green tabular-nums">已打卡 {{ clockTime(todayClockIn) }}</span>
          </p>
        </template>
        <!-- 不在轮牌队列里时轮牌卡不显示，「我今天」占满整行。 -->
        <div class="flex flex-col gap-4 md:flex-row">
          <HomeRotation v-if="can('queue.view') && me" :staff-id="me.staff.id" class="md:w-[40%] md:shrink-0" />
          <HomeMyDay v-if="can('performance.view') && me && businessDate" :date="today" :staff-id="me.staff.id" class="min-w-0 flex-1" />
        </div>
        <template v-if="can('booking.manage') && me && businessDate">
          <HomeWidget title="我的今天" icon="i-lucide-calendar">
            <template #aside>时间按门店营业时间排</template>
            <HomeTimeline :date="today" :staff-id="me.staff.id" />
          </HomeWidget>
          <HomeMyCustomers :date="today" :staff-id="me.staff.id" />
        </template>
        <HomeWidget v-if="myDrafts.length" title="我起草的" icon="i-lucide-pencil">
          <template #aside>等有权限的人确认</template>
          <HomeDrafts :drafts="myDrafts" status="待确认" />
        </HomeWidget>
      </template>

      <template v-else-if="selectedHome === 'front'">
        <AiCommandBox :suggestions="frontSuggestions" placeholder="说一句要做的事，例如“陈女士到了，开剪发”" />
        <div data-testid="home-front-actions" class="mx-auto grid w-full max-w-[720px] grid-cols-2 gap-2 sm:grid-cols-4">
          <NuxtLink v-if="can('order.create')" to="/cashier?open=walkin" class="quick"><UIcon name="i-lucide-receipt" class="size-4 text-muted" aria-hidden="true" />散客开单</NuxtLink>
          <NuxtLink v-if="can('booking.manage')" to="/bookings" class="quick"><UIcon name="i-lucide-calendar-plus" class="size-4 text-muted" aria-hidden="true" />新预约</NuxtLink>
          <NuxtLink v-if="can('customer.view')" to="/customers" class="quick"><UIcon name="i-lucide-user-search" class="size-4 text-muted" aria-hidden="true" />找顾客</NuxtLink>
          <button v-if="can('attendance.clock') && !todayClockIn" type="button" class="quick" :disabled="punching" @click="punch"><UIcon name="i-lucide-clock" class="size-4 text-muted" aria-hidden="true" />打卡</button>
          <p v-if="todayClockIn" class="quick text-muted tabular-nums">已打卡 {{ clockTime(todayClockIn) }}</p>
        </div>
        <HomeWidget v-if="myDrafts.length" title="我起草的" icon="i-lucide-pencil">
          <template #aside>等店长确认</template>
          <HomeDrafts :drafts="myDrafts" status="待店长确认" />
        </HomeWidget>
        <HomeWidget v-if="can('booking.manage') && businessDate" title="今日顾客" icon="i-lucide-users">
          <template #aside>预约和散客</template>
          <HomeBoard :date="today" @summary="visits = $event" />
        </HomeWidget>
      </template>

      <template v-else-if="selectedHome === 'manager'">
        <HomeWidget v-if="recentPages.length" title="最近访问" icon="i-lucide-clock">
          <div class="-mx-1 flex gap-3 overflow-x-auto px-1 pt-1 pb-1" data-testid="home-recent">
            <NuxtLink
              v-for="page in recentPages"
              :key="page.to"
              :to="page.to"
              class="flex h-[150px] w-[150px] shrink-0 flex-col overflow-hidden rounded-card bg-white shadow-card transition-colors hover:bg-card-hover"
            >
              <span class="h-11 shrink-0 bg-side" aria-hidden="true" />
              <UIcon :name="page.icon" class="-mt-4 ms-4 size-7 shrink-0 text-toned" aria-hidden="true" />
              <span class="px-4 pt-2.5 text-lead font-medium">{{ page.label }}</span>
              <span class="mt-auto flex items-center gap-1.5 px-4 pb-3.5 text-meta text-dimmed tabular-nums">
                <UIcon :name="page.when ? 'i-lucide-clock' : 'i-lucide-star'" class="size-3.5 shrink-0" aria-hidden="true" />
                {{ page.when || '常用' }}
              </span>
            </NuxtLink>
          </div>
        </HomeWidget>

        <HomeWidget title="待你确认" icon="i-lucide-inbox">
          <template #aside>AI 起草，确认后生效</template>
          <div data-testid="home-drafts">
            <HomeDrafts v-if="pendingDrafts.length" :drafts="pendingDrafts" status="待确认" />
            <p v-else class="flex min-h-9 items-center gap-2 text-dimmed">
              <UIcon name="i-lucide-check" class="size-4 shrink-0" aria-hidden="true" />
              没有等你确认的草稿。员工吩咐 AI 做的事会先到这里。
            </p>
          </div>
        </HomeWidget>

        <div class="grid gap-4 md:grid-cols-2">
          <HomeWidget title="今日经营" icon="i-lucide-chart-line">
            <template #aside>实收口径</template>
            <ul data-testid="home-manager-metrics" class="tabular-nums">
              <li v-for="metric in metrics" :key="metric.label" :data-metric="metric.label" class="flex min-h-9 items-center gap-3">
                <span class="w-16 shrink-0 text-lead font-medium">{{ metric.label }}</span>
                <span data-testid="metric-compare" class="min-w-0 flex-1 truncate text-meta text-muted">
                  <template v-if="metric.change !== null">
                    <span :class="metric.change >= 0 ? 'text-ink-green' : 'text-ink-red'">{{ metric.change >= 0 ? '↑' : '↓' }} {{ Math.abs(metric.change) }}%</span>
                    较上周同日{{ metric.label === '实收' ? '' : ` · ${metric.note}` }}
                  </template>
                  <template v-else>{{ metric.note }}</template>
                </span>
                <span data-testid="metric-value" class="shrink-0 text-lead font-semibold" :class="zeroText(metric.value) && 'text-dimmed'">{{ metric.value }}</span>
              </li>
            </ul>
          </HomeWidget>

          <HomeWidget v-if="shortcuts.length" title="常用" icon="i-lucide-zap">
            <ul class="-mx-1" data-testid="home-shortcuts">
              <li v-for="item in shortcuts" :key="item.to">
                <NuxtLink :to="item.to" class="flex min-h-9 items-center gap-2.5 rounded-md px-1 text-lead font-medium hover:bg-hover">
                  <UIcon :name="item.icon" class="size-4 shrink-0 text-muted" aria-hidden="true" />
                  {{ item.label }}
                </NuxtLink>
              </li>
            </ul>
          </HomeWidget>
        </div>

        <HomeWidget v-if="can('booking.manage') && businessDate" title="今日排班" icon="i-lucide-calendar">
          <template #aside><NuxtLink to="/bookings" class="hover:underline">到预约页改约</NuxtLink></template>
          <HomeTimeline :date="today" @summary="visits = $event" />
        </HomeWidget>
      </template>
    </div>
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
