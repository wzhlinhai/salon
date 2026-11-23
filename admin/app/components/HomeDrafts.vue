<script setup lang="ts">
import type { Draft } from '~/composables/useDrafts'

// 首页组件卡里的草稿清单（照 Notion 首页 My tasks）：一行一条，点开到待确认页。最多列 5 条，其余给一个链接。
const props = defineProps<{ drafts: Draft[], status: string }>()
const shown = computed(() => props.drafts.slice(0, 5))
</script>

<template>
  <ul class="-mx-1">
    <li v-for="draft in shown" :key="draft.id">
      <NuxtLink to="/drafts" class="flex min-h-9 items-center gap-2.5 rounded-md px-1 hover:bg-hover">
        <UIcon name="i-lucide-pencil" class="size-4 shrink-0 text-dimmed" aria-hidden="true" />
        <span class="min-w-0 flex-1 truncate text-lead font-medium">{{ draft.summary }}</span>
        <span class="shrink-0 text-meta text-dimmed tabular-nums">{{ formatStamp(draft.created_at) }}</span>
        <span class="tag shrink-0 bg-tag-yellow">{{ status }}</span>
      </NuxtLink>
    </li>
  </ul>
  <p v-if="drafts.length > shown.length" class="mt-2 text-meta">
    <NuxtLink to="/drafts" class="text-muted hover:underline">还有 {{ drafts.length - shown.length }} 条，到待确认里看</NuxtLink>
  </p>
</template>
