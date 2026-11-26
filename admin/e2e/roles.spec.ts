import { expect, test } from '@playwright/test'
import { login, owner } from './helpers'

test('角色页迟到的旧列表不覆盖第二次保存', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/roles')
  await expect(page.getByText('内置 · 全部权限', { exact: true })).toBeVisible()
  let releaseOld!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseOld = resolve
  })
  let startedOld!: () => void
  const started = new Promise<void>((resolve) => {
    startedOld = resolve
  })
  let requests = 0
  await page.route('**/api/ops/list_roles', async (route) => {
    if (++requests === 1) {
      const response = await route.fetch()
      startedOld()
      await blocked
      await route.fulfill({ response })
    } else {
      await route.continue()
    }
  })
  const first = `第一次${Date.now()}`
  const second = `${first}第二次`

  async function save(name: string) {
    await page.getByRole('button', { name: '新建角色', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '新建角色', exact: true })
    await dialog.getByLabel('角色名称', { exact: true }).fill(name)
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(dialog).toHaveCount(0)
  }

  await save(first)
  await started
  await save(second)
  await expect(page.getByText(second, { exact: true })).toBeVisible()
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/list_roles'))
  releaseOld()
  await (await lateResponse).finished()
  // eslint-disable-next-line no-restricted-syntax -- 旧列表被丢弃后没有可观察的状态变化，响应接收完成也不能确认页面回调已执行。
  await page.waitForTimeout(500)
  await expect(page.getByText(second, { exact: true })).toBeVisible()
})

test('角色旧保存只刷新列表，不关闭另一个角色窗口', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/roles')
  await expect(page.getByText('内置 · 全部权限', { exact: true })).toBeVisible()
  let release!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  let started!: () => void
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/create_role', async (route) => {
    const response = await route.fetch()
    started()
    await blocked
    await route.fulfill({ response })
  })
  await page.getByRole('button', { name: '新建角色', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新建角色', exact: true })
  const name = `角色保存竞态${Date.now()}`
  await dialog.getByLabel('角色名称', { exact: true }).fill(name)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await pending
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await page.locator('[data-role="前台"]').click()
  const editor = page.getByRole('dialog', { name: '编辑「前台」', exact: true })
  await expect(editor.getByLabel('角色名称', { exact: true })).toHaveValue('前台')
  release()
  await expect(page.getByText(name, { exact: true })).toBeVisible()
  await expect(editor).toBeVisible()
  await expect(editor.getByLabel('角色名称', { exact: true })).toHaveValue('前台')
  await expect(page.getByText(/^已(新建|改好)角色/)).toHaveCount(0)
})

test('保存角色断网后恢复并可重试', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/roles')
  await expect(page.getByText('内置 · 全部权限', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '新建角色', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新建角色', exact: true })
  const name = `重试角色${Date.now()}`
  await dialog.getByLabel('角色名称', { exact: true }).fill(name)
  let abort = true
  await page.route('**/api/ops/create_role', async (route) => {
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

test('角色天数非法文本不发出保存请求', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/roles')
  await expect(page.getByText('内置 · 全部权限', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '新建角色', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新建角色', exact: true })
  await dialog.getByLabel('角色名称', { exact: true }).fill('非法天数')
  await dialog.getByRole('switch', { name: '改单', exact: true }).click()
  await dialog.getByPlaceholder('不限天数', { exact: true }).fill('-')
  const requests: string[] = []
  await page.route('**/api/ops/create_role', async (route) => {
    requests.push(route.request().url())
    await route.abort()
  })
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByText('天数须为非负整数或留空', { exact: true })).toBeVisible()
  expect(requests).toEqual([])
})

test('删除角色先弹确认，不删了就留着', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/roles')
  await expect(page.getByText('内置 · 全部权限', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '新建角色', exact: true }).click()
  const editor = page.getByRole('dialog', { name: '新建角色', exact: true })
  const name = `待删角色${Date.now()}`
  await editor.getByLabel('角色名称', { exact: true }).fill(name)
  await editor.getByRole('button', { name: '保存', exact: true }).click()
  await expect(editor).toHaveCount(0)
  // 删除只在编辑侧滑底部，对照表上不放。
  const column = page.locator(`[data-role="${name}"]`)
  await expect(column).toBeVisible()
  const opened = page.getByRole('dialog', { name: `编辑「${name}」`, exact: true })

  const ask = page.getByRole('dialog', { name: `删除角色「${name}」？`, exact: true })
  await column.click()
  await opened.getByRole('button', { name: '删除这个角色', exact: true }).click()
  await expect(ask).toContainText('删了不能恢复')
  await ask.getByRole('button', { name: '不删了', exact: true }).click()
  await expect(ask).toHaveCount(0)
  await expect(column).toBeVisible()

  await opened.getByRole('button', { name: '删除这个角色', exact: true }).click()
  await ask.getByRole('button', { name: '删除角色', exact: true }).click()
  await expect(ask).toHaveCount(0)
  await expect(opened).toHaveCount(0)
  await expect(column).toHaveCount(0)
})

test('点红线只看那几项，再点一次回到全部', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/roles')
  const matrix = page.getByTestId('role-matrix')
  await expect(matrix.getByText('开单（含加项、挂单）', { exact: true })).toBeVisible()
  const money = page.locator('[data-line="money"]')
  await money.click()
  await expect(page.locator('[data-filter="line"]')).toHaveAttribute('aria-pressed', 'true')
  await expect(matrix.getByText('改价、打折', { exact: true })).toBeVisible()
  await expect(matrix.getByText('开单（含加项、挂单）', { exact: true })).toHaveCount(0)
  await money.click()
  await expect(page.locator('[data-filter="line"]')).toHaveCount(0)
  await expect(matrix.getByText('开单（含加项、挂单）', { exact: true })).toBeVisible()
})

test('点格子打开那个角色的编辑，并停在那一项', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/roles')
  await page.getByRole('button', { name: '前台：改价、打折', exact: false }).click()
  const editor = page.getByRole('dialog', { name: '编辑「前台」', exact: true })
  await expect(editor.locator('[data-perm="order.reprice"]')).toBeInViewport()
})
