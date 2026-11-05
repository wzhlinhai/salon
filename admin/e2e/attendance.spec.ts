import { expect, test, type Locator, type Page } from '@playwright/test'
import type { components } from '../app/utils/api'
import { api, apiLogin, hire, login, navLabels, owner, uniqueSuffix } from './helpers'

// 考勤用例只改自己创建的员工、班次和指定，不影响其他 spec 读取门店级数据，放在默认项目运行。
type Schema = components['schemas']

function addDays(date: string, days: number) {
  const value = new Date(`${date}T00:00:00Z`)
  value.setUTCDate(value.getUTCDate() + days)
  return value.toISOString().slice(0, 10)
}

function previousMonth(month: string) {
  const [year, monthNumber] = month.split('-').map(Number)
  return new Date(Date.UTC(year!, monthNumber! - 2, 1)).toISOString().slice(0, 7)
}

function shortDate(date: string) {
  const [, monthNumber, day] = date.split('-')
  return `${Number(monthNumber)}月${Number(day)}日`
}

function shanghaiCalendarDate() {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  }).formatToParts(new Date())
  const value = Object.fromEntries(parts.map(part => [part.type, part.value]))
  return `${value.year}-${value.month}-${value.day}`
}

function tableRow(page: Page, testId: string, name: string) {
  return page.getByTestId(testId).locator('tbody tr').filter({ hasText: name })
}

async function choose(page: Page, scope: Page | Locator, label: string, option: string) {
  await scope.getByRole('combobox', { name: label, exact: true }).click()
  await page.getByRole('option', { name: option, exact: true }).click()
}

async function openShifts(page: Page) {
  await page.getByRole('tab', { name: '班次', exact: true }).click()
  await expect(page.getByTestId('shifts-table')).toBeVisible()
}

async function showMonthOf(page: Page, date: string, currentMonth: string) {
  const targetMonth = date.slice(0, 7)
  if (targetMonth === currentMonth) return
  const input = page.getByLabel('月份', { exact: true })
  await input.fill(targetMonth)
  await expect(input).toHaveValue(targetMonth)
}

async function createShift(page: Page, name: string, start: string, end: string) {
  await page.getByRole('button', { name: '新建班次', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新建班次', exact: true })
  await dialog.getByLabel('名称', { exact: true }).fill(name)
  await dialog.getByLabel('上班时间', { exact: true }).fill(start)
  await dialog.getByLabel('下班时间', { exact: true }).fill(end)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
}

async function assignShift(page: Page, staffName: string, shiftName: string, effectiveFrom: string, today: string) {
  await tableRow(page, 'staff-shifts-table', staffName).getByRole('button', { name: '指定班次', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: `指定班次：${staffName}`, exact: true })
  await choose(page, dialog, '班次', shiftName)
  const date = dialog.getByLabel('生效日期', { exact: true })
  await expect(date).toHaveAttribute('min', today)
  await expect(date).toHaveValue(today)
  await date.fill(effectiveFrom)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
}

async function addPunch(page: Page, staffName: string, date: string, time: string, reason: string, maxDate: string) {
  await page.getByRole('button', { name: '补卡', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '补卡', exact: true })
  await choose(page, dialog, '员工', staffName)
  await expect(dialog.getByLabel('日期', { exact: true })).toHaveAttribute('max', maxDate)
  await dialog.getByLabel('日期', { exact: true }).fill(date)
  await dialog.getByLabel('时间', { exact: true }).fill(time)
  await expect(dialog.getByLabel('原因', { exact: true })).toHaveAttribute('maxlength', '100')
  await dialog.getByLabel('原因', { exact: true }).fill(reason)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
}

async function addPunchFromDetail(page: Page, staffName: string, date: string, time: string, reason: string) {
  await page.getByRole('button', { name: '给该员工补卡', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '补卡', exact: true })
  await expect(dialog.getByRole('combobox', { name: '员工', exact: true })).toBeDisabled()
  await expect(dialog.getByRole('combobox', { name: '员工', exact: true })).toContainText(staffName)
  await dialog.getByLabel('日期', { exact: true }).fill(date)
  await dialog.getByLabel('时间', { exact: true }).fill(time)
  await dialog.getByLabel('原因', { exact: true }).fill(reason)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
}

async function businessDate() {
  const token = await apiLogin(owner.account, owner.password)
  const settings = await api<Schema['SettingList']>(token, 'get_settings', {})
  return { token, today: settings.business_date, month: settings.business_date.slice(0, 7) }
}

test('班次可以新建、指定和停用', async ({ page }) => {
  const { token, today } = await businessDate()
  const tomorrow = addDays(today, 1)
  const staff = await hire('发型师')
  const shiftName = `班次${uniqueSuffix()}`
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  await openShifts(page)
  await createShift(page, shiftName, '08:00', '17:00')

  const shift = tableRow(page, 'shifts-table', shiftName)
  await expect(shift.locator('td').nth(1)).toHaveText('08:00')
  await expect(shift.locator('td').nth(2)).toHaveText('17:00')
  await expect(shift.locator('td').nth(3)).toHaveText('在用')

  await assignShift(page, staff.name, `${shiftName} 08:00–17:00`, today, today)
  const staffShift = tableRow(page, 'staff-shifts-table', staff.name)
  await expect(staffShift.locator('td').nth(1)).toHaveText(`${shiftName} 08:00–17:00`)
  await assignShift(page, staff.name, `${shiftName} 08:00–17:00`, tomorrow, today)
  await expect(staffShift.locator('td').nth(2)).toContainText(`${shortDate(tomorrow)}起 ${shiftName} 08:00–17:00`)

  await shift.getByRole('button', { name: '停用', exact: true }).click()
  const confirmation = page.getByRole('dialog', { name: `停用班次「${shiftName}」？`, exact: true })
  await expect(confirmation).toContainText('已经指定的员工照常执行')
  await confirmation.getByRole('button', { name: '确认停用', exact: true }).click()
  await expect(shift.locator('td').nth(3)).toHaveText('已停用')
  await expect(shift.getByRole('button', { name: '停用', exact: true })).toHaveCount(0)
  await expect(shift.locator('td').last()).toHaveText('')

  await staffShift.getByRole('button', { name: '指定班次', exact: true }).click()
  const assignDialog = page.getByRole('dialog', { name: `指定班次：${staff.name}`, exact: true })
  const shiftSelect = assignDialog.getByRole('combobox', { name: '班次', exact: true })
  const assignSave = assignDialog.getByRole('button', { name: '保存', exact: true })
  await expect(shiftSelect).toContainText('选择班次')
  await expect(assignSave).toBeDisabled()
  await shiftSelect.click()
  await expect(page.getByRole('option', { name: `${shiftName} 08:00–17:00`, exact: true })).toHaveCount(0)
  await page.getByRole('option', { name: '不按班次', exact: true }).click()
  await expect(assignSave).toBeEnabled()
  await assignSave.click()
  await expect(assignDialog).toHaveCount(0)
  await expect(staffShift.locator('td').nth(1)).toHaveText('不按班次')

  await api(token, 'deactivate_staff', { staff_id: staff.id })
  await page.reload()
  await openShifts(page)
  const departed = tableRow(page, 'staff-shifts-table', staff.name)
  await expect(departed).toContainText('（离职）')
  await expect(departed.getByRole('button', { name: '指定班次', exact: true })).toHaveCount(0)
})

test('补卡、核准、撤销和作废后统计更新', async ({ page }) => {
  const { today, month } = await businessDate()
  const yesterday = addDays(today, -1)
  const calendarToday = shanghaiCalendarDate()
  const staff = await hire('发型师')
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  await expect(page.getByLabel('月份', { exact: true })).toHaveAttribute('max', today.slice(0, 7))
  await showMonthOf(page, yesterday, month)

  await addPunch(page, staff.name, yesterday, '10:00', '忘记打卡', calendarToday)
  const row = tableRow(page, 'attendance-table', staff.name)
  await expect(row.locator('td').nth(1)).toHaveText('1')
  await expect(row.locator('td').nth(4)).toHaveText('1 次')
  await row.click()

  const detail = page.getByTestId('attendance-detail')
  const day = detail.locator(`[data-testid="attendance-day"][data-date="${yesterday}"]`)
  await expect(day.getByText('缺卡', { exact: true })).toBeVisible()
  await expect(day.getByRole('button', { name: '核准', exact: true })).toBeVisible()
  await day.getByRole('button', { name: '核准', exact: true }).click()
  const approval = page.getByRole('dialog', { name: '核准', exact: true })
  await expect(approval).toContainText(yesterday)
  await expect(approval).toContainText('缺卡')
  await approval.getByLabel('原因', { exact: true }).fill('确认补卡有效')
  await approval.getByRole('button', { name: '保存', exact: true }).click()
  await expect(approval).toHaveCount(0)
  await expect(row.locator('td').nth(4)).toHaveText('0 次')
  await expect(row.locator('td').nth(5)).toHaveText('1 次')
  await expect(day).toContainText('已核准 · 确认补卡有效')
  await expect(day.getByRole('button', { name: '核准', exact: true })).toHaveCount(0)
  await expect(day.getByRole('button', { name: '撤销核准', exact: true })).toBeVisible()

  await day.getByRole('button', { name: '撤销核准', exact: true }).click()
  const revoke = page.getByRole('dialog', { name: '撤销核准', exact: true })
  await expect(revoke).toContainText(`撤销 ${yesterday} 的核准？`)
  await revoke.getByRole('button', { name: '确认撤销', exact: true }).click()
  await expect(revoke).toHaveCount(0)
  await expect(row.locator('td').nth(4)).toHaveText('1 次')
  await expect(row.locator('td').nth(5)).toHaveText('0 次')
  await expect(day.getByRole('button', { name: '撤销核准', exact: true })).toHaveCount(0)
  await expect(day.getByRole('button', { name: '核准', exact: true })).toBeVisible()

  await addPunchFromDetail(page, staff.name, yesterday, '19:00', '补下班卡')
  await expect(day.getByText('缺卡', { exact: true })).toHaveCount(0)
  await expect(day).toContainText('下班：19:00')
  await expect(row.locator('td').nth(4)).toHaveText('0 次')
  await expect(day.getByRole('button', { name: '核准', exact: true })).toHaveCount(0)
  const closingPunch = detail.getByTestId('attendance-punch').filter({ hasText: '19:00' })
  await expect(closingPunch).toContainText('补卡 · 补下班卡')
  await closingPunch.getByRole('button', { name: '作废', exact: true }).click()
  const voidDialog = page.getByRole('dialog', { name: '作废补卡', exact: true })
  await expect(voidDialog).toContainText('补下班卡')
  await voidDialog.getByLabel('作废原因', { exact: true }).fill('补错了')
  await voidDialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(voidDialog).toHaveCount(0)
  await expect(closingPunch).toContainText('已作废 · 补错了')
  await expect(closingPunch.getByRole('button', { name: '作废', exact: true })).toHaveCount(0)
  await expect(row.locator('td').nth(4)).toHaveText('1 次')
  await expect(day.getByText('缺卡', { exact: true })).toBeVisible()
})

test('迟到按班次的上班时间计算', async ({ page }) => {
  const { today } = await businessDate()
  const calendarToday = shanghaiCalendarDate()
  const staff = await hire('发型师')
  const shiftName = `全天${uniqueSuffix()}`
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  await openShifts(page)
  await createShift(page, shiftName, '00:00', '23:59')
  await page.getByRole('tab', { name: '月度考勤', exact: true }).click()
  // 上海时区 00:01 在午夜后的前两分钟可能还是未来；这个用例假定不在那两分钟内运行。
  await addPunch(page, staff.name, today, '00:01', '早到打卡', calendarToday)
  const row = tableRow(page, 'attendance-table', staff.name)
  await expect(row.locator('td').nth(2)).toHaveText('0 次 / 0 分钟')
  await openShifts(page)
  await assignShift(page, staff.name, `${shiftName} 00:00–23:59`, today, today)
  await page.getByRole('tab', { name: '月度考勤', exact: true }).click()
  await expect(row.locator('td').nth(2)).toHaveText('1 次 / 1 分钟')
  await row.click()
  const day = page.getByTestId('attendance-detail').locator(`[data-testid="attendance-day"][data-date="${today}"]`)
  await expect(day).toContainText('迟到 1 分钟')
})

test('本人打卡不能作废', async ({ page }) => {
  const staff = await hire('发型师')
  await api(await apiLogin(staff.account, staff.password), 'clock_in', {})
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  await tableRow(page, 'attendance-table', staff.name).click()
  const punch = page.getByTestId('attendance-punch').filter({ hasText: '本人打卡' })
  await expect(punch).toBeVisible()
  await expect(punch.getByRole('button', { name: '作废', exact: true })).toHaveCount(0)
})

test('发型师没有考勤权限', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  expect(await navLabels(page)).not.toContain('考勤')
  await page.goto('/attendance')
  await expect(page).toHaveURL('/forbidden')
})

test('清空月份后重新选择会重新加载统计', async ({ page }) => {
  const { month } = await businessDate()
  const staff = await hire('发型师')
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  const input = page.getByLabel('月份', { exact: true })
  const row = tableRow(page, 'attendance-table', staff.name)
  await expect(row).toBeVisible()
  await input.fill('')
  await expect(input).toHaveValue('')
  await expect(row).toHaveCount(0)
  await expect(page.getByTestId('attendance-table')).toHaveAttribute('aria-busy', 'false')
  await input.fill(month)
  await expect(row).toBeVisible()
})

test('清空月份后丢弃迟到的旧统计', async ({ page }) => {
  const { month } = await businessDate()
  const previous = previousMonth(month)
  const staff = await hire('发型师')
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  const input = page.getByLabel('月份', { exact: true })
  const row = tableRow(page, 'attendance-table', staff.name)
  await expect(row).toBeVisible()

  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/list_attendance_summary', async (route) => {
    if (route.request().postDataJSON().month === previous) {
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await input.fill(previous)
  await pending
  await input.fill('')
  await expect(input).toHaveValue('')
  await expect(row).toHaveCount(0)
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/list_attendance_summary') && r.request().postDataJSON().month === previous)
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧响应没有可观察变化，等待回调执行后核对空表。
  await page.waitForTimeout(500)
  await expect(row).toHaveCount(0)
})

test('考勤明细切换员工后丢弃迟到的旧详情', async ({ page }) => {
  const { token, today, month } = await businessDate()
  const yesterday = addDays(today, -1)
  const first = await hire('发型师')
  const second = await hire('发型师')
  await api(token, 'add_punch', { staff_id: first.id, at: `${yesterday}T10:00:00+08:00`, reason: '甲专属' })
  await api(token, 'add_punch', { staff_id: second.id, at: `${yesterday}T10:00:00+08:00`, reason: '乙专属' })
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  await showMonthOf(page, yesterday, month)

  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/get_staff_attendance', async (route) => {
    if (route.request().postDataJSON().staff_id === first.id) {
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await tableRow(page, 'attendance-table', first.name).click()
  await pending
  await tableRow(page, 'attendance-table', second.name).click()
  const detail = page.getByTestId('attendance-detail')
  await expect(detail).toContainText('乙专属')
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/get_staff_attendance') && r.request().postDataJSON().staff_id === first.id)
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧响应没有可观察变化，等待回调执行后核对当前详情。
  await page.waitForTimeout(500)
  await expect(detail).toContainText('乙专属')
  await expect(detail).not.toContainText('甲专属')
})

test('同一员工的迟到旧明细不覆盖新明细', async ({ page }) => {
  const { today, month } = await businessDate()
  const yesterday = addDays(today, -1)
  const staff = await hire('发型师')
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  await showMonthOf(page, yesterday, month)
  await tableRow(page, 'attendance-table', staff.name).click()
  const detail = page.getByTestId('attendance-detail')
  await expect(detail.getByTestId('attendance-summary-line')).toBeVisible()

  let release!: () => void
  let started!: () => void
  let first = true
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/get_staff_attendance', async (route) => {
    if (first && route.request().postDataJSON().staff_id === staff.id) {
      first = false
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await addPunchFromDetail(page, staff.name, yesterday, '10:00', '第一张')
  await pending
  await addPunchFromDetail(page, staff.name, yesterday, '11:00', '第二张')
  await expect(detail.getByTestId('attendance-punch')).toHaveCount(2)
  release()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧响应没有可观察变化，等待回调执行后核对当前详情。
  await page.waitForTimeout(500)
  await expect(detail.getByTestId('attendance-punch')).toHaveCount(2)
  await expect(detail).toContainText('第一张')
  await expect(detail).toContainText('第二张')
})

test('班次列表迟到的旧响应不覆盖新班次', async ({ page }) => {
  const shiftName = `班次${uniqueSuffix()}`
  await login(page, owner.account, owner.password)
  let release!: () => void
  let started!: () => void
  let first = true
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/list_shifts', async (route) => {
    if (first) {
      first = false
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await page.goto('/attendance')
  await pending
  await openShifts(page)
  await createShift(page, shiftName, '08:00', '17:00')
  const row = tableRow(page, 'shifts-table', shiftName)
  await expect(row).toBeVisible()
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/list_shifts'))
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧响应没有可观察变化，等待回调执行后核对当前列表。
  await page.waitForTimeout(500)
  await expect(row).toBeVisible()
})

test('员工班次列表迟到的旧响应不覆盖新指定', async ({ page }) => {
  const { token, today } = await businessDate()
  const tomorrow = addDays(today, 1)
  const shiftName = `轮班${uniqueSuffix()}`
  await api(token, 'create_shift', { name: shiftName, start_time: '09:00', end_time: '18:00' })
  const staff = await hire('发型师')
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  await openShifts(page)
  const row = tableRow(page, 'staff-shifts-table', staff.name)
  await expect(row).toContainText('未指定')

  let release!: () => void
  let started!: () => void
  let first = true
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/list_staff_shifts', async (route) => {
    if (first) {
      first = false
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  await assignShift(page, staff.name, `${shiftName} 09:00–18:00`, today, today)
  await pending
  await assignShift(page, staff.name, `${shiftName} 09:00–18:00`, tomorrow, today)
  await expect(row.locator('td').nth(2)).toContainText(`${shortDate(tomorrow)}起 ${shiftName} 09:00–18:00`)
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/list_staff_shifts'))
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧响应没有可观察变化，等待回调执行后核对当前列表。
  await page.waitForTimeout(500)
  await expect(row.locator('td').nth(2)).toContainText(`${shortDate(tomorrow)}起 ${shiftName} 09:00–18:00`)
})

test('考勤统计切月后丢弃迟到的旧统计', async ({ page }) => {
  const { today, month } = await businessDate()
  const previous = previousMonth(month)
  const staff = await hire('发型师')
  await api(await apiLogin(staff.account, staff.password), 'clock_in', {})
  await login(page, owner.account, owner.password)
  await page.goto('/attendance')
  const row = tableRow(page, 'attendance-table', staff.name)
  await expect(row.locator('td').nth(1)).toHaveText('1')

  let release!: () => void
  let started!: () => void
  const blocked = new Promise<void>((resolve) => {
    release = resolve
  })
  const pending = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/ops/list_attendance_summary', async (route) => {
    if (route.request().postDataJSON().month === previous) {
      const response = await route.fetch()
      started()
      await blocked
      await route.fulfill({ response })
    } else await route.continue()
  })
  const input = page.getByLabel('月份', { exact: true })
  await expect(input).toHaveAttribute('max', month)
  await input.fill(previous)
  await pending
  await input.fill(month)
  await expect(row.locator('td').nth(1)).toHaveText('1')
  const response = page.waitForResponse(r => r.url().endsWith('/api/ops/list_attendance_summary') && r.request().postDataJSON().month === previous)
  release()
  await (await response).finished()
  // eslint-disable-next-line no-restricted-syntax -- 丢弃旧响应没有可观察变化，等待回调执行后核对当前统计。
  await page.waitForTimeout(500)
  await expect(row.locator('td').nth(1)).toHaveText('1')
  await expect(input).toHaveValue(month)
  await expect(input).toHaveAttribute('max', today.slice(0, 7))
})
