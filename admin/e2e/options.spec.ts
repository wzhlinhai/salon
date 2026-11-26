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

test('添加级别断网后恢复并可重试', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/options')
  const form = page.locator('form').filter({ has: page.getByPlaceholder('新级别', { exact: true }) })
  const name = `重试级别${Date.now()}`
  await form.getByRole('textbox').fill(name)
  let abort = true
  await page.route('**/api/ops/create_level', async (route) => {
    if (abort) {
      abort = false
      await route.abort()
    } else await route.continue()
  })
  const button = form.getByRole('button', { name: '添加', exact: true })
  await button.click()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(button).toBeEnabled()
  await button.click()
  await expect(form.getByRole('textbox')).toHaveValue('')
  await expect(page.locator(`input[value="${name}"]`)).toHaveValue(name)
})

test('级别列表失败显示错误提示', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.route('**/api/ops/list_levels', route => route.abort())
  await page.goto('/options')
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
})

test('项目类别可设置业绩比例并停用', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  await login(page, owner.account, owner.password)
  await page.goto('/options')
  const card = page.locator('[data-category=item_categories]')
  const name = `类别${suffix}`
  await card.getByLabel('新类别', { exact: true }).fill(name)
  await card.getByLabel('新类别业绩比例', { exact: true }).fill('90')
  await card.getByRole('button', { name: '添加', exact: true }).click()
  const row = card.locator(`[data-category-row][data-category-name="${name}"]`)
  await expect(row.getByLabel(`类别 ${name}`, { exact: true })).toHaveValue(name)
  await expect(row.getByLabel(`${name}业绩比例`, { exact: true })).toHaveValue('90')
  const deactivated = page.waitForResponse(r => r.url().endsWith('/api/ops/update_item_category'))
  await row.getByRole('switch').click()
  await expect(row.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  expect((await deactivated).ok()).toBe(true)
  const result = await api<{ items: { name: string, performance_ratio: number, active: boolean }[] }>(token, 'list_item_categories', {})
  const saved = result.items.find(item => item.name === name)!
  expect(saved.performance_ratio).toBe(90)
  expect(saved.active).toBe(false)
})

test('项目类别添加断网后恢复并可重试', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/options')
  const card = page.locator('[data-category=item_categories]')
  const form = card.locator('form')
  const name = `重试类别${Date.now()}`
  await form.getByLabel('新类别', { exact: true }).fill(name)
  let abort = true
  await page.route('**/api/ops/create_item_category', async (route) => {
    if (abort) {
      abort = false
      await route.abort()
    } else await route.continue()
  })
  const button = form.getByRole('button', { name: '添加', exact: true })
  await button.click()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await expect(button).toBeEnabled()
  await button.click()
  await expect(form.getByLabel('新类别', { exact: true })).toHaveValue('')
  await expect(card.locator(`[data-category-row][data-category-name="${name}"]`)).toBeVisible()
})

test('项目类别迟到的旧列表不覆盖第二次保存', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  const seed = await api<{ id: string, name: string }>(token, 'create_item_category', { name: `延迟类别${suffix}`, performance_ratio: 90 })
  await login(page, owner.account, owner.password)
  await page.goto('/options')
  const card = page.locator('[data-category=item_categories]')
  const row = card.locator(`[data-category-row][data-category-name="${seed.name}"]`)
  await expect(row).toBeVisible()
  let releaseOld!: () => void
  let startedOld!: () => void
  const blocked = new Promise<void>((resolve) => {
    releaseOld = resolve
  })
  const started = new Promise<void>((resolve) => {
    startedOld = resolve
  })
  let requests = 0
  await page.route('**/api/ops/list_item_categories', async (route) => {
    if (++requests === 1) {
      const response = await route.fetch()
      startedOld()
      await blocked
      await route.fulfill({ response })
    } else {
      await route.continue()
    }
  })
  const firstSave = page.waitForResponse(r => r.url().endsWith('/api/ops/update_item_category'))
  await row.getByRole('switch').click()
  await firstSave
  await started
  const second = `第二次类别${suffix}`
  await api(token, 'create_item_category', { name: second, performance_ratio: 100 })
  const secondSave = page.waitForResponse(r => r.url().endsWith('/api/ops/update_item_category'))
  await row.getByRole('switch').click()
  await secondSave
  const secondRow = card.locator(`[data-category-row][data-category-name="${second}"]`)
  await expect(secondRow).toBeVisible()
  const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/ops/list_item_categories'))
  releaseOld()
  await (await lateResponse).finished()
  // eslint-disable-next-line no-restricted-syntax -- 旧列表被丢弃后没有可观察的状态变化，等待响应回调执行完。
  await page.waitForTimeout(500)
  await expect(secondRow).toBeVisible()
})
