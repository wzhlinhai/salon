import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'
import { api, apiLogin, login, owner } from './helpers'

// 员工列表的一行：第一列是头像、姓名和编号，按姓名精确找。
function staffRow(page: Page, name: string) {
  return page.getByRole('row').filter({ has: page.getByText(name, { exact: true }) })
}

// 行尾 ··· 菜单里的操作。
async function rowAction(page: Page, name: string, action: string) {
  await staffRow(page, name).getByRole('button', { name: `${name} 的操作`, exact: true }).click()
  await page.getByRole('menuitem', { name: action, exact: true }).click()
}

test('员工页迟到的旧列表不覆盖第二次保存', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/staff')
  await expect(page.getByRole('button', { name: /的操作$/ }).first()).toBeAttached()
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
  test(`员工${kind}旧保存只刷新列表，不关闭另一个员工窗口`, async ({ page }) => {
    const token = await apiLogin(owner.account, owner.password)
    const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
    const a = await api(token, 'create_staff', { name: `员工甲${suffix}`, number: `甲${suffix}`, role_ids: [] })
    const b = await api(token, 'create_staff', { name: `员工乙${suffix}`, number: `乙${suffix}`, role_ids: [] })
    await login(page, owner.account, owner.password)
    await page.goto('/staff')
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
      // 回包前再改变甲的编号，列表刷新必须读到这个值。
      await api(token, 'update_staff', { staff_id: a.id, name: kind === '档案' ? `${a.name}改` : a.name, number: `刷新${suffix}` })
      started()
      await blocked
      await route.fulfill({ response })
    })
    const action = kind === '档案' ? '修改档案' : kind === '角色' ? '设置角色' : '登录账号'
    await rowAction(page, a.name, action)
    const title = (name: string) => kind === '档案' ? '修改档案' : `${kind === '角色' ? '设置角色' : '登录账号'}：${name}`
    let dialog = page.getByRole('dialog', { name: title(a.name), exact: true })
    if (kind === '档案') await dialog.getByLabel('姓名', { exact: true }).fill(`${a.name}改`)
    if (kind === '账号') {
      await dialog.getByLabel('账号', { exact: true }).fill(`guard-${suffix}`)
      await dialog.getByLabel('新密码（至少 8 位）', { exact: true }).fill('guard-password')
    }
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await pending
    await dialog.getByRole('button', { name: '关闭', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    await rowAction(page, b.name, action)
    dialog = page.getByRole('dialog', { name: title(b.name), exact: true })
    if (kind === '档案') await expect(dialog.getByLabel('姓名', { exact: true })).toHaveValue(b.name)
    else await expect(dialog).toContainText(b.name)
    release()
    await expect(page.locator('[data-testid="main-panel"] tbody')).toContainText(`刷新${suffix}`)
    await expect(dialog).toBeVisible()
    if (kind === '档案') await expect(dialog.getByLabel('姓名', { exact: true })).toHaveValue(b.name)
    else await expect(dialog).toContainText(b.name)
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
  // 不可约时这一格留白，不写"不可预约"（DESIGN.md 列表页：空值留白）。
  await expect(row).toBeVisible()
  await expect(row.getByText('可约', { exact: true })).toHaveCount(0)
  await rowAction(page, created.name, '修改档案')
  const dialog = page.getByRole('dialog', { name: '修改档案', exact: true })
  const checkbox = dialog.getByRole('checkbox', { name: '顾客可在小程序预约', exact: true })
  await expect(checkbox).not.toBeChecked()
  await checkbox.check()
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(row.getByText('可约', { exact: true })).toBeVisible()

  // 点一行也打开档案。
  await row.getByText(created.name, { exact: true }).click()
  await expect(checkbox).toBeChecked()
  await checkbox.uncheck()
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(row.getByText('可约', { exact: true })).toHaveCount(0)
})

test('员工列表默认只列在职，停用的在「已停用」里，可以搜索和恢复', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const kept = await api<{ id: string, name: string }>(token, 'create_staff', { name: `在职员工${suffix}`, number: `在${suffix}`, role_ids: [] })
  const gone = await api<{ id: string, name: string }>(token, 'create_staff', { name: `停用员工${suffix}`, number: `停${suffix}`, role_ids: [] })
  await api(token, 'deactivate_staff', { staff_id: gone.id })
  await login(page, owner.account, owner.password)
  await page.goto('/staff')

  await expect(page.locator('[data-filter=active]')).toHaveAttribute('aria-pressed', 'true')
  await expect(staffRow(page, kept.name)).toBeVisible()
  await expect(staffRow(page, kept.name).getByText('未开通', { exact: true })).toBeVisible()
  await expect(staffRow(page, gone.name)).toHaveCount(0)

  await page.getByLabel('搜索姓名或编号').fill(`停${suffix}`)
  await expect(page.getByText('没有找到这位员工', { exact: true })).toBeVisible()
  await page.locator('[data-filter=inactive]').click()
  await expect(page.locator('[data-filter=inactive]')).toHaveAttribute('aria-pressed', 'true')
  await expect(staffRow(page, gone.name).getByText('已停用', { exact: true })).toBeVisible()
  await expect(page.getByTestId('staff-count')).toHaveText('共 1 人')

  await rowAction(page, gone.name, '恢复')
  await expect(page.getByText('已恢复', { exact: true })).toBeVisible()
  await expect(staffRow(page, gone.name)).toHaveCount(0)
  await page.locator('[data-filter=active]').click()
  await expect(staffRow(page, gone.name)).toBeVisible()
})
