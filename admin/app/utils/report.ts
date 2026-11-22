// 报表共用：报表目录、日期范围（快捷范围、上一段、范围上限）和数字显示。
import type { components } from '~/utils/api'
import { addDays } from './datetime'

export type Granularity = 'day' | 'month'
export interface ReportRange { granularity: Granularity, from: string, to: string }

// 报表入口的目录（DESIGN.md 报表入口，照 report-02）：分类在左，报表在右。只列做好了的报表。
export const reportCategories = ['营业', '员工', '项目与卡', '顾客'] as const
export const reportCatalog: { key: string, label: string, description: string, icon: string, category: typeof reportCategories[number], permission: string }[] = [
  { key: 'business', label: '营业报表', description: '每天或每月的实收、办卡充值、客数，按收款方式分列', icon: 'i-lucide-chart-line', category: '营业', permission: 'report.view' },
  { key: 'debts', label: '欠款对账', description: '到现在为止的挂账和历史欠款，已还多少、还剩多少', icon: 'i-lucide-hand-coins', category: '营业', permission: 'report.view' },
  { key: 'staff', label: '员工业绩', description: '每人的项目、耗卡、手工费、销售业绩和客数、提成；发型师只看自己', icon: 'i-lucide-users', category: '员工', permission: 'performance.view' },
  { key: 'items', label: '项目报表', description: '每个服务、卖品做了几次、收了多少，实收前 10 名', icon: 'i-lucide-scissors', category: '项目与卡', permission: 'report.view' },
  { key: 'cards', label: '卡项报表', description: '每种卡开卡、充值、耗卡、退卡多少，结束时余额', icon: 'i-lucide-credit-card', category: '项目与卡', permission: 'report.view' },
  { key: 'sources', label: '新客来源', description: '新客从哪来、几个回头、花了多少；只有人数，不列顾客', icon: 'i-lucide-user-plus', category: '顾客', permission: 'report.view' }
]

// 业绩明细里每笔的来源方式：项目、耗卡、手工费按支付方式，销售按开卡、充值这类动作。
export const performanceMethodNames: Record<components['schemas']['PerformanceEntry']['method'], string> = {
  cash: '现金', unionpay: '银联', wechat: '微信', alipay: '支付宝', groupbuy: '团购', debt: '挂账', red_packet: '红包',
  card_principal: '卡本金', card_bonus: '卡赠送金', count_paid: '扣次（购买）', count_gift: '扣次（赠送）', labor: '手工费',
  card_open: '开卡', card_recharge: '充值', card_upgrade: '补差价', red_packet_sold: '卖红包'
}

// 后端上限：日报起止相差不超过 92 天，月报不超过 12 个月。
export const MAX_DAY_SPAN = 92
export const MAX_MONTHS = 12

function monthIndex(month: string) {
  const [year, m] = month.split('-').map(Number)
  return (year ?? 0) * 12 + (m ?? 1) - 1
}

function monthOf(index: number) {
  return `${Math.floor(index / 12)}-${String((index % 12) + 1).padStart(2, '0')}`
}

export function addMonths(month: string, delta: number) {
  return monthOf(monthIndex(month) + delta)
}

function dayIndex(day: string) {
  const [year, month, date] = day.split('-').map(Number)
  return Date.UTC(year ?? 0, (month ?? 1) - 1, date ?? 1) / 86_400_000
}

// 范围里有几天或几个月（含两头）。
export function rangeLength(range: ReportRange) {
  if (range.granularity === 'month') return monthIndex(range.to) - monthIndex(range.from) + 1
  return dayIndex(range.to) - dayIndex(range.from) + 1
}

// 真实存在的日期（YYYY-MM-DD）或月份（YYYY-MM）。
export function validPeriod(value: string, granularity: Granularity) {
  if (granularity === 'month') {
    const m = /^(\d{4})-(\d{2})$/.exec(value)
    return !!m && Number(m[2]) >= 1 && Number(m[2]) <= 12
  }
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  return addDays(value, 0) === value
}

// 范围有问题时返回一句说明，放在范围旁边；没问题返回空串。
// dayOnly：报表只能按日查，超限时不提示切按月。
export function rangeProblem(range: ReportRange, dayOnly = false) {
  if (!validPeriod(range.from, range.granularity) || !validPeriod(range.to, range.granularity)) return range.granularity === 'month' ? '请填写有效的起止月份' : '请填写有效的起止日期'
  if (range.from > range.to) return range.granularity === 'month' ? '开始月份不能晚于结束月份' : '开始日期不能晚于结束日期'
  if (range.granularity === 'day' && rangeLength(range) - 1 > MAX_DAY_SPAN) return dayOnly ? `最多查 ${MAX_DAY_SPAN} 天` : `按日最多查 ${MAX_DAY_SPAN} 天，长了请切到按月`
  if (range.granularity === 'month' && rangeLength(range) > MAX_MONTHS) return `按月最多查 ${MAX_MONTHS} 个月`
  return ''
}

// 紧挨在前面、同样长的一段，用来算指标卡的涨跌。
export function previousRange(range: ReportRange): ReportRange {
  const n = rangeLength(range)
  if (range.granularity === 'month') return { granularity: 'month', from: addMonths(range.from, -n), to: addMonths(range.from, -1) }
  return { granularity: 'day', from: addDays(range.from, -n), to: addDays(range.from, -1) }
}

export function previousLabel(range: ReportRange) {
  const n = rangeLength(range)
  if (range.granularity === 'month') return n === 1 ? '比上月' : `比前 ${n} 个月`
  return n === 1 ? '比前一天' : `比前 ${n} 天`
}

// 快捷范围。today 是门店营业日 YYYY-MM-DD。
export function rangePresets(granularity: Granularity, today: string): { label: string, range: ReportRange }[] {
  const month = today.slice(0, 7)
  if (granularity === 'month') {
    return [
      { label: '今年', range: { granularity, from: `${today.slice(0, 4)}-01`, to: month } },
      { label: '近 6 个月', range: { granularity, from: addMonths(month, -5), to: month } },
      { label: '近 12 个月', range: { granularity, from: addMonths(month, -11), to: month } }
    ]
  }
  const weekday = (new Date(dayIndex(today) * 86_400_000).getUTCDay() + 6) % 7
  const lastMonth = addMonths(month, -1)
  return [
    { label: '今天', range: { granularity, from: today, to: today } },
    { label: '昨天', range: { granularity, from: addDays(today, -1), to: addDays(today, -1) } },
    { label: '本周', range: { granularity, from: addDays(today, -weekday), to: today } },
    { label: '本月', range: { granularity, from: `${month}-01`, to: today } },
    { label: '上月', range: { granularity, from: `${lastMonth}-01`, to: addDays(`${month}-01`, -1) } },
    { label: '近 30 天', range: { granularity, from: addDays(today, -29), to: today } }
  ]
}

// 打开报表时的默认范围：按日是本月，按月是近 6 个月。
export function defaultRange(granularity: Granularity, today: string): ReportRange {
  return rangePresets(granularity, today).find(p => p.label === (granularity === 'day' ? '本月' : '近 6 个月'))!.range
}

// 切换日 / 月时保留大致的范围：日 → 月取起止所在月；月 → 日取起始月 1 日到结束月末（不超过今天）。
export function switchGranularity(range: ReportRange, granularity: Granularity, today: string): ReportRange {
  if (granularity === range.granularity) return range
  // 起止没填好时没法换算，直接给新粒度的默认范围。
  if (!validPeriod(range.from, range.granularity) || !validPeriod(range.to, range.granularity)) return defaultRange(granularity, today)
  if (granularity === 'month') return { granularity, from: range.from.slice(0, 7), to: range.to.slice(0, 7) }
  const end = addDays(`${addMonths(range.to, 1)}-01`, -1)
  return { granularity, from: `${range.from}-01`, to: end > today ? today : end }
}

// 涨跌百分比；上一段是 0 时不比。
export function percentChange(current: string | number, previous: string | number | undefined) {
  const before = Number(previous ?? 0)
  if (!before) return null
  return Math.round(((Number(current) - before) / before) * 100)
}

// 客数后端给四位小数（一单多人分摊），显示最多一位。
export function formatGuest(value: string | number) {
  return Number(value || '0').toLocaleString('zh-CN', { maximumFractionDigits: 1 })
}
