<script setup lang="ts">
const form = reactive({ account: '', password: '' })
const error = ref('')
const loading = ref(false)
let loginRequest = 0

async function submit() {
  const request = ++loginRequest
  error.value = ''
  loading.value = true
  try {
    const { data, error: err } = await useApi().POST('/api/auth/login', { body: form })
    if (request !== loginRequest) return
    if (!data) {
      error.value = errorText(err)
      return
    }
    setToken(data.token)
    await useMe().load()
    if (request !== loginRequest) return
    await navigateTo('/')
  } catch (e) {
    if (request !== loginRequest) return
    error.value = errorText(e)
  } finally {
    if (request === loginRequest) loading.value = false
  }
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
