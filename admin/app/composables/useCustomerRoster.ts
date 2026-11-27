import { customerRosterState } from '~/utils/customerRoster'

// 顾客名册（见 utils/customerRoster.ts）：每个页面建一份，列表刷新时 resolve 这一批的顾客 id。
export function useCustomerRoster() {
  const api = useApi()
  const { can } = useMe()
  return customerRosterState(async (ids) => {
    const { data } = await api.POST('/api/ops/get_customers', { body: { ids } })
    return data?.items ?? null
  }, () => can('customer.view'))
}
