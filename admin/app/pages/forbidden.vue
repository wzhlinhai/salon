<script setup lang="ts">
// 一张纸（样稿 17）：没权限说清找谁，一幅小画、一句话、回首页。外壳不写大标题（bare），纸上这句就是标题。
definePageMeta({ title: '无权限', bare: true })
</script>

<template>
  <div class="box mx-auto mt-16 max-w-sm px-8 py-10 text-center" data-testid="forbidden">
    <SpotArt name="key" class="mx-auto mb-4 w-24" />
    <h1 class="text-base font-medium">没有权限</h1>
    <p class="mt-2 text-sm text-pretty text-muted">你的角色看不了这一页。需要的话，请老板或店长帮你开权限。</p>
    <UButton to="/" color="neutral" variant="outline" class="mt-6">回首页</UButton>
  </div>
</template>
