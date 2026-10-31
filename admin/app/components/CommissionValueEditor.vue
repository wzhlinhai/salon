<script setup lang="ts">
import type { components } from '~/utils/api'

export interface CommissionValue {
  kind: 'none' | 'percent' | 'fixed_amount' | 'tiers'
  amount: string
  mode: 'whole' | 'progressive'
  levels: components['schemas']['TierLevel'][]
}

const value = defineModel<CommissionValue>({ required: true })
const props = defineProps<{ tiers?: boolean }>()
const kinds = computed(() => [
  ...(props.tiers ? [{ label: '不计提成', value: 'none' }] : []),
  { label: '比例', value: 'percent' },
  { label: '固定金额', value: 'fixed_amount' },
  ...(props.tiers ? [{ label: '阶梯', value: 'tiers' }] : [])
])
</script>

<template>
  <div class="space-y-2">
    <USelect v-model="value.kind" aria-label="数值类型" :items="kinds" />
    <UInput v-if="value.kind === 'percent' || value.kind === 'fixed_amount'" v-model="value.amount" :aria-label="value.kind === 'percent' ? '比例' : '固定金额'" inputmode="decimal" />
    <template v-if="value.kind === 'tiers'">
      <USelect v-model="value.mode" aria-label="阶梯方式" :items="[{ label: '整笔跳档', value: 'whole' }, { label: '超额累进', value: 'progressive' }]" />
      <div v-for="(level, index) in value.levels" :key="index" class="flex flex-wrap gap-2" data-testid="tier-level">
        <UFormField label="起点"><UInput v-model="level.from" :disabled="index === 0" aria-label="起点" inputmode="decimal" /></UFormField>
        <UFormField label="比例"><UInput v-model="level.percent" aria-label="档比例" inputmode="decimal" /></UFormField>
        <UButton v-if="index > 0" variant="ghost" @click="value.levels.splice(index, 1)">删除</UButton>
      </div>
      <UButton variant="outline" @click="value.levels.push({ from: '', percent: '' })">加一档</UButton>
    </template>
  </div>
</template>
