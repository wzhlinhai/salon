<script setup lang="ts">
const form = reactive({ account: '', password: '' })
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  loading.value = true
  const { data, error: err } = await useApi().POST('/api/auth/login', { body: form })
  loading.value = false
  if (!data) {
    error.value = errorText(err)
    return
  }
  setToken(data.token)
  await useMe().load()
  await navigateTo('/')
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center">
    <UCard class="w-80">
      <template #header>
        <div class="font-semibold">员工登录</div>
      </template>
      <form class="space-y-4" @submit.prevent="submit">
        <UFormField label="账号">
          <UInput v-model="form.account" class="w-full" autocomplete="username" />
        </UFormField>
        <UFormField label="密码">
          <UInput v-model="form.password" type="password" class="w-full" autocomplete="current-password" />
        </UFormField>
        <UAlert v-if="error" color="error" variant="subtle" :title="error" />
        <UButton type="submit" block :loading="loading">登录</UButton>
      </form>
    </UCard>
  </div>
</template>
