import { expect, test } from '@playwright/test'
import { env } from './env'
import { api, apiLogin, hire, login, owner } from './helpers'

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

test('登录断网后按钮恢复并可重新登录', async ({ page }) => {
  const employee = await hire('前台')
  let abortLogin = true
  await page.route('**/api/auth/login', async (route) => {
    if (abortLogin) {
      abortLogin = false
      await route.abort()
    } else {
      await route.continue()
    }
  })
  const pageErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.message))
  await page.goto('/login')
  await page.getByLabel('账号').fill(employee.account)
  await page.getByLabel('密码', { exact: true }).fill(employee.password)
  const button = page.getByRole('button', { name: '登录', exact: true })
  await button.click()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(button).toBeEnabled()
  await button.click()
  await expect(page.getByText(/你好/)).toBeVisible()
  expect(pageErrors).toEqual([])
})

test('换账号后迟到的 get_me 不覆盖当前身份', async ({ page }) => {
  const employee = await hire('前台')
  const tokenB = await apiLogin(employee.account, employee.password)
  const identityB = await api(tokenB, 'get_me', {})
  await login(page, owner.account, owner.password)
  let release!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  let started!: () => void
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  let first = true
  await page.route('**/api/ops/get_me', async (route) => {
    if (first) {
      first = false
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else {
      await route.continue()
    }
  })
  // 页面没有重新读取身份的按钮，直接调用开发服务器导出的现有 composable；不刷新文档，保留在途 A 请求。
  const oldLoad = page.evaluate(async () => {
    const modulePath = '/_nuxt/composables/useMe.ts'
    const { useMe } = await import(modulePath)
    const app = (window as unknown as { useNuxtApp: () => { runWithContext: <T>(fn: () => T) => T } }).useNuxtApp()
    return app.runWithContext(() => useMe().load())
  })
  await pending
  await page.evaluate(async (token) => {
    localStorage.setItem('salon_token', token)
    const modulePath = '/_nuxt/composables/useMe.ts'
    const { useMe } = await import(modulePath)
    const app = (window as unknown as { useNuxtApp: () => { runWithContext: <T>(fn: () => T) => T } }).useNuxtApp()
    await app.runWithContext(() => useMe().load())
  }, tokenB)
  await expect(page.getByRole('heading', { name: `你好，${identityB.staff.name}`, exact: true })).toBeVisible()
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/get_me'))
  release()
  await (await response).finished()
  expect((await oldLoad).staff.id).toBe(identityB.staff.id)
  await expect(page.getByRole('heading', { name: `你好，${identityB.staff.name}`, exact: true })).toBeVisible()
})

test('修改密码断网后恢复并可重试', async ({ page }) => {
  const employee = await hire('前台')
  await login(page, employee.account, employee.password)
  await page.getByLabel('原密码', { exact: true }).fill(employee.password)
  await page.getByLabel('新密码（至少 8 位）', { exact: true }).fill('retry-e2e-password')
  let abort = true
  await page.route('**/api/ops/change_my_password', async (route) => {
    if (abort) {
      abort = false
      await route.abort()
    } else await route.continue()
  })
  const button = page.getByRole('button', { name: '修改', exact: true })
  await button.click()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(button).toBeEnabled()
  await button.click()
  await expect(page).toHaveURL(/\/login$/)
})
