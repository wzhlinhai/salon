import { expect, test, type Page } from '@playwright/test'
import { hire, login } from './helpers'

const NOW_ID = '0195b000-0000-7000-8000-000000000001'
const READ_ID = '0195b000-0000-7000-8000-000000000002'
const REPLY_ID = '0195b000-0000-7000-8000-000000000003'
const COUPON_ID = '0195b000-0000-7000-8000-0000000000aa'

function reminders(): { unread_count: number, items: { id: string, tier: string, subject_kind: string, subject_id: string, body: string, sent_at: string, read_at: string | null, replies: { id: string, tier: string, subject_kind: string, subject_id: string, body: string, sent_at: string, read_at: string | null }[] }[] } {
  const sentAt = new Date(Date.now() - 10 * 60_000).toISOString()
  return {
    unread_count: 2,
    items: [
      {
        id: NOW_ID, tier: 'now', subject_kind: 'coupon_redemption', subject_id: COUPON_ID,
        body: '美团券核销 30 分钟了还没关联单据，离撤销时限还有 15 分钟。', sent_at: sentAt, read_at: null,
        replies: [{ id: REPLY_ID, tier: 'now', subject_kind: 'coupon_redemption', subject_id: COUPON_ID, body: '还差 5 分钟到撤销时限。', sent_at: sentAt, read_at: null }]
      },
      {
        id: READ_ID, tier: 'daily', subject_kind: 'coupon_redemption', subject_id: COUPON_ID,
        body: '昨天有一张券关联晚了。', sent_at: sentAt, read_at: sentAt, replies: []
      }
    ]
  }
}

type Route = Parameters<Parameters<Page['route']>[1]>[0]

// mark 返回 true 表示后端存上了，之后的读取返回已读。
async function withReminders(page: Page, mark: (route: Route) => Promise<boolean>, shape: (body: ReturnType<typeof reminders>) => void = () => {}) {
  const sent: string[] = []
  let read = false
  await page.route('**/api/ops/list_my_reminders', async (route) => {
    const body = reminders()
    shape(body)
    if (read) {
      body.unread_count = 0
      body.items[0]!.read_at = body.items[0]!.sent_at
      body.items[0]!.replies[0]!.read_at = body.items[0]!.sent_at
    }
    await route.fulfill({ json: body })
  })
  await page.route('**/api/ops/mark_my_reminder_read', async (route) => {
    sent.push(route.request().postDataJSON().id)
    if (await mark(route)) read = true
  })
  return sent
}

test('我的提醒：侧栏写没看过的件数，首页列出，「知道了」后计数清掉', async ({ page }) => {
  const sent = await withReminders(page, async (route) => {
    await route.fulfill({ json: { id: route.request().postDataJSON().id } })
    return true
  })
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)

  const nav = page.getByTestId('reminders-nav')
  await expect(nav.getByTestId('reminders-unread')).toHaveText('1 件没看过')
  const home = page.getByTestId('home-reminders')
  // 有后续时首页写最新的那句。
  await expect(home).toContainText('还差 5 分钟到撤销时限。')
  await expect(home).toContainText('要马上处理')
  await expect(home).not.toContainText('昨天有一张券关联晚了')

  await home.getByRole('link').first().click()
  await expect(page).toHaveURL(/\/reminders$/)
  await expect(page.getByRole('heading', { level: 1, name: '我的提醒' })).toBeVisible()
  const item = page.locator(`[data-reminder="${NOW_ID}"]`)
  await expect(item).toContainText('没看过')
  await expect(item).toContainText('还差 5 分钟到撤销时限。')
  // 发型师没有收银权限，不给「去收银关联」。
  await expect(item.getByRole('link', { name: '去收银关联' })).toHaveCount(0)
  await expect(page.locator(`[data-reminder="${READ_ID}"]`).getByRole('button', { name: '知道了' })).toHaveCount(0)

  await item.getByRole('button', { name: '知道了' }).click()
  await expect(item).not.toContainText('没看过')
  await expect(item.getByRole('button', { name: '知道了' })).toHaveCount(0)
  await expect(nav.getByTestId('reminders-unread')).toHaveCount(0)
  expect(sent).toEqual([NOW_ID])

  await page.goto('/')
  await expect(page.getByTestId('home-greeting')).toBeVisible()
  await expect(page.getByTestId('home-reminders')).toHaveCount(0)
})

test('我的提醒：没存上时退回没看过并提示；能收银的人看得到「去收银关联」', async ({ page }) => {
  await withReminders(page, async (route) => {
    await route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ detail: '已读没存上' }) })
    return false
  })
  const cashier = await hire('店长')
  await login(page, cashier.account, cashier.password)
  await page.goto('/reminders')
  const item = page.locator(`[data-reminder="${NOW_ID}"]`)
  await expect(item.getByRole('link', { name: '去收银关联' })).toHaveAttribute('href', '/cashier')
  await item.getByRole('button', { name: '知道了' }).click()
  await expect(page.getByText('已读没存上', { exact: true })).toBeVisible()
  await expect(item).toContainText('没看过')
  await expect(page.getByTestId('reminders-nav').getByTestId('reminders-unread')).toHaveText('1 件没看过')
})

test('我的提醒：没有提醒时写一句，首页不出这一块', async ({ page }) => {
  await page.route('**/api/ops/list_my_reminders', route => route.fulfill({ json: { unread_count: 0, items: [] } }))
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await expect(page.getByTestId('home-greeting')).toBeVisible()
  await expect(page.getByTestId('home-reminders')).toHaveCount(0)
  await page.goto('/reminders')
  await expect(page.getByTestId('empty-state')).toContainText('没有提醒')
})

test('我的提醒：父条看过、后续没看过，这件事仍算没看过', async ({ page }) => {
  await withReminders(page, async (route) => {
    await route.fulfill({ json: { id: route.request().postDataJSON().id } })
    return true
  }, (body) => {
    body.items[0]!.read_at = body.items[0]!.sent_at
  })
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await expect(page.getByTestId('reminders-nav').getByTestId('reminders-unread')).toHaveText('1 件没看过')
  await page.goto('/reminders')
  const item = page.locator(`[data-reminder="${NOW_ID}"]`)
  await expect(item).toContainText('没看过')
  await item.getByRole('button', { name: '知道了' }).click()
  await expect(item).not.toContainText('没看过')
})

for (const missing of ['order.create', 'groupbuy.redeem']) {
  test(`我的提醒：缺 ${missing} 的人看不到「去收银关联」`, async ({ page }) => {
    await withReminders(page, async () => false)
    await page.route('**/api/ops/get_me', async (route) => {
      const response = await route.fetch()
      const me = await response.json()
      me.permissions = me.permissions.filter((p: { permission: string }) => p.permission !== missing)
      await route.fulfill({ response, json: me })
    })
    const manager = await hire('店长')
    await login(page, manager.account, manager.password)
    await page.goto('/reminders')
    const item = page.locator(`[data-reminder="${NOW_ID}"]`)
    await expect(item.getByRole('button', { name: '知道了' })).toBeVisible()
    await expect(item.getByRole('link', { name: '去收银关联' })).toHaveCount(0)
  })
}
