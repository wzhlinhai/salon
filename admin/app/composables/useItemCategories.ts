// 项目 → 类别名：首页排班时间轴的窄块只写类别的头一个字。
// 不再按类别上色：服务类别的颜色只在报表的构成图里出现（DESIGN.md「颜色只留给有事的东西」）。
export function useItemCategories() {
  const api = useApi()
  const categories = useState<Record<string, string>>('item-categories', () => ({}))

  async function load() {
    const { data } = await api.POST('/api/ops/list_items', { body: {} })
    if (!data) return
    categories.value = Object.fromEntries(data.items.map(item => [item.id, item.category?.name ?? item.name]))
  }

  return { categories, load }
}
