// 项目 → 类别底色：按项目类别列表的顺序取 Notion 标签色，同一类别同一种颜色，没有类别的用灰色。
// 首页的排班时间轴、今日顾客看板和我的顾客卡片共用，三处颜色一致。
const PALETTE = ['bg-tag-blue', 'bg-tag-orange', 'bg-tag-pink', 'bg-tag-green', 'bg-tag-purple', 'bg-tag-yellow', 'bg-tag-brown']

export interface ItemTone {
  tone: string
  category: string
}

export function useItemTones() {
  const api = useApi()
  const tones = useState<Record<string, ItemTone>>('item-tones', () => ({}))

  async function load() {
    const [items, categories] = await Promise.all([
      api.POST('/api/ops/list_items', { body: {} }),
      api.POST('/api/ops/list_item_categories', { body: {} })
    ])
    if (!items.data) return
    const order = (categories.data?.items ?? []).map(category => category.id)
    tones.value = Object.fromEntries(items.data.items.map((item) => {
      const index = item.category ? order.indexOf(item.category.id) : -1
      return [item.id, { tone: index < 0 ? 'bg-tag-gray' : PALETTE[index % PALETTE.length]!, category: item.category?.name ?? item.name }]
    }))
  }

  function toneOf(itemId: string) {
    return tones.value[itemId]?.tone ?? 'bg-tag-gray'
  }

  return { tones, load, toneOf }
}
