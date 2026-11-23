// 首页「最近访问」（照 Notion 首页）：记本人最近打开过的侧栏页面，只存在这台电脑上。
const LIMIT = 6

export interface RecentPage { to: string, at: number }

export function useRecentPages() {
  const { me } = useMe()
  const key = computed(() => me.value ? `salon_recent_${me.value.staff.id}` : '')

  function read(): RecentPage[] {
    if (!key.value) return []
    try {
      const value: unknown = JSON.parse(localStorage.getItem(key.value) || '[]')
      if (!Array.isArray(value)) return []
      return value.filter((row): row is RecentPage => typeof row?.to === 'string' && typeof row?.at === 'number')
    } catch {
      return []
    }
  }

  function record(to: string) {
    if (!key.value) return
    const list = [{ to, at: Date.now() }, ...read().filter(row => row.to !== to)].slice(0, LIMIT)
    localStorage.setItem(key.value, JSON.stringify(list))
  }

  return { read, record }
}
