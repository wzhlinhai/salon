<script setup lang="ts">
// 子页面链接（DESIGN.md 简报）：照 Notion 页内的子页面，一行一个、不包框——图标、带下划线的名称、一句灰字，右边一个关键数。
defineProps<{ to: string, label: string, icon: string, description?: string, figure?: string }>()
</script>

<template>
  <NuxtLink :to="to" class="-mx-2 flex items-baseline gap-2.5 rounded-md px-2 py-1.5 transition-colors hover:bg-hover">
    <UIcon :name="icon" class="size-4 shrink-0 self-center text-muted" aria-hidden="true" />
    <span class="shrink-0 border-b border-accented font-medium">{{ label }}</span>
    <span v-if="description" class="min-w-0 truncate text-meta text-dimmed">{{ description }}</span>
    <span v-if="figure" class="ml-auto shrink-0 pl-3 text-muted tabular-nums">{{ figure }}</span>
  </NuxtLink>
</template>
