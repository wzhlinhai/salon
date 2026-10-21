import { expect, test } from '@playwright/test'
import { env } from './env'
import { hire, login } from './helpers'

test('修改密码后清除登录状态并重新登录', async ({ page }) => {
  const employee = await hire('前台')
  await login(page, employee.account, employee.password)
  await page.getByLabel('原密码', { exact: true }).fill(employee.password)
  await page.getByLabel('新密码（至少 8 位）', { exact: true }).fill('new-e2e-password')
  await page.getByRole('button', { name: '修改', exact: true }).click()
  await expect(page).toHaveURL(/\/login$/)
  await expect(page.getByText('密码已修改，请重新登录', { exact: true })).toBeVisible()
  expect(await page.evaluate(() => localStorage.getItem('salon_token'))).toBeNull()
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
  await login(page, employee.account, 'new-e2e-password')
})

test('锁定账号的登录页显示后端 429 提示', async ({ page, request }) => {
  const employee = await hire('前台')
  for (let i = 0; i < 15; i++) {
    const response = await request.post(`${env.baseURL}/api/auth/login`, {
      data: { account: employee.account, password: 'wrong-password' }
    })
    expect(response.status()).toBe(401)
  }
  await page.goto('/login')
  await page.getByLabel('账号').fill(employee.account)
  await page.getByLabel('密码', { exact: true }).fill(employee.password)
  const response = page.waitForResponse(r => r.url().endsWith('/api/auth/login'))
  await page.getByRole('button', { name: '登录', exact: true }).click()
  expect((await response).status()).toBe(429)
  await expect(page.getByText('登录失败次数过多，请 15 分钟后再试', { exact: true })).toBeVisible()
})
