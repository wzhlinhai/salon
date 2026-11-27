<script setup lang="ts">
import type { components } from '~/utils/api'

type Summary = components['schemas']['ConversationSummary']

// AI 面板标题处的历史对话（照 Notion AI 的历史下拉）：搜索、按今天 / 近 7 天 / 更早分组，
// 悬停一行可归档；底下进「已归档的对话」，那里可以取消归档。只列本人的对话（服务端按本人过滤）。
const api = useApi()
const toast = useToast()
const { title, conversationId, openConversation, startNew } = useAiChat()
const { me } = useMe()
const guard = useRequestGuard()

const open = ref(false)
const archived = ref(false)
const query = ref('')
const items = ref<Summary[]>([])
const loading = ref(false)
const loadError = ref('')
const heading = computed(() => title.value || '新的 AI 对话')

// 打字停一下再搜，免得每个字都发一次；任何一次读取开始时，排着的搜索都取消。
let timer: ReturnType<typeof setTimeout> | undefined

function cancelSearch() {
  clearTimeout(timer)
  timer = undefined
}

onBeforeUnmount(cancelSearch)

async function load() {
  cancelSearch()
  const current = guard.next()
  loading.value = true
  loadError.value = ''
  const q = query.value.trim().slice(0, 50)
  try {
    const { data, error } = await api.POST('/api/ops/list_ai_conversations', { body: { archived: archived.value, q: q || undefined } })
    if (!current()) return
    items.value = data?.conversations ?? []
    loadError.value = data ? '' : errorText(error)
  } catch (error) {
    if (current()) loadError.value = errorText(error)
  } finally {
    if (current()) loading.value = false
  }
}

function search() {
  cancelSearch()
  timer = setTimeout(() => void load(), 250)
}

watch(open, (value) => {
  if (!value) {
    cancelSearch()
    guard.invalidate()
    return
  }
  archived.value = false
  query.value = ''
  void load()
})

// 换人登录：上一个人的列表不留。
watch(() => me.value?.staff.id, () => {
  open.value = false
  items.value = []
})

function toggleArchived() {
  archived.value = !archived.value
  void load()
}

const GROUPS = [
  { key: 'today', label: '今天' },
  { key: 'week', label: '近 7 天' },
  { key: 'older', label: '更早' }
] as const
const groups = computed(() => {
  const now = new Date()
  return GROUPS
    .map(g => ({ ...g, items: items.value.filter(c => recencyOf(c.updated_at, now) === g.key) }))
    .filter(g => g.items.length)
})

function choose(item: Summary) {
  open.value = false
  if (item.id !== conversationId.value) void openConversation(item.id)
}

async function setArchived(item: Summary, value: boolean) {
  const { error } = value
    ? await api.POST('/api/ops/archive_ai_conversation', { body: { id: item.id } })
    : await api.POST('/api/ops/unarchive_ai_conversation', { body: { id: item.id } })
  if (error) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  items.value = items.value.filter(c => c.id !== item.id)
  // 归档的正是面板里开着的这个对话，面板换成新对话。
  if (value && item.id === conversationId.value) startNew()
  toast.add({ title: value ? '已归档' : '已取消归档', color: 'success' })
}
</script>

<template>
  <UPopover v-model:open="open" :content="{ align: 'start', sideOffset: 4 }">
    <button type="button" class="flex min-w-0 items-center gap-1 rounded-md px-2 py-1 text-start hover:bg-hover focus-visible:outline-2 focus-visible:outline-primary" :title="heading" data-testid="ai-history-trigger">
      <span class="truncate">{{ heading }}</span>
      <UIcon name="i-lucide-chevron-down" class="size-4 shrink-0 text-muted" aria-hidden="true" />
    </button>
    <template #content>
      <div class="w-80 p-2 text-sm" data-testid="ai-history">
        <UInput v-model="query" icon="i-lucide-search" size="sm" placeholder="搜索对话" aria-label="搜索对话" class="w-full" data-testid="ai-history-search" @update:model-value="search" />
        <p v-if="archived" class="mt-2 px-2 text-xs text-dimmed">已归档的对话</p>
        <div class="mt-2 max-h-96 overflow-y-auto">
          <ListSkeleton v-if="loading && !items.length" :rows="3" />
          <LoadError v-else-if="loadError" title="历史对话没有读出来" :hint="loadError" @retry="load()" />
          <p v-else-if="!items.length" class="px-2 py-2 text-muted">{{ query.trim() ? '没有找到' : archived ? '没有归档的对话' : '还没有对话' }}</p>
          <section v-for="g in groups" v-else :key="g.key" class="mb-1">
            <p class="px-2 pt-2 pb-1 text-xs text-dimmed">{{ g.label }}</p>
            <ul>
              <li v-for="item in g.items" :key="item.id" class="group flex items-center gap-1 rounded-md hover:bg-hover" :class="item.id === conversationId && 'bg-hover'" data-testid="ai-history-item">
                <button type="button" class="min-w-0 flex-1 truncate rounded-md px-2 py-2 text-start text-default focus-visible:outline-2 focus-visible:outline-primary" :title="item.title" @click="choose(item)">{{ item.title }}</button>
                <span class="shrink-0 text-xs text-muted tabular-nums">{{ formatShortStamp(item.updated_at) }}</span>
                <UButton
                  color="neutral"
                  variant="ghost"
                  size="xs"
                  :icon="archived ? 'i-lucide-archive-restore' : 'i-lucide-archive'"
                  :aria-label="archived ? '取消归档' : '归档'"
                  class="opacity-0 group-hover:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100"
                  @click="setArchived(item, !archived)"
                />
              </li>
            </ul>
          </section>
        </div>
        <div class="mt-1 border-t border-default pt-1">
          <button type="button" class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-start text-muted hover:bg-hover focus-visible:outline-2 focus-visible:outline-primary" data-testid="ai-history-archived" @click="toggleArchived">
            <UIcon :name="archived ? 'i-lucide-arrow-left' : 'i-lucide-archive'" class="size-4 shrink-0" aria-hidden="true" />
            {{ archived ? '返回对话' : '已归档的对话' }}
          </button>
        </div>
      </div>
    </template>
  </UPopover>
</template>
