import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, hire, login, owner } from './helpers'

let phoneSequence = Date.now() % 100000000
const phone = () => `138${String(++phoneSequence).padStart(8, '0')}`

async function search(page: Page, query: string) {
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/search_customers') && r.request().postDataJSON().query === query)
  await page.getByLabel('搜索顾客', { exact: true }).fill(query)
  await page.getByLabel('搜索顾客', { exact: true }).press('Enter')
  return (await response).json()
}

async function create(page: Page, name: string, number: string) {
  await page.getByRole('button', { name: '新建顾客', exact: true }).click()
  const form = page.getByRole('dialog', { name: '新建顾客', exact: true })
  await form.getByLabel('手机号', { exact: true }).fill(number)
  await form.getByLabel('姓名', { exact: true }).fill(name)
  await form.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '顾客详情', exact: true })).toBeVisible()
}

async function select(page: Page, label: string, name: string) {
  await page.getByLabel(label, { exact: true }).click()
  await page.getByRole('option', { name, exact: true }).click()
}

test('前台重复手机号打开同一位顾客，不再新建', async ({ page }) => {
  const receptionist = await hire('前台')
  const number = phone()
  const name = `前台顾客${Date.now()}`
  await login(page, receptionist.account, receptionist.password)
  await page.goto('/customers')
  await create(page, name, number)
  const firstURL = page.url()
  await page.goto('/customers')
  await create(page, '重复建档', number)
  await expect(page.getByText('这个手机号已有档案', { exact: true })).toBeVisible()
  await expect(page).toHaveURL(firstURL)
  await expect(page.getByRole('dialog', { name: '顾客详情', exact: true })).toContainText(name)
})

test('前台看到完整手机号，发型师看到打码号码', async ({ page }) => {
  const receptionist = await hire('前台')
  const stylist = await hire('发型师')
  const token = await apiLogin(receptionist.account, receptionist.password)
  const number = phone()
  const name = `手机号顾客${Date.now()}`
  const { customer } = await api(token, 'create_customer', { name, phone: number })
  await login(page, receptionist.account, receptionist.password)
  await page.goto('/customers')
  await search(page, name)
  await expect(page.locator('tbody tr').filter({ hasText: name })).toContainText(number)
  await page.goto(`/customers?id=${customer.id}`)
  await expect(page.getByRole('dialog', { name: '顾客详情', exact: true })).toContainText(number)
  await login(page, stylist.account, stylist.password)
  await page.goto('/customers')
  await search(page, name)
  const masked = `138****${number.slice(-4)}`
  await expect(page.locator('tbody tr').filter({ hasText: name })).toContainText(masked)
  await expect(page.locator('tbody tr').filter({ hasText: name })).not.toContainText(number)
  await page.goto(`/customers?id=${customer.id}`)
  await expect(page.getByRole('dialog', { name: '顾客详情', exact: true })).toContainText(masked)
  await expect(page.getByRole('dialog', { name: '顾客详情', exact: true })).not.toContainText(number)
})

test('店长合并顾客时迁移安全标签，默认搜索隐藏被合并顾客', async ({ page }) => {
  const manager = await hire('店长')
  const token = await apiLogin(manager.account, manager.password)
  const suffix = Date.now()
  const keepName = `保留顾客${suffix}`
  const mergedName = `并入顾客${suffix}`
  const keep = (await api(token, 'create_customer', { name: keepName, phone: phone() })).customer
  const merged = (await api(token, 'create_customer', { name: mergedName, phone: phone() })).customer
  const { business_date } = await api(token, 'get_settings', {})
  await api(token, 'add_safety_label', { customer_id: merged.id, kind: 'allergy', source: 'customer_said', observed_on: business_date, note: `染发过敏${suffix}` })
  await login(page, manager.account, manager.password)
  await page.goto(`/customers?id=${keep.id}`)
  await page.getByRole('button', { name: '合并重复顾客', exact: true }).click()
  await page.getByLabel('搜索另一位顾客', { exact: true }).fill(mergedName)
  await page.getByLabel('搜索另一位顾客', { exact: true }).press('Enter')
  await page.getByRole('button', { name: new RegExp(mergedName) }).click()
  await expect(page.getByRole('button', { name: '保留这位顾客' })).toHaveCount(2)
  await page.getByRole('button', { name: '保留这位顾客' }).first().click()
  await page.getByRole('button', { name: '继续合并', exact: true }).click()
  await expect(page.getByText('合并后不可撤销：被合并顾客的安全标签等记录归到保留的顾客名下', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '确认合并', exact: true }).click()
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await expect(detail.getByText(`染发过敏${suffix}`, { exact: true })).toBeVisible()
  await expect(detail.getByText('过敏', { exact: true })).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`id=${keep.id}`))
  await page.goto('/customers')
  expect((await search(page, mergedName)).items).toEqual([])
  await expect(page.getByRole('button', { name: mergedName, exact: true })).toHaveCount(0)
  await page.goto(`/customers?id=${merged.id}`)
  await expect(page.getByText('已合并到另一位顾客', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '修改资料', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '查看保留的顾客', exact: true }).click()
  await expect(detail.getByRole('heading', { name: keepName, exact: true })).toBeVisible()
})

test('店长从空查询分页找到全空未识别顾客，并选择保留另一位有手机号的顾客', async ({ page }) => {
  const manager = await hire('店长')
  const token = await apiLogin(manager.account, manager.password)
  const name = `识别顾客${Date.now()}`
  const keep = (await api(token, 'create_customer', { name, phone: phone() })).customer
  const blank = (await api(token, 'create_customer', {})).customer
  expect(blank.name).toBeNull()
  expect(blank.phone).toBeNull()
  expect(blank.gender).toBeNull()
  expect(blank.source).toBeNull()
  expect(blank.note).toBeNull()
  const newerIDs: string[] = []
  for (let i = 0; i < 50; i++) {
    const { customer } = await api(token, 'create_customer', {})
    newerIDs.unshift(customer.id)
  }
  await login(page, manager.account, manager.password)
  await page.goto(`/customers?id=${keep.id}`)
  await page.getByRole('button', { name: '合并重复顾客', exact: true }).click()
  const mergeDialog = page.getByRole('dialog', { name: '合并重复顾客', exact: true })
  await expect(mergeDialog.getByLabel('搜索另一位顾客', { exact: true })).toHaveValue('')
  await expect(mergeDialog.locator('[data-customer-id]')).toHaveCount(50)
  expect(await mergeDialog.locator('[data-customer-id]').evaluateAll(rows => rows.map(r => r.getAttribute('data-customer-id')))).toEqual(newerIDs)
  await mergeDialog.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(mergeDialog.getByText('第 2 页', { exact: true })).toBeVisible()
  await expect(mergeDialog.locator(`[data-customer-id="${keep.id}"]`)).toHaveCount(0)
  await mergeDialog.locator(`[data-customer-id="${blank.id}"]`).click()
  await expect(mergeDialog.getByRole('button', { name: '保留这位顾客', exact: true })).toHaveCount(2)
  await mergeDialog.getByRole('button', { name: '上一页', exact: true }).click()
  await expect(mergeDialog.locator('[data-customer-id]')).toHaveCount(50)
  await page.goto(`/customers?id=${blank.id}`)
  await page.getByRole('button', { name: '合并重复顾客', exact: true }).click()
  await expect(mergeDialog.locator('[data-customer-id]')).toHaveCount(50)
  await mergeDialog.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(mergeDialog.locator(`[data-customer-id="${blank.id}"]`)).toHaveCount(0)
  await mergeDialog.locator(`[data-customer-id="${keep.id}"]`).click()
  await mergeDialog.getByRole('button', { name: '保留这位顾客', exact: true }).nth(1).click()
  await mergeDialog.getByRole('button', { name: '继续合并', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '确认合并顾客', exact: true })).toContainText(`保留：${name}；被合并：未识别顾客`)
  await page.getByRole('button', { name: '确认合并', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`id=${keep.id}`))
  await expect(page.getByRole('dialog', { name: '顾客详情', exact: true }).getByRole('heading', { name, exact: true })).toBeVisible()
  expect((await api(token, 'get_customer', { id: blank.id })).merged_into).toBe(keep.id)
})

test('店长把停用员工负责的顾客交接后，按新负责人筛选可见', async ({ page }) => {
  const manager = await hire('店长')
  const from = await hire('发型师')
  const to = await hire('发型师')
  const token = await apiLogin(owner.account, owner.password)
  const { staff: fromMe } = await api(await apiLogin(from.account, from.password), 'get_me', {})
  const { staff: toMe } = await api(await apiLogin(to.account, to.password), 'get_me', {})
  const suffix = Date.now()
  const names = [`交接甲${suffix}`, `交接乙${suffix}`]
  const a = (await api(token, 'create_customer', { name: names[0], phone: phone() })).customer
  const b = (await api(token, 'create_customer', { name: names[1], phone: phone() })).customer
  await api(token, 'assign_customer_responsible', { customer_ids: [a.id, b.id], staff_id: fromMe.id })
  await api(token, 'deactivate_staff', { staff_id: fromMe.id })
  await login(page, manager.account, manager.password)
  await page.goto('/customers')
  await page.getByRole('button', { name: '负责人交接', exact: true }).click()
  await select(page, '交出的员工', `${fromMe.name}（已停用）`)
  await select(page, '接手的员工', toMe.name)
  await page.getByRole('button', { name: '确认交接', exact: true }).click()
  await expect(page.getByText('已移交 2 位顾客', { exact: true })).toBeVisible()
  await select(page, '负责人筛选', toMe.name)
  await expect(page.locator('tbody tr')).toHaveCount(2)
  await expect(page.locator('tbody')).toContainText(names[0]!)
  await expect(page.locator('tbody')).toContainText(names[1]!)
  await page.getByRole('button', { name: names[0]!, exact: true }).click()
  const log = page.getByRole('dialog', { name: '顾客详情', exact: true }).getByRole('row').filter({ has: page.getByRole('cell', { name: '负责人交接', exact: true }) })
  await expect(log).toHaveCount(1)
  await expect(log.locator('td').nth(3)).toContainText(`负责人：${fromMe.name}`)
  await expect(log.locator('td').nth(4)).toContainText(`负责人：${toMe.name}`)
})

test('发型师没有合并重复顾客和负责人交接入口', async ({ page }) => {
  const stylist = await hire('发型师')
  const token = await apiLogin(stylist.account, stylist.password)
  const { customer } = await api(token, 'create_customer', { name: `权限顾客${Date.now()}`, phone: phone() })
  await login(page, stylist.account, stylist.password)
  await page.goto('/customers')
  await expect(page.getByRole('heading', { name: '顾客', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '负责人交接', exact: true })).toHaveCount(0)
  await page.goto(`/customers?id=${customer.id}`)
  await expect(page.getByRole('button', { name: '修改资料', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '合并重复顾客', exact: true })).toHaveCount(0)
})

test('修改顾客资料保留已有字段，补改手机号及安全标签可操作并留下日志', async ({ page }) => {
  const manager = await hire('店长')
  const token = await apiLogin(manager.account, manager.password)
  const suffix = Date.now()
  const name = `资料顾客${suffix}`
  const { customer } = await api(token, 'create_customer', { name, gender: 'female', note: `原备注${suffix}` })
  const number = phone()
  await login(page, manager.account, manager.password)
  await page.goto(`/customers?id=${customer.id}`)
  await page.getByRole('button', { name: '修改资料', exact: true }).click()
  const form = page.getByRole('dialog', { name: '修改顾客资料', exact: true })
  await form.getByLabel('姓名', { exact: true }).fill(`${name}改`)
  await form.getByRole('button', { name: '保存', exact: true }).click()
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await expect(detail).toContainText(`原备注${suffix}`)
  await expect(detail).toContainText('女')
  await page.getByRole('button', { name: '补手机号', exact: true }).click()
  await page.getByRole('dialog', { name: '补手机号', exact: true }).getByLabel('手机号', { exact: true }).fill(number)
  await page.getByRole('dialog', { name: '补手机号', exact: true }).getByRole('button', { name: '保存', exact: true }).click()
  await expect(detail.getByRole('button', { name: '改手机号', exact: true })).toBeVisible()
  await expect(detail).toContainText(number)
  await page.getByRole('button', { name: '改手机号', exact: true }).click()
  const newNumber = phone()
  await page.getByRole('dialog', { name: '改手机号', exact: true }).getByLabel('手机号', { exact: true }).fill(newNumber)
  await page.getByRole('dialog', { name: '改手机号', exact: true }).getByRole('button', { name: '保存', exact: true }).click()
  await expect(detail).toContainText(newNumber)
  await detail.getByRole('button', { name: '添加安全标签', exact: true }).click()
  const labelForm = page.getByRole('dialog', { name: '添加安全标签', exact: true })
  await labelForm.getByLabel('备注', { exact: true }).fill(`观察备注${suffix}`)
  await labelForm.getByRole('button', { name: '保存', exact: true }).click()
  await expect(detail.getByText(`观察备注${suffix}`, { exact: true })).toBeVisible()
  await detail.getByRole('button', { name: '撤销', exact: true }).click()
  await page.getByRole('button', { name: '确认撤销', exact: true }).click()
  await expect(detail.getByText('暂无有效安全标签', { exact: true })).toBeVisible()
  await expect(detail.locator('table')).toContainText('撤销安全标签')
  const saved = await api(token, 'get_customer', { id: customer.id })
  expect(saved.name).toBe(`${name}改`)
  expect(saved.gender).toBe('female')
  expect(saved.note).toBe(`原备注${suffix}`)
  expect(saved.phone).toBe(newNumber)
  expect(saved.active_safety_labels).toEqual([])
})

test('店长多选分配负责人，可清空负责人', async ({ page }) => {
  const manager = await hire('店长')
  const token = await apiLogin(manager.account, manager.password)
  const { staff } = await api(token, 'get_me', {})
  const prefix = `分配顾客${Date.now()}`
  const a = (await api(token, 'create_customer', { name: `${prefix}甲`, phone: phone() })).customer
  const b = (await api(token, 'create_customer', { name: `${prefix}乙`, phone: phone() })).customer
  await login(page, manager.account, manager.password)
  await page.goto('/customers')
  await search(page, prefix)
  await page.getByRole('checkbox', { name: `选择顾客 ${prefix}甲`, exact: true }).check()
  await page.getByRole('checkbox', { name: `选择顾客 ${prefix}乙`, exact: true }).check()
  await page.getByRole('button', { name: '分配负责人', exact: true }).click()
  await select(page, '负责人', staff.name)
  await page.getByRole('button', { name: '确认分配', exact: true }).click()
  await expect(page.locator('tbody tr').filter({ hasText: staff.name })).toHaveCount(2)
  await page.getByRole('checkbox', { name: `选择顾客 ${prefix}甲`, exact: true }).check()
  await page.getByRole('checkbox', { name: `选择顾客 ${prefix}乙`, exact: true }).check()
  await page.getByRole('button', { name: '分配负责人', exact: true }).click()
  await page.getByRole('button', { name: '确认分配', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '分配负责人', exact: true })).toHaveCount(0)
  expect((await api(token, 'get_customer', { id: a.id })).responsible).toBeNull()
  expect((await api(token, 'get_customer', { id: b.id })).responsible).toBeNull()
})

test('老板管理顾客来源，发型师只读且新建时不列停用来源', async ({ page }) => {
  const stylist = await hire('发型师')
  const name = `来源${Date.now()}`
  await login(page, owner.account, owner.password)
  await page.goto('/settings')
  const card = page.locator('[data-setting=customer_sources]')
  await card.getByLabel('新顾客来源', { exact: true }).fill(name)
  await card.getByRole('button', { name: '添加', exact: true }).click()
  const row = card.locator('div.flex').filter({ has: page.getByRole('textbox', { name: `顾客来源 ${name}`, exact: true }) })
  await row.getByRole('switch').uncheck()
  await expect(row.getByRole('switch')).not.toBeChecked()
  await login(page, stylist.account, stylist.password)
  await page.goto('/settings')
  await expect(card).toContainText(name)
  await expect(card.getByRole('textbox')).toHaveCount(0)
  await expect(card.getByRole('switch')).toHaveCount(0)
  await expect(card.getByRole('button', { name: '添加', exact: true })).toHaveCount(0)
  await page.goto('/customers')
  await page.getByRole('button', { name: '新建顾客', exact: true }).click()
  await page.getByLabel('顾客来源', { exact: true }).click()
  await expect(page.getByRole('option', { name, exact: true })).toHaveCount(0)
})

test('切换顾客后丢弃迟到的详情，编辑仍预填当前顾客', async ({ page }) => {
  const manager = await hire('店长')
  const token = await apiLogin(manager.account, manager.password)
  const suffix = Date.now()
  const a = (await api(token, 'create_customer', { name: `竞态甲${suffix}`, phone: phone() })).customer
  const b = (await api(token, 'create_customer', { name: `竞态乙${suffix}`, phone: phone() })).customer
  let releaseA!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseA = resolve
  })
  let startedA!: () => void
  const started = new Promise<void>((resolve) => {
    startedA = resolve
  })
  await page.route('**/api/ops/get_customer', async (route) => {
    if (route.request().postDataJSON().id === a.id) {
      startedA()
      await blocked
    }
    await route.continue()
  })
  await page.route('**/api/ops/list_audit_log', async (route) => {
    if (route.request().postDataJSON().target_id === a.id) {
      await blocked
    }
    await route.continue()
  })
  await login(page, manager.account, manager.password)
  const startedLog = page.waitForRequest(r => r.url().endsWith('/api/ops/list_audit_log') && r.postDataJSON().target_id === a.id)
  await page.goto(`/customers?id=${a.id}`)
  await Promise.all([started, startedLog])
  await page.evaluate((id) => {
    window.history.pushState({}, '', `/customers?id=${id}`)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, b.id)
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await expect(detail.getByRole('heading', { name: b.name, exact: true })).toBeVisible()
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/get_customer') && r.request().postDataJSON().id === a.id)
  const lateLogResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/list_audit_log') && r.request().postDataJSON().target_id === a.id)
  releaseA()
  await Promise.all([lateResponse, lateLogResponse].map(async response => (await response).finished()))
  // eslint-disable-next-line no-restricted-syntax -- 旧详情被丢弃后没有可观察的状态变化，响应接收完成也不能确认组件回调已执行。
  await page.waitForTimeout(500)
  await expect(detail).toContainText(b.name)
  await expect(detail).not.toContainText(a.name)
  await page.getByRole('button', { name: '修改资料', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '修改顾客资料', exact: true }).getByLabel('姓名', { exact: true })).toHaveValue(b.name)
})

test('切换顾客后迟到的资料保存不关闭当前编辑弹窗或提示成功', async ({ page }) => {
  const manager = await hire('店长')
  const token = await apiLogin(manager.account, manager.password)
  const suffix = Date.now()
  const a = (await api(token, 'create_customer', { name: `保存竞态甲${suffix}`, phone: phone() })).customer
  const b = (await api(token, 'create_customer', { name: `保存竞态乙${suffix}`, phone: phone() })).customer
  let releaseA!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseA = resolve
  })
  let startedA!: () => void
  const started = new Promise<void>((resolve) => {
    startedA = resolve
  })
  await page.route('**/api/ops/update_customer_profile', async (route) => {
    if (route.request().postDataJSON().id === a.id) {
      startedA()
      await blocked
    }
    await route.continue()
  })
  await login(page, manager.account, manager.password)
  await page.goto(`/customers?id=${a.id}`)
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await expect(detail.getByRole('heading', { name: a.name, exact: true })).toBeVisible()
  await detail.getByRole('button', { name: '修改资料', exact: true }).click()
  const form = page.getByRole('dialog', { name: '修改顾客资料', exact: true })
  await form.getByLabel('姓名', { exact: true }).fill(`${a.name}改`)
  await form.getByRole('button', { name: '保存', exact: true }).click()
  await started
  await page.evaluate((id) => {
    window.history.pushState({}, '', `/customers?id=${id}`)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, b.id)
  await expect(detail.getByRole('heading', { name: b.name, exact: true })).toBeVisible()
  await detail.getByRole('button', { name: '修改资料', exact: true }).click()
  await expect(form.getByLabel('姓名', { exact: true })).toHaveValue(b.name)
  const save = form.getByRole('button', { name: '保存', exact: true })
  await expect(save).toBeDisabled()
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/update_customer_profile') && r.request().postDataJSON().id === a.id)
  releaseA()
  const response = await lateResponse
  await response.finished()
  expect(response.ok()).toBe(true)
  await expect(save).toBeEnabled()
  await expect(form).toBeVisible()
  await expect(form.getByLabel('姓名', { exact: true })).toHaveValue(b.name)
  await expect(page).toHaveURL(new RegExp(`id=${b.id}`))
  await expect(page.getByText('资料已保存', { exact: true })).toHaveCount(0)
  expect((await api(token, 'get_customer', { id: a.id })).name).toBe(`${a.name}改`)
})

test('切换顾客后迟到的合并不跳转详情或提示成功', async ({ page }) => {
  const manager = await hire('店长')
  const token = await apiLogin(manager.account, manager.password)
  const suffix = Date.now()
  const a = (await api(token, 'create_customer', { name: `合并竞态甲${suffix}`, phone: phone() })).customer
  const c = (await api(token, 'create_customer', { name: `合并竞态丙${suffix}`, phone: phone() })).customer
  const b = (await api(token, 'create_customer', { name: `合并竞态乙${suffix}`, phone: phone() })).customer
  let releaseA!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseA = resolve
  })
  let startedA!: () => void
  const started = new Promise<void>((resolve) => {
    startedA = resolve
  })
  await page.route('**/api/ops/merge_customers', async (route) => {
    if (route.request().postDataJSON().keep_id === a.id) {
      startedA()
      await blocked
    }
    await route.continue()
  })
  await login(page, manager.account, manager.password)
  await page.goto(`/customers?id=${a.id}`)
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await expect(detail.getByRole('heading', { name: a.name, exact: true })).toBeVisible()
  await detail.getByRole('button', { name: '合并重复顾客', exact: true }).click()
  const mergeDialog = page.getByRole('dialog', { name: '合并重复顾客', exact: true })
  await mergeDialog.getByLabel('搜索另一位顾客', { exact: true }).fill(c.name)
  await mergeDialog.getByLabel('搜索另一位顾客', { exact: true }).press('Enter')
  await mergeDialog.locator(`[data-customer-id="${c.id}"]`).click()
  await mergeDialog.getByRole('button', { name: '继续合并', exact: true }).click()
  await page.getByRole('dialog', { name: '确认合并顾客', exact: true }).getByRole('button', { name: '确认合并', exact: true }).click()
  await started
  await page.evaluate((id) => {
    window.history.pushState({}, '', `/customers?id=${id}`)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, b.id)
  await expect(detail.getByRole('heading', { name: b.name, exact: true })).toBeVisible()
  await detail.getByRole('button', { name: '修改资料', exact: true }).click()
  const form = page.getByRole('dialog', { name: '修改顾客资料', exact: true })
  await expect(form.getByLabel('姓名', { exact: true })).toHaveValue(b.name)
  const save = form.getByRole('button', { name: '保存', exact: true })
  await expect(save).toBeDisabled()
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/merge_customers') && r.request().postDataJSON().keep_id === a.id)
  releaseA()
  const response = await lateResponse
  await response.finished()
  expect(response.ok()).toBe(true)
  await expect(save).toBeEnabled()
  await expect(page).toHaveURL(new RegExp(`id=${b.id}`))
  await expect(form).toBeVisible()
  await expect(form.getByLabel('姓名', { exact: true })).toHaveValue(b.name)
  await expect(page.getByText('顾客已合并', { exact: true })).toHaveCount(0)
  expect((await api(token, 'get_customer', { id: c.id })).merged_into).toBe(a.id)
})

test('顾客日志按游标加载更多，不重不漏', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const { customer } = await api(token, 'create_customer', { name: `日志分页${Date.now()}`, phone: phone() })
  for (let i = 0; i < 51; i++) {
    await api(token, 'update_customer_profile', { id: customer.id, note: `分页记录${i}` })
  }
  await login(page, owner.account, owner.password)
  await page.goto(`/customers?id=${customer.id}`)
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  const rows = detail.locator('tbody tr')
  await expect(rows).toHaveCount(50)
  const more = detail.getByRole('button', { name: '加载更多', exact: true })
  await expect(more).toBeVisible()
  await more.click()
  await expect(rows).toHaveCount(52)
  await expect(more).toHaveCount(0)
  await expect(rows.filter({ hasText: '新建顾客' })).toHaveCount(1)
})

test('切换顾客后丢弃迟到的加载更多日志', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const a = (await api(token, 'create_customer', { name: `日志竞态甲${Date.now()}`, phone: phone() })).customer
  const b = (await api(token, 'create_customer', { name: `日志竞态乙${Date.now()}`, phone: phone() })).customer
  for (let i = 0; i < 50; i++) {
    await api(token, 'update_customer_profile', { id: a.id, note: `旧顾客${i}` })
  }
  let release!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  let started!: () => void
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/list_audit_log', async (route) => {
    const body = route.request().postDataJSON()
    if (body.target_id === a.id && body.before_id) {
      started()
      await blocked
    }
    await route.continue()
  })
  await login(page, owner.account, owner.password)
  await page.goto(`/customers?id=${a.id}`)
  const detail = page.getByRole('dialog', { name: '顾客详情', exact: true })
  await detail.getByRole('button', { name: '加载更多', exact: true }).click()
  await pending
  await page.evaluate((id) => {
    window.history.pushState({}, '', `/customers?id=${id}`)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, b.id)
  await expect(detail.getByRole('heading', { name: b.name, exact: true })).toBeVisible()
  await expect(detail.locator('tbody tr')).toHaveCount(1)
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/list_audit_log') && r.request().postDataJSON().before_id)
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 当前顾客没有加载更多按钮，旧请求结束也不会改变其 loading 状态，无法观察回调完成。
  await page.waitForTimeout(500)
  await expect(detail.locator('tbody tr')).toHaveCount(1)
  await expect(detail.locator('table')).not.toContainText(a.name)
  await expect(detail.getByRole('button', { name: '加载更多', exact: true })).toHaveCount(0)
})
