import { expect, test } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { api, apiLogin, owner, uniqueSuffix } from './helpers'

const output = process.env.SCREENSHOTS_DIR
test.skip(!output, 'SCREENSHOTS_DIR not set')
// 按 2 倍像素截：店里的屏幕多是高分屏，1 倍截图放大看是糊的。
test.use({ deviceScaleFactor: 2 })

const sizes = [{ width: 1366, height: 900 }, { width: 1024, height: 768 }]

async function ensureStaff(token: string, roleName: string, name: string) {
  const roles = await api<{ roles: { id: string, name: string }[] }>(token, 'list_role_options', {})
  const role = roles.roles.find(row => row.name === roleName)
  if (!role) throw new Error(`no role ${roleName}`)
  const list = await api<{ staff: { id: string, name: string }[] }>(token, 'list_staff', {})
  let staff = list.staff.find(row => row.name === name)
  if (!staff) {
    staff = await api<{ id: string, name: string }>(token, 'create_staff', {
      name,
      number: `S${uniqueSuffix()}`,
      role_ids: [role.id]
    })
  }
  const suffix = uniqueSuffix()
  const account = `shot-${suffix}`
  const password = `password-${suffix}`
  await api(token, 'set_staff_login', { staff_id: staff.id, login_account: account, password })
  return { id: staff.id, name, account, password }
}

async function ensurePosition(token: string) {
  const list = await api<{ items: { id: string, name: string }[] }>(token, 'list_positions', {})
  const existing = list.items.find(row => row.name === '发型师')
  if (existing) return existing
  return api<{ id: string, name: string }>(token, 'create_position', { name: '发型师' })
}

async function ensureItem(token: string, positionID: string, name: string, price: string) {
  const list = await api<{ items: { id: string, name: string }[] }>(token, 'list_items', {})
  const existing = list.items.find(row => row.name === name)
  if (existing) return existing
  return api<{ id: string, name: string }>(token, 'create_item', {
    name,
    price,
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    positions: [{ position_id: positionID, required: true }],
    level_prices: []
  })
}

async function ensureCustomer(token: string, name: string) {
  const search = await api<{ items: { id: string, name: string }[] }>(token, 'search_customers', {
    query: name,
    include_unidentified: false,
    limit: 20
  })
  const existing = search.items.find(row => row.name === name)
  if (existing) return existing
  const created = await api<{ customer: { id: string, name: string } }>(token, 'create_customer', {
    name,
    phone: `15${String(Date.now()).slice(-8)}${Math.floor(Math.random() * 10)}`
  })
  return created.customer
}

async function openOrder(token: string, customerID: string, stylistID: string, itemIDs: string[]) {
  return api<{ id: string, net: string }>(token, 'open_order', {
    customer_id: customerID,
    gender: 'female',
    designated: false,
    items: itemIDs.map(itemID => ({ item_id: itemID, staff: [{ slot: 1, staff_id: stylistID }] }))
  })
}

async function checkout(token: string, customerID: string, stylistID: string, itemIDs: string[]) {
  const order = await openOrder(token, customerID, stylistID, itemIDs)
  await api(token, 'checkout_orders', {
    orders: [{ order_id: order.id, payments: [{ method: 'cash', amount: order.net }] }]
  })
}

test('保存各角色和外壳的验收截图', async ({ page }) => {
  if (!output) return
  mkdirSync(output, { recursive: true })

  async function waitForSidebarIcons() {
    const links = page.locator('[data-testid="sidebar-nav"] a')
    await expect(links.first().locator('svg')).toBeVisible()
    await expect.poll(() => links.evaluateAll(rows => rows.every(row => !!row.querySelector('svg')))).toBe(true)
    await expect(page.getByTestId('sidebar-collapse')).toBeVisible()
  }

  async function shot(name: string) {
    for (const size of sizes) {
      await page.setViewportSize(size)
      await waitForSidebarIcons()
      await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
      await page.screenshot({ path: join(output!, `${name}-${size.width}x${size.height}.png`) })
    }
  }

  const token = await apiLogin(owner.account, owner.password)
  const manager = await ensureStaff(token, '店长', '周店长')
  const front = await ensureStaff(token, '前台', '小林')
  const stylist = await ensureStaff(token, '发型师', '阿杰')
  const position = await ensurePosition(token)
  const haircut = await ensureItem(token, position.id, '精剪 · 洗剪吹', '88.00')
  const dye = await ensureItem(token, position.id, '染发', '288.00')
  const chen = await ensureCustomer(token, '陈女士')
  const wang = await ensureCustomer(token, '王先生')

  await checkout(token, chen.id, stylist.id, [haircut.id])
  await checkout(token, wang.id, stylist.id, [dye.id])
  const open = await openOrder(token, chen.id, stylist.id, [haircut.id, dye.id])

  await page.setViewportSize(sizes[0]!)
  await page.goto('/login')
  await page.getByLabel('账号').fill(owner.account)
  await page.getByLabel('密码').fill(owner.password)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByTestId('next-done')).toHaveText(/¥[1-9]/)
  await expect(page.getByTestId('personal-menu')).toContainText('王老板')
  await shot('home-owner')

  await page.setViewportSize(sizes[0]!)
  await page.getByTestId('sidebar-collapse').click()
  await expect(page.getByTestId('sidebar-collapse')).toHaveAttribute('aria-label', '展开侧栏')
  await shot('sidebar-collapsed')
  await page.setViewportSize(sizes[0]!)
  await page.getByTestId('sidebar-collapse').click()

  await page.setViewportSize(sizes[0]!)
  await page.keyboard.press('Control+k')
  await expect(page.getByRole('dialog', { name: '搜索' })).toBeVisible()
  await shot('ctrl-k')
  await page.keyboard.press('Escape')

  await page.setViewportSize(sizes[0]!)
  await page.getByTestId('personal-menu').click()
  await expect(page.getByRole('menuitem', { name: '改密码' })).toBeVisible()
  await shot('personal-menu')
  await page.keyboard.press('Escape')

  await page.setViewportSize(sizes[0]!)
  await page.goto('/customers')
  await expect(page.getByRole('heading', { name: '顾客', exact: true })).toBeVisible()
  await shot('customers')

  async function loginAs(role: { account: string, password: string }) {
    await page.setViewportSize(sizes[0]!)
    await page.goto('/login')
    await page.getByLabel('账号').fill(role.account)
    await page.getByLabel('密码').fill(role.password)
    await page.getByRole('button', { name: '登录' }).click()
    await expect(page.getByTestId('home-greeting')).toBeVisible()
  }

  await loginAs(manager)
  await expect(page.getByTestId('next-done')).toBeVisible()
  await expect(page.getByTestId('personal-menu')).toContainText('周店长')
  await shot('home-manager')

  await loginAs(front)
  await expect(page.getByTestId('home-next')).toBeVisible()
  await expect(page.getByTestId('personal-menu')).toContainText('小林')
  await shot('home-front')

  await page.setViewportSize(sizes[0]!)
  await page.goto('/cashier')
  await page.locator(`[data-order-id="${open.id}"]`).click()
  await expect(page.getByTestId('selected-order')).toContainText('精剪 · 洗剪吹')
  await expect(page.getByTestId('selected-order')).toContainText('染发')
  await shot('cashier-front')

  await loginAs(stylist)
  await expect(page.getByTestId('home-punch')).toBeVisible()
  await expect(page.getByTestId('personal-menu')).toContainText('阿杰')
  await shot('home-stylist')

  await page.setViewportSize(sizes[0]!)
  await page.goto('/cashier')
  await page.locator(`[data-order-id="${open.id}"]`).click()
  await expect(page.getByTestId('selected-order')).toContainText('精剪 · 洗剪吹')
  await expect(page.getByTestId('selected-order')).toContainText('染发')
  await shot('cashier-stylist')
})
