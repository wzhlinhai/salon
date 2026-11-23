import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'
import { addDays } from '../app/utils/datetime'
import { yuanFixed } from '../app/utils/money'

// 日结页：日结昨天、重新日结出第 2 版；封账到一个月前那天，只有下一天能接着封，店长看不到撤销，老板写原因撤销。
// 封账会挡住之后对那天以前的改动，所以排在其他项目都跑完之后（playwright.config 的 closing 项目），用完就撤销。
// 要求库里还没封过账：单独跑也要先 scripts/dev.sh reset。
test.describe.configure({ mode: 'serial' })

type Line = { method: string, label: string, amount: string }

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

function dayRow(page: Page, date: string) {
  return page.locator('tr', { has: page.locator(`[data-date="${date}"]`) })
}

test('日结昨天，重新日结后留下两版记录 @closing', async ({ page }) => {
  const { token, today } = await setup()
  const yesterday = addDays(today, -1)
  await businessYesterday(token, today)
  const preview = await api<{ on_site: Line[] }>(token, 'preview_day_close', { date: yesterday })
  const cashLine = preview.on_site.find(l => l.method === 'cash')!

  await login(page, owner.account, owner.password)
  await page.goto('/closing')
  const row = dayRow(page, yesterday)
  await expect(row.getByText('待日结', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '日结昨天', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: `日结 ${yesterday}`, exact: false })
  const form = dialog.getByTestId('close-form')
  await expect(form).toBeVisible()
  // 现金多点 5 元，其余照系统填。
  for (const line of preview.on_site) {
    await form.getByLabel(`${line.label}实际`, { exact: true }).fill(line.method === 'cash' ? plus(line.amount, 5) : line.amount)
  }
  await expect(form.locator('tr[data-method="cash"]')).toContainText(`多 ${yuanFixed(5)}`)
  await expect(form.locator('tr[data-method="wechat"]')).toContainText('对上了')
  await dialog.getByPlaceholder('对不上的原因，例如：找零少了 5 元').fill('多收了一张五元')
  await dialog.getByRole('button', { name: '保存日结', exact: true }).click()

  const done = page.getByTestId('close-done')
  await expect(done).toContainText('有 1 项对不上')
  await expect(done).toContainText('第 1 版')
  await page.getByRole('button', { name: '完成', exact: true }).click()

  await expect(row.getByText('待日结', { exact: true })).toHaveCount(0)
  await expect(row).toContainText(`${cashLine.label} 多 ${yuanFixed(5)}`)
  await expect(row).toContainText('多收了一张五元')
  await expect(page.getByRole('button', { name: '日结昨天', exact: true })).toHaveCount(0)

  // 打开详情重新日结，这次都对上。
  await row.click()
  const sheet = page.getByRole('dialog', { name: `${yesterday}`, exact: false }).filter({ has: page.getByTestId('close-differences') })
  await expect(sheet.getByTestId('close-differences')).toContainText(`多 ${yuanFixed(5)}`)
  await sheet.getByRole('button', { name: '重新日结', exact: true }).click()
  await expect(form).toBeVisible()
  await expect(form.getByLabel(`${cashLine.label}实际`, { exact: true })).toHaveValue(plus(cashLine.amount, 5))
  await form.getByLabel(`${cashLine.label}实际`, { exact: true }).fill(cashLine.amount)
  await dialog.getByRole('button', { name: '保存日结', exact: true }).click()
  await expect(done).toContainText('都对上了')
  await expect(done).toContainText('第 2 版')
  await page.getByRole('button', { name: '完成', exact: true }).click()

  await expect(row).toContainText('第 2 版')
  await expect(row).toContainText('对上了')
  const versions = sheet.getByTestId('close-versions')
  await expect(versions).toContainText('第 2 版')
  await expect(versions).toContainText('第 1 版')
  await expect(versions).toContainText(`${cashLine.label} 多 ${yuanFixed(5)}`)
})

test('封账到一个月前那天，只有下一天能接着封，老板写原因撤销 @closing', async ({ page, browser }) => {
  const { today } = await setup()
  const day = addDays(today, -30)
  const next = addDays(today, -29)
  const later = addDays(today, -28)

  await login(page, owner.account, owner.password)
  await page.goto('/closing')
  const sealedThrough = page.getByTestId('sealed-through')
  await expect(sealedThrough).toHaveText('还没有封账')
  // 一个月前那几天不一定营业，默认的「营业日」视图里可能没有，切到「所有日子」。
  await page.locator('[data-filter=days]').click()
  await dayRow(page, day).click()

  await page.getByRole('button', { name: '封账到这天…', exact: true }).click()
  const confirm = page.getByRole('dialog', { name: `封账到 ${day}`, exact: true })
  await expect(confirm).toContainText('这是第一次封账')
  await confirm.getByRole('button', { name: '封账', exact: true }).click()
  await expect(page.getByText(`封好了，${day} 及以前的账不能再改`, { exact: true })).toBeVisible()
  await expect(sealedThrough).toHaveText(`已封账到 ${day}`)
  await expect(dayRow(page, day)).toContainText('已封账')
  // 封了的日子不能再日结，也不能再封。
  await expect(page.getByRole('button', { name: '日结', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '封账到这天…', exact: true })).toHaveCount(0)

  // 只有紧接着的下一天能封。
  await expect(page.getByRole('button', { name: `封账到 ${next}…`, exact: true })).toBeVisible()
  await dayRow(page, next).click()
  await expect(page.getByRole('button', { name: '封账到这天…', exact: true })).toBeVisible()
  await dayRow(page, later).click()
  await expect(page.getByRole('button', { name: '日结', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '封账到这天…', exact: true })).toHaveCount(0)

  // 店长能封，不能撤销。
  const manager = await hire('店长')
  const managerPage = await browser.newPage()
  await login(managerPage, manager.account, manager.password)
  await managerPage.goto('/closing')
  await expect(managerPage.getByTestId('sealed-through')).toHaveText(`已封账到 ${day}`)
  await expect(managerPage.getByRole('button', { name: `封账到 ${next}…`, exact: true })).toBeVisible()
  await expect(managerPage.getByRole('button', { name: '撤销封账…', exact: true })).toHaveCount(0)
  await managerPage.close()

  await page.getByRole('button', { name: '撤销封账…', exact: true }).first().click()
  const unseal = page.getByRole('dialog', { name: `撤销 ${day} 的封账`, exact: true })
  await unseal.getByRole('button', { name: '撤销封账', exact: true }).click()
  await expect(page.getByText('写一下为什么要撤销封账', { exact: true })).toBeVisible()
  await unseal.getByLabel('原因').fill('端到端测试撤销')
  await unseal.getByRole('button', { name: '撤销封账', exact: true }).click()
  await expect(sealedThrough).toHaveText('还没有封账')
  await expect(dayRow(page, day)).not.toContainText('已封账')
})
