<script setup lang="ts">
// 页面的主操作放到页头标题行右侧（DESIGN.md 页面骨架）：一个实心主按钮，其余收进 ··· 菜单；窄屏折到说明下面。
</script>

<template>
  <Teleport defer to="#title-actions">
    <div class="flex items-center gap-2">
      <slot />
    </div>
  </Teleport>
</template>
