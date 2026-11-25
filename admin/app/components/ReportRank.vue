<script setup lang="ts">
// 排名细条（DESIGN.md 简报、报表页）：名字、一根按比例的细条、右边的数，代替排名表格和柱状图。
// 排的是人（员工业绩）时名字前放头像；排的是别的（新客来源）时不放。
const props = withDefaults(defineProps<{ rows: { key: string, name: string, value: number, display: string }[], avatar?: boolean }>(), { avatar: true })

const max = computed(() => Math.max(...props.rows.map(r => r.value), 0))
</script>

<template>
  <ol v-if="max > 0" class="flex flex-col" data-testid="report-rank">
    <li v-for="r in rows" :key="r.key" class="grid items-center gap-2 py-1" :class="avatar ? 'grid-cols-[24px_6rem_1fr_5.5rem]' : 'grid-cols-[7rem_1fr_5.5rem]'" :data-rank="r.name">
      <PersonAvatar v-if="avatar" :name="r.name" size="sm" />
      <span class="truncate">{{ r.name }}</span>
      <i class="h-1.5 rounded-full bg-dye-1 opacity-70" :style="{ width: `${Math.max(r.value, 0) / max * 100}%` }" aria-hidden="true" />
      <span class="text-right text-muted tabular-nums">{{ r.display }}</span>
    </li>
  </ol>
</template>
