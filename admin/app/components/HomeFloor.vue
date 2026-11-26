<script setup lang="ts">
import type { components } from '~/utils/api'

// 首页的「在店里」（样稿 01）：轮牌队列里今天来了的人，按轮牌顺序一人一行，右边写他现在在干什么。
// 排在最前面的等着的人写「轮到」并加粗。传 staffId 时取这个人所在的队列，并标出「我」。
// 没有启用的队列（或这个人不在任何队列里）就整块不显示。
type Rotation = components['schemas']['Rotation']
type Turn = components['schemas']['Turn']

const props = defineProps<{ staffId?: string, serving?: Record<string, string> }>()

const api = useApi()
const rotation = ref<Rotation | null>(null)
const guard = useRequestGuard()

async function load() {
  const current = guard.next()
  const queues = await api.POST('/api/ops/list_rotation_queues', { body: {} })
  if (!current()) return
  // 一次失败留着旧的，不让整块闪没。
  if (!queues.data) return
  const queue = queues.data.entries.find(row => row.active && (!props.staffId || row.members.some(member => member.staff.id === props.staffId)))
  if (!queue) {
    rotation.value = null
    return
  }
  const { data } = await api.POST('/api/ops/get_rotation', { body: { queue_id: queue.id } })
  if (!current() || !data) return
  rotation.value = data
}

watch(() => props.staffId, load, { immediate: true })

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') void load()
  }, 60_000)
})
onBeforeUnmount(() => clearInterval(timer))

// 等着的按轮牌顺序在前，其后是在做的、休息的、盖牌的；没到岗的不列。
const order: Record<Turn['status'], number> = { waiting: 0, serving: 1, resting: 2, covered: 3, off: 4 }
const turns = computed(() => [...(rotation.value?.entries ?? [])]
  .filter(turn => turn.status !== 'off')
  .sort((a, b) => order[a.status] - order[b.status] || a.position - b.position))
const next = computed(() => turns.value.find(turn => turn.status === 'waiting')?.staff.id ?? null)

function doing(turn: Turn) {
  if (turn.status === 'waiting') {
    if (turn.staff.id === next.value) return turn.staff.id === props.staffId ? '轮到你' : '轮到'
    if (turn.staff.id !== props.staffId) return '等着'
    return `第 ${turns.value.filter(row => row.status === 'waiting').findIndex(row => row.staff.id === props.staffId) + 1} 位`
  }
  if (turn.status === 'serving') {
    const name = turn.serving_order_id ? props.serving?.[turn.serving_order_id] : undefined
    return name ? `做${name}` : '在做'
  }
  if (turn.status === 'resting') return turn.rest_until ? `休息到 ${clockTime(turn.rest_until)}` : '休息'
  return '盖牌'
}
</script>

<template>
  <section v-if="rotation && turns.length" data-testid="home-floor" class="min-w-0">
    <header class="mb-2 flex items-baseline gap-2">
      <h2 class="text-base font-semibold">在店里</h2>
      <NuxtLink :to="`/rotation?queue=${rotation.queue.id}`" class="tap-area inline-flex items-center text-meta text-muted hover:text-default">轮牌顺序</NuxtLink>
    </header>
    <ul class="divide-y divide-default">
      <li
        v-for="turn in turns"
        :key="turn.staff.id"
        data-testid="floor-row"
        class="flex min-h-11 items-center gap-2 text-sm"
      >
        <span class="grid size-6 shrink-0 place-items-center rounded-full bg-tag-gray text-xs" aria-hidden="true">{{ turn.staff.name.slice(0, 1) }}</span>
        <span class="min-w-0 truncate" :class="turn.staff.id === props.staffId && 'font-semibold'">{{ turn.staff.name }}<template v-if="turn.staff.id === props.staffId">（我）</template></span>
        <span
          class="ms-auto shrink-0 tabular-nums"
          :class="turn.staff.id === next ? 'font-semibold' : 'text-muted'"
        >{{ doing(turn) }}</span>
      </li>
    </ul>
  </section>
</template>
