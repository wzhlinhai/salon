<script setup lang="ts">
// 报表入口：一张平铺的清单，只列当前员工有权限看的报表。报表只有几张，不分类、不筛选。
definePageMeta({ permission: REPORT_PERMISSIONS, title: '报表' })

const { can } = useMe()
const visible = computed(() => reportCatalog.filter(r => can(r.permission)))
</script>

<template>
  <div class="max-w-3xl">
    <div v-if="visible.length" class="divide-y divide-default border-y border-default" data-testid="report-list">
      <NuxtLink
        v-for="r in visible"
        :key="r.key"
        :to="`/reports/${r.key}`"
        class="flex items-center gap-3 px-2 py-3 hover:bg-hover"
      >
        <UIcon :name="r.icon" class="size-4 shrink-0 text-muted" aria-hidden="true" />
        <span class="min-w-0 flex-1">
          <span class="block font-medium">{{ r.label }}</span>
          <span class="block truncate text-meta text-muted">{{ r.description }}</span>
        </span>
        <UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" aria-hidden="true" />
      </NuxtLink>
    </div>
    <EmptyState v-else title="没有可看的报表" hint="看报表要相应的权限，找老板开通" />
  </div>
</template>
