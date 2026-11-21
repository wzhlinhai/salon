<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ title: '待确认' })

type DraftDetail = components['schemas']['Detail']

const api = useApi()
const toast = useToast()
const { me } = useMe()
const { drafts, pending, load, staffName } = useDrafts()

const tab = ref<'pending' | 'mine' | 'done'>('pending')
const tabs = computed(() => [
  { label: `待确认${pending.value.length ? ` ${pending.value.length}` : ''}`, value: 'pending' },
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
  pending: '没有待确认的草稿',
  mine: '你还没有让 AI 起草过',
  done: '还没有处理过的草稿'
})[tab.value])

// 详情：右侧滑出，显示预览和每次修改。
const detailOpen = ref(false)
const detail = ref<DraftDetail | null>(null)
const detailGuard = useRequestGuard()

async function openDetail(id: string) {
  const current = detailGuard.next()
  detail.value = null
  detailOpen.value = true
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

const eventLabels: Record<string, string> = {
  created: '起草',
  modified: '修改',
  confirmed: '确认',
  rejected: '拒绝',
  expired: '过期'
}

function stamp(value: string) {
  const date = new Date(value)
  return `${date.getMonth() + 1} 月 ${date.getDate()} 日 ${date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })}`
}

onMounted(load)
</script>

<template>
  <div class="mx-auto w-full max-w-3xl space-y-4">
    <UTabs v-model="tab" :items="tabs" :content="false" variant="link" color="neutral" data-testid="drafts-tabs" />

    <p v-if="!shown.length" class="py-8 text-center text-sm text-muted" data-testid="drafts-empty">{{ emptyText }}</p>
    <div v-else class="space-y-3">
      <DraftCard v-for="draft in shown" :key="draft.id" :draft="draft" quiet @open="openDetail" />
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
                <span class="ms-auto shrink-0 text-muted tabular-nums">{{ stamp(event.at) }}</span>
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
