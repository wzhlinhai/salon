import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'

interface Seed {
  token: string
  suffix: string
  position: { id: string, name: string }
  staff: { id: string, name: string, account: string, password: string }
  item: { id: string, name: string, price: string }
  customer: { id: string, name: string }
  phone: string
}

function phone() {
  return `139${String(Date.now()).slice(-8)}`
}

async function seedItem(token: string, suffix: string, price = '88.00') {
  const position = await api<{ id: string, name: string }>(token, 'create_position', { name: `收银岗位${suffix}` })
  const staff = await hire('发型师')
  const item = await api<{ id: string, name: string, price: string }>(token, 'create_item', {
    name: `收银项目${suffix}`,
    price,
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    positions: [{ position_id: position.id, required: true }],
    level_prices: []
  })
  return { position, staff, item }
}

async function seed(): Promise<Seed> {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const { position, staff, item } = await seedItem(token, suffix)
  const phoneNumber = phone()
  const result = await api<{ customer: { id: string, name: string } }>(token, 'create_customer', { name: `收银顾客${suffix}`, phone: phoneNumber })
  return { token, suffix, position, staff, item, customer: result.customer, phone: phoneNumber }
}

async function businessDate(token: string) {
  const settings = await api<{ business_date: string, settings: { key: string, value: unknown }[] }>(token, 'get_settings', {})
  return settings
}

function addDays(value: string, delta: number) {
  const [year, month, date] = value.split('-').map(Number)
  return new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, (date ?? 1) + delta)).toISOString().slice(0, 10)
}

async function chooseItem(page: Page, dialogRole: 'dialog', itemName: string, staffName: string) {
  const dialog = page.getByRole(dialogRole, { name: '开单' })
  await dialog.getByRole('combobox', { name: '项目1' }).click()
  await page.getByRole('option', { name: new RegExp(itemName) }).click()
  await dialog.getByRole('combobox', { name: '项目1岗位1员工' }).click()
  await page.getByRole('option', { name: staffName, exact: true }).click()
  return dialog
}

test('扫码开单、现金结账并按设置打印小票', async ({ page }) => {
  const { token, suffix, staff, item, customer, phone: phoneNumber } = await seed()
  await page.addInitScript(() => {
    const state = window as unknown as { __receiptPrinted?: number }
    window.print = () => {
      state.__receiptPrinted = (state.__receiptPrinted ?? 0) + 1
    }
  })
  const settings = await businessDate(token)
  const header = `小票抬头${suffix}`
  const footer = `小票页尾${suffix}`
  await api(token, 'change_setting', { key: 'receipt_header', value: header, effective_from: settings.business_date })
  await api(token, 'change_setting', { key: 'receipt_footer', value: footer, effective_from: settings.business_date })
  await api(token, 'change_setting', { key: 'receipt_show_staff', value: 'hide', effective_from: settings.business_date })
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  const scan = page.getByLabel('扫码或输入手机号、券码')
  await scan.fill(phoneNumber)
  await scan.press('Enter')
  await expect(page.getByTestId('scan-kind')).toHaveText('识别为手机号')
  await page.getByRole('button', { name: new RegExp(customer.name) }).click()
  await page.getByRole('button', { name: '为这位顾客开单' }).click()
  const dialog = await chooseItem(page, 'dialog', item.name, staff.name)
  await dialog.getByRole('button', { name: '开单', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  await expect(page.getByTestId('selected-order')).toContainText(item.name)
  await expect(page.getByTestId('cash-expected')).toHaveText('88.00')
  await page.getByRole('button', { name: '结账', exact: true }).click()
  const receipt = page.getByTestId('receipt')
  await expect(receipt).toBeVisible()
  await expect(page.getByTestId('receipt-header')).toHaveText(header)
  await expect(page.getByTestId('receipt-footer')).toHaveText(footer)
  await expect(page.getByTestId('receipt-payments')).toContainText('现金')
  await expect(page.getByTestId('receipt-staff')).toHaveCount(0)
  await expect(page.getByTestId('receipt-customer')).toContainText('*')
  await expect.poll(() => page.evaluate(() => (window as unknown as { __receiptPrinted?: number }).__receiptPrinted ?? 0)).toBe(1)
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(page.getByTestId('last-receipt')).toBeVisible()
  await page.getByRole('button', { name: '补打小票' }).click()
  await expect(page.getByTestId('receipt-header')).toHaveText(header)
  expect(await page.evaluate(() => (window as unknown as { __receiptPrinted?: number }).__receiptPrinted ?? 0)).toBe(1)
})

test('补录单有标记且超出补录天数被拒绝', async ({ page }) => {
  const { token, staff, item, customer } = await seed()
  await page.addInitScript(() => {
    window.print = () => {}
  })
  const settings = await businessDate(token)
  const yesterday = addDays(settings.business_date, -1)
  const tooOld = addDays(settings.business_date, -4)
  const normal = await api<{ id: string }>(token, 'open_order', {
    customer_id: customer.id, gender: 'female', designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  await expect(page.locator(`[data-order-id="${normal.id}"]`)).toBeVisible()
  await expect(page.locator(`[data-order-id="${normal.id}"]`).getByTestId('backfill-badge')).toHaveCount(0)
  await page.getByRole('button', { name: '开单', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '开单' })
  await dialog.getByRole('switch', { name: '补录' }).click()
  await dialog.getByLabel('纸单营业日').fill(yesterday)
  await dialog.getByRole('combobox', { name: '项目1' }).click()
  await page.getByRole('option', { name: new RegExp(item.name) }).click()
  await dialog.getByRole('combobox', { name: '项目1岗位1员工' }).click()
  await page.getByRole('option', { name: staff.name, exact: true }).click()
  await dialog.getByRole('button', { name: '开单', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  await expect(page.getByTestId('backfill-badge')).toBeVisible()
  await expect(page.getByTestId('selected-backfill')).toBeVisible()
  await expect(page.getByTestId('cash-expected')).toHaveText('88.00')
  await page.getByRole('button', { name: '结账', exact: true }).click()
  await expect(page.getByTestId('receipt-backfill')).toHaveText('补录')

  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('button', { name: '开单', exact: true }).click()
  await dialog.getByRole('switch', { name: '补录' }).click()
  await dialog.getByLabel('纸单营业日').fill(tooOld)
  await dialog.getByRole('combobox', { name: '项目1' }).click()
  await page.getByRole('option', { name: new RegExp(item.name) }).click()
  await dialog.getByRole('combobox', { name: '项目1岗位1员工' }).click()
  await page.getByRole('option', { name: staff.name, exact: true }).click()
  await dialog.getByRole('button', { name: '开单', exact: true }).click()
  await expect(page.getByText(/只能补录最近 \d+ 个营业日的单/).first()).toBeVisible()
})

test('团购验券、核销、立即开单、暂不开单后关联与撤销', async ({ page }) => {
  const { token, suffix, staff, item, customer } = await seed()
  await page.addInitScript(() => {
    window.print = () => {}
  })
  const deal = await api<{ id: string }>(token, 'create_groupbuy_deal', {
    platform: 'meituan', name: `团购商品${suffix}`, purchase_price: '59.00', item_id: item.id
  })
  await api(token, 'open_order', {
    customer_id: customer.id,
    gender: 'female',
    designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  const code1 = `meituan-${suffix}-1`
  const code2 = `meituan-${suffix}-2`
  const code3 = `meituan-${suffix}-3`
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')

  const scan = page.getByLabel('扫码或输入手机号、券码')
  await scan.fill(code1)
  await scan.press('Enter')
  await expect(page.getByTestId('scan-kind')).toHaveText('识别为券码')
  await expect(page.getByLabel('券码平台')).toBeDisabled()
  await page.getByRole('button', { name: '核销', exact: true }).click()
  await expect(page.getByRole('button', { name: '立即开单', exact: true })).toBeVisible()
  await expect(scan).toBeFocused()
  await page.getByRole('button', { name: '立即开单', exact: true }).click()
  const groupbuyDialog = page.getByRole('dialog', { name: '团购立即开单' })
  await groupbuyDialog.getByRole('combobox', { name: '项目1岗位1员工' }).click()
  await page.getByRole('option', { name: staff.name, exact: true }).click()
  await groupbuyDialog.getByRole('button', { name: '开单', exact: true }).click()
  await expect(groupbuyDialog).not.toBeVisible()
  await expect(page.getByTestId('coupon-panel')).toHaveCount(0)
  await expect(page.getByTestId('order-groupbuy')).toContainText(deal.name)
  await expect(page.getByTestId('cash-expected')).toHaveText('0.00')
  await page.getByRole('button', { name: '结账', exact: true }).click()
  await expect(page.getByTestId('receipt-payments')).toContainText('团购')
  await expect(page.getByTestId('receipt-payments')).toContainText('门店标价 88.00')
  await expect(page.getByTestId('receipt-total')).toHaveText('88.00')
  await page.getByRole('button', { name: '关闭', exact: true }).click()

  await scan.fill(code2)
  await scan.press('Enter')
  await page.getByRole('button', { name: '核销', exact: true }).click()
  await page.getByRole('button', { name: '暂不开单', exact: true }).click()
  await expect(page.getByTestId('unlinked-coupons')).toContainText(code2)
  const row2 = page.getByTestId('unlinked-coupons').locator('div').filter({ hasText: code2 }).last()
  await row2.getByRole('button', { name: '关联挂单' }).click()
  const linkDialog = page.getByRole('dialog', { name: '关联团购券到挂单' })
  await linkDialog.getByRole('combobox', { name: '关联到挂单' }).click()
  await page.getByRole('option', { name: new RegExp(customer.name) }).click()
  await linkDialog.getByRole('button', { name: '关联', exact: true }).click()
  await expect(page.getByText('已关联挂单').first()).toBeVisible()
  await page.locator('[data-order-id]').filter({ hasText: customer.name }).first().click()
  await expect(page.getByTestId('order-groupbuy')).toContainText(deal.name)

  await scan.fill(code3)
  await scan.press('Enter')
  await page.getByRole('button', { name: '核销', exact: true }).click()
  await page.getByRole('button', { name: '暂不开单', exact: true }).click()
  const row3 = page.getByTestId('unlinked-coupons').locator('div').filter({ hasText: code3 }).last()
  await row3.getByRole('button', { name: '撤销核销' }).click()
  await expect(page.getByText('已撤销核销').first()).toBeVisible()
  await expect(page.getByTestId('unlinked-coupons')).not.toContainText(code3)
})

test('切换挂单丢弃旧单的迟到响应', async ({ page }) => {
  const { token, suffix, staff, item } = await seed()
  const other = await seedItem(token, `${suffix}b`)
  const first = await api<{ customer: { id: string, name: string } }>(token, 'create_customer', { name: `先开${suffix}`, phone: phone() })
  const second = await api<{ customer: { id: string, name: string } }>(token, 'create_customer', { name: `后开${suffix}`, phone: phone() })
  const orderA = await api<{ id: string }>(token, 'open_order', {
    customer_id: first.customer.id, gender: 'female', designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  const orderB = await api<{ id: string }>(token, 'open_order', {
    customer_id: second.customer.id, gender: 'female', designated: false,
    items: [{ item_id: other.item.id, staff: [{ slot: 1, staff_id: other.staff.id }] }]
  })
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/get_order', async (route) => {
    const body = route.request().postDataJSON() as { id?: string }
    if (body.id !== orderA.id) {
      await route.continue()
      return
    }
    const response = await route.fetch()
    started()
    await blocked
    await route.fulfill({ response })
  })
  const responseA = page.waitForResponse(r => r.url().endsWith('/api/ops/get_order') && (r.request().postDataJSON() as { id?: string }).id === orderA.id)
  await page.locator(`[data-order-id="${orderA.id}"]`).click()
  await pending
  await page.locator(`[data-order-id="${orderB.id}"]`).click()
  await expect(page.getByTestId('selected-order')).toContainText(second.customer.name)
  release()
  await (await responseA).finished()
  await page.waitForLoadState('networkidle')
  await expect(page.getByTestId('selected-order')).toContainText(second.customer.name)
  await expect(page.getByTestId('selected-order').locator(`[data-order-item="${item.name}"]`)).toHaveCount(0)
})

test('团购核销结果未知时查询状态沿用同一个幂等键', async ({ page }) => {
  const { suffix } = await seed()
  const code = `unknown-${suffix}`
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  let redeemKey = ''
  let queryKey = ''
  let redeemCalls = 0
  await page.route('**/api/ops/redeem_coupon', async (route) => {
    redeemCalls++
    redeemKey = (route.request().postDataJSON() as { key: string }).key
    await route.fulfill({ status: 409, contentType: 'application/problem+json', body: JSON.stringify({ detail: '结果未知，请稍后查询' }) })
  })
  await page.route('**/api/ops/get_redemption', async (route) => {
    queryKey = (route.request().postDataJSON() as { key: string }).key
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        id: '00000000-0000-0000-0000-000000000001',
        platform: 'meituan',
        coupon_code: code,
        key: queryKey,
        status: 'unknown',
        redeemed_at: null,
        revoked_at: null,
        redeemed_by: '00000000-0000-0000-0000-000000000002',
        order_id: null,
        order_payment_id: null,
        deal_id: null,
        purchase_price: null,
        store_price: null,
        linked_at: null,
        created_at: new Date().toISOString()
      })
    })
  })
  const scan = page.getByLabel('扫码或输入手机号、券码')
  await scan.fill(code)
  await scan.press('Enter')
  await page.getByRole('button', { name: '核销', exact: true }).click()
  await expect(page.getByText('核销结果未知，请先查询状态，不要再次核销。')).toBeVisible()
  await page.getByRole('button', { name: '查询状态', exact: true }).click()
  await expect.poll(() => queryKey).toBe(redeemKey)
  expect(queryKey).toBe(redeemKey)
  expect(redeemCalls).toBe(1)
  await expect(page.getByTestId('coupon-status')).toHaveText('unknown')
})

async function seedCountCard(token: string, suffix: string, customerID: string, staffID: string, itemID: string) {
  const cardType = await api<{ id: string, name: string }>(token, 'create_card_type', {
    name: `次卡${suffix}`, kind: 'count', validity_kind: 'none',
    items: [{ item_id: itemID, paid_count: 2, gift_count: 0, unit_price: '88.00' }]
  })
  await api(token, 'open_card', {
    customer_id: customerID, card_type_id: cardType.id, principal: '0.00', bonus: '0.00',
    sales_staff_ids: [staffID], payments: [{ method: 'cash', amount: '176.00' }]
  })
  return cardType
}

async function seedValueCard(token: string, suffix: string, customerID: string, staffID: string) {
  const cardType = await api<{ id: string, name: string }>(token, 'create_card_type', {
    name: `储值${suffix}`, kind: 'stored_value', validity_kind: 'none'
  })
  await api(token, 'open_card', {
    customer_id: customerID, card_type_id: cardType.id, principal: '100.00', bonus: '0.00',
    sales_staff_ids: [staffID], payments: [{ method: 'cash', amount: '100.00' }]
  })
  return cardType
}

test('用次卡结账并在小票显示剩余次数', async ({ page }) => {
  const { token, suffix, staff, item, customer } = await seed()
  await seedCountCard(token, suffix, customer.id, staff.id, item.id)
  const order = await api<{ id: string }>(token, 'open_order', {
    customer_id: customer.id, gender: 'female', designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  await page.addInitScript(() => {
    window.print = () => {}
  })
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  await page.locator(`[data-order-id="${order.id}"]`).click()
  await expect(page.getByTestId('cash-expected')).toHaveText('88.00')
  await page.getByRole('button', { name: '用次卡', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '用次卡', exact: true })
  await expect(dialog.getByRole('combobox', { name: '次卡' })).toContainText('剩 2 次')
  await dialog.getByRole('button', { name: '确定', exact: true }).click()
  await expect(page.getByTestId('cash-expected')).toHaveText('0.00')
  await expect(page.getByTestId('count-payments')).toContainText(item.name)
  await page.getByRole('button', { name: '结账', exact: true }).click()
  await expect(page.getByTestId('receipt-cards')).toContainText(item.name)
  await expect(page.getByTestId('receipt-cards')).toContainText('1 次')
})

test('计价卡试算刷新后保留默认收款行，卡余额支付要选卡', async ({ page }) => {
  const { token, suffix, staff, item, customer } = await seed()
  const cardType = await seedValueCard(token, suffix, customer.id, staff.id)
  const order = await api<{ id: string }>(token, 'open_order', {
    customer_id: customer.id, gender: 'female', designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  await page.locator(`[data-order-id="${order.id}"]`).click()
  await expect(page.getByTestId('cash-expected')).toHaveText('88.00')
  const amount = page.getByTestId('sale-payments').getByLabel('金额')
  await expect(amount).toHaveValue('88.00')

  const firstPreview = page.waitForResponse(r => r.url().endsWith('/api/ops/preview_checkout'))
  await page.getByLabel('计价卡').click()
  await page.getByRole('option', { name: new RegExp(cardType.name) }).click()
  await firstPreview
  await expect(page.getByTestId('cash-expected')).toHaveText('88.00')
  await expect(amount).toHaveValue('88.00')

  const secondPreview = page.waitForResponse(r => r.url().endsWith('/api/ops/preview_checkout'))
  await page.getByLabel('计价卡').click()
  await page.getByRole('option', { name: '不使用卡', exact: true }).click()
  await secondPreview
  await page.getByTestId('sale-payments').getByRole('combobox', { name: '方式' }).click()
  await page.getByRole('option', { name: '卡余额', exact: true }).click()

  let checkoutCalls = 0
  await page.route('**/api/ops/checkout_orders', async (route) => {
    checkoutCalls++
    await route.fulfill({ status: 422, contentType: 'application/problem+json', body: JSON.stringify({ detail: 'blocked' }) })
  })
  await page.getByRole('button', { name: '结账', exact: true }).click()
  await expect(page.getByText('卡余额支付请先选择计价卡', { exact: true })).toBeVisible()
  expect(checkoutCalls).toBe(0)
})

test('结账进行中不能重复结账或切换挂单', async ({ page }) => {
  const { token, staff, item, customer } = await seed()
  const other = await seedItem(token, `${Date.now().toString(36)}c`)
  const first = await api<{ id: string }>(token, 'open_order', {
    customer_id: customer.id, gender: 'female', designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  const second = await api<{ id: string }>(token, 'open_order', {
    customer_id: customer.id, gender: 'female', designated: false,
    items: [{ item_id: other.item.id, staff: [{ slot: 1, staff_id: other.staff.id }] }]
  })
  await page.addInitScript(() => {
    window.print = () => {}
  })
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  await page.locator(`[data-order-id="${first.id}"]`).click()
  await expect(page.getByTestId('cash-expected')).toHaveText('88.00')
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  let checkoutCalls = 0
  await page.route('**/api/ops/checkout_orders', async (route) => {
    checkoutCalls++
    const response = await route.fetch()
    started()
    await blocked
    await route.fulfill({ response })
  })
  await page.getByRole('button', { name: '结账', exact: true }).click()
  await pending
  await expect(page.getByRole('button', { name: '结账', exact: true })).toBeDisabled()
  await expect(page.locator(`[data-order-id="${first.id}"]`)).toBeDisabled()
  await expect(page.locator(`[data-order-id="${second.id}"]`)).toBeDisabled()
  expect(checkoutCalls).toBe(1)
  release()
  await expect(page.getByTestId('receipt')).toBeVisible()
})

async function twoItemOrder() {
  const seedData = await seed()
  const other = await seedItem(seedData.token, `${seedData.suffix}b`)
  const order = await api<{ id: string }>(seedData.token, 'open_order', {
    customer_id: seedData.customer.id, gender: 'female', designated: false,
    items: [
      { item_id: seedData.item.id, staff: [{ slot: 1, staff_id: seedData.staff.id }] },
      { item_id: other.item.id, staff: [{ slot: 1, staff_id: other.staff.id }] }
    ]
  })
  return { ...seedData, other, order }
}

test('项目删除进行中不能结账', async ({ page }) => {
  const { item, order } = await twoItemOrder()
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  await page.locator(`[data-order-id="${order.id}"]`).click()
  await expect(page.getByTestId('cash-expected')).toHaveText('176.00')
  let removeStarted!: () => void
  let releaseRemove!: () => void
  const removePending = new Promise<void>((resolve) => {
    removeStarted = resolve
  })
  const removeBlocked = new Promise<void>((resolve) => {
    releaseRemove = resolve
  })
  await page.route('**/api/ops/remove_order_item', async (route) => {
    removeStarted()
    await removeBlocked
    const response = await route.fetch()
    await route.fulfill({ response })
  })
  await page.locator(`[data-order-item="${item.name}"]`).getByRole('button', { name: '去掉' }).click()
  await removePending
  await expect(page.getByRole('button', { name: '结账', exact: true })).toBeDisabled()
  releaseRemove()
  await expect(page.getByTestId('cash-expected')).toHaveText('88.00')
  await expect(page.getByLabel('扫码或输入手机号、券码')).toBeFocused()
})

test('项目写成功后先清空旧试算', async ({ page }) => {
  const { item, order } = await twoItemOrder()
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  await page.locator(`[data-order-id="${order.id}"]`).click()
  await expect(page.getByTestId('cash-expected')).toHaveText('176.00')
  await page.route('**/api/ops/get_order', async (route) => {
    await route.fulfill({ status: 503, contentType: 'application/problem+json', body: JSON.stringify({ detail: '刷新失败' }) })
  })
  await page.locator(`[data-order-item="${item.name}"]`).getByRole('button', { name: '去掉' }).click()
  await expect(page.getByText('刷新失败').first()).toBeVisible()
  await expect(page.getByTestId('cash-expected')).toHaveCount(0)
})

test('暂不开单后不保留上次关联的挂单', async ({ page }) => {
  const { token, suffix, staff, item, customer } = await seed()
  await api(token, 'create_groupbuy_deal', {
    platform: 'meituan', name: `重置券${suffix}`, purchase_price: '59.00', item_id: item.id
  })
  await api(token, 'open_order', {
    customer_id: customer.id, gender: 'female', designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  const scan = page.getByLabel('扫码或输入手机号、券码')
  await scan.fill(`reset-${suffix}-1`)
  await scan.press('Enter')
  await page.getByRole('button', { name: '核销', exact: true }).click()
  const orderSelect = page.getByRole('combobox', { name: '关联挂单' })
  await orderSelect.click()
  await page.getByRole('option', { name: new RegExp(customer.name) }).click()
  await page.getByRole('button', { name: '暂不开单', exact: true }).click()
  await expect(scan).toBeFocused()
  await scan.fill(`reset-${suffix}-2`)
  await scan.press('Enter')
  await page.getByRole('button', { name: '核销', exact: true }).click()
  await expect(page.getByTestId('coupon-panel').getByRole('button', { name: '关联挂单', exact: true })).toBeDisabled()
})

test('券码扫描丢弃未完成的手机号搜索', async ({ page }) => {
  const { phone: phoneNumber, suffix } = await seed()
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  let searchCalls = 0
  await page.route('**/api/ops/search_customers', async (route) => {
    searchCalls++
    const response = await route.fetch()
    started()
    await blocked
    await route.fulfill({ response })
  })
  const scan = page.getByLabel('扫码或输入手机号、券码')
  await scan.fill(phoneNumber)
  await scan.press('Enter')
  await pending
  const code = `scan-${suffix}`
  await scan.fill(code)
  await expect(scan).toHaveValue(code)
  await scan.press('Enter')
  await expect(page.getByTestId('coupon-panel')).toBeVisible()
  release()
  await page.waitForLoadState('networkidle')
  await expect(page.getByTestId('customer-matches')).toHaveCount(0)
  expect(searchCalls).toBe(1)
})
