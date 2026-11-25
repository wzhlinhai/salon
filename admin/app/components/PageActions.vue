<script setup lang="ts">
// 页面的主操作放进顶栏右侧（DESIGN.md 页面骨架第 1 层）：一个实心主按钮，其余收进 ··· 菜单。
</script>

<template>
  <Teleport defer to="#navbar-actions">
    <div class="flex items-center gap-2">
      <slot />
    </div>
  </Teleport>
</template>
