<script setup lang="ts">
import type { SpotArtName } from '~/utils/art'

// 列表为空或筛选无结果（DESIGN.md 详情、弹窗和提示）：一句话 + 能解决它的那个按钮，居中放在内容区。
// art：页面级「还没有数据」时上面放一张小插画（SpotArt）；出错和筛选无结果仍用图标或不放。
// 页面标题上方已经有插画（页面元数据 art）时不再画，一屏最多一张。
const props = defineProps<{ title: string, hint?: string, icon?: string, art?: SpotArtName }>()
const route = useRoute()
const shownArt = computed(() => route.meta.art ? undefined : props.art)
</script>

<template>
  <div class="flex flex-col items-center gap-1.5 px-4 py-12 text-center" data-testid="empty-state">
    <SpotArt v-if="shownArt" :name="shownArt" class="mb-2 w-36" />
    <UIcon v-else-if="icon && !art" :name="icon" class="mb-1 size-6 text-dimmed" aria-hidden="true" />
    <p class="font-medium">{{ title }}</p>
    <p v-if="hint" class="text-meta text-muted">{{ hint }}</p>
    <div v-if="$slots.default" class="mt-2 flex gap-2">
      <slot />
    </div>
  </div>
</template>
