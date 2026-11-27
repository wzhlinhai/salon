import { describe, expect, it } from 'vitest'
import { minutesLate, probablyNotComing, storeCalendarDay, storeClock, storeDateTime, storeInstant, storeMonth, storeTime, storeTimeShort } from '~/utils/storeClock'
import { formatDate, formatStamp, recencyOf } from '~/utils/datetime'

const start = '2026-11-27T14:00:00+08:00'
const after = (minutes: number, seconds = 0) => new Date(start).getTime() + minutes * 60_000 + seconds * 1000

describe('门店时钟', () => {
  it('按门店时区算日子和钟点，不看浏览器时区', () => {
    // UTC 16:05 是门店第二天 0:05
    expect(storeClock('2026-11-27T16:05:00Z')).toEqual({ day: '2026-11-28', month: 11, date: 28, weekday: 6, time: '00:05', minutes: 5 })
    expect(storeCalendarDay('2026-03-04T16:00:00Z')).toBe('2026-03-05')
    expect(storeTime('2026-03-05T01:02:03Z')).toBe('09:02')
    expect(storeDateTime('2026-03-05T01:02:03Z')).toBe('2026/3/5 09:02:03')
    expect(storeTimeShort('2026-03-05T01:02:03Z')).toBe('9:02')
    expect(storeTimeShort('2026-11-27T16:05:00Z')).toBe('0:05')
    expect(storeMonth('2026-11-30T16:30:00Z')).toBe('2026-12')
  })

  it('门店的日子和钟点拼成时刻，再读回来不变', () => {
    const at = storeInstant('2026-11-27', '09:30')
    expect(at).toBe('2026-11-27T09:30:00+08:00')
    expect(new Date(at).toISOString()).toBe('2026-11-27T01:30:00.000Z')
    expect(storeClock(at)).toMatchObject({ day: '2026-11-27', time: '09:30' })
  })

  it('显示用的 datetime 也按门店时钟', () => {
    expect(formatDate('2026-11-27T16:05:00Z')).toBe('2026-11-28')
    expect(formatStamp('2026-11-27T16:05:00Z', new Date('2026-11-27T17:00:00Z'))).toBe('00:05')
    expect(formatStamp('2026-11-27T06:30:00Z', new Date('2026-11-27T17:00:00Z'))).toBe('11月27日 周五 14:30')
    expect(recencyOf('2026-11-27T15:59:00Z', new Date('2026-11-27T16:01:00Z'))).toBe('week')
  })
})

describe('到点没来', () => {
  it('过了 5 分钟才提醒', () => {
    expect(minutesLate(start, after(-10))).toBe(0)
    expect(minutesLate(start, after(5, 59))).toBe(0)
    expect(minutesLate(start, after(6))).toBe(6)
  })

  it('过了 90 分钟多半不来了，不再提醒', () => {
    expect(minutesLate(start, after(90, 59))).toBe(90)
    expect(probablyNotComing(start, after(90, 59))).toBe(false)
    expect(minutesLate(start, after(91))).toBe(0)
    expect(probablyNotComing(start, after(91))).toBe(true)
  })
})
