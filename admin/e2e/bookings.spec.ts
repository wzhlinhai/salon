import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, hire, login, navLabels, owner, uniqueSuffix } from './helpers'
import { env } from './env'
import { addDays } from '../app/utils/datetime'

interface Hours { open: string, close: string }
interface Customer { id: string, name: string, phone: string }
interface Seeded {
  token: string
  staff: { id: string, name: string, account: string, password: string }
  item: { id: string, name: string }
  customer: Customer
  suffix: string
}

function clockMinutes(value: string) {
  const [hour, minute] = value.split(':').map(Number)
  return (hour ?? 0) * 60 + (minute ?? 0)
}

function clockText(total: number) {
  const hour = Math.floor(total / 60) % 24
  const minute = total % 60
  return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`
}

function clockAt(hours: Hours, offset: number) {
  const open = clockMinutes(hours.open)
  const close = clockMinutes(hours.close)
  return clockText(open + Math.max(0, Math.min(offset, close - open - 15)))
}

function phone() {
  return `139${String(Math.floor(Math.random() * 100_000_000)).padStart(8, '0')}`
}

async function settings(token: string, date?: string) {
  const body = date ? { date } : {}
  const data = await api<{ business_date: string, settings: { key: string, value: unknown }[] }>(token, 'get_settings', body)
  const setting = data.settings.find(item => item.key === 'business_hours')
  return { businessDate: data.business_date, hours: setting?.value as Hours }
}

async function seed(): Promise<Seeded> {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const staff = await hire('发型师')
  const item = await api<{ id: string, name: string }>(token, 'create_item', {
    name: `预约项目${suffix}`,
    kind: 'service',
    price: '100.00',
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    duration_minutes: 60,
    bookable: true,
    positions: [],
    level_prices: [],
    member_prices: []
  })
  const created = await api<{ customer: Customer }>(token, 'create_customer', { name: `预约顾客${suffix}`, phone: phone() })
  return { token, staff, item, customer: created.customer, suffix }
}

async function fillDay(page: Page, day: string) {
  const appointments = page.waitForResponse((response) => {
    if (!response.url().endsWith('/api/ops/list_appointments')) return false
    return (response.request().postDataJSON() as { from?: string }).from === day
  })
  const daySettings = page.waitForResponse((response) => {
    if (!response.url().endsWith('/api/ops/get_settings')) return false
    return (response.request().postDataJSON() as { date?: string }).date === day
  })
  await page.getByLabel('营业日').fill(day)
  await Promise.all([appointments, daySettings])
}

async function openSlot(page: Page, staffID: string, time: string, choice: '代约' | '私约占位') {
  await page.locator(`[data-slot-staff="${staffID}"][data-slot-time="${time}"]`).click()
  const chooser = page.getByRole('dialog', { name: '新增预约', exact: true })
  await chooser.getByRole('button', { name: choice, exact: true }).click()
}

function entry(page: Page, staffID: string, text: string) {
  return page.locator(`[data-staff-column="${staffID}"] [data-entry]`).filter({ hasText: text }).first()
}

async function openEntry(page: Page, staffID: string, text: string) {
  await entry(page, staffID, text).click()
  const dialog = page.getByRole('dialog', { name: '预约详情', exact: true })
  await expect(dialog.locator('[data-appointment-detail]')).toBeVisible()
  return dialog
}

async function apiBook(token: string, staffID: string, customerID: string, day: string, start: string, end: string) {
  return api<{ id: string }>(token, 'create_appointment', {
    customer_id: customerID,
    staff_id: staffID,
    starts_at: `${day}T${start}:00+08:00`,
    ends_at: `${day}T${end}:00+08:00`
  })
}

test('代约、改约、完成后显示全部状态变化', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  const start = clockAt(hours, 60)
  const end = clockAt(hours, 120)
  const moved = clockAt(hours, 180)

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)

  await openSlot(page, seeded.staff.id, start, '代约')
  const booking = page.getByRole('dialog', { name: '代约', exact: true })
  await expect(booking.getByRole('combobox', { name: '发型师', exact: true })).toContainText(seeded.staff.name)
  await expect(booking.getByLabel('开始时间', { exact: true })).toHaveValue(start)
  await booking.getByLabel('结束时间', { exact: true }).fill(end)
  await booking.getByRole('checkbox', { name: seeded.item.name }).click()
  await booking.getByLabel('搜索顾客').fill(seeded.customer.name)
  await booking.getByRole('button', { name: '搜索', exact: true }).click()
  await booking.locator('[data-customer-results]').getByRole('button', { name: new RegExp(seeded.customer.name) }).click()
  const reload = page.waitForResponse((response) => {
    if (!response.url().endsWith('/api/ops/list_appointments')) return false
    return (response.request().postDataJSON() as { from?: string }).from === tomorrow
  })
  await booking.getByRole('button', { name: '保存', exact: true }).click()
  await expect(booking).not.toBeVisible()
  await reload

  const confirmedEntry = entry(page, seeded.staff.id, seeded.customer.name)
  await expect(confirmedEntry).toHaveAttribute('data-entry-status', 'confirmed')
  await expect(confirmedEntry).toContainText(seeded.item.name)
  // 列头：头像 + 姓名 + 当天没取消的预约数。
  await expect(page.locator(`[data-staff-column="${seeded.staff.id}"]`).getByTestId('booking-column-head')).toContainText(`${seeded.staff.name}1 约`)
  const detail = await openEntry(page, seeded.staff.id, seeded.customer.name)
  await expect(detail.locator('[data-event-kind="create"]')).toHaveCount(1)

  await detail.getByRole('button', { name: '改约', exact: true }).click()
  const reschedule = page.getByRole('dialog', { name: '改约预约', exact: true })
  await reschedule.getByLabel('开始时间', { exact: true }).fill(moved)
  await reschedule.getByRole('button', { name: '保存', exact: true }).click()
  await expect(reschedule).not.toBeVisible()
  await expect(detail.locator('[data-event-kind="reschedule"]')).toHaveCount(1)
  await expect(detail).toContainText(`${moved}`)

  await detail.getByRole('button', { name: '完成', exact: true }).click()
  const complete = page.getByRole('dialog', { name: '完成预约', exact: true })
  await complete.getByRole('button', { name: '完成预约', exact: true }).click()
  await expect(complete).not.toBeVisible()
  await expect(detail.locator('[data-appointment-detail]')).toHaveAttribute('data-detail-status', 'completed')
  await expect(detail.locator('[data-event-kind="create"]')).toHaveCount(1)
  await expect(detail.locator('[data-event-kind="reschedule"]')).toHaveCount(1)
  await expect(detail.locator('[data-event-kind="complete"]')).toHaveCount(1)

  await page.keyboard.press('Escape')
  await expect(detail).not.toBeVisible()
  await expect(entry(page, seeded.staff.id, seeded.customer.name)).toHaveAttribute('data-entry-status', 'completed')
})

test('完成预约时列出这位顾客当天已结的单，只有一张时先选上，完成后关联', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  await apiBook(seeded.token, seeded.staff.id, seeded.customer.id, tomorrow, clockAt(hours, 60), clockAt(hours, 120))
  const order = await api<{ id: string, net: string }>(seeded.token, 'open_order', {
    customer_id: seeded.customer.id,
    gender: 'female',
    designated: false,
    items: [{ item_id: seeded.item.id, staff: [] }]
  })
  await api(seeded.token, 'checkout_orders', { orders: [{ order_id: order.id, payments: [{ method: 'cash', amount: order.net }] }] })

  // 预约不能约过去的时段，单又只能结在今天：约在明天，读单时先核对问的是预约那天，再换成今天去读真单。
  const asked: unknown[] = []
  await page.route('**/api/ops/list_orders', async (route) => {
    const body = route.request().postDataJSON() as { business_date?: string }
    asked.push(body)
    await route.continue({ postData: JSON.stringify({ ...body, business_date: today.businessDate }) })
  })
  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await fillDay(page, tomorrow)
  const detail = await openEntry(page, seeded.staff.id, seeded.customer.name)
  await detail.getByRole('button', { name: '完成', exact: true }).click()
  const complete = page.getByRole('dialog', { name: '完成预约', exact: true })
  const choice = complete.getByTestId('complete-orders').getByRole('button')
  await expect(choice).toHaveCount(1)
  await expect(choice).toContainText(seeded.item.name)
  await expect(choice).toHaveAttribute('aria-pressed', 'true')
  expect(asked).toContainEqual({ customer_id: seeded.customer.id, business_date: tomorrow, status: 'paid' })
  const saved = page.waitForRequest(r => r.url().endsWith('/api/ops/complete_appointment'))
  await complete.getByRole('button', { name: '完成预约', exact: true }).click()
  expect((await saved).postDataJSON()).toMatchObject({ order_id: order.id })
  await expect(complete).not.toBeVisible()
  await expect(detail).toContainText('已关联收银单')
})

test('时段冲突时显示后端提示', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  const firstStart = clockAt(hours, 60)
  const firstEnd = clockAt(hours, 120)
  const secondStart = clockAt(hours, 30)
  const secondEnd = clockAt(hours, 90)
  const existing = await apiBook(seeded.token, seeded.staff.id, seeded.customer.id, tomorrow, firstStart, firstEnd)

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)

  await openSlot(page, seeded.staff.id, secondStart, '代约')
  const booking = page.getByRole('dialog', { name: '代约', exact: true })
  await booking.getByLabel('结束时间', { exact: true }).fill(secondEnd)
  await booking.getByLabel('姓名', { exact: true }).fill(`冲突顾客${seeded.suffix}`)
  await booking.getByLabel('手机号', { exact: true }).fill(phone())
  await booking.getByRole('button', { name: '保存', exact: true }).click()

  await expect(page.getByText(`该时段已有预约或占位：${existing.id}`, { exact: true })).toBeVisible()
  await expect(booking).toBeVisible()
})

test('取消原因选其他必须填写说明，取消后保留记录并隐藏操作', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  const start = clockAt(hours, 60)
  const end = clockAt(hours, 120)
  await apiBook(seeded.token, seeded.staff.id, seeded.customer.id, tomorrow, start, end)
  const note = `顾客临时有事${seeded.suffix}`

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)

  const detail = await openEntry(page, seeded.staff.id, seeded.customer.name)
  await detail.getByRole('button', { name: '取消', exact: true }).click()
  const cancel = page.getByRole('dialog', { name: '取消预约', exact: true })
  await cancel.getByRole('combobox', { name: '取消原因', exact: true }).click()
  await page.getByRole('option', { name: '其他', exact: true }).click()
  const confirm = cancel.getByRole('button', { name: '取消预约', exact: true })
  await expect(confirm).toBeDisabled()
  await cancel.getByLabel('说明', { exact: true }).fill(note)
  await expect(confirm).toBeEnabled()
  await confirm.click()
  await expect(cancel).not.toBeVisible()
  await expect(detail.locator('[data-appointment-detail]')).toHaveAttribute('data-detail-status', 'cancelled')
  await expect(detail.locator('[data-event-kind="cancel"]')).toContainText('其他')
  await expect(detail.locator('[data-event-kind="cancel"]')).toContainText(note)
  await expect(detail.getByRole('button', { name: '完成', exact: true })).toHaveCount(0)
  await expect(detail.getByRole('button', { name: '改约', exact: true })).toHaveCount(0)

  await page.keyboard.press('Escape')
  await expect(detail).not.toBeVisible()
  await expect(entry(page, seeded.staff.id, seeded.customer.name)).toHaveAttribute('data-entry-status', 'cancelled')
})

test('私约占位可以新增和取消', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  const start = clockAt(hours, 180)
  const end = clockAt(hours, 240)

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)

  await openSlot(page, seeded.staff.id, start, '私约占位')
  const block = page.getByRole('dialog', { name: '私约占位', exact: true })
  await expect(block.getByRole('combobox', { name: '员工', exact: true })).toContainText(seeded.staff.name)
  await expect(block.getByLabel('开始时间', { exact: true })).toHaveValue(start)
  await block.getByLabel('结束时间', { exact: true }).fill(end)
  await block.getByLabel('备注', { exact: true }).fill(`私事${seeded.suffix}`)
  const reload = page.waitForResponse((response) => {
    if (!response.url().endsWith('/api/ops/list_appointments')) return false
    return (response.request().postDataJSON() as { from?: string }).from === tomorrow
  })
  await block.getByRole('button', { name: '保存', exact: true }).click()
  await expect(block).not.toBeVisible()
  await reload

  // 私约占位不算预约数。
  await expect(page.locator(`[data-staff-column="${seeded.staff.id}"]`).getByTestId('booking-column-head')).toContainText(`${seeded.staff.name}0 约`)
  const detail = await openEntry(page, seeded.staff.id, '私约占位')
  await expect(detail.locator('[data-appointment-detail]')).toHaveAttribute('data-detail-status', 'confirmed')
  await detail.getByRole('button', { name: '取消占位', exact: true }).click()
  await expect(detail.locator('[data-appointment-detail]')).toHaveAttribute('data-detail-status', 'cancelled')
  await expect(detail.locator('[data-event-kind="create"]')).toHaveCount(1)
  await expect(detail.locator('[data-event-kind="cancel"]')).toHaveCount(1)

  await page.keyboard.press('Escape')
  await expect(detail).not.toBeVisible()
  await expect(entry(page, seeded.staff.id, '私约占位')).toHaveAttribute('data-entry-status', 'cancelled')
})

test('已停用员工的列不能新增预约', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  await apiBook(seeded.token, seeded.staff.id, seeded.customer.id, tomorrow, clockAt(hours, 60), clockAt(hours, 120))
  await api(seeded.token, 'deactivate_staff', { staff_id: seeded.staff.id })

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)

  const column = page.locator(`[data-staff-column="${seeded.staff.id}"]`)
  await expect(column).toContainText(`${seeded.staff.name}（已停用）`)
  await expect(column.locator('[data-slot-staff]')).toHaveCount(0)
  await expect(entry(page, seeded.staff.id, seeded.customer.name)).toBeVisible()
})

test('重新打开代约窗口丢弃上一次顾客搜索的迟到结果', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  const start = clockAt(hours, 60)

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)

  await openSlot(page, seeded.staff.id, start, '代约')
  const first = page.getByRole('dialog', { name: '代约', exact: true })
  await first.getByLabel('搜索顾客').fill(seeded.customer.name)

  let release!: () => void
  let started!: () => void
  let finished!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  const staleDone = new Promise<void>((resolve) => {
    finished = resolve
  })
  let held = false
  await page.route('**/api/ops/search_customers', async (route) => {
    if (held) {
      await route.continue()
      return
    }
    held = true
    const response = await route.fetch()
    started()
    await blocked
    await route.fulfill({ response })
    finished()
  })

  await first.getByRole('button', { name: '搜索', exact: true }).click()
  await pending
  await page.keyboard.press('Escape')
  await expect(first).not.toBeVisible()

  await openSlot(page, seeded.staff.id, start, '代约')
  const second = page.getByRole('dialog', { name: '代约', exact: true })
  await expect(second).toBeVisible()
  release()
  await staleDone
  // eslint-disable-next-line no-restricted-syntax -- 旧搜索被丢弃时没有可观察的 DOM 变化；等响应回调执行后再断言新窗口没有结果。
  await page.waitForTimeout(500)
  await expect(second.locator('[data-customer-results]')).toHaveCount(0)
})

// 过时没到要过去的时间，后端不让约过去的时段：固定浏览器时钟，预约列表用假数据，只测页头怎么数、块怎么写。
test('今天的页头写约了几位、几位没到；过时没到的写打个电话，过了 90 分钟不再提醒；现在的时间标在左栏', async ({ page }) => {
  const seeded = await seed()
  const { businessDate: today, hours } = await settings(seeded.token)
  const at = (offset: number) => `${today}T${clockAt(hours, offset)}:00+08:00`
  await page.clock.install({ time: new Date(at(200)) })
  const staff = { id: seeded.staff.id, name: seeded.staff.name }
  const booking = (id: string, name: string, start: number, extra: Record<string, unknown> = {}) => ({
    id,
    kind: 'appointment',
    status: 'confirmed',
    customer: { id: `c-${id}`, name, phone: null, phone_masked: true },
    staff,
    items: [{ item_id: seeded.item.id, name: seeded.item.name, duration_minutes: 60, position: 1 }],
    starts_at: at(start),
    ends_at: at(start + 60),
    order_id: null,
    note: null,
    events: [],
    created_at: at(0),
    created_by: { id: staff.id, kind: 'staff', name: staff.name },
    ...extra
  })
  await page.route('**/api/ops/list_appointments', route => route.fulfill({ json: {
    entries: [
      booking('stale', '早约顾客', 60),
      booking('serving', '在做顾客', 150, { order_id: 'order-1' }),
      booking('late', '迟到顾客', 170),
      booking('soon', '稍后顾客', 260, { status: 'pending' }),
      booking('gone', '取消顾客', 230, { status: 'cancelled' }),
      booking('block', '', 300, { kind: 'block', customer: null, items: [], note: '私事' })
    ],
    walk_ins: []
  } }))

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByTestId('booking-day-summary')).toHaveText(/约了 4 位\s*1 位没到/)
  await expect(entry(page, staff.id, '迟到顾客')).toContainText('打个电话')
  await expect(entry(page, staff.id, '迟到顾客')).toContainText('过了 30 分钟没到')
  await expect(entry(page, staff.id, '早约顾客')).not.toContainText('打个电话')
  await expect(entry(page, staff.id, '在做顾客')).toContainText('正在做')
  await expect(page.getByTestId('booking-now')).toHaveText(clockAt(hours, 200))
})

test('员工列多到要横着滚时，时间栏钉在左边', async ({ page }) => {
  await seed()
  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  const grid = page.locator('[data-booking-grid]')
  const axis = page.getByTestId('booking-time-axis')
  await expect(axis).toBeVisible()
  // 把看板压到比「时间栏 + 一列员工」（56 + 160）还窄，库里只有一个员工也要横着滚。
  await grid.evaluate((el) => {
    el.style.width = '200px'
  })
  await expect.poll(() => grid.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(true)
  await grid.evaluate((el) => {
    el.scrollLeft = el.scrollWidth
  })
  await expect.poll(() => grid.evaluate(el => el.scrollLeft)).toBeGreaterThan(0)
  const gridBox = (await grid.boundingBox())!
  const axisBox = (await axis.boundingBox())!
  expect(Math.abs(axisBox.x - gridBox.x)).toBeLessThan(2)
})

test('未来已确认预约不显示记爽约', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  const start = clockAt(hours, 60)
  const end = clockAt(hours, 120)
  await apiBook(seeded.token, seeded.staff.id, seeded.customer.id, tomorrow, start, end)

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)

  const detail = await openEntry(page, seeded.staff.id, seeded.customer.name)
  await expect(detail.getByRole('button', { name: '完成', exact: true })).toBeVisible()
  await expect(detail.getByRole('button', { name: '改约', exact: true })).toBeVisible()
  await expect(detail.getByRole('button', { name: '记爽约', exact: true })).toHaveCount(0)
})

test('没有预约管理权限的员工看不到入口并进入无权限页', async ({ page }) => {
  const assistant = await hire('助理')
  await login(page, assistant.account, assistant.password)
  expect(await navLabels(page)).not.toContain('预约')
  await page.goto('/bookings')
  await expect(page).toHaveURL('/forbidden')
})

test('切换营业日丢弃过期的预约列表', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const dayAfter = addDays(today.businessDate, 2)
  const { hours: tomorrowHours } = await settings(seeded.token, tomorrow)
  const { hours: dayAfterHours } = await settings(seeded.token, dayAfter)
  const firstStart = clockAt(tomorrowHours, 60)
  const secondStart = clockAt(dayAfterHours, 120)
  const firstCustomer = { name: `过期顾客${seeded.suffix}`, id: seeded.customer.id }
  const second = await api<{ customer: Customer }>(seeded.token, 'create_customer', { name: `当日顾客${seeded.suffix}`, phone: phone() })
  await apiBook(seeded.token, seeded.staff.id, firstCustomer.id, tomorrow, firstStart, clockAt(tomorrowHours, 120))
  await apiBook(seeded.token, seeded.staff.id, second.customer.id, dayAfter, secondStart, clockAt(dayAfterHours, 150))

  await login(page, owner.account, owner.password)
  let release!: () => void
  let started!: () => void
  let finished!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  const staleDone = new Promise<void>((resolve) => {
    finished = resolve
  })
  let held = false
  await page.route('**/api/ops/list_appointments', async (route) => {
    const body = route.request().postDataJSON() as { from?: string }
    if (!held && body.from === tomorrow) {
      held = true
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
      finished()
      return
    }
    await route.continue()
  })

  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await page.getByLabel('营业日').fill(tomorrow)
  await pending
  await fillDay(page, dayAfter)
  await expect(entry(page, seeded.staff.id, second.customer.name)).toBeVisible()
  release()
  await staleDone
  // eslint-disable-next-line no-restricted-syntax -- 旧响应被丢弃时没有可观察的 DOM 变化；等响应回调执行后再断言它没有覆盖当天列表。
  await page.waitForTimeout(500)
  await expect(entry(page, seeded.staff.id, firstCustomer.name)).toHaveCount(0)
  await expect(entry(page, seeded.staff.id, second.customer.name)).toBeVisible()
})

test('详情窗口丢弃切换到另一条预约后的旧响应', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  const firstStart = clockAt(hours, 60)
  const secondStart = clockAt(hours, 180)
  const first = await apiBook(seeded.token, seeded.staff.id, seeded.customer.id, tomorrow, firstStart, clockAt(hours, 120))
  const secondCustomer = await api<{ customer: Customer }>(seeded.token, 'create_customer', { name: `另一顾客${seeded.suffix}`, phone: phone() })
  const second = await apiBook(seeded.token, seeded.staff.id, secondCustomer.customer.id, tomorrow, secondStart, clockAt(hours, 240))

  await login(page, owner.account, owner.password)
  let release!: () => void
  let started!: () => void
  let finished!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  const staleDone = new Promise<void>((resolve) => {
    finished = resolve
  })
  let held = false
  await page.route('**/api/ops/get_appointment', async (route) => {
    const body = route.request().postDataJSON() as { id?: string }
    if (!held && body.id === first.id) {
      held = true
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
      finished()
      return
    }
    await route.continue()
  })

  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)

  await entry(page, seeded.staff.id, seeded.customer.name).click()
  const dialog = page.getByRole('dialog', { name: '预约详情', exact: true })
  await pending
  await page.keyboard.press('Escape')
  await expect(dialog).not.toBeVisible()

  await entry(page, seeded.staff.id, secondCustomer.customer.name).click()
  await expect(dialog.locator('[data-detail-customer]')).toHaveText(secondCustomer.customer.name)
  release()
  await staleDone
  // eslint-disable-next-line no-restricted-syntax -- 旧详情被丢弃时没有可观察的 DOM 变化；等响应回调执行后再断言窗口仍是另一条预约。
  await page.waitForTimeout(500)
  await expect(dialog.locator('[data-detail-customer]')).toHaveText(secondCustomer.customer.name)
  await expect(dialog.locator('[data-appointment-detail]')).toHaveAttribute('data-detail-id', second.id)
  // 详情里点顾客名弹小卡片，看上次做了什么。
  await dialog.locator('[data-detail-customer]').click()
  await expect(page.getByTestId('customer-peek')).toContainText(secondCustomer.customer.name)
})

async function selfBookable(token: string, staffID: string) {
  const list = await api<{ staff: { id: string, name: string, number: string }[] }>(token, 'list_staff', {})
  const row = list.staff.find(person => person.id === staffID)
  if (!row) throw new Error(`staff ${staffID} missing`)
  await api(token, 'update_staff', { staff_id: staffID, name: row.name, number: row.number, self_bookable: true })
}

async function customerLogin(storeID: string, openid: string, phone: string): Promise<string> {
  const response = await fetch(`${env.baseURL}/api/customer/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ store_id: storeID, code: `fake-openid:${openid}`, phone_code: `fake-phone:${phone}` })
  })
  if (!response.ok) throw new Error(`customer login: ${response.status} ${await response.text()}`)
  return (await response.json()).token as string
}

async function customerApi<T = unknown>(token: string, op: string, body: object): Promise<T> {
  const response = await fetch(`${env.baseURL}/api/ops/${op}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
    body: JSON.stringify(body)
  })
  if (!response.ok) throw new Error(`${op}: ${response.status} ${await response.text()}`)
  return response.json() as Promise<T>
}

test('顾客自助预约显示顾客创建人并可确认', async ({ page }) => {
  const seeded = await seed()
  const today = await settings(seeded.token)
  const tomorrow = addDays(today.businessDate, 1)
  const { hours } = await settings(seeded.token, tomorrow)
  const start = clockAt(hours, 60)
  await selfBookable(seeded.token, seeded.staff.id)
  const store = await api<{ id: string }>(seeded.token, 'get_store', {})
  const customerToken = await customerLogin(store.id, `e2e-${seeded.suffix}`, seeded.customer.phone)
  await customerApi(customerToken, 'book_appointment', {
    staff_id: seeded.staff.id,
    starts_at: `${tomorrow}T${start}:00+08:00`,
    item_ids: [seeded.item.id]
  })

  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.getByLabel('营业日')).toHaveValue(today.businessDate)
  await fillDay(page, tomorrow)
  const entryRow = entry(page, seeded.staff.id, seeded.customer.name)
  await expect(entryRow).toHaveAttribute('data-entry-status', 'pending')

  const dialog = await openEntry(page, seeded.staff.id, seeded.customer.name)
  await expect(dialog.locator('[data-detail-created-by]')).toHaveAttribute('data-actor-kind', 'customer')
  await expect(dialog.locator('[data-detail-created-by]')).toContainText(`顾客 ${seeded.customer.name}`)
  await expect(dialog.locator('[data-event-actor]')).toHaveAttribute('data-actor-kind', 'customer')
  await expect(dialog.locator('[data-event-actor]')).toContainText(`顾客 ${seeded.customer.name}`)
  await dialog.getByRole('button', { name: '确认', exact: true }).click()
  await expect(dialog.locator('[data-appointment-detail]')).toHaveAttribute('data-detail-status', 'confirmed')
})

test('换一天读失败时原地给重试，不留着前一天的预约', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const today = await settings(token)
  const tomorrow = addDays(today.businessDate, 1)
  await login(page, owner.account, owner.password)
  await page.goto('/bookings')
  await expect(page.locator('[data-booking-grid]')).toBeVisible()
  let fail = true
  await page.route('**/api/ops/list_appointments', async (route) => {
    const body = route.request().postDataJSON() as { from?: string }
    if (fail && body.from === tomorrow) {
      fail = false
      await route.abort()
      return
    }
    await route.continue()
  })
  await page.getByLabel('营业日').fill(tomorrow)
  const error = page.getByTestId('load-error')
  await expect(error).toContainText('这天的预约没有读出来')
  await expect(page.locator('[data-booking-grid]')).toHaveCount(0)
  await expect(page.getByTestId('booking-day-summary')).not.toContainText('还没有人约')
  await error.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.locator('[data-booking-grid]')).toBeVisible()
  await expect(error).toHaveCount(0)
})
