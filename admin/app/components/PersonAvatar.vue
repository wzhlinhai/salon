<script setup lang="ts">
// 人的头像（DESIGN.md 头像）：名字首字放在一个标签色圆里，顾客和员工同一个写法。
// 颜色按名字算，同一个人在每一页都是同一种颜色；只做装饰，读屏读旁边的名字。
// size：sm 24px 用在窄行，默认 30px，lg 64px 用在档案页名字上方。
const props = defineProps<{ name: string, size?: 'sm' | 'lg' }>()

// 类名写全，工具类扫描才看得到。
const tones = ['bg-tag-pink', 'bg-tag-yellow', 'bg-tag-green', 'bg-tag-blue', 'bg-tag-purple', 'bg-tag-orange', 'bg-tag-brown']
const tone = computed(() => {
  let h = 0
  for (const ch of props.name) h = (h * 31 + ch.codePointAt(0)!) % 9973
  return tones[h % tones.length]
})
const sizeClass = { sm: 'size-6 text-xs', lg: 'size-16 text-[26px]' }
</script>

<template>
  <span
    class="grid shrink-0 place-items-center rounded-full text-ink"
    :class="[size ? sizeClass[size] : 'size-[30px] text-meta', tone]"
    aria-hidden="true"
  >{{ name.slice(0, 1) }}</span>
</template>
