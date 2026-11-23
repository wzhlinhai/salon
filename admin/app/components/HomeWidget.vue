<script setup lang="ts">
// 首页的组件卡（照 Notion 首页）：白卡，卡内顶上一行灰色小图标加灰字标题，右边可放补充说明或链接。
defineProps<{ title: string, icon: string }>()
</script>

<template>
  <section class="min-w-0 rounded-card bg-white shadow-card">
    <header class="flex min-h-11 items-center gap-2 px-4 pt-1 text-sm font-medium text-muted">
      <UIcon :name="icon" class="size-4 shrink-0" aria-hidden="true" />
      <h2>{{ title }}</h2>
      <div v-if="$slots.aside" class="ms-auto text-meta font-normal text-dimmed">
        <slot name="aside" />
      </div>
    </header>
    <div class="px-4 pb-4">
      <slot />
    </div>
  </section>
</template>
