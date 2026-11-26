import { expect, test } from '@playwright/test'
import { navItems } from '../app/utils/nav'
import { api, apiLogin, hire, login, owner } from './helpers'

test('侧栏每项都有图标、不请求 Iconify，折叠后仍可用', async ({ page }) => {
  const iconifyRequests: string[] = []
  page.on('request', (request) => {
    if (/iconify/.test(new URL(request.url()).hostname)) iconifyRequests.push(request.url())
  })
  await page.setViewportSize({ width: 1024, height: 768 })
  await login(page, owner.account, owner.password)

  const navLinks = page.locator('[data-testid="sidebar-nav"] a')
  // 侧栏每项一个 Lucide 线条图标（DESIGN.md 图标一节），图标随包发布，渲染出带路径的 svg。
  await expect(navLinks.first().locator('.side-icon')).toBeVisible()
  await expect.poll(() => navLinks.evaluateAll(links => links.every(link => !!link.querySelector('svg.side-icon :is(path, circle, rect, line, polyline)')))).toBe(true)

  const search = page.getByTestId('search-button')
  await expect(search).toContainText('搜索')
  const label = search.getByText('搜索', { exact: true })
  expect(await label.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)

  await page.getByTestId('sidebar-collapse').click()
  await expect(page.getByTestId('sidebar-collapse')).toHaveAttribute('aria-label', '展开侧栏')
  await expect(page.getByTestId('sidebar-collapse')).toBeVisible()
  await expect(navLinks.first().locator('.side-icon')).toBeVisible()
  await expect(page.getByTestId('personal-menu')).toBeVisible()
  expect(iconifyRequests).toEqual([])
})

test('个人菜单可以打卡、打开我的考勤并退出登录', async ({ page }) => {
  const front = await hire('前台')
  const token = await apiLogin(owner.account, owner.password)
  const settings = await api<{ business_date: string }>(token, 'get_settings', {})
  await login(page, front.account, front.password)

  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '打卡' }).click()
  await expect(page.locator('[data-slot="title"]').filter({ hasText: '已打卡' })).toBeVisible()

  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '我的考勤' }).click()
  const dialog = page.getByRole('dialog', { name: '我的考勤' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText(settings.business_date)).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog).not.toBeVisible()

  await page.getByTestId('personal-menu').click()
  await page.getByRole('menuitem', { name: '退出登录' }).click()
  await expect(page).toHaveURL(/\/login$/)
  expect(await page.evaluate(() => localStorage.getItem('salon_token'))).toBeNull()
})

test('页面骨架：正文顶上统一插画加标题，设置类页面居中收窄，首页用问候不用标题', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 })
  await login(page, owner.account, owner.password)
  await expect(page.getByTestId('page-header')).toHaveCount(0)

  await page.goto('/staff')
  const header = page.getByTestId('page-header')
  await expect(header.getByRole('heading', { level: 1, name: '员工', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  // 说明只写标题说不清的事；员工页没有说明，那一行不占位置。
  await expect(page.getByTestId('page-description')).toBeHidden()
  const wide = await header.evaluate(element => element.parentElement!.getBoundingClientRect().width)

  await page.goto('/settings')
  await expect(header.getByRole('heading', { level: 1, name: '门店设置', exact: true })).toBeVisible()
  const narrow = await header.evaluate(element => element.parentElement!.getBoundingClientRect().width)
  expect(narrow).toBeLessThanOrEqual(800)
  expect(wide).toBeGreaterThan(narrow)
  // 随数据变的半句接在说明末尾，不另起一行。
  await expect(page.getByTestId('page-description')).toHaveText(/^修改从选定的日期开始执行，之前的业务和已经下的单不受影响。今天是营业日 \d{4}-\d{2}-\d{2}。$/)

  // 侧栏每一页（首页除外）顶上都是插画加标题，工作台也一样，插画各不相同。
  // 日结、工资表打开在某一天、某个月时标题带上日子（「11 月 24 日日结」「11 月工资」），
  // 打开哪天看库里有没有没结的日子、有没有算过的月份，所以这两页两种写法都认。
  const dataTitles: Record<string, RegExp> = {
    '/closing': /^(日结|\d{1,2} 月 \d{1,2} 日日结)$/,
    '/payroll': /^(工资表|\d{1,2} 月工资)$/
  }
  const arts = new Set<string>()
  const pages = [{ label: '待确认', to: '/drafts' }, ...navItems.filter(n => n.to !== '/')]
  for (const item of pages) {
    await page.goto(item.to)
    await expect(header.getByRole('heading', { level: 1, name: dataTitles[item.to] ?? item.label, exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
    await expect(header.getByTestId('page-art')).toBeVisible()
    // 插画背后的色块要真有颜色：标签色变量没输出时 fill 落空，只剩网点。
    expect(await header.getByTestId('page-art').evaluate(svg => getComputedStyle(svg.querySelector('path')!).fill)).not.toBe('none')
    arts.add((await header.getByTestId('page-art').getAttribute('data-art'))!)
  }
  expect(arts.size).toBe(pages.length)
})

test('侧栏配置类分组默认收起，当前页所在组展开，点组名切换并记住', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 })
  await login(page, owner.account, owner.password)
  await page.evaluate(() => localStorage.removeItem('salon-nav-folded'))
  await page.goto('/staff')
  const nav = page.getByTestId('sidebar-nav')
  const settingsGroup = nav.getByRole('button', { name: '设置', exact: true })
  await expect(settingsGroup).toHaveAttribute('aria-expanded', 'false')
  await expect(nav.getByRole('link', { name: '门店设置' })).toBeHidden()
  await expect(nav.getByRole('button', { name: '员工', exact: true })).toHaveAttribute('aria-expanded', 'true')

  await settingsGroup.click()
  await expect(settingsGroup).toHaveAttribute('aria-expanded', 'true')
  await expect(nav.getByRole('link', { name: '门店设置' })).toBeVisible()
  await page.reload()
  await expect(nav.getByRole('link', { name: '门店设置' })).toBeVisible()

  // 收起「项目与卡」后打开组里的页面，这个组照样展开。
  const itemsGroup = nav.getByRole('button', { name: '项目与卡', exact: true })
  await expect(itemsGroup).toHaveAttribute('aria-expanded', 'false')
  await page.goto('/items')
  await expect(itemsGroup).toHaveAttribute('aria-expanded', 'true')
  await expect(nav.getByRole('link', { name: '卡类型' })).toBeVisible()

  // 收成窄条后收起的组照样收着：只留一个按钮，点开列出组里的页面。
  await page.goto('/staff')
  await page.getByTestId('sidebar-collapse').click()
  await expect(nav.getByRole('link', { name: '卡类型' })).toBeHidden()
  await expect(nav.getByRole('link', { name: '员工' })).toBeVisible()
  await nav.getByRole('button', { name: '项目与卡（已收起）' }).click()
  await page.getByRole('menuitem', { name: '卡类型' }).click()
  await expect(page).toHaveURL(/\/card-types$/)
  await expect(nav.getByRole('link', { name: '卡类型' })).toBeVisible()
  await page.getByTestId('sidebar-collapse').click()
})

test('收起侧栏的按钮在顶栏固定位置，收起和展开时不挪动', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 })
  await login(page, owner.account, owner.password)
  const toggle = page.getByTestId('sidebar-collapse')
  await expect(toggle).toHaveAttribute('aria-label', '收起侧栏')
  const before = (await toggle.boundingBox())!
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-label', '展开侧栏')
  // 侧栏收窄后主区左移，按钮跟着顶栏走；它在顶栏里的位置（离主区左边和顶边的距离）不变。
  const panel = page.getByTestId('main-panel')
  const after = (await toggle.boundingBox())!
  const panelBox = (await panel.boundingBox())!
  expect(after.y).toBe(before.y)
  expect(after.width).toBe(before.width)
  expect(after.x - panelBox.x).toBeLessThan(24)
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-label', '收起侧栏')
})

test('平板宽度下问 AI 收进顶栏，不在右下角压着内容；宽屏仍是右下角圆钮；正文是 main 地标', async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 768 })
  await login(page, owner.account, owner.password)
  await page.goto('/cashier')
  await expect(page.getByRole('main')).toHaveCount(1)
  await expect(page.getByRole('main')).toContainText('收银')
  await expect(page.getByTestId('ai-fab')).toBeHidden()
  await page.getByTestId('ai-navbar').click()
  await expect(page.getByRole('dialog', { name: '问 AI' })).toBeVisible()
  await expect(page.getByTestId('ai-navbar')).toHaveCount(0)

  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '问 AI' })).toHaveCount(0)
  await page.setViewportSize({ width: 1366, height: 900 })
  await expect(page.getByTestId('ai-fab')).toBeVisible()
  await expect(page.getByTestId('ai-navbar')).toBeHidden()
})
