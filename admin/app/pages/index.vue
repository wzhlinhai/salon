<script setup lang="ts">
const { me } = useMe()
</script>

<template>
  <div v-if="me" class="space-y-4">
    <h1 class="text-xl font-semibold">你好，{{ me.staff.name }}</h1>
    <p class="text-muted">角色：{{ me.staff.roles.map(r => r.name).join('、') || '无' }}</p>
    <ChangePassword />
  </div>
</template>
