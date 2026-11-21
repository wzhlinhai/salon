<script setup lang="ts">
// 接在标题下那行说明末尾的动态内容（DESIGN.md 页面骨架第 2 层），比如"今天是营业日 2026-11-22。"。
// 固定的说明写在页面元数据 description 里，只有随数据变的半句才用这个。
</script>

<template>
  <Teleport defer to="#page-description">
    <span><slot /></span>
  </Teleport>
</template>
