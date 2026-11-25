<script setup lang="ts">
import type { components } from '~/utils/api'

// 个人首页的"轮牌"（照原型 K）：我在哪个队列排第几、前面还有几人，下面一排是队列里的人。
// 不在任何启用的队列里就什么都不显示。
type Rotation = components['schemas']['Rotation']
type Turn = components['schemas']['Turn']

const props = defineProps<{ staffId: string }>()

const api = useApi()
const rotation = ref<Rotation | null>(null)
const guard = useRequestGuard()

async function load() {
  const current = guard.next()
  const queues = await api.POST('/api/ops/list_rotation_queues', { body: {} })
  if (!current()) return
  const queue = queues.data?.entries.find(row => row.active && row.members.some(member => member.staff.id === props.staffId))
  if (!queue) {
    rotation.value = null
    return
  }
  const { data } = await api.POST('/api/ops/get_rotation', { body: { queue_id: queue.id } })
  if (!current()) return
  rotation.value = data ?? null
}

watch(() => props.staffId, load, { immediate: true })

const statusLabels: Record<Turn['status'], string> = {
  off: '未到岗',
  waiting: '等待中',
  serving: '服务中',
  resting: '休息中',
  covered: '盖牌中'
}

const turns = computed(() => [...(rotation.value?.entries ?? [])].sort((a, b) => a.position - b.position))
const waiting = computed(() => turns.value.filter(turn => turn.status === 'waiting'))
const mine = computed(() => turns.value.find(turn => turn.staff.id === props.staffId) ?? null)
const myPlace = computed(() => {
  const index = waiting.value.findIndex(turn => turn.staff.id === props.staffId)
  return index < 0 ? null : index + 1
})
</script>

<template>
  <HomeWidget v-if="rotation" title="轮牌" icon="i-lucide-list-ordered" data-testid="home-rotation">
    <template #aside><NuxtLink :to="`/rotation?queue=${rotation.queue.id}`" class="inline-flex items-center gap-0.5 hover:text-default">{{ rotation.queue.name }}<UIcon name="i-lucide-chevron-right" class="size-3.5" aria-hidden="true" /></NuxtLink></template>
    <div>
      <div class="flex items-center gap-4">
        <div v-if="myPlace" class="font-serif text-title leading-none font-semibold tabular-nums">
          第 {{ myPlace }} <small class="text-sm font-medium text-dimmed">位</small>
        </div>
        <div>
          <b>{{ mine ? statusLabels[mine.status] : '不在今天的名单里' }}</b>
          <p v-if="myPlace" class="mt-1 text-meta text-muted">
            {{ myPlace === 1 ? '下一位就轮到你' : `前面还有 ${myPlace - 1} 人` }}
          </p>
        </div>
      </div>
      <div class="mt-3 flex flex-wrap gap-2">
        <span
          v-for="turn in turns.filter(row => row.status !== 'off')"
          :key="turn.staff.id"
          class="flex items-center gap-2 rounded-full py-1 pr-2 pl-1 text-meta"
          :class="turn.staff.id === props.staffId ? 'bg-elevated font-semibold ring-serving' : 'bg-elevated'"
        >
          <i class="grid size-5 place-items-center rounded-full bg-white text-xs not-italic">{{ turn.staff.name.slice(0, 1) }}</i>
          {{ turn.staff.id === props.staffId ? '我' : turn.staff.name }}<template v-if="turn.status !== 'waiting'"> · {{ statusLabels[turn.status] }}</template>
        </span>
      </div>
    </div>
  </HomeWidget>
</template>
