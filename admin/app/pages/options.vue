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

const saving = reactive<Record<string, boolean>>({})
const actionGuard = useRequestGuard()
const loadGuard = useRequestGuard()

async function load() {
  const request = loadGuard.next()
  for (const k of kinds) {
    const { data, error } = await api.POST(k.list, { body: {} })
    if (!request()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      continue
    }
    items[k.key] = data?.items ?? []
  }
}

onMounted(load)

function report(error: unknown, ok: unknown) {
  if (!ok) toast.add({ title: errorText(error), color: 'error' })
  load()
}

async function add(k: typeof kinds[number]) {
  if (saving[k.key]) return
  const current = actionGuard.hold()
  saving[k.key] = true
  try {
    const name = draft[k.key] ?? ''
    const { data, error } = await api.POST(k.create, { body: { name } })
    if (!current()) return
    if (data && draft[k.key] === name) draft[k.key] = ''
    report(error, data)
  } finally {
    if (current()) saving[k.key] = false
  }
}

async function save(k: typeof kinds[number], o: LevelOrPosition) {
  if (saving[o.id]) return
  const current = actionGuard.hold()
  saving[o.id] = true
  try {
    const { data, error } = await api.POST(k.update, { body: { id: o.id, name: o.name, active: o.active } })
    if (!current()) return
    report(error, data)
  } finally {
    if (current()) saving[o.id] = false
  }
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
            <UInput v-model="o.name" :disabled="saving[o.id]" class="flex-1" @change="save(k, o)" />
            <USwitch v-model="o.active" :loading="saving[o.id]" :label="o.active ? '启用' : '停用'" @update:model-value="save(k, o)" />
          </div>
          <form class="flex gap-2 pt-2" @submit.prevent="add(k)">
            <UInput v-model="draft[k.key]" :placeholder="`新${k.title}`" class="flex-1" />
            <UButton :loading="saving[k.key]" type="submit" icon="i-lucide-plus">添加</UButton>
          </form>
        </div>
      </UCard>
    </div>
  </div>
</template>
