<script setup lang="ts" generic="T extends string">
// 收款方式一键选（DESIGN.md 表单）：方式不多，平铺成一排按钮，不用下拉。
// 选中照 Notion 视图页签的灰底，加一圈深一点的细线，不用蓝色。
// 单选组的键盘约定：整组只占一个 Tab 位，方向键在方式间移动并选中。
const props = withDefaults(defineProps<{
  items: { label: string, value: T }[]
  label?: string
}>(), { label: '方式' })
const model = defineModel<T | undefined>()
const emit = defineEmits<{ pick: [value: T] }>()

function pick(value: T) {
  if (model.value === value) return
  model.value = value
  emit('pick', value)
}

function move(event: KeyboardEvent, step: number) {
  const list = props.items
  const index = list.findIndex(m => m.value === model.value)
  const next = list[(index + step + list.length) % list.length]
  if (!next) return
  pick(next.value)
  const group = (event.currentTarget as HTMLElement).parentElement
  void nextTick(() => group?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus())
}
</script>

<template>
  <div role="radiogroup" :aria-label="label" class="flex flex-wrap gap-2">
    <UButton
      v-for="m in items"
      :key="m.value"
      role="radio"
      :aria-checked="model === m.value"
      :tabindex="model === m.value || !items.some(x => x.value === model) ? 0 : -1"
      size="sm"
      color="neutral"
      :variant="model === m.value ? 'soft' : 'outline'"
      :class="model === m.value ? 'bg-selected font-medium text-default ring-1 ring-(--ui-border-accented) ring-inset' : ''"
      @click="pick(m.value)"
      @keydown.left.prevent="move($event, -1)"
      @keydown.right.prevent="move($event, 1)"
      @keydown.up.prevent="move($event, -1)"
      @keydown.down.prevent="move($event, 1)"
    >
      {{ m.label }}
    </UButton>
  </div>
</template>
