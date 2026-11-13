import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, hire, login, navLabels, owner, uniqueSuffix } from './helpers'

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

function addDays(value: string, delta: number) {
  const [year, month, date] = value.split('-').map(Number)
  return new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, (date ?? 1) + delta)).toISOString().slice(0, 10)
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
  await complete.getByRole('button', { name: '确定', exact: true }).click()
  await expect(complete).not.toBeVisible()
  await expect(detail.locator('[data-appointment-detail]')).toHaveAttribute('data-detail-status', 'completed')
  await expect(detail.locator('[data-event-kind="create"]')).toHaveCount(1)
  await expect(detail.locator('[data-event-kind="reschedule"]')).toHaveCount(1)
  await expect(detail.locator('[data-event-kind="complete"]')).toHaveCount(1)

  await page.keyboard.press('Escape')
  await expect(detail).not.toBeVisible()
  await expect(entry(page, seeded.staff.id, seeded.customer.name)).toHaveAttribute('data-entry-status', 'completed')
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
  const confirm = cancel.getByRole('button', { name: '确定', exact: true })
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
})
