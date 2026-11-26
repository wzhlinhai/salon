<script setup lang="ts">
// 改密码（个人菜单打开）：弹窗里直接是两个字段，不再套一张带标题的卡片；按钮在弹窗底栏，回车也能提交。
const open = defineModel<boolean>('open', { required: true })
const form = reactive({ old_password: '', new_password: '' })
const toast = useToast()
const { me } = useMe()

const guard = useRequestGuard()
const saving = ref(false)

watch(open, (value) => {
  if (!value) return
  form.old_password = ''
  form.new_password = ''
})

async function submit() {
  if (saving.value) return
  const current = guard.next()
  saving.value = true
  try {
    const { data, error } = await useApi().POST('/api/ops/change_my_password', { body: form })
    if (!current()) return
    if (data) {
      toast.add({ title: '密码已修改，请重新登录', color: 'success' })
      setToken(null)
      me.value = null
      await navigateTo('/login')
    } else {
      toast.add({ title: errorText(error), color: 'error' })
    }
  } finally {
    if (current()) saving.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" title="改密码">
    <template #body>
      <form id="change-password" class="space-y-4" @submit.prevent="submit">
        <UFormField label="原密码"><UInput v-model="form.old_password" type="password" autocomplete="current-password" class="w-full" /></UFormField>
        <UFormField label="新密码（至少 8 位）"><UInput v-model="form.new_password" type="password" autocomplete="new-password" class="w-full" /></UFormField>
      </form>
    </template>
    <template #footer>
      <ModalActions @cancel="open = false">
        <UButton type="submit" form="change-password" :loading="saving">改密码</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
