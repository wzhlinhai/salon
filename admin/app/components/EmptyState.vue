<script setup lang="ts">
// 列表为空或筛选无结果（DESIGN.md 详情、弹窗和提示）：一句话 + 能解决它的那个按钮，居中放在内容区。
defineProps<{ title: string, hint?: string, icon?: string }>()
</script>

<template>
  <div class="flex flex-col items-center gap-1.5 px-4 py-12 text-center" data-testid="empty-state">
    <UIcon v-if="icon" :name="icon" class="mb-1 size-6 text-dimmed" aria-hidden="true" />
    <p class="font-medium">{{ title }}</p>
    <p v-if="hint" class="text-meta text-muted">{{ hint }}</p>
    <div v-if="$slots.default" class="mt-2 flex gap-2">
      <slot />
    </div>
  </div>
</template>
