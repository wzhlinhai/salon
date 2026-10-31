import { expect, test } from '@playwright/test'
import { api, apiLogin, login, owner } from './helpers'

test('员工页迟到的旧列表不覆盖第二次保存', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/staff')
  await expect(page.getByRole('button', { name: '档案', exact: true }).first()).toBeVisible()
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
  await expect(page.getByRole('cell', { name: second, exact: true })).toBeVisible()
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/list_staff'))
  releaseOld()
  await (await lateResponse).finished()
  // eslint-disable-next-line no-restricted-syntax -- 旧列表被丢弃后没有可观察的状态变化，响应接收完成也不能确认页面回调已执行。
  await page.waitForTimeout(500)
  await expect(page.getByRole('cell', { name: second, exact: true })).toBeVisible()
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
    const rowA = page.getByRole('row').filter({ has: page.getByRole('cell', { name: a.name, exact: true }) })
    await rowA.getByRole('button', { name: kind, exact: true }).click()
    const title = (name: string) => kind === '档案' ? '修改档案' : `${kind === '角色' ? '设置角色' : '登录账号'}：${name}`
    let dialog = page.getByRole('dialog', { name: title(a.name), exact: true })
    if (kind === '档案') await dialog.getByLabel('姓名', { exact: true }).fill(`${a.name}改`)
    if (kind === '账号') {
      await dialog.getByLabel('账号', { exact: true }).fill(`guard-${suffix}`)
      await dialog.getByLabel('新密码（至少 8 位）', { exact: true }).fill('guard-password')
    }
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await pending
    await dialog.getByRole('button', { name: 'Close', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    await page.getByRole('row').filter({ has: page.getByRole('cell', { name: b.name, exact: true }) }).getByRole('button', { name: kind, exact: true }).click()
    dialog = page.getByRole('dialog', { name: title(b.name), exact: true })
    if (kind === '档案') await expect(dialog.getByLabel('姓名', { exact: true })).toHaveValue(b.name)
    else await expect(dialog).toContainText(b.name)
    release()
    await expect(page.locator('main tbody')).toContainText(`刷新${suffix}`)
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
  await expect(page.getByRole('cell', { name, exact: true })).toBeVisible()
})
