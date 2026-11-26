<script setup lang="ts">
// 子页面链接（DESIGN.md 简报）：照 Notion 页内的子页面，一行一个、不包框——带下划线的名称（不放图标，店主 2026-11-26 定）、一句灰字，右边一个关键数。
defineProps<{ to: string, label: string, description?: string, figure?: string }>()
</script>

<template>
  <NuxtLink :to="to" class="flex items-baseline gap-2 rounded-md py-2 transition-colors hover:bg-hover sm:-mx-2 sm:px-2">
    <span class="shrink-0 border-b border-accented font-medium">{{ label }}</span>
    <span v-if="description" class="min-w-0 truncate text-meta text-muted">{{ description }}</span>
    <span v-if="figure" class="ml-auto shrink-0 pl-3 text-muted tabular-nums">{{ figure }}</span>
  </NuxtLink>
</template>
