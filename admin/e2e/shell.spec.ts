import { expect, test } from '@playwright/test'
import { api, apiLogin, hire, login, owner } from './helpers'

test('侧栏图标随客户端打包，不请求 Iconify，折叠后仍可用', async ({ page }) => {
  const iconifyRequests: string[] = []
  page.on('request', (request) => {
    if (/iconify/.test(new URL(request.url()).hostname)) iconifyRequests.push(request.url())
  })
  await page.setViewportSize({ width: 1024, height: 768 })
  await login(page, owner.account, owner.password)

  const navLinks = page.locator('[data-testid="sidebar-nav"] a')
  await expect(navLinks.first().locator('svg')).toBeVisible()
  await expect.poll(() => navLinks.evaluateAll(links => links.every(link => !!link.querySelector('svg')))).toBe(true)

  const search = page.getByRole('button', { name: /搜索/ })
  await expect(search).toContainText('搜索')
  const label = search.getByText('搜索', { exact: true })
  expect(await label.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)

  await page.getByTestId('sidebar-collapse').click()
  await expect(page.getByTestId('sidebar-collapse')).toHaveAttribute('aria-label', '展开侧栏')
  await expect(page.getByTestId('sidebar-collapse')).toBeVisible()
  await expect(navLinks.first().locator('svg')).toBeVisible()
  await expect(page.getByTestId('personal-menu')).toBeVisible()
  expect(iconifyRequests).toEqual([])
})

test('个人菜单可以打卡、打开我的考勤并退出登录', async ({ page }) => {
  const front = await hire('前台')
  const token = await apiLogin(owner.account, owner.password)
  const settings = await api<{ business_date: string }>(token, 'get_settings', {})
  await login(page, front.account, front.password)

  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '打卡' }).click()
  await expect(page.locator('[data-slot="title"]').filter({ hasText: '已打卡' })).toBeVisible()

  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '我的考勤' }).click()
  const dialog = page.getByRole('dialog', { name: '我的考勤' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText(settings.business_date)).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog).not.toBeVisible()

  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '退出登录' }).click()
  await expect(page).toHaveURL(/\/login$/)
  expect(await page.evaluate(() => localStorage.getItem('salon_token'))).toBeNull()
})
