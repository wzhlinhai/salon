<script setup lang="ts">
import type { components } from '~/utils/api'

// 开单的「项目」和「谁来做」（样稿 18）：点「选项目」弹出全部项目，上面可以打字搜，点一下挑上、再点一下去掉，弹层不关可以连着挑。
// 下面一项一行：项目名、按岗位选做的人、标价。收银开单和修改开单草稿共用。
type Item = components['schemas']['Item']
export interface OrderItemRow { item_id: string, staff: Record<number, string> }

const rows = defineModel<OrderItemRow[]>({ required: true })
const props = defineProps<{ items: Item[], staffItems: { label: string, value: string }[] }>()

const api = useApi()
const positionNames = ref(new Map<string, string>())
onMounted(async () => {
  const { data } = await api.POST('/api/ops/list_positions', { body: {} })
  if (data) positionNames.value = new Map(data.items.map(p => [p.id, p.name]))
})

const itemOf = (id: string) => props.items.find(i => i.id === id)
const picked = computed(() => rows.value.filter(r => r.item_id))

function isPicked(id: string) {
  return picked.value.some(r => r.item_id === id)
}

// 再点一下只去掉这个项目的最后一行：同一项目搜进来两次时，另一行和已选的人留着。
function toggle(id: string) {
  const last = picked.value.findLast(r => r.item_id === id)
  rows.value = last
    ? rows.value.filter(r => r !== last)
    : [...picked.value, { item_id: id, staff: {} }]
}

const pickerOpen = ref(false)
const query = ref('')
const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? props.items.filter(i => i.name.toLowerCase().includes(q)) : props.items
})
watch(pickerOpen, (value) => {
  if (value) query.value = ''
})

function positionsOf(itemID: string) {
  return [...(itemOf(itemID)?.positions ?? [])].sort((a, b) => a.slot - b.slot)
}

function positionLabel(position: components['schemas']['ItemPosition']) {
  return positionNames.value.get(position.position_id) ?? `岗位 ${position.slot}`
}

function remove(row: OrderItemRow) {
  rows.value = rows.value.filter(r => r !== row)
}
</script>

<template>
  <div class="space-y-4">
    <div>
      <p class="mb-2 text-meta text-muted">项目</p>
      <p v-if="!items.length" class="text-meta text-muted">还没有项目，先到「项目与价格」新建</p>
      <UPopover v-else v-model:open="pickerOpen" :content="{ align: 'start', sideOffset: 4 }">
        <button
          type="button"
          class="inline-flex h-8 items-center gap-2 rounded-full border border-default px-3 text-sm transition-colors hover:bg-hover focus-visible:outline-2 focus-visible:outline-primary"
          data-testid="item-picker-trigger"
        >
          <UIcon name="i-lucide-scissors" class="size-4 shrink-0 text-muted" aria-hidden="true" />
          <span>{{ picked.length ? `已选 ${picked.length} 项` : '选项目' }}</span>
          <UIcon name="i-lucide-chevron-down" class="size-4 shrink-0 text-muted" aria-hidden="true" />
        </button>
        <template #content>
          <div class="w-72 p-2 text-sm" data-testid="item-picker">
            <UInput v-model="query" icon="i-lucide-search" size="sm" placeholder="项目名" aria-label="搜项目" autofocus class="w-full" />
            <ul class="mt-2 max-h-72 overflow-y-auto" role="group" aria-label="挑项目">
              <li v-for="item in shown" :key="item.id">
                <button
                  type="button"
                  class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-start transition-colors focus-visible:outline-2 focus-visible:outline-primary"
                  :class="isPicked(item.id) ? 'bg-selected' : 'hover:bg-hover'"
                  :aria-pressed="isPicked(item.id)"
                  data-testid="item-option"
                  @click="toggle(item.id)"
                >
                  <UIcon name="i-lucide-check" class="size-4 shrink-0" :class="isPicked(item.id) ? '' : 'invisible'" aria-hidden="true" />
                  <span class="min-w-0 flex-1 truncate">{{ item.name }}</span>
                  <span class="shrink-0 text-muted tabular-nums">{{ yuan(item.price) }}</span>
                </button>
              </li>
            </ul>
            <p v-if="!shown.length" class="px-2 py-2 text-muted">没有找到</p>
          </div>
        </template>
      </UPopover>
    </div>

    <div v-if="picked.length">
      <p class="mb-1 text-meta text-muted">谁来做</p>
      <div class="divide-y divide-default border-b border-default">
        <template v-for="(row, index) in picked" :key="index">
          <div class="flex flex-wrap items-center gap-x-4 gap-y-2 py-2" data-testid="order-item-row" :data-item="itemOf(row.item_id)?.name">
            <span class="w-32 truncate font-medium">{{ itemOf(row.item_id)?.name }}</span>
            <div class="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-2">
              <span v-if="!positionsOf(row.item_id).length" class="text-meta text-dimmed">不用选人</span>
              <span v-for="position in positionsOf(row.item_id)" :key="position.slot" class="flex items-center gap-2">
                <span class="text-meta text-muted">{{ positionLabel(position) }}</span>
                <USelect
                  v-model="row.staff[position.slot]"
                  :items="staffItems"
                  size="sm"
                  variant="soft"
                  class="w-28"
                  :aria-label="`项目${index + 1}岗位${position.slot}员工`"
                  :placeholder="position.required ? '选人（必填）' : '选人'"
                />
              </span>
            </div>
            <span class="tabular-nums">{{ yuan(itemOf(row.item_id)?.price ?? 0) }}</span>
            <UButton size="sm" variant="ghost" color="neutral" icon="i-lucide-x" :aria-label="`去掉${itemOf(row.item_id)?.name ?? ''}`" @click="remove(row)" />
          </div>
        </template>
      </div>
    </div>
  </div>
</template>
