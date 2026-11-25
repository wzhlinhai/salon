import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'

// 轮牌用例各建自己的队列和员工，只在自己的队列里操作，放在默认项目运行。

async function makeQueue(size: number) {
  const token = await apiLogin(owner.account, owner.password)
  const staff = []
  for (let i = 0; i < size; i++) staff.push(await hire('发型师'))
  const queue = await api<{ id: string, name: string }>(token, 'create_rotation_queue', { name: `牌${uniqueSuffix()}`.slice(0, 20) })
  await api(token, 'set_queue_members', { queue_id: queue.id, staff_ids: staff.map(s => s.id) })
  return { token, queue, staff }
}

function onDuty(page: Page, name: string) {
  return page.getByTestId('on-duty').locator(`li[data-staff="${name}"]`)
}

async function rowMenu(page: Page, name: string, action: string) {
  await page.getByRole('button', { name: `${name} 的操作` }).click()
  await page.getByRole('menuitem', { name: action }).click()
}

test('前台按到岗、轮牌接客、点客、撤销、跳过和休息走完一轮，每步都留记录', async ({ page }) => {
  const { queue, staff: [a, b, c] } = await makeQueue(3)
  const desk = await hire('前台')
  await login(page, desk.account, desk.password)
  await page.goto(`/rotation?queue=${queue.id}`)

  // 先到先排：按到岗顺序上牌。
  for (const s of [a!, b!, c!]) {
    await page.getByTestId('off-duty').locator(`li[data-staff="${s.name}"]`).getByRole('button', { name: '到岗' }).click()
    await expect(onDuty(page, s.name)).toBeVisible()
  }
  await expect(page.getByTestId('next-up')).toHaveText(a!.name)

  await page.getByTestId('assign-next').click()
  await expect(page.getByText(`轮到${a!.name}接客`, { exact: true })).toBeVisible()
  await expect(onDuty(page, a!.name)).toHaveAttribute('data-status', 'serving')
  await expect(page.getByTestId('next-up')).toHaveText(b!.name)

  await onDuty(page, c!.name).getByRole('button', { name: '点客' }).click()
  await expect(onDuty(page, c!.name)).toContainText('在做 · 点客')
  await onDuty(page, c!.name).getByRole('button', { name: '撤销接客' }).click()
  await expect(onDuty(page, c!.name)).toHaveAttribute('data-status', 'waiting')

  await rowMenu(page, b!.name, '跳过…')
  const skip = page.getByRole('dialog', { name: `跳过 ${b!.name}`, exact: true })
  await skip.getByRole('radio', { name: '其他' }).check()
  await skip.getByRole('button', { name: '跳过', exact: true }).click()
  await expect(page.getByText('原因选「其他」时要写说明', { exact: true })).toBeVisible()
  await skip.getByRole('radio', { name: '迟到' }).check()
  await skip.getByRole('button', { name: '跳过', exact: true }).click()
  await expect(skip).toBeHidden()
  // 默认「排到队尾」：被跳过的人去最后，下一位轮到后面的人。
  await expect(page.getByTestId('next-up')).toHaveText(c!.name)
  await expect(page.getByTestId('on-duty').locator('li').last()).toHaveAttribute('data-staff', b!.name)

  await rowMenu(page, c!.name, '临时休息…')
  const rest = page.getByRole('dialog', { name: `${c!.name} 临时休息`, exact: true })
  await rest.getByRole('button', { name: '15', exact: true }).click()
  await rest.getByRole('button', { name: '开始休息' }).click()
  await expect(onDuty(page, c!.name)).toHaveAttribute('data-status', 'resting')
  await expect(onDuty(page, c!.name)).toContainText('休息到')
  await expect(page.getByTestId('next-up')).toHaveText(b!.name)
  // 提前回来：结束休息后原位等待，又排在被跳过的人前面。
  await onDuty(page, c!.name).getByRole('button', { name: '结束休息' }).click()
  await expect(onDuty(page, c!.name)).toHaveAttribute('data-status', 'waiting')
  await expect(page.getByTestId('next-up')).toHaveText(c!.name)

  const events = page.getByTestId('rotation-events')
  await expect(events).toContainText(`${c!.name} 休息结束，回到等待`)
  await expect(events).toContainText(`${b!.name} 被跳过（迟到） · 我 操作`)
  await expect(events).toContainText(`${c!.name} 临时休息 15 分钟`)
  await expect(events).toContainText(`${c!.name} 撤销接客，不算一轮`)
  await expect(events).toContainText(`${a!.name} 轮牌接客`)
  // 今天的轮次只记真接了的：撤销掉的点客不算，椅子上的计数跟着。
  const rounds = page.getByTestId('rotation-rounds')
  await expect(rounds.locator('li')).toHaveCount(1)
  await expect(rounds).toContainText(`${a!.name} 接客`)
  await expect(onDuty(page, a!.name)).toContainText('今天 1 位 · 轮 1')
  await expect(onDuty(page, c!.name)).toContainText('今天还没接')
})

test('调整顺序要写原因，盖牌后不能点客、取消盖牌恢复，离岗回到未到岗', async ({ page }) => {
  const { token, queue, staff: [a, b] } = await makeQueue(2)
  for (const s of [a!, b!]) await api(token, 'join_rotation', { queue_id: queue.id, staff_id: s.id })
  await login(page, owner.account, owner.password)
  await page.goto(`/rotation?queue=${queue.id}`)

  await rowMenu(page, b!.name, '调整顺序…')
  const move = page.getByRole('dialog', { name: `调整 ${b!.name} 的顺序`, exact: true })
  await move.getByRole('combobox').click()
  await page.getByRole('option', { name: `第 1 位（${a!.name} 的位置）` }).click()
  await move.getByRole('button', { name: '调整' }).click()
  await expect(page.getByText('写一下为什么调整顺序', { exact: true })).toBeVisible()
  await move.getByRole('textbox').fill('顾客等得久')
  await move.getByRole('button', { name: '调整' }).click()
  await expect(move).toBeHidden()
  await expect(page.getByTestId('next-up')).toHaveText(b!.name)
  await expect(page.getByTestId('rotation-events')).toContainText(`${b!.name} 往前调了顺序（顾客等得久）`)

  await rowMenu(page, b!.name, '盖牌')
  await expect(onDuty(page, b!.name)).toHaveAttribute('data-status', 'covered')
  await expect(onDuty(page, b!.name).getByRole('button', { name: '点客' })).toHaveCount(0)
  await expect(page.getByTestId('next-up')).toHaveText(a!.name)
  await onDuty(page, b!.name).getByRole('button', { name: '取消盖牌' }).click()
  await expect(onDuty(page, b!.name)).toHaveAttribute('data-status', 'waiting')

  await rowMenu(page, a!.name, '离岗')
  await expect(page.getByTestId('off-duty').locator(`li[data-staff="${a!.name}"]`)).toBeVisible()
  await expect(onDuty(page, a!.name)).toHaveCount(0)
})

test('新建队列后接着选成员和默认顺序，名单按顺序列在未到岗里', async ({ page }) => {
  const x = await hire('发型师')
  const y = await hire('发型师')
  await login(page, owner.account, owner.password)
  await page.goto('/rotation')
  await page.getByRole('button', { name: '队列设置' }).click()
  await page.getByRole('menuitem', { name: '新建队列…' }).click()
  const name = `牌${uniqueSuffix()}`.slice(0, 20)
  const create = page.getByRole('dialog', { name: '新建轮牌队列', exact: true })
  await create.getByRole('textbox').fill(name)
  await create.getByRole('button', { name: '新建' }).click()

  const members = page.getByRole('dialog', { name: `${name}的成员和顺序`, exact: true })
  await members.getByRole('checkbox', { name: x.name }).check()
  await members.getByRole('checkbox', { name: y.name }).check()
  await members.getByRole('button', { name: `${y.name} 上移` }).click()
  await expect(members.getByTestId('member-order').locator('li').first()).toContainText(y.name)
  await members.getByRole('button', { name: '保存' }).click()
  await expect(members).toBeHidden()

  const off = page.getByTestId('off-duty').locator('li')
  await expect(off).toHaveCount(2)
  await expect(off.first()).toHaveAttribute('data-staff', y.name)
  await expect(off.last()).toHaveAttribute('data-staff', x.name)
})

test('发型师只能看轮牌，不能操作', async ({ page }) => {
  const { token, queue, staff: [a, b] } = await makeQueue(2)
  await api(token, 'join_rotation', { queue_id: queue.id, staff_id: a!.id })
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await page.goto(`/rotation?queue=${queue.id}`)
  await expect(onDuty(page, a!.name)).toBeVisible()
  await expect(page.getByTestId('assign-next')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '队列设置' })).toHaveCount(0)
  await expect(onDuty(page, a!.name).getByRole('button')).toHaveCount(0)
  const absent = page.getByTestId('off-duty').locator(`li[data-staff="${b!.name}"]`)
  await expect(absent).toBeVisible()
  await expect(absent.getByRole('button')).toHaveCount(0)
})

test('门店按项目记半轮时，队列设置里能选记半轮的项目，保存后再打开仍是选中的', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const settings = await api<{ business_date: string, settings: { key: string, value: unknown, is_default: boolean }[] }>(token, 'get_settings', {})
  const current = settings.settings.find(s => s.key === 'rotation_half_rule')!
  // 原来是系统默认就恢复成默认（null），不留一条显式覆盖。
  const before = current.is_default ? null : current.value
  const suffix = uniqueSuffix()
  const item = await api<{ id: string, name: string }>(token, 'create_item', {
    name: `半轮项目${suffix}`, kind: 'service', price: '40.00', discountable: true,
    labor_fee_amount: null, labor_fee_percent: null, positions: [], level_prices: [], member_prices: []
  })
  const setRule = (value: unknown) => api(token, 'change_setting', { key: 'rotation_half_rule', value, effective_from: settings.business_date })
  try {
    if (current.value === 'by_item') await setRule('none')
    await login(page, owner.account, owner.password)
    await page.goto('/rotation')
    await page.getByRole('button', { name: '队列设置' }).click()
    await expect(page.getByRole('menuitem', { name: '轮牌规则（门店设置）' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: '记半轮的项目…' })).toHaveCount(0)
    await page.keyboard.press('Escape')

    await setRule('by_item')
    await page.reload()
    await page.getByRole('button', { name: '队列设置' }).click()
    await page.getByRole('menuitem', { name: '记半轮的项目…' }).click()
    const dialog = page.getByRole('dialog', { name: '记半轮的项目', exact: true })
    await dialog.getByRole('checkbox', { name: item.name }).check()
    await dialog.getByRole('button', { name: '保存' }).click()
    await expect(dialog).toBeHidden()

    await page.getByRole('button', { name: '队列设置' }).click()
    await page.getByRole('menuitem', { name: '记半轮的项目…' }).click()
    await expect(dialog.getByRole('checkbox', { name: item.name })).toBeChecked()
  } finally {
    await api(token, 'set_half_turn_items', { item_ids: [] })
    await setRule(before)
  }
})

test('今天的轮次写出接的是哪位顾客：结账后照样写，点客记在「点」里', async ({ page }) => {
  const { token, queue, staff: [a, b] } = await makeQueue(2)
  for (const s of [a!, b!]) await api(token, 'join_rotation', { queue_id: queue.id, staff_id: s.id })
  const positions = await api<{ items: { id: string }[] }>(token, 'list_positions', {})
  const position = positions.items[0] ?? await api<{ id: string }>(token, 'create_position', { name: '发型师' })
  const item = await api<{ id: string, name: string }>(token, 'create_item', { name: `轮次剪${uniqueSuffix()}`.slice(0, 12), price: '60.00', discountable: true, labor_fee_amount: null, labor_fee_percent: null, positions: [{ position_id: position.id, required: true }], level_prices: [] })
  const customerName = `轮次客${uniqueSuffix()}`.slice(0, 12)
  const customer = await api<{ customer: { id: string } }>(token, 'create_customer', { name: customerName, phone: `13${String(Date.now()).slice(-9)}` })
  const order = await api<{ id: string, net: string }>(token, 'open_order', { customer_id: customer.customer.id, gender: 'female', designated: true, items: [{ item_id: item.id, staff: [{ slot: 1, staff_id: b!.id }] }] })
  await api(token, 'assign_turn', { queue_id: queue.id, staff_id: b!.id, designated: true, order_id: order.id })
  await api(token, 'checkout_orders', { orders: [{ order_id: order.id, payments: [{ method: 'cash', amount: order.net }] }] })

  await login(page, owner.account, owner.password)
  await page.goto(`/rotation?queue=${queue.id}`)
  const rounds = page.getByTestId('rotation-rounds')
  await expect(rounds.locator('li')).toHaveCount(1)
  await expect(rounds).toContainText(`${b!.name} 接 ${customerName}`)
  await expect(rounds).toContainText(item.name)
  await expect(onDuty(page, b!.name)).toContainText('今天 1 位 · 点 1')
  await expect(onDuty(page, b!.name)).toContainText('等着')
})
