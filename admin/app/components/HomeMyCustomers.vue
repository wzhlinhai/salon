<script setup lang="ts">
import type { components } from '~/utils/api'

// 个人首页的"我的顾客"（照原型 K）：我今天正在服务和接下来的预约、私约占位，每位一张卡。
type Appointment = components['schemas']['Appointment']

const props = defineProps<{ date: string, staffId: string }>()

const api = useApi()
const { can } = useMe()
const entries = ref<Appointment[]>([])
const guard = useRequestGuard()
const { load: loadTones, toneOf } = useItemTones()
const now = ref(Date.now())

async function load() {
  if (!props.date) return
  const current = guard.next()
  const { data } = await api.POST('/api/ops/list_appointments', { body: { from: props.date, to: props.date, staff_id: props.staffId } })
  if (!current()) return
  entries.value = data?.entries ?? []
}

watch(() => [props.date, props.staffId], load, { immediate: true })
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

// 还没完成的：正在服务的排前面，其余按开始时间，最多三张。
const upcoming = computed(() => entries.value
  .filter(entry => entry.staff.id === props.staffId && (entry.status === 'pending' || entry.status === 'confirmed'))
  .filter(entry => entry.order_id || new Date(entry.ends_at).getTime() > now.value)
  .sort((a, b) => Number(!!b.order_id) - Number(!!a.order_id) || a.starts_at.localeCompare(b.starts_at))
  .slice(0, 3))

function clock(value: string) {
  return new Date(value).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })
}

function lateMinutes(entry: Appointment) {
  if (entry.order_id || entry.kind === 'block') return 0
  return Math.floor((now.value - new Date(entry.starts_at).getTime()) / 60_000)
}
</script>

<template>
  <div v-if="upcoming.length" data-testid="home-my-customers">
    <div class="sec">我的顾客<span class="ms-auto font-normal text-dimmed">正在服务和接下来</span></div>
    <div class="grid gap-3 md:grid-cols-3">
      <div
        v-for="entry in upcoming"
        :key="entry.id"
        class="box flex flex-col gap-1.5"
        :class="entry.kind === 'block' ? 'hatch' : ''"
      >
        <div class="flex items-center gap-1.5">
          <b class="truncate text-lead font-semibold">{{ entry.kind === 'block' ? (entry.note || '私约占位') : (entry.customer?.name || '未留名顾客') }}</b>
          <span v-if="entry.order_id" class="tag bg-tag-blue">服务中</span>
          <span v-else-if="lateMinutes(entry) > 5" class="tag bg-tag-red">迟到 {{ lateMinutes(entry) }} 分</span>
          <span class="ms-auto shrink-0 text-meta text-dimmed tabular-nums">{{ clock(entry.starts_at) }} – {{ clock(entry.ends_at) }}</span>
        </div>
        <div class="flex flex-wrap gap-1">
          <span v-for="item in entry.items" :key="item.item_id" class="tag" :class="toneOf(item.item_id)">{{ item.name }}</span>
          <span class="tag bg-tag-gray">{{ entry.kind === 'block' ? '占位' : '预约' }}</span>
          <span v-if="entry.status === 'pending'" class="tag bg-tag-yellow">待确认</span>
        </div>
        <p v-if="entry.kind === 'block'" class="text-meta text-muted">这段时间留给私约，顾客到店后开单。</p>
        <p v-else-if="entry.note" class="text-meta text-muted">{{ entry.note }}</p>
        <div v-if="entry.customer && can('customer.view')" class="mt-auto flex gap-1.5 pt-1">
          <UButton color="neutral" variant="outline" class="min-h-8 px-3" :to="{ path: '/customers', query: { id: entry.customer.id } }">完整档案</UButton>
        </div>
      </div>
    </div>
  </div>
</template>
