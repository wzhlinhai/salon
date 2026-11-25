<script setup lang="ts">
// 首页的一块（样稿 01）：不套卡片，靠标题和留白分开；标题右边可放一句灰字补充。
defineProps<{ title: string }>()
</script>

<template>
  <section class="min-w-0">
    <header class="mb-2 flex items-baseline gap-2">
      <h2 class="text-base font-semibold">{{ title }}</h2>
      <span v-if="$slots.aside" class="text-meta text-dimmed">
        <slot name="aside" />
      </span>
    </header>
    <slot />
  </section>
</template>
