import { describe, expect, it } from 'vitest'
import type { components } from '~/utils/api'
import { customerLabel, customerRosterState, UNIDENTIFIED_CUSTOMER, UNNAMED_CUSTOMER } from '~/utils/customerRoster'

type Customer = components['schemas']['Customer']

function customer(id: string, name: string | null, unidentified = false) {
  return { id, name, unidentified } as Customer
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

describe('customerLabel', () => {
  it('没认出是谁叫未识别顾客，认出了没留姓名叫未留姓名的顾客', () => {
    expect(customerLabel({ name: null, unidentified: true })).toBe(UNIDENTIFIED_CUSTOMER)
    expect(customerLabel({ name: '王女士', unidentified: true })).toBe(UNIDENTIFIED_CUSTOMER)
    expect(customerLabel({ name: '', unidentified: false })).toBe(UNNAMED_CUSTOMER)
    expect(customerLabel({ name: '王女士', unidentified: false })).toBe('王女士')
  })
})

describe('customerRosterState', () => {
  it('一批 id 去重后一次取回，称呼和是否认出写进名册', async () => {
    const calls: string[][] = []
    const roster = customerRosterState(async (ids) => {
      calls.push(ids)
      return [customer('a', '王女士'), customer('b', null, true)]
    }, () => true)
    await roster.resolve(['a', 'b', 'a', ''])
    expect(calls).toEqual([['a', 'b']])
    expect(roster.label('a', '顾客')).toBe('王女士')
    expect(roster.label('b', '顾客')).toBe(UNIDENTIFIED_CUSTOMER)
    expect(roster.identified('a')).toBe(true)
    expect(roster.identified('b')).toBe(false)
    expect(roster.label('c', '顾客')).toBe('顾客')
    expect(roster.identified('c')).toBeUndefined()
  })

  it('没有看顾客的权限就不取', async () => {
    const calls: string[][] = []
    const roster = customerRosterState(async (ids) => {
      calls.push(ids)
      return []
    }, () => false)
    await roster.resolve(['a'])
    expect(calls).toEqual([])
    expect(roster.label('a', '顾客')).toBe('顾客')
  })

  it('超过 200 个分批取', async () => {
    const calls: number[] = []
    const roster = customerRosterState(async (ids) => {
      calls.push(ids.length)
      return ids.map(id => customer(id, id))
    }, () => true)
    await roster.resolve(Array.from({ length: 450 }, (_, i) => `c${i}`))
    expect(calls).toEqual([200, 200, 50])
    expect(roster.label('c449', '')).toBe('c449')
  })

  it('正在取的 id 不重复取', async () => {
    const pending = deferred<Customer[]>()
    const calls: string[][] = []
    const roster = customerRosterState(async (ids) => {
      calls.push(ids)
      return calls.length === 1 ? pending.promise : ids.map(id => customer(id, id))
    }, () => true)
    const first = roster.resolve(['a'])
    await roster.resolve(['a', 'b'])
    expect(calls).toEqual([['a'], ['b']])
    pending.resolve([customer('a', '王女士')])
    await first
    expect(roster.label('a', '')).toBe('王女士')
    expect(roster.label('b', '')).toBe('b')
  })

  it('每次都重新取，改过的名字下次刷新看得到；读失败不记，问了没返回的去掉', async () => {
    const answers: (Customer[] | null)[] = [[customer('a', null, true), customer('b', '李女士')], null, [customer('a', '王女士')]]
    const roster = customerRosterState(async () => answers.shift()!, () => true)
    await roster.resolve(['a', 'b'])
    expect(roster.label('a', '')).toBe(UNIDENTIFIED_CUSTOMER)
    await roster.resolve(['a', 'b'])
    expect(roster.label('a', '')).toBe(UNIDENTIFIED_CUSTOMER)
    expect(roster.label('b', '')).toBe('李女士')
    await roster.resolve(['a', 'b'])
    expect(roster.label('a', '')).toBe('王女士')
    expect(roster.identified('a')).toBe(true)
    expect(roster.label('b', '顾客')).toBe('顾客')
  })

  it('分批时一批读失败，另一批照常写进名册', async () => {
    const roster = customerRosterState(async ids => ids[0] === 'c0' ? null : ids.map(id => customer(id, id)), () => true)
    await roster.resolve(Array.from({ length: 201 }, (_, i) => `c${i}`))
    expect(roster.label('c0', '顾客')).toBe('顾客')
    expect(roster.label('c200', '')).toBe('c200')
  })

  it('读出错（抛异常）后正在取的标记清掉，下次还能取', async () => {
    let fail = true
    const roster = customerRosterState(async (ids) => {
      if (fail) throw new Error('offline')
      return ids.map(id => customer(id, id))
    }, () => true)
    await expect(roster.resolve(['a'])).rejects.toThrow('offline')
    fail = false
    await roster.resolve(['a'])
    expect(roster.label('a', '')).toBe('a')
  })
})
