<script setup lang="ts">
// 报表入口（DESIGN.md 报表入口，照 report-02）：左边分类，右边报表列表；只列当前员工有权限看的报表。
definePageMeta({ permission: REPORT_PERMISSIONS, title: '报表', description: '看门店和员工的经营数字，点一张报表进去。' })

const { can } = useMe()
const visible = computed(() => reportCatalog.filter(r => can(r.permission)))
const categories = computed(() => reportCategories
  .map(label => ({ label, count: visible.value.filter(r => r.category === label).length }))
  .filter(c => c.count))
const picked = ref<string>('')
const shown = computed(() => picked.value ? visible.value.filter(r => r.category === picked.value) : visible.value)
</script>

<template>
  <div class="grid gap-6 md:grid-cols-[12rem_minmax(0,1fr)]">
    <nav aria-label="报表分类" class="flex flex-wrap gap-1 md:flex-col">
      <button
        v-for="c in [{ label: '', count: visible.length }, ...categories]"
        :key="c.label"
        type="button"
        class="flex items-center justify-between gap-3 rounded-md px-2.5 py-1.5 text-left text-sm hover:bg-hover"
        :class="picked === c.label ? 'bg-selected font-medium' : 'text-muted'"
        :aria-pressed="picked === c.label"
        @click="picked = c.label"
      >
        <span>{{ c.label || '全部' }}</span>
        <span class="text-xs text-dimmed tabular-nums">{{ c.count }}</span>
      </button>
    </nav>

    <div class="box overflow-hidden p-0" data-testid="report-list">
      <NuxtLink
        v-for="(r, i) in shown"
        :key="r.key"
        :to="`/reports/${r.key}`"
        class="flex items-center gap-3 px-4 py-3 hover:bg-card-hover"
        :class="i ? 'border-t border-default' : ''"
      >
        <span class="grid size-9 shrink-0 place-items-center rounded-md bg-tag-blue">
          <UIcon :name="r.icon" class="size-4.5 text-muted" aria-hidden="true" />
        </span>
        <span class="min-w-0 flex-1">
          <span class="block font-medium">{{ r.label }}</span>
          <span class="block truncate text-meta text-muted">{{ r.description }}</span>
        </span>
        <UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" aria-hidden="true" />
      </NuxtLink>
      <EmptyState v-if="!shown.length" title="没有可看的报表" hint="看报表要相应的权限，找老板开通" />
    </div>
  </div>
</template>
