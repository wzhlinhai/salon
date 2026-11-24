// 首页「最近访问」（照 Notion 首页）：记本人最近真正看过的东西——某位顾客、某张报表、日结、工资表等，
// 只存在这台电脑上。收银、预约、轮牌、顾客列表这些天天开的工作台侧栏里就有，不记。
const LIMIT = 6
const FLOOR_PAGES = ['/', '/cashier', '/bookings', '/rotation', '/customers', '/drafts']

export interface RecentPage { to: string, label: string, icon: string, at: number }

export function useRecentPages() {
  const { me } = useMe()
  const key = computed(() => me.value ? `salon_recent_${me.value.staff.id}` : '')

  function read(): RecentPage[] {
    if (!key.value) return []
    try {
      const value: unknown = JSON.parse(localStorage.getItem(key.value) || '[]')
      if (!Array.isArray(value)) return []
      // 旧版只存了 to 和 at，没有名字，丢掉。
      return value.filter((row): row is RecentPage =>
        typeof row?.to === 'string' && typeof row?.label === 'string' && typeof row?.icon === 'string' && typeof row?.at === 'number')
    } catch {
      return []
    }
  }

  function record(page: Omit<RecentPage, 'at'>) {
    if (!key.value || !page.label || FLOOR_PAGES.includes(page.to)) return
    const list = [{ ...page, at: Date.now() }, ...read().filter(row => row.to !== page.to)].slice(0, LIMIT)
    localStorage.setItem(key.value, JSON.stringify(list))
  }

  return { read, record }
}
