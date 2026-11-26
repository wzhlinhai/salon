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

// 楷体写一句问候（样稿 17）：按钟点换，早上是开门。
const greeting = (() => {
  const hour = new Date().getHours()
  if (hour < 5) return '夜深了，辛苦了'
  if (hour < 12) return '早上好，开门啦'
  if (hour < 18) return '下午好'
  return '晚上好，辛苦了'
})()
</script>

<!-- 一张纸（样稿 17）：暖灰底上居中一张白纸，一幅店里的墨线小画、宋体「本店」、楷体问候，账号密码和一个实心按钮。 -->
<template>
  <main class="flex min-h-dvh flex-col bg-side px-6 py-10">
    <div class="m-auto w-full max-w-sm rounded-card border border-default bg-white px-8 py-10 shadow-paper">
      <SpotArt name="curl" class="mx-auto mb-4 w-24" />
      <h1 class="text-center text-title">本店</h1>
      <p class="said mt-2 mb-8 text-center text-base text-muted" data-testid="login-greeting">{{ greeting }}</p>
      <form class="space-y-4" @submit.prevent="submit">
        <UInput v-model="form.account" size="lg" class="w-full" placeholder="账号" aria-label="账号" autocomplete="username" autofocus />
        <UInput v-model="form.password" size="lg" type="password" class="w-full" placeholder="密码" aria-label="密码" autocomplete="current-password" />
        <UAlert v-if="error" color="error" variant="subtle" :title="error" />
        <UButton type="submit" size="lg" block :loading="loading" class="mt-2">登录</UButton>
      </form>
      <p class="mt-6 text-center text-meta text-pretty text-muted">用店里给你开的账号登录，忘了密码请找店长重置</p>
    </div>
  </main>
</template>
