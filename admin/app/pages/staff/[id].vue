<script setup lang="ts">
import type { components } from '~/utils/api'

// 员工详情（样稿 09）：和顾客档案同一副骨架。页头头像和名字，下面一张资料表；左栏「这个月」「最近做的」，右栏考勤。
// 外壳只在顶栏写「员工 / 名字」。业绩要 performance.view，班次和考勤要 attendance.manage，没权限的那块不出现。
definePageMeta({ permission: 'staff.manage', title: '员工', bare: true })

type Staff = components['schemas']['Staff']
type Entry = components['schemas']['PerformanceEntry']
const route = useRoute()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const crumbTitle = useState('page-crumb-title', () => '')
const id = computed(() => String(route.params.id))
const editors = useTemplateRef('editors')

const staff = ref<Staff | null>(null)
const loadError = ref('')
const today = ref('')
const shift = ref<components['schemas']['Shift'] | null | undefined>(undefined)
const report = ref<components['schemas']['StaffReportRow'] | null>(null)
const recent = ref<Entry[]>([])
const attendance = ref<components['schemas']['AttendanceSummary'] | null>(null)
const guard = useRequestGuard()
const factsGuard = useRequestGuard()

const monthStart = computed(() => today.value ? `${today.value.slice(0, 8)}01` : '')

async function load() {
  const current = guard.next()
  const staffID = id.value
  loadError.value = ''
  const res = await api.POST('/api/ops/list_staff', { body: {} })
  if (!current()) return
  const found = res.data?.staff.find(s => s.id === staffID)
  if (found) {
    staff.value = found
    crumbTitle.value = found.name
    return
  }
  const message = res.data ? '没有这位员工' : errorText(res.error)
  // 已经显示着这位员工时（保存后刷新）出错，页面不换成错误，弹一条提示，免得旧状态被当成新的。
  if (staff.value) toast.add({ title: message, color: 'error' })
  else loadError.value = message
}

// 这个月的业绩、最近做的、班次和考勤：各取各的，取不到的那块不写，不挡整页。
async function loadFacts() {
  const current = factsGuard.next()
  const settings = await api.POST('/api/ops/get_settings', { body: {} })
  if (!current() || !settings.data) return
  today.value = settings.data.business_date
  const staffID = id.value
  const from = monthStart.value
  const jobs: Promise<unknown>[] = []
  if (can('performance.view')) {
    jobs.push(api.POST('/api/ops/get_staff_report', { body: { from, to: today.value, staff_id: staffID } }).then((r) => {
      if (current() && r.data) report.value = r.data.rows.find(row => row.staff?.id === staffID) ?? r.data.totals
    }))
    jobs.push(api.POST('/api/ops/list_performance', { body: { date_from: from, date_to: today.value, staff_id: staffID, kind: 'item', limit: 50 } }).then((r) => {
      if (!current() || !r.data) return
      // 冲回的那一笔和被冲回的原记录都不算「做的」。
      const reversed = new Set(r.data.entries.filter(e => e.reversal).map(e => e.reverses_id))
      recent.value = r.data.entries.filter(e => !e.reversal && !reversed.has(e.id)).slice(0, 8)
    }))
  }
  if (can('attendance.manage')) {
    jobs.push(api.POST('/api/ops/list_staff_shifts', { body: { date: today.value } }).then((r) => {
      if (current() && r.data) shift.value = r.data.entries.find(e => e.staff.id === staffID)?.current?.shift ?? null
    }))
    jobs.push(api.POST('/api/ops/get_staff_attendance', { body: { month: today.value.slice(0, 7), staff_id: staffID } }).then((r) => {
      if (current() && r.data) attendance.value = r.data.summary
    }))
  }
  await Promise.all(jobs)
}

function reload() {
  load()
  loadFacts()
}

onMounted(reload)
// 换了一位员工：清掉上一位的东西再取。
watch(id, () => {
  staff.value = null
  report.value = null
  recent.value = []
  attendance.value = null
  shift.value = undefined
  reload()
})
onBeforeUnmount(() => {
  crumbTitle.value = ''
})

const fields = computed(() => {
  const s = staff.value
  if (!s) return []
  return [
    { label: '岗位', value: [s.position?.name ?? '没设岗位', s.level ? `级别 ${s.level.name}` : ''].filter(Boolean).join(' · ') },
    { label: '编号', value: s.number || '没填' },
    { label: '角色', value: s.roles.map(r => r.name).join('、') || '没设角色' },
    { label: '登录', value: s.login_account || '没开登录账号' },
    { label: '预约', value: s.self_bookable ? '顾客可以在小程序约' : '小程序不能约' },
    ...(shift.value !== undefined && s.active ? [{ label: '班次', value: shift.value ? `${shift.value.name} ${shift.value.start_time.slice(0, 5)}–${shift.value.end_time.slice(0, 5)}` : '没排班' }] : [])
  ]
})

const monthSentence = computed(() => {
  const r = report.value
  if (!r) return ''
  const guests = Number(r.guest_total)
  const designated = Number(r.guest_designated)
  return [
    `项目业绩 ${yuan(r.item_performance)}`,
    guests ? `，做了 ${guests} 位` : '，这个月还没做',
    designated ? `，其中点名 ${designated} 位` : '',
    '。',
    Number(r.commission_total) ? `提成预计 ${yuan(r.commission_total)}。` : ''
  ].join('')
})

const attendanceSentence = computed(() => {
  const a = attendance.value
  if (!a) return ''
  const parts = [`本月出勤 ${a.attendance_days} 天`]
  if (a.late_count) parts.push(`迟到 ${a.late_count} 次`)
  if (a.early_count) parts.push(`早退 ${a.early_count} 次`)
  if (a.missing_count) parts.push(`缺卡 ${a.missing_count} 次`)
  return parts.join('，')
})

function dayText(date: string) {
  return date === today.value ? '今天' : monthDay(date)
}

const moreItems = computed(() => {
  const s = staff.value
  if (!s) return []
  return [
    [
      { label: '设置角色', icon: 'i-lucide-shield', onSelect: () => editors.value?.openRoles(s) },
      { label: '登录账号', icon: 'i-lucide-key-round', onSelect: () => editors.value?.openLogin(s) }
    ],
    [s.active
      ? { label: '停用', icon: 'i-lucide-user-x', color: 'error' as const, onSelect: () => editors.value?.setActive(s, false) }
      : { label: '恢复', icon: 'i-lucide-user-check', onSelect: () => editors.value?.setActive(s, true) }]
  ]
})
</script>

<template>
  <div>
    <div class="mx-auto max-w-[1100px] pb-10" data-testid="staff-profile">
      <template v-if="staff">
        <header class="flex flex-wrap items-start gap-4">
          <PersonAvatar :name="staff.name" size="lg" />
          <div class="min-w-0 flex-1">
            <h1 class="flex items-center gap-2 truncate text-title">
              {{ staff.name }}
              <span v-if="!staff.active" class="tag bg-tag-gray text-sm font-normal">已停用</span>
            </h1>
          </div>
          <div class="flex items-center gap-2">
            <UButton color="neutral" variant="outline" icon="i-lucide-pencil" @click="editors?.openProfile(staff)">修改档案</UButton>
            <UDropdownMenu :items="moreItems" :content="{ align: 'end' }">
              <UButton color="neutral" variant="outline" icon="i-lucide-ellipsis" aria-label="员工操作" :loading="editors?.activeSaving[staff.id]" />
            </UDropdownMenu>
          </div>
        </header>

        <div class="mt-8 grid gap-10 lg:grid-cols-[minmax(0,1fr)_20rem]">
          <main class="min-w-0 space-y-8">
            <dl class="grid grid-cols-[6rem_minmax(0,1fr)] gap-y-3 text-sm" data-testid="staff-fields">
              <template v-for="field in fields" :key="field.label">
                <dt class="text-muted">{{ field.label }}</dt>
                <dd :data-field="field.label">{{ field.value }}</dd>
              </template>
            </dl>

            <section v-if="report" data-testid="staff-month">
              <div class="flex items-center">
                <h2 class="sec mt-0!">这个月</h2>
                <NuxtLink :to="`/reports/staff?from=${monthStart}&to=${today}`" class="ms-auto inline-flex items-center text-meta text-muted hover:underline">业绩明细<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" /></NuxtLink>
              </div>
              <p class="text-base tabular-nums">{{ monthSentence }}</p>
            </section>

            <section v-if="can('performance.view')" data-testid="staff-recent">
              <h2 class="sec">最近做的</h2>
              <p v-if="!recent.length" class="text-sm text-muted">这个月还没有做过的项目</p>
              <div v-else class="divide-y divide-default text-sm">
                <div v-for="entry in recent" :key="entry.id" class="grid grid-cols-[4rem_minmax(0,1fr)_auto] gap-3 py-2">
                  <span class="text-meta text-muted tabular-nums">{{ dayText(entry.business_date) }}</span>
                  <span class="truncate">{{ entry.item_name || '项目' }}</span>
                  <span class="tabular-nums">{{ yuan(entry.amount) }}</span>
                </div>
              </div>
            </section>
          </main>

          <aside class="space-y-8" data-testid="staff-rail">
            <section v-if="attendance" data-testid="staff-attendance">
              <h3 class="flex h-7 items-center text-meta text-muted">考勤</h3>
              <p class="text-sm tabular-nums">{{ attendanceSentence }}</p>
              <NuxtLink to="/attendance" class="mt-1 inline-flex items-center text-meta text-muted hover:underline">考勤明细<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" /></NuxtLink>
            </section>
          </aside>
        </div>
      </template>
      <div v-else-if="loadError" class="space-y-3">
        <p>{{ loadError }}</p>
        <UButton variant="outline" @click="load()">重试</UButton>
      </div>
      <p v-else class="text-muted">正在读取员工…</p>
    </div>

    <StaffEditors ref="editors" @changed="reload" />
  </div>
</template>
