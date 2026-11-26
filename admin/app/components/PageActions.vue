<script setup lang="ts">
// 页面的主操作放进顶栏右侧（DESIGN.md 页面骨架第 1 层）：一个实心主按钮，其余收进 ··· 菜单。
// at="title"：放到页头标题行右侧（角色页这类内容本身是一张大表、主操作要和标题一起看的页面）。
defineProps<{ at?: 'navbar' | 'title' }>()
</script>

<template>
  <Teleport defer :to="at === 'title' ? '#title-actions' : '#navbar-actions'">
    <div class="flex items-center gap-2">
      <slot />
    </div>
  </Teleport>
</template>
