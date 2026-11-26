import { expect, test, type Locator } from '@playwright/test'
import { api, apiLogin, cash, hire, login, owner, uniqueSuffix } from './helpers'
import { yuan } from '../app/utils/money'

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
  return { token, suffix, today: settings.business_date, stylist, customerId: created.customer.id, itemId: item.id, itemName: `报表项目${suffix}`, orderId: order.id }
}

// 表格里的数换成分（金额）或整数（单数），合计逐列核对用。
// 表身的零写成一道长横（yuanCell）。
function amount(text: string) {
  if (text.trim() === '—') return 0
  return Math.round(Number(text.replace(/[¥,\s]/g, '')) * 100)
}

// 合计行逐列核对：每个带 data-col 的格等于表身同列之和。minCols 是这张表至少该有的列数，防止列没渲染出来时空过。
// skip 跳过不该相加的列（客数四位小数只显示一位，逐行相加有舍入差）。
async function expectTotalsAreSums(table: Locator, totalsId: string, minCols: number, skip: (col: string) => boolean = () => false) {
  const totals = table.getByTestId(totalsId)
  const cols = await totals.locator('td[data-col]').evaluateAll(cells => cells.map(c => c.getAttribute('data-col')!))
  expect(cols.length).toBeGreaterThanOrEqual(minCols)
  for (const col of cols) {
    if (skip(col)) continue
    const cells = await table.locator(`tbody td[data-col="${col}"]`).allTextContents()
    const sum = cells.reduce((s, t) => s + amount(t), 0)
    expect(amount(await totals.locator(`td[data-col="${col}"]`).textContent() ?? ''), col).toBe(sum)
  }
}

// 顶上一段话（ReportBrief）里的数就是合计行同一列的数：结论和明细说的是同一件事。
async function expectBriefIsTotal(table: Locator, totalsId: string, col: string) {
  const total = await table.getByTestId(totalsId).locator(`td[data-col="${col}"]`).textContent()
  const figure = table.page().getByTestId('report-brief').locator(`[data-figure="${col}"]`)
  expect(amount(await figure.textContent() ?? ''), col).toBe(amount(total ?? ''))
}

test('店长从报表入口进营业报表：合计等于各行之和，切按月、超范围有说明', async ({ page }) => {
  const { today } = await seedCheckout()
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.getByTestId('sidebar-nav').getByRole('link', { name: '报表' }).click()
  await expect(page).toHaveURL('/reports')
  // 报表首页先用一段话说本月：刚结的账让实收不是零，走势和构成条随之出现。
  await expect(page.getByTestId('report-brief')).toContainText(/这个月到今天实收 ¥[1-9]/)
  await expect(page.getByTestId('report-spark').or(page.getByTestId('report-mix')).first()).toBeVisible()
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
  await expectTotalsAreSums(table, 'business-totals', 5, col => col === 'guest_total')
  // 合计行也要有微信这一列，免得列被藏掉后求和检查不到它。
  await expect(table.getByTestId('business-totals').locator('td[data-col="wechat"]')).toBeVisible()
  const service = await table.getByTestId('business-totals').locator('td[data-col="service_revenue"]').textContent()
  expect(amount(service ?? '')).toBe(amount(report.totals.service_revenue))
  await expectBriefIsTotal(table, 'business-totals', 'service_revenue')

  // 按月：一月一行，月份写 YYYY-MM。
  await page.getByRole('radiogroup', { name: '按日或按月' }).getByRole('radio', { name: '按月' }).click()
  await expect(page).toHaveURL(/g=month/)
  await expect(table.locator(`tbody tr[data-period="${today.slice(0, 7)}"]`)).toBeVisible()
  await expectTotalsAreSums(table, 'business-totals', 5, col => col === 'guest_total')

  // 按日超过 92 天：不查，旁边写为什么。
  await page.getByRole('radiogroup', { name: '按日或按月' }).getByRole('radio', { name: '按日' }).click()
  await page.getByLabel('开始').fill('2020-01-01')
  await expect(page.getByTestId('range-problem')).toHaveText('按日最多查 92 天，长了请切到按月')
})

test('店长看员工业绩：每人一行、合计等于各行之和，点一个人看每笔业绩，撤单的冲回记负数', async ({ page }) => {
  const { today, stylist, itemName, orderId } = await seedCheckout()
  // 撤掉这一单：业绩写一笔冲回，明细里要显示成负数。
  const ownerToken = await apiLogin(owner.account, owner.password)
  // 撤单授权窗口默认不开（不用授权）；收银用例会临时开 5 分钟，那时得先输密码开窗口，没开就忽略。
  await api(ownerToken, 'open_void_window', { password: owner.password }).catch(() => undefined)
  await api(ownerToken, 'void_order', { order_id: orderId, reason: '报表用例撤单' })
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
  await expectTotalsAreSums(table, 'staff-totals', 7, col => col.startsWith('guest_'))
  await expectBriefIsTotal(table, 'staff-totals', 'item_performance')

  await row.getByRole('button', { name: stylist.name }).click()
  const detail = page.getByTestId('staff-detail')
  const list = detail.getByTestId('staff-performance-list')
  await expect(list).toContainText(itemName)
  const reversal = list.locator('li').filter({ hasText: '冲回' })
  await expect(reversal).toHaveCount(1)
  await expect(reversal).toContainText(/-¥\d/)
  await detail.getByRole('radiogroup', { name: '明细类别' }).getByRole('radio', { name: '提成' }).click()
  await expect(detail.getByText(/提成合计/)).toBeVisible()

  // 超过 92 天：不查，旁边写为什么（不提示切按月）。
  await page.keyboard.press('Escape')
  await page.getByLabel('开始').fill('2020-01-01')
  await expect(page.getByTestId('range-problem')).toHaveText('最多查 92 天')
})

test('发型师从报表入口只看得到员工业绩，表里只有自己，顶栏「报表」点回入口，营业报表进不去', async ({ page }) => {
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
  await page.keyboard.press('Escape')
  await page.getByTestId('parent-crumb').click()
  await expect(page).toHaveURL('/reports')

  await page.goto('/reports/business')
  await expect(page).toHaveURL('/forbidden')
})

test('店长看项目报表：今天做的项目在表里，合计等于各行之和，只看服务时合计是服务各行相加，只看卖品时它不在', async ({ page }) => {
  const { itemName } = await seedCheckout()
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.goto('/reports')
  await page.getByTestId('report-list').getByRole('link', { name: /项目报表/ }).click()
  await expect(page).toHaveURL(/\/reports\/items\?from=/)

  const table = page.getByTestId('item-table')
  const row = table.locator(`tbody tr[data-item="${itemName}"]`)
  await expect(row.locator('td[data-col="times"]')).toHaveText('1')
  await expect(row.locator('td[data-col="paid"]')).toHaveText('¥66')
  await expectTotalsAreSums(table, 'item-totals', 8)
  await expectBriefIsTotal(table, 'item-totals', 'paid')

  // 只看服务：合计改成服务各行在前端相加，这个服务项目还在。
  const kinds = page.getByRole('radiogroup', { name: '项目类型' })
  await kinds.getByRole('radio', { name: '服务' }).click()
  await expect(row.locator('td[data-col="paid"]')).toHaveText('¥66')
  await expect(table.locator('tbody tr', { has: page.getByText('卖品', { exact: true }) })).toHaveCount(0)
  await expectTotalsAreSums(table, 'item-totals', 8)
  await expectBriefIsTotal(table, 'item-totals', 'paid')

  // 只看卖品：服务项目不在。
  await kinds.getByRole('radio', { name: '卖品' }).click()
  await expect(row).toHaveCount(0)
})

test('店长看卡项报表：今天开的卡记在它的卡类型下，合计等于各行之和', async ({ page }) => {
  const { token, suffix, customerId, stylist } = await seedCheckout()
  const typeName = `报表卡${suffix}`
  const type = await api<{ id: string }>(token, 'create_card_type', { name: typeName, kind: 'stored_value', validity_kind: 'none' })
  await api(token, 'open_card', { customer_id: customerId, card_type_id: type.id, principal: '300.00', bonus: '30.00', sales_staff_ids: [stylist.id], payments: cash('300.00') })
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.goto('/reports/cards')

  // 每种卡一行一根条，右边写还剩多少；逐列明细折叠着，对账时展开。
  await expect(page.getByTestId('card-rows').locator(`[data-card-row="${typeName}"]`)).toContainText('剩 ¥330')
  await page.getByTestId('card-details').locator('summary').click()
  const table = page.getByTestId('card-table')
  const row = table.locator(`tbody tr[data-card-type="${typeName}"]`)
  await expect(row.locator('td[data-col="open_count"]')).toHaveText('1')
  await expect(row.locator('td[data-col="open_principal"]')).toHaveText('¥300')
  await expect(row.locator('td[data-col="open_bonus"]')).toHaveText('¥30')
  await expect(row.locator('td[data-col="ending_balance"]')).toHaveText('¥330')
  await expectTotalsAreSums(table, 'card-totals', 14)
  await expectBriefIsTotal(table, 'card-totals', 'ending_balance')
})

test('店长看新客来源：今天第一次结账的顾客算新客；新客来源和欠款对账的合计都等于各行之和', async ({ page }) => {
  const { token, customerId, itemId, stylist } = await seedCheckout()
  // 再结一单挂账，欠款对账就一定有没还清的欠款。
  const owed = await api<{ id: string, net: string }>(token, 'open_order', {
    customer_id: customerId, gender: 'male', designated: false, items: [{ item_id: itemId, staff: [{ slot: 1, staff_id: stylist.id }] }]
  })
  await api(token, 'checkout_orders', { orders: [{ order_id: owed.id, payments: [{ method: 'debt', amount: owed.net }] }] })
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.goto('/reports/sources')
  const table = page.getByTestId('source-table')
  // 用例造的顾客都没填来源。
  await expect(table.locator('tbody tr[data-source="未填来源"] td[data-col="new_customers"]')).not.toHaveText('0')
  await expectTotalsAreSums(table, 'source-totals', 3)
  await expectBriefIsTotal(table, 'source-totals', 'new_customers')

  await page.goto('/reports/debts')
  // 这笔挂账在「还没还的」里，点进去是那位顾客。
  const owedRow = page.getByTestId('debt-row').and(page.locator(`[href="/customers/${customerId}"]`))
  await expect(owedRow.locator('[data-col="outstanding"]')).toHaveText(yuan(owed.net))
  await expect(owedRow).toContainText('结账挂账')
  await page.getByTestId('debt-details').locator('summary').click()
  const debts = page.getByTestId('debt-table')
  await expect(debts).toBeVisible()
  await expectTotalsAreSums(debts, 'debt-totals', 3)
  await expectBriefIsTotal(debts, 'debt-totals', 'outstanding')
})

test('发型师打不开只给店长看的报表', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  for (const path of ['/reports/items', '/reports/cards', '/reports/sources', '/reports/debts']) {
    await page.goto(path)
    await expect(page).toHaveURL('/forbidden')
  }
})

test('报表读不出来时原地一块红框写哪里没读出来，点「重试」重新读', async ({ page }) => {
  let fail = true
  await page.route('**/api/ops/get_business_report', route => fail ? route.abort() : route.continue())
  await login(page, owner.account, owner.password)
  await page.goto('/reports/business')
  const error = page.getByTestId('load-error')
  await expect(error).toContainText('报表没有读出来')
  fail = false
  await error.getByRole('button', { name: '重试', exact: true }).click()
  await expect(error).toHaveCount(0)
  await expect(page.getByTestId('report-brief')).toBeVisible()
})
