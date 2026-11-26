import { expect, test } from '@playwright/test'
import type { components } from '../app/utils/api'
import { commissionExplanation } from '../app/utils/commission'
import { yuan } from '../app/utils/money'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'

type Schema = components['schemas']

async function seedCheckout() {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const position = await api<{ id: string }>(token, 'create_position', { name: `首页岗位${suffix}` })
  const stylist = await hire('发型师')
  const item = await api<{ id: string }>(token, 'create_item', {
    name: `首页项目${suffix}`,
    price: '88.00',
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    positions: [{ position_id: position.id, required: true }],
    level_prices: []
  })
  const created = await api<{ customer: { id: string } }>(token, 'create_customer', {
    name: `首页顾客${suffix}`,
    phone: `136${String(Date.now()).slice(-8)}`
  })
  const order = await api<{ id: string, net: string }>(token, 'open_order', {
    customer_id: created.customer.id,
    gender: 'female',
    designated: false,
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: stylist.id }] }]
  })
  await api(token, 'checkout_orders', {
    orders: [{ order_id: order.id, payments: [{ method: 'cash', amount: order.net }] }]
  })
  return { stylist, itemName: `首页项目${suffix}` }
}

test('发型师是个人首页，打卡后按钮消失并显示打卡时间', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await expect(page.getByTestId('home-punch')).toBeVisible()
  await expect(page.getByTestId('home-next')).toBeVisible()
  await page.getByTestId('home-punch').click()
  await expect(page.getByTestId('home-punch')).toHaveCount(0)
  await expect(page.getByTestId('home-punch-time')).toContainText('已打卡')
})

// 提成按读取时的方案算，别的用例会改今天的方案，所以期望值取自本人的 list_commissions，而不是写死比例。
test('发型师在首页看到今天的提成，点开看每笔怎么算', async ({ page }) => {
  const { stylist, itemName } = await seedCheckout()
  const token = await apiLogin(stylist.account, stylist.password)
  const { business_date: today } = await api<{ business_date: string }>(await apiLogin(owner.account, owner.password), 'get_settings', {})
  const earned = await api<Schema['CommissionList']>(token, 'list_commissions', { date_from: today, date_to: today, staff_id: stylist.id })
  expect(earned.entries).toHaveLength(1)
  await login(page, stylist.account, stylist.password)
  const card = page.getByTestId('home-my-commission')
  await expect(card).toContainText(yuan(earned.total))
  await card.click()
  const sheet = page.getByRole('dialog', { name: `我今天的提成 ${yuan(earned.total)}`, exact: true })
  const list = sheet.getByTestId('home-commission-list')
  await expect(list.getByRole('listitem')).toHaveCount(1)
  await expect(list).toContainText(`项目业绩${itemName}`)
  await expect(list).toContainText(commissionExplanation(earned.entries[0]!))
})

test('从个人菜单打卡后，首页的打卡按钮随即消失', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await expect(page.getByTestId('home-punch')).toBeVisible()
  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '打卡' }).click()
  await expect(page.getByTestId('home-punch')).toHaveCount(0)
  await expect(page.getByTestId('home-punch-time')).toContainText('已打卡')
})

test('店长首页的接下来把今天做完的收成一行，写营业额，点开是营业报表', async ({ page }) => {
  await seedCheckout()
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  const done = page.getByTestId('next-done')
  await expect(done).toHaveText(/做完 \d+ 位 · ¥[\d,]+/)
  await done.getByRole('link').click()
  await expect(page).toHaveURL(/\/reports\/business(\?|$)/)
})

// 迟到要过去的时间，后端不让约过去的时段：这一条固定浏览器时钟，预约列表和服务记录用假数据，只测首页怎么排、怎么写。
test('前台首页的接下来按时间排：正在做的写配方和还要多久，过时没到的提醒打电话，两小时后的收成一行', async ({ page }) => {
  const front = await hire('前台')
  const { business_date: today } = await api<{ business_date: string }>(await apiLogin(owner.account, owner.password), 'get_settings', {})
  const at = (clock: string) => `${today}T${clock}:00+08:00`
  await page.clock.install({ time: new Date(at('15:00')) })
  const staff = { id: '00000000-0000-0000-0000-0000000000a1', name: '阿杰' }
  const entry = (id: string, name: string, item: string, starts: string, ends: string, extra: Partial<Schema['Appointment']> = {}): Schema['Appointment'] => ({
    id,
    kind: 'appointment',
    status: 'confirmed',
    customer: { id: `c-${id}`, name, phone: null, phone_masked: true },
    staff,
    items: [{ item_id: `i-${id}`, name: item, duration_minutes: 60, position: 1 }],
    starts_at: at(starts),
    ends_at: at(ends),
    order_id: null,
    note: null,
    events: [],
    created_at: at('09:00'),
    created_by: { id: staff.id, kind: 'staff', name: staff.name },
    ...extra
  })
  await page.route('**/api/ops/list_appointments', route => route.fulfill({ json: {
    entries: [
      entry('done', '周女士', '剪发', '11:00', '12:00', { status: 'completed' }),
      entry('serving', '王女士', '染发', '14:00', '15:50', { order_id: 'order-1' }),
      entry('late', '林小姐', '护理', '14:30', '15:30'),
      entry('soon', '新客', '洗剪吹', '16:00', '17:00', { status: 'pending' }),
      entry('later', '张女士', '烫发', '18:30', '20:00')
    ],
    walk_ins: []
  } }))
  await page.route('**/api/ops/list_service_history', async (route) => {
    const { customer_id: id } = route.request().postDataJSON() as { customer_id: string }
    const items = id === 'c-serving'
      ? [{ business_date: `${today.slice(0, 4)}-09-12`, item: { id: 'i-x', name: '染发' }, category: null, order_id: 'o', order_item_id: 'oi', staff: [], record: { id: 'r', order_id: 'o', order_item_id: 'oi', formula: '7/1 + 6% 双氧 1:1.5', result: null, feedback: null, recorded_by: '阿杰', created_at: at('09:00'), updated_at: at('09:00') } }]
      : []
    await route.fulfill({ json: { items, total: items.length } })
  })

  await login(page, front.account, front.password)
  const next = page.getByTestId('home-next')
  await expect(next.getByTestId('next-done')).toHaveText('之前做完 1 位')
  const rows = next.getByTestId('next-row')
  await expect(rows).toHaveCount(3)
  await expect(rows.nth(0)).toHaveAttribute('data-state', 'serving')
  await expect(rows.nth(0)).toContainText('王女士')
  await expect(rows.nth(0)).toContainText('上次 9/12 染发，配方 7/1 + 6% 双氧 1:1.5')
  await expect(rows.nth(0)).toContainText(/还要 \d+ 分钟/)
  await expect(rows.nth(1)).toContainText('林小姐')
  await expect(rows.nth(1)).toContainText('过了 30 分钟没到 · 打个电话')
  await expect(rows.nth(2)).toContainText('新客')
  await expect(rows.nth(2)).toContainText('待确认')
  await expect(next.getByTestId('next-later')).toHaveText('之后还有 1 位 · 18:30 张女士')
})

test('发型师首页的在店里按轮牌顺序列人，写出自己排第几', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const first = await hire('发型师')
  const me = await hire('发型师')
  const queue = await api<{ id: string }>(token, 'create_rotation_queue', { name: `首页牌${uniqueSuffix()}`.slice(0, 20) })
  await api(token, 'set_queue_members', { queue_id: queue.id, staff_ids: [first.id, me.id] })
  for (const s of [first, me]) await api(token, 'join_rotation', { queue_id: queue.id, staff_id: s.id })

  await login(page, me.account, me.password)
  const rows = page.getByTestId('home-floor').getByTestId('floor-row')
  await expect(rows).toHaveCount(2)
  await expect(rows.nth(0)).toContainText(first.name)
  await expect(rows.nth(0)).toContainText('轮到')
  await expect(rows.nth(1)).toContainText(`${me.name}（我）`)
  await expect(rows.nth(1)).toContainText('第 2 位')
})

test('打卡还没回来就切到别的页，回来后打卡按钮还能按', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  let release!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  let started!: () => void
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/clock_in', async (route) => {
    started()
    await blocked
    await route.abort()
  }, { times: 1 })
  await page.getByTestId('home-punch').click()
  await pending
  const nav = page.getByTestId('sidebar-nav')
  await nav.locator('a:not([href="/"])').first().click()
  await expect(page).not.toHaveURL(/\/$/)
  release()
  await expect(page.getByText('网络连接失败，请重试', { exact: true })).toBeVisible()
  await nav.locator('a[href="/"]').click()
  await expect(page.getByTestId('home-punch')).toBeEnabled()
})
