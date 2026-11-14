import { expect, test } from '@playwright/test'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'

async function seed() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const settings = await api<{ business_date: string }>(token, 'get_settings', {})
  const month = settings.business_date.slice(0, 7)
  const position = await api<{ id: string }>(token, 'create_position', { name: `对账岗位${suffix}` })
  const staff = await hire('发型师')
  const item = await api<{ id: string, name: string }>(token, 'create_item', {
    name: `对账项目${suffix}`,
    price: '298.00',
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    positions: [{ position_id: position.id, required: false }],
    level_prices: []
  })
  const customer = await api<{ customer: { id: string } }>(token, 'create_customer', { name: `对账顾客${suffix}` })
  const order = await api<{ id: string }>(token, 'open_order', {
    customer_id: customer.customer.id,
    gender: 'female',
    designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  const deal = await api<{ id: string }>(token, 'create_groupbuy_deal', {
    platform: 'meituan', name: `对账券${suffix}`, purchase_price: '99.00', item_id: item.id
  })
  const redeem = async (index: number) => {
    const key = `month-${suffix}-${index}`
    return api<{ id: string }>(token, 'redeem_coupon', { platform: 'meituan', coupon_code: key, key })
  }
  const linked = await redeem(1)
  await api(token, 'link_coupon_order', { redemption_id: linked.id, deal_id: deal.id, order_id: order.id })
  const unlinked = await redeem(2)
  await api(token, 'link_coupon_order', { redemption_id: unlinked.id, deal_id: deal.id, order_id: order.id })
  await api(token, 'unlink_coupon_order', { redemption_id: unlinked.id })
  await redeem(3)
  return { token, suffix, month }
}

test('团购对账显示核销分类并录入结算 @groupbuy-month', async ({ page }) => {
  const { token, month } = await seed()
  const expected = await api<{
    redeemed_count: number
    purchase_total: string
    linked_open: { count: number, purchase_total: string }
    unlinked: { count: number, purchase_total: string }
    unlinked_no_deal: number
  }>(token, 'get_groupbuy_month', { platform: 'meituan', month })
  await login(page, owner.account, owner.password)
  await page.goto('/groupbuy-month')
  await page.getByLabel('结算月').fill(month)
  await page.getByRole('button', { name: '查询', exact: true }).click()
  await expect(page.getByTestId('redeemed-count')).toHaveText(String(expected.redeemed_count))
  await expect(page.getByTestId('purchase-total')).toHaveText(expected.purchase_total)
  await expect(page.getByTestId('linked-open')).toContainText(`${expected.linked_open.count} 张`)
  await expect(page.getByTestId('linked-open')).toContainText(expected.linked_open.purchase_total)
  await expect(page.getByTestId('unlinked')).toContainText(`${expected.unlinked.count} 张`)
  await expect(page.getByTestId('unlinked')).toContainText(expected.unlinked.purchase_total)
  await expect(page.getByTestId('unlinked-no-deal')).toContainText(`${expected.unlinked_no_deal} 张`)
  await page.getByRole('button', { name: '录入结算', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '录入团购平台结算额', exact: true })
  await dialog.getByLabel('结算额').fill('150.00')
  await dialog.getByLabel('结算备注').fill('平台后台账单')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  await expect(page.getByTestId('settlement-amount')).toHaveText('150.00')
  await expect(page.getByTestId('difference')).toHaveText((150 - Number(expected.purchase_total)).toFixed(2))
  await expect(page.getByTestId('settlement-history')).toContainText('150.00')
  await expect(page.getByTestId('settlement-history')).toContainText('平台后台账单')
})

test('没有日报权限的员工看不到结算录入', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const role = await api<{ id: string }>(token, 'create_role', {
    name: `对账员${suffix}`,
    permissions: [{ permission: 'report.view' }]
  })
  const staff = await api<{ id: string, name: string }>(token, 'create_staff', {
    name: `对账员${suffix}`, number: suffix, role_ids: [role.id]
  })
  const account = `month-${suffix}`
  const password = `password-${suffix}`
  await api(token, 'set_staff_login', { staff_id: staff.id, login_account: account, password })
  await login(page, account, password)
  await page.goto('/groupbuy-month')
  await expect(page).toHaveURL('/groupbuy-month')
  await expect(page.getByRole('heading', { name: '团购对账', exact: true })).toBeVisible()
  await expect(page.getByLabel('平台')).toBeVisible()
  await expect(page.getByRole('button', { name: '录入结算', exact: true })).toHaveCount(0)
})
