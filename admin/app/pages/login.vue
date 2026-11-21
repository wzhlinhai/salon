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

// 右侧示意图用的虚构排班，只做装饰，不是真实数据。
const sample = [
  { name: '阿凯', blocks: [{ left: 8, width: 26, tone: 'bg-tag-purple', text: '染发' }, { left: 58, width: 16, tone: 'bg-tag-orange', text: '剪发' }] },
  { name: '小林', blocks: [{ left: 22, width: 14, tone: 'bg-tag-orange', text: '剪发' }, { left: 44, width: 22, tone: 'bg-tag-green', text: '护理' }] },
  { name: 'Mia', blocks: [{ left: 4, width: 18, tone: 'bg-tag-blue', text: '洗吹' }, { left: 70, width: 24, tone: 'hatch', text: '私约' }] }
]
</script>

<!-- 照参考图 login-01：左边窄表单，右边一块展示；右边在窄屏隐藏。 -->
<template>
  <div class="grid min-h-dvh bg-white lg:grid-cols-[minmax(420px,1fr)_1.15fr]">
    <main class="flex flex-col px-6 py-10">
      <div class="m-auto w-full max-w-sm">
        <h1 class="text-greet font-bold">员工登录</h1>
        <p class="mt-2 mb-8 text-muted">用店里给你开的账号登录。忘了密码，请找店长重置。</p>
        <form class="space-y-5" @submit.prevent="submit">
          <UFormField label="账号">
            <UInput v-model="form.account" size="xl" class="w-full" autocomplete="username" autofocus />
          </UFormField>
          <UFormField label="密码">
            <UInput v-model="form.password" size="xl" type="password" class="w-full" autocomplete="current-password" />
          </UFormField>
          <UAlert v-if="error" color="error" variant="subtle" :title="error" />
          <UButton type="submit" size="xl" block :loading="loading">登录</UButton>
        </form>
      </div>
    </main>

    <aside class="hidden flex-col justify-center gap-8 bg-side px-14 py-10 lg:flex" aria-hidden="true">
      <div>
        <p class="text-hero leading-tight font-bold">今天的店，<br>一眼看清。</p>
        <p class="mt-3 text-lead text-muted">谁在忙、谁空着、还有几位没到，打开首页就知道。</p>
      </div>
      <div class="box max-w-xl">
        <div class="mb-2 flex justify-between ps-14 text-2xs text-dimmed tabular-nums">
          <span>10</span><span>12</span><span>14</span><span>16</span><span>18</span><span>20</span>
        </div>
        <div v-for="row in sample" :key="row.name" class="flex items-center border-t border-default py-2">
          <span class="w-14 shrink-0 text-meta">{{ row.name }}</span>
          <div class="relative h-8 flex-1">
            <span
              v-for="block in row.blocks"
              :key="block.left"
              class="absolute inset-y-0 rounded-tag px-1.5 py-1 text-2xs"
              :class="block.tone"
              :style="{ left: `${block.left}%`, width: `${block.width}%` }"
            >{{ block.text }}</span>
          </div>
        </div>
      </div>
    </aside>
  </div>
</template>
