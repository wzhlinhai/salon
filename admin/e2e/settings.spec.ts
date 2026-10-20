import { expect, test } from '@playwright/test'
import { login, owner } from './helpers'

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
