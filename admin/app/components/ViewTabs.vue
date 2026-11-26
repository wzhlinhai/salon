<script setup lang="ts" generic="K extends string">
// 列表上方的视图页签，照 Notion 数据库的视图切换：灰底胶囊表示当前视图，其余是灰字。
// count 不传就不显示数字；hint 是悬停提示。testid 给了就按 `${testid}-${key}` 标每个页签，数字标 `${testid}-count`。
defineProps<{ items: { key: K, label: string, count?: number | null, hint?: string }[], label: string, testid?: string }>()
const model = defineModel<K>({ required: true })
</script>

<template>
  <div class="flex flex-wrap items-center gap-0.5" role="group" :aria-label="label">
    <button
      v-for="v in items"
      :key="v.key"
      type="button"
      class="inline-flex h-8 items-center gap-2 rounded-full px-3 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-primary"
      :class="model === v.key ? 'bg-selected text-default' : 'text-muted hover:bg-hover hover:text-default'"
      :aria-pressed="model === v.key"
      :data-filter="v.key"
      :data-testid="testid ? `${testid}-${v.key}` : undefined"
      :title="v.hint || undefined"
      @click="model = v.key"
    >
      {{ v.label }}<span v-if="v.count !== undefined" class="tabular-nums text-muted" :data-testid="testid ? `${testid}-count` : undefined">{{ v.count ?? '' }}</span>
    </button>
  </div>
</template>
