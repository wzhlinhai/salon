import { storeDateTime, storeTime } from '~/utils/storeClock'

// 考勤时间按门店时钟（utils/storeClock.ts）显示，补卡时刻用 storeInstant 组装。

// formatAttendanceTime 把 ISO 时刻显示为门店时区的时分。
export function formatAttendanceTime(value: string | null | undefined): string {
  if (!value) return '—'
  return storeTime(value)
}

// formatAttendanceDateTime 把 ISO 时刻显示为门店时区的日期和时分。
export function formatAttendanceDateTime(value: string | null | undefined): string {
  if (!value) return '—'
  return storeDateTime(value)
}
