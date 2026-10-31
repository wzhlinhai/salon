import type { Page } from '@playwright/test'
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
export async function hire(roleName: string): Promise<{ account: string, password: string }> {
  const token = await apiLogin(owner.account, owner.password)
  const { roles } = await api<{ roles: { id: string, name: string }[] }>(token, 'list_role_options', {})
  const role = roles.find(r => r.name === roleName)
  if (!role) throw new Error(`no role ${roleName}`)
  const suffix = uniqueSuffix()
  const staff = await api<{ id: string }>(token, 'create_staff', { name: `${roleName}${suffix}`, number: suffix, role_ids: [role.id] })
  const account = `e2e-${suffix}`
  const password = `password-${suffix}`
  await api(token, 'set_staff_login', { staff_id: staff.id, login_account: account, password })
  return { account, password }
}

export async function login(page: Page, account: string, password: string) {
  await page.goto('/login')
  await page.getByLabel('账号').fill(account)
  await page.getByLabel('密码').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
  await page.getByText(/你好/).waitFor()
}

export async function navLabels(page: Page): Promise<string[]> {
  return (await page.locator('aside nav a').allInnerTexts()).map(s => s.trim())
}
