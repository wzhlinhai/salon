import type { Locator, Page } from '@playwright/test'
import { expect } from '@playwright/test'
import { env } from './env'

export const owner = env.owner

const base = env.baseURL

export async function apiLogin(account: string, password: string): Promise<string> {
  const r = await fetch(`${base}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ account, password })
  })
  if (!r.ok) throw new Error(`login ${account}: ${r.status}`)
  return (await r.json()).token
}

export async function api<T = any>(token: string, op: string, body: object): Promise<T> {
  const r = await fetch(`${base}/api/ops/${op}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
    body: JSON.stringify(body)
  })
  if (!r.ok) throw new Error(`${op}: ${r.status} ${await r.text()}`)
  return r.json()
}

// 岗位等名称限 20 字，后缀用短的 base36 时间和随机数。
export function uniqueSuffix(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}

// 由老板建一名有登录账号的员工。账号带时间戳，测试可以对同一个库反复运行。
export async function hire(roleName: string): Promise<{ id: string, name: string, account: string, password: string }> {
  const token = await apiLogin(owner.account, owner.password)
  const { roles } = await api<{ roles: { id: string, name: string }[] }>(token, 'list_role_options', {})
  const role = roles.find(r => r.name === roleName)
  if (!role) throw new Error(`no role ${roleName}`)
  const suffix = uniqueSuffix()
  const staff = await api<{ id: string }>(token, 'create_staff', { name: `${roleName}${suffix}`, number: suffix, role_ids: [role.id] })
  const account = `e2e-${suffix}`
  const password = `password-${suffix}`
  await api(token, 'set_staff_login', { staff_id: staff.id, login_account: account, password })
  return { id: staff.id, name: staff.name, account, password }
}

export async function login(page: Page, account: string, password: string) {
  await page.goto('/login')
  await page.getByLabel('账号').fill(account)
  await page.getByLabel('密码').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
  await page.getByTestId('home-greeting').waitFor()
}

// 看得到的导航项，包括侧栏里收起的组（收起只是不展开，权限上仍可进）。
export async function navLabels(page: Page): Promise<string[]> {
  return (await page.locator('[data-testid="sidebar-nav"] a [data-nav-label]').allTextContents()).map(s => s.trim())
}

export function cash(amount: string) {
  return [{ method: 'cash', amount }]
}

// 收进 ··· 菜单的操作：先点开菜单（trigger），再点菜单项。
export async function menuAction(page: Page, trigger: Locator, name: string) {
  await trigger.click()
  await page.getByRole('menuitem', { name, exact: true }).click()
}

// 顾客档案里卡的操作（充值以外都在卡面右上角的 ··· 里）。卡的明细从右侧滑出、盖着右栏，开着就先关掉。
export async function cardAction(page: Page, card: Locator, name: string) {
  const history = page.getByTestId('card-history')
  if (await history.count()) {
    await page.keyboard.press('Escape')
    await expect(history).toHaveCount(0)
  }
  await menuAction(page, card.getByRole('button', { name: / 的操作$/ }), name)
}

// 顾客档案页头的 ··· 菜单。
export async function customerAction(page: Page, name: string) {
  await menuAction(page, page.getByRole('button', { name: '顾客操作', exact: true }), name)
}
