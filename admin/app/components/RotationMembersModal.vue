<script setup lang="ts">
import type { components } from '~/utils/api'

// 轮牌队列的成员和默认顺序：左边勾选在职员工，右边是排好的顺序，可以上移下移。
// 默认顺序用于「接昨天顺序」的新成员和「每晚重置」；当天已经上牌的顺序不受影响。
type Queue = components['schemas']['Queue']
type StaffOption = components['schemas']['StaffOption']

const emit = defineEmits<{ saved: [queue: Queue] }>()

const api = useApi()
const toast = useToast()
const form = useModalForm()
const queue = ref<Queue | null>(null)
const options = ref<StaffOption[]>([])
const order = ref<{ id: string, name: string }[]>([])
// 已停用的员工还留在成员里；后端只收在职员工，所以打开时就从顺序里去掉，保存后移出队列。
const dropped = ref<string[]>([])

function start(target: Queue) {
  form.begin(() => {
    queue.value = target
    order.value = [...target.members].sort((a, b) => a.sort_order - b.sort_order).map(m => ({ id: m.staff.id, name: m.staff.name }))
    options.value = []
    dropped.value = []
  })
  form.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/list_staff_options', { body: {} })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    options.value = data.staff.filter(s => s.active)
    const active = new Set(options.value.map(s => s.id))
    dropped.value = order.value.filter(m => !active.has(m.id)).map(m => m.name)
    order.value = order.value.filter(m => active.has(m.id))
  })
}

defineExpose({ start })

const chosen = computed(() => new Set(order.value.map(m => m.id)))

function toggle(s: StaffOption, on: boolean | 'indeterminate') {
  if (on === true && !chosen.value.has(s.id)) order.value = [...order.value, { id: s.id, name: s.name }]
  if (on !== true) order.value = order.value.filter(m => m.id !== s.id)
}

function move(index: number, step: number) {
  const next = [...order.value]
  const [row] = next.splice(index, 1)
  next.splice(index + step, 0, row!)
  order.value = next
}

function save() {
  const target = queue.value
  if (!target) return
  form.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/set_queue_members', { body: { queue_id: target.id, staff_ids: order.value.map(m => m.id) } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已保存成员和顺序', color: 'success' })
    emit('saved', data)
    form.open.value = false
  })
}
</script>

<template>
  <UModal v-model:open="form.open.value" :title="`${queue?.name ?? ''}的成员和顺序`" description="勾选参加这个队列的员工，右边的顺序是每天预排时用的默认顺序。" :ui="{ content: 'sm:max-w-2xl' }">
    <template #body>
      <ListSkeleton v-if="form.loading.value" :rows="4" />
      <template v-else>
        <p v-if="dropped.length" class="mb-4 text-meta text-muted">{{ dropped.join('、') }} 已离职，不再列出，保存后移出这个队列。</p>
        <div class="grid gap-6 sm:grid-cols-2">
          <fieldset>
            <legend class="mb-2 text-meta text-muted">在职员工</legend>
            <div class="flex flex-col gap-2" data-testid="member-options">
              <UCheckbox
                v-for="s in options"
                :key="s.id"
                :model-value="chosen.has(s.id)"
                :label="s.name"
                @update:model-value="v => toggle(s, v)"
              />
            </div>
          </fieldset>
          <div>
            <p class="mb-2 text-meta text-muted">默认顺序</p>
            <ol v-if="order.length" class="divide-y divide-default border-y border-default" data-testid="member-order">
              <li v-for="(m, i) in order" :key="m.id" class="flex items-center gap-2 py-1.5">
                <span class="w-5 text-end text-meta text-dimmed tabular-nums">{{ i + 1 }}</span>
                <PersonAvatar :name="m.name" />
                <span class="min-w-0 flex-1 truncate">{{ m.name }}</span>
                <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-arrow-up" :aria-label="`${m.name} 上移`" :disabled="i === 0" @click="move(i, -1)" />
                <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-arrow-down" :aria-label="`${m.name} 下移`" :disabled="i === order.length - 1" @click="move(i, 1)" />
              </li>
            </ol>
            <p v-else class="text-meta text-dimmed">还没有成员，从左边勾选。</p>
          </div>
        </div>
      </template>
    </template>
    <template #footer>
      <ModalActions @cancel="form.open.value = false">
        <UButton :loading="form.saving.value" :disabled="form.loading.value" @click="save">保存</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
