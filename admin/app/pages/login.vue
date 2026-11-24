<script setup lang="ts">
definePageMeta({ title: '登录' })

const form = reactive({ account: '', password: '' })
const error = ref('')
const loading = ref(false)
const loginGuard = useRequestGuard()

async function submit() {
  const request = loginGuard.next()
  error.value = ''
  loading.value = true
  try {
    const { data, error: err } = await useApi().POST('/api/auth/login', { body: form })
    if (!request()) return
    if (!data) {
      error.value = errorText(err)
      return
    }
    setToken(data.token)
    await useMe().load()
    if (!request()) return
    await navigateTo('/')
  } finally {
    if (request()) loading.value = false
  }
}
</script>

<!-- 照 Notion 的登录页：一张纸，居中一窄栏，上面一幅店里的墨线小画。 -->
<template>
  <main class="flex min-h-dvh flex-col bg-white px-6 py-10">
    <div class="m-auto w-full max-w-sm">
      <SpotArt name="scissors" class="mx-auto mb-4 w-28" />
      <h1 class="text-center text-greet font-semibold">员工登录</h1>
      <p class="mt-2 mb-8 text-center text-pretty text-muted">用店里给你开的账号登录。忘了密码，请找店长重置。</p>
      <form class="space-y-4" @submit.prevent="submit">
        <UFormField label="账号">
          <UInput v-model="form.account" size="lg" class="w-full" autocomplete="username" autofocus />
        </UFormField>
        <UFormField label="密码">
          <UInput v-model="form.password" size="lg" type="password" class="w-full" autocomplete="current-password" />
        </UFormField>
        <UAlert v-if="error" color="error" variant="subtle" :title="error" />
        <UButton type="submit" size="lg" block :loading="loading" class="mt-2">登录</UButton>
      </form>
    </div>
  </main>
</template>
