<script setup lang="ts">
// 首页（样稿 01）：只回答「接下来两个小时」。左边是接下来的时间线，右边是在店里的人和等人确认的 AI 草稿。
// 管店、收银、派轮牌的人看全店；其余员工看自己的那一条，加上今天的业绩提成。有打卡权限的没打卡时最上面提醒一句。
definePageMeta({ title: '首页' })

const toast = useToast()
const api = useApi()
const { me, can } = useMe()

const wholeStore = computed(() => can('report.view') || can('payment.collect') || can('queue.operate'))

const { pending: pendingDrafts, drafts: allDrafts } = useDrafts()
const myDrafts = computed(() => allDrafts.value.filter(draft => draft.status === 'pending' && draft.initiator === me.value?.staff.id))

// 读不到营业日时「接下来」整块出不来：原地给「重试」（settingsError），别只弹一条提示留一片空白。
const { businessDate, error: settingsError, load: loadSettings } = useStoreToday()
const attendanceGuard = useRequestGuard()
const { clockIn: todayClockIn, known: punchKnown, punching, punch, remember: rememberClockIn } = usePunch()
// 考勤没读出来时也要露出打卡按钮：读完（成功或失败）才决定。
const attendanceTried = ref(false)
// 「在店里」写「做王女士」用：正在做的单是哪位顾客，由时间线报上来。
const serving = ref<Record<string, string>>({})

const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour < 5) return '夜深了'
  if (hour < 12) return '早上好'
  if (hour < 18) return '下午好'
  return '晚上好'
})

async function loadAttendance() {
  if (!can('attendance.clock') || !businessDate.value) return
  const current = attendanceGuard.next()
  const before = todayClockIn.value
  const { data, error } = await api.POST('/api/ops/list_my_attendance', { body: { month: businessDate.value.slice(0, 7) } })
  if (!current()) return
  attendanceTried.value = true
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  const day = data.days.find(row => row.business_date === businessDate.value)
  // 请求期间在个人菜单里打过卡，就以那次打卡为准。
  if (todayClockIn.value !== before) return
  rememberClockIn(day?.clock_in ?? null)
}

async function retrySettings() {
  await loadSettings()
  await loadAttendance()
}

onMounted(retrySettings)
</script>

<template>
  <div class="mx-auto w-full max-w-[1100px]">
    <SpotArt name="curl" class="mb-1 -ml-1 w-[60px]" />
    <h1 data-testid="home-greeting" class="mb-8 text-title text-balance">
      {{ greeting }}，{{ me?.staff.name }}
    </h1>

    <div class="grid gap-12 lg:has-[>aside>*]:grid-cols-[minmax(0,1fr)_300px]">
      <div class="flex min-w-0 flex-col gap-8">
        <template v-if="can('attendance.clock') && (punchKnown || attendanceTried)">
          <div v-if="!todayClockIn" class="flex items-center gap-4 rounded-card bg-notice px-4 py-4">
            <p class="text-sm font-semibold">今天还没打卡<small class="block text-meta font-normal text-muted">上班先打卡，迟到、早退按门店的班次算</small></p>
            <UButton data-testid="home-punch" size="lg" class="ms-auto shrink-0 px-8" :loading="punching" @click="punch">打卡</UButton>
          </div>
          <p v-else class="-mt-4 text-meta text-muted">
            <span data-testid="home-punch-time" class="tag bg-tag-gray tabular-nums">已打卡 {{ clockTime(todayClockIn) }}</span>
          </p>
        </template>
        <LoadError v-if="settingsError" title="今天的安排没有读出来" :hint="settingsError" @retry="retrySettings()" />
        <HomeNext
          v-if="businessDate && can('booking.manage')"
          :date="businessDate"
          :staff-id="wholeStore ? undefined : me?.staff.id"
          @serving="serving = $event"
        />
      </div>

      <aside class="flex min-w-0 flex-col gap-8 empty:hidden">
        <HomeFloor v-if="can('queue.view') && me" :staff-id="wholeStore ? undefined : me.staff.id" :serving="serving" />
        <HomeDrafts v-if="can('report.view')" title="待你确认" :drafts="pendingDrafts" />
        <HomeDrafts v-else title="我起草的" :drafts="myDrafts" waiting-for="等店长确认" />
        <HomeMyDay v-if="!wholeStore && can('performance.view') && me && businessDate" :date="businessDate" :staff-id="me.staff.id" />
      </aside>
    </div>
  </div>
</template>
