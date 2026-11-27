import type { components } from '~/utils/api'
import { storeCalendarDay, storeClock } from '~/utils/storeClock'

// 顾客列表和顾客详情共用的显示规则：到店时间的说法、下次预约的说法。
export type CustomerOverview = components['schemas']['CustomerOverview']

export function sinceText(days: number) {
  if (days === 0) return '今天'
  if (days === 1) return '昨天'
  return `${days} 天前`
}

export function shortDate(value: string) {
  const [, m, d] = value.split('-')
  return `${Number(m)}月${Number(d)}日`
}

// 上次到店的补充一行：日期（昨天以前才写）· 项目 · 员工。
export function lastVisitMeta(row: CustomerOverview) {
  const parts = [
    row.days_since_last_visit && row.days_since_last_visit > 1 && row.last_visit_date ? shortDate(row.last_visit_date) : '',
    row.last_visit_items.join('、'),
    row.last_visit_staff.map(s => s.name).join('、')
  ]
  return parts.filter(Boolean).join(' · ')
}

const DAY = 24 * 60 * 60 * 1000

export function nextText(startsAt: string) {
  const at = new Date(startsAt)
  const { day, time } = storeClock(at)
  const now = new Date()
  if (day === storeCalendarDay(now)) return `今天 ${time}`
  if (day === storeCalendarDay(now.getTime() + DAY)) return `明天 ${time}`
  return `${shortDate(day)} ${time}`
}

export function averageSpend(row: CustomerOverview) {
  return row.visit_count ? Number(row.total_spend) / row.visit_count : 0
}
