<script setup lang="ts">
import type { components } from '~/utils/api'

// 「按项目记半轮」的项目清单：做这些项目结账时只记半轮，两次半轮算一轮。
// 清单是全店的，所有轮牌队列共用；只有门店把半轮规则设成「按项目」时才用得上。
type Item = components['schemas']['Item']

const api = useApi()
const toast = useToast()
const form = useModalForm()
const items = ref<Item[]>([])
const chosen = ref<Set<string>>(new Set())

function start() {
  form.begin(() => {
    items.value = []
    chosen.value = new Set()
  })
  form.load(async (current) => {
    const [i, h] = await Promise.all([
      api.POST('/api/ops/list_items', { body: {} }),
      api.POST('/api/ops/list_half_turn_items', { body: {} })
    ])
    if (!current()) return
    if (!i.data || !h.data) {
      toast.add({ title: errorText(i.error ?? h.error), color: 'error' })
      return
    }
    items.value = i.data.items.filter(it => it.active && it.kind === 'service')
    chosen.value = new Set(h.data.entries.map(e => e.item_id))
  })
}

defineExpose({ start })

const groups = computed(() => {
  const out = new Map<string, Item[]>()
  for (const it of items.value) {
    const key = it.category?.name ?? '未分类'
    out.set(key, [...(out.get(key) ?? []), it])
  }
  return [...out.entries()].map(([name, list]) => ({ name, list }))
})

function toggle(id: string, on: boolean | 'indeterminate') {
  const next = new Set(chosen.value)
  if (on === true) next.add(id)
  else next.delete(id)
  chosen.value = next
}

function save() {
  form.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/set_half_turn_items', { body: { item_ids: [...chosen.value] } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: `已保存，${data.entries.length} 个项目记半轮`, color: 'success' })
    form.open.value = false
  })
}
</script>

<template>
  <UModal v-model:open="form.open.value" title="记半轮的项目" description="做这些项目结账时只记半轮，两次半轮算一轮。清单全店通用。" :ui="{ content: 'sm:max-w-2xl' }">
    <template #body>
      <ListSkeleton v-if="form.loading.value" :rows="4" />
      <p v-else-if="!items.length" class="text-meta text-dimmed">还没有在用的服务项目。</p>
      <div v-else class="flex flex-col gap-5" data-testid="half-items">
        <fieldset v-for="g in groups" :key="g.name">
          <legend class="mb-2 text-meta text-muted">{{ g.name }}</legend>
          <div class="grid gap-2 sm:grid-cols-2">
            <UCheckbox
              v-for="it in g.list"
              :key="it.id"
              :model-value="chosen.has(it.id)"
              :label="it.name"
              @update:model-value="v => toggle(it.id, v)"
            />
          </div>
        </fieldset>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="form.open.value = false">
        <template #leading>
          <span class="text-meta text-muted tabular-nums">已选 {{ chosen.size }} 个</span>
        </template>
        <UButton :loading="form.saving.value" :disabled="form.loading.value" @click="save">保存</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
