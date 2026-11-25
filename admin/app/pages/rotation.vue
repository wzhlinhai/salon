<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'
import type { components } from '~/utils/api'

definePageMeta({ permission: 'queue.view', title: '轮牌', description: '到岗的员工按顺序轮流接客。跳牌、休息、调整顺序都会留下记录。', art: 'chair' })

type Queue = components['schemas']['Queue']
type Rotation = components['schemas']['Rotation']
type Turn = components['schemas']['Turn']
type RotationEvent = components['schemas']['RotationEvent']
type Setting = components['schemas']['Setting']
type SkipReason = components['schemas']['SkipStaffInput']['reason']

const api = useApi()
const route = useRoute()
const toast = useToast()
const { can, me } = useMe()
const canOperate = computed(() => can('queue.operate'))

// 本人在列表和记录里写「我」。
function who(s: { id: string, name: string } | null | undefined) {
  if (!s) return ''
  return s.id === me.value?.staff.id ? '我' : s.name
}

// ---- 队列 ----
const QUEUE_KEY = 'salon-rotation-queue'
const queues = ref<Queue[]>([])
const queuesLoaded = ref(false)
const queuesFailed = ref(false)
const queueId = ref('')
const activeQueues = computed(() => queues.value.filter(q => q.active))
const inactiveQueues = computed(() => queues.value.filter(q => !q.active))
const queue = computed(() => activeQueues.value.find(q => q.id === queueId.value) ?? null)

const queuesGuard = useRequestGuard()

async function loadQueues() {
  const request = queuesGuard.next()
  try {
    const { data, error } = await api.POST('/api/ops/list_rotation_queues', { body: {} })
    if (!request()) return
    if (!data) {
      queuesFailed.value = !queues.value.length
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    queuesFailed.value = false
    queues.value = data.entries
    if (!activeQueues.value.some(q => q.id === queueId.value)) {
      // 链接里带 ?queue= 的先用它（例如从首页轮牌点进来），其次是这台电脑上次看的队列。
      const wanted = [String(route.query.queue ?? ''), localStorage.getItem(QUEUE_KEY) ?? '']
      queueId.value = wanted.map(id => activeQueues.value.find(q => q.id === id)?.id).find(Boolean) ?? activeQueues.value[0]?.id ?? ''
    }
  } finally {
    if (request()) queuesLoaded.value = true
  }
}

function pickQueue(id: string) {
  queueId.value = id
  localStorage.setItem(QUEUE_KEY, id)
}

// ---- 今天的轮牌 ----
const rotation = ref<Rotation | null>(null)
const rotationLoading = ref(false)
const rotationGuard = useRequestGuard()

async function loadRotation(quiet = false) {
  const id = queueId.value
  const request = rotationGuard.next()
  if (!id) {
    rotation.value = null
    return
  }
  if (!quiet) rotationLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/get_rotation', { body: { queue_id: id } })
    if (!request()) return
    if (!data) {
      if (!quiet) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    rotation.value = data
  } finally {
    if (request()) rotationLoading.value = false
  }
  loadEvents()
}

watch(queueId, () => {
  rotation.value = null
  events.value = []
  loadRotation()
})

// 别的设备也在操作、休息到点会自动恢复：页面开着时每 30 秒静默刷新一次。
let timer: ReturnType<typeof setInterval> | undefined
// 已停用的员工还留在队列成员里，未到岗名单只列在职的人。
const activeStaff = ref<Set<string> | null>(null)

async function loadActiveStaff() {
  const { data } = await api.POST('/api/ops/list_staff_options', { body: {} })
  if (data) activeStaff.value = new Set(data.staff.filter(s => s.active).map(s => s.id))
}

onMounted(async () => {
  loadRules()
  loadActiveStaff()
  await loadQueues()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && !busy.value) loadRotation(true)
  }, 30_000)
})
onBeforeUnmount(() => clearInterval(timer))

const turns = computed(() => [...(rotation.value?.entries ?? [])].sort((a, b) => a.position - b.position))
const onDuty = computed(() => turns.value.filter(t => t.status !== 'off'))
const waiting = computed(() => onDuty.value.filter(t => t.status === 'waiting'))
const nextUp = computed(() => waiting.value[0] ?? null)
// 未到岗：当天名单里还没上牌的，加上「先到先排」时还没出现在名单里的成员。
const offDuty = computed(() => {
  const listed = new Set(turns.value.map(t => t.staff.id))
  const off = turns.value.filter(t => t.status === 'off').map(t => t.staff)
  const absent = [...(queue.value?.members ?? [])].sort((a, b) => a.sort_order - b.sort_order).map(m => m.staff).filter(s => !listed.has(s.id) && (activeStaff.value?.has(s.id) ?? true))
  return [...off, ...absent]
})
const counts = computed(() => ({
  serving: onDuty.value.filter(t => t.status === 'serving').length,
  away: onDuty.value.filter(t => t.status === 'resting' || t.status === 'covered').length
}))

// ---- 操作 ----
const busy = ref(false)
const actionGuard = useRequestGuard()

// 所有轮牌写操作都返回当天的轮牌：成功就直接换上，并刷新记录。
type Call = (queueId: string) => Promise<{ data?: Rotation, error?: unknown }>

async function act(call: Call, done: (r: Rotation) => string): Promise<boolean> {
  const id = queueId.value
  if (!id || busy.value) return false
  const request = actionGuard.next()
  busy.value = true
  try {
    const { data, error } = await call(id)
    if (!request()) return false
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      loadRotation(true)
      return false
    }
    if (queueId.value === id) {
      rotationGuard.invalidate()
      rotation.value = data
      loadEvents()
    }
    toast.add({ title: done(data), color: 'success' })
    return true
  } finally {
    if (request()) busy.value = false
  }
}

function servingName(r: Rotation, before: Set<string>) {
  return r.entries.find(t => t.status === 'serving' && !before.has(t.staff.id))?.staff.name
}

function assignNext() {
  const before = new Set(onDuty.value.filter(t => t.status === 'serving').map(t => t.staff.id))
  act(q => api.POST('/api/ops/assign_turn', { body: { queue_id: q } }), r => `轮到${servingName(r, before) ?? '下一位'}接客`)
}

function designate(t: Turn) {
  act(q => api.POST('/api/ops/assign_turn', { body: { queue_id: q, staff_id: t.staff.id, designated: true } }), () => `${t.staff.name} 点客接客`)
}

function release(t: Turn) {
  act(q => api.POST('/api/ops/release_turn', { body: { queue_id: q, staff_id: t.staff.id } }), () => `已撤销 ${t.staff.name} 这次接客，不算一轮`)
}

function join(staff: { id: string, name: string }) {
  act(q => api.POST('/api/ops/join_rotation', { body: { queue_id: q, staff_id: staff.id } }), () => `${staff.name} 已到岗`)
}

function leave(t: Turn) {
  act(q => api.POST('/api/ops/leave_rotation', { body: { queue_id: q, staff_id: t.staff.id } }), () => `${t.staff.name} 已离岗，位置保留到今天结束`)
}

function cover(t: Turn) {
  act(q => api.POST('/api/ops/cover_turn', { body: { queue_id: q, staff_id: t.staff.id } }), () => `${t.staff.name} 已盖牌`)
}

function endRest(t: Turn) {
  act(q => api.POST('/api/ops/end_rest', { body: { queue_id: q, staff_id: t.staff.id } }), () => `${t.staff.name} 已结束休息`)
}

function uncover(t: Turn) {
  act(q => api.POST('/api/ops/uncover_turn', { body: { queue_id: q, staff_id: t.staff.id } }), () => `${t.staff.name} 已取消盖牌`)
}

// 跳过
const SKIP_REASONS: SkipReason[] = ['迟到', '休息', '服务中', '顾客指定他人', '其他']
const skipOpen = ref(false)
const skipTarget = ref<Turn | null>(null)
const skipForm = reactive({ reason: '迟到' as SkipReason, note: '' })

function askSkip(t: Turn) {
  skipTarget.value = t
  skipForm.reason = '迟到'
  skipForm.note = ''
  skipOpen.value = true
}

async function submitSkip() {
  const t = skipTarget.value
  if (!t) return
  const note = skipForm.note.trim()
  if (skipForm.reason === '其他' && !note) {
    toast.add({ title: '原因选「其他」时要写说明', color: 'error' })
    return
  }
  const ok = await act(q => api.POST('/api/ops/skip_staff', { body: { queue_id: q, staff_id: t.staff.id, reason: skipForm.reason, note: note || undefined } }), () => `已跳过 ${t.staff.name}`)
  if (ok) skipOpen.value = false
}

// 临时休息
const REST_PRESETS = [15, 30, 60]
const restOpen = ref(false)
const restTarget = ref<Turn | null>(null)
const restMinutes = ref(30)

function askRest(t: Turn) {
  restTarget.value = t
  restMinutes.value = 30
  restOpen.value = true
}

async function submitRest() {
  const t = restTarget.value
  if (!t) return
  const ok = await act(q => api.POST('/api/ops/rest_staff', { body: { queue_id: q, staff_id: t.staff.id, minutes: restMinutes.value } }), () => `${t.staff.name} 休息 ${restMinutes.value} 分钟，到点自动回到等待`)
  if (ok) restOpen.value = false
}

// 调整顺序
const moveOpen = ref(false)
const moveTarget = ref<Turn | null>(null)
const moveForm = reactive({ to: 1, reason: '' })
// 序号和在岗列表一致（只数在岗的人）；提交的是后端位置，后端位置还包括未到岗的人。
const moveOptions = computed(() => onDuty.value.map((t, i) => ({ label: `第 ${i + 1} 位${t.staff.id === moveTarget.value?.staff.id ? '（现在）' : `（${t.staff.name} 的位置）`}`, value: t.position })))

function askMove(t: Turn) {
  moveTarget.value = t
  moveForm.to = t.position
  moveForm.reason = ''
  moveOpen.value = true
}

async function submitMove() {
  const t = moveTarget.value
  if (!t) return
  const reason = moveForm.reason.trim()
  if (!reason) {
    toast.add({ title: '写一下为什么调整顺序', color: 'error' })
    return
  }
  const place = onDuty.value.findIndex(x => x.position === moveForm.to) + 1
  const ok = await act(q => api.POST('/api/ops/drag_turn', { body: { queue_id: q, staff_id: t.staff.id, to_position: moveForm.to, reason } }), () => `${t.staff.name} 已移到第 ${place} 位`)
  if (ok) moveOpen.value = false
}

function rowActions(t: Turn): DropdownMenuItem[][] {
  const items: DropdownMenuItem[] = []
  if (t.status === 'waiting') {
    items.push(
      { label: '跳过…', icon: 'i-lucide-skip-forward', onSelect: () => askSkip(t) },
      { label: '临时休息…', icon: 'i-lucide-coffee', onSelect: () => askRest(t) },
      { label: '盖牌', icon: 'i-lucide-eye-off', onSelect: () => cover(t) },
      { label: '调整顺序…', icon: 'i-lucide-arrow-up-down', onSelect: () => askMove(t) }
    )
  }
  if (t.status !== 'serving') items.push({ label: '离岗', icon: 'i-lucide-log-out', onSelect: () => leave(t) })
  return [items]
}

// ---- 今日记录 ----
const events = ref<RotationEvent[]>([])
const eventsGuard = useRequestGuard()
const showAllEvents = ref(false)

async function loadEvents() {
  const day = rotation.value?.business_date
  const id = queueId.value
  const request = eventsGuard.next()
  if (!day || !id) return
  const { data } = await api.POST('/api/ops/list_rotation_events', { body: { queue_id: id, from: day, to: day } })
  if (!request() || !data || id !== queueId.value) return
  events.value = [...data.entries].sort((a, b) => b.created_at.localeCompare(a.created_at))
}

const shownEvents = computed(() => showAllEvents.value ? events.value : events.value.slice(0, 8))

function eventText(e: RotationEvent): string {
  const reason = e.reason ? `（${e.reason}${e.note ? `：${e.note}` : ''}）` : ''
  switch (e.kind) {
    case 'join': return '到岗'
    case 'leave': return '离岗'
    case 'assign': return e.designated ? '点客接客' : '轮牌接客'
    case 'release': return '撤销接客，不算一轮'
    case 'skip': return `被跳过${reason}`
    case 'auto_skip': return `自动跳过${reason}`
    case 'cover': return '盖牌'
    case 'uncover': return '取消盖牌'
    case 'rest': return `临时休息 ${e.minutes ?? ''} 分钟`
    case 'rest_end': return '休息结束，回到等待'
    case 'drag': return `${(e.to_position ?? 0) < (e.from_position ?? 0) ? '往前' : '往后'}调了顺序${reason}`
    case 'advance_whole': return '结账，算一轮排到队尾'
    case 'advance_half': return '结账，记半轮'
    case 'advance_designated_no_turn': return '点客结账，不占轮'
  }
  return e.kind
}

// ---- 门店规则（只读，改在门店设置） ----
const rules = ref<Setting[]>([])

async function loadRules() {
  const { data, error } = await api.POST('/api/ops/get_settings', { body: {} })
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  rules.value = data.settings.filter(s => ['rotation_order_rule', 'rotation_skip_policy', 'rotation_half_rule'].includes(s.key))
}

const rulesText = computed(() => rules.value
  .filter(s => s.key !== 'rotation_half_rule')
  .map(s => s.options?.find(o => o.value === s.value)?.label ?? '').filter(Boolean).join('，'))
// 半轮项目清单只在「按项目记半轮」时才有用，其它规则下不出现入口。
const halfByItem = computed(() => rules.value.some(s => s.key === 'rotation_half_rule' && s.value === 'by_item'))

// ---- 队列设置 ----
const members = ref<{ start: (q: Queue) => void } | null>(null)
const halfItems = ref<{ start: () => void } | null>(null)
const nameOpen = ref(false)
const nameForm = reactive({ id: '', name: '' })
const nameSaving = ref(false)
const nameGuard = useRequestGuard()

function askName(q: Queue | null) {
  nameGuard.next()
  nameSaving.value = false
  nameForm.id = q?.id ?? ''
  nameForm.name = q?.name ?? ''
  nameOpen.value = true
}

async function submitName() {
  const name = nameForm.name.trim()
  const id = nameForm.id
  if (!name || nameSaving.value) return
  const request = nameGuard.next()
  nameSaving.value = true
  try {
    const { data, error } = id
      ? await api.POST('/api/ops/update_rotation_queue', { body: { id, name } })
      : await api.POST('/api/ops/create_rotation_queue', { body: { name } })
    if (!request()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: id ? '已改名' : `已新建「${data.name}」，接着选成员`, color: 'success' })
    nameOpen.value = false
    const created = !id
    await loadQueues()
    if (created) {
      pickQueue(data.id)
      members.value?.start(data)
    }
  } finally {
    if (request()) nameSaving.value = false
  }
}

const deactivateOpen = ref(false)
const activeSaving = ref(false)

async function setActive(q: Queue, active: boolean) {
  if (activeSaving.value) return
  activeSaving.value = true
  const { data, error } = await api.POST('/api/ops/update_rotation_queue', { body: { id: q.id, active } }).finally(() => {
    activeSaving.value = false
  })
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  toast.add({ title: active ? `已启用「${q.name}」` : `已停用「${q.name}」`, color: 'success' })
  deactivateOpen.value = false
  await loadQueues()
  if (active) pickQueue(q.id)
}

const settingsMenu = computed<DropdownMenuItem[][]>(() => {
  const current = queue.value
  const groups: DropdownMenuItem[][] = []
  if (current) {
    groups.push([
      { label: '成员和顺序…', icon: 'i-lucide-users', onSelect: () => members.value?.start(current) },
      { label: '改名…', icon: 'i-lucide-pencil', onSelect: () => askName(current) },
      { label: '停用这个队列…', icon: 'i-lucide-archive', color: 'error', onSelect: () => {
        deactivateOpen.value = true
      } }
    ])
  }
  groups.push([
    { label: '新建队列…', icon: 'i-lucide-plus', onSelect: () => askName(null) },
    ...inactiveQueues.value.map(q => ({ label: `启用「${q.name}」`, icon: 'i-lucide-archive-restore', onSelect: () => setActive(q, true) }))
  ])
  groups.push([
    ...(halfByItem.value ? [{ label: '记半轮的项目…', icon: 'i-lucide-scissors', onSelect: () => halfItems.value?.start() }] : []),
    { label: '轮牌规则（门店设置）', icon: 'i-lucide-settings', to: '/settings' }
  ])
  return groups
})

function onMembersSaved() {
  loadQueues().then(() => loadRotation(true))
}

function statusClass(t: Turn) {
  // 正在做的墨色加粗，其余灰：颜色只留给有事的东西（DESIGN.md）。
  return t.status === 'serving' ? 'bg-tag-gray font-semibold' : 'bg-tag-gray'
}

function statusText(t: Turn) {
  if (t.status === 'serving') return t.serving_designated ? '服务中 · 点客' : '服务中'
  if (t.status === 'resting') return `休息到 ${formatAttendanceTime(t.rest_until)}`
  if (t.status === 'covered') return '盖牌'
  return ''
}
</script>

<template>
  <div class="space-y-4">
    <PageDescriptionTail v-if="rulesText">今天的规则：{{ rulesText }}。</PageDescriptionTail>

    <PageActions>
      <UDropdownMenu v-if="canOperate" :items="settingsMenu" :content="{ align: 'end' }">
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-ellipsis" aria-label="队列设置" />
      </UDropdownMenu>
      <UButton
        v-if="canOperate && queue"
        size="sm"
        icon="i-lucide-user-check"
        :disabled="!nextUp || busy"
        data-testid="assign-next"
        @click="assignNext"
      >
        轮牌接客
      </UButton>
    </PageActions>

    <ListSkeleton v-if="!queuesLoaded" />

    <EmptyState v-else-if="queuesFailed" icon="i-lucide-cloud-off" title="轮牌没有加载出来" hint="检查网络后重试">
      <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" @click="loadQueues">重试</UButton>
    </EmptyState>

    <EmptyState v-else-if="!activeQueues.length" art="chair" title="还没有轮牌队列" hint="先建一个队列，例如「剪发牌」，再选参加的员工和默认顺序">
      <UButton v-if="canOperate" icon="i-lucide-plus" @click="askName(null)">新建队列</UButton>
    </EmptyState>

    <template v-else>
      <ViewTabs
        v-if="activeQueues.length > 1"
        :model-value="queueId"
        :items="activeQueues.map(q => ({ key: q.id, label: q.name, count: q.members.length }))"
        label="轮牌队列"
        @update:model-value="pickQueue"
      />

      <div class="flex flex-wrap items-baseline gap-x-4 gap-y-1" data-testid="rotation-summary">
        <p v-if="nextUp" class="text-sm">下一位：<b data-testid="next-up">{{ who(nextUp.staff) }}</b></p>
        <p v-else-if="rotation" class="text-sm text-muted">现在没有人在等，{{ onDuty.length ? '等有人空下来' : '员工到岗后点「到岗」上牌' }}</p>
        <p class="ms-auto text-meta text-dimmed">等待 {{ waiting.length }} 人 · 服务中 {{ counts.serving }} 人<template v-if="counts.away"> · 休息或盖牌 {{ counts.away }} 人</template></p>
      </div>

      <ListSkeleton v-if="rotationLoading && !rotation" />

      <template v-else-if="rotation">
        <section>
          <div class="sec">在岗 <span class="font-normal text-dimmed">{{ onDuty.length }}</span></div>
          <ol v-if="onDuty.length" class="border-y border-default divide-y divide-default" data-testid="on-duty">
            <li
              v-for="(t, i) in onDuty"
              :key="t.staff.id"
              class="group/row flex min-h-14 items-center gap-3 px-3 py-2 hover:bg-hover"
              :data-staff="t.staff.name"
              :data-status="t.status"
            >
              <span class="w-6 text-end text-meta text-dimmed tabular-nums">{{ i + 1 }}</span>
              <PersonAvatar :name="t.staff.name" />
              <div class="min-w-0 flex-1">
                <span class="font-medium">{{ who(t.staff) }}</span>
                <span v-if="t === nextUp" class="ms-2 text-meta font-semibold">下一位</span>
                <span v-else-if="statusText(t)" class="tag ms-2" :class="statusClass(t)">{{ statusText(t) }}</span>
                <span v-if="t.half_pending" class="ms-2 text-meta text-dimmed">已记半轮</span>
              </div>
              <template v-if="canOperate">
                <UButton v-if="t.status === 'waiting'" size="sm" color="neutral" variant="outline" :disabled="busy" @click="designate(t)">点客</UButton>
                <UButton v-else-if="t.status === 'serving'" size="sm" color="neutral" variant="ghost" :disabled="busy" @click="release(t)">撤销接客</UButton>
                <UButton v-else-if="t.status === 'resting'" size="sm" color="neutral" variant="outline" :disabled="busy" @click="endRest(t)">结束休息</UButton>
                <UButton v-else-if="t.status === 'covered'" size="sm" color="neutral" variant="outline" :disabled="busy" @click="uncover(t)">取消盖牌</UButton>
                <div class="flex w-8 shrink-0 justify-end">
                  <UDropdownMenu v-if="rowActions(t)[0]!.length" :items="rowActions(t)" :content="{ align: 'end' }">
                    <UButton
                      size="sm"
                      color="neutral"
                      variant="ghost"
                      icon="i-lucide-ellipsis"
                      :aria-label="`${t.staff.name} 的操作`"
                      :disabled="busy"
                      class="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 aria-expanded:opacity-100 any-pointer-coarse:opacity-100"
                    />
                  </UDropdownMenu>
                </div>
              </template>
            </li>
          </ol>
          <EmptyState v-else-if="offDuty.length" icon="i-lucide-user-check" title="还没有人上牌" hint="员工到店后在下面点「到岗」" />
        </section>

        <section v-if="offDuty.length">
          <div class="sec">未到岗 <span class="font-normal text-dimmed">{{ offDuty.length }}</span></div>
          <ul class="border-y border-default divide-y divide-default" data-testid="off-duty">
            <li v-for="s in offDuty" :key="s.id" class="flex items-center gap-3 px-3 py-2 hover:bg-hover" :data-staff="s.name">
              <span class="w-6" aria-hidden="true" />
              <PersonAvatar :name="s.name" />
              <span class="min-w-0 flex-1 text-muted">{{ who(s) }}</span>
              <UButton v-if="canOperate" size="sm" color="neutral" variant="outline" :disabled="busy" @click="join(s)">到岗</UButton>
              <span v-if="canOperate" class="w-8 shrink-0" aria-hidden="true" />
            </li>
          </ul>
        </section>

        <EmptyState v-if="!onDuty.length && !offDuty.length" art="chair" title="这个队列还没有成员" hint="选好参加的员工和默认顺序后，员工到店就能上牌">
          <UButton v-if="canOperate && queue" icon="i-lucide-users" @click="members?.start(queue)">选成员</UButton>
        </EmptyState>

        <section>
          <div class="sec">今日记录</div>
          <ul v-if="events.length" class="space-y-2" data-testid="rotation-events">
            <li v-for="e in shownEvents" :key="e.id" class="flex gap-3 text-meta">
              <span class="w-11 shrink-0 text-dimmed tabular-nums">{{ formatAttendanceTime(e.created_at) }}</span>
              <span>
                <b class="font-medium">{{ who(e.staff) }}</b> {{ eventText(e) }}
                <span v-if="e.kind === 'skip' || e.kind === 'drag'" class="text-dimmed"> · {{ who(e.actor) }} 操作</span>
              </span>
            </li>
          </ul>
          <p v-else class="text-meta text-dimmed">今天还没有记录。</p>
          <UButton v-if="events.length > 8" size="sm" color="neutral" variant="ghost" class="mt-1" @click="showAllEvents = !showAllEvents">
            {{ showAllEvents ? '收起' : `显示全部 ${events.length} 条` }}
          </UButton>
        </section>
      </template>
    </template>

    <UModal v-model:open="skipOpen" :title="`跳过 ${skipTarget?.staff.name ?? ''}`" description="跳过会留下记录。按门店设置，被跳过的人排到队尾或保留原位。">
      <template #body>
        <UFormField label="原因">
          <URadioGroup v-model="skipForm.reason" :items="SKIP_REASONS" orientation="horizontal" />
        </UFormField>
        <UFormField v-if="skipForm.reason === '其他'" label="说明" class="mt-4" required>
          <UInput v-model="skipForm.note" maxlength="100" class="w-full" />
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="skipOpen = false">
          <UButton :loading="busy" @click="submitSkip">跳过</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="restOpen" :title="`${restTarget?.staff.name ?? ''} 临时休息`" description="休息期间不接客，到点自动回到等待，位置不变。">
      <template #body>
        <UFormField label="休息多久（分钟）">
          <div class="flex flex-wrap items-center gap-2">
            <UButton v-for="m in REST_PRESETS" :key="m" size="sm" color="neutral" :variant="restMinutes === m ? 'solid' : 'outline'" @click="restMinutes = m">{{ m }}</UButton>
            <UInputNumber v-model="restMinutes" :min="1" :max="240" class="w-32" />
          </div>
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="restOpen = false">
          <UButton :loading="busy" @click="submitRest">开始休息</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="moveOpen" :title="`调整 ${moveTarget?.staff.name ?? ''} 的顺序`" description="调整会留下记录，写明原因。">
      <template #body>
        <div class="space-y-5">
          <UFormField label="移到">
            <USelect v-model="moveForm.to" :items="moveOptions" class="w-full" />
          </UFormField>
          <UFormField label="原因" required>
            <UInput v-model="moveForm.reason" maxlength="200" placeholder="例如：顾客等了很久，先安排" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="moveOpen = false">
          <UButton :loading="busy" @click="submitMove">调整</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="nameOpen" :title="nameForm.id ? '队列改名' : '新建轮牌队列'" :description="nameForm.id ? undefined : '例如剪发牌、助理牌。一名员工可以同时在几个队列里。'">
      <template #body>
        <UFormField label="名称" required>
          <UInput v-model="nameForm.name" maxlength="20" class="w-full" @keydown.enter="submitName" />
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="nameOpen = false">
          <UButton :loading="nameSaving" :disabled="!nameForm.name.trim()" @click="submitName">{{ nameForm.id ? '保存' : '新建' }}</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="deactivateOpen" :title="`停用「${queue?.name ?? ''}」？`" description="停用后不再显示在轮牌页，记录都保留，以后可以在 ··· 菜单里重新启用。">
      <template #footer>
        <ModalActions @cancel="deactivateOpen = false">
          <UButton color="error" :loading="activeSaving" @click="queue && setActive(queue, false)">停用</UButton>
        </ModalActions>
      </template>
    </UModal>

    <RotationMembersModal ref="members" @saved="onMembersSaved" />
    <RotationHalfItemsModal ref="halfItems" />
  </div>
</template>
