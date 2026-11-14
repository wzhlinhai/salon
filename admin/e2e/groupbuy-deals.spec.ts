import { expect, test } from '@playwright/test'
import { api, apiLogin, login, owner, uniqueSuffix } from './helpers'

async function seed() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const position = await api<{ id: string }>(token, 'create_position', { name: `团购岗位${suffix}` })
  const item = await api<{ id: string, name: string }>(token, 'create_item', {
    name: `团购项目${suffix}`,
    price: '298.00',
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    positions: [{ position_id: position.id, required: false }],
    level_prices: []
  })
  return { token, suffix, item }
}

test('新建和编辑团购商品，平台创建后不可修改', async ({ page }) => {
  const { token, suffix, item } = await seed()
  const name = `团购券${suffix}`
  const renamed = `${name}改名`
  await login(page, owner.account, owner.password)
  await page.goto('/groupbuy-deals')
  await page.getByRole('button', { name: '新建团购商品', exact: true }).click()
  const createDialog = page.getByRole('dialog', { name: '新建团购商品', exact: true })
  await createDialog.getByRole('combobox', { name: '平台' }).click()
  await page.getByRole('option', { name: '抖音', exact: true }).click()
  await createDialog.getByLabel('商品名称').fill(name)
  await createDialog.getByLabel('平台购买价').fill('99.00')
  await createDialog.getByRole('combobox', { name: '对应门店项目' }).click()
  await page.getByRole('option', { name: new RegExp(item.name) }).click()
  await createDialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(createDialog).not.toBeVisible()
  const card = page.locator(`[data-deal="${name}"]`)
  await expect(card).toContainText('抖音')
  await expect(card).toContainText('99.00')
  await expect(card).toContainText(item.name)

  await card.getByRole('button', { name: '编辑' }).click()
  const editDialog = page.getByRole('dialog', { name: '编辑团购商品', exact: true })
  await expect(editDialog).toBeVisible()
  await expect(editDialog).toContainText('抖音')
  await expect(editDialog.getByRole('combobox', { name: '平台' })).toHaveCount(0)
  await editDialog.getByLabel('商品名称').fill(renamed)
  await editDialog.getByLabel('平台购买价').fill('120.00')
  await editDialog.getByRole('switch', { name: '启用' }).click()
  await editDialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(editDialog).not.toBeVisible()
  const updated = page.locator(`[data-deal="${renamed}"]`)
  await expect(updated).toContainText('120.00')
  await expect(updated).toContainText('停用')
  const deals = await api<{ items: { name: string, platform: string, purchase_price: string, active: boolean }[] }>(token, 'list_groupbuy_deals', {})
  const saved = deals.items.find(d => d.name === renamed)!
  expect(saved.platform).toBe('douyin')
  expect(saved.purchase_price).toBe('120.00')
  expect(saved.active).toBe(false)
})
