import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, cash, hire, login, owner, uniqueSuffix } from './helpers'

// 售后页：看板三列，办售后从原单开始，批准、拒绝在详情侧滑里做。
type Order = { id: string, net: string, business_date: string }

async function seed(payments: (order: Order, card: { id: string }) => object[]) {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const position = await api<{ id: string }>(token, 'create_position', { name: `售后岗位${suffix}` })
  const staff = await hire('发型师')
  const item = await api<{ id: string, name: string }>(token, 'create_item', {
    name: `售后项目${suffix}`, price: '88.00', discountable: true, labor_fee_amount: null, labor_fee_percent: null,
    positions: [{ position_id: position.id, required: true }], level_prices: []
  })
  const { customer } = await api<{ customer: { id: string, name: string } }>(token, 'create_customer', { name: `售后顾客${suffix}`, phone: `137${String(Date.now()).slice(-8)}` })
  const type = await api<{ id: string, name: string }>(token, 'create_card_type', { name: `售后卡${suffix}`, kind: 'stored_value', validity_kind: 'none' })
  const card = await api<{ id: string }>(token, 'open_card', { customer_id: customer.id, card_type_id: type.id, principal: '100.00', bonus: '0.00', sales_staff_ids: [staff.id], payments: cash('100.00') })
  const order = await api<Order>(token, 'open_order', {
    customer_id: customer.id, gender: 'female', designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  await api(token, 'checkout_orders', { orders: [{ order_id: order.id, payments: payments(order, card) }] })
  return { token, customer, item, card, type, order }
}

function lane(page: Page, status: string) {
  return page.getByTestId(`aftersale-lane-${status}`)
}

function cardOf(page: Page, status: string, customer: string) {
  return lane(page, status).getByTestId('aftersale-card').filter({ hasText: customer })
}

test('部分退款：从原单办起，批准后挪到已批准', async ({ page }) => {
  const { customer, item } = await seed(order => [{ method: 'cash', amount: order.net }])
  await login(page, owner.account, owner.password)
  await page.goto('/aftersales')
  await page.getByTestId('aftersale-new').click()
  const dialog = page.getByRole('dialog', { name: '办售后' })
  await dialog.getByRole('combobox', { name: '原单' }).click()
  await page.getByRole('option', { name: new RegExp(`${customer.name} · ${item.name}`) }).click()
  // 挑好的原单收成一条灰底条，下拉不再出现。
  await expect(dialog.getByTestId('aftersale-order-strip')).toContainText(customer.name)
  await expect(dialog.getByRole('combobox', { name: '原单' })).toHaveCount(0)
  await expect(dialog.getByTestId('refund-status')).toHaveText('')
  await dialog.getByRole('radio', { name: '部分退款' }).click()
  await expect(dialog.getByTestId('refund-status')).toHaveText('可退 ¥88')
  await expect(dialog.getByTestId('refund-total')).toHaveText('退 ¥0')
  await dialog.getByLabel('原因').fill('烫后卷度不满意，退一部分')
  await dialog.getByLabel('退款金额').fill('30')
  await expect(dialog.getByTestId('refund-total')).toHaveText('退 ¥30')
  await dialog.getByTestId('aftersale-submit').click()
  await expect(dialog).toHaveCount(0)

  // 提交后直接滑出详情，卡片在待审批列。
  const card = cardOf(page, 'pending', customer.name)
  await expect(card).toContainText('部分退款')
  await expect(card).toContainText('退 ¥30')
  const detail = page.getByTestId('aftersale-detail')
  await expect(detail).toHaveAttribute('data-status', 'pending')
  await expect(detail.getByTestId('detail-refund-line')).toHaveText([/现金\s*¥30/])
  await expect(detail.getByTestId('detail-lead')).toContainText('要部分退款，等审批')
  await expect(detail).toContainText(item.name)
  await page.getByTestId('aftersale-approve').click()
  await expect(detail).toHaveAttribute('data-status', 'approved')
  await expect(detail.getByTestId('detail-lead')).toContainText('批准了，钱已退回')
  await expect(cardOf(page, 'approved', customer.name)).toHaveCount(1)
  await expect(cardOf(page, 'pending', customer.name)).toHaveCount(0)
  await expect(detail.getByTestId('aftersale-event')).toHaveCount(2)
})

test('全额退款先按原单填好卡本金和现金；拒绝写原因', async ({ page }) => {
  const { customer, order, type } = await seed((o, card) => [{ method: 'cash', amount: '38.00' }, { method: 'card', amount: '50.00', card_id: card.id }])
  await login(page, owner.account, owner.password)
  // 从别处带着原单进来：网址 ?order= 直接选好那一单。
  await page.goto(`/aftersales?order=${order.id}`)
  const dialog = page.getByRole('dialog', { name: '办售后' })
  await expect(dialog.getByTestId('aftersale-order-strip')).toContainText(customer.name)
  await dialog.getByRole('radio', { name: '全额退款' }).click()
  const lines = dialog.getByTestId('refund-line')
  await expect(lines).toHaveCount(2)
  await expect(lines.nth(0).getByRole('combobox', { name: '退回哪张卡' })).toContainText(type.name)
  await expect(lines.nth(0).getByLabel('退款金额')).toHaveValue('50.00')
  await expect(lines.nth(1).getByLabel('退款金额')).toHaveValue('38.00')
  await expect(dialog.getByTestId('refund-status')).toHaveText('可退 ¥88')
  await expect(dialog.getByTestId('refund-total')).toHaveText('退 ¥88')
  // 全额退款不等于可退金额时标红，提交按钮不可点。
  await lines.nth(1).getByLabel('退款金额').fill('37')
  await expect(dialog.getByTestId('refund-status')).toHaveClass(/text-ink-red/)
  await expect(dialog.getByTestId('refund-status')).toHaveText('可退 ¥88，全额退款还差 ¥1')
  await expect(dialog.getByTestId('aftersale-submit')).toBeDisabled()
  await lines.nth(1).getByLabel('退款金额').fill('38')
  await dialog.getByLabel('原因').fill('顾客要求全退')
  await dialog.getByTestId('aftersale-submit').click()
  await expect(dialog).toHaveCount(0)

  const detail = page.getByTestId('aftersale-detail')
  await expect(detail.getByTestId('detail-refund-line')).toHaveText([/退回卡本金\s*¥50/, /现金\s*¥38/])
  await expect(detail.getByTestId('detail-refund-total')).toHaveText('¥88')
  await page.getByTestId('aftersale-reject').click()
  const confirm = page.getByRole('dialog', { name: `拒绝${customer.name}这张售后单？` })
  await confirm.getByTestId('reject-note').fill('已经重做过，不退')
  await confirm.getByTestId('reject-confirm').click()
  await expect(confirm).toHaveCount(0)
  await expect(detail).toHaveAttribute('data-status', 'rejected')
  await expect(detail).toContainText('已经重做过，不退')
  await expect(cardOf(page, 'rejected', customer.name)).toHaveCount(1)
})

test('弹窗关掉再开仍能挑原单；修补单不填退款，批准后写批准了修补', async ({ page }) => {
  const { customer, item } = await seed(order => [{ method: 'cash', amount: order.net }])
  await login(page, owner.account, owner.password)
  await page.goto('/aftersales')
  const dialog = page.getByRole('dialog', { name: '办售后' })
  await page.getByTestId('aftersale-new').click()
  await expect(dialog.getByRole('combobox', { name: '原单' })).toBeVisible()
  await dialog.getByRole('button', { name: '取消' }).click()
  await expect(dialog).toHaveCount(0)
  // 第二次打开，营业日没变也要重新读那天的单。
  await page.getByTestId('aftersale-new').click()
  await dialog.getByRole('combobox', { name: '原单' }).click()
  await page.getByRole('option', { name: new RegExp(`${customer.name} · ${item.name}`) }).click()
  await expect(dialog.getByTestId('aftersale-order-strip')).toContainText(customer.name)
  await expect(dialog.getByTestId('refund-line')).toHaveCount(0)
  await expect(dialog.getByTestId('aftersale-submit')).toBeDisabled()
  await dialog.getByLabel('原因').fill('发尾修一下')
  await dialog.getByTestId('aftersale-submit').click()
  await expect(dialog).toHaveCount(0)

  const detail = page.getByTestId('aftersale-detail')
  await expect(detail.getByTestId('detail-lead')).toContainText('要修补，等审批')
  await expect(detail.getByTestId('deduct-performance')).toHaveCount(0)
  await page.getByTestId('aftersale-approve').click()
  await expect(detail).toHaveAttribute('data-status', 'approved')
  await expect(detail.getByTestId('detail-lead')).toContainText('批准了修补')
  await expect(cardOf(page, 'approved', customer.name)).toContainText('修补')
})
