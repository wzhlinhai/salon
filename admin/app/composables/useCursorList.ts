import type { Ref } from 'vue'
import { LOG_PAGE_SIZE } from '~/utils/paging'

// 游标分页列表：reload 从第一页开始，loadMore 接着最后一条往前取；过期的响应一律丢弃。
export function useCursorList<T extends { id: string }>(
  fetchPage: (beforeId?: string) => Promise<{ data?: T[], error?: unknown }>
): { items: Ref<T[]>, hasMore: Ref<boolean>, loading: Ref<boolean>, reload: () => Promise<void>, loadMore: () => Promise<void>, clear: () => void } {
  const items: Ref<T[]> = ref([])
  const hasMore = ref(false)
  const loading = ref(false)
  const toast = useToast()
  const pageGuard = useRequestGuard()

  function clear() {
    pageGuard.invalidate()
    items.value = []
    hasMore.value = false
    loading.value = false
  }

  async function reload() {
    clear()
    const current = pageGuard.next()
    loading.value = true
    try {
      const { data, error } = await fetchPage()
      if (!current()) return
      if (error) {
        toast.add({ title: errorText(error), color: 'error' })
        return
      }
      items.value = data ?? []
      hasMore.value = items.value.length === LOG_PAGE_SIZE
    } finally {
      if (current()) loading.value = false
    }
  }

  async function loadMore() {
    const last = items.value.at(-1)
    if (!last || loading.value) return
    const current = pageGuard.hold()
    loading.value = true
    try {
      const { data, error } = await fetchPage(last.id)
      if (!current()) return
      if (error) {
        toast.add({ title: errorText(error), color: 'error' })
        return
      }
      items.value.push(...(data ?? []))
      hasMore.value = data?.length === LOG_PAGE_SIZE
    } finally {
      if (current()) loading.value = false
    }
  }

  return { items, hasMore, loading, reload, loadMore, clear }
}
