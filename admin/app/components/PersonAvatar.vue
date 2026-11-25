<script setup lang="ts">
// 人的头像（DESIGN.md 头像）：名字首字放在浅灰圆里，顾客和员工同一个写法。
// 不按人上色：颜色只留给要人动手的事（DESIGN.md「颜色只留给有事的东西」）；只做装饰，读屏读旁边的名字。
// size：sm 24px 用在窄行，默认 30px，md 44px 用在轮牌的椅子卡片，lg 64px 用在档案页名字上方。
defineProps<{ name: string, size?: 'sm' | 'md' | 'lg' }>()

const sizeClass = { sm: 'size-6 text-xs', md: 'size-11 text-figure', lg: 'size-16 text-figure' }
</script>

<template>
  <span
    class="grid shrink-0 place-items-center rounded-full bg-elevated text-toned"
    :class="size ? sizeClass[size] : 'size-[30px] text-meta'"
    aria-hidden="true"
  >{{ name.slice(0, 1) }}</span>
</template>
