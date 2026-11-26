// 角色与权限页的分组、红线和格子文字（DESIGN.md 内容模式「对照表」）。
import type { components } from '~/utils/api'

type Def = components['schemas']['Def']
type Grant = components['schemas']['GrantSpec']

export interface PermGroup { key: string, label: string, hint: string, defs: Def[] }

// 按权限名的前缀分到五组；后端新加了这里没认出的权限，落进「其他」，不会在页面上消失。
const GROUPS: { key: string, label: string, hint: string, prefixes: string[] }[] = [
  { key: 'counter', label: '收银台', hint: '开单、收款、卡和团购', prefixes: ['order', 'payment', 'card', 'groupbuy', 'aftersales'] },
  { key: 'floor', label: '店面', hint: '轮牌、预约和打卡', prefixes: ['queue', 'booking', 'attendance'] },
  { key: 'customer', label: '顾客', hint: '档案、手机号和负责人', prefixes: ['customer'] },
  { key: 'money', label: '经营与账目', hint: '业绩、报表、日结和工资', prefixes: ['performance', 'report', 'closing', 'payroll'] },
  { key: 'admin', label: '店务管理', hint: '员工、角色、设置和数据', prefixes: ['staff', 'audit_log', 'role', 'settings', 'data'] }
]

export function groupPermissions(defs: Def[]): PermGroup[] {
  const out: PermGroup[] = [...GROUPS.map(g => ({ key: g.key, label: g.label, hint: g.hint, defs: [] as Def[] })), { key: 'other', label: '其他', hint: '', defs: [] }]
  for (const d of defs) {
    const i = GROUPS.findIndex(g => g.prefixes.includes(d.name.split('.')[0]!))
    out[i < 0 ? GROUPS.length : i]!.defs.push(d)
  }
  return out.filter(g => g.defs.length)
}

// 红线：老板最要盯的三类事，页面顶上写出除老板以外谁能做。
export const RED_LINES: { key: string, label: string, hint: string, permissions: string[] }[] = [
  { key: 'money', label: '动钱', hint: '改价、撤单、退卡和售后', permissions: ['order.reprice', 'order.void', 'card.adjust', 'aftersales.manage'] },
  { key: 'privacy', label: '顾客隐私', hint: '看完整手机号、导出数据', permissions: ['customer.full_phone', 'data.export'] },
  { key: 'books', label: '账目', hint: '封账、撤销封账和工资', permissions: ['closing.seal', 'closing.unseal', 'payroll.manage'] }
]

// 格子里的限制：本人、当天、N 天；没有限制返回空串（画一个墨点）。
export function limitText(g: Pick<Grant, 'scope' | 'window_days'>): string {
  const parts: string[] = []
  if (g.scope === 'self') parts.push('本人')
  if (g.window_days === 0) parts.push('当天')
  else if (g.window_days != null) parts.push(`${g.window_days} 天`)
  return parts.join('·')
}

// 一句大白话写清这个角色能做什么：整组都有写组名「全开」，有一部分写「部分能做」，限制点出前三条。
export function roleSentence(grants: Grant[], groups: PermGroup[]): string {
  const has = new Set(grants.map(g => g.permission))
  const full: string[] = []
  const part: string[] = []
  for (const g of groups) {
    const n = g.defs.filter(d => has.has(d.name)).length
    if (n === g.defs.length) full.push(g.label)
    else if (n) part.push(g.label)
  }
  const bits: string[] = []
  if (full.length) bits.push(`${full.join('、')}全开`)
  if (part.length) bits.push(`${part.join('、')}部分能做`)
  if (!bits.length) return '还没有任何权限。'
  const label = (name: string) => groups.flatMap(g => g.defs).find(d => d.name === name)?.label.replace(/（.*）/, '') ?? name
  const limits = grants.filter(g => limitText(g)).map(g => `${label(g.permission)}限${limitText(g)}`)
  const tail = limits.length ? `；${limits.slice(0, 3).join('，')}${limits.length > 3 ? ' 等' : ''}` : ''
  return `${bits.join('；')}${tail}。`
}
