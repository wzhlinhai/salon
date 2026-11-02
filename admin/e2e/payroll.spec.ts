import { readFileSync } from 'node:fs'
import { expect, test, type Page, type Locator } from '@playwright/test'
import type { components } from '../app/utils/api'
import { api, apiLogin, hire, login, navLabels, owner, uniqueSuffix } from './helpers'
import preparePayroll from './payroll.setup'

type Schema = components['schemas']

async function prepare() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const staff = await api<Schema['Staff']>(token, 'create_staff', { name: `工资${suffix}`, number: suffix, role_ids: [] })
  const settings = await api<{ business_date: string }>(token, 'get_settings', {})
  return { token, staff, today: settings.business_date, month: settings.business_date.slice(0, 7) }
}

function label(month: string) {
  const [y, m] = month.split('-')
  return `${y}年${Number(m)}月`
}

async function choose(page: Page, scope: Page | Locator, field: string, value: string) {
  await scope.getByRole('combobox', { name: field, exact: true }).click()
  await page.getByRole('option', { name: value, exact: true }).click()
}

function row(page: Page, name: string) {
  return page.locator('main tbody tr').filter({ has: page.getByRole('cell', { name, exact: true, includeHidden: true }) })
}

async function adjustment(page: Page, staff: string, name: string, amount: string) {
  await page.getByRole('button', { name: '添加加减项', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '添加加减项', exact: true })
  await choose(page, dialog, '员工', staff)
  await dialog.getByLabel('名称', { exact: true }).fill(name)
  await dialog.getByLabel('金额', { exact: true }).fill(amount)
  await dialog.getByLabel('备注', { exact: true }).fill('工资测试备注')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
}

async function assertTotal(page: Page) {
  const values = await page.locator('main tbody tr').evaluateAll(rows => rows.map(r => r.querySelector('td:last-child')?.textContent?.trim() ?? ''))
  const total = values.pop()
  const cents = (value: string) => {
    const negative = value.startsWith('-')
    const [yuan, fen] = value.replace(/^-/, '').split('.')
    const amount = Number(yuan) * 100 + Number(fen)
    return negative ? -amount : amount
  }
  expect(cents(total!)).toBe(values.reduce((sum, value) => sum + cents(value), 0))
}

test('老板设置底薪和加减项', async ({ page }) => {
  const { staff, month } = await prepare()
  await login(page, owner.account, owner.password)
  await page.goto('/payroll')
  await page.getByRole('button', { name: '设置底薪', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '设置底薪', exact: true })
  await choose(page, dialog, '员工', staff.name)
  await expect(dialog).toContainText('当前：未设置')
  await expect(dialog).toContainText(`从 ${label(month)} 起执行`)
  await dialog.getByRole('radio', { name: '保底', exact: true }).click()
  await dialog.getByLabel('金额', { exact: true }).fill('3000')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await adjustment(page, staff.name, '奖励', '200')
  await adjustment(page, staff.name, '更正', '-50')
  const staffRow = row(page, staff.name)
  await expect(staffRow.locator('td').nth(1)).toHaveText('保底')
  await expect(staffRow.locator('td').nth(2)).toHaveText('3000.00')
  await expect(staffRow.locator('td').nth(3)).toHaveText('0.00')
  await expect(staffRow.locator('td').nth(9)).toHaveText('3000.00')
  await expect(staffRow.locator('td').nth(10)).toHaveText('150.00')
  await expect(staffRow.locator('td').last()).toHaveText('3150.00')
  await assertTotal(page)
  await staffRow.click()
  const entry = page.getByTestId('payroll-adjustment').filter({ hasText: '更正' })
  await expect(entry).toContainText('-50.00')
  await entry.getByRole('button', { name: '删除', exact: true }).click()
  const confirmation = page.getByRole('dialog', { name: '删除加减项「更正」？', exact: true })
  await expect(confirmation).toBeVisible()
  await confirmation.getByRole('button', { name: '确认删除', exact: true }).click()
  await expect(entry).toHaveCount(0)
  await expect(staffRow.locator('td').last()).toHaveText('3200.00')
  await assertTotal(page)
})

test('工资表保存断网和拒绝后可以重试', async ({ page }) => {
  const { staff } = await prepare()
  await login(page, owner.account, owner.password)
  await page.goto('/payroll')
  await page.getByRole('button', { name: '添加加减项', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '添加加减项', exact: true })
  await choose(page, dialog, '员工', staff.name)
  await dialog.getByLabel('名称', { exact: true }).fill('重试')
  await dialog.getByLabel('金额', { exact: true }).fill('0')
  const save = dialog.getByRole('button', { name: '保存', exact: true })
  await save.click()
  await expect(page.getByText('加减项金额不能为 0', { exact: true })).toBeVisible()
  await expect(save).toBeEnabled()
  await dialog.getByLabel('金额', { exact: true }).fill('1')
  await page.route('**/api/ops/add_payroll_adjustment', route => route.abort(), { times: 1 })
  await save.click()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(save).toBeEnabled()
  await save.click()
  await expect(dialog).toHaveCount(0)
  await expect(row(page, staff.name).locator('td').last()).toHaveText('1.00')
})

test('发型师没有工资表权限', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  expect(await navLabels(page)).not.toContain('工资表')
  await page.goto('/payroll')
  await expect(page).toHaveURL('/forbidden')
})

test('工资明细迟到的旧响应不覆盖当前员工', async ({ page }) => {
  const { token, staff: a, month } = await prepare()
  const { staff: b } = await prepare()
  for (const [staff, name] of [[a, '甲专属'], [b, '乙专属']] as const) {
    await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name, amount: '1' })
  }
  await login(page, owner.account, owner.password)
  await page.goto('/payroll')
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/list_payroll_adjustments', async (route) => {
    if (route.request().postDataJSON().staff_id === a.id) {
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await row(page, a.name).click()
  await pending
  await row(page, b.name).click()
  await expect(page.getByTestId('payroll-detail')).toContainText('乙专属')
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/list_payroll_adjustments') && r.request().postDataJSON().staff_id === a.id)
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 旧响应丢弃没有可观察变化，等待回调执行。
  await page.waitForTimeout(500)
  await expect(page.getByTestId('payroll-detail')).toContainText('乙专属')
  await expect(page.getByTestId('payroll-detail')).not.toContainText('甲专属')
})

// 修改提成方案和锁月都会改变全店数据，等默认项目读取提成规则结束后运行。
test.describe('工资表全店数据 @payroll-store', () => {
  test.describe.configure({ mode: 'serial' })
  test('重复全局准备沿用已有样本', async () => {
    const path = new URL('../../.dev/payroll-e2e.json', import.meta.url)
    const before = readFileSync(path, 'utf8')
    await expect(preparePayroll()).resolves.toBeUndefined()
    expect(readFileSync(path, 'utf8')).toBe(before)
  })

  test('提成明细', async ({ page }) => {
    const { staff, today, version } = JSON.parse(readFileSync(new URL('../../.dev/payroll-e2e.json', import.meta.url), 'utf8')) as { staff: Schema['Staff'], today: string, version: Schema['CommissionSchemeVersionView'] }
    await login(page, owner.account, owner.password)
    await page.goto('/payroll')
    const staffRow = row(page, staff.name)
    await expect(staffRow.locator('td').nth(4)).toHaveText('10.00')
    await staffRow.click()
    await expect(page.getByTestId('payroll-commission')).toContainText(`${today} · 项目业绩 · 10.00`)
    await expect(page.getByTestId('payroll-commission')).toContainText(`实收（折后价） 100.00 · 10.00% · 方案第 ${version.version} 版`)
    await expect(page.getByTestId('commission-totals')).toContainText('项目业绩 10.00')
  })

  // 此用例需要 scripts/dev.sh reset 的全新数据库，上个月必须尚未锁定。
  test('锁定上个月', async ({ page }) => {
    const { token, staff, month } = await prepare()
    await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name: '本月更正', amount: '2' })
    const [y, m] = month.split('-').map(Number)
    const previous = new Date(Date.UTC(y!, m! - 2, 1)).toISOString().slice(0, 7)
    await login(page, owner.account, owner.password)
    await page.goto('/payroll')
    await expect(page.getByRole('button', { name: '锁定本月', exact: true })).toBeDisabled()
    await expect(page.getByText('月份结束且之前的月份都已锁定后才能锁定', { exact: true })).toBeVisible()
    await choose(page, page, '结算月', label(previous))
    await adjustment(page, staff.name, '上月更正', '123')
    const total = await row(page, '合计').locator('td').last().innerText()
    await page.getByRole('button', { name: '锁定本月', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: `锁定 ${label(previous)}`, exact: true })
    await expect(dialog).toContainText(`全店应发 ${total}`)
    await expect(dialog).toContainText('锁定不能撤销')
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await expect(page.getByText('未锁定', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: '锁定本月', exact: true }).click()
    const oldError = await deferOperation(page, 'lock_payroll_month', { failure: '旧锁月失败' })
    await dialog.getByRole('button', { name: '确认锁定', exact: true }).click()
    await oldError.pending
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await choose(page, page, '结算月', label(month))
    await expect(row(page, staff.name).locator('td').last()).toHaveText('2.00')
    await oldError.finish()
    await discardedResponse(page)
    expect(await page.getByText('旧锁月失败', { exact: true }).count()).toBe(0)
    await choose(page, page, '结算月', label(previous))
    await page.getByRole('button', { name: '锁定本月', exact: true }).click()
    await page.route('**/api/ops/lock_payroll_month', route => route.fulfill({ status: 409, contentType: 'application/problem+json', json: { detail: '锁月暂时拒绝' } }), { times: 1 })
    await dialog.getByRole('button', { name: '确认锁定', exact: true }).click()
    await expect(page.getByText('锁月暂时拒绝', { exact: true })).toBeVisible()
    await expect(dialog.getByRole('button', { name: '确认锁定', exact: true })).toBeEnabled()
    const oldSuccess = await deferOperation(page, 'lock_payroll_month')
    await dialog.getByRole('button', { name: '确认锁定', exact: true }).click()
    await oldSuccess.pending
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await choose(page, page, '结算月', label(month))
    await expect(row(page, staff.name).locator('td').last()).toHaveText('2.00')
    const salary = await salaryEditor(page, staff.name, '88')
    const refreshed = page.waitForResponse(r => r.url().endsWith('/api/ops/list_payroll_months'))
    await oldSuccess.finish()
    await (await refreshed).finished()
    await discardedResponse(page)
    await expect(salary).toBeVisible()
    await expect(page.getByText('未锁定', { exact: true })).toBeVisible()
    await expect(row(page, staff.name).locator('td').last()).toHaveText('2.00')
    expect(await page.getByText('已锁定', { exact: true }).count()).toBe(0)
    await salary.getByRole('button', { name: 'Close', exact: true }).click()
    await choose(page, page, '结算月', `${label(previous)}（已锁定）`)
    await expect(page.getByText(/已锁定 · 王老板 ·/)).toBeVisible()
    for (const name of ['设置底薪', '添加加减项', '锁定本月']) await expect(page.getByRole('button', { name, exact: true })).toHaveCount(0)
    await row(page, staff.name).click()
    await expect(page.getByTestId('payroll-adjustment')).toContainText('上月更正')
    await expect(page.getByTestId('payroll-detail').getByRole('button', { name: '删除', exact: true })).toHaveCount(0)
  })
})

test('工资表月份切走再切回丢弃旧工资表', async ({ page }) => {
  const { token, staff, month } = await prepare()
  await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name: '初始', amount: '1' })
  await login(page, owner.account, owner.password)
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  let first = true
  await page.route('**/api/ops/get_payroll', async (route) => {
    if (first) {
      first = false
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await page.goto('/payroll')
  await pending
  const months = await api<Schema['PayrollMonthList']>(token, 'list_payroll_months', {})
  const previous = months.entries[1]!
  await choose(page, page, '结算月', label(previous.month))
  await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name: '更新', amount: '2' })
  await choose(page, page, '结算月', label(month))
  await expect(row(page, staff.name).locator('td').last()).toHaveText('3.00')
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/get_payroll') && r.request().postDataJSON().month === month)
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧响应没有状态变化，等待回调执行。
  await page.waitForTimeout(500)
  await expect(row(page, staff.name).locator('td').last()).toHaveText('3.00')
})

async function salaryEditor(page: Page, staffName: string, amount = '77.77') {
  await page.getByRole('button', { name: '设置底薪', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '设置底薪', exact: true })
  await choose(page, dialog, '员工', staffName)
  await dialog.getByLabel('金额', { exact: true }).fill(amount)
  return dialog
}

async function deferOperation(page: Page, op: string, options: { failure?: string, beforeSend?: boolean } = {}) {
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route(`**/api/ops/${op}`, async (route) => {
    const response = options.failure || options.beforeSend ? undefined : await route.fetch()
    started()
    await blocked
    if (options.failure) {
      await route.fulfill({ status: 409, contentType: 'application/problem+json', json: { detail: options.failure } })
    } else await route.fulfill({ response: response ?? await route.fetch() })
  }, { times: 1 })
  return {
    pending,
    async finish() {
      const response = page.waitForResponse(r => r.url().endsWith(`/api/ops/${op}`))
      release()
      await (await response).finished()
    }
  }
}

async function discardedResponse(page: Page) {
  // eslint-disable-next-line no-restricted-syntax -- 与提成、顾客用例一致：丢弃响应没有可观察变化，finished 不能确认页面回调已执行。
  await page.waitForTimeout(500)
}

test('上月底薪迟到保存刷新当前月', async ({ page }) => {
  const { token, staff, month } = await prepare()
  await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name: '本月', amount: '0.10' })
  const months = await api<Schema['PayrollMonthList']>(token, 'list_payroll_months', {})
  await login(page, owner.account, owner.password)
  await page.goto('/payroll')
  await choose(page, page, '结算月', label(months.entries[1]!.month))
  const dialog = await salaryEditor(page, staff.name, '1234.56')
  // 先暂缓转发，让本月先读到旧底薪；释放后走真实保存，验证回包确实触发本月刷新。
  const delayed = await deferOperation(page, 'set_base_salary', { beforeSend: true })
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await delayed.pending
  await dialog.getByRole('button', { name: 'Close', exact: true }).click()
  await choose(page, page, '结算月', label(month))
  await expect(row(page, staff.name).locator('td').nth(2)).toHaveText('0.00')
  await delayed.finish()
  await expect(row(page, staff.name).locator('td').nth(2)).toHaveText('1234.56')
  await expect(row(page, staff.name).locator('td').last()).toHaveText('1234.66')
  await assertTotal(page)
})

for (const fails of [false, true]) {
  test(`底薪旧保存${fails ? '失败' : '成功'}不影响新弹窗`, async ({ page }) => {
    const { staff: a } = await prepare()
    const { staff: b } = await prepare()
    await login(page, owner.account, owner.password)
    await page.goto('/payroll')
    let dialog = await salaryEditor(page, a.name)
    const delayed = await deferOperation(page, 'set_base_salary', { failure: fails ? '旧底薪保存失败' : undefined })
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await delayed.pending
    await dialog.getByRole('button', { name: 'Close', exact: true }).click()
    dialog = await salaryEditor(page, b.name, '88.88')
    const refreshed = fails ? undefined : page.waitForResponse(r => r.url().endsWith('/api/ops/get_payroll'))
    await delayed.finish()
    if (fails) await discardedResponse(page)
    else {
      await (await refreshed!).finished()
      await expect(row(page, a.name).locator('td').last()).toHaveText('77.77')
    }
    await expect(dialog).toBeVisible()
    await expect(dialog.getByLabel('金额', { exact: true })).toHaveValue('88.88')
    await expect(dialog.getByRole('button', { name: '保存', exact: true })).toBeEnabled()
    expect(await page.getByText(fails ? '旧底薪保存失败' : '已保存', { exact: true }).count()).toBe(0)
  })
}

test('工资加载断网后结束加载并可换月重试', async ({ page }) => {
  const { token, staff, month } = await prepare()
  await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name: '加载', amount: '0.20' })
  const months = await api<Schema['PayrollMonthList']>(token, 'list_payroll_months', {})
  await login(page, owner.account, owner.password)
  await page.route('**/api/ops/get_payroll', route => route.abort(), { times: 1 })
  await page.goto('/payroll')
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(page.getByTestId('payroll-table')).toHaveAttribute('aria-busy', 'false')
  await choose(page, page, '结算月', label(months.entries[1]!.month))
  await choose(page, page, '结算月', label(month))
  await expect(row(page, staff.name).locator('td').last()).toHaveText('0.20')
  await expect(page.getByTestId('payroll-table')).toHaveAttribute('aria-busy', 'false')
})

test('工资旧请求结束不能清除新月份加载状态', async ({ page }) => {
  const { token, month } = await prepare()
  const months = await api<Schema['PayrollMonthList']>(token, 'list_payroll_months', {})
  const previous = months.entries[1]!.month
  await login(page, owner.account, owner.password)
  let releaseFirst!: () => void
  let releaseSecond!: () => void
  let startFirst!: () => void
  let startSecond!: () => void
  const first = new Promise<void>((resolve) => {
    releaseFirst = resolve
  })
  const second = new Promise<void>((resolve) => {
    releaseSecond = resolve
  })
  const firstStarted = new Promise<void>((resolve) => {
    startFirst = resolve
  })
  const secondStarted = new Promise<void>((resolve) => {
    startSecond = resolve
  })
  await page.route('**/api/ops/get_payroll', async (route) => {
    if (route.request().postDataJSON().month === month) {
      startFirst()
      await first
    } else {
      startSecond()
      await second
    }
    await route.fulfill({ response: await route.fetch() })
  })
  await page.goto('/payroll')
  await firstStarted
  await choose(page, page, '结算月', label(previous))
  await secondStarted
  await expect(page.getByTestId('payroll-table')).toHaveAttribute('aria-busy', 'true')
  const oldResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/get_payroll') && r.request().postDataJSON().month === month)
  releaseFirst()
  await (await oldResponse).finished()
  await discardedResponse(page)
  await expect(page.getByTestId('payroll-table')).toHaveAttribute('aria-busy', 'true')
  const newResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/get_payroll') && r.request().postDataJSON().month === previous)
  releaseSecond()
  await (await newResponse).finished()
  await expect(page.getByTestId('payroll-table')).toHaveAttribute('aria-busy', 'false')
})

test('删除加减项拒绝和断网后可重试且空备注没有前导分隔符', async ({ page }) => {
  const { token, staff, month } = await prepare()
  await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name: '无备注', amount: '0.30' })
  await login(page, owner.account, owner.password)
  await page.goto('/payroll')
  await row(page, staff.name).click()
  const entry = page.getByTestId('payroll-adjustment').filter({ hasText: '无备注' })
  await expect(entry.locator('p.text-sm')).toHaveText(/^王老板 · /)
  await entry.getByRole('button', { name: '删除', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '删除加减项「无备注」？', exact: true })
  const button = dialog.getByRole('button', { name: '确认删除', exact: true })
  await page.route('**/api/ops/delete_payroll_adjustment', route => route.fulfill({ status: 409, contentType: 'application/problem+json', json: { detail: '删除暂时拒绝' } }), { times: 1 })
  await button.click()
  await expect(page.getByText('删除暂时拒绝', { exact: true })).toBeVisible()
  await expect(button).toBeEnabled()
  await page.route('**/api/ops/delete_payroll_adjustment', route => route.abort(), { times: 1 })
  await button.click()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(button).toBeEnabled()
  await button.click()
  await expect(dialog).toHaveCount(0)
  await expect(entry).toHaveCount(0)
})

for (const fails of [false, true]) {
  test(`加减项旧删除${fails ? '失败' : '成功'}不影响新确认窗口`, async ({ page }) => {
    const { token, staff, month } = await prepare()
    for (const name of ['删除甲', '删除乙']) await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name, amount: '1' })
    await login(page, owner.account, owner.password)
    await page.goto('/payroll')
    await row(page, staff.name).click()
    const entry = (name: string) => page.getByTestId('payroll-adjustment').filter({ hasText: name })
    await entry('删除甲').getByRole('button', { name: '删除', exact: true }).click()
    const a = page.getByRole('dialog', { name: '删除加减项「删除甲」？', exact: true })
    const delayed = await deferOperation(page, 'delete_payroll_adjustment', { failure: fails ? '旧删除失败' : undefined })
    await a.getByRole('button', { name: '确认删除', exact: true }).click()
    await delayed.pending
    await a.getByRole('button', { name: '取消', exact: true }).click()
    await entry('删除乙').getByRole('button', { name: '删除', exact: true }).click()
    const b = page.getByRole('dialog', { name: '删除加减项「删除乙」？', exact: true })
    await delayed.finish()
    if (fails) await discardedResponse(page)
    else await expect(entry('删除甲')).toHaveCount(0)
    await expect(b).toBeVisible()
    await expect(b.getByRole('button', { name: '确认删除', exact: true })).toBeEnabled()
    expect(await page.getByText(fails ? '旧删除失败' : '已删除', { exact: true }).count()).toBe(0)
  })
}

test('加减项迟到保存刷新匹配的当前明细', async ({ page }) => {
  const { token, staff, month } = await prepare()
  await api(token, 'add_payroll_adjustment', { month, staff_id: staff.id, name: '原有', amount: '1' })
  await login(page, owner.account, owner.password)
  await page.goto('/payroll')
  await page.getByRole('button', { name: '添加加减项', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '添加加减项', exact: true })
  await choose(page, dialog, '员工', staff.name)
  await dialog.getByLabel('名称', { exact: true }).fill('迟到加项')
  await dialog.getByLabel('金额', { exact: true }).fill('0.10')
  const delayed = await deferOperation(page, 'add_payroll_adjustment', { beforeSend: true })
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await delayed.pending
  await dialog.getByRole('button', { name: 'Close', exact: true }).click()
  await row(page, staff.name).click()
  await expect(page.getByTestId('payroll-detail')).toContainText('原有')
  await expect(page.getByTestId('payroll-detail')).not.toContainText('迟到加项')
  await delayed.finish()
  await expect(page.getByTestId('payroll-detail')).toContainText('迟到加项 +0.10')
  await expect(row(page, staff.name).locator('td').last()).toHaveText('1.10')
})

test('月份列表迟到错误不提示到其他页面', async ({ page }) => {
  await login(page, owner.account, owner.password)
  const delayed = await deferOperation(page, 'list_payroll_months', { failure: '旧月份列表失败' })
  await page.goto('/payroll')
  await delayed.pending
  await page.locator('aside nav a[href="/staff"]').click()
  await expect(page).toHaveURL('/staff')
  await expect(page.getByRole('button', { name: '新建员工', exact: true })).toBeVisible()
  await delayed.finish()
  await discardedResponse(page)
  expect(await page.getByText('旧月份列表失败', { exact: true }).count()).toBe(0)
})
