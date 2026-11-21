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

function aiPanel(page: Page) {
  return page.getByRole('dialog', { name: '吩咐 AI' })
}

test('待确认：卡片写清发起人、原话和 AI 理解的结果，确认后计数减一并真正执行', async ({ page }) => {
  const { token, name, draft } = await draftCategory('加一个烫染护理的分类')
  await login(page, owner.account, owner.password)
  const before = await pendingCount(token)
  const badge = page.getByTestId('drafts-nav')
  await expect(badge).toHaveText(new RegExp(`^\\s*◉?\\s*待确认\\s*${before}\\s*$`))

  await badge.getByText('待确认').click()
  const row = card(page, draft.id)
  await expect(row.getByTestId('draft-initiator')).toHaveText('王老板')
  await expect(row.getByTestId('draft-utterance')).toHaveText('“加一个烫染护理的分类”')
  await expect(row.getByTestId('draft-summary')).toHaveText(`新建项目分类「${name}」`)

  await row.getByTestId('draft-confirm').click()
  await expect(page.getByText('已确认').first()).toBeVisible()
  await expect(row).toHaveCount(0)
  await expect(badge).toHaveText(new RegExp(`^\\s*◉?\\s*待确认\\s*${before - 1 || ''}\\s*$`))

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
  const bodies: { messages: { role: string, content: string }[] }[] = []
  await page.route('**/api/ai/chat', async (route) => {
    bodies.push(route.request().postDataJSON())
    if (bodies.length === 1) {
      await route.fulfill({ json: { reply: '好的，已起草，请确认。', sources: [], drafts: [{ ...draft, preview_error: '' }] } })
    } else if (bodies.length === 2) {
      await route.fulfill({ status: 502, json: { detail: 'AI 响应超时，请重试' } })
    } else {
      await route.fulfill({ json: { reply: '今天实收 ¥0。', sources: [], drafts: [] } })
    }
  })
  await login(page, owner.account, owner.password)

  await page.getByTestId('ai-command').fill('加个造型分类')
  await page.getByTestId('ai-command').press('Enter')
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
  expect(bodies[2]!.messages).toEqual([
    { role: 'user', content: '加个造型分类' },
    { role: 'assistant', content: '好的，已起草，请确认。' },
    { role: 'user', content: '今天实收多少' }
  ])
})

test('修改：在面板里说怎么改，发给 AI 的话带上草稿编号；没发成时重说仍带编号', async ({ page }) => {
  const { draft } = await draftCategory()
  let sent = ''
  let calls = 0
  await page.route('**/api/ai/chat', async (route) => {
    sent = route.request().postDataJSON().messages.at(-1).content
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
  await expect(panel.getByTestId('ai-editing')).toHaveCount(0)
})

test('Ctrl K：输入页面名回车仍打开页面，说一句话回车交给 AI', async ({ page }) => {
  await page.route('**/api/ai/chat', route => route.fulfill({ json: { reply: '收到。', sources: [], drafts: [] } }))
  await login(page, owner.account, owner.password)
  const input = page.getByPlaceholder('说一句要做的事，或搜索顾客、页面…')

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
  await expect(page.locator('[role=option][data-highlighted]')).toHaveText('吩咐 AI：明天下午谁有空')
  await expect(async () => {
    if (await input.isVisible()) await input.press('Enter')
    await expect(aiPanel(page).getByText('收到。')).toBeVisible({ timeout: 1000 })
  }).toPass()
})

test('换人登录后看不到上一个人的 AI 对话', async ({ page }) => {
  await page.route('**/api/ai/chat', route => route.fulfill({ json: { reply: '上一个人的回复', sources: [], drafts: [] } }))
  const stylist = await hire('发型师')
  await login(page, owner.account, owner.password)
  await page.getByTestId('ai-command').fill('老板的问题')
  await page.getByTestId('ai-command').press('Enter')
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
  await page.getByTestId('ai-command').fill('你好')
  await page.getByTestId('ai-command').press('Enter')
  await expect(aiPanel(page).getByTestId('ai-entry')).toHaveCount(2)
  await expect(aiPanel(page).getByText('老板的问题')).toHaveCount(0)
})

test('对话里的草稿卡片是旧版本时不能确认，要去看最新内容', async ({ page }) => {
  const { token, draft } = await draftCategory()
  // 回复之后草稿又被改过：对话里的卡片还是旧内容。
  await api(token, 'update_draft_params', { id: draft.id, params: { name: `分类${uniqueSuffix()}` } })
  await page.route('**/api/ai/chat', route => route.fulfill({ json: { reply: '已起草。', sources: [], drafts: [{ ...draft, preview_error: '' }] } }))
  await login(page, owner.account, owner.password)
  await page.getByTestId('ai-command').fill('加一个分类')
  await page.getByTestId('ai-command').press('Enter')
  const row = aiPanel(page).locator(`[data-testid="draft-card"][data-draft-id="${draft.id}"]`)
  await expect(row.getByTestId('draft-stale')).toBeVisible()
  await expect(row.getByTestId('draft-confirm')).toHaveCount(0)
})

test('AI 回了空话，后面的对话照常发，空话不进历史', async ({ page }) => {
  const bodies: { messages: { role: string, content: string }[] }[] = []
  await page.route('**/api/ai/chat', async (route) => {
    bodies.push(route.request().postDataJSON())
    await route.fulfill({ json: { reply: bodies.length === 1 ? '' : '好的。', sources: [], drafts: [] } })
  })
  await login(page, owner.account, owner.password)
  await page.getByTestId('ai-command').fill('第一句')
  await page.getByTestId('ai-command').press('Enter')
  const panel = aiPanel(page)
  await expect(panel.getByTestId('ai-entry')).toHaveCount(2)
  await panel.getByTestId('ai-input').fill('第二句')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('好的。')).toBeVisible()
  expect(bodies[1]!.messages).toEqual([
    { role: 'user', content: '第一句' },
    { role: 'user', content: '第二句' }
  ])
})

test('AI 还在回上一句时说的话放进输入框；清空对话后迟到的回复不再出现', async ({ page }) => {
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  let calls = 0
  await page.route('**/api/ai/chat', async (route) => {
    if (++calls === 1) {
      await held
      await route.fulfill({ json: { reply: '迟到的回复', sources: [], drafts: [] } })
      return
    }
    await route.fulfill({ json: { reply: '新回复', sources: [], drafts: [] } })
  })
  await login(page, owner.account, owner.password)
  await page.getByTestId('ai-command').fill('第一句')
  await page.getByTestId('ai-command').press('Enter')
  const panel = aiPanel(page)
  await expect(panel.getByTestId('ai-thinking')).toBeVisible()

  // 关掉面板，在首页又说一句：不丢，放进面板输入框。
  await page.keyboard.press('Escape')
  await page.getByTestId('ai-command').fill('第二句')
  await page.getByTestId('ai-command').press('Enter')
  await expect(panel.getByTestId('ai-input')).toHaveValue('第二句')

  // 清空对话，再放出上一句的回复：不能出现在新对话里。
  await panel.getByRole('button', { name: '清空对话' }).click()
  await expect(panel.getByTestId('ai-thinking')).toHaveCount(0)
  const late = page.waitForResponse('**/api/ai/chat')
  release()
  await late
  await panel.getByTestId('ai-input').fill('第三句')
  await panel.getByTestId('ai-input').press('Enter')
  await expect(panel.getByText('新回复')).toBeVisible()
  await expect(panel.getByText('迟到的回复')).toHaveCount(0)
  await expect(panel.getByTestId('ai-entry')).toHaveCount(2)
})
