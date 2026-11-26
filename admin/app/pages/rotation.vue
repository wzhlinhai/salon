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
  // 后端按发生先后（同一刻再按 id）给，倒过来就是新的在上；不按时间字符串比，小数位数不一会比反。
  events.value = [...data.entries].reverse()
  // 顾客名取不到只是少写几个字，断网时不报错。
  void loadOrders().catch(() => {})
}

const shownEvents = computed(() => showAllEvents.value ? events.value : events.value.slice(0, 8))

// ---- 今天的轮次（样稿 04）：谁接了谁，轮到的还是点的 ----
// 撤销接客抵掉这人上一次接客。接的是哪位顾客：这人接客之后第一次结账记下的单；还在做的，找他名下最近开的挂单。
type Order = components['schemas']['Order']
interface Round { id: string, at: string, staff: RotationEvent['staff'], designated: boolean, orderId: string | null }

const rounds = computed<Round[]>(() => {
  const asc = [...events.value].reverse()
  const kept: Round[] = []
  const stacks: Record<string, Round[]> = {}
  for (const e of asc) {
    const stack = (stacks[e.staff.id] ??= [])
    if (e.kind === 'assign') {
      const round = { id: e.id, at: e.created_at, staff: e.staff, designated: !!e.designated, orderId: e.order_id }
      stack.push(round)
      kept.push(round)
    } else if (e.kind === 'release') {
      const dropped = stack.pop()
      if (dropped) kept.splice(kept.indexOf(dropped), 1)
    } else if (e.order_id && e.kind.startsWith('advance')) {
      const open = stack.findLast(r => !r.orderId)
      if (open) open.orderId = e.order_id
    }
  }
  // 已经认过的单不再配给别人。
  const claimed = new Set(kept.map(r => r.orderId).filter(Boolean))
  for (const turn of onDuty.value) {
    if (turn.status !== 'serving') continue
    const last = stacks[turn.staff.id]?.at(-1)
    if (!last || last.orderId) continue
    last.orderId = turn.serving_order_id ?? servingOrder(turn.staff.id, last.at, claimed)?.id ?? null
    if (last.orderId) claimed.add(last.orderId)
  }
  return kept.reverse()
})

// 每人今天接了几位：轮到的和点的分开数。
const tally = computed(() => {
  const out: Record<string, { round: number, designated: number }> = {}
  for (const r of rounds.value) {
    const row = (out[r.staff.id] ??= { round: 0, designated: 0 })
    if (r.designated) row.designated++
    else row.round++
  }
  return out
})

function tallyText(staffId: string) {
  const row = tally.value[staffId]
  if (!row) return '今天还没接'
  return [`今天 ${row.round + row.designated} 位`, row.round && `轮 ${row.round}`, row.designated && `点 ${row.designated}`].filter(Boolean).join(' · ')
}

const canOrders = computed(() => can('order.create'))
const orders = ref<Order[]>([])
const customerNames = ref<Record<string, string>>({})
const ordersGuard = useRequestGuard()

// 和轮牌一起刷新；没有开单权限的只写「接客」，不写接的是谁。
async function loadOrders() {
  if (!canOrders.value) return
  const request = ordersGuard.next()
  const [open, done] = await Promise.all([
    api.POST('/api/ops/list_open_orders', { body: {} }),
    api.POST('/api/ops/list_orders', { body: { limit: 100 } })
  ])
  if (!request() || !open.data) return
  orders.value = [...open.data.items, ...(done.data?.items ?? [])]
  if (!can('customer.view')) return
  const ids = [...new Set(orders.value.map(o => o.customer_id))].filter(id => !(id in customerNames.value))
  const found = await Promise.all(ids.map(id => api.POST('/api/ops/get_customer', { body: { id } })))
  for (const { data } of found) {
    if (data) customerNames.value[data.id] = data.unidentified ? '散客' : (data.name || '未留名顾客')
  }
}

// 接客之后开的、他在单上、还没被别人认走的挂单里，最早开的那张。
function servingOrder(staffId: string, since: string, claimed: Set<string | null>) {
  const from = Date.parse(since)
  return orders.value
    .filter(o => o.status === 'open' && !claimed.has(o.id) && Date.parse(o.opened_at) >= from && o.items.some(i => !i.removed_at && i.positions.some(p => p.staff_id === staffId)))
    .sort((a, b) => Date.parse(a.opened_at) - Date.parse(b.opened_at))[0] ?? null
}

function orderOf(id: string | null) {
  return id ? orders.value.find(o => o.id === id) ?? null : null
}

function customerOf(order: Order | null) {
  return order ? customerNames.value[order.customer_id] ?? '' : ''
}

function roundText(r: Round) {
  const order = orderOf(r.orderId)
  const name = customerOf(order)
  return { who: name ? `接 ${name}` : '接客', name, items: order ? orderItemNames(order).replaceAll('、', ' · ') : '' }
}

// 认得出的顾客点名字弹小卡片（样稿 26）；散客没有档案可看。
function peekId(r: Round) {
  const order = orderOf(r.orderId)
  return order && can('customer.view') && customerNames.value[order.customer_id] && customerNames.value[order.customer_id] !== '散客' ? order.customer_id : null
}

// 椅子上那一行：在做的写做谁，其余写在干什么。
function doingText(t: Turn) {
  if (t.status === 'serving') {
    const last = rounds.value.find(r => r.staff.id === t.staff.id)
    const name = customerOf(orderOf(last?.orderId ?? null))
    return [name ? `做${name}` : '在做', t.serving_designated && '点客'].filter(Boolean).join(' · ')
  }
  if (t.status === 'resting') return `休息到 ${formatAttendanceTime(t.rest_until)}`
  if (t.status === 'covered') return '盖牌'
  return '等着'
}

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
</script>

<template>
  <div class="space-y-4">
    <PageDescriptionTail v-if="rulesText">今天的规则：{{ rulesText }}。</PageDescriptionTail>

    <PageActions>
      <UDropdownMenu v-if="canOperate" :items="settingsMenu" :content="{ align: 'end' }">
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-ellipsis" aria-label="队列设置" />
      </UDropdownMenu>
    </PageActions>

    <ListSkeleton v-if="!queuesLoaded" />

    <EmptyState v-else-if="queuesFailed" icon="i-lucide-cloud-off" title="轮牌没有加载出来" hint="检查网络后重试">
      <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" @click="loadQueues">重试</UButton>
    </EmptyState>

    <EmptyState v-else-if="!activeQueues.length" art="chair" title="还没有轮牌队列" hint="先建一个队列，例如「剪发牌」，再选参加的员工和默认顺序">
      <UButton v-if="canOperate" @click="askName(null)">新建队列</UButton>
    </EmptyState>

    <template v-else>
      <ViewTabs
        v-if="activeQueues.length > 1"
        :model-value="queueId"
        :items="activeQueues.map(q => ({ key: q.id, label: q.name, count: q.members.length }))"
        label="轮牌队列"
        @update:model-value="pickQueue"
      />

      <p v-if="rotation && !nextUp" class="text-sm text-muted">现在没有人在等，{{ onDuty.length ? '等有人空下来' : '员工到岗后点「到岗」上牌' }}</p>

      <ListSkeleton v-if="rotationLoading && !rotation" />

      <template v-else-if="rotation">
        <!-- 一排椅子（样稿 04）：按轮牌顺序，轮到的描黑边；没到岗的排在后面，淡一层。 -->
        <div class="chairs">
          <ol v-if="onDuty.length" class="contents" role="list" data-testid="on-duty">
            <li
              v-for="(t, i) in onDuty"
              :key="t.staff.id"
              class="chair"
              :class="t === nextUp && 'chair-next'"
              :data-staff="t.staff.name"
              :data-status="t.status"
            >
              <div class="flex h-7 items-center justify-between gap-2">
                <p class="text-meta text-muted">第 {{ i + 1 }} 位<template v-if="t === nextUp"> · 轮到</template></p>
                <UDropdownMenu v-if="canOperate && rowActions(t)[0]!.length" :items="rowActions(t)" :content="{ align: 'end' }">
                  <UButton
                    size="sm"
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-ellipsis"
                    class="-me-2"
                    :aria-label="`${t.staff.name} 的操作`"
                    :disabled="busy"
                  />
                </UDropdownMenu>
              </div>
              <div class="mt-2 flex items-center gap-3">
                <PersonAvatar :name="t.staff.name" size="md" />
                <div class="min-w-0">
                  <p class="truncate text-base font-semibold" :title="t.staff.name" :data-testid="t === nextUp ? 'next-up' : undefined">{{ who(t.staff) }}</p>
                  <p class="truncate text-meta text-muted tabular-nums">{{ tallyText(t.staff.id) }}</p>
                </div>
              </div>
              <p class="mt-3 text-sm" :class="t.status === 'serving' ? 'text-default' : 'text-muted'">
                {{ doingText(t) }}<span v-if="t.half_pending" class="text-muted"> · 已记半轮</span>
              </p>
              <div v-if="canOperate" class="mt-auto flex items-center gap-2 pt-4">
                <template v-if="t === nextUp">
                  <UButton size="lg" class="flex-1 justify-center whitespace-nowrap" :disabled="busy" data-testid="assign-next" @click="assignNext">接散客</UButton>
                  <UButton size="lg" color="neutral" variant="outline" class="whitespace-nowrap" :disabled="busy" @click="askSkip(t)">跳过…</UButton>
                </template>
                <UButton v-else-if="t.status === 'waiting'" color="neutral" variant="outline" :disabled="busy" @click="designate(t)">点客</UButton>
                <UButton v-else-if="t.status === 'serving'" color="neutral" variant="ghost" :disabled="busy" @click="release(t)">撤销接客</UButton>
                <UButton v-else-if="t.status === 'resting'" color="neutral" variant="outline" :disabled="busy" @click="endRest(t)">结束休息</UButton>
                <UButton v-else-if="t.status === 'covered'" color="neutral" variant="outline" :disabled="busy" @click="uncover(t)">取消盖牌</UButton>
              </div>
            </li>
          </ol>
          <ul v-if="offDuty.length" class="contents" role="list" data-testid="off-duty">
            <li v-for="s in offDuty" :key="s.id" class="chair chair-off" :data-staff="s.name">
              <p class="flex h-7 items-center text-meta text-muted">不在牌上</p>
              <div class="mt-2 flex items-center gap-3">
                <PersonAvatar :name="s.name" size="md" />
                <div class="min-w-0">
                  <p class="truncate text-base font-semibold text-muted" :title="s.name">{{ who(s) }}</p>
                  <p class="text-meta text-muted">还没到岗</p>
                </div>
              </div>
              <div v-if="canOperate" class="mt-auto pt-4">
                <UButton color="neutral" variant="outline" :disabled="busy" @click="join(s)">到岗</UButton>
              </div>
            </li>
          </ul>
        </div>

        <EmptyState v-if="!onDuty.length && !offDuty.length" art="chair" title="这个队列还没有成员" hint="选好参加的员工和默认顺序后，员工到店就能上牌">
          <UButton v-if="canOperate && queue" icon="i-lucide-users" @click="members?.start(queue)">选成员</UButton>
        </EmptyState>

        <section class="pt-4">
          <h2 class="mb-3 flex items-baseline gap-2">
            <span class="text-base font-semibold">今天的轮次</span>
            <span class="text-meta text-dimmed">谁接了谁</span>
          </h2>
          <ol v-if="rounds.length" class="rounds" data-testid="rotation-rounds">
            <li v-for="r in rounds" :key="r.id" class="flex min-h-10 items-center gap-3 text-sm">
              <span class="w-12 shrink-0 text-end text-meta text-muted tabular-nums">{{ formatAttendanceTime(r.at) }}</span>
              <span class="rail" aria-hidden="true"><i /></span>
              <PersonAvatar :name="r.staff.name" size="sm" />
              <span class="min-w-0 truncate" :title="`${r.staff.name} ${roundText(r).who} ${roundText(r).items}`">
                {{ who(r.staff) }}
                <template v-if="peekId(r)">
                  接
                  <CustomerPeek :customer-id="peekId(r)!">
                    <button type="button" class="hover:underline focus-visible:underline" aria-haspopup="dialog">{{ roundText(r).name }}</button>
                  </CustomerPeek>
                </template>
                <template v-else>{{ roundText(r).who }}</template>
                <span v-if="roundText(r).items" class="ms-2 text-muted">{{ roundText(r).items }}</span>
              </span>
              <span class="tag bg-tag-gray"><span class="sr-only">{{ r.designated ? '点客' : '轮到的' }}</span><span aria-hidden="true">{{ r.designated ? '点' : '轮' }}</span></span>
            </li>
          </ol>
          <p v-else class="text-meta text-muted">今天还没有人接客。</p>

          <!-- 跳过、休息、调顺序这些也都记着，收在下面，要查再展开。 -->
          <details v-if="events.length" class="mt-4 text-meta">
            <summary class="cursor-pointer text-muted hover:text-default">全部记录 {{ events.length }} 条</summary>
            <ul class="mt-2 space-y-2" data-testid="rotation-events">
              <li v-for="e in shownEvents" :key="e.id" class="flex gap-3">
                <span class="w-12 shrink-0 text-end text-muted tabular-nums">{{ formatAttendanceTime(e.created_at) }}</span>
                <span>
                  <b class="font-medium">{{ who(e.staff) }}</b> {{ eventText(e) }}
                  <span v-if="e.kind === 'skip' || e.kind === 'drag'" class="text-muted"> · {{ who(e.actor) }} 操作</span>
                </span>
              </li>
            </ul>
            <UButton v-if="events.length > 8" size="sm" color="neutral" variant="ghost" class="mt-1" @click="showAllEvents = !showAllEvents">
              {{ showAllEvents ? '收起' : `显示全部 ${events.length} 条` }}
            </UButton>
          </details>
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

<style scoped>
/* 椅子：等宽卡片一排，放不下就换行；纸面白底、细边、大圆角（DESIGN.md 卡片）。 */
.chairs {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
  gap: 16px;
}

.chair {
  display: flex;
  flex-direction: column;
  min-height: 12rem;
  padding: 16px 20px;
  border: 1px solid var(--ui-border);
  border-radius: var(--radius-card);
  background: var(--ui-bg);
}

.chair-next {
  border-color: var(--ui-text);
  box-shadow: 0 0 0 1px var(--ui-text);
}

.chair-off {
  background: transparent;
  border-style: dashed;
}

/* 轮次时间线：一根竖线串起每一次接客。 */
.rounds .rail {
  position: relative;
  align-self: stretch;
  width: 10px;
  flex-shrink: 0;
}

.rounds .rail::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 4px;
  border-left: 1px solid var(--ui-border-accented);
}

.rounds .rail i {
  position: absolute;
  top: calc(50% - 5px);
  left: 0;
  width: 10px;
  height: 10px;
  border: 1px solid var(--ui-border-accented);
  border-radius: 999px;
  background: var(--ui-bg);
}
</style>
