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
  await expect(page.getByTestId('range-trigger')).toContainText('本月')
  await page.getByTestId('range-trigger').click()
  await expect(page.getByRole('group', { name: '快捷范围' }).getByRole('button', { name: '本月' })).toHaveAttribute('aria-pressed', 'true')
  await page.keyboard.press('Escape')
  // ‹ 翻到上个整月，› 翻回本月；本月再往后没有数，› 不能点。
  await expect(page.getByTestId('range-later')).toBeDisabled()
  const [year, month] = today.split('-').map(Number)
  const lastMonth = month === 1 ? `${year - 1}-12` : `${year}-${String(month - 1).padStart(2, '0')}`
  await page.getByTestId('range-earlier').click()
  await expect(page).toHaveURL(new RegExp(`from=${lastMonth}-01`))
  await expect(page.getByTestId('range-later')).toBeEnabled()
  await page.getByTestId('range-later').click()
  await expect(page.getByTestId('range-trigger')).toContainText('本月')
  await expect(page.getByTestId('range-later')).toBeDisabled()
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
  await page.getByRole('group', { name: '按日或按月' }).getByRole('button', { name: '按月', exact: true }).click()
  await expect(page).toHaveURL(/g=month/)
  await expect(table.locator(`tbody tr[data-period="${today.slice(0, 7)}"]`)).toBeVisible()
  await expectTotalsAreSums(table, 'business-totals', 5, col => col === 'guest_total')

  // 按日超过 92 天：不查，旁边写为什么。
  await page.getByRole('group', { name: '按日或按月' }).getByRole('button', { name: '按日', exact: true }).click()
  await page.getByTestId('range-trigger').click()
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
  await expect(page.getByRole('group', { name: '按日或按月' })).toHaveCount(0)

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
  await detail.getByRole('group', { name: '明细类别' }).getByRole('button', { name: '提成', exact: true }).click()
  await expect(detail.getByText(/提成合计/)).toBeVisible()

  // 超过 92 天：不查，旁边写为什么（不提示切按月）。
  await page.keyboard.press('Escape')
  await expect(detail).toHaveCount(0)
  await page.getByTestId('range-trigger').click()
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
  const kinds = page.getByRole('group', { name: '项目类型' })
  await kinds.getByRole('button', { name: '服务', exact: true }).click()
  await expect(row.locator('td[data-col="paid"]')).toHaveText('¥66')
  await expect(table.locator('tbody tr', { has: page.getByText('卖品', { exact: true }) })).toHaveCount(0)
  await expectTotalsAreSums(table, 'item-totals', 8)
  await expectBriefIsTotal(table, 'item-totals', 'paid')

  // 只看卖品：服务项目不在。
  await kinds.getByRole('button', { name: '卖品', exact: true }).click()
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

// 储值余额表里的数换成分：零是长横，负数前面是减号（U+2212）。
function signedAmount(text: string) {
  const t = text.trim()
  return t.startsWith('−') ? -amount(t.slice(1)) : amount(t)
}

test('店长看储值余额：今天开的储值卡算进充入，每天期末等于期初加进出，本金和赠送分开看加起来等于合起来', async ({ page }) => {
  const { token, suffix, customerId, stylist, today } = await seedCheckout()
  const type = await api<{ id: string }>(token, 'create_card_type', { name: `储值报表卡${suffix}`, kind: 'stored_value', validity_kind: 'none' })
  await api(token, 'open_card', { customer_id: customerId, card_type_id: type.id, principal: '300.00', bonus: '30.00', sales_staff_ids: [stylist.id], payments: cash('300.00') })
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.goto('/reports/stored-value')
  const table = page.getByTestId('stored-value-table')
  const todayRow = table.locator(`tbody tr[data-day="${today}"]`)
  expect(signedAmount(await todayRow.locator('td[data-col="added"]').textContent() ?? '')).toBeGreaterThanOrEqual(33000)

  // 有进出的每一天：期末 = 期初 + 充入 + 升级补差 − 耗卡 − 退卡 + 调整。
  const rows = table.locator('tbody tr')
  for (let i = 0; i < await rows.count(); i++) {
    const row = rows.nth(i)
    if (await row.locator('td[data-col="added"]').count() === 0) continue
    const col = async (key: string) => {
      const cell = row.locator(`td[data-col="${key}"]`)
      return await cell.count() ? signedAmount(await cell.textContent() ?? '') : 0
    }
    const expected = await col('opening') + await col('added') + await col('upgrade') - await col('consumed') - await col('refunded') + await col('adjusted')
    expect(await col('closing'), await row.getAttribute('data-day') ?? '').toBe(expected)
  }

  // 合计行：充入、耗卡是各天之和，期初是最早那天的期初，期末是今天的期末，也是顶上一句话里的数。
  const totals = table.getByTestId('stored-value-totals')
  const total = async (key: string) => signedAmount(await totals.locator(`td[data-col="${key}"]`).textContent() ?? '')
  for (const key of ['added', 'consumed']) {
    const cells = await table.locator(`tbody td[data-col="${key}"]`).allTextContents()
    expect(await total(key), key).toBe(cells.reduce((s, t) => s + signedAmount(t), 0))
  }
  expect(await total('opening')).toBe(signedAmount(await rows.last().locator('td[data-col="opening"]').textContent() ?? ''))
  expect(await total('closing')).toBe(signedAmount(await todayRow.locator('td[data-col="closing"]').textContent() ?? ''))
  expect(amount(await page.getByTestId('report-brief').locator('[data-figure="closing"]').textContent() ?? '')).toBe(await total('closing'))

  const all = await total('closing')
  await page.getByTestId('stored-value-part-principal').click()
  await expect(page.getByTestId('stored-value-heading')).toContainText('只看本金')
  const principal = await total('closing')
  await page.getByTestId('stored-value-part-bonus').click()
  await expect(page.getByTestId('stored-value-heading')).toContainText('只看赠送')
  const bonus = await total('closing')
  expect(bonus).toBeGreaterThanOrEqual(3000)
  expect(principal + bonus).toBe(all)
})

test('店长看卡结余和到期：10 天后到期的次卡在 30 天内到期里、写红，剩几次、值多少；只看储值卡时它不在', async ({ page }) => {
  const { token, suffix, customerId, stylist, itemId, itemName } = await seedCheckout()
  const type = await api<{ id: string }>(token, 'create_card_type', {
    name: `到期次卡${suffix}`, kind: 'count', validity_kind: 'days_after_open', validity_days: 10,
    items: [{ item_id: itemId, paid_count: 5, gift_count: 1, unit_price: '20.00' }]
  })
  await api(token, 'open_card', { customer_id: customerId, card_type_id: type.id, principal: '0.00', bonus: '0.00', sales_staff_ids: [stylist.id], payments: cash('100.00') })
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.goto('/reports/card-balances')
  await page.getByTestId('balance-within-30').click()
  await page.getByTestId('balance-kind-count').click()

  const row = page.getByTestId('balance-row').and(page.locator(`[href="/customers/${customerId}"]`))
  // 购买 5 次 × ¥20 算价值，送的 1 次不算钱但算剩几次。
  await expect(row.locator('[data-col="remaining_value"]')).toHaveText('¥100')
  await expect(row).toContainText(`${itemName} 剩 6 次（送 1）`)
  await expect(row.locator('[data-col="expiry"]')).toContainText(/还有 \d+ 天/)
  await expect(row.locator('[data-col="expiry"]')).toHaveClass(/text-ink-red/)
  await expect(page.getByTestId('report-brief')).toContainText('30 天内到期')

  await page.getByTestId('balance-kind-stored_value').click()
  await expect(page.locator('[data-testid="balance-row"][data-kind="count"]')).toHaveCount(0)
  await expect(row).toHaveCount(0)
})

test('店长看业绩工资：本月结过账的发型师一行，项目业绩记在他名下，合计等于各行之和', async ({ page }) => {
  const { stylist } = await seedCheckout()
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.goto('/reports/wages')
  const table = page.getByTestId('wages-table')
  const row = table.locator(`tbody tr[data-staff="${stylist.name}"]`)
  await expect(row.locator('td[data-col="item"]')).toHaveText('¥66')
  await expect(row.locator('td[data-col="performance"]')).toHaveText('¥66')
  await expectTotalsAreSums(table, 'wages-totals', 2, col => col === 'ratio')
})

test('发型师打不开只给店长看的报表', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  for (const path of ['/reports/items', '/reports/cards', '/reports/sources', '/reports/debts', '/reports/stored-value', '/reports/card-balances', '/reports/wages']) {
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
