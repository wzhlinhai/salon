import type { components } from '~/utils/api'

type Customer = components['schemas']['Customer']

// 一眼看清一位顾客（样稿 26 顾客小卡片、收银台顾客头）：上次哪天谁做的、做成什么样、配方，卡里还剩多少。
// 完整的去档案页看，这里只取三样：顾客、最近 10 条服务记录、卡。没有 customer.view 时什么都不取。
export interface CustomerGlance {
  customer: Customer
  // 储值卡本金加赠送，按分。
  balance: number
  // 次卡剩余次数合计（付费加赠送）。
  counts: number
  last: { date: string, staff: string, items: string, result: string | null } | null
  formula: { date: string, item: string, formula: string } | null
}

export function useCustomerGlance() {
  const api = useApi()
  const { can } = useMe()

  async function load(id: string): Promise<CustomerGlance | null> {
    if (!can('customer.view')) return null
    const [customer, history, cards] = await Promise.all([
      api.POST('/api/ops/get_customer', { body: { id } }),
      api.POST('/api/ops/list_service_history', { body: { customer_id: id, limit: 10 } }),
      api.POST('/api/ops/list_customer_cards', { body: { customer_id: id } })
    ])
    if (!customer.data) return null
    const active = (cards.data?.items ?? []).filter(card => card.status === 'active')
    const items = history.data?.items ?? []
    // 最近一次到店：第一条记录所在的那张单，同单的项目和做的人合在一行。
    const first = items[0]
    const visit = first ? items.filter(item => item.order_id === first.order_id) : []
    const hit = items.find(item => item.record?.formula)
    return {
      customer: customer.data,
      balance: active.filter(card => card.kind === 'stored_value').reduce((sum, card) => sum + cents(card.principal) + cents(card.bonus), 0),
      counts: active.filter(card => card.kind === 'count').reduce((sum, card) => sum + card.items.reduce((n, item) => n + item.paid_remaining + item.gift_remaining, 0), 0),
      last: first
        ? {
            date: first.business_date,
            staff: [...new Set(visit.flatMap(item => item.staff.map(staff => staff.name)))].join('、'),
            items: visit.map(item => item.item.name).join(' · '),
            result: visit.find(item => item.record?.result)?.record?.result ?? null
          }
        : null,
      formula: hit ? { date: hit.business_date, item: hit.item.name, formula: hit.record!.formula! } : null
    }
  }

  return { load }
}

// 11/02 这样的月/日。
export function monthDay(date: string) {
  const [, month, day] = date.split('-').map(Number)
  return `${month}/${day}`
}
