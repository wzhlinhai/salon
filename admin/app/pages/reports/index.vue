<script setup lang="ts">
// 报表入口：一张平铺的清单，只列当前员工有权限看的报表。报表只有几张，不分类、不筛选。
definePageMeta({ permission: REPORT_PERMISSIONS, title: '报表' })

const { can } = useMe()
const visible = computed(() => reportCatalog.filter(r => can(r.permission)))
</script>

<template>
  <div class="max-w-3xl">
    <!-- 照 Notion 的子页面链接：图标 + 标题，悬停整行浅灰底；说明跟在标题后面。 -->
    <div v-if="visible.length" class="-mx-2 space-y-0.5" data-testid="report-list">
      <NuxtLink
        v-for="r in visible"
        :key="r.key"
        :to="`/reports/${r.key}`"
        class="flex items-baseline gap-2 rounded-md px-2 py-1.5 hover:bg-hover"
      >
        <UIcon :name="r.icon" class="size-4 shrink-0 translate-y-0.5 text-muted" aria-hidden="true" />
        <span class="shrink-0 font-medium underline decoration-(--ui-border-accented) underline-offset-4">{{ r.label }}</span>
        <span class="min-w-0 truncate text-muted">{{ r.description }}</span>
      </NuxtLink>
    </div>
    <EmptyState v-else title="没有可看的报表" hint="看报表要相应的权限，找老板开通" />
  </div>
</template>
