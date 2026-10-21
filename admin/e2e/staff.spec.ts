import { expect, test } from '@playwright/test'
import { login, owner } from './helpers'

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
