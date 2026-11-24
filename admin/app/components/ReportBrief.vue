<script setup lang="ts">
// 简报的一段话（DESIGN.md 简报、报表页）：报表首页和每张报表顶上的结论，句子由 utils/report.ts 的 briefWriter 写。
import type { BriefPart } from '~/utils/report'

defineProps<{ parts: BriefPart[] }>()
</script>

<template>
  <p v-if="parts.length" class="text-base leading-[1.9] text-pretty" data-testid="report-brief">
    <template v-for="(part, i) in parts" :key="i">
      <b v-if="part.strong" class="font-semibold tabular-nums" :class="{ 'text-ink-green': part.tone === 'up', 'text-ink-red': part.tone === 'down' }" :data-figure="part.figure">{{ part.text }}</b>
      <template v-else>{{ part.text }}</template>
    </template>
  </p>
</template>
