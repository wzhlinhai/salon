import { expect, test } from '@playwright/test'
import { hire, login, navLabels, owner } from './helpers'

// #26：员工登录后只看到权限内的页面；直接访问无权限的页面跳到无权限页。
const managementPages = ['/staff', '/options', '/roles', '/items', '/card-types', '/commission']

test('老板看到全部页面', async ({ page }) => {
  await login(page, owner.account, owner.password)
  expect(await navLabels(page)).toEqual(['首页', '顾客', '员工', '级别与岗位', '角色与权限', '项目与价格', '卡类型', '提成规则', '门店设置'])
  for (const path of managementPages) {
    await page.goto(path)
    await expect(page).toHaveURL(path)
  }
})

test('发型师看到首页、顾客和门店设置，直接访问管理页面被挡住', async ({ page }) => {
  const stylist = await hire('发型师')
  await login(page, stylist.account, stylist.password)
  expect(await navLabels(page)).toEqual(['首页', '顾客', '门店设置'])
  for (const path of managementPages) {
    await page.goto(path)
    await expect(page).toHaveURL('/forbidden')
  }
})

test('店长看到员工页面，看不到角色与权限', async ({ page }) => {
  const manager = await hire('店长')
  await login(page, manager.account, manager.password)
  expect(await navLabels(page)).toEqual(['首页', '顾客', '员工', '级别与岗位', '门店设置'])
  await page.goto('/roles')
  await expect(page).toHaveURL('/forbidden')
})

test('未登录访问任何页面都跳到登录页', async ({ page }) => {
  await page.goto('/staff')
  await expect(page).toHaveURL('/login')
})
