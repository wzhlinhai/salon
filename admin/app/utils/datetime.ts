import { storeCalendarDay, storeClock } from '~/utils/storeClock'

const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

// 时间点的显示：今天只写时分，其他日子写"11 月 20 日 周五 14:30"。按门店时钟（utils/storeClock.ts），不随浏览器所在时区变。

export function formatStamp(value: string, now = new Date()) {
  const at = storeClock(new Date(value))
  if (at.day === storeClock(now).day) return at.time
  return `${at.month}月${at.date}日 ${WEEKDAYS[at.weekday]} ${at.time}`
}

// 列表里的短时间（AI 历史对话）：今天写时分，其他日子写"11月20日"，按门店时区。
export function formatShortStamp(value: string, now = new Date()) {
  const at = storeClock(new Date(value))
  if (at.day === storeClock(now).day) return at.time
  return `${at.month}月${at.date}日`
}

// 按门店时区分组：今天、近 7 天（不含今天）、更早。
export function recencyOf(value: string, now = new Date()): 'today' | 'week' | 'older' {
  const serial = (date: Date) => {
    const [year, month, day] = storeClock(date).day.split('-').map(Number)
    return Date.UTC(year ?? 0, (month ?? 1) - 1, day ?? 1) / 86400000
  }
  const days = serial(now) - serial(new Date(value))
  if (days <= 0) return 'today'
  return days < 7 ? 'week' : 'older'
}

// 表格里的日期：门店时区的 2026-11-20（DESIGN.md 组件约定）。
export function formatDate(value: string) {
  return storeCalendarDay(value)
}

// 营业日 YYYY-MM-DD 加减天数，按日历算，不受时区影响。
export function addDays(value: string, delta: number) {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, (day ?? 1) + delta)).toISOString().slice(0, 10)
}

// 营业日的星期：周一……周日。
export function weekdayOf(value: string) {
  const [year, month, day] = value.split('-').map(Number)
  return WEEKDAYS[new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, day ?? 1)).getUTCDay()]!
}
