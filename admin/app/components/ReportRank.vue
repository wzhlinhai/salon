<script setup lang="ts">
// 排名细条（DESIGN.md 简报）：头像、名字、一根按比例的细条、右边的数，代替排名表格。
const props = defineProps<{ rows: { key: string, name: string, value: number, display: string }[] }>()

const max = computed(() => Math.max(...props.rows.map(r => r.value), 0))
</script>

<template>
  <ol v-if="max > 0" class="flex flex-col" data-testid="report-rank">
    <li v-for="r in rows" :key="r.key" class="grid grid-cols-[24px_6rem_1fr_5.5rem] items-center gap-2.5 py-1">
      <PersonAvatar :name="r.name" size="sm" />
      <span class="truncate">{{ r.name }}</span>
      <i class="h-1.5 rounded-full bg-dye-1 opacity-70" :style="{ width: `${Math.max(r.value, 0) / max * 100}%` }" aria-hidden="true" />
      <span class="text-right text-muted tabular-nums">{{ r.display }}</span>
    </li>
  </ol>
</template>
