import { writeFileSync } from 'node:fs'
import type { components } from '../app/utils/api'
import { env } from './env'
import { api, apiLogin, owner, uniqueSuffix } from './helpers'

type Schema = components['schemas']

// 默认项目会安排明天的方案，工资明细的今天样本必须先经真实 API 结账留存。
export default async function () {
  const token = await apiLogin(owner.account, owner.password)
  const { business_date: today } = await api<{ business_date: string }>(token, 'get_settings', {})
  // 全局准备会在单独重跑其他用例时再次执行；已有当天方案就沿用样本，避免未来版本使回填遭到 409。
  const inForce = await fetch(`${env.baseURL}/api/ops/get_commission_scheme`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
    body: JSON.stringify({ date: today })
  })
  if (inForce.status === 200) return
  if (inForce.status !== 404) throw new Error(`读取当天提成方案失败：${inForce.status} ${await inForce.text()}`)
  const suffix = uniqueSuffix()
  const staff = await api<Schema['Staff']>(token, 'create_staff', { name: `提成工资${suffix}`, number: suffix, role_ids: [] })
  const version = await api<Schema['CommissionSchemeVersionView']>(token, 'save_commission_scheme', {
    effective_from: today,
    scheme: { rules: [{ source: 'item', base: 'paid', by_position: false, by_designated: false, by_member: false, cells: [{ percent: '10.00' }] }], overrides: [], excluded_staff: [] }
  })
  const position = await api<Schema['Position']>(token, 'create_position', { name: `工资岗位${uniqueSuffix()}` })
  const item = await api<Schema['Item']>(token, 'create_item', {
    name: `工资项目${uniqueSuffix()}`, kind: 'service', price: '100.00', discountable: true,
    labor_fee_amount: null, labor_fee_percent: null, positions: [{ position_id: position.id, required: true, share_percent: 100 }], level_prices: [], member_prices: []
  })
  let order = await api<Schema['Order']>(token, 'open_order', { gender: 'female', designated: false, items: [] })
  order = await api<Schema['Order']>(token, 'add_order_item', { order_id: order.id, item: { item_id: item.id, staff: [] } })
  await api(token, 'set_order_item_staff', { order_id: order.id, order_item_id: order.items[0]!.id, staff: [{ slot: 1, staff_id: staff.id }] })
  await api(token, 'checkout_orders', { orders: [{ order_id: order.id, payments: [{ method: 'cash', amount: '100.00' }] }] })
  writeFileSync(new URL('../../.dev/payroll-e2e.json', import.meta.url), JSON.stringify({ staff, today, version }))
}
