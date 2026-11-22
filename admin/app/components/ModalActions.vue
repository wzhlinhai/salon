<script setup lang="ts">
// 弹窗底部（DESIGN.md 详情、弹窗和提示）：靠右，"取消"描边在左，主操作实心在右；左边可以放一句状态（"已选 3 个"）。
const emit = defineEmits<{ cancel: [] }>()
</script>

<template>
  <div class="flex w-full items-center justify-end gap-2">
    <div v-if="$slots.leading" class="me-auto min-w-0">
      <slot name="leading" />
    </div>
    <UButton color="neutral" variant="outline" @click="emit('cancel')">取消</UButton>
    <slot />
  </div>
</template>
