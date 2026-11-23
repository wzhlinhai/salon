<script setup lang="ts">
// 员工首字头像（DESIGN.md 表格模式第一列）：一律灰底，不按人上色，名字本身就能区分人。
// size="sm" 用在表头这类窄行（24px）。
const props = defineProps<{ name: string, size?: 'sm' }>()
</script>

<template>
  <span class="grid shrink-0 place-items-center rounded-full bg-tag-gray" :class="props.size === 'sm' ? 'size-6 text-xs' : 'size-[30px] text-meta'" aria-hidden="true">{{ name.slice(0, 1) }}</span>
</template>
