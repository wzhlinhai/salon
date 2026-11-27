import { storeTodayState } from '~/utils/storeToday'

// 门店当前状态（营业日 + 这一天执行的门店设置），见 utils/storeToday.ts。
// 用法：const store = useStoreToday()；const today = await store.load()，读不到时 today 为 null、store.error 写原因。
export function useStoreToday() {
  return storeTodayState(async () => {
    const { data, error } = await useApi().POST('/api/ops/get_settings', { body: {} })
    return data ? { data } : { error: errorText(error) }
  })
}
