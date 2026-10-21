import { expect, test } from '@playwright/test'
import { hire, login, owner } from './helpers'

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
