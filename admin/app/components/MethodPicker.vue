<script setup lang="ts" generic="T extends string">
// 表单里一键选一个值（DESIGN.md 表单）：收款方式、性别这类选项不多的，平铺成一排按钮，不用下拉。页面上切视图用 ViewTabs。
// 选中照 Notion 视图页签的灰底，加一圈深一点的细线，不用蓝色。
// 单选组的键盘约定：整组只占一个 Tab 位，方向键在方式间移动并选中。
// counter：收银台站着点（样稿 02），按钮 44 高，选中用墨色实心，一眼看清收的是哪种。
const props = withDefaults(defineProps<{
  items: { label: string, value: T }[]
  label?: string
  counter?: boolean
}>(), { label: '方式', counter: false })
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
  <!-- 收银台（counter）：四种以内等宽铺满一排（太窄时两个一排）；五六种一排三个、排两行，大按钮不挤成细条。 -->
  <div :class="counter ? '@container w-full' : 'contents'">
    <div role="radiogroup" :aria-label="label" :class="counter ? (items.length <= 4 ? 'grid w-full grid-cols-2 gap-2 @sm:grid-flow-col @sm:auto-cols-fr @sm:grid-cols-none' : 'grid w-full grid-cols-3 gap-2') : 'flex flex-wrap gap-2'">
      <UButton
        v-for="m in items"
        :key="m.value"
        role="radio"
        :aria-checked="model === m.value"
        :tabindex="model === m.value || !items.some(x => x.value === model) ? 0 : -1"
        :size="counter ? 'lg' : 'sm'"
        color="neutral"
        :variant="model === m.value ? (counter ? 'solid' : 'soft') : 'outline'"
        :class="[counter ? 'justify-center px-0' : '', model === m.value && !counter ? 'bg-selected font-medium text-default ring-1 ring-(--ui-border-accented) ring-inset' : '']"
        @click="pick(m.value)"
        @keydown.left.prevent="move($event, -1)"
        @keydown.right.prevent="move($event, 1)"
        @keydown.up.prevent="move($event, -1)"
        @keydown.down.prevent="move($event, 1)"
      >
        {{ m.label }}
      </UButton>
    </div>
  </div>
</template>
