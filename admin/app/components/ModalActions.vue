<script setup lang="ts">
// 弹窗底部（DESIGN.md 详情、弹窗和提示）：靠右，"取消"描边在左，主操作实心在右；左边可以放一句状态（"已选 3 个"）。
// before：紧挨在「取消」前面的一小段，例如开单的合计金额（样稿 18）。
// cancelLabel：危险确认里写成「不撤了」这类话（样稿 21）。
withDefaults(defineProps<{ cancelLabel?: string }>(), { cancelLabel: '取消' })
const emit = defineEmits<{ cancel: [] }>()
</script>

<template>
  <div class="flex w-full items-center justify-end gap-2">
    <div v-if="$slots.leading" class="me-auto min-w-0">
      <slot name="leading" />
    </div>
    <slot name="before" />
    <UButton color="neutral" variant="outline" @click="emit('cancel')">{{ cancelLabel }}</UButton>
    <slot />
  </div>
</template>
