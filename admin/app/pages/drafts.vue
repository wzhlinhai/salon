<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ title: '待确认', description: 'AI 起草的操作，确认后才会执行。' })

type DraftDetail = components['schemas']['Detail']

const api = useApi()
const toast = useToast()
const { me } = useMe()
const { drafts, pending, load, staffName } = useDrafts()

const tab = ref<'pending' | 'mine' | 'done'>('pending')
const tabs = computed(() => [
  { label: `未处理${pending.value.length ? ` ${pending.value.length}` : ''}`, value: 'pending' },
  { label: '我起草的', value: 'mine' },
  { label: '已处理', value: 'done' }
])

// 最新的在前。
const shown = computed(() => {
  const rows = tab.value === 'pending'
    ? pending.value
    : tab.value === 'mine'
      ? drafts.value.filter(draft => draft.initiator === me.value?.staff.id)
      : drafts.value.filter(draft => draft.status !== 'pending')
  return [...rows].reverse()
})

const emptyText = computed(() => ({
  pending: '没有未处理的草稿',
  mine: '你还没有让 AI 起草过',
  done: '还没有处理过的草稿'
})[tab.value])

// 详情：右侧滑出，显示预览和每次修改。
const detailOpen = ref(false)
const detail = ref<DraftDetail | null>(null)
const detailGuard = useRequestGuard()

async function openDetail(id: string) {
  detail.value = null
  detailOpen.value = true
  await loadDetail(id)
}

async function loadDetail(id: string) {
  const current = detailGuard.next()
  const { data, error } = await api.POST('/api/ops/get_draft', { body: { id } })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    detailOpen.value = false
    return
  }
  detail.value = data
}

watch(detailOpen, (value) => {
  if (!value) detailGuard.invalidate()
})

// 列表刷新后，打开着的草稿若被改过或已处理，重取详情：预览和记录跟上，也不会按旧版本确认。
watch(drafts, (rows) => {
  const shownDetail = detail.value
  if (!detailOpen.value || !shownDetail) return
  const latest = rows.find(row => row.id === shownDetail.id)
  if (latest && (latest.version !== shownDetail.version || latest.status !== shownDetail.status)) void loadDetail(shownDetail.id)
})

const eventLabels: Record<string, string> = {
  created: '起草',
  modified: '修改',
  confirmed: '确认',
  rejected: '拒绝',
  expired: '过期'
}

onMounted(() => load())
</script>

<template>
  <div class="w-full space-y-4">
    <UTabs v-model="tab" :items="tabs" :content="false" variant="link" color="neutral" data-testid="drafts-tabs" />

    <EmptyState v-if="!shown.length" class="box" icon="i-lucide-inbox" :title="emptyText" :hint="tab === 'pending' ? '在侧栏「搜索或吩咐 AI」里说一句话，AI 起草后会出现在这里等你确认' : undefined" data-testid="drafts-empty" />
    <div v-else class="grid items-stretch gap-3 md:grid-cols-2 xl:grid-cols-3">
      <DraftCard v-for="draft in shown" :key="draft.id" :draft="draft" @open="openDetail" />
    </div>

    <USlideover v-model:open="detailOpen" side="right" title="草稿详情" :ui="{ content: 'max-w-md' }">
      <template #body>
        <div v-if="detail" class="space-y-6" data-testid="draft-detail">
          <DraftCard :draft="detail" />
          <section class="space-y-2">
            <h3 class="text-sm font-medium text-muted">记录</h3>
            <ul class="divide-y divide-default border-y border-default text-sm">
              <li v-for="event in detail.events" :key="event.id" class="flex gap-3 py-2">
                <span class="w-10 shrink-0">{{ eventLabels[event.kind] ?? event.kind }}</span>
                <span class="truncate text-muted">{{ staffName(event.actor_id) }}</span>
                <span class="ms-auto shrink-0 text-muted tabular-nums">{{ formatStamp(event.at) }}</span>
              </li>
            </ul>
          </section>
        </div>
        <div v-else class="flex justify-center py-8">
          <UIcon name="i-lucide-loader-circle" class="size-5 animate-spin text-muted" />
        </div>
      </template>
    </USlideover>
  </div>
</template>
