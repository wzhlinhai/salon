<script setup lang="ts">
import type { components } from '~/utils/api'

// 首页的「接下来」（样稿 01）：一条竖着的时间线，只回答接下来两个小时。
// 做完的收成一行，正在做的实心点，现在一条红线，两小时以后的也收成一行。
// 传 staffId 时只列这个人的（个人首页），不画做的人的头像。
const props = defineProps<{ date: string, staffId?: string }>()
// 正在做的单是哪位顾客，给「在店里」写「做王女士」。
const emit = defineEmits<{ serving: [value: Record<string, string>] }>()

type Appointment = components['schemas']['Appointment']
type Walkin = components['schemas']['WalkinOccupancyView']
type Overview = components['schemas']['CustomerOverview']
type Totals = components['schemas']['BusinessReport']['totals']

const api = useApi()
const { can } = useMe()
const WINDOW = 2 * 60 * 60_000

const entries = ref<Appointment[]>([])
const walkIns = ref<Walkin[]>([])
const overviews = ref<Record<string, Overview>>({})
const totals = ref<Totals | null>(null)
// 每位顾客最近一次写了配方的服务，一次打开只取一回。
const formulas = ref<Record<string, { date: string, item: string, formula: string } | null>>({})
const loaded = ref(false)
const failed = ref('')
const guard = useRequestGuard()
const now = ref(Date.now())

async function load() {
  if (!props.date) return
  const current = guard.next()
  const body = props.staffId ? { from: props.date, to: props.date, staff_id: props.staffId } : { from: props.date, to: props.date }
  const [list, overview, report] = await Promise.all([
    api.POST('/api/ops/list_appointments', { body }),
    can('customer.view') ? api.POST('/api/ops/list_customer_overview', { body: { segment: 'today', limit: 100 } }) : null,
    !props.staffId && can('report.view') ? api.POST('/api/ops/get_business_report', { body: { from: props.date, to: props.date, granularity: 'day' } }) : null
  ])
  if (!current()) return
  if (!list.data) {
    // 拿过一次就留着旧的，一次失败不把整条时间线换成报错。
    if (!loaded.value) failed.value = errorText(list.error)
    return
  }
  failed.value = ''
  entries.value = list.data.entries.filter(entry => entry.status !== 'cancelled')
  walkIns.value = list.data.walk_ins.filter(walk => !props.staffId || walk.staff.id === props.staffId)
  overviews.value = Object.fromEntries((overview?.data?.items ?? []).map(row => [row.customer.id, row]))
  totals.value = report?.data?.totals ?? null
  loaded.value = true
  emit('serving', Object.fromEntries(entries.value.filter(entry => entry.order_id && entry.customer?.name).map(entry => [entry.order_id!, entry.customer!.name!])))
  void loadFormulas()
}

// 正在取的顾客，下一分钟的刷新不再重发；取失败的不记，下一分钟再试。
const fetching = new Set<string>()

async function loadFormulas() {
  if (!can('customer.view')) return
  const ids = rows.value.filter(row => row.near && row.customerId && !(row.customerId in formulas.value) && !fetching.has(row.customerId)).map(row => row.customerId!)
  await Promise.all([...new Set(ids)].map(async (id) => {
    fetching.add(id)
    const { data } = await api.POST('/api/ops/list_service_history', { body: { customer_id: id, limit: 10 } })
    fetching.delete(id)
    if (!data) return
    const hit = data.items.find(item => item.record?.formula)
    formulas.value[id] = hit ? { date: hit.business_date, item: hit.item.name, formula: hit.record!.formula! } : null
  }))
}

watch(() => [props.date, props.staffId], load, { immediate: true })

let timer: ReturnType<typeof setInterval> | undefined

// 切回这个标签页时马上走钟、重取，不等下一个整分钟。
function onVisible() {
  if (document.visibilityState !== 'visible') return
  now.value = Date.now()
  void load()
}

onMounted(() => {
  // 每分钟走一次钟，并重新取一次数据：前台平板常开，到店、开单要自己更新。
  timer = setInterval(() => {
    now.value = Date.now()
    if (document.visibilityState === 'visible') void load()
  }, 60_000)
  document.addEventListener('visibilitychange', onVisible)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  document.removeEventListener('visibilitychange', onVisible)
})

function clock(value: string | number) {
  return storeTimeShort(value)
}

function monthDay(date: string) {
  const [, month, day] = date.split('-').map(Number)
  return `${month}/${day}`
}

interface Row {
  key: string
  at: number
  iso: string
  time: string
  who: string
  title: string
  items: string
  // serving 实心点，late 黄签，hold 私约占位。
  state: 'serving' | 'late' | 'waiting' | 'hold'
  tag: string
  remain: string
  customerId: string | null
  note: string
  // 正在做的和接下来最先到的几位：去取上次的配方。
  near: boolean
}

function minutesFrom(value: string) {
  return Math.floor((now.value - new Date(value).getTime()) / 60_000)
}

function appointmentRow(entry: Appointment): Row {
  const base = {
    key: entry.id,
    at: new Date(entry.starts_at).getTime(),
    iso: entry.starts_at,
    time: clock(entry.starts_at),
    who: entry.staff.name,
    items: entry.items.map(item => item.name).join(' · '),
    customerId: entry.customer?.id ?? null,
    note: entry.note ?? '',
    remain: '',
    tag: '',
    near: false
  }
  if (entry.kind === 'block') return { ...base, title: entry.note || '私约占位', note: '', customerId: null, state: 'hold' }
  const title = entry.customer?.name || UNNAMED_CUSTOMER
  if (entry.order_id) {
    const left = -minutesFrom(entry.ends_at)
    return { ...base, title, state: 'serving', remain: left > 0 ? `还要 ${left} 分钟` : '' }
  }
  // 到点没来的口径在 utils/storeClock.ts，和预约页共用；多半不来了的已在 rows 里滤掉。
  const late = minutesLate(entry.starts_at, now.value)
  if (late) return { ...base, title, state: 'late', tag: `过了 ${late} 分钟没到 · 打个电话` }
  return { ...base, title, state: 'waiting', tag: entry.status === 'pending' ? '待确认' : '' }
}

const open = computed(() => entries.value.filter(entry => entry.status === 'pending' || entry.status === 'confirmed'))

const rows = computed<Row[]>(() => {
  const list = open.value
    .filter((entry) => {
      if (entry.order_id) return true
      return new Date(entry.starts_at).getTime() <= now.value + WINDOW && !probablyNotComing(entry.starts_at, now.value)
    })
    .map(appointmentRow)
  for (const walk of walkIns.value) {
    const left = -minutesFrom(walk.ends_at)
    list.push({
      key: `walk-${walk.order_id}`,
      at: new Date(walk.starts_at).getTime(),
      iso: walk.starts_at,
      time: clock(walk.starts_at),
      who: walk.staff.name,
      title: '散客',
      items: '',
      state: 'serving',
      tag: '',
      remain: left > 0 ? `还要 ${left} 分钟` : '',
      customerId: null,
      note: '',
      near: false
    })
  }
  list.sort((a, b) => a.at - b.at)
  // 正在做的和还没到的头三位去取配方：做的人要照着调，等的人提前备料。
  let ahead = 0
  for (const row of list) {
    if (row.state === 'serving') row.near = true
    else if (row.state !== 'hold' && ahead < 3) {
      row.near = true
      ahead++
    }
  }
  return list
})

// 现在这条红线画在第一位还没开始的前面。
const nowIndex = computed(() => {
  const index = rows.value.findIndex(row => row.at > now.value)
  return index < 0 ? rows.value.length : index
})

// 做完的收成一行：能看报表的写营业额（和营业报表同一口径），否则只数做完的预约。
const done = computed(() => {
  if (totals.value) {
    const guests = Number(totals.value.guest_total)
    if (!guests) return ''
    return `做完 ${formatGuest(totals.value.guest_total)} 位 · ${yuan(totals.value.service_revenue)}`
  }
  const count = entries.value.filter(entry => entry.kind === 'appointment' && entry.status === 'completed').length
  return count ? `做完 ${count} 位` : ''
})

// 两小时以后的也收成一行：还有几位，下一位几点、是谁。
const later = computed(() => {
  const rest = open.value
    .filter(entry => entry.kind === 'appointment' && !entry.order_id && new Date(entry.starts_at).getTime() > now.value + WINDOW)
    .sort((a, b) => a.starts_at.localeCompare(b.starts_at))
  const first = rest[0]
  if (!first) return ''
  return `还有 ${rest.length} 位 · ${clock(first.starts_at)} ${first.customer?.name || UNNAMED_CUSTOMER}`
})

// 第二行：正在做和快到的写上次的配方；其余写是第几次来、卡里还有多少。
function detail(row: Row) {
  if (!row.customerId) return ''
  const formula = row.near ? formulas.value[row.customerId] : null
  if (formula) return `上次 ${monthDay(formula.date)} ${formula.item}，配方 ${formula.formula}`
  const overview = overviews.value[row.customerId]
  if (!overview) return ''
  if (!overview.visit_count) return '新客'
  const balance = Number(overview.stored_balance) > 0 ? ` · 卡余 ${yuan(overview.stored_balance)}` : ''
  return `老顾客 · 第 ${overview.visit_count + 1} 次来${balance}`
}

const dateLabel = computed(() => {
  const [year, month, day] = props.date.split('-').map(Number)
  const weekday = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'][new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, day ?? 1)).getUTCDay()]
  return `${month} 月 ${day} 日 ${weekday}`
})

const empty = computed(() => loaded.value && !rows.value.length && !done.value && !later.value)
</script>

<template>
  <section data-testid="home-next" class="min-w-0">
    <header class="mb-3 flex items-baseline gap-2">
      <h2 class="text-base font-semibold">接下来</h2>
      <span class="text-meta text-muted tabular-nums">{{ dateLabel }}</span>
      <NuxtLink v-if="can('booking.manage')" to="/bookings" class="tap-area ms-auto inline-flex items-center text-meta text-muted hover:text-default">
        打开预约<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" />
      </NuxtLink>
    </header>

    <LoadError v-if="failed" title="今天的预约没有读出来" :hint="failed" @retry="load()" />
    <p v-else-if="empty" class="py-2 text-sm text-muted">今天还没有预约。</p>
    <ol v-else-if="loaded" class="next tabular-nums">
      <li v-if="done" class="row" data-testid="next-done">
        <span class="time">之前</span>
        <span class="rail"><i class="dot" /></span>
        <NuxtLink v-if="totals" to="/reports/business" class="tap-area body inline-flex items-center text-sm text-muted hover:text-default">
          {{ done }}<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" />
        </NuxtLink>
        <span v-else class="body text-sm text-muted">{{ done }}</span>
      </li>
      <template v-for="(row, index) in rows" :key="row.key">
        <li v-if="index === nowIndex" class="now">
          <span class="time"><span class="sr-only">现在 </span><time :datetime="new Date(now).toISOString()">{{ clock(now) }}</time></span>
          <i aria-hidden="true" />
        </li>
        <li class="row" data-testid="next-row" :data-state="row.state">
          <span class="time"><time :datetime="row.iso">{{ row.time }}</time></span>
          <span class="rail"><i class="dot" :class="row.state === 'serving' && 'dot-on'" /></span>
          <div class="body min-w-0">
            <p class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm" :class="row.state === 'hold' && 'text-muted'">
              <span v-if="!props.staffId" class="grid size-5 shrink-0 place-items-center rounded-full bg-tag-gray text-xs" :title="row.who" aria-hidden="true">{{ row.who.slice(0, 1) }}</span>
              <span v-if="!props.staffId" class="sr-only">{{ row.who }}：</span>
              <span v-if="row.state === 'serving'" class="sr-only">正在做：</span>
              <span class="font-medium">{{ row.title }}</span>
              <span v-if="row.items">{{ row.items }}</span>
              <span v-if="row.tag" class="tag bg-tag-yellow">{{ row.tag }}</span>
              <!-- 没有第二行（散客、占位）时，还要多久跟在名字后面，一位只占一行。 -->
              <span v-if="row.remain && !detail(row)" class="text-meta text-muted">{{ row.remain }}</span>
            </p>
            <p v-if="detail(row)" class="mt-1 flex flex-wrap gap-x-4 text-meta text-muted">
              <span>{{ detail(row) }}</span>
              <span v-if="row.remain">{{ row.remain }}</span>
            </p>
            <p v-if="row.note" class="said mt-1 text-sm text-toned">{{ row.note }}</p>
          </div>
        </li>
      </template>
      <li v-if="nowIndex === rows.length" class="now">
        <span class="time"><span class="sr-only">现在 </span><time :datetime="new Date(now).toISOString()">{{ clock(now) }}</time></span>
        <i aria-hidden="true" />
      </li>
      <!-- 只剩「之前」「之后」时，现在这条红线下面明说两小时内是空的，不让人对着一行「之前」猜。 -->
      <li v-if="!rows.length" class="row" data-testid="next-none">
        <span class="time" />
        <span class="rail"><i class="dot" /></span>
        <span class="body text-sm text-muted">两小时内没有预约，也没有在做的</span>
      </li>
      <li v-if="later" class="row" data-testid="next-later">
        <span class="time">之后</span>
        <span class="rail"><i class="dot" /></span>
        <span class="body text-sm text-muted">{{ later }}</span>
      </li>
    </ol>
  </section>
</template>

<style scoped>
/* 时间 48、竖线 24、内容；竖线是每行中间一道 1px，点压在线上。 */
.row,
.now {
  display: grid;
  grid-template-columns: 48px 24px minmax(0, 1fr);
  column-gap: 8px;
}
.row {
  padding-bottom: 16px;
}
.time {
  padding-top: 2px;
  text-align: right;
  font-size: var(--text-meta);
  color: var(--ui-text-muted);
}
.rail {
  position: relative;
}
.rail::before {
  content: "";
  position: absolute;
  top: 0;
  bottom: -16px;
  left: 50%;
  width: 1px;
  background: var(--ui-border);
}
.row:last-child .rail::before {
  bottom: 0;
}
.dot {
  position: relative;
  display: block;
  width: 8px;
  height: 8px;
  margin: 8px auto 0;
  border-radius: 999px;
  background: var(--color-white);
  box-shadow: inset 0 0 0 1px var(--ui-border-accented);
}
.dot-on {
  background: var(--color-ink);
  box-shadow: none;
}
.now {
  align-items: center;
  margin: -8px 0 8px;
}
.now .time {
  color: var(--color-ink-red);
}
.now i {
  grid-column: 2 / 4;
  height: 1px;
  background: var(--color-ink-red);
}
</style>
