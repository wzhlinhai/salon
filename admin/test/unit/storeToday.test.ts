import { describe, expect, it } from 'vitest'
import { monthStart, storeTodayState, type StoreSetting, type StoreToday, type StoreTodayResult } from '~/utils/storeToday'

function snapshot(day: string, cap: number): StoreToday {
  const setting: StoreSetting = { key: 'backfill_max_days', label: '补录天数', hint: '', kind: 'integer', min: 0, max: 30, default: 3, is_default: false, upcoming: [], value: cap }
  return { business_date: day, settings: [setting] }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

describe('storeTodayState', () => {
  it('读到后给营业日和这一天执行的设置值', async () => {
    const store = storeTodayState(async () => ({ data: snapshot('2026-11-27', 5) }))
    expect(store.businessDate.value).toBe('')
    expect(await store.load()).not.toBeNull()
    expect(store.businessDate.value).toBe('2026-11-27')
    expect(store.value('backfill_max_days')).toBe(5)
    expect(store.value('no_such_key')).toBeUndefined()
    expect(store.error.value).toBe('')
  })

  it('读失败返回 null、写原因，留着上次读到的；再读成功清掉原因', async () => {
    const answers: StoreTodayResult[] = [{ data: snapshot('2026-11-27', 5) }, { error: '网络连接失败，请重试' }, { data: snapshot('2026-11-28', 3) }]
    const store = storeTodayState(async () => answers.shift()!)
    await store.load()
    expect(await store.load()).toBeNull()
    expect(store.error.value).toBe('网络连接失败，请重试')
    expect(store.businessDate.value).toBe('2026-11-27')
    await store.load()
    expect(store.error.value).toBe('')
    expect(store.businessDate.value).toBe('2026-11-28')
  })

  it('没给原因的失败也有一句话', async () => {
    const store = storeTodayState(async () => ({}))
    expect(await store.load()).toBeNull()
    expect(store.error.value).toBe('操作失败')
  })

  it('每次 load 都重新问（营业日跨过日界线会变）', async () => {
    let calls = 0
    const store = storeTodayState(async () => {
      calls++
      return { data: snapshot('2026-11-27', 5) }
    })
    await store.load()
    await store.load()
    expect(calls).toBe(2)
  })
})

describe('storeTodayState 先发后到', () => {
  it('先发的旧结果后回来，不覆盖后发的新结果（改完设置重读）', async () => {
    const old = deferred<StoreTodayResult>()
    const fresh = deferred<StoreTodayResult>()
    const queue = [old.promise, fresh.promise]
    const store = storeTodayState(() => queue.shift()!)
    const first = store.load()
    const second = store.load()
    fresh.resolve({ data: snapshot('2026-11-27', 7) })
    await second
    old.resolve({ data: snapshot('2026-11-27', 5) })
    await first
    expect(store.value('backfill_max_days')).toBe(7)
  })

  it('先发的失败后回来，不把后发成功的标成失败', async () => {
    const old = deferred<StoreTodayResult>()
    const fresh = deferred<StoreTodayResult>()
    const queue = [old.promise, fresh.promise]
    const store = storeTodayState(() => queue.shift()!)
    const first = store.load()
    const second = store.load()
    fresh.resolve({ data: snapshot('2026-11-27', 7) })
    await second
    old.resolve({ error: '迟到的错误' })
    await first
    expect(store.error.value).toBe('')
  })
})

describe('monthStart', () => {
  it('营业日所在月的 1 号', () => {
    expect(monthStart('2026-11-27')).toBe('2026-11-01')
    expect(monthStart('2026-01-01')).toBe('2026-01-01')
  })
})
