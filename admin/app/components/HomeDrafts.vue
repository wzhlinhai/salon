<script setup lang="ts">
import type { Draft } from '~/composables/useDrafts'

// 首页的 AI 草稿（样稿 01）：草稿是 AI 写下的「东西」，所以一条一张浅灰小卡，点开到待确认页。
// 第二行写谁吩咐的；自己起草的写等谁确认。最多列 5 条，其余给一个链接。
const props = defineProps<{ title: string, drafts: Draft[], waitingFor?: string }>()
const { staffName } = useDrafts()
const shown = computed(() => props.drafts.slice(0, 5))
</script>

<template>
  <section v-if="drafts.length" data-testid="home-drafts" class="min-w-0">
    <header class="mb-2 flex items-baseline gap-2">
      <h2 class="text-base font-semibold">{{ title }}</h2>
      <span class="text-meta text-dimmed tabular-nums">{{ drafts.length }}</span>
    </header>
    <ul class="flex flex-col gap-2">
      <li v-for="draft in shown" :key="draft.id">
        <NuxtLink to="/drafts" class="flex gap-2 rounded-card bg-elevated px-4 py-3 hover:bg-accented">
          <AiMark class="mt-0.5 size-4 shrink-0" />
          <span class="min-w-0">
            <span class="block text-sm">{{ draft.summary }}</span>
            <span class="block text-meta text-muted tabular-nums">{{ waitingFor ?? `${staffName(draft.initiator)}吩咐` }} · {{ formatStamp(draft.created_at) }}</span>
          </span>
        </NuxtLink>
      </li>
    </ul>
    <NuxtLink v-if="drafts.length > shown.length" to="/drafts" class="tap-area mt-2 inline-flex items-center text-meta text-muted hover:text-default">
      还有 {{ drafts.length - shown.length }} 条<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" />
    </NuxtLink>
  </section>
</template>
