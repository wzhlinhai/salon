<script setup lang="ts">
import type { components } from '~/utils/api'
import { formatAttendanceDateTime, formatAttendanceTime } from '~/utils/attendance'

type Schema = components['schemas']
const props = defineProps<{ month: string, staffId: string }>()
const emit = defineEmits<{ 'changed': [], 'add-punch': [staff: Schema['AttendanceSummary']] }>()
const api = useApi()
const toast = useToast()
const attendance = ref<Schema['StaffAttendance']>()
const loading = ref(false)
const guard = useRequestGuard()

const days = computed(() => [...(attendance.value?.days ?? [])].reverse())

function summaryText(summary: Schema['AttendanceSummary']) {
  return `出勤 ${summary.attendance_days} 天 · 迟到 ${summary.late_count} 次/${summary.late_minutes} 分钟 · 早退 ${summary.early_count} 次/${summary.early_minutes} 分钟 · 缺卡 ${summary.missing_count} 次 · 已核准 ${summary.approved_count} 次`
}

function punchText(punch: Schema['Punch']) {
  return punch.source === 'self' ? '本人打卡' : `补卡 · ${punch.reason} · ${punch.created_by.name}`
}

async function reload() {
  const current = guard.next()
  loading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/get_staff_attendance', {
      body: { month: props.month, staff_id: props.staffId }
    })
    if (!current()) return
    if (data) attendance.value = data
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) loading.value = false
  }
}

onMounted(reload)
defineExpose({ reload })

const voidForm = useModalForm()
const { open: voidOpen, saving: voidSaving } = voidForm
const voidTarget = ref<Schema['Punch']>()
const voidReason = ref('')

function askVoid(punch: Schema['Punch']) {
  voidForm.begin(() => {
    voidTarget.value = punch
    voidReason.value = ''
  })
}

async function saveVoid() {
  await voidForm.save(async (current) => {
    const { error } = await api.POST('/api/ops/void_punch', {
      body: { id: voidTarget.value!.id, reason: voidReason.value }
    })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    emit('changed')
    void reload()
    if (!current()) return
    voidOpen.value = false
    toast.add({ title: '已作废补卡', color: 'success' })
  })
}

const approvalForm = useModalForm()
const { open: approvalOpen, saving: approvalSaving } = approvalForm
const approvalTarget = ref<Schema['AttendanceDay']>()
const approvalReason = ref('')

function askApprove(day: Schema['AttendanceDay']) {
  approvalForm.begin(() => {
    approvalTarget.value = day
    approvalReason.value = ''
  })
}

async function saveApproval() {
  await approvalForm.save(async (current) => {
    const { error } = await api.POST('/api/ops/approve_attendance', {
      body: {
        staff_id: props.staffId,
        business_date: approvalTarget.value!.business_date,
        reason: approvalReason.value
      }
    })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    emit('changed')
    void reload()
    if (!current()) return
    approvalOpen.value = false
    toast.add({ title: '已核准', color: 'success' })
  })
}

const revokeForm = useModalForm()
const { open: revokeOpen, saving: revokeSaving } = revokeForm
const revokeTarget = ref<Schema['AttendanceDay']>()

function askRevoke(day: Schema['AttendanceDay']) {
  revokeForm.begin(() => {
    revokeTarget.value = day
  })
}

async function saveRevoke() {
  await revokeForm.save(async (current) => {
    const { error } = await api.POST('/api/ops/revoke_attendance_approval', {
      body: { id: revokeTarget.value!.approval!.id }
    })
    if (error) {
      if (current()) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    emit('changed')
    void reload()
    if (!current()) return
    revokeOpen.value = false
    toast.add({ title: '已撤销核准', color: 'success' })
  })
}
</script>

<template>
  <div class="space-y-6" data-testid="attendance-detail" :aria-busy="loading">
    <section v-if="attendance" class="space-y-3">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <p data-testid="attendance-summary-line">{{ summaryText(attendance.summary) }}</p>
        <UButton size="sm" @click="emit('add-punch', attendance.summary)">给该员工补卡</UButton>
      </div>
      <p v-if="!days.length" class="text-sm text-muted">本月还没有考勤明细</p>
      <div v-for="day in days" :key="day.business_date" data-testid="attendance-day" :data-date="day.business_date" class="space-y-2 rounded-lg border border-default p-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <p class="font-medium">{{ day.business_date }}</p>
          <div class="flex flex-wrap items-center gap-1">
            <UBadge v-if="day.late" color="warning" variant="subtle">迟到 {{ day.late_minutes }} 分钟</UBadge>
            <UBadge v-if="day.early" color="warning" variant="subtle">早退 {{ day.early_minutes }} 分钟</UBadge>
            <UBadge v-if="day.missing" color="error" variant="subtle">缺卡</UBadge>
            <UButton v-if="(day.late || day.early || day.missing) && !day.approval" size="xs" @click="askApprove(day)">核准</UButton>
            <UButton v-if="day.approval" size="xs" variant="outline" @click="askRevoke(day)">撤销核准</UButton>
          </div>
        </div>
        <p class="text-sm">班次：{{ day.shift ? `${day.shift.name} ${day.shift.start_time}–${day.shift.end_time}` : '无班次' }}</p>
        <p class="text-sm">上班：{{ formatAttendanceTime(day.clock_in) }} · 下班：{{ formatAttendanceTime(day.clock_out) }}</p>
        <p v-if="day.approval" class="text-sm text-muted">
          已核准 · {{ day.approval.reason }} · {{ day.approval.approved_by.name }} · {{ formatAttendanceDateTime(day.approval.approved_at) }}
        </p>
        <div class="space-y-1 border-t border-default pt-2">
          <div v-for="punch in day.punches" :key="punch.id" data-testid="attendance-punch" class="flex items-start justify-between gap-2 text-sm">
            <span :class="punch.voided ? 'text-muted line-through' : ''">
              {{ formatAttendanceTime(punch.at) }} · {{ punchText(punch) }}
              <template v-if="punch.voided">
                · 已作废 · {{ punch.void_reason }} · {{ punch.voided_by?.name }}
              </template>
            </span>
            <UButton v-if="punch.source === 'manual' && !punch.voided" size="xs" color="error" variant="ghost" @click="askVoid(punch)">作废</UButton>
          </div>
        </div>
      </div>
    </section>

    <UModal v-model:open="voidOpen" title="作废补卡">
      <template #body>
        <div class="space-y-3">
          <p v-if="voidTarget">补卡时间：{{ formatAttendanceDateTime(voidTarget.at) }}，原因：{{ voidTarget.reason }}</p>
          <UFormField label="作废原因"><UInput v-model="voidReason" :maxlength="100" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer><UButton :loading="voidSaving" @click="saveVoid">保存</UButton></template>
    </UModal>

    <UModal v-model:open="approvalOpen" title="核准">
      <template #body>
        <div class="space-y-3">
          <p v-if="approvalTarget">{{ approvalTarget.business_date }} 的异常：{{ [approvalTarget.late ? `迟到 ${approvalTarget.late_minutes} 分钟` : '', approvalTarget.early ? `早退 ${approvalTarget.early_minutes} 分钟` : '', approvalTarget.missing ? '缺卡' : ''].filter(Boolean).join(' · ') }}</p>
          <UFormField label="原因"><UInput v-model="approvalReason" :maxlength="100" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer><UButton :loading="approvalSaving" @click="saveApproval">保存</UButton></template>
    </UModal>

    <UModal v-model:open="revokeOpen" title="撤销核准">
      <template #body>
        <p>撤销 {{ revokeTarget?.business_date }} 的核准？撤销后这一天的异常重新计入统计。</p>
      </template>
      <template #footer><UButton color="error" :loading="revokeSaving" @click="saveRevoke">确认撤销</UButton></template>
    </UModal>
  </div>
</template>
