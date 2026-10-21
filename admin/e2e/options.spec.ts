import { expect, test } from '@playwright/test'
import { api, apiLogin, login, owner } from './helpers'

test('级别岗位页迟到的旧列表不覆盖第二次保存', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const position = await api(token, 'create_position', { name: `岗位种子${suffix}` })
  await login(page, owner.account, owner.password)
  await page.goto('/options')
  const positions = page.locator('form').filter({ has: page.getByPlaceholder('新岗位', { exact: true }) }).locator('..')
  const seedRow = positions.locator('div.flex').filter({ has: page.locator(`input[value="${position.name}"]`) })
  await expect(seedRow.getByRole('textbox')).toHaveValue(position.name)
  let releaseOld!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseOld = resolve
  })
  let startedOld!: () => void
  const started = new Promise<void>((resolve) => {
    startedOld = resolve
  })
  let requests = 0
  await page.route('**/api/ops/list_positions', async (route) => {
    if (++requests === 1) {
      const response = await route.fetch()
      startedOld()
      await blocked
      await route.fulfill({ response })
    } else {
      await route.continue()
    }
  })
  const first = `第一次${suffix}`
  const second = `${first}第二次`

  async function save(name: string) {
    const form = page.locator('form').filter({ has: page.getByPlaceholder('新岗位', { exact: true }) })
    await form.getByRole('textbox').fill(name)
    await form.getByRole('button', { name: '添加', exact: true }).click()
    await expect(form.getByRole('textbox')).toHaveValue('')
  }

  await save(first)
  await started
  await save(second)
  const secondRow = positions.locator('div.flex').filter({ has: page.locator(`input[value="${second}"]`) })
  await expect(secondRow.getByRole('textbox')).toHaveValue(second)
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/list_positions'))
  releaseOld()
  await (await lateResponse).finished()
  // eslint-disable-next-line no-restricted-syntax -- 旧列表被丢弃后没有可观察的状态变化，响应接收完成也不能确认页面回调已执行。
  await page.waitForTimeout(500)
  await expect(secondRow.getByRole('textbox')).toHaveValue(second)
})

test('添加级别的旧响应不清空下一条输入', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/options')
  let release!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  let started!: () => void
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/create_level', async (route) => {
    const response = await route.fetch()
    started()
    await blocked
    await route.fulfill({ response })
  })
  const input = page.getByPlaceholder('新级别', { exact: true })
  const name = `添加竞态${Date.now()}`
  await input.fill(name)
  await input.press('Enter')
  await pending
  await input.fill(`${name}下一条`)
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/create_level'))
  release()
  await (await response).finished()
  await expect(page.locator(`input[value="${name}"]`)).toHaveValue(name)
  await expect(input).toHaveValue(`${name}下一条`)
})
