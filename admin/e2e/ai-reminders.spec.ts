import { expect, test, type Page } from '@playwright/test'
import { login, owner } from './helpers'

test('AI 提醒：切换档位后重新打开仍是新档位，「开」暂不可选', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.goto('/ai-reminders')
  const rule = page.locator('[data-rule="closing_yesterday_brief"]')
  await expect(rule.getByRole('heading', { name: '昨日日结简报' })).toBeVisible()
  const modes = rule.getByRole('group', { name: '昨日日结简报的档位' })
  await expect(modes.getByRole('button', { name: '开' })).toBeDisabled()
  await modes.getByRole('button', { name: '关' }).click()
  await expect(modes.getByRole('button', { name: '关' })).toHaveAttribute('aria-pressed', 'true')
  await page.reload()
  await expect(modes.getByRole('button', { name: '关' })).toHaveAttribute('aria-pressed', 'true')
  await modes.getByRole('button', { name: '观察' }).click()
  await expect(modes.getByRole('button', { name: '观察' })).toHaveAttribute('aria-pressed', 'true')
  await expect(rule.getByTestId('reminder-summary')).toContainText('最近 7 天')
})

const SAMPLE_ID = '0195a000-0000-7000-8000-000000000001'

async function withSample(page: Page) {
  await page.route('**/api/ops/get_reminder_summary', async (route) => {
    if (route.request().postDataJSON().rule_key !== 'closing_yesterday_brief') return route.fallback()
    const response = await route.fetch()
    const body = await response.json()
    body.emitted = 1
    body.ran_days = 1
    body.matched_days = 1
    body.average_per_recipient_day = 1
    body.samples = [{ id: SAMPLE_ID, business_date: '2026-11-26', recipient_name: '王老板', text: '昨天还没日结，删单 2 笔。', ai_written: true, facts: {}, feedback: null }]
    await route.fulfill({ response, json: body })
  })
}

test('AI 提醒：样例点「有用」发出评价，再点一次取消', async ({ page }) => {
  await withSample(page)
  const sent: unknown[] = []
  await page.route('**/api/ops/set_reminder_feedback', async (route) => {
    const body = route.request().postDataJSON()
    sent.push(body.feedback)
    await route.fulfill({ json: { id: body.reminder_id, feedback: body.feedback } })
  })
  await login(page, owner.account, owner.password)
  await page.goto('/ai-reminders')
  const sample = page.locator(`[data-reminder="${SAMPLE_ID}"]`)
  await expect(sample).toContainText('给王老板 · AI 写的')
  await expect(sample).toContainText('昨天还没日结，删单 2 笔。')
  const helpful = sample.getByRole('button', { name: '有用' })
  await helpful.click()
  await expect(helpful).toHaveAttribute('aria-pressed', 'true')
  await helpful.click()
  await expect(helpful).toHaveAttribute('aria-pressed', 'false')
  expect(sent).toEqual(['helpful', null])
})

test('AI 提醒：评价没存上时退回原样并提示', async ({ page }) => {
  await withSample(page)
  await page.route('**/api/ops/set_reminder_feedback', route => route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ detail: '评价没存上' }) }))
  await login(page, owner.account, owner.password)
  await page.goto('/ai-reminders')
  const helpful = page.locator(`[data-reminder="${SAMPLE_ID}"]`).getByRole('button', { name: '有用' })
  await helpful.click()
  await expect(page.getByText('评价没存上', { exact: true })).toBeVisible()
  await expect(helpful).toHaveAttribute('aria-pressed', 'false')
})
