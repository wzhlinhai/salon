import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'
import { api, apiLogin, login, owner } from './helpers'

// 员工名册的一行：头像、姓名，下面一句话；按姓名精确找。
function staffRow(page: Page, name: string) {
  return page.getByTestId('staff-row').filter({ has: page.getByText(name, { exact: true }) })
}

// 员工详情页上的操作：修改档案是按钮，其余在 ··· 里。
async function profileAction(page: Page, action: string) {
  if (action === '修改档案') {
    await page.getByRole('button', { name: '修改档案', exact: true }).click()
    return
  }
  await page.getByRole('button', { name: '员工操作', exact: true }).click()
  await page.getByRole('menuitem', { name: action, exact: true }).click()
}

test('员工页迟到的旧列表不覆盖第二次保存', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/staff')
  await expect(page.getByTestId('staff-row').first()).toBeVisible()
  let releaseOld!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseOld = resolve
  })
  let startedOld!: () => void
  const started = new Promise<void>((resolve) => {
    startedOld = resolve
  })
  let requests = 0
  await page.route('**/api/ops/list_staff', async (route) => {
    if (++requests === 1) {
      const response = await route.fetch()
      startedOld()
      await blocked
      await route.fulfill({ response })
    } else {
      await route.continue()
    }
  })
  const first = `第一次${Date.now().toString(36)}`
  const second = `${first}第二次`

  async function save(name: string) {
    await page.getByRole('button', { name: '新建员工', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '新建员工', exact: true })
    await dialog.getByLabel('姓名', { exact: true }).fill(name)
    await dialog.getByLabel('编号', { exact: true }).fill(`编号${name}`)
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(dialog).toHaveCount(0)
  }

  await save(first)
  await started
  await save(second)
  await expect(page.getByText(second, { exact: true })).toBeVisible()
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/list_staff'))
  releaseOld()
  await (await lateResponse).finished()
  // eslint-disable-next-line no-restricted-syntax -- 旧列表被丢弃后没有可观察的状态变化，响应接收完成也不能确认页面回调已执行。
  await page.waitForTimeout(500)
  await expect(page.getByText(second, { exact: true })).toBeVisible()
})

for (const kind of ['档案', '角色', '账号']) {
  test(`员工${kind}旧保存只刷新详情，不关闭重新打开的窗口`, async ({ page }) => {
    const token = await apiLogin(owner.account, owner.password)
    const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
    const a = await api(token, 'create_staff', { name: `员工甲${suffix}`, number: `甲${suffix}`, role_ids: [] })
    await login(page, owner.account, owner.password)
    await page.goto(`/staff/${a.id}`)
    await expect(page.getByRole('heading', { level: 1, name: a.name })).toBeVisible()
    let release!: () => void
    const blocked = new Promise<void>((resolve) => {
      release = resolve
    })
    let started!: () => void
    const pending = new Promise<void>((resolve) => {
      started = resolve
    })
    const op = kind === '档案' ? 'update_staff' : kind === '角色' ? 'set_staff_roles' : 'set_staff_login'
    await page.route(`**/api/ops/${op}`, async (route) => {
      const response = await route.fetch()
      expect(response.ok()).toBe(true)
      // 回包前再改变甲的编号，详情刷新必须读到这个值。
      await api(token, 'update_staff', { staff_id: a.id, name: kind === '档案' ? `${a.name}改` : a.name, number: `刷新${suffix}` })
      started()
      await blocked
      await route.fulfill({ response })
    })
    const action = kind === '档案' ? '修改档案' : kind === '角色' ? '设置角色' : '登录账号'
    await profileAction(page, action)
    const title = kind === '档案' ? '修改档案' : `${kind === '角色' ? '设置角色' : '登录账号'}：${a.name}`
    const dialog = page.getByRole('dialog', { name: title, exact: true })
    if (kind === '档案') await dialog.getByLabel('姓名', { exact: true }).fill(`${a.name}改`)
    if (kind === '账号') {
      await dialog.getByLabel('账号', { exact: true }).fill(`guard-${suffix}`)
      await dialog.getByLabel('新密码（至少 8 位）', { exact: true }).fill('guard-password')
    }
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await pending
    await dialog.getByRole('button', { name: '关闭', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    // 重新打开同一个窗口：迟到的旧保存只刷新详情，不关它、不报成功。
    await profileAction(page, action)
    await expect(dialog).toBeVisible()
    if (kind === '档案') await dialog.getByLabel('编号', { exact: true }).fill(`新开${suffix}`)
    release()
    await expect(page.getByTestId('staff-fields')).toContainText(`刷新${suffix}`)
    await expect(dialog).toBeVisible()
    if (kind === '档案') await expect(dialog.getByLabel('编号', { exact: true })).toHaveValue(`新开${suffix}`)
    await expect(page.getByText(kind === '档案' ? '已保存' : kind === '角色' ? '角色已更新' : '登录账号已设置', { exact: true })).toHaveCount(0)
  })
}

test('新建员工断网后恢复并可重试', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/staff')
  await page.getByRole('button', { name: '新建员工', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新建员工', exact: true })
  const name = `重试员工${Date.now()}`
  await dialog.getByLabel('姓名', { exact: true }).fill(name)
  await dialog.getByLabel('编号', { exact: true }).fill(`编号${name}`)
  let abort = true
  await page.route('**/api/ops/create_staff', async (route) => {
    if (abort) {
      abort = false
      await route.abort()
    } else await route.continue()
  })
  const button = dialog.getByRole('button', { name: '保存', exact: true })
  await button.click()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(button).toBeEnabled()
  await button.click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText(name, { exact: true })).toBeVisible()
})

test('员工档案可以设置顾客在小程序预约', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const created = await api<{ id: string, name: string }>(token, 'create_staff', {
    name: `可约员工${suffix}`,
    number: suffix,
    role_ids: []
  })
  await login(page, owner.account, owner.password)
  await page.goto('/staff')
  const row = staffRow(page, created.name)
  // 不可约时那句话里不写，可约才写「小程序可约」。
  await expect(row).toBeVisible()
  await expect(row).not.toContainText('小程序可约')
  // 点一行进员工详情。
  await row.click()
  await expect(page).toHaveURL(`/staff/${created.id}`)
  await expect(page.getByTestId('parent-crumb')).toHaveText('员工')
  await profileAction(page, '修改档案')
  const dialog = page.getByRole('dialog', { name: '修改档案', exact: true })
  const checkbox = dialog.getByRole('checkbox', { name: '顾客可在小程序预约', exact: true })
  await expect(checkbox).not.toBeChecked()
  await checkbox.check()
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(page.locator('[data-field="预约"]')).toHaveText('顾客可以在小程序约')
  await page.getByTestId('parent-crumb').click()
  await expect(staffRow(page, created.name)).toContainText('小程序可约')
})

test('员工名册默认只列在职，停用的在「已停用」里，可以搜索和恢复', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const kept = await api<{ id: string, name: string }>(token, 'create_staff', { name: `在职员工${suffix}`, number: `在${suffix}`, role_ids: [] })
  const gone = await api<{ id: string, name: string }>(token, 'create_staff', { name: `停用员工${suffix}`, number: `停${suffix}`, role_ids: [] })
  await api(token, 'deactivate_staff', { staff_id: gone.id })
  await login(page, owner.account, owner.password)
  await page.goto('/staff')

  await expect(page.locator('[data-filter=active]')).toHaveAttribute('aria-pressed', 'true')
  await expect(staffRow(page, kept.name)).toBeVisible()
  await expect(staffRow(page, kept.name)).toContainText('没开登录账号')
  await expect(staffRow(page, gone.name)).toHaveCount(0)

  await page.getByLabel('搜索姓名或编号').fill(`停${suffix}`)
  await expect(page.getByText('没有找到这位员工', { exact: true })).toBeVisible()
  await page.locator('[data-filter=inactive]').click()
  await expect(page.locator('[data-filter=inactive]')).toHaveAttribute('aria-pressed', 'true')
  await expect(staffRow(page, gone.name).getByText('已停用', { exact: true })).toBeVisible()
  await expect(page.getByTestId('staff-row')).toHaveCount(1)

  await staffRow(page, gone.name).click()
  await profileAction(page, '恢复')
  await expect(page.getByText('已恢复', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { level: 1 }).getByText('已停用', { exact: true })).toHaveCount(0)
  await page.getByTestId('parent-crumb').click()
  await expect(page.locator('[data-filter=active]')).toHaveAttribute('aria-pressed', 'true')
  await page.getByLabel('搜索姓名或编号').fill(`停${suffix}`)
  await expect(staffRow(page, gone.name)).toBeVisible()
})
