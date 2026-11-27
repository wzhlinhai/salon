import { describe, expect, it } from 'vitest'
import { pageLoadState, type Fetched, type Requests } from '~/utils/pageLoad'

function harness(answers: (() => Requests)[], extra: { optional?: number[], fresh?: boolean } = {}) {
  const notes: string[] = []
  const applied: Fetched[][] = []
  let cleared = 0
  const state = pageLoadState({
    fetch: () => answers.shift()!(),
    apply: (r) => {
      applied.push(r)
    },
    hasData: () => !extra.fresh && applied.length > 0,
    onFail: () => {
      cleared++
    },
    optional: extra.optional
  }, {
    describe: e => (e as { detail?: string } | undefined)?.detail ?? '操作失败',
    notify: t => notes.push(t)
  })
  return { state, notes, applied, cleared: () => cleared }
}

const ok = (data: unknown = {}) => ({ data })
const bad = (detail: string) => ({ error: { detail } })

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

describe('pageLoadState', () => {
  it('必须的几路都读到才写，loading 收起', async () => {
    const h = harness([() => [ok(1), ok(2)]])
    const pending = h.state.load()
    expect(h.state.loading.value).toBe(true)
    await pending
    expect(h.applied).toHaveLength(1)
    expect(h.state.loading.value).toBe(false)
    expect(h.state.error.value).toBe('')
  })

  it('从没读到过时有一路失败：原地报错，不写，不弹提示', async () => {
    const h = harness([() => [ok(), bad('项目没读到')]])
    await h.state.load()
    expect(h.applied).toHaveLength(0)
    expect(h.state.error.value).toBe('项目没读到')
    expect(h.notes).toEqual([])
    expect(h.cleared()).toBe(1)
  })

  it('已经有数据时刷新失败：只弹提示，留着旧的', async () => {
    const h = harness([() => [ok()], () => [bad('刷新失败')]])
    await h.state.load()
    await h.state.load()
    expect(h.applied).toHaveLength(1)
    expect(h.state.error.value).toBe('')
    expect(h.notes).toEqual(['刷新失败'])
    expect(h.cleared()).toBe(0)
  })

  it('总要最新数据的页面（hasData 恒为否）：刷新失败也原地报错并清掉旧的', async () => {
    const h = harness([() => [ok()], () => [bad('换了月份没读到')]], { fresh: true })
    await h.state.load()
    await h.state.load()
    expect(h.state.error.value).toBe('换了月份没读到')
    expect(h.cleared()).toBe(1)
  })

  it('重试成功后清掉原地的报错', async () => {
    const h = harness([() => [bad('第一次失败')], () => [ok()]])
    await h.state.load()
    await h.state.load()
    expect(h.state.error.value).toBe('')
    expect(h.applied).toHaveLength(1)
  })

  it('重试一发出去就收起上次的报错，不等结果回来', async () => {
    const gate = deferred<Fetched>()
    const h = harness([() => [bad('上个月没读到')], () => [gate.promise]])
    await h.state.load()
    const pending = h.state.load()
    expect(h.state.error.value).toBe('')
    gate.resolve(ok())
    await pending
    expect(h.applied).toHaveLength(1)
  })

  it('可选的一路读不到：照样写，并弹一条提示', async () => {
    const h = harness([() => [ok(), bad('规则没读到'), bad('项目没读到')]], { optional: [1, 2] })
    await h.state.load()
    expect(h.applied).toHaveLength(1)
    expect(h.notes).toEqual(['规则没读到', '项目没读到'])
  })

  it('null 的一路（这次不取）不算失败', async () => {
    const h = harness([() => [ok(), null]])
    await h.state.load()
    expect(h.applied).toHaveLength(1)
  })

  it('先发后到的旧结果不写，也不改 loading', async () => {
    const old = deferred<Fetched>()
    const h = harness([() => [old.promise], () => [ok('新')]])
    const first = h.state.load()
    await h.state.load()
    old.resolve(bad('迟到的错误'))
    await first
    expect(h.applied).toEqual([[ok('新')]])
    expect(h.state.error.value).toBe('')
    expect(h.notes).toEqual([])
    expect(h.state.loading.value).toBe(false)
  })

  it('作废后（组件卸载）回来的结果什么都不做', async () => {
    const gate = deferred<Fetched>()
    const h = harness([() => [gate.promise]])
    const pending = h.state.load()
    h.state.invalidate()
    gate.resolve(bad('卸载后的错误'))
    await pending
    expect(h.state.error.value).toBe('')
    expect(h.notes).toEqual([])
  })

  it('取数抛异常按失败处理', async () => {
    const h = harness([() => [Promise.reject(new Error('boom'))]])
    await h.state.load()
    expect(h.state.error.value).toBe('操作失败')
    expect(h.state.loading.value).toBe(false)
  })
})
