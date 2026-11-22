<script setup lang="ts">
// 员工首字头像（DESIGN.md 表格模式第一列）：按员工 ID 固定取一种标签底色，同一个人在各页颜色一致。
// size="sm" 用在表头这类窄行（24px）。
const props = defineProps<{ id: string, name: string, size?: 'sm' }>()

const TONES = ['bg-tag-orange', 'bg-tag-blue', 'bg-tag-pink', 'bg-tag-green', 'bg-tag-purple', 'bg-tag-brown', 'bg-tag-yellow']

const tone = computed(() => {
  let sum = 0
  for (const ch of props.id) sum += ch.charCodeAt(0)
  return TONES[sum % TONES.length]
})
</script>

<template>
  <span class="grid shrink-0 place-items-center rounded-full" :class="[tone, size === 'sm' ? 'size-6 text-xs' : 'size-[30px] text-meta']" aria-hidden="true">{{ name.slice(0, 1) }}</span>
</template>
