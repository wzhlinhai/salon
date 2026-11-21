<script setup lang="ts">
import type { components } from '~/utils/api'

defineProps<{ collapsed?: boolean }>()

type AttendanceDay = components['schemas']['AttendanceDay']

const api = useApi()
const toast = useToast()
const { me, can } = useMe()

const { punch } = usePunch()
const attendance = useModalForm()
const { open: attendanceOpen, loading: attendanceLoading } = attendance
const attendanceMonth = ref('')
const attendanceDays = ref<AttendanceDay[]>([])
const passwordOpen = ref(false)

function currentMonth() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

function openAttendance() {
  attendance.begin(() => {
    attendanceMonth.value = currentMonth()
    attendanceDays.value = []
  })
  void attendance.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/list_my_attendance', { body: { month: attendanceMonth.value } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    attendanceDays.value = data.days
  })
}

function openPassword() {
  passwordOpen.value = true
}

function logout() {
  setToken(null)
  me.value = null
  navigateTo('/login')
}

const menuItems = computed(() => {
  const items = []
  if (can('attendance.clock')) {
    items.push({ label: '打卡', icon: 'i-lucide-clock-3', onSelect: punch })
    items.push({ label: '我的考勤', icon: 'i-lucide-calendar-check', onSelect: openAttendance })
  }
  items.push({ label: '改密码', icon: 'i-lucide-key-round', onSelect: openPassword })
  items.push({ label: '退出登录', icon: 'i-lucide-log-out', onSelect: logout })
  return items
})
</script>

<template>
  <UDropdownMenu :items="menuItems" :content="{ align: 'start', side: 'top' }">
    <UButton
      data-testid="personal-menu"
      color="neutral"
      variant="ghost"
      :square="collapsed"
      :class="collapsed ? '' : 'w-full justify-start'"
      :aria-label="me?.staff.name || '个人菜单'"
    >
      <UAvatar :alt="me?.staff.name || '未登录'" size="2xs" />
      <span v-if="!collapsed" class="truncate">{{ me?.staff.name }}</span>
    </UButton>
  </UDropdownMenu>

  <UModal v-model:open="attendanceOpen" title="我的考勤">
    <template #body>
      <p v-if="attendanceLoading" class="text-sm text-muted">加载中</p>
      <p v-else-if="!attendanceDays.length" class="text-sm text-muted">这个月还没有考勤记录。</p>
      <div v-else class="space-y-1">
        <div
          v-for="day in attendanceDays"
          :key="day.business_date"
          class="flex min-h-10 items-center gap-3 border-b border-default py-2 text-sm last:border-b-0"
        >
          <span class="w-24 tabular-nums">{{ day.business_date }}</span>
          <span class="w-32 tabular-nums text-muted">{{ clockTime(day.clock_in) }} – {{ clockTime(day.clock_out) }}</span>
          <UBadge v-if="day.missing" color="error" variant="subtle">缺卡</UBadge>
          <UBadge v-else-if="day.late" color="warning" variant="subtle">迟到</UBadge>
          <UBadge v-else-if="day.early" color="warning" variant="subtle">早退</UBadge>
        </div>
      </div>
    </template>
  </UModal>

  <UModal v-model:open="passwordOpen" title="改密码">
    <template #body>
      <ChangePassword />
    </template>
  </UModal>
</template>
