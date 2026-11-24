<script setup lang="ts">
import type { components } from '~/utils/api'

// 前台首页的"今日顾客"看板（照原型 J 的助理视图）：今天的预约按稍后到店、已到店、已完成分三列；
// 轮牌派的散客（挂单）也算已到店。
type Appointment = components['schemas']['Appointment']
type Walkin = components['schemas']['WalkinOccupancyView']

const props = defineProps<{ date: string }>()
// 加载后报给首页：今天的预约共几位、几位还没到店（和 HomeTimeline 同一口径）。
const emit = defineEmits<{ summary: [value: { total: number, waiting: number }] }>()

const api = useApi()
const entries = ref<Appointment[]>([])
const walkIns = ref<Walkin[]>([])
const loaded = ref(false)
const guard = useRequestGuard()
const { load: loadTones, toneOf } = useItemTones()
const now = ref(Date.now())

async function load() {
  if (!props.date) return
  const current = guard.next()
  const { data } = await api.POST('/api/ops/list_appointments', { body: { from: props.date, to: props.date } })
  if (!current()) return
  entries.value = (data?.entries ?? []).filter(entry => entry.kind === 'appointment' && entry.status !== 'cancelled')
  walkIns.value = data?.walk_ins ?? []
  loaded.value = true
  const visits = entries.value.filter(entry => entry.status !== 'no_show')
  emit('summary', {
    total: visits.length,
    waiting: visits.filter(entry => !entry.order_id && entry.status !== 'completed').length
  })
}

watch(() => props.date, load, { immediate: true })
void loadTones()

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
  return new Date(value).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })
}

function lateMinutes(entry: Appointment) {
  if (entry.order_id || (entry.status !== 'confirmed' && entry.status !== 'pending')) return 0
  return Math.floor((now.value - new Date(entry.starts_at).getTime()) / 60_000)
}

const columns = computed(() => {
  const sorted = [...entries.value].sort((a, b) => a.starts_at.localeCompare(b.starts_at))
  return [
    { label: '稍后到店', tone: 'bg-tag-gray', rows: sorted.filter(entry => !entry.order_id && (entry.status === 'pending' || entry.status === 'confirmed')) },
    { label: '已到店', tone: 'bg-tag-blue', rows: sorted.filter(entry => entry.order_id && entry.status !== 'completed'), walkIns: [...walkIns.value].sort((a, b) => a.starts_at.localeCompare(b.starts_at)) },
    { label: '已完成', tone: 'bg-tag-green', rows: sorted.filter(entry => entry.status === 'completed' || entry.status === 'no_show') }
  ]
})
</script>

<template>
  <div data-testid="home-board" class="grid items-start gap-3 tabular-nums md:grid-cols-3">
    <div v-for="column in columns" :key="column.label" class="rounded-card bg-lane p-2">
      <div class="flex items-center gap-2 px-1 pt-1 pb-2">
        <span class="tag" :class="column.tone">{{ column.label }}</span>
        <span class="text-meta text-dimmed">{{ column.rows.length + (column.walkIns?.length ?? 0) }}</span>
      </div>
      <NuxtLink
        v-for="entry in column.rows"
        :key="entry.id"
        to="/bookings"
        class="mb-2 block rounded-card bg-white px-3 py-2.5 shadow-card hover:bg-card-hover"
      >
        <b class="font-semibold">{{ entry.customer?.name || '未留名顾客' }}</b>
        <div class="mt-1.5 mb-1 flex flex-wrap gap-1">
          <span v-for="item in entry.items" :key="item.item_id" class="tag" :class="toneOf(item.item_id)">{{ item.name }}</span>
          <span class="tag bg-tag-gray">{{ entry.staff.name }}</span>
          <span v-if="entry.status === 'no_show'" class="tag bg-tag-red">爽约</span>
          <span v-else-if="lateMinutes(entry) > 5" class="tag bg-tag-red">迟到 {{ lateMinutes(entry) }} 分</span>
          <span v-else-if="entry.status === 'pending'" class="tag bg-tag-yellow">待确认</span>
        </div>
        <small class="text-xs text-dimmed">{{ clock(entry.starts_at) }} – {{ clock(entry.ends_at) }}</small>
      </NuxtLink>
      <div
        v-for="walk in column.walkIns ?? []"
        :key="walk.order_id"
        class="mb-2 rounded-card bg-white px-3 py-2.5 shadow-card"
        data-testid="board-walkin"
      >
        <b class="font-semibold">散客</b>
        <div class="mt-1.5 mb-1 flex flex-wrap gap-1">
          <span class="tag bg-tag-gray">{{ walk.staff.name }}</span>
          <span class="tag bg-tag-blue">服务中</span>
        </div>
        <small class="text-xs text-dimmed">{{ clock(walk.starts_at) }} 开单</small>
      </div>
      <p v-if="loaded && !column.rows.length && !column.walkIns?.length" class="px-2 py-1.5 text-meta text-dimmed">没有</p>
    </div>
  </div>
</template>
