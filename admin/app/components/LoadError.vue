<script setup lang="ts">
// 读取出错（样稿 25）：原地一块浅红底，「!」加一句哪里没读出来、一句怎么办，右边「重试」。
// 不放插画，也不只弹一条提示就留一块空白。
defineProps<{ title: string, hint?: string }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="flex items-center gap-3 rounded-card bg-tag-red p-4" role="alert" data-testid="load-error">
    <span class="grid size-6 shrink-0 place-items-center rounded-full bg-error text-xs font-semibold text-white" aria-hidden="true">!</span>
    <div class="min-w-0 flex-1">
      <p class="font-medium">{{ title }}</p>
      <p v-if="hint" class="text-meta text-toned">{{ hint }}</p>
    </div>
    <UButton color="neutral" variant="outline" size="sm" class="bg-default" @click="$emit('retry')">重试</UButton>
  </div>
</template>
