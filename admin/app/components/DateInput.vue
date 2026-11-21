<script setup lang="ts">
// 日期或月份输入（DESIGN.md 组件约定）：框里是 2026-11-22 / 2026-11，可以直接键入；右边按钮弹出中文月历。
// 不用浏览器原生 type=date，它按系统语言显示成 11/22/2026，弹出的月历也不跟页面样式。
import { CalendarDate, parseDate } from '@internationalized/date'

const props = defineProps<{ min?: string, max?: string, month?: boolean, size?: 'xs' | 'sm' | 'md' | 'lg' }>()
const model = defineModel<string>({ default: '' })

const open = ref(false)
const text = ref(model.value)
// 最近一次由本框写出去的值；外部改了 v-model（不是本框写的）才覆盖框里的字，免得打到一半被冲掉。
let emitted = model.value
watch(model, (v) => {
  if (v !== emitted) text.value = v
  emitted = v
})

const pattern = computed(() => props.month ? /^\d{4}-\d{2}$/ : /^\d{4}-\d{2}-\d{2}$/)

function toDate(v?: string): CalendarDate | undefined {
  if (!v) return undefined
  try {
    return parseDate(props.month ? `${v}-01` : v)
  } catch {
    return undefined
  }
}

// 和原生日期框一样：完整、真实的日期才是值，没写完就是空；超出 min/max 的照样写出去，由保存时的校验报错，不悄悄改掉。
function complete(v: string): boolean {
  return pattern.value.test(v) && !!toDate(v)
}

const outOfRange = computed(() => {
  const v = text.value
  if (!complete(v)) return false
  return (!!props.min && v < props.min.slice(0, v.length)) || (!!props.max && v > props.max.slice(0, v.length))
})
// 没写完、写错或超出范围时标红，读屏也读出"无效"。
const invalid = computed(() => text.value !== '' && (!complete(text.value) || outOfRange.value))

function onInput(v: string | number) {
  text.value = String(v)
  emitted = complete(text.value) ? text.value : ''
  model.value = emitted
}

const calendarValue = computed(() => toDate(model.value))
const minValue = computed(() => toDate(props.min?.slice(0, props.month ? 7 : 10)))
const maxValue = computed(() => toDate(props.max?.slice(0, props.month ? 7 : 10)))

function pick(d: unknown) {
  if (!(d instanceof CalendarDate)) return
  const iso = d.toString()
  text.value = props.month ? iso.slice(0, 7) : iso
  emitted = text.value
  model.value = text.value
  open.value = false
}
</script>

<template>
  <UInput
    :model-value="text"
    :min="min"
    :max="max"
    :size="size"
    :placeholder="month ? '年-月' : '年-月-日'"
    inputmode="numeric"
    autocomplete="off"
    class="tabular-nums"
    :ui="{ trailing: 'pe-1' }"
    :color="invalid ? 'error' : undefined"
    :highlight="invalid"
    :aria-invalid="invalid || undefined"
    @update:model-value="onInput"
  >
    <template #trailing>
      <UPopover v-model:open="open" :content="{ align: 'end' }">
        <UButton
          icon="i-lucide-calendar"
          color="neutral"
          variant="ghost"
          size="xs"
          :aria-label="month ? '选月份' : '选日期'"
        />
        <template #content>
          <UCalendar
            :model-value="calendarValue"
            :min-value="minValue"
            :max-value="maxValue"
            :type="month ? 'month' : undefined"
            prevent-deselect
            class="p-2"
            @update:model-value="pick"
          />
        </template>
      </UPopover>
    </template>
  </UInput>
</template>
