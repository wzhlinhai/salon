<script setup lang="ts">
import type { components } from '~/utils/api'

// 开单的「项目」和「谁来做」（样稿 18）：上面一排胶囊挑项目，再点一下去掉；不在这排的用「搜全部项目」找。
// 下面一项一行：项目名、按岗位选做的人、标价。收银开单和修改开单草稿共用。
type Item = components['schemas']['Item']
export interface OrderItemRow { item_id: string, staff: Record<number, string> }

const rows = defineModel<OrderItemRow[]>({ required: true })
const props = defineProps<{ items: Item[], staffItems: { label: string, value: string }[] }>()

// 这排胶囊最多放几个：已经挑了的在前，其余按项目目录的顺序补满。
const CHIP_COUNT = 8

const api = useApi()
const positionNames = ref(new Map<string, string>())
onMounted(async () => {
  const { data } = await api.POST('/api/ops/list_positions', { body: {} })
  if (data) positionNames.value = new Map(data.items.map(p => [p.id, p.name]))
})

const itemOf = (id: string) => props.items.find(i => i.id === id)
const picked = computed(() => rows.value.filter(r => r.item_id))

const chips = computed(() => {
  const chosen = [...new Set(picked.value.map(r => r.item_id))].map(itemOf).filter((i): i is Item => !!i)
  const rest = props.items.filter(i => !chosen.includes(i))
  return [...chosen, ...rest].slice(0, Math.max(CHIP_COUNT, chosen.length))
})

function isPicked(id: string) {
  return picked.value.some(r => r.item_id === id)
}

function toggle(id: string) {
  rows.value = isPicked(id)
    ? rows.value.filter(r => r.item_id !== id)
    : [...rows.value.filter(r => r.item_id), { item_id: id, staff: {} }]
}

const searchOptions = computed(() => props.items.map(i => ({ label: `${i.name}（${i.price}）`, value: i.id })))
const searchPick = ref<string | undefined>()
watch(searchPick, (id) => {
  if (!id) return
  rows.value = [...rows.value.filter(r => r.item_id), { item_id: id, staff: {} }]
  searchPick.value = undefined
})

function positionsOf(itemID: string) {
  return [...(itemOf(itemID)?.positions ?? [])].sort((a, b) => a.slot - b.slot)
}

function positionLabel(position: components['schemas']['ItemPosition']) {
  return positionNames.value.get(position.position_id) ?? `岗位 ${position.slot}`
}

function remove(index: number) {
  rows.value = rows.value.filter((_, i) => i !== index)
}
</script>

<template>
  <div class="space-y-4">
    <div>
      <p class="mb-2 text-meta text-muted">项目</p>
      <div class="flex flex-wrap items-center gap-2" role="group" aria-label="挑项目">
        <button
          v-for="item in chips"
          :key="item.id"
          type="button"
          :aria-pressed="isPicked(item.id)"
          class="h-7 rounded-full border px-3 text-sm transition-colors"
          :class="isPicked(item.id) ? 'border-transparent bg-inverted text-inverted' : 'border-default hover:bg-hover'"
          data-testid="item-chip"
          @click="toggle(item.id)"
        >
          {{ item.name }}
        </button>
        <USelectMenu
          v-model="searchPick"
          :items="searchOptions"
          value-key="value"
          size="sm"
          icon="i-lucide-search"
          placeholder="搜全部项目"
          aria-label="搜全部项目"
          :search-input="{ placeholder: '项目名' }"
          class="w-36"
          :ui="{ base: 'rounded-full' }"
        />
      </div>
    </div>

    <div v-if="picked.length">
      <p class="mb-1 text-meta text-muted">谁来做</p>
      <div class="divide-y divide-default border-b border-default">
        <template v-for="(row, index) in rows" :key="index">
          <div v-if="row.item_id" class="flex flex-wrap items-center gap-x-4 gap-y-2 py-2" data-testid="order-item-row" :data-item="itemOf(row.item_id)?.name">
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
                  :placeholder="position.required ? '选人（必填）' : '＋ 选人'"
                />
              </span>
            </div>
            <span class="tabular-nums">{{ yuan(itemOf(row.item_id)?.price ?? 0) }}</span>
            <UButton size="sm" variant="ghost" color="neutral" icon="i-lucide-x" :aria-label="`去掉${itemOf(row.item_id)?.name ?? ''}`" @click="remove(index)" />
          </div>
        </template>
      </div>
    </div>
  </div>
</template>
