import { expect, test } from '@playwright/test'
import { api, apiLogin, hire, login, owner } from './helpers'

async function seed() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const customer = (await api(token, 'create_customer', { name: `红包甲${suffix}` })).customer
  const staff = await api(token, 'create_staff', { name: `红包业绩甲${suffix}`, number: `ra${suffix}`, role_ids: [] })
  const second = await api(token, 'create_staff', { name: `红包业绩乙${suffix}`, number: `rb${suffix}`, role_ids: [] })
  return { token, suffix, customer, staff, second }
}

test('发放卖和送的红包、两名业绩归属人、撤回原因', async ({ page }) => {
  const { customer, staff, second } = await seed()
  await login(page, owner.account, owner.password)
  await page.goto(`/customers?id=${customer.id}`)
  const section = page.getByTestId('red-packets')
  await section.getByRole('button', { name: '发放红包', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '发放红包', exact: true })
  await dialog.getByLabel('面额', { exact: true }).fill('200.00')
  await dialog.getByLabel('售价', { exact: true }).fill('150.00')
  await dialog.getByLabel('有效期', { exact: true }).fill('2999-01-01')
  await dialog.getByRole('combobox', { name: '业绩归属人', exact: true }).click()
  await page.getByRole('option', { name: staff.name, exact: true }).click()
  await page.getByRole('option', { name: second.name, exact: true }).click()
  await page.keyboard.press('Escape')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  await expect(section).toContainText('面额 200.00')
  await expect(section).toContainText('售价 150.00')
  await expect(section).toContainText(staff.name)
  await expect(section).toContainText(second.name)
  await section.getByRole('button', { name: '发放红包', exact: true }).click()
  await dialog.getByRole('combobox', { name: '卖或送', exact: true }).click()
  await page.getByRole('option', { name: '送', exact: true }).click()
  await dialog.getByLabel('面额', { exact: true }).fill('30.00')
  await dialog.getByLabel('有效期', { exact: true }).fill('2999-01-01')
  await expect(dialog.getByLabel('售价', { exact: true })).toHaveCount(0)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  await expect(section.locator('[data-red-packet]')).toHaveCount(2)
  const gift = section.locator('[data-red-packet]').filter({ hasText: '面额 30.00' })
  await gift.getByRole('button', { name: '撤回', exact: true }).click()
  const revoking = page.getByRole('dialog', { name: '撤回红包', exact: true })
  await expect(revoking.getByRole('button', { name: '确认撤回', exact: true })).toBeDisabled()
  await revoking.getByLabel('撤回原因', { exact: true }).fill('发错顾客')
  await revoking.getByRole('button', { name: '确认撤回', exact: true }).click()
  await expect(revoking).not.toBeVisible()
  await expect(gift).toContainText('已撤回')
  await expect(gift).toContainText('发错顾客')
})

test('无红包发放和撤回权限隐藏按钮', async ({ page }) => {
  const { token, customer } = await seed()
  await api(token, 'issue_red_packet', { customer_id: customer.id, kind: 'gift', face_value: '25.00', expires_on: '2999-01-01' })
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await page.goto(`/customers?id=${customer.id}`)
  const section = page.getByTestId('red-packets')
  await expect(section).toContainText('面额 25.00')
  await expect(section.getByRole('button', { name: '发放红包', exact: true })).toHaveCount(0)
  await expect(section.getByRole('button', { name: '撤回', exact: true })).toHaveCount(0)
})

test('切换顾客丢弃迟到的红包列表并支持切回同一顾客', async ({ page }) => {
  const { token, suffix, customer: a } = await seed()
  const b = (await api(token, 'create_customer', { name: `红包乙${suffix}` })).customer
  const packet = await api(token, 'issue_red_packet', { customer_id: a.id, kind: 'gift', face_value: '123.45', expires_on: '2999-01-01' })
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  let first = true
  await page.route('**/api/ops/list_customer_red_packets', async (route) => {
    if (route.request().postDataJSON().customer_id === a.id && first) {
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
    await pending
    await page.evaluate((id) => {
      window.history.pushState({}, '', `/customers?id=${id}`)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, b.id)
    const section = page.getByTestId('red-packets')
    await expect(section).toContainText('暂无红包')
    await api(token, 'revoke_red_packet', { red_packet_id: packet.id, reason: '切回前撤回' })
    await page.evaluate((id) => {
      window.history.pushState({}, '', `/customers?id=${id}`)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, a.id)
    await expect(section).toContainText('切回前撤回')
    const late = page.waitForResponse(r => r.url().endsWith('/api/ops/list_customer_red_packets') && r.request().postDataJSON().customer_id === a.id)
    release()
    await (await late).finished()
    // eslint-disable-next-line no-restricted-syntax -- 丢弃响应无状态变化，等待回调后确认第二次列表未被覆盖。
    await page.waitForTimeout(500)
    await expect(section).toContainText('已撤回')
    await expect(section).toContainText('切回前撤回')
  } finally {
    release()
  }
})

test('同一顾客发放刷新丢弃首次迟到的红包列表', async ({ page }) => {
  const { customer } = await seed()
  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  let first = true
  await page.route('**/api/ops/list_customer_red_packets', async (route) => {
    if (route.request().postDataJSON().customer_id === customer.id && first) {
      first = false
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  try {
    await login(page, owner.account, owner.password)
    await page.goto(`/customers?id=${customer.id}`)
    await pending
    const section = page.getByTestId('red-packets')
    await section.getByRole('button', { name: '发放红包', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '发放红包', exact: true })
    await dialog.getByRole('combobox', { name: '卖或送', exact: true }).click()
    await page.getByRole('option', { name: '送', exact: true }).click()
    await dialog.getByLabel('面额', { exact: true }).fill('77.77')
    await dialog.getByLabel('有效期', { exact: true }).fill('2999-01-01')
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    await expect(section).toContainText('77.77')
    const late = page.waitForResponse(r => r.url().endsWith('/api/ops/list_customer_red_packets') && r.request().postDataJSON().customer_id === customer.id)
    release()
    await (await late).finished()
    // eslint-disable-next-line no-restricted-syntax -- 旧响应丢弃后没有状态变化，等待回调执行。
    await page.waitForTimeout(500)
    await expect(section).toContainText('77.77')
  } finally {
    release()
  }
})

for (const action of ['issue', 'revoke'] as const) {
  test(`红包${action}旧保存响应不关闭后来打开的窗口`, async ({ page }) => {
    const { token, customer } = await seed()
    const a = await api(token, 'issue_red_packet', { customer_id: customer.id, kind: 'gift', face_value: '11', expires_on: '2999-01-01' })
    const b = await api(token, 'issue_red_packet', { customer_id: customer.id, kind: 'gift', face_value: '22', expires_on: '2999-01-01' })
    let release!: () => void
    let started!: () => void
    const blocked = new Promise<void>((resolve) => {
      release = resolve
    })
    const pending = new Promise<void>((resolve) => {
      started = resolve
    })
    const op = action === 'issue' ? 'issue_red_packet' : 'revoke_red_packet'
    await page.route(`**/api/ops/${op}`, async (route) => {
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    })
    try {
      await login(page, owner.account, owner.password)
      await page.goto(`/customers?id=${customer.id}`)
      const section = page.getByTestId('red-packets')
      const dialog = page.getByRole('dialog', { name: action === 'issue' ? '发放红包' : '撤回红包', exact: true })
      if (action === 'issue') {
        await section.getByRole('button', { name: '发放红包', exact: true }).click()
        await dialog.getByRole('combobox', { name: '卖或送', exact: true }).click()
        await page.getByRole('option', { name: '送', exact: true }).click()
        await dialog.getByLabel('面额', { exact: true }).fill('33')
        await dialog.getByLabel('有效期', { exact: true }).fill('2999-01-01')
        await dialog.getByRole('button', { name: '保存', exact: true }).click()
      } else {
        await section.locator(`[data-red-packet="${a.id}"]`).getByRole('button', { name: '撤回', exact: true }).click()
        await dialog.getByLabel('撤回原因', { exact: true }).fill('第一次')
        await dialog.getByRole('button', { name: '确认撤回', exact: true }).click()
      }
      await pending
      await dialog.getByRole('button', { name: 'Close', exact: true }).click()
      if (action === 'issue') {
        await section.getByRole('button', { name: '发放红包', exact: true }).click()
        await dialog.getByLabel('面额', { exact: true }).fill('44')
      } else {
        await section.locator(`[data-red-packet="${b.id}"]`).getByRole('button', { name: '撤回', exact: true }).click()
        await dialog.getByLabel('撤回原因', { exact: true }).fill('第二次')
      }
      const late = page.waitForResponse(r => r.url().endsWith(`/api/ops/${op}`))
      release()
      await (await late).finished()
      // eslint-disable-next-line no-restricted-syntax -- 旧保存被丢弃没有状态变化，等待回调执行后断言。
      await page.waitForTimeout(500)
      await expect(dialog).toBeVisible()
      await expect(dialog.getByLabel(action === 'issue' ? '面额' : '撤回原因', { exact: true })).toHaveValue(action === 'issue' ? '44' : '第二次')
    } finally {
      release()
    }
  })

  test(`红包${action}保存断网后可重试`, async ({ page }) => {
    const { token, customer } = await seed()
    const packet = await api(token, 'issue_red_packet', { customer_id: customer.id, kind: 'gift', face_value: '11', expires_on: '2999-01-01' })
    const op = action === 'issue' ? 'issue_red_packet' : 'revoke_red_packet'
    let failed = false
    await page.route(`**/api/ops/${op}`, async (route) => {
      if (!failed) {
        failed = true
        await route.abort('failed')
      } else await route.continue()
    })
    await login(page, owner.account, owner.password)
    await page.goto(`/customers?id=${customer.id}`)
    const section = page.getByTestId('red-packets')
    const dialog = page.getByRole('dialog', { name: action === 'issue' ? '发放红包' : '撤回红包', exact: true })
    if (action === 'issue') {
      await section.getByRole('button', { name: '发放红包', exact: true }).click()
      await dialog.getByRole('combobox', { name: '卖或送', exact: true }).click()
      await page.getByRole('option', { name: '送', exact: true }).click()
      await dialog.getByLabel('面额', { exact: true }).fill('33')
      await dialog.getByLabel('有效期', { exact: true }).fill('2999-01-01')
    } else {
      await section.locator(`[data-red-packet="${packet.id}"]`).getByRole('button', { name: '撤回', exact: true }).click()
      await dialog.getByLabel('撤回原因', { exact: true }).fill('发错了')
    }
    const save = dialog.getByRole('button', { name: action === 'issue' ? '保存' : '确认撤回', exact: true })
    await save.click()
    await expect(save).toBeEnabled()
    await save.click()
    await expect(dialog).not.toBeVisible()
    await expect(section).toContainText(action === 'issue' ? '33.00' : '发错了')
  })
}
