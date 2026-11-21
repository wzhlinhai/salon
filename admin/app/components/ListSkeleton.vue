<script setup lang="ts">
// 列表第一次加载时的骨架（DESIGN.md 加载）：几行灰条占住表格的位置，数据到了原地换成真行，页面不跳。
// 放进 UTable 的 #loading 插槽，或直接放在自己画的列表里。有旧数据时刷新不用骨架，保留旧行。
withDefaults(defineProps<{ rows?: number }>(), { rows: 5 })
</script>

<template>
  <div class="divide-y divide-default" role="status" data-testid="list-skeleton">
    <div v-for="i in rows" :key="i" class="flex items-center gap-4 px-3 py-3" aria-hidden="true">
      <span class="h-3 w-1/5 animate-pulse rounded-tag bg-elevated motion-reduce:animate-none" />
      <span class="h-3 w-1/6 animate-pulse rounded-tag bg-elevated motion-reduce:animate-none" />
      <span class="h-3 w-1/4 animate-pulse rounded-tag bg-elevated motion-reduce:animate-none" />
      <span class="ms-auto h-3 w-12 animate-pulse rounded-tag bg-elevated motion-reduce:animate-none" />
    </div>
    <span class="sr-only">加载中</span>
  </div>
</template>
