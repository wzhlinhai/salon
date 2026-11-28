<script setup lang="ts">
import type { MyReminder } from '~/composables/useMyReminders'

// 首页的「我的提醒」：只列没看过的，最多 3 条，点一条去「我的提醒」页；都看过了整块不出。
const { unreadItems } = useMyReminders()
const shown = computed(() => unreadItems.value.slice(0, 3))

// 一件事有后续时，卡片写最新的那句。
function latest(item: MyReminder) {
  return item.replies.at(-1) ?? item
}
</script>

<template>
  <section v-if="unreadItems.length" data-testid="home-reminders" class="min-w-0">
    <header class="mb-2 flex items-baseline gap-2">
      <h2 class="text-base font-semibold">我的提醒</h2>
      <span class="text-meta text-dimmed tabular-nums">{{ unreadItems.length }}</span>
    </header>
    <ul class="flex flex-col gap-2">
      <li v-for="item in shown" :key="item.id">
        <NuxtLink to="/reminders" class="flex gap-2 rounded-card bg-elevated px-4 py-3 hover:bg-accented">
          <AiMark class="mt-0.5 size-4 shrink-0" />
          <span class="min-w-0">
            <span class="block text-sm text-pretty">{{ latest(item).body }}</span>
            <span class="flex flex-wrap items-center gap-x-2 text-meta text-muted tabular-nums">
              <span v-if="item.tier === 'now'" class="tag bg-tag-yellow">要马上处理</span>
              <span>门店 AI · {{ formatStamp(latest(item).sent_at) }}</span>
            </span>
          </span>
        </NuxtLink>
      </li>
    </ul>
    <NuxtLink v-if="unreadItems.length > shown.length" to="/reminders" class="tap-area mt-2 inline-flex items-center text-meta text-muted hover:text-default">
      还有 {{ unreadItems.length - shown.length }} 条<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" />
    </NuxtLink>
  </section>
</template>
