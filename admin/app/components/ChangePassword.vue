<script setup lang="ts">
const form = reactive({ old_password: '', new_password: '' })
const toast = useToast()
const { me } = useMe()

async function submit() {
  const { data, error } = await useApi().POST('/api/ops/change_my_password', { body: form })
  if (data) {
    toast.add({ title: '密码已修改，请重新登录', color: 'success' })
    setToken(null)
    me.value = null
    await navigateTo('/login')
  } else {
    toast.add({ title: errorText(error), color: 'error' })
  }
}
</script>

<template>
  <UCard class="max-w-sm">
    <template #header>修改密码</template>
    <form class="space-y-3" @submit.prevent="submit">
      <UFormField label="原密码"><UInput v-model="form.old_password" type="password" class="w-full" /></UFormField>
      <UFormField label="新密码（至少 8 位）"><UInput v-model="form.new_password" type="password" class="w-full" /></UFormField>
      <UButton type="submit">修改</UButton>
    </form>
  </UCard>
</template>
