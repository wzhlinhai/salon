<script setup lang="ts">
const route = useRoute()
const { me, can } = useMe()

const items = computed(() =>
  navItems.filter(i => !i.permission || can(i.permission)).map(({ label, icon, to }) => ({ label, icon, to }))
)

function logout() {
  setToken(null)
  me.value = null
  navigateTo('/login')
}
</script>

<template>
  <UApp>
    <NuxtPage v-if="route.path === '/login'" />
    <div v-else class="flex min-h-screen">
      <aside class="w-56 shrink-0 border-r border-default p-4 flex flex-col gap-4">
        <div class="font-semibold text-lg">门店后台</div>
        <UNavigationMenu orientation="vertical" :items="items" />
        <div class="mt-auto text-sm text-muted space-y-2">
          <div v-if="me">{{ me.staff.name }}（{{ me.staff.roles.map(r => r.name).join('、') || '无角色' }}）</div>
          <UButton size="sm" variant="ghost" icon="i-lucide-log-out" @click="logout">退出登录</UButton>
        </div>
      </aside>
      <main class="flex-1 p-6 min-w-0">
        <NuxtPage />
      </main>
    </div>
  </UApp>
</template>
