<script setup lang="ts">
import type { components } from '~/utils/api'

// 首页的今日排班时间轴（照原型 J/K）：一行一位员工，块是预约、私约占位和正在服务的散客。
// 传 staffId 时只画这一个人，不显示名字列（个人首页"我的今天"）。
const props = defineProps<{ date: string, staffId?: string }>()
// 加载后报给首页：今天的预约共几位、几位还没到店（不算私约占位）。
const emit = defineEmits<{ summary: [value: { total: number, waiting: number }] }>()

type Appointment = components['schemas']['Appointment']
type Walkin = components['schemas']['WalkinOccupancyView']

const api = useApi()
const TIME_ZONE = 'Asia/Shanghai'

const entries = ref<Appointment[]>([])
const { categories: itemCategory, load: loadCategories } = useItemCategories()
const walkIns = ref<Walkin[]>([])
const hours = ref({ open: '10:00', close: '21:00' })
const loaded = ref(false)
const failed = ref('')
const guard = useRequestGuard()
const now = ref(Date.now())

async function load() {
  if (!props.date) return
  const current = guard.next()
  const body = props.staffId ? { from: props.date, to: props.date, staff_id: props.staffId } : { from: props.date, to: props.date }
  const [list, settings] = await Promise.all([
    api.POST('/api/ops/list_appointments', { body }),
    api.POST('/api/ops/get_settings', { body: { date: props.date } }),
    loadCategories()
  ])
  if (!current()) return
  if (!list.data) {
    failed.value = errorText(list.error)
    return
  }
  failed.value = ''
  entries.value = list.data.entries.filter(entry => entry.status !== 'cancelled')
  walkIns.value = list.data.walk_ins
  const setting = settings.data?.settings.find(row => row.key === 'business_hours')
  if (setting?.value) hours.value = setting.value as { open: string, close: string }
  loaded.value = true
  const visits = entries.value.filter(entry => entry.kind === 'appointment' && entry.status !== 'no_show')
  emit('summary', {
    total: visits.length,
    waiting: visits.filter(entry => !entry.order_id && entry.status !== 'completed').length
  })
}

watch(() => [props.date, props.staffId], load, { immediate: true })

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  // 每分钟走一次钟，并重新取一次数据：前台平板常开，到店、开单要自己更新。
  timer = setInterval(() => {
    now.value = Date.now()
    if (document.visibilityState === 'visible') void load()
  }, 60_000)
})
onBeforeUnmount(() => clearInterval(timer))

function clock(value: string) {
  const [h, m] = value.split(':').map(Number)
  return (h ?? 0) + (m ?? 0) / 60
}

// 门店所在时区的钟点（小时，带小数）。
function hourOf(value: string | number) {
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone: TIME_ZONE, hour: '2-digit', minute: '2-digit', hour12: false }).formatToParts(new Date(value))
  const hour = Number(parts.find(part => part.type === 'hour')?.value ?? 0) % 24
  const minute = Number(parts.find(part => part.type === 'minute')?.value ?? 0)
  return hour + minute / 60
}

function hhmm(hour: number) {
  return `${Math.floor(hour)}:${String(Math.round((hour % 1) * 60)).padStart(2, '0')}`
}

interface Block {
  key: string
  start: number
  end: number
  title: string
  detail: string
  short: string
  category: string
  state: string
  tone: string
}

const statusText: Record<Appointment['status'], string> = {
  pending: '待确认',
  confirmed: '',
  completed: '完成',
  cancelled: '已取消',
  no_show: '爽约'
}

function appointmentBlock(entry: Appointment): Block {
  const start = hourOf(entry.starts_at)
  const end = hourOf(entry.ends_at)
  const title = entry.kind === 'block' ? (entry.note || '私约占位') : (entry.customer?.name || '未留名顾客')
  const first = entry.items[0]
  const category = (first ? itemCategory.value[first.item_id] : undefined) ?? first?.name ?? ''
  const base = { key: entry.id, start, end, title, short: title.slice(0, 1), category }
  if (entry.kind === 'block') return { ...base, category: '占位', state: '', detail: `${hhmm(start)} · 占位`, tone: 'tl-hold' }
  const late = !entry.order_id && entry.status !== 'completed' && entry.status !== 'no_show'
    ? Math.floor((now.value - new Date(entry.starts_at).getTime()) / 60_000)
    : 0
  let state = statusText[entry.status]
  // 平时全白；正在做的灰底黑框，顾客过了时间没到的黄底（该打个电话），爽约红框（DESIGN.md「颜色只留给有事的东西」）。
  let tone = 'bg-white'
  if (entry.status === 'completed') {
    tone = 'bg-white opacity-45'
  } else if (entry.status === 'no_show') {
    tone = 'bg-white ring-alert'
  } else if (entry.order_id) {
    state = '服务中'
    tone = 'bg-elevated ring-serving'
  } else if (late > 5) {
    state = `迟到 ${late} 分`
    tone = 'bg-tag-yellow ring-pending late'
  }
  return { ...base, state, detail: [`${category} ${hhmm(start)}`, state].filter(Boolean).join(' · '), tone }
}

const lanes = computed(() => {
  const map = new Map<string, { id: string, name: string, blocks: Block[] }>()
  const lane = (staff: { id: string, name: string }) => {
    if (!map.has(staff.id)) map.set(staff.id, { id: staff.id, name: staff.name, blocks: [] })
    return map.get(staff.id)!
  }
  for (const entry of entries.value) lane(entry.staff).blocks.push(appointmentBlock(entry))
  for (const walk of walkIns.value) {
    const start = hourOf(walk.starts_at)
    lane(walk.staff).blocks.push({
      key: `walk-${walk.order_id}`,
      start,
      end: hourOf(walk.ends_at),
      title: '散客',
      short: '散',
      category: '散客',
      state: '服务中',
      detail: `${hhmm(start)} · 服务中`,
      tone: 'bg-elevated ring-serving'
    })
  }
  const rows = [...map.values()]
  if (props.staffId) return rows.filter(row => row.id === props.staffId)
  return rows.sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))
})

const range = computed(() => {
  let open = Math.floor(clock(hours.value.open))
  let close = Math.ceil(clock(hours.value.close))
  for (const row of lanes.value) {
    for (const block of row.blocks) {
      open = Math.min(open, Math.floor(block.start))
      close = Math.max(close, Math.ceil(block.end))
    }
  }
  return { open, close: Math.max(close, open + 1) }
})

const hourMarks = computed(() => {
  const marks: number[] = []
  for (let hour = range.value.open; hour <= range.value.close; hour++) marks.push(hour)
  return marks
})

function pct(hour: number) {
  return ((hour - range.value.open) / (range.value.close - range.value.open)) * 100
}

const nowHour = computed(() => {
  const hour = hourOf(now.value)
  return hour >= range.value.open && hour <= range.value.close ? hour : null
})

const whoWidth = computed(() => props.staffId ? '0px' : '80px')

defineExpose({ reload: load })
</script>

<template>
  <div data-testid="home-timeline">
    <p v-if="failed" class="text-sm text-muted">{{ failed }}</p>
    <p v-else-if="loaded && !lanes.length" class="py-2 text-sm text-dimmed">今天还没有预约。</p>
    <div v-else-if="loaded" class="tl relative hidden tabular-nums sm:block" :style="{ '--who': whoWidth }">
      <div class="tl-head">
        <div />
        <div class="relative h-[26px]">
          <span
            v-for="(hour, index) in hourMarks"
            :key="hour"
            class="absolute top-1 text-xs text-dimmed"
            :class="index === 0 ? '' : '-translate-x-1/2'"
            :style="{ left: `${pct(hour)}%` }"
          >{{ hour }}</span>
        </div>
      </div>
      <div v-for="row in lanes" :key="row.id" class="tl-row border-t border-default">
        <div class="overflow-hidden pt-2 pr-2 leading-tight">
          <b v-if="!props.staffId" class="block truncate text-sm font-medium">{{ row.name }}</b>
        </div>
        <div class="relative">
          <i
            v-for="hour in hourMarks.slice(1, -1)"
            :key="hour"
            class="absolute inset-y-0 w-px bg-line"
            :style="{ left: `${pct(hour)}%` }"
          />
          <div
            v-for="block in row.blocks"
            :key="block.key"
            data-testid="timeline-block"
            class="absolute top-[7px] bottom-[7px] overflow-hidden rounded-md px-2 py-1 text-xs leading-tight whitespace-nowrap shadow-line"
            :class="block.tone"
            :style="{ left: `${pct(block.start)}%`, width: `calc(${pct(block.end) - pct(block.start)}% - 3px)` }"
            :title="`${block.title} ${block.detail}`"
          >
            <template v-if="block.end - block.start < 0.75">
              <b class="block truncate font-medium">{{ block.short }}</b>
              <small class="block truncate text-xs text-muted">{{ block.category.slice(0, 1) }}</small>
            </template>
            <template v-else>
              <b class="block truncate font-medium">{{ block.title }}</b>
              <small class="block truncate text-xs text-muted">{{ block.detail }}</small>
            </template>
          </div>
        </div>
      </div>
      <div
        v-if="nowHour !== null"
        class="pointer-events-none absolute top-[22px] bottom-0 z-[2] w-0"
        :style="{ left: `calc(var(--who) + (100% - var(--who)) * ${pct(nowHour) / 100})` }"
      >
        <span class="absolute -top-0.5 left-0 -translate-x-1/2 rounded-md bg-ink-red px-1 text-xs whitespace-nowrap text-white">{{ hhmm(nowHour) }}</span>
        <i class="absolute top-3.5 bottom-0 -left-px w-0.5 bg-ink-red" />
      </div>
    </div>
    <!-- 窄屏横轴挤不下：改成按人分组、按时间排的列表。 -->
    <div v-if="loaded && !failed && lanes.length" class="flex flex-col gap-3 tabular-nums sm:hidden">
      <div v-for="row in lanes" :key="row.id">
        <b v-if="!props.staffId" class="mb-1 block text-sm font-medium">{{ row.name }}</b>
        <div
          v-for="block in [...row.blocks].sort((a, b) => a.start - b.start)"
          :key="block.key"
          class="flex items-center gap-2 border-t border-default py-2 text-sm"
          :class="block.tone.includes('opacity-45') ? 'opacity-45' : ''"
        >
          <span class="w-24 shrink-0 text-meta text-muted">{{ hhmm(block.start) }}–{{ hhmm(block.end) }}</span>
          <span class="min-w-0 truncate">{{ block.title }}</span>
          <span class="shrink-0 text-meta text-muted">{{ block.category }}</span>
          <span v-if="block.state" class="ms-auto shrink-0 text-meta" :class="block.tone.includes('ring-alert') ? 'text-ink-red' : block.tone.includes('late') ? 'text-ink-yellow' : 'text-muted'">{{ block.state }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tl-head,
.tl-row {
  display: grid;
  grid-template-columns: var(--who) 1fr;
}
.tl-row {
  min-height: 56px;
}
.late small {
  color: var(--color-ink-yellow);
}
.tl-hold {
  background: repeating-linear-gradient(135deg, #fff 0 6px, var(--color-hatch) 6px 12px);
}
</style>
