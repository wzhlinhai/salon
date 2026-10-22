import { expect, test } from '@playwright/test'
import { api, apiLogin, hire, login, owner } from './helpers'

async function seed() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const customer = (await api(token, 'create_customer', { name: `卡顾客${suffix}` })).customer
  const staff = await api(token, 'create_staff', { name: `业绩甲${suffix}`, number: `a${suffix}`, role_ids: [] })
  const second = await api(token, 'create_staff', { name: `业绩乙${suffix}`, number: `b${suffix}`, role_ids: [] })
  return { token, suffix, customer, staff, second }
}

test('卡类型有效期、两名业绩归属人开卡、充值及流水', async ({ page }) => {
  const { suffix, customer, staff, second } = await seed()
  await login(page, owner.account, owner.password)
  await page.goto('/card-types')
  await page.getByRole('button', { name: '新建卡类型', exact: true }).click()
  const typeDialog = page.getByRole('dialog', { name: '新建卡类型', exact: true })
  const name = `储值${suffix}`
  await typeDialog.getByLabel('卡类型名称', { exact: true }).fill(name)
  await typeDialog.getByRole('combobox', { name: '有效期', exact: true }).click()
  await page.getByRole('option', { name: '开卡后 N 天', exact: true }).click()
  await typeDialog.getByLabel('天数', { exact: true }).fill('30')
  await typeDialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(typeDialog).not.toBeVisible()
  await expect(page.locator(`[data-card-type="${name}"]`)).toContainText('开卡后 30 天')
  await page.goto(`/customers?id=${customer.id}`)
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await detail.getByRole('button', { name: '开卡', exact: true }).click()
  const opening = page.getByRole('dialog', { name: '开卡', exact: true })
  await opening.getByRole('combobox', { name: '卡类型', exact: true }).click()
  await page.getByRole('option', { name, exact: true }).click()
  await opening.getByLabel('本金', { exact: true }).fill('1234.56')
  await opening.getByLabel('赠送金', { exact: true }).fill('0.50')
  await opening.getByRole('combobox', { name: '业绩归属人', exact: true }).click()
  await page.getByRole('option', { name: staff.name, exact: true }).click()
  await page.getByRole('option', { name: second.name, exact: true }).click()
  await page.keyboard.press('Escape')
  await opening.getByRole('button', { name: '保存', exact: true }).click()
  await expect(opening).not.toBeVisible()
  await expect(detail).toContainText('本金 1234.56')
  await expect(detail).toContainText('赠送金 0.50')
  await detail.getByRole('button', { name: '充值', exact: true }).click()
  const recharge = page.getByRole('dialog', { name: '充值', exact: true })
  await recharge.getByLabel('本金', { exact: true }).fill('0.50')
  await recharge.getByLabel('赠送金', { exact: true }).fill('10.00')
  await recharge.getByRole('combobox', { name: '业绩归属人', exact: true }).click()
  await page.getByRole('option', { name: staff.name, exact: true }).click()
  await page.keyboard.press('Escape')
  await recharge.getByRole('button', { name: '保存', exact: true }).click()
  await expect(recharge).not.toBeVisible()
  await expect(detail).toContainText('本金 1235.06')
  await expect(detail).toContainText('赠送金 10.50')
  await detail.getByRole('button', { name: '流水', exact: true }).click()
  await expect(detail.getByTestId('card-ledger-entry')).toHaveCount(2)
  await expect(detail.getByTestId('card-ledger-entry').last()).toContainText(staff.name)
  await expect(detail.getByTestId('card-ledger-entry').last()).toContainText(second.name)
  await expect(detail.getByTestId('card-ledger-entry').first()).toContainText('1234.56 + 0.50 = 1235.06')
})

test('切换顾客后丢弃迟到的卡列表', async ({ page }) => {
  const { token, suffix, customer: a } = await seed()
  const b = (await api(token, 'create_customer', { name: `卡乙${suffix}` })).customer
  const ct = await api(token, 'create_card_type', { name: `甲专有卡${suffix}`, kind: 'stored_value', validity_kind: 'none' })
  const me = await api(token, 'get_me', {})
  await api(token, 'open_card', { customer_id: a.id, card_type_id: ct.id, principal: '99.00', bonus: '0.00', sales_staff_ids: [me.staff.id] })
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/list_customer_cards', async (route) => {
    const response = await route.fetch()
    if (route.request().postDataJSON().customer_id === a.id) {
      started()
      await blocked
    }
    await route.fulfill({ response })
  })
  await login(page, owner.account, owner.password)
  await page.goto(`/customers?id=${a.id}`)
  await pending
  await page.evaluate((id) => {
    window.history.pushState({}, '', `/customers?id=${id}`)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, b.id)
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await expect(detail.locator('dd').filter({ hasText: b.name })).toBeVisible()
  await expect(detail).toContainText('暂无卡')
  const late = page.waitForResponse(r => r.url().endsWith('/api/ops/list_customer_cards') && r.request().postDataJSON().customer_id === a.id)
  release()
  await (await late).finished()
  // eslint-disable-next-line no-restricted-syntax -- 被丢弃的响应没有状态变化，等待组件回调执行后断言。
  await page.waitForTimeout(500)
  await expect(detail).toContainText('暂无卡')
  await expect(detail).not.toContainText(ct.name)
})

test('无开卡权限不显示开卡和充值按钮', async ({ page }) => {
  const { token, suffix, customer } = await seed()
  const ct = await api(token, 'create_card_type', { name: `只读卡${suffix}`, kind: 'stored_value', validity_kind: 'none' })
  const me = await api(token, 'get_me', {})
  await api(token, 'open_card', { customer_id: customer.id, card_type_id: ct.id, principal: '1.00', bonus: '0.00', sales_staff_ids: [me.staff.id] })
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await page.goto(`/customers?id=${customer.id}`)
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await expect(detail).toContainText(ct.name)
  await expect(detail.getByRole('button', { name: '开卡', exact: true })).toHaveCount(0)
  await expect(detail.getByRole('button', { name: '充值', exact: true })).toHaveCount(0)
  await expect(detail.getByRole('button', { name: '调整', exact: true })).toHaveCount(0)
})

test('切回同一顾客后丢弃第一次卡列表，保留第二次响应', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const a = (await api(token, 'create_customer', { name: `回切卡甲${suffix}` })).customer
  const b = (await api(token, 'create_customer', { name: `回切卡乙${suffix}` })).customer
  const ct = await api(token, 'create_card_type', { name: `回切卡${suffix}`, kind: 'stored_value', validity_kind: 'none' })
  const me = await api(token, 'get_me', {})
  const card = await api(token, 'open_card', { customer_id: a.id, card_type_id: ct.id, principal: '99.00', bonus: '0.00', sales_staff_ids: [me.staff.id] })
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  let firstA = true
  await page.route('**/api/ops/list_customer_cards', async (route) => {
    if (route.request().postDataJSON().customer_id === a.id && firstA) {
      firstA = false
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else {
      await route.continue()
    }
  })
  try {
    await login(page, owner.account, owner.password)
    await page.goto(`/customers?id=${a.id}`)
    await pending
    await page.evaluate((id) => {
      window.history.pushState({}, '', `/customers?id=${id}`)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, b.id)
    const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
    await expect(detail.locator('dd').filter({ hasText: b.name })).toBeVisible()
    await expect(detail).toContainText('暂无卡')
    await api(token, 'recharge_card', { card_id: card.id, principal: '24.56', bonus: '0.50', sales_staff_ids: [me.staff.id] })
    await page.evaluate((id) => {
      window.history.pushState({}, '', `/customers?id=${id}`)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, a.id)
    await expect(detail.locator('dd').filter({ hasText: a.name })).toBeVisible()
    const shownCard = detail.locator(`[data-card="${card.id}"]`)
    await expect(shownCard).toContainText('本金 123.56')
    await expect(shownCard).toContainText('赠送金 0.50')
    const late = page.waitForResponse(r => r.url().endsWith('/api/ops/list_customer_cards') && r.request().postDataJSON().customer_id === a.id)
    release()
    await (await late).finished()
    // eslint-disable-next-line no-restricted-syntax -- 同一顾客的旧响应被丢弃没有状态变化，等待组件回调后确认仍显示第二次响应。
    await page.waitForTimeout(500)
    await expect(shownCard).toContainText('本金 123.56')
    await expect(shownCard).toContainText('赠送金 0.50')
    await expect(shownCard).not.toContainText('本金 99.00')
  } finally {
    release()
  }
})

test('调整余额、记录原因并清除有效期', async ({ page }) => {
  const { token, suffix, customer } = await seed()
  const ct = await api(token, 'create_card_type', { name: `调整卡${suffix}`, kind: 'stored_value', validity_kind: 'fixed_date', validity_date: '2030-01-01' })
  const me = await api(token, 'get_me', {})
  const card = await api(token, 'open_card', { customer_id: customer.id, card_type_id: ct.id, principal: '100.00', bonus: '0.50', sales_staff_ids: [me.staff.id] })
  await login(page, owner.account, owner.password)
  await page.goto(`/customers?id=${customer.id}`)
  const shown = page.locator(`[data-card="${card.id}"]`)
  await shown.getByRole('button', { name: '调整', exact: true }).click()
  const modal = page.getByRole('dialog', { name: '调整', exact: true })
  await expect(modal.getByLabel('本金', { exact: true })).toHaveValue('100.00')
  await modal.getByLabel('本金', { exact: true }).fill('80.00')
  await modal.getByLabel('原因', { exact: true }).fill('核对原始收款')
  await modal.getByRole('button', { name: '保存', exact: true }).click()
  await expect(modal).not.toBeVisible()
  await shown.getByRole('button', { name: '流水', exact: true }).click()
  await expect(shown.getByTestId('card-ledger-entry').first()).toContainText('100.00 + -20.00 = 80.00')
  await expect(shown.getByTestId('card-ledger-entry').first()).toContainText('核对原始收款')
  await shown.getByRole('button', { name: '调整记录', exact: true }).click()
  await expect(shown.getByTestId('card-adjustment-entry')).toContainText('核对原始收款')
  await shown.getByRole('button', { name: '调整', exact: true }).click()
  await modal.getByRole('checkbox', { name: '不限', exact: true }).check()
  await modal.getByLabel('原因', { exact: true }).fill('原卡不限期')
  let submitted: Record<string, unknown> = {}
  await page.route('**/api/ops/adjust_card', async (route) => {
    submitted = route.request().postDataJSON()
    await route.continue()
  })
  await modal.getByRole('button', { name: '保存', exact: true }).click()
  await expect(modal).not.toBeVisible()
  await expect(shown).toContainText('有效期 不限')
  expect(submitted).toEqual({ card_id: card.id, reason: '原卡不限期', expiry: { expires_on: null } })
  await expect(shown.getByTestId('card-adjustment-entry').first()).toContainText('2030-01-01 → 不限')
})

test('调整记录切走再切回同一卡丢弃旧响应', async ({ page }) => {
  const { token, suffix, customer: a } = await seed()
  const b = (await api(token, 'create_customer', { name: `调整乙${suffix}` })).customer
  const ct = await api(token, 'create_card_type', { name: `回切调整${suffix}`, kind: 'stored_value', validity_kind: 'none' })
  const me = await api(token, 'get_me', {})
  const card = await api(token, 'open_card', { customer_id: a.id, card_type_id: ct.id, principal: '100.00', bonus: '0.00', sales_staff_ids: [me.staff.id] })
  await api(token, 'adjust_card', { card_id: card.id, principal: '90.00', reason: '第一次更正' })
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  let first = true
  await page.route('**/api/ops/list_card_adjustments', async (route) => {
    if (route.request().postDataJSON().card_id === card.id && first) {
      first = false
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  try {
    await login(page, owner.account, owner.password)
    await page.goto(`/customers?id=${a.id}`)
    const shown = page.locator(`[data-card="${card.id}"]`)
    await shown.getByRole('button', { name: '调整记录', exact: true }).click()
    await pending
    await page.evaluate((id) => {
      window.history.pushState({}, '', `/customers?id=${id}`)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, b.id)
    const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
    await expect(detail.locator('dd').filter({ hasText: b.name })).toBeVisible()
    await api(token, 'adjust_card', { card_id: card.id, principal: '80.00', reason: '第二次更正' })
    await page.evaluate((id) => {
      window.history.pushState({}, '', `/customers?id=${id}`)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, a.id)
    await shown.getByRole('button', { name: '调整记录', exact: true }).click()
    await expect(shown.getByTestId('card-adjustment-entry')).toHaveCount(2)
    await expect(shown.getByTestId('card-adjustment-entry').first()).toContainText('第二次更正')
    const late = page.waitForResponse(r => r.url().endsWith('/api/ops/list_card_adjustments') && r.request().postDataJSON().card_id === card.id)
    release()
    await (await late).finished()
    // eslint-disable-next-line no-restricted-syntax -- 旧响应被丢弃没有状态变化，等待组件回调后断言第二次结果。
    await page.waitForTimeout(500)
    await expect(shown.getByTestId('card-adjustment-entry')).toHaveCount(2)
    await expect(shown.getByTestId('card-adjustment-entry').first()).toContainText('第二次更正')
  } finally {
    release()
  }
})
