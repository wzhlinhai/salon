import { ref } from 'vue'

// 这个文件不用 Nuxt 的自动导入，单元测试（test/unit）能直接引；页面用 composables/usePageLoad.ts。

// 一路读取的结果：openapi-fetch 的 { data, error }；null 表示这次不取（例如没权限）。
export type Fetched = { data?: unknown, error?: unknown } | null

// 每一路是一个请求（或现成的结果）；apply 收到的是各路的结果，顺序不变。
export type Requests = readonly (Fetched | Promise<Fetched>)[]
export type Results<T extends Requests> = { -readonly [K in keyof T]: Awaited<T[K]> }

export type PageLoadOptions<T extends Requests> = {
  // 几路一起发，返回各路请求（这里统一等齐，页面不写 Promise.all）。
  fetch: () => T
  // 必须的几路都读到了才调，可选的那几路读不到时 data 为空。
  apply: (results: Results<NoInfer<T>>) => void
  // 页面上已经有数据吗：有就留着、只弹一条提示（刷新失败）；没有就原地给「重试」（error）。
  hasData: () => boolean
  // 原地报错时顺带清掉的东西（例如换了月份后，上个月的数不能留着冒充这个月）。
  onFail?: () => void
  // 这几路（下标）读不到也照样 apply，各弹一条提示。
  optional?: readonly number[]
}

export type PageLoadDeps = {
  describe: (error: unknown) => string
  notify: (text: string) => void
}

// 页面读取（DESIGN「读取出错」）：loading、请求有效期（只有最后一次 load 能改页面）、
// 「从没读到过就原地重试，已有数据就弹提示」、必须和可选分开，都在这里；页面只给取数和写数。
export function pageLoadState<const T extends Requests>(options: PageLoadOptions<T>, deps: PageLoadDeps) {
  const loading = ref(false)
  const error = ref('')
  let latest = 0
  const optional = new Set(options.optional ?? [])

  function invalidate() {
    latest++
  }

  function fail(text: string) {
    if (options.hasData()) {
      deps.notify(text)
      return
    }
    error.value = text
    options.onFail?.()
  }

  async function load() {
    const seq = ++latest
    loading.value = true
    // 重试或换了条件（月份）时，上一次的报错先收起，别让它挂在新条件的标题下。
    error.value = ''
    try {
      let results: Results<T>
      try {
        results = await Promise.all(options.fetch()) as Results<T>
      } catch (e) {
        if (seq === latest) fail(deps.describe(e))
        return
      }
      if (seq !== latest) return
      const missing = results.find((r, i) => r && !r.data && !optional.has(i))
      if (missing) {
        fail(deps.describe(missing.error))
        return
      }
      results.forEach((r, i) => {
        if (r && !r.data && optional.has(i)) deps.notify(deps.describe(r.error))
      })
      error.value = ''
      options.apply(results)
    } finally {
      if (seq === latest) loading.value = false
    }
  }

  return { loading, error, load, invalidate }
}
