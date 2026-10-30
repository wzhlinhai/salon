import { expect, test } from '@playwright/test'
import { api, apiLogin, hire, login, owner } from './helpers'

// #27：老板修改设置并指定生效日期，页面显示已安排的修改和修改记录。
test('老板安排明天起的营业日结束时间，并在修改记录里看到', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/settings')
  const card = page.locator('[data-setting=business_day_end]')
  await card.getByRole('button', { name: '修改' }).click()

  const tomorrow = await page.locator('input[type=date]').getAttribute('min')
    .then(d => new Date(Date.parse(d! + 'T00:00:00Z') + 86400000).toISOString().slice(0, 10))
  await page.getByLabel('时间', { exact: true }).fill('02:00')
  await page.getByLabel('生效日期').fill(tomorrow)
  await page.getByRole('button', { name: '保存', exact: true }).click()

  await expect(page.getByText(`${tomorrow} 起改为 02:00`)).toBeVisible()
  const latest = page.locator('table tbody tr').first()
  await expect(latest).toContainText('营业日结束时间')
  await expect(latest).toContainText('02:00')
  await expect(latest).toContainText(tomorrow)
})

test('店长能看到设置和系统默认做法，但不能修改', async ({ page }) => {
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  await page.goto('/settings')
  const card = page.locator('[data-setting=business_hours]')
  await expect(card).toContainText('营业时段')
  await expect(card.getByRole('button', { name: '修改' })).toHaveCount(0)
})

test('设置页迟到的旧 get_settings 不覆盖新结果', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/settings')
  const card = page.locator('[data-setting=business_day_end]')
  await expect(card).toBeVisible()
  let releaseOld!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseOld = resolve
  })
  let startedOld!: () => void
  const started = new Promise<void>((resolve) => {
    startedOld = resolve
  })
  let requests = 0
  await page.route('**/api/ops/get_settings', async (route) => {
    if (++requests === 1) {
      const response = await route.fetch()
      startedOld()
      await blocked
      await route.fulfill({ response })
    } else {
      await route.continue()
    }
  })
  await card.getByRole('button', { name: '修改', exact: true }).click()
  const tomorrow = await page.locator('input[type=date]').getAttribute('min')
    .then(d => new Date(Date.parse(d! + 'T00:00:00Z') + 86400000).toISOString().slice(0, 10))
  await page.getByLabel('时间', { exact: true }).fill('01:15')
  await page.getByLabel('生效日期').fill(tomorrow)
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await started
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await card.getByRole('button', { name: '修改', exact: true }).click()
  await page.getByLabel('时间', { exact: true }).fill('02:15')
  await page.getByLabel('生效日期').fill(tomorrow)
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(card).toContainText(`${tomorrow} 起改为 02:15`)
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/get_settings'))
  releaseOld()
  await (await lateResponse).finished()
  // eslint-disable-next-line no-restricted-syntax -- 旧设置被丢弃后没有可观察的状态变化，响应接收完成也不能确认页面回调已执行。
  await page.waitForTimeout(500)
  await expect(card).toContainText(`${tomorrow} 起改为 02:15`)
  await expect(card).not.toContainText(`${tomorrow} 起改为 01:15`)
})

test('设置页顾客来源迟到的旧列表不覆盖第二次保存', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const seed = await api(token, 'create_customer_source', { name: `来源种子${Date.now()}` })
  await login(page, owner.account, owner.password)
  await page.goto('/settings')
  const card = page.locator('[data-setting=customer_sources]')
  await expect(card.getByRole('textbox', { name: `顾客来源 ${seed.name}`, exact: true })).toHaveValue(seed.name)
  let releaseOld!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseOld = resolve
  })
  let startedOld!: () => void
  const started = new Promise<void>((resolve) => {
    startedOld = resolve
  })
  let requests = 0
  await page.route('**/api/ops/list_customer_sources', async (route) => {
    if (++requests === 1) {
      const response = await route.fetch()
      startedOld()
      await blocked
      await route.fulfill({ response })
    } else {
      await route.continue()
    }
  })
  const first = `第一次${Date.now()}`
  const second = `${first}第二次`

  async function save(name: string) {
    await card.getByLabel('新顾客来源', { exact: true }).fill(name)
    await card.getByRole('button', { name: '添加', exact: true }).click()
    await expect(card.getByLabel('新顾客来源', { exact: true })).toHaveValue('')
  }

  await save(first)
  await started
  await save(second)
  await expect(card.getByRole('textbox', { name: `顾客来源 ${second}`, exact: true })).toHaveValue(second)
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/list_customer_sources'))
  releaseOld()
  await (await lateResponse).finished()
  // eslint-disable-next-line no-restricted-syntax -- 旧列表被丢弃后没有可观察的状态变化，响应接收完成也不能确认页面回调已执行。
  await page.waitForTimeout(500)
  await expect(card.getByRole('textbox', { name: `顾客来源 ${second}`, exact: true })).toHaveValue(second)
})

test('设置旧保存只刷新列表，不关闭另一个设置窗口', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/settings')
  const card = page.locator('[data-setting=business_day_end]')
  await card.getByRole('button', { name: '修改', exact: true }).click()
  const tomorrow = await page.locator('input[type=date]').getAttribute('min')
    .then(d => new Date(Date.parse(d! + 'T00:00:00Z') + 86400000).toISOString().slice(0, 10))
  let release!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  let started!: () => void
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/change_setting', async (route) => {
    const response = await route.fetch()
    started()
    await blocked
    await route.fulfill({ response })
  })
  await page.getByLabel('时间', { exact: true }).fill('03:45')
  await page.getByLabel('生效日期', { exact: true }).fill(tomorrow)
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await pending
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.locator('[data-setting=business_hours]').getByRole('button', { name: '修改', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '修改营业时段', exact: true })
  await expect(dialog.getByLabel('开门', { exact: true })).toBeVisible()
  const open = await dialog.getByLabel('开门', { exact: true }).inputValue()
  release()
  await expect(card).toContainText(`${tomorrow} 起改为 03:45`)
  await expect(dialog).toBeVisible()
  await expect(dialog.getByLabel('开门', { exact: true })).toHaveValue(open)
  await expect(page.getByText(`已保存，${tomorrow} 起执行`, { exact: true })).toHaveCount(0)
})

test('修改会员价取价和充值积分', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/settings')
  const choice = page.locator('[data-setting=member_price_rule]')
  await choice.getByRole('button', { name: '修改', exact: true }).click()
  await page.getByRole('combobox', { name: '做法', exact: true }).click()
  await page.getByRole('option', { name: '取较低价', exact: true }).click()
  const tomorrow = await page.locator('input[type=date]').getAttribute('min')
    .then(d => new Date(Date.parse(d! + 'T00:00:00Z') + 86400000).toISOString().slice(0, 10))
  await page.getByLabel('生效日期', { exact: true }).fill(tomorrow)
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(choice).toContainText(`${tomorrow} 起改为 取较低价`)
  const integer = page.locator('[data-setting=points_per_recharge_yuan]')
  await integer.getByRole('button', { name: '修改', exact: true }).click()
  await page.getByLabel('充值积分', { exact: true }).fill('10')
  await page.getByLabel('生效日期', { exact: true }).fill(tomorrow)
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(integer).toContainText('10')
  for (const card of [choice, integer]) {
    await card.getByRole('button', { name: '修改', exact: true }).click()
    await page.getByLabel('生效日期', { exact: true }).fill(tomorrow)
    await page.getByRole('button', { name: '恢复系统默认', exact: true }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(card).toContainText('系统默认')
  }
})

test('退卡手续费编辑使用自己的标签', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/settings')
  await page.locator('[data-setting=card_refund_fee_percent]').getByRole('button', { name: '修改', exact: true }).click()
  await expect(page.getByRole('dialog').getByLabel('退卡手续费', { exact: true })).toBeVisible()
})
