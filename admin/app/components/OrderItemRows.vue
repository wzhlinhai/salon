<script setup lang="ts">
import type { components } from '~/utils/api'

// 开单的「项目」一栏：每行一个项目，下面按岗位选做的人。收银开单和修改开单草稿共用。
type Item = components['schemas']['Item']
export interface OrderItemRow { item_id: string, staff: Record<number, string> }

const rows = defineModel<OrderItemRow[]>({ required: true })
const props = defineProps<{ items: Item[], staffItems: { label: string, value: string }[] }>()
const itemOptions = computed(() => props.items.map(i => ({ label: `${i.name}（${i.price}）`, value: i.id })))

function positionsOf(itemID: string) {
  const item = props.items.find(i => i.id === itemID)
  return [...(item?.positions ?? [])].sort((a, b) => a.slot - b.slot)
}
</script>

<template>
  <FieldGroup label="项目">
    <div v-for="(row, index) in rows" :key="index" class="space-y-2" data-testid="order-item-row">
      <div class="flex items-center gap-2">
        <USelect v-model="row.item_id" :items="itemOptions" class="min-w-0 flex-1" :aria-label="`项目${index + 1}`" placeholder="选择项目" @update:model-value="row.staff = {}" />
        <UButton variant="ghost" color="error" icon="i-lucide-x" aria-label="去掉" :disabled="rows.length === 1" @click="rows.splice(index, 1)" />
      </div>
      <div v-for="position in positionsOf(row.item_id)" :key="position.slot" class="flex items-center gap-2 ps-3">
        <span class="w-14 shrink-0 text-meta text-muted">岗位 {{ position.slot }}</span>
        <USelect v-model="row.staff[position.slot]" :items="staffItems" class="min-w-0 flex-1" :aria-label="`项目${index + 1}岗位${position.slot}员工`" placeholder="选择员工" />
        <span v-if="position.required" class="tag shrink-0 bg-tag-yellow">必填</span>
      </div>
    </div>
    <UButton color="neutral" icon="i-lucide-plus" class="-ms-2.5 self-start" variant="ghost" @click="rows.push({ item_id: '', staff: {} })">添加项目</UButton>
  </FieldGroup>
</template>
