// 门店时钟：门店所在时区里的日子和钟点，以及「到点没来」的口径。不随浏览器所在时区变。
// 这个文件不用 Nuxt 的自动导入，单元测试（test/unit）能直接引。
// 营业日（跨日界线才换）不在这里，用 useStoreToday；这里的「日子」是门店墙上日历的那一天。

const STORE_TIME_ZONE = 'Asia/Shanghai'
// 门店时区固定东八区、没有夏令时，按门店的日子和钟点拼时刻时直接带上这个偏移。
const STORE_OFFSET = '+08:00'

const WEEKDAY_INDEX: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 }
const clockParts = new Intl.DateTimeFormat('en-US', {
  timeZone: STORE_TIME_ZONE,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  weekday: 'short',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23'
})
const dateTimeText = new Intl.DateTimeFormat('zh-CN', {
  timeZone: STORE_TIME_ZONE,
  year: 'numeric',
  month: 'numeric',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23'
})

export type StoreClock = {
  // 2026-11-27
  day: string
  month: number
  date: number
  // 0 是周日
  weekday: number
  // 09:05
  time: string
  // 从 0 点起的分钟数
  minutes: number
}

// 一个时刻在门店墙上是哪天几点。
export function storeClock(at: Date | string | number): StoreClock {
  const parts = clockParts.formatToParts(new Date(at))
  const part = (type: string) => parts.find(p => p.type === type)?.value ?? ''
  const hour = Number(part('hour'))
  const minute = Number(part('minute'))
  return {
    day: `${part('year')}-${part('month')}-${part('day')}`,
    month: Number(part('month')),
    date: Number(part('day')),
    weekday: WEEKDAY_INDEX[part('weekday')] ?? 0,
    time: `${part('hour')}:${part('minute')}`,
    minutes: hour * 60 + minute
  }
}

// 门店墙上的日期，2026-11-27。
export function storeCalendarDay(at: Date | string | number = new Date()) {
  return storeClock(at).day
}

// 门店墙上的钟点，09:05。
export function storeTime(at: Date | string | number) {
  return storeClock(at).time
}

// 完整的日期和时间，2026/3/5 09:05:00（流水、日志里一条一条的时刻）。
export function storeDateTime(at: Date | string | number) {
  return dateTimeText.format(new Date(at))
}

// 门店那天的几点几分对应的时刻，交给后端（预约起止、补卡）。time 是 09:05。
export function storeInstant(day: string, time: string) {
  return `${day}T${time}:00${STORE_OFFSET}`
}

// 到点没来：过了开始时间 5 分钟还没开单，该打个电话；过了 90 分钟多半不来了，不再提醒。
// 首页「接下来」和预约页用同一个口径。
const LATE_AFTER_MINUTES = 5
const GONE_AFTER_MINUTES = 90

function minutesPast(startsAt: string, now: number) {
  return Math.floor((now - new Date(startsAt).getTime()) / 60_000)
}

// 迟到了几分钟（该提醒的那段）；还没到该提醒、或者已经多半不来了，都是 0。
export function minutesLate(startsAt: string, now: number) {
  const late = minutesPast(startsAt, now)
  return late > LATE_AFTER_MINUTES && late <= GONE_AFTER_MINUTES ? late : 0
}

// 过了开始时间 90 分钟还没开单：多半不来了。
export function probablyNotComing(startsAt: string, now: number) {
  return minutesPast(startsAt, now) > GONE_AFTER_MINUTES
}
