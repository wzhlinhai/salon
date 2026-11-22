import type { ReportRange } from '~/utils/report'

// 只按营业日查的报表页（员工业绩、项目、卡项、新客来源）共用：先取营业日定默认范围（本月），
// 范围写在网址 ?from=&to= 里，范围一变就查；范围无效不查，也作废还在路上的旧请求。
// fetch 传一个按起止营业日取数的函数，返回 openapi-fetch 的 { data, error }。
export function useDayReport<T>(fetch: (from: string, to: string) => Promise<{ data?: T, error?: unknown }>) {
  const api = useApi()
  const route = useRoute()
  const router = useRouter()
  const toast = useToast()
  const today = ref('')
  const range = ref<ReportRange>({ granularity: 'day', from: '', to: '' })
  const report = ref<T | null>(null) as Ref<T | null>
  const loading = ref(false)
  const failed = ref(false)
  const guard = useRequestGuard()

  function fromQuery(): ReportRange | null {
    const { from, to } = route.query
    const pattern = /^\d{4}-\d{2}-\d{2}$/
    if (typeof from !== 'string' || typeof to !== 'string' || !pattern.test(from) || !pattern.test(to)) return null
    const asked: ReportRange = { granularity: 'day', from, to }
    return rangeProblem(asked, true) ? null : asked
  }

  async function load() {
    const current = guard.next()
    if (rangeProblem(range.value, true)) {
      loading.value = false
      return
    }
    loading.value = true
    failed.value = false
    try {
      const { data, error } = await fetch(range.value.from, range.value.to)
      if (!current()) return
      if (!data) {
        failed.value = true
        report.value = null
        toast.add({ title: errorText(error), color: 'error' })
        return
      }
      report.value = data
    } finally {
      if (current()) loading.value = false
    }
  }

  async function start() {
    failed.value = false
    const settings = await api.POST('/api/ops/get_settings', { body: {} })
    if (!settings.data) {
      failed.value = true
      toast.add({ title: errorText(settings.error), color: 'error' })
      return
    }
    today.value = settings.data.business_date
    range.value = fromQuery() ?? defaultRange('day', today.value)
  }

  function retry() {
    void (today.value ? load() : start())
  }

  onMounted(start)

  watch(range, (value) => {
    void router.replace({ query: { from: value.from, to: value.to } })
    void load()
  }, { deep: true })

  return { today, range, report, loading, failed, retry }
}
