import { expect, test, type Locator, type Page } from '@playwright/test'
import type { components } from '../app/utils/api'
import { api, apiLogin, hire, login, navLabels, owner } from './helpers'

type Schema = components['schemas']
type View = Schema['CommissionSchemeVersionView']
const empty: Schema['CommissionScheme'] = { rules: [], overrides: [], excluded_staff: [] }

async function seed(percent = '11.00') {
  const token = await apiLogin(owner.account, owner.password)
  const settings = await api<Schema['SettingList']>(token, 'get_settings', {})
  const list = await api<Schema['CommissionSchemeVersionList']>(token, 'list_commission_scheme_versions', { limit: 1 })
  const date = [settings.business_date, list.versions[0]?.effective_from ?? ''].sort().at(-1)!
  const scheme: Schema['CommissionScheme'] = {
    ...empty,
    rules: [{ source: 'item', base: 'paid', by_position: false, by_designated: false, by_member: false, cells: [{ percent }] }]
  }
  const version = await api<View>(token, 'save_commission_scheme', { effective_from: date, scheme })
  return { token, version, today: settings.business_date }
}

async function choose(page: Page, container: Locator, label: string, option: string) {
  await container.getByRole('combobox', { name: label, exact: true }).click()
  await page.getByRole('option', { name: option, exact: true }).click()
}

async function openEditor(page: Page) {
  await page.getByRole('button', { name: '修改方案', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '修改提成方案', exact: true })
  await expect(dialog.getByRole('button', { name: '保存', exact: true })).toBeEnabled()
  return dialog
}

async function saved(page: Page) {
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/save_commission_scheme'))
  await page.getByRole('dialog', { name: '修改提成方案', exact: true }).getByRole('button', { name: '保存', exact: true }).click()
  const result = await response
  expect(result.status()).toBe(200)
  const view = await result.json() as View
  await expect(page.getByText(`已保存版本 ${view.version}，${view.effective_from} 起执行`, { exact: true })).toBeVisible()
  await expect(page.getByRole('dialog', { name: '修改提成方案', exact: true })).toHaveCount(0)
  await expect(page.getByTestId('scheme-detail')).toContainText(`版本 ${view.version}`)
  return view
}

// API 返回的 refs 仅用于显示；这里去掉 refs，逐字段核对 UI 写入的方案。
function inputScheme(view: View): Schema['CommissionScheme'] {
  return {
    rules: view.scheme.rules.map(r => ({ ...r, cells: r.cells.map(({ position: _position, ...c }) => c) })),
    overrides: view.scheme.overrides.map(({ staff: _staff, item: _item, ...o }) => o),
    excluded_staff: view.scheme.excluded_staff.map(s => s.staff_id)
  }
}

test('老板新建方案', async ({ page }) => {
  const { token, today } = await seed()
  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`
  const first = await api<Schema['Position']>(token, 'create_position', { name: `大工${suffix}` })
  const second = await api<Schema['Position']>(token, 'create_position', { name: `助理${suffix}` })
  const staff = await api<Schema['Staff']>(token, 'create_staff', { name: `提成员工${suffix}`, number: suffix, role_ids: [] })
  await api(token, 'create_item', { name: `提成项目${suffix}`, price: '100.00', positions: [], level_prices: [], discountable: true, labor_fee_amount: null, labor_fee_percent: null })
  // 从空方案开始，避免先前运行留下的规则影响本次表单。
  const list = await api<Schema['CommissionSchemeVersionList']>(token, 'list_commission_scheme_versions', { limit: 1 })
  await api(token, 'save_commission_scheme', { effective_from: list.versions[0]!.effective_from, scheme: empty })
  await login(page, owner.account, owner.password)
  await page.goto('/commission')
  const dialog = await openEditor(page)
  const item = dialog.locator('[data-rule=item]')
  await item.getByRole('switch', { name: '按项目业绩计提成', exact: true }).click()
  await choose(page, item, '计提基数', '实收（折后价）')
  await item.getByRole('switch', { name: '按岗位', exact: true }).click()
  const firstCell = item.locator(`[data-position="${first.id}"]`)
  const secondCell = item.locator(`[data-position="${second.id}"]`)
  await choose(page, firstCell, '数值类型', '比例')
  await firstCell.getByLabel('比例', { exact: true }).fill('10.00')
  await choose(page, secondCell, '数值类型', '阶梯')
  await choose(page, secondCell, '阶梯方式', '超额累进')
  await secondCell.getByLabel('档比例', { exact: true }).nth(0).fill('10.00')
  await secondCell.getByLabel('起点', { exact: true }).nth(1).fill('10000.00')
  await secondCell.getByLabel('档比例', { exact: true }).nth(1).fill('15.00')
  const sale = dialog.locator('[data-rule=sale]')
  await sale.getByRole('switch').click()
  await choose(page, sale, '数值类型', '固定金额')
  await sale.getByLabel('固定金额', { exact: true }).fill('5.00')
  await dialog.getByRole('button', { name: '添加覆盖', exact: true }).click()
  const override = dialog.getByTestId('override-row')
  await choose(page, override, '员工', staff.name)
  await override.getByLabel('比例', { exact: true }).fill('20.00')
  await choose(page, dialog, '不计提成的员工', staff.name)
  await page.keyboard.press('Escape')
  const min = await dialog.getByLabel('生效日期').getAttribute('min')
  expect(min! >= today).toBe(true)
  await expect(dialog.getByLabel('生效日期')).toHaveValue(min!)
  const result = await saved(page)
  const top = page.locator('[data-version]').first()
  await expect(top).toHaveAttribute('data-version', String(result.version))
  await expect(top).toContainText(result.saved_by.name)
  await expect(top).toContainText(result.effective_from > today ? '已安排' : '执行中')
  const detail = page.getByTestId('scheme-detail')
  for (const text of ['实收（折后价）', first.name, second.name, '10.00%', '超额累进', '从 10000.00 元起 15.00%', '固定 5.00 元', '20.00%', staff.name]) await expect(detail).toContainText(text)
  const actual = await api<View>(token, 'get_commission_scheme', { version: result.version })
  expect(inputScheme(actual)).toEqual({
    rules: [
      { source: 'item', base: 'paid', by_position: true, by_designated: false, by_member: false, cells: [
        { position_id: first.id, percent: '10.00' },
        { position_id: second.id, tiers: { mode: 'progressive', levels: [{ from: '0.00', percent: '10.00' }, { from: '10000.00', percent: '15.00' }] } }
      ] },
      { source: 'sale', base: 'performance', by_position: false, by_designated: false, by_member: false, cells: [{ fixed_amount: '5.00' }] }
    ], overrides: [{ source: 'item', staff_id: staff.id, percent: '20.00' }], excluded_staff: [staff.id]
  })
})

test('安排下一版并查看历史', async ({ page }) => {
  const { version, today } = await seed('12.00')
  await login(page, owner.account, owner.password)
  await page.goto('/commission')
  // 选中的旧版与最新版本不同，编辑仍应读取最新版本。
  const dialog = await openEditor(page)
  const percent = dialog.locator('[data-rule=item]').getByLabel('比例', { exact: true })
  await expect(percent).toHaveValue('12.00')
  const expectedMin = version.effective_from > today ? version.effective_from : today
  await expect(dialog.getByLabel('生效日期')).toHaveAttribute('min', expectedMin)
  const tomorrow = new Date(Date.parse(expectedMin + 'T00:00:00Z') + 86400000).toISOString().slice(0, 10)
  await dialog.getByLabel('生效日期').fill(tomorrow)
  await percent.fill('18.00')
  const next = await saved(page)
  await expect(page.locator(`[data-version="${next.version}"]`)).toContainText('已安排')
  await page.getByRole('button', { name: `版本 ${version.version}`, exact: true }).click()
  await expect(page.getByTestId('scheme-detail')).toContainText('12.00%')
  await expect(page.getByTestId('scheme-detail')).not.toContainText('18.00%')
  const latestEditor = await openEditor(page)
  await expect(latestEditor.getByLabel('生效日期')).toHaveAttribute('min', tomorrow)
  await expect(latestEditor.getByLabel('生效日期')).toHaveValue(tomorrow)
  await expect(latestEditor.locator('[data-rule=item]').getByLabel('比例', { exact: true })).toHaveValue('18.00')
})

test('服务器拒绝时显示提示', async ({ page }) => {
  const { token, version } = await seed()
  await login(page, owner.account, owner.password)
  await page.goto('/commission')
  const dialog = await openEditor(page)
  const item = dialog.locator('[data-rule=item]')
  await choose(page, item, '数值类型', '阶梯')
  const levels = item.getByTestId('tier-level')
  await expect(levels.first().getByLabel('起点', { exact: true })).toBeDisabled()
  await expect(levels.first().getByRole('button', { name: '删除', exact: true })).toHaveCount(0)
  await expect(levels.nth(1).getByRole('button', { name: '删除', exact: true })).toBeVisible()
  await item.getByLabel('档比例').nth(0).fill('10.00')
  await item.getByLabel('起点', { exact: true }).nth(1).fill('0.00')
  await item.getByLabel('档比例').nth(1).fill('15.00')
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/save_commission_scheme'))
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  expect((await response).status()).toBe(409)
  await expect(page.getByText('提成规则阶梯起点必须严格递增：项目业绩', { exact: true })).toBeVisible()
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('button', { name: '保存', exact: true })).toBeEnabled()
  await expect(page.locator('[data-version]').first()).toHaveAttribute('data-version', String(version.version))
  const list = await api<Schema['CommissionSchemeVersionList']>(token, 'list_commission_scheme_versions', { limit: 1 })
  expect(list.versions[0]!.version).toBe(version.version)
})

test('店长看不到提成规则', async ({ page }) => {
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  expect(await navLabels(page)).not.toContain('提成规则')
  await page.goto('/commission')
  await expect(page).toHaveURL('/forbidden')
})

test('提成版本迟到的旧详情不覆盖当前版本', async ({ page }) => {
  const { version: a } = await seed('21.00')
  const { version: b } = await seed('32.00')
  await login(page, owner.account, owner.password)
  await page.goto('/commission')
  await expect(page.getByTestId('scheme-detail')).toBeVisible()
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/get_commission_scheme', async (route) => {
    if (route.request().postDataJSON().version === a.version) {
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await page.getByRole('button', { name: `版本 ${a.version}`, exact: true }).click()
  await pending
  await page.getByRole('button', { name: `版本 ${b.version}`, exact: true }).click()
  await expect(page.getByTestId('scheme-detail')).toContainText('32.00%')
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/get_commission_scheme') && r.request().postDataJSON().version === a.version)
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧响应没有可观察变化；等回调执行后核对当前详情。
  await page.waitForTimeout(500)
  await expect(page.getByTestId('scheme-detail')).toContainText(`版本 ${b.version}`)
  await expect(page.getByTestId('scheme-detail')).toContainText('32.00%')
  await expect(page.getByTestId('scheme-detail')).not.toContainText('21.00%')
})

test('历史岗位和离职员工在编辑后保留', async ({ page }) => {
  const { token, version } = await seed()
  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`
  const position = await api<Schema['Position']>(token, 'create_position', { name: `历史岗位${suffix}` })
  const staff = await api<Schema['Staff']>(token, 'create_staff', { name: `离职员工${suffix}`, number: suffix, role_ids: [] })
  const scheme: Schema['CommissionScheme'] = {
    rules: [{ source: 'item', base: 'paid', by_position: true, by_designated: true, by_member: true, cells: [{ position_id: position.id, designated: true, member: false, percent: '13.00' }] }],
    overrides: [], excluded_staff: [staff.id]
  }
  const old = await api<View>(token, 'save_commission_scheme', { effective_from: version.effective_from, scheme })
  await api(token, 'update_position', { ...position, active: false })
  await api(token, 'deactivate_staff', { staff_id: staff.id })
  await login(page, owner.account, owner.password)
  // 某岗位已不在选项里时，编辑仍使用版本视图带回的名称和格子。
  await page.route('**/api/ops/list_positions', async (route) => {
    const response = await route.fetch()
    const data = await response.json() as Schema['PositionList']
    await route.fulfill({ response, json: { items: data.items.filter(p => p.id !== position.id) } })
  })
  await page.goto('/commission')
  await page.getByRole('button', { name: `版本 ${old.version}`, exact: true }).click()
  const detail = page.getByTestId('scheme-detail')
  await expect(detail).toContainText(position.name)
  await expect(detail).toContainText('指定')
  await expect(detail).toContainText('散客')
  const dialog = await openEditor(page)
  const cell = dialog.locator(`[data-position="${position.id}"]`).filter({ has: page.getByLabel('比例', { exact: true }) })
  await expect(cell).toContainText(`${position.name} · 指定 · 散客`)
  await expect(cell.getByLabel('比例', { exact: true })).toHaveValue('13.00')
  await expect(dialog.getByRole('combobox', { name: '不计提成的员工', exact: true })).toContainText(`${staff.name}（离职）`)
  const next = await saved(page)
  expect(inputScheme(await api<View>(token, 'get_commission_scheme', { version: next.version }))).toEqual(scheme)
})

test('提成方案加载中的自动选择不覆盖手动选择', async ({ page }) => {
  const { version: clicked, today } = await seed('21.00')
  await seed('32.00')
  await login(page, owner.account, owner.password)
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/get_commission_scheme', async (route) => {
    if (route.request().postDataJSON().date) {
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await page.goto('/commission')
  await pending
  await expect(page.getByText(`保存后从生效日期起执行，之前写入的业绩仍按当时的方案版本算；阶梯提成随当月累计变化，月底锁定后才固定。今天是营业日 ${today}。`, { exact: true })).toBeVisible()
  await page.getByRole('button', { name: `版本 ${clicked.version}`, exact: true }).click()
  const detail = page.getByTestId('scheme-detail')
  await expect(detail).toContainText('21.00%')
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/get_commission_scheme') && !!r.request().postDataJSON().date)
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 保留手动选择没有状态变化，等迟到的自动选择回调执行后核对详情。
  await page.waitForTimeout(500)
  await expect(detail).toContainText(`版本 ${clicked.version}`)
  await expect(detail).toContainText('21.00%')
})

test('关闭来源规则时移除该来源的覆盖比例', async ({ page }) => {
  const { token, version } = await seed()
  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`
  const staff = await api<Schema['Staff']>(token, 'create_staff', { name: `覆盖员工${suffix}`, number: suffix, role_ids: [] })
  const scheme = inputScheme(version)
  scheme.rules.push({ source: 'consume', base: 'performance', by_position: false, by_designated: false, by_member: false, cells: [{ percent: '10.00' }] })
  scheme.overrides.push({ source: 'consume', staff_id: staff.id, percent: '20.00' })
  await api(token, 'save_commission_scheme', { effective_from: version.effective_from, scheme })
  await login(page, owner.account, owner.password)
  await page.goto('/commission')
  const dialog = await openEditor(page)
  await expect(dialog.getByTestId('override-row')).toContainText('耗卡业绩')
  await dialog.getByRole('switch', { name: '按耗卡业绩计提成', exact: true }).click()
  const next = await saved(page)
  const actual = inputScheme(await api<View>(token, 'get_commission_scheme', { version: next.version }))
  expect(actual.rules.some(r => r.source === 'consume')).toBe(false)
  expect(actual.overrides.some(o => o.source === 'consume')).toBe(false)
  expect(actual.rules).toEqual(inputScheme(version).rules)
})
