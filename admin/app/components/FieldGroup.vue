<script setup lang="ts">
// 表单里一组多行的值（如级别价、开单的项目）：和 UFormField 一样标签在上，下面逐行排值，最后一个「＋ 添加」文字按钮。
// 单个值用 UFormField；这里放不止一个控件，所以不做 label for，改用 role=group 命名。
defineProps<{ label: string }>()
</script>

<template>
  <div role="group" :aria-label="label">
    <div class="text-sm font-semibold text-highlighted">{{ label }}</div>
    <div class="mt-2 flex min-w-0 flex-col gap-2">
      <slot />
    </div>
  </div>
</template>
