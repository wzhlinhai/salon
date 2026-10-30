import { expect, test } from '@playwright/test'
import { api, apiLogin, hire, login, navLabels, owner } from './helpers'

async function seed() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const level = await api<{ id: string, name: string }>(token, 'create_level', { name: `首席${suffix}` })
  const first = await api<{ id: string, name: string }>(token, 'create_position', { name: `大工${suffix}` })
  const second = await api<{ id: string, name: string }>(token, 'create_position', { name: `助理${suffix}` })
  const cardType = await api(token, 'create_card_type', { name: `会员${suffix}`, kind: 'stored_value', validity_kind: 'none' })
  return { token, suffix, level, first, second, cardType }
}

test('项目完整新建、编辑和停用', async ({ page }) => {
  const { token, suffix, level, first, second, cardType } = await seed()
  await login(page, owner.account, owner.password)
  await page.goto('/items')
  await page.getByRole('button', { name: '新建项目', exact: true }).click()
  const createDialog = page.getByRole('dialog', { name: '新建项目', exact: true })
  const editDialog = page.getByRole('dialog', { name: '编辑项目', exact: true })
  const name = `剪发${suffix}`
  await createDialog.getByRole('textbox', { name: '项目名称', exact: true }).fill(name)
  await expect(createDialog.getByRole('combobox', { name: '种类', exact: true })).toContainText('服务')
  await createDialog.getByRole('combobox', { name: '种类', exact: true }).click()
  await page.getByRole('option', { name: '卖品', exact: true }).click()
  await createDialog.getByRole('textbox', { name: '标准价', exact: true }).fill('1234.56')
  await createDialog.getByRole('switch', { name: '可以打折' }).click()
  await createDialog.getByRole('combobox', { name: '手工费类型' }).click()
  await page.getByRole('option', { name: '固定金额', exact: true }).click()
  await createDialog.getByRole('textbox', { name: '手工费数值' }).fill('0.50')
  for (const [index, position] of [first, second].entries()) {
    await createDialog.getByRole('button', { name: '添加岗位' }).click()
    await createDialog.getByRole('combobox', { name: `岗位${index + 1}`, exact: true }).click()
    await page.getByRole('option', { name: position.name, exact: true }).click()
  }
  await createDialog.getByTestId('position-row').first().getByRole('checkbox', { name: '必填' }).click()
  await createDialog.getByRole('button', { name: '添加级别价' }).click()
  await createDialog.getByRole('combobox', { name: '级别1', exact: true }).click()
  await page.getByRole('option', { name: level.name, exact: true }).click()
  await createDialog.getByRole('textbox', { name: '级别价1', exact: true }).fill('88.00')
  await createDialog.getByRole('button', { name: '添加会员价', exact: true }).click()
  await createDialog.getByRole('combobox', { name: '卡类型1', exact: true }).click()
  await page.getByRole('option', { name: cardType.name, exact: true }).click()
  await createDialog.getByRole('textbox', { name: '会员价1', exact: true }).fill('66.00')
  await createDialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(createDialog).not.toBeVisible()
  const card = page.locator(`[data-item="${name}"]`)
  await expect(card).toContainText('1234.56')
  await expect(card).toContainText('种类 卖品')
  await expect(card).toContainText('不打折项目')
  await card.getByRole('button', { name: '编辑' }).click()
  await expect(editDialog.getByRole('combobox', { name: '种类', exact: true })).toContainText('卖品')
  await editDialog.getByRole('combobox', { name: '种类', exact: true }).click()
  await page.getByRole('option', { name: '服务', exact: true }).click()
  await editDialog.getByRole('textbox', { name: '标准价', exact: true }).fill('99.00')
  await editDialog.getByTestId('position-row').first().getByRole('button', { name: '下移' }).click()
  await editDialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(editDialog).not.toBeVisible()
  await expect(card).toContainText('99.00')
  await expect(card).toContainText('种类 服务')
  await card.getByRole('button', { name: '编辑' }).click()
  await editDialog.getByRole('switch', { name: '启用项目' }).click()
  await editDialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(editDialog).not.toBeVisible()
  await expect(card).toContainText('停用')
  const result = await api<{ items: { name: string, positions: { position_id: string, required: boolean }[], level_prices: { level_id: string, price: string }[], member_prices: { card_type_id: string, price: string }[] }[] }>(token, 'list_items', {})
  const item = result.items.find(i => i.name === name)!
  expect(item.positions.map(p => p.position_id)).toEqual([second.id, first.id])
  expect(item.positions.map(p => p.required)).toEqual([false, true])
  expect(item.level_prices).toEqual([{ level_id: level.id, price: '88.00' }])
  expect(item.member_prices).toEqual([{ card_type_id: cardType.id, price: '66.00' }])
})

test('发型师没有项目入口，直接访问无权限', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  expect(await navLabels(page)).not.toContain('项目与价格')
  await page.goto('/items')
  await expect(page).toHaveURL('/forbidden')
})

test('项目旧保存响应不关闭后来打开的窗口', async ({ page }) => {
  const { token, suffix } = await seed()
  const body = { name: `旧项目${suffix}`, price: '88.00', discountable: true, labor_fee_amount: null, labor_fee_percent: null, positions: [], level_prices: [] }
  await api(token, 'create_item', body)
  await login(page, owner.account, owner.password)
  await page.goto('/items')
  await page.locator(`[data-item="${body.name}"]`).getByRole('button', { name: '编辑' }).click()
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/update_item', async (route) => {
    const response = await route.fetch()
    started()
    await blocked
    await route.fulfill({ response })
  })
  const editDialog = page.getByRole('dialog', { name: '编辑项目', exact: true })
  const createDialog = page.getByRole('dialog', { name: '新建项目', exact: true })
  await editDialog.getByRole('button', { name: '保存', exact: true }).click()
  await pending
  await page.keyboard.press('Escape')
  await expect(editDialog).not.toBeVisible()
  await page.getByRole('button', { name: '新建项目', exact: true }).click()
  await createDialog.getByRole('textbox', { name: '项目名称', exact: true }).fill(`下一项目${suffix}`)
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/update_item'))
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧保存没有状态变化；等待响应回调执行，确认它没有关闭新窗口。
  await page.waitForTimeout(500)
  await expect(createDialog).toBeVisible()
  await expect(createDialog.getByRole('textbox', { name: '项目名称', exact: true })).toHaveValue(`下一项目${suffix}`)
  await createDialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(createDialog).not.toBeVisible()
  await expect(page.locator(`[data-item="下一项目${suffix}"]`)).toBeVisible()
})

test('项目保存断网后结束保存状态并能重试', async ({ page }) => {
  const { suffix } = await seed()
  await login(page, owner.account, owner.password)
  await page.goto('/items')
  await page.getByRole('button', { name: '新建项目', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新建项目', exact: true })
  const name = `断网重试${suffix}`
  await dialog.getByRole('textbox', { name: '项目名称', exact: true }).fill(name)
  let abort = true
  await page.route('**/api/ops/create_item', async (route) => {
    if (abort) {
      abort = false
      await route.abort()
    } else await route.continue()
  })
  const save = dialog.getByRole('button', { name: '保存', exact: true })
  await save.click()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(save).toBeEnabled()
  await save.click()
  await expect(dialog).not.toBeVisible()
  await expect(page.locator(`[data-item="${name}"]`)).toBeVisible()
})

test('项目岗位比例留存并显示规则错误', async ({ page }) => {
  const { token, suffix, first, second } = await seed()
  await login(page, owner.account, owner.password)
  await page.goto('/items')
  await page.getByRole('button', { name: '新建项目', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新建项目', exact: true })
  const name = `岗位比例${suffix}`
  await dialog.getByLabel('项目名称', { exact: true }).fill(name)
  await dialog.getByLabel('标准价', { exact: true }).fill('100')
  for (const [index, position] of [first, second].entries()) {
    await dialog.getByRole('button', { name: '添加岗位' }).click()
    await dialog.getByRole('combobox', { name: `岗位${index + 1}`, exact: true }).click()
    await page.getByRole('option', { name: position.name, exact: true }).click()
  }
  await dialog.getByLabel('岗位1比例', { exact: true }).fill('70')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByText('岗位比例要么都不填，要么都填且合计 100%', { exact: true })).toBeVisible()
  await expect(dialog).toBeVisible()
  await dialog.getByLabel('岗位2比例', { exact: true }).fill('30')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  await page.locator(`[data-item="${name}"]`).getByRole('button', { name: '编辑' }).click()
  const edit = page.getByRole('dialog', { name: '编辑项目', exact: true })
  await expect(edit.getByLabel('岗位1比例', { exact: true })).toHaveValue('70')
  await expect(edit.getByLabel('岗位2比例', { exact: true })).toHaveValue('30')
  await edit.getByLabel('岗位1比例', { exact: true }).fill('')
  await edit.getByLabel('岗位2比例', { exact: true }).fill('')
  await edit.getByRole('button', { name: '保存', exact: true }).click()
  await expect(edit).not.toBeVisible()
  const result = await api<{ items: { name: string, positions: { share_percent: number | null }[] }[] }>(token, 'list_items', {})
  expect(result.items.find(i => i.name === name)!.positions.map(p => p.share_percent)).toEqual([null, null])
})
