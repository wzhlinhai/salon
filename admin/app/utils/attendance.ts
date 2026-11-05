// 考勤时间统一按门店所在时区显示和组装。
const ATTENDANCE_TIME_ZONE = 'Asia/Shanghai'

// attendanceCalendarDate 返回门店时区的今天，用于补卡日期上限和默认值。
export function attendanceCalendarDate(now = new Date()): string {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: ATTENDANCE_TIME_ZONE,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  }).formatToParts(now)
  const value = Object.fromEntries(parts.map(part => [part.type, part.value]))
  return `${value.year}-${value.month}-${value.day}`
}

// formatAttendanceTime 把 ISO 时刻显示为门店时区的时分。
export function formatAttendanceTime(value: string | null | undefined): string {
  if (!value) return '—'
  return new Date(value).toLocaleTimeString('zh-CN', {
    timeZone: ATTENDANCE_TIME_ZONE,
    hour12: false,
    hour: '2-digit',
    minute: '2-digit'
  })
}

// formatAttendanceDateTime 把 ISO 时刻显示为门店时区的日期和时分。
export function formatAttendanceDateTime(value: string | null | undefined): string {
  if (!value) return '—'
  return new Date(value).toLocaleString('zh-CN', {
    timeZone: ATTENDANCE_TIME_ZONE,
    hour12: false
  })
}

// attendancePunchAt 用门店时区的日历日和时分组装补卡时刻。
export function attendancePunchAt(date: string, time: string): string {
  return `${date}T${time}:00+08:00`
}
