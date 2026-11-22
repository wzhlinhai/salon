import { expect, test, type Locator } from '@playwright/test'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'

async function seedCheckout() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const position = await api<{ id: string }>(token, 'create_position', { name: `报表岗位${suffix}` })
  const stylist = await hire('发型师')
  const item = await api<{ id: string }>(token, 'create_item', {
    name: `报表项目${suffix}`,
    price: '66.00',
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    positions: [{ position_id: position.id, required: true }],
    level_prices: []
  })
  const created = await api<{ customer: { id: string } }>(token, 'create_customer', {
    name: `报表顾客${suffix}`,
    phone: `137${String(Date.now()).slice(-8)}`
  })
  const order = await api<{ id: string, net: string }>(token, 'open_order', {
    customer_id: created.customer.id,
    gender: 'male',
    designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: stylist.id }] }]
  })
  await api(token, 'checkout_orders', {
    orders: [{ order_id: order.id, payments: [{ method: 'wechat', amount: order.net }] }]
  })
  const settings = await api<{ business_date: string }>(token, 'get_settings', {})
  return { today: settings.business_date, stylist, itemName: `报表项目${suffix}` }
}

// 表格里的数换成分（金额）或整数（单数），合计逐列核对用。
function amount(text: string) {
  return Math.round(Number(text.replace(/[¥,\s]/g, '')) * 100)
}

async function expectTotalsAreSums(table: Locator) {
  const totals = table.getByTestId('business-totals')
  const cols = await totals.locator('td[data-col]').evaluateAll(cells => cells.map(c => c.getAttribute('data-col')!))
  expect(cols.length).toBeGreaterThan(10)
  for (const col of cols) {
    // 客数是四位小数、只显示一位，逐行相加会有舍入差，不在这里核。
    if (col === 'guest_total') continue
    const cells = await table.locator(`tbody td[data-col="${col}"]`).allTextContents()
    const sum = cells.reduce((s, t) => s + amount(t), 0)
    expect(amount(await totals.locator(`td[data-col="${col}"]`).textContent() ?? ''), col).toBe(sum)
  }
}

test('店长从报表入口进营业报表：合计等于各行之和，切按月、超范围有说明', async ({ page }) => {
  const { today } = await seedCheckout()
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.getByTestId('sidebar-nav').getByRole('link', { name: '报表' }).click()
  await expect(page).toHaveURL('/reports')
  await page.getByTestId('report-list').getByRole('link', { name: /营业报表/ }).click()
  await expect(page).toHaveURL(/\/reports\/business\?g=day/)

  // 默认本月：一天一行。别的用例同一天也在收款、退款，所以不猜金额，拿接口的数核对今天这行和合计行。
  const table = page.getByTestId('business-table')
  const todayRow = table.locator(`tbody tr[data-period="${today}"]`)
  await expect(todayRow).toBeVisible()
  await expect(page.getByRole('group', { name: '快捷范围' }).getByRole('button', { name: '本月' })).toHaveAttribute('aria-pressed', 'true')
  const token = await apiLogin(manager.account, manager.password)
  const report = await api<{ rows: { period: string, on_site: { method: string, amount: string }[] }[], totals: { service_revenue: string } }>(token, 'get_business_report', { granularity: 'day', from: `${today.slice(0, 7)}-01`, to: today })
  const wechat = report.rows.find(r => r.period === today)!.on_site.find(m => m.method === 'wechat')!.amount
  expect(amount(await todayRow.locator('td[data-col="wechat"]').textContent() ?? '')).toBe(amount(wechat))
  await expectTotalsAreSums(table)
  const service = await table.getByTestId('business-totals').locator('td[data-col="service_revenue"]').textContent()
  expect(amount(service ?? '')).toBe(amount(report.totals.service_revenue))
  expect(amount(await page.locator('[data-metric="实收"]').getByTestId('metric-value').textContent() ?? '')).toBe(amount(service ?? ''))

  // 按月：一月一行，月份写 YYYY-MM。
  await page.getByRole('radiogroup', { name: '按日或按月' }).getByRole('radio', { name: '按月' }).click()
  await expect(page).toHaveURL(/g=month/)
  await expect(table.locator(`tbody tr[data-period="${today.slice(0, 7)}"]`)).toBeVisible()
  await expectTotalsAreSums(table)

  // 按日超过 92 天：不查，旁边写为什么。
  await page.getByRole('radiogroup', { name: '按日或按月' }).getByRole('radio', { name: '按日' }).click()
  await page.getByLabel('开始').fill('2020-01-01')
  await expect(page.getByTestId('range-problem')).toHaveText('按日最多查 92 天，长了请切到按月')
})

// 员工业绩表里的列，合计行逐列核对；客数四位小数只显示一位，不在这里核。
async function expectStaffTotalsAreSums(table: Locator) {
  const totals = table.getByTestId('staff-totals')
  const cols = await totals.locator('td[data-col]').evaluateAll(cells => cells.map(c => c.getAttribute('data-col')!))
  expect(cols.length).toBeGreaterThan(8)
  for (const col of cols) {
    if (col.startsWith('guest_')) continue
    const cells = await table.locator(`tbody td[data-col="${col}"]`).allTextContents()
    const sum = cells.reduce((s, t) => s + amount(t), 0)
    expect(amount(await totals.locator(`td[data-col="${col}"]`).textContent() ?? ''), col).toBe(sum)
  }
}

test('店长看员工业绩：每人一行、合计等于各行之和，点一个人看每笔业绩', async ({ page }) => {
  const { today, stylist, itemName } = await seedCheckout()
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.goto('/reports')
  await page.getByTestId('report-list').getByRole('link', { name: /员工业绩/ }).click()
  await expect(page).toHaveURL(/\/reports\/staff\?from=/)
  // 员工业绩只按营业日查，没有按日 / 按月切换。
  await expect(page.getByRole('radiogroup', { name: '按日或按月' })).toHaveCount(0)

  const table = page.getByTestId('staff-table')
  const row = table.locator(`tbody tr[data-staff="${stylist.name}"]`)
  await expect(row).toBeVisible()
  const token = await apiLogin(manager.account, manager.password)
  const report = await api<{ rows: { staff: { id: string } | null, item_performance: string }[] }>(token, 'get_staff_report', { from: `${today.slice(0, 7)}-01`, to: today, staff_id: stylist.id })
  expect(report.rows).toHaveLength(1)
  expect(amount(await row.locator('td[data-col="item_performance"]').textContent() ?? '')).toBe(amount(report.rows[0]!.item_performance))
  await expectStaffTotalsAreSums(table)

  await row.getByRole('button', { name: stylist.name }).click()
  const detail = page.getByTestId('staff-detail')
  await expect(detail.getByTestId('staff-performance-list')).toContainText(itemName)
  await detail.getByRole('radiogroup', { name: '明细类别' }).getByRole('radio', { name: '提成' }).click()
  await expect(detail.getByText(/提成合计/)).toBeVisible()

  // 超过 92 天：不查，旁边写为什么（不提示切按月）。
  await page.keyboard.press('Escape')
  await page.getByLabel('开始').fill('2020-01-01')
  await expect(page.getByTestId('range-problem')).toHaveText('最多查 92 天')
})

test('发型师从报表入口只看得到员工业绩，表里只有自己，营业报表进不去', async ({ page }) => {
  const { stylist, itemName } = await seedCheckout()
  await login(page, stylist.account, stylist.password)
  await page.getByTestId('sidebar-nav').getByRole('link', { name: '报表' }).click()
  await expect(page).toHaveURL('/reports')
  const list = page.getByTestId('report-list')
  await expect(list.getByRole('link')).toHaveText([/员工业绩/])
  await list.getByRole('link', { name: /员工业绩/ }).click()

  const rows = page.getByTestId('staff-table').locator('tbody tr')
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toHaveAttribute('data-staff', stylist.name)
  await expect(page.getByText('只显示你自己的业绩。')).toBeVisible()
  await rows.first().getByRole('button', { name: stylist.name }).click()
  await expect(page.getByTestId('staff-performance-list')).toContainText(itemName)

  await page.goto('/reports/business')
  await expect(page).toHaveURL('/forbidden')
})
