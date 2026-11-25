import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'
import { addDays, weekdayOf } from '../app/utils/datetime'
import { yuan } from '../app/utils/money'

// 日结页（一页一天）：日结昨天、重新日结出第 2 版；封账到一个月前那天，只有下一天能接着封，店长看不到撤销，老板写原因撤销。
// 封账会挡住之后对那天以前的改动，所以排在其他项目都跑完之后（playwright.config 的 closing 项目），用完就撤销。
// 要求库里还没封过账：单独跑也要先 scripts/dev.sh reset。
test.describe.configure({ mode: 'serial' })

type Line = { method: string, label: string, amount: string }

// 页头日期写法：11月24日 周一。
function dayLabel(day: string) {
  return `${Number(day.slice(5, 7))}月${Number(day.slice(8))}日 ${weekdayOf(day)}`
}

async function setup() {
  const token = await apiLogin(owner.account, owner.password)
  const { business_date: today } = await api<{ business_date: string }>(token, 'get_settings', {})
  return { token, today }
}

// 昨天补录一单现金结账，让昨天成为要日结的营业日。
async function businessYesterday(token: string, today: string) {
  const suffix = uniqueSuffix()
  const position = await api<{ id: string }>(token, 'create_position', { name: `日结岗位${suffix}` })
  const staff = await hire('发型师')
  const item = await api<{ id: string }>(token, 'create_item', {
    name: `日结项目${suffix}`, price: '128.00', discountable: true, labor_fee_amount: null, labor_fee_percent: null,
    positions: [{ position_id: position.id, required: false }], level_prices: []
  })
  const customer = await api<{ customer: { id: string } }>(token, 'create_customer', { name: `日结顾客${suffix}` })
  const order = await api<{ id: string }>(token, 'open_order', {
    customer_id: customer.customer.id, gender: 'female', designated: false, paper_date: addDays(today, -1),
    items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: staff.id }] }]
  })
  await api(token, 'checkout_orders', { orders: [{ order_id: order.id, payments: [{ method: 'cash', amount: '128.00' }] }] })
}

function plus(amount: string, delta: number) {
  return (Number(amount) + delta).toFixed(2)
}

// 主按钮在能封账时叫「对完了，封账」：先存日结，再弹封账确认。这里只日结，确认框点取消。
async function closeWithoutSealing(page: Page) {
  await page.getByRole('button', { name: '对完了，封账', exact: true }).click()
  const confirm = page.getByRole('dialog', { name: /^封账到/ })
  await confirm.getByRole('button', { name: '取消', exact: true }).click()
  await expect(confirm).toHaveCount(0)
}

test('日结昨天，重新日结后留下两版记录 @closing', async ({ page }) => {
  const { token, today } = await setup()
  const yesterday = addDays(today, -1)
  await businessYesterday(token, today)
  const preview = await api<{ on_site: Line[] }>(token, 'preview_day_close', { date: yesterday })
  const cashLine = preview.on_site.find(l => l.method === 'cash')!

  await login(page, owner.account, owner.password)
  await page.goto('/closing')
  await expect(page.getByTestId('close-date')).toHaveText(dayLabel(yesterday))
  const form = page.getByTestId('close-form')
  // 现金多点 5 元，其余照系统填。
  for (const line of preview.on_site) {
    await form.getByLabel(`${line.label}实际`, { exact: true }).fill(line.method === 'cash' ? plus(line.amount, 5) : line.amount)
  }
  await expect(form.locator('tr[data-method="cash"]')).toContainText(`+${yuan(5)}`)
  await expect(form.locator('tr[data-method="cash"]')).toContainText('写个原因')
  await expect(page.getByTestId('close-brief')).toContainText(`多了 ${yuan(5)}`)
  await page.getByPlaceholder('对不上的原因，例如：找零少了 5 元').fill('多收了一张五元')
  await closeWithoutSealing(page)
  await expect(page.getByText(/^已日结（.+，第 \d+ 版），有 1 项对不上$/)).toBeVisible()

  // 重新日结，这次都对上。
  await expect(form.getByLabel(`${cashLine.label}实际`, { exact: true })).toHaveValue(String(Number(plus(cashLine.amount, 5))))
  await form.getByLabel(`${cashLine.label}实际`, { exact: true }).fill(cashLine.amount)
  await expect(page.getByTestId('close-brief')).toContainText('记得重新日结')
  await closeWithoutSealing(page)
  await expect(page.getByText(/^已日结（.+，第 \d+ 版），都对上了$/)).toBeVisible()
  await expect(form.locator('tr[data-method="cash"]')).toContainText('对上')
  await expect(page.getByTestId('close-brief')).not.toContainText('记得重新日结')

  const versions = page.getByTestId('close-versions')
  await versions.locator('summary').click()
  await expect(versions).toContainText('第 2 版')
  await expect(versions).toContainText('第 1 版')
  await expect(versions).toContainText(`现金 +${yuan(5)}`)
})

// 那天有营业时要先日结才能封：照系统的数填上。
async function fillAsSystem(page: Page, token: string, date: string) {
  const preview = await api<{ on_site: Line[] }>(token, 'preview_day_close', { date })
  for (const line of preview.on_site) await page.getByTestId('close-form').getByLabel(`${line.label}实际`, { exact: true }).fill(line.amount)
}

test('封账到一个月前那天，只有下一天能接着封，老板写原因撤销 @closing', async ({ page, browser }) => {
  const { token, today } = await setup()
  const day = addDays(today, -30)
  const next = addDays(today, -29)
  const later = addDays(today, -28)
  const sealButton = page.getByRole('button', { name: /封账$/ })

  await login(page, owner.account, owner.password)
  await page.goto(`/closing?date=${day}`)
  const sealedThrough = page.getByTestId('sealed-through')
  await expect(sealedThrough).toHaveText('还没有封账')
  await fillAsSystem(page, token, day)
  await sealButton.click()
  const confirm = page.getByRole('dialog', { name: `封账到 ${day}`, exact: true })
  await expect(confirm).toContainText('这是第一次封账')
  await confirm.getByRole('button', { name: '封账', exact: true }).click()
  await expect(page.getByText(`封好了，${day} 及以前的账不能再改`, { exact: true })).toBeVisible()
  await expect(sealedThrough).toHaveText(`已封账到 ${day}`)
  // 封了的日子不能再日结，也不能再封；格子换成存下的数。
  await expect(page.getByTestId('close-brief')).toContainText('已封账')
  await expect(sealButton).toHaveCount(0)
  await expect(page.getByTestId('close-form').getByRole('textbox')).toHaveCount(0)

  // 只有紧接着的下一天能封。
  await page.getByRole('button', { name: `接着封 ${next.slice(5)} ›`, exact: true }).click()
  await expect(page.getByTestId('close-date')).toHaveText(dayLabel(next))
  await expect(sealButton).toBeVisible()
  await page.getByRole('button', { name: '后一天', exact: true }).click()
  await expect(page.getByTestId('close-date')).toHaveText(dayLabel(later))
  await expect(page.getByTestId('close-form')).toBeVisible()
  await expect(sealButton).toHaveCount(0)

  // 店长能封，不能撤销。
  const manager = await hire('店长')
  const managerPage = await browser.newPage()
  await login(managerPage, manager.account, manager.password)
  await managerPage.goto(`/closing?date=${day}`)
  await expect(managerPage.getByTestId('sealed-through')).toHaveText(`已封账到 ${day}`)
  await expect(managerPage.getByRole('button', { name: `接着封 ${next.slice(5)} ›`, exact: true })).toBeVisible()
  await expect(managerPage.getByRole('button', { name: '撤销封账…', exact: true })).toHaveCount(0)
  await managerPage.close()

  await page.goto(`/closing?date=${day}`)
  await page.getByRole('button', { name: '撤销封账…', exact: true }).click()
  const unseal = page.getByRole('dialog', { name: `撤销 ${day} 的封账`, exact: true })
  await unseal.getByRole('button', { name: '撤销封账', exact: true }).click()
  await expect(page.getByText('写一下为什么要撤销封账', { exact: true })).toBeVisible()
  await unseal.getByLabel('原因').fill('端到端测试撤销')
  await unseal.getByRole('button', { name: '撤销封账', exact: true }).click()
  await expect(sealedThrough).toHaveText('还没有封账')
  await expect(page.getByTestId('close-brief')).not.toContainText('已封账')
})
