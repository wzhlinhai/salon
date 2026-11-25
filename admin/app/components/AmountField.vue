<script setup lang="ts">
// 小操作弹窗的大数字（样稿 20）：¥ 加宋体大字的金额输入，下面一排快捷胶囊，点一下填进去。改价、充值共用。
const model = defineModel<string>({ required: true })
defineProps<{ label: string, presets?: { label: string, value: string }[] }>()
</script>

<template>
  <div>
    <UInput
      v-model="model"
      inputmode="decimal"
      :aria-label="label"
      class="w-full"
      :ui="{ base: 'h-14 ps-10 font-serif text-figure! tabular-nums', leading: 'ps-4' }"
    >
      <template #leading>
        <span class="font-serif text-figure text-muted">¥</span>
      </template>
    </UInput>
    <div v-if="presets?.length" class="mt-2 flex flex-wrap gap-2">
      <button
        v-for="preset in presets"
        :key="preset.label"
        type="button"
        class="h-7 rounded-full border border-default px-3 text-sm hover:bg-hover"
        :aria-pressed="model === preset.value"
        @click="model = preset.value"
      >
        {{ preset.label }}
      </button>
    </div>
  </div>
</template>
