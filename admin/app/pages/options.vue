<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'staff.manage' })

type LevelOrPosition = components['schemas']['Level'] | components['schemas']['Position']
const api = useApi()
const toast = useToast()

const kinds = [
  { key: 'level', title: '级别', hint: '例如首席、总监，可用于按级别定价', list: '/api/ops/list_levels', create: '/api/ops/create_level', update: '/api/ops/update_level' },
  { key: 'position', title: '岗位', hint: '例如大工、助理，用于计算提成', list: '/api/ops/list_positions', create: '/api/ops/create_position', update: '/api/ops/update_position' }
] as const

const items = reactive<Record<string, LevelOrPosition[]>>({ level: [], position: [] })
const draft = reactive<Record<string, string>>({ level: '', position: '' })

async function load() {
  for (const k of kinds) {
    const { data } = await api.POST(k.list, { body: {} })
    items[k.key] = data?.items ?? []
  }
}

onMounted(load)

function report(error: unknown, ok: unknown) {
  if (!ok) toast.add({ title: errorText(error), color: 'error' })
  load()
}

async function add(k: typeof kinds[number]) {
  const { data, error } = await api.POST(k.create, { body: { name: draft[k.key] ?? '' } })
  if (data) draft[k.key] = ''
  report(error, data)
}

async function save(k: typeof kinds[number], o: LevelOrPosition) {
  const { data, error } = await api.POST(k.update, { body: { id: o.id, name: o.name, active: o.active } })
  report(error, data)
}
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-xl font-semibold">级别与岗位</h1>
    <div class="grid md:grid-cols-2 gap-4">
      <UCard v-for="k in kinds" :key="k.key">
        <template #header>
          <div class="font-medium">{{ k.title }}</div>
          <div class="text-sm text-muted">{{ k.hint }}</div>
        </template>
        <div class="space-y-2">
          <div v-for="o in items[k.key]" :key="o.id" class="flex items-center gap-2">
            <UInput v-model="o.name" class="flex-1" @change="save(k, o)" />
            <USwitch v-model="o.active" :label="o.active ? '启用' : '停用'" @update:model-value="save(k, o)" />
          </div>
          <form class="flex gap-2 pt-2" @submit.prevent="add(k)">
            <UInput v-model="draft[k.key]" :placeholder="`新${k.title}`" class="flex-1" />
            <UButton type="submit" icon="i-lucide-plus">添加</UButton>
          </form>
        </div>
      </UCard>
    </div>
  </div>
</template>
