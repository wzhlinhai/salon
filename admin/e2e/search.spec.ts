import { expect, test } from '@playwright/test'
import { api, apiLogin, login, owner, uniqueSuffix } from './helpers'

test('Ctrl K 搜索页面和顾客并导航', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const phoneNumber = `138${String(Date.now()).slice(-8)}`
  const created = await api<{ customer: { id: string, name: string } }>(token, 'create_customer', {
    name: `搜索顾客${suffix}`,
    phone: phoneNumber
  })
  await login(page, owner.account, owner.password)

  await page.keyboard.press('Control+k')
  const dialog = page.getByRole('dialog', { name: '搜索' })
  await expect(dialog).toBeVisible()
  const input = dialog.getByPlaceholder('搜索顾客、页面，或说一句话交给 AI…')
  await input.fill('团购对账')
  await dialog.getByRole('option', { name: '团购对账', exact: true }).click()
  await expect(page).toHaveURL('/groupbuy-month')

  await page.keyboard.press('Control+k')
  await expect(dialog).toBeVisible()
  await input.fill(created.customer.name)
  await dialog.getByRole('option', { name: `${created.customer.name} · ${phoneNumber}`, exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/customers/${created.customer.id}$`))
  await expect(page.getByTestId('customer-profile').getByRole('heading', { name: created.customer.name, exact: true })).toBeVisible()
})

test('没有查看顾客权限时 Ctrl K 不查顾客', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const role = await api<{ id: string }>(token, 'create_role', {
    name: `只打卡${suffix}`,
    permissions: [{ permission: 'attendance.clock' }]
  })
  const staff = await api<{ id: string }>(token, 'create_staff', { name: `只打卡${suffix}`, number: suffix, role_ids: [role.id] })
  const account = `search-${suffix}`
  const password = `password-${suffix}`
  await api(token, 'set_staff_login', { staff_id: staff.id, login_account: account, password })
  const searched: string[] = []
  page.on('request', (request) => {
    if (request.url().includes('/api/ops/search_customers')) searched.push(request.url())
  })
  // 用假时钟把 300ms 防抖直接跑过去，不靠固定等待。
  await page.clock.install()
  await login(page, account, password)

  await page.keyboard.press('Control+k')
  const dialog = page.getByRole('dialog', { name: '搜索' })
  await expect(dialog).toBeVisible()
  const input = dialog.getByPlaceholder('搜索顾客、页面，或说一句话交给 AI…')
  await input.fill('首页')
  await expect(dialog.getByText('页面', { exact: true })).toBeVisible()
  await page.clock.runFor(1000)
  // 再和页面往返一次，确保防抖回调里如果发了请求，事件已经送到。
  await expect(input).toHaveValue('首页')
  expect(searched).toEqual([])
  await expect(dialog.getByText('顾客', { exact: true })).toHaveCount(0)
})

test('Ctrl K 换了词立即清掉上一词的顾客结果', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const phoneNumber = `139${String(Date.now()).slice(-8)}`
  const created = await api<{ customer: { name: string } }>(token, 'create_customer', { name: `换词顾客${suffix}`, phone: phoneNumber })
  // 第二个词的顾客查询故意拖慢，旧结果若没清掉就会留在列表里。
  let slow = false
  await page.route('**/api/ops/search_customers', async (route) => {
    if (slow) await new Promise(resolve => setTimeout(resolve, 3000))
    await route.continue()
  })
  await login(page, owner.account, owner.password)
  await page.keyboard.press('Control+k')
  const dialog = page.getByRole('dialog', { name: '搜索' })
  const input = dialog.getByPlaceholder('搜索顾客、页面，或说一句话交给 AI…')
  await input.fill(created.customer.name)
  const option = dialog.getByRole('option', { name: `${created.customer.name} · ${phoneNumber}`, exact: true })
  await expect(option).toBeVisible()
  slow = true
  await input.fill('明天下午谁有空')
  await expect(dialog.getByRole('option', { name: '吩咐 AI：明天下午谁有空' })).toBeVisible()
  await expect(option).toHaveCount(0, { timeout: 1000 })
})
