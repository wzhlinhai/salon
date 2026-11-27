import { computed, ref } from 'vue'
import type { components } from './api'

// 这个文件不用 Nuxt 的自动导入，单元测试（test/unit）能直接引。
export type StoreToday = components['schemas']['SettingList']
export type StoreSetting = components['schemas']['Setting']
export type StoreTodayResult = { data?: StoreToday, error?: string }

// 门店当前状态：营业日和这一天执行的门店设置（设置用系统默认做法时，value 就是后端给的默认值，前端不另写默认）。
// 每次 load 都重新问后端（营业日会跨过日界线变；刚改过设置要读到新的），不在页面之间留旧的，也不合并在路上的请求。
// 只有最后一次 load 的结果写进来，先发后到的旧结果不覆盖新的；读失败时留着上次读到的，error 写原因。
// 被后一次 load 作废的那次不写 error，所以一个实例只给一条读取流程用；页面里有两处各自读的，各建一份。
export function storeTodayState(fetch: () => Promise<StoreTodayResult>) {
  const data = ref<StoreToday | null>(null)
  const error = ref('')
  const businessDate = computed(() => data.value?.business_date ?? '')
  const settings = computed(() => data.value?.settings ?? [])

  function setting(key: string): StoreSetting | undefined {
    return settings.value.find(s => s.key === key)
  }

  function value(key: string): unknown {
    return setting(key)?.value
  }

  let latest = 0

  async function load(): Promise<StoreToday | null> {
    const seq = ++latest
    const result = await fetch()
    if (seq !== latest) return result.data ?? null
    if (!result.data) {
      error.value = result.error || '操作失败'
      return null
    }
    data.value = result.data
    error.value = ''
    return result.data
  }

  return { businessDate, settings, setting, value, error, load }
}

// 营业日所在月的 1 号。
export function monthStart(day: string) {
  return `${day.slice(0, 7)}-01`
}
