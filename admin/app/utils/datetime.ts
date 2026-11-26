const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

// 时间点的显示：今天只写时分，其他日子写"11 月 20 日 周五 14:30"。按门店时区（Asia/Shanghai），不随浏览器所在时区变。
const stampParts = new Intl.DateTimeFormat('en-US', { timeZone: 'Asia/Shanghai', year: 'numeric', month: 'numeric', day: 'numeric', weekday: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
const WEEKDAY_INDEX: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 }

function shanghai(date: Date) {
  const parts = stampParts.formatToParts(date)
  const part = (type: string) => parts.find(p => p.type === type)?.value ?? ''
  return { day: `${part('year')}-${part('month')}-${part('day')}`, month: Number(part('month')), date: Number(part('day')), weekday: WEEKDAY_INDEX[part('weekday')] ?? 0, time: `${part('hour')}:${part('minute')}` }
}

export function formatStamp(value: string, now = new Date()) {
  const at = shanghai(new Date(value))
  if (at.day === shanghai(now).day) return at.time
  return `${at.month}月${at.date}日 ${WEEKDAYS[at.weekday]} ${at.time}`
}

// 表格里的日期：门店时区的 2026-11-20（DESIGN.md 组件约定）。
export function formatDate(value: string) {
  return new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value))
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
