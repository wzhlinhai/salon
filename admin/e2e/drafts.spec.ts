import { expect, test, type Page } from '@playwright/test'
import { api, apiLogin, hire, login, owner, uniqueSuffix } from './helpers'

interface Draft { id: string, status: string, summary: string, version: number }

// 老板用接口起草一张新建项目分类的草稿，模拟 AI 起草的结果。
async function draftCategory(utterance = '加一个分类') {
  const token = await apiLogin(owner.account, owner.password)
  const name = `分类${uniqueSuffix()}`
  const draft = await api<Draft>(token, 'create_draft', {
    operation: 'create_item_category',
    params: { name },
    utterance,
    summary: `新建项目分类「${name}」`
  })
  return { token, name, draft }
}

async function pendingCount(token: string) {
  const { drafts } = await api<{ drafts: Draft[] }>(token, 'list_drafts', {})
  return drafts.filter(d => d.status === 'pending').length
}

function card(page: Page, id: string) {
  return page.locator(`[data-testid="draft-card"][data-draft-id="${id}"]`)
}

interface ChatBody { conversation_id?: string, content: string, sent?: string, editing?: string, draft_id?: string }
const CONVERSATION = '00000000-0000-4000-8000-000000000001'

function aiPanel(page: Page) {
  return page.getByRole('dialog', { name: '问 AI' })
}

// 从右下角的 AI 按钮打开面板（已打开就直接说），在面板里说一句。
async function sayToAi(page: Page, text: string) {
  const panel = aiPanel(page)
  if (!await panel.isVisible()) await page.getByTestId('ai-fab').click()
  await panel.getByTestId('ai-input').fill(text)
  await panel.getByTestId('ai-input').press('Enter')
}

test('待确认：卡片写清发起人、原话和 AI 理解的结果，确认后计数减一并真正执行', async ({ page }) => {
  const { token, name, draft } = await draftCategory('加一个烫染护理的分类')
  await login(page, owner.account, owner.password)
  const before = await pendingCount(token)
  const badge = page.getByTestId('drafts-nav')
  await expect(badge).toHaveText(new RegExp(`^\\s*待确认\\s*${before}\\s*$`))

  await badge.getByText('待确认').click()
  const row = card(page, draft.id)
  await expect(row.getByTestId('draft-initiator')).toHaveText('王老板')
  await expect(row.getByTestId('draft-utterance')).toHaveText('“加一个烫染护理的分类”')
  await expect(row.getByTestId('draft-summary')).toHaveText(`新建项目分类「${name}」`)

  await row.getByTestId('draft-confirm').click()
  await expect(page.getByText('已确认').first()).toBeVisible()
  await expect(row).toHaveCount(0)
  await expect(badge).toHaveText(new RegExp(`^\\s*待确认\\s*${before - 1 || ''}\\s*$`))

  const { items: categories } = await api<{ items: { name: string }[] }>(token, 'list_item_categories', {})
  expect(categories.map(c => c.name)).toContain(name)

  await page.getByTestId('drafts-tabs').getByText('已处理').click()
  await expect(card(page, draft.id).getByText('已确认')).toBeVisible()
})

test('拒绝草稿必须写原因，拒绝后不执行并显示原因', async ({ page }) => {
  const { token, name, draft } = await draftCategory()
  await login(page, owner.account, owner.password)
  await page.goto('/drafts')
  const row = card(page, draft.id)
  await row.getByTestId('draft-reject').click()
  await expect(page.getByTestId('reject-submit')).toBeDisabled()
  await page.getByTestId('reject-reason').fill('分类重复了')
  await page.getByTestId('reject-submit').click()
  await expect(row).toHaveCount(0)

  await page.getByTestId('drafts-tabs').getByText('已处理').click()
  await expect(card(page, draft.id)).toContainText('已拒绝')
  await expect(card(page, draft.id)).toContainText('拒绝原因：分类重复了')
  const { items: categories } = await api<{ items: { name: string }[] }>(token, 'list_item_categories', {})
  expect(categories.map(c => c.name)).not.toContain(name)
})

test('首页指令框把话交给 AI，回复里的草稿可在面板里直接确认，后续追问带上前文', async ({ page }) => {
  const { draft } = await draftCategory('加个造型分类')
  const bodies: ChatBody[] = []
  await page.route('**/api/ai/chat', async (route) => {
    bodies.push(route.request().postDataJSON())
    if (bodies.length === 1) {
      await route.fulfill({ json: { reply: '好的，已起草，请确认。', sources: [], drafts: [{ ...draft, preview_error: '' }], conversation_id: CONVERSATION, title: '加个造型分类' } })
    } else if (bodies.length === 2) {
      await route.fulfill({ status: 502, json: { detail: 'AI 响应超时，请重试' } })
    } else {
      await route.fulfill({ json: { reply: '今天实收 ¥0。', sources: [], drafts: [], conversation_id: CONVERSATION, title: '加个造型分类' } })
    }
  })
  await login(page, owner.account, owner.password)

  await sayToAi(page, '加个造型分类')
  const panel = aiPanel(page)
  await expect(panel.getByText('好的，已起草，请确认。')).toBeVisible()
  // 首页"待你确认"里也有同一张卡，这里只点面板里的。
  const row = panel.locator(`[data-testid="draft-card"][data-draft-id="${draft.id}"]`)
  await row.getByTestId('draft-confirm').click()
  await expect(row.getByText('已确认')).toBeVisible()
  await expect(row.getByTestId('draft-confirm')).toHaveCount(0)

  // 失败的一问一答不进历史。
  await panel.getByTestId('ai-input').fill('再查一下')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('AI 响应超时，请重试')).toBeVisible()
  await panel.getByTestId('ai-input').fill('今天实收多少')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('今天实收 ¥0。')).toBeVisible()
  // 第一句是新对话；后面每句只发这一句，带上服务端给的对话编号，前文由服务端取。
  expect(bodies[0]!.conversation_id).toBeUndefined()
  expect(bodies[2]).toMatchObject({ conversation_id: CONVERSATION, content: '今天实收多少' })
  expect(bodies[2]).not.toHaveProperty('messages')
  await expect(panel.getByTestId('ai-history-trigger')).toHaveText('加个造型分类')
})

test('修改：在面板里说怎么改，发给 AI 的话带上草稿编号；没发成时重说仍带编号', async ({ page }) => {
  const { draft } = await draftCategory()
  let sent = ''
  let draftID = ''
  let calls = 0
  await page.route('**/api/ai/chat', async (route) => {
    const body = route.request().postDataJSON()
    sent = body.sent
    draftID = body.draft_id
    if (++calls === 1) {
      await route.fulfill({ status: 502, json: { detail: 'AI 响应超时，请重试' } })
      return
    }
    await route.fulfill({ json: { reply: '已改好。', sources: [], drafts: [] } })
  })
  await login(page, owner.account, owner.password)
  await page.goto('/drafts')
  await card(page, draft.id).getByTestId('draft-modify').click()
  const panel = aiPanel(page)
  await expect(panel.getByTestId('ai-editing')).toContainText(draft.summary)
  // 正在改草稿时不列常用的事，免得点了「今天收了多少」被当成改这张草稿。
  await expect(panel.getByTestId('ai-quick')).toHaveCount(0)
  await panel.getByTestId('ai-input').fill('业绩算八成')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('AI 响应超时，请重试')).toBeVisible()
  await expect(panel.getByTestId('ai-editing')).toContainText(draft.summary)
  await panel.getByTestId('ai-input').fill('业绩算八成')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('已改好。')).toBeVisible()
  expect(calls).toBe(2)
  expect(sent).toContain(draft.id)
  expect(sent).toContain('业绩算八成')
  // 草稿是之前起草的，后端凭 draft_id 才允许这次对话改它。
  expect(draftID).toBe(draft.id)
  await expect(panel.getByTestId('ai-editing')).toHaveCount(0)
})

test('修改新建顾客草稿：直接弹表单改，卡片标题跟着改，确认后按改后的内容建档', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const before = `顾客${uniqueSuffix()}`
  const after = `顾客${uniqueSuffix()}`
  const phone = `15${String(Date.now()).slice(-8)}${Math.floor(Math.random() * 10)}`
  const draft = await api<Draft>(token, 'create_draft', {
    operation: 'create_customer',
    params: { name: before, phone, gender: 'female' },
    utterance: `新建顾客${before}`,
    summary: `新建顾客：${before}，女`
  })
  await login(page, owner.account, owner.password)
  await page.goto('/drafts')
  await card(page, draft.id).getByTestId('draft-modify').click()
  await expect(aiPanel(page)).toHaveCount(0)
  const modal = page.getByRole('dialog', { name: '修改新建顾客草稿' })
  await expect(modal.getByLabel('姓名')).toHaveValue(before)
  await modal.getByLabel('姓名').fill(after)
  await modal.getByRole('radio', { name: '男客' }).click()
  await modal.getByTestId('draft-edit-save').click()
  await expect(modal).toHaveCount(0)
  await expect(card(page, draft.id).getByTestId('draft-summary')).toHaveText(`新建顾客：${after}，男`)
  await card(page, draft.id).getByTestId('draft-confirm').click()
  // 待确认页默认只列未处理的，确认后卡片离开列表；状态用接口核对。
  await expect(card(page, draft.id)).toHaveCount(0)
  const { drafts } = await api<{ drafts: (Draft & { params: { name: string, phone: string, gender: string } })[] }>(token, 'list_drafts', {})
  const done = drafts.find(d => d.id === draft.id)!
  expect(done.status).toBe('confirmed_modified')
  expect(done.params).toMatchObject({ name: after, phone, gender: 'male' })
})

test('修改开单草稿：表单带出顾客、项目和各岗位的人，只改「指定」保存后其余参数原样', async ({ page }) => {
  const token = await apiLogin(owner.account, owner.password)
  const suffix = uniqueSuffix()
  const first = await api<{ id: string }>(token, 'create_position', { name: `草稿岗位甲${suffix}` })
  const second = await api<{ id: string }>(token, 'create_position', { name: `草稿岗位乙${suffix}` })
  const [stylist, assistant] = [await hire('发型师'), await hire('发型师')]
  const itemName = `草稿项目${suffix}`
  const item = await api<{ id: string }>(token, 'create_item', {
    name: itemName,
    price: '66.00',
    discountable: true,
    labor_fee_amount: null,
    labor_fee_percent: null,
    positions: [{ position_id: first.id, required: true }, { position_id: second.id, required: false }],
    level_prices: []
  })
  const customerName = `草稿顾客${suffix}`
  const { customer } = await api<{ customer: { id: string } }>(token, 'create_customer', { name: customerName, phone: `137${String(Date.now()).slice(-8)}` })
  const items = [{ item_id: item.id, staff: [{ slot: 1, staff_id: stylist.id }, { slot: 2, staff_id: assistant.id }] }]
  const draft = await api<Draft>(token, 'create_draft', {
    operation: 'open_order',
    params: { customer_id: customer.id, gender: 'female', designated: false, items },
    utterance: `${customerName}做${itemName}`,
    summary: `开单：${customerName}，${itemName}`
  })
  await login(page, owner.account, owner.password)
  await page.goto('/drafts')
  await card(page, draft.id).getByTestId('draft-modify').click()
  const modal = page.getByRole('dialog', { name: '修改开单草稿' })
  await expect(modal.getByTestId('draft-edit-customer')).toHaveText(customerName)
  await expect(modal.getByLabel('项目1岗位1员工')).toContainText(stylist.name)
  await expect(modal.getByLabel('项目1岗位2员工')).toContainText(assistant.name)
  await modal.getByRole('switch', { name: '指定' }).click()
  await modal.getByTestId('draft-edit-save').click()
  await expect(modal).toHaveCount(0)
  await expect(card(page, draft.id).getByTestId('draft-summary')).toHaveText(`开单：${customerName}，${itemName}，指定`)
  const { drafts } = await api<{ drafts: (Draft & { params: object })[] }>(token, 'list_drafts', {})
  expect(drafts.find(d => d.id === draft.id)!.params).toEqual({ customer_id: customer.id, gender: 'female', designated: true, items })
})

test('Ctrl K：输入页面名回车仍打开页面，说一句话回车交给 AI', async ({ page }) => {
  await page.route('**/api/ai/chat', route => route.fulfill({ json: { reply: '收到。', sources: [], drafts: [] } }))
  await login(page, owner.account, owner.password)
  const input = page.getByPlaceholder('找顾客、员工、页面，或说一句话交给 AI')

  await page.keyboard.press('Control+k')
  await input.fill('收银')
  await expect(page.locator('[role=option][data-highlighted]')).toHaveText(/收银/)
  // 列表在输入后几十毫秒内还会重排，这时的回车可能落空（组件行为，真人选字后再回车碰不到）；落空就再按一次。
  await expect(async () => {
    if (!page.url().endsWith('/cashier')) await input.press('Enter')
    await expect(page).toHaveURL(/\/cashier$/, { timeout: 1000 })
  }).toPass()

  await page.keyboard.press('Control+k')
  await input.fill('明天下午谁有空')
  await expect(page.locator('[role=option][data-highlighted]')).toHaveText('问 AI：明天下午谁有空')
  await expect(async () => {
    if (await input.isVisible()) await input.press('Enter')
    await expect(aiPanel(page).getByText('收到。')).toBeVisible({ timeout: 1000 })
  }).toPass()
})

test('换人登录后看不到上一个人的 AI 对话', async ({ page }) => {
  await page.route('**/api/ai/chat', route => route.fulfill({ json: { reply: '上一个人的回复', sources: [], drafts: [] } }))
  const stylist = await hire('发型师')
  await login(page, owner.account, owner.password)
  await sayToAi(page, '老板的问题')
  await expect(aiPanel(page).getByText('上一个人的回复')).toBeVisible()
  await page.keyboard.press('Escape')

  // 在页面里退出再登录，不整页刷新，前一个人的对话还在内存里。
  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '退出登录' }).click()
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('账号').fill(stylist.account)
  await page.getByLabel('密码').fill(stylist.password)
  await page.getByRole('button', { name: '登录' }).click()
  await page.getByTestId('home-greeting').waitFor()
  await sayToAi(page, '你好')
  await expect(aiPanel(page).getByTestId('ai-entry')).toHaveCount(2)
  await expect(aiPanel(page).getByText('老板的问题')).toHaveCount(0)
})

test('对话里的草稿卡片是旧版本时不能确认，要去看最新内容', async ({ page }) => {
  const { token, draft } = await draftCategory()
  // 回复之后草稿又被改过：对话里的卡片还是旧内容。
  await api(token, 'update_draft_params', { id: draft.id, params: { name: `分类${uniqueSuffix()}` }, summary: '改了分类名' })
  await page.route('**/api/ai/chat', route => route.fulfill({ json: { reply: '已起草。', sources: [], drafts: [{ ...draft, preview_error: '' }] } }))
  await login(page, owner.account, owner.password)
  await sayToAi(page, '加一个分类')
  const row = aiPanel(page).locator(`[data-testid="draft-card"][data-draft-id="${draft.id}"]`)
  await expect(row.getByTestId('draft-stale')).toBeVisible()
  await expect(row.getByTestId('draft-confirm')).toHaveCount(0)
})

test('AI 回了空话，后面的对话照常发，仍在同一个对话里', async ({ page }) => {
  const bodies: ChatBody[] = []
  await page.route('**/api/ai/chat', async (route) => {
    bodies.push(route.request().postDataJSON())
    await route.fulfill({ json: { reply: bodies.length === 1 ? '' : '好的。', sources: [], drafts: [], conversation_id: CONVERSATION, title: '第一句' } })
  })
  await login(page, owner.account, owner.password)
  await sayToAi(page, '第一句')
  const panel = aiPanel(page)
  await expect(panel.getByTestId('ai-entry')).toHaveCount(2)
  await panel.getByTestId('ai-input').fill('第二句')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('好的。')).toBeVisible()
  expect(bodies[1]).toMatchObject({ conversation_id: CONVERSATION, content: '第二句' })
})

test('AI 还在回上一句时说的话放进输入框；开新对话后迟到的回复不再出现', async ({ page }) => {
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  let calls = 0
  let lastBody: ChatBody | undefined
  await page.route('**/api/ai/chat', async (route) => {
    lastBody = route.request().postDataJSON()
    if (++calls === 1) {
      await held
      await route.fulfill({ json: { reply: '迟到的回复', sources: [], drafts: [], conversation_id: CONVERSATION, title: '第一句' } })
      return
    }
    await route.fulfill({ json: { reply: '新回复', sources: [], drafts: [] } })
  })
  await login(page, owner.account, owner.password)
  await sayToAi(page, '第一句')
  const panel = aiPanel(page)
  await expect(panel.getByTestId('ai-thinking')).toBeVisible()

  // 关掉面板，从 Ctrl K 又交给 AI 一句：上一句还没回完，这句放进面板输入框，不丢。
  await page.keyboard.press('Escape')
  await page.keyboard.press('Control+k')
  const search = page.getByPlaceholder('找顾客、员工、页面，或说一句话交给 AI')
  await search.fill('第二句')
  await expect(page.locator('[role=option][data-highlighted]')).toHaveText('问 AI：第二句')
  await expect(async () => {
    if (await search.isVisible()) await search.press('Enter')
    await expect(panel.getByTestId('ai-input')).toHaveValue('第二句', { timeout: 1000 })
  }).toPass()

  // 开新对话，再放出上一句的回复：不能出现在新对话里，也不能把新对话接到旧对话上。
  await panel.getByRole('button', { name: '新对话' }).click()
  await expect(panel.getByTestId('ai-thinking')).toHaveCount(0)
  const late = page.waitForResponse('**/api/ai/chat')
  release()
  await late
  await panel.getByTestId('ai-input').fill('第三句')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('新回复')).toBeVisible()
  await expect(panel.getByText('迟到的回复')).toHaveCount(0)
  await expect(panel.getByTestId('ai-entry')).toHaveCount(2)
  expect(lastBody?.conversation_id).toBeUndefined()
})

test('面板空着时列常用的事：开单先填开头和提示，补完再发；问数的直接发；按权限出', async ({ page }) => {
  const bodies: ChatBody[] = []
  await page.route('**/api/ai/chat', async (route) => {
    bodies.push(route.request().postDataJSON())
    await route.fulfill({ json: { reply: '收到。', sources: [], drafts: [] } })
  })
  await login(page, owner.account, owner.password)
  await page.getByTestId('ai-fab').click()
  const panel = aiPanel(page)
  const quick = panel.getByTestId('ai-quick')
  await expect(quick.getByRole('button')).toHaveText(['开单', '约个时间', '会员卡充值', '今天收了多少'])

  // 输入框里已经打了字：点「开单」不覆盖，把开头加在前面。
  const input = panel.getByTestId('ai-input')
  await input.fill('陈女士')
  await quick.getByRole('button', { name: '开单' }).click()
  await expect(input).toHaveValue('开单：陈女士')
  await input.fill('')
  await quick.getByRole('button', { name: '开单' }).click()
  await expect(input).toHaveValue('开单：')
  await expect(input).toBeFocused()
  await expect(panel.getByTestId('ai-starter')).toContainText('说顾客、项目和做的人')
  await input.pressSequentially('陈女士 剪发')
  await input.press('Enter')
  await expect(panel.getByText('收到。')).toBeVisible()
  expect(bodies[0]!.content).toBe('开单：陈女士 剪发')
  await expect(panel.getByTestId('ai-starter')).toHaveCount(0)

  await panel.getByRole('button', { name: '新对话' }).click()
  await quick.getByRole('button', { name: '今天收了多少' }).click()
  await expect.poll(() => bodies.length).toBe(2)
  expect(bodies[1]!.content).toBe('今天实收多少？按付款方式分开说')
})

test('面板常用的事按权限出：发型师没有充值和报表', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  await page.getByTestId('ai-fab').click()
  const quick = aiPanel(page).getByTestId('ai-quick')
  await expect(quick.getByRole('button', { name: '会员卡充值' })).toHaveCount(0)
  await expect(quick.getByRole('button', { name: '今天收了多少' })).toHaveCount(0)
})

test('历史对话：分组列出，打开接着聊带上对话编号；归档后从列表里去掉，在已归档里能取消归档', async ({ page }) => {
  const { draft } = await draftCategory()
  const now = new Date()
  const older = new Date(now.getTime() - 30 * 86400000)
  const OLD = '00000000-0000-4000-8000-000000000002'
  const list = {
    active: [
      { id: CONVERSATION, title: '今天的对话', updated_at: now.toISOString(), archived: false },
      { id: OLD, title: '上个月的对话', updated_at: older.toISOString(), archived: false }
    ],
    archived: [] as { id: string, title: string, updated_at: string, archived: boolean }[]
  }
  const queries: string[] = []
  await page.route('**/api/ops/list_ai_conversations', async (route) => {
    const body = route.request().postDataJSON()
    if (body.q) queries.push(body.q)
    const rows = body.archived ? list.archived : list.active
    await route.fulfill({ json: { conversations: rows.filter(c => !body.q || c.title.includes(body.q)) } })
  })
  await page.route('**/api/ops/get_ai_conversation', route => route.fulfill({ json: {
    id: OLD,
    title: '上个月的对话',
    archived: false,
    messages: [
      { id: 'm1', role: 'user', content: '加个分类', editing: null, created_at: older.toISOString(), drafts: [] },
      { id: 'm2', role: 'assistant', content: '已起草。', editing: null, created_at: older.toISOString(), drafts: [{ ...draft, preview_error: '' }] }
    ]
  } }))
  await page.route('**/api/ops/archive_ai_conversation', async (route) => {
    const moved = list.active.splice(list.active.findIndex(c => c.id === route.request().postDataJSON().id), 1)[0]!
    list.archived.push({ ...moved, archived: true })
    await route.fulfill({ json: { ...moved, archived: true } })
  })
  await page.route('**/api/ops/unarchive_ai_conversation', async (route) => {
    const moved = list.archived.splice(list.archived.findIndex(c => c.id === route.request().postDataJSON().id), 1)[0]!
    list.active.push({ ...moved, archived: false })
    await route.fulfill({ json: { ...moved, archived: false } })
  })
  const bodies: ChatBody[] = []
  await page.route('**/api/ai/chat', async (route) => {
    bodies.push(route.request().postDataJSON())
    await route.fulfill({ json: { reply: '接着说。', sources: [], drafts: [], conversation_id: OLD, title: '上个月的对话' } })
  })
  await login(page, owner.account, owner.password)
  await page.getByTestId('ai-fab').click()
  const panel = aiPanel(page)
  await expect(panel.getByTestId('ai-history-trigger')).toHaveText('新的 AI 对话')

  await panel.getByTestId('ai-history-trigger').click()
  const history = page.getByTestId('ai-history')
  await expect(history.getByText('今天', { exact: true })).toBeVisible()
  await expect(history.getByText('更早', { exact: true })).toBeVisible()
  await expect(history.getByTestId('ai-history-item')).toHaveCount(2)
  await history.getByTestId('ai-history-search').fill('上个月')
  await expect(history.getByTestId('ai-history-item')).toHaveCount(1)
  expect(queries).toContain('上个月')

  // 打开旧对话：消息和草稿卡片都在，草稿按现在的状态显示（这张还没确认，能确认）。
  await history.getByRole('button', { name: '上个月的对话' }).click()
  await expect(history).toHaveCount(0)
  await expect(panel.getByTestId('ai-history-trigger')).toHaveText('上个月的对话')
  await expect(panel.getByTestId('ai-entry')).toHaveCount(2)
  await expect(panel.locator(`[data-testid="draft-card"][data-draft-id="${draft.id}"]`).getByTestId('draft-confirm')).toBeVisible()
  await panel.getByTestId('ai-input').fill('再加一个')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('接着说。')).toBeVisible()
  expect(bodies[0]).toMatchObject({ conversation_id: OLD, content: '再加一个' })

  // 归档正开着的对话：列表里去掉，面板换成新对话。
  await panel.getByTestId('ai-history-trigger').click()
  const row = history.getByTestId('ai-history-item').filter({ hasText: '上个月的对话' })
  await row.hover()
  await row.getByRole('button', { name: '归档' }).click()
  await expect(history.getByTestId('ai-history-item')).toHaveCount(1)
  await expect(panel.getByTestId('ai-entry')).toHaveCount(0)
  await expect(panel.getByTestId('ai-history-trigger')).toHaveText('新的 AI 对话')

  await history.getByTestId('ai-history-archived').click()
  const archivedRow = history.getByTestId('ai-history-item').filter({ hasText: '上个月的对话' })
  await expect(archivedRow).toHaveCount(1)
  await archivedRow.hover()
  await archivedRow.getByRole('button', { name: '取消归档' }).click()
  await expect(history.getByText('没有归档的对话')).toBeVisible()
  await history.getByTestId('ai-history-archived').click()
  await expect(history.getByTestId('ai-history-item')).toHaveCount(2)
})

test('AI 面板全屏和侧栏一键切换，刷新后照上次的样子', async ({ page }) => {
  await login(page, owner.account, owner.password)
  await page.getByTestId('ai-fab').click()
  const panel = aiPanel(page)
  const narrow = (await panel.boundingBox())!.width
  await panel.getByRole('button', { name: '全屏' }).click()
  await expect(panel.getByRole('button', { name: '退出全屏' })).toBeVisible()
  await expect.poll(async () => (await panel.boundingBox())!.width).toBeGreaterThan(narrow + 200)

  await page.reload()
  await page.getByTestId('ai-fab').click()
  await expect(panel.getByRole('button', { name: '退出全屏' })).toBeVisible()
  await panel.getByRole('button', { name: '退出全屏' }).click()
  await expect(panel.getByRole('button', { name: '全屏' })).toBeVisible()
  await expect.poll(async () => (await panel.boundingBox())!.width).toBeLessThanOrEqual(narrow)
})

test('先点的历史对话迟到时不盖住后点的；换人登录后历史列表不留上一个人的', async ({ page }) => {
  const now = new Date().toISOString()
  const A = '00000000-0000-4000-8000-00000000000a'
  const B = '00000000-0000-4000-8000-00000000000b'
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route('**/api/ops/list_ai_conversations', route => route.fulfill({ json: { conversations: [
    { id: A, title: '对话甲', updated_at: now, archived: false },
    { id: B, title: '对话乙', updated_at: now, archived: false }
  ] } }))
  await page.route('**/api/ops/get_ai_conversation', async (route) => {
    const id = route.request().postDataJSON().id
    if (id === A) await held
    const name = id === A ? '甲' : '乙'
    await route.fulfill({ json: { id, title: `对话${name}`, archived: false, messages: [
      { id: `${name}1`, role: 'user', content: `${name}的问题`, editing: null, created_at: now, drafts: [] }
    ] } })
  })
  const stylist = await hire('发型师')
  await login(page, owner.account, owner.password)
  await page.getByTestId('ai-fab').click()
  const panel = aiPanel(page)
  const history = page.getByTestId('ai-history')
  await panel.getByTestId('ai-history-trigger').click()
  await history.getByRole('button', { name: '对话甲' }).click()
  await panel.getByTestId('ai-history-trigger').click()
  await history.getByRole('button', { name: '对话乙' }).click()
  await expect(panel.getByText('乙的问题')).toBeVisible()
  const late = page.waitForResponse('**/api/ops/get_ai_conversation')
  release()
  await late
  await expect(panel.getByText('甲的问题')).toHaveCount(0)
  await expect(panel.getByTestId('ai-history-trigger')).toHaveText('对话乙')
  // 点完历史下拉后焦点不在面板里，在输入框上按 Esc 关面板。
  await panel.getByTestId('ai-input').press('Escape')
  await expect(panel).toHaveCount(0)

  await page.unroute('**/api/ops/list_ai_conversations')
  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '退出登录' }).click()
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('账号').fill(stylist.account)
  await page.getByLabel('密码').fill(stylist.password)
  await page.getByRole('button', { name: '登录' }).click()
  await page.getByTestId('home-greeting').waitFor()
  await page.getByTestId('ai-fab').click()
  await expect(panel.getByTestId('ai-history-trigger')).toHaveText('新的 AI 对话')
  await expect(panel.getByTestId('ai-entry')).toHaveCount(0)
  await panel.getByTestId('ai-history-trigger').click()
  await expect(history.getByText('还没有对话')).toBeVisible()
})
