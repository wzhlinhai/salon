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
  const input = dialog.getByPlaceholder('找顾客、员工、页面，或说一句话交给 AI')
  await input.fill('团购对账')
  await dialog.getByRole('option', { name: '团购对账', exact: true }).click()
  await expect(page).toHaveURL('/groupbuy-month')

  await page.keyboard.press('Control+k')
  await expect(dialog).toBeVisible()
  await input.fill(created.customer.name)
  const found = dialog.getByRole('option').filter({ hasText: phoneNumber })
  // 顾客那行先写手机号和上次做了什么（样稿 L5），没来过的写「还没来过」。
  await expect(found).toContainText(`${phoneNumber} · 还没来过`)
  await found.click()
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
    if (request.url().includes('/api/ops/list_customer_overview')) searched.push(request.url())
  })
  // 用假时钟把 300ms 防抖直接跑过去，不靠固定等待。
  await page.clock.install()
  await login(page, account, password)

  await page.keyboard.press('Control+k')
  const dialog = page.getByRole('dialog', { name: '搜索' })
  await expect(dialog).toBeVisible()
  const input = dialog.getByPlaceholder('找顾客、员工、页面，或说一句话交给 AI')
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
  await page.route('**/api/ops/list_customer_overview', async (route) => {
    if (slow) await new Promise(resolve => setTimeout(resolve, 3000))
    await route.continue()
  })
  await login(page, owner.account, owner.password)
  await page.keyboard.press('Control+k')
  const dialog = page.getByRole('dialog', { name: '搜索' })
  const input = dialog.getByPlaceholder('找顾客、员工、页面，或说一句话交给 AI')
  await input.fill(created.customer.name)
  const option = dialog.getByRole('option').filter({ hasText: phoneNumber })
  await expect(option).toBeVisible()
  slow = true
  await input.fill('明天下午谁有空')
  await expect(dialog.getByRole('option', { name: '问 AI：明天下午谁有空' })).toBeVisible()
  await expect(option).toHaveCount(0, { timeout: 1000 })
})

test('Ctrl K 直接做：给搜到的顾客开单；搜不到的手机号就地新建，建好接着开单', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const number = `137${String(Date.now()).slice(-8)}`
  const created = await api<{ customer: { name: string } }>(token, 'create_customer', { name: `直做顾客${suffix}`, phone: number })
  await login(page, owner.account, owner.password)

  await page.keyboard.press('Control+k')
  const dialog = page.getByRole('dialog', { name: '搜索' })
  const input = dialog.getByPlaceholder('找顾客、员工、页面，或说一句话交给 AI')
  // 粘贴来的号码带空格也按手机号找到这位，不提示新建。
  await input.fill(`${number.slice(0, 3)} ${number.slice(3, 7)} ${number.slice(7)}`)
  await expect(dialog.getByRole('option').filter({ hasText: number })).toBeVisible()
  await expect(dialog.getByRole('option', { name: /新建顾客/ })).toHaveCount(0)
  await dialog.getByRole('option', { name: `给${created.customer.name}开单` }).click()
  await expect(page).toHaveURL(/\/cashier/)
  const order = page.getByRole('dialog', { name: '开单' })
  await expect(order).toContainText(created.customer.name)
  await order.getByRole('button', { name: '取消', exact: true }).click()

  // 没见过的手机号：最后一行「用这个号新建」，弹窗带着号码，只问怎么称呼。
  const fresh = `136${String(Date.now()).slice(-8)}`
  await page.keyboard.press('Control+k')
  await input.fill(fresh)
  await dialog.getByRole('option', { name: `用 ${fresh.slice(0, 3)} ${fresh.slice(3, 7)} ${fresh.slice(7)} 新建顾客` }).click()
  const form = page.getByRole('dialog', { name: '新顾客' })
  await expect(form.getByLabel('手机号', { exact: true })).toHaveValue(`${fresh.slice(0, 3)} ${fresh.slice(3, 7)} ${fresh.slice(7)}`)
  // 输完称呼按回车就是「建好，开单」。
  await form.getByLabel('怎么称呼', { exact: true }).fill(`新客${suffix}`)
  await form.getByLabel('怎么称呼', { exact: true }).press('Enter')
  await expect(page.getByRole('dialog', { name: '开单' })).toContainText(`新客${suffix}`)
})

test('Ctrl K 搜到不止一位顾客时，按 Tab 换「直接做」对着的人，回车就给换到的那位开单', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix().slice(-6)
  const people: { id: string, name: string }[] = []
  for (const [index, name] of [`换人甲${suffix}`, `换人乙${suffix}`].entries()) {
    const res = await api<{ customer: { id: string, name: string } }>(token, 'create_customer', { name, phone: `135${String(Date.now() + index).slice(-8)}` })
    people.push(res.customer)
  }
  await login(page, owner.account, owner.password)

  await page.keyboard.press('Control+k')
  const dialog = page.getByRole('dialog', { name: '搜索' })
  const input = dialog.getByPlaceholder('找顾客、员工、页面，或说一句话交给 AI')
  await input.fill(suffix)
  const order = dialog.getByRole('option', { name: /^给换人.+开单/ })
  await expect(order).toContainText('Tab 换人')
  const first = (await order.textContent()) ?? ''
  // 方向键走到「给…开单」，按 Tab 换人：高亮留在这一行，焦点还在搜索框里。
  while (await order.getAttribute('data-highlighted') === null) await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Tab')
  await expect(order).not.toHaveText(first)
  await expect(order).toHaveAttribute('data-highlighted', '')
  await expect(input).toBeFocused()
  const chosen = people.find(person => !first.includes(person.name))!
  await expect(order).toContainText(chosen.name)
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/cashier`))
  await expect(page.getByRole('dialog', { name: '开单' })).toContainText(chosen.name)
})

test('Ctrl K 每次打开都重读员工；读不出来时说一声，手上的名单照样能找', async ({ page }) => {
  await login(page, owner.account, owner.password)
  const dialog = page.getByRole('dialog', { name: '搜索' })
  const input = dialog.getByPlaceholder('找顾客、员工、页面，或说一句话交给 AI')
  await page.keyboard.press('Control+k')
  await input.fill('王老板')
  await expect(dialog.getByRole('option', { name: /王老板/ }).first()).toBeVisible()
  await page.keyboard.press('Escape')

  let asked = 0
  await page.route('**/api/ops/list_staff', (route) => {
    asked++
    return route.abort()
  })
  await page.keyboard.press('Control+k')
  await expect(page.getByText('员工没有读出来', { exact: true })).toBeVisible()
  expect(asked).toBe(1)
  await input.fill('王老板')
  await expect(dialog.getByRole('option', { name: /王老板/ }).first()).toBeVisible()
})
