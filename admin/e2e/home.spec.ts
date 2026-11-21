import { expect, test } from '@playwright/test'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'

async function seedCheckout() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const position = await api<{ id: string }>(token, 'create_position', { name: `首页岗位${suffix}` })
  const stylist = await hire('发型师')
  const item = await api<{ id: string }>(token, 'create_item', {
    name: `首页项目${suffix}`,
    price: '88.00',
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    positions: [{ position_id: position.id, required: true }],
    level_prices: []
  })
  const created = await api<{ customer: { id: string } }>(token, 'create_customer', {
    name: `首页顾客${suffix}`,
    phone: `136${String(Date.now()).slice(-8)}`
  })
  const order = await api<{ id: string, net: string }>(token, 'open_order', {
    customer_id: created.customer.id,
    gender: 'female',
    designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: stylist.id }] }]
  })
  await api(token, 'checkout_orders', {
    orders: [{ order_id: order.id, payments: [{ method: 'cash', amount: order.net }] }]
  })
}

test('发型师是个人首页，打卡后按钮消失并显示打卡时间', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await expect(page.getByTestId('home-punch')).toBeVisible()
  await expect(page.getByTestId('home-front-actions')).toHaveCount(0)
  await expect(page.getByTestId('home-manager-metrics')).toHaveCount(0)
  await expect(page.getByTestId('home-switcher')).toHaveCount(0)
  await page.getByTestId('home-punch').click()
  await expect(page.getByTestId('home-punch')).toHaveCount(0)
  await expect(page.getByTestId('home-punch-time')).toContainText('已打卡')
})

test('前台是前台首页，显示快捷按钮且没有首页切换', async ({ page }) => {
  const front = await hire('前台')
  await login(page, front.account, front.password)
  const actions = page.getByTestId('home-front-actions')
  await expect(actions).toBeVisible()
  await expect(actions.getByRole('link', { name: '散客开单' })).toBeVisible()
  await expect(actions.getByRole('link', { name: '新预约' })).toBeVisible()
  await expect(actions.getByRole('button', { name: '打卡' })).toBeVisible()
  await expect(page.getByTestId('home-manager-metrics')).toHaveCount(0)
  await expect(page.getByTestId('home-switcher')).toHaveCount(0)

  await actions.getByRole('link', { name: '散客开单' }).click()
  const dialog = page.getByRole('dialog', { name: '开单', exact: true })
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('未识别顾客')
  // 参数用完即去掉，刷新不会再弹开单。
  await expect(page).toHaveURL(/\/cashier$/)
  await page.reload()
  await expect(page.getByTestId('scan-input')).toBeVisible()
  await expect(dialog).toHaveCount(0)
})

test('从个人菜单打卡后，首页的打卡按钮随即消失', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await expect(page.getByTestId('home-punch')).toBeVisible()
  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '打卡' }).click()
  await expect(page.getByTestId('home-punch')).toHaveCount(0)
  await expect(page.getByTestId('home-punch-time')).toContainText('已打卡')
})

test('店长默认是店长首页，显示今日经营指标', async ({ page }) => {
  await seedCheckout()
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  const metrics = page.getByTestId('home-manager-metrics')
  await expect(metrics).toBeVisible()
  await expect(metrics).toContainText('实收')
  await expect(metrics).toContainText('办卡充值')
  await expect(metrics).toContainText('客数')
  const guests = page.locator('[data-metric="客数"]')
  await expect(guests.getByTestId('metric-value')).toHaveText(/^\d+$/)
  await expect(guests.getByTestId('metric-value')).not.toHaveText(/\.\d/)
  // 有上周同日数据时写涨跌和"较上周同日"，没有时只写口径；两种都要带上单数。
  await expect(guests.getByTestId('metric-compare')).toHaveText(/^\s*([↑↓] \d+%\s+较上周同日 · )?共 \d+ 单\s*$/)
})

test('老板有多个首页，选择前台首页后刷新仍保留', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await expect(page.getByTestId('home-manager-metrics')).toBeVisible()
  const switcher = page.getByTestId('home-switcher')
  await expect(switcher).toBeVisible()
  await switcher.getByRole('radio', { name: '前台首页' }).click()
  await expect(page.getByTestId('home-front-actions')).toBeVisible()
  await page.reload()
  await expect(page.getByTestId('home-front-actions')).toBeVisible()
  await expect(page.getByTestId('home-switcher').getByRole('radio', { name: '前台首页' })).toHaveAttribute('aria-checked', 'true')
})

test('前台首页的推荐说法点一下就交给 AI', async ({ page }) => {
  let sent = ''
  await page.route('**/api/ai/chat', async (route) => {
    sent = route.request().postDataJSON().messages.at(-1).content
    await route.fulfill({ json: { reply: '查好了。', sources: [], drafts: [] } })
  })
  const front = await hire('前台')
  await login(page, front.account, front.password)
  await page.getByTestId('ai-suggestions').getByRole('button', { name: '今天还有谁没到店' }).click()
  await expect(page.getByRole('dialog', { name: '吩咐 AI' }).getByText('查好了。')).toBeVisible()
  expect(sent).toBe('今天还有谁没到店')
})
