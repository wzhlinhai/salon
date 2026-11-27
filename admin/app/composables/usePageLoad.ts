import { pageLoadState, type PageLoadOptions, type Requests } from '~/utils/pageLoad'

// 页面读取，见 utils/pageLoad.ts：错误说明用 errorText，提示用 toast，组件卸载时作废还在路上的读取。
export function usePageLoad<const T extends Requests>(options: PageLoadOptions<T>) {
  const toast = useToast()
  const state = pageLoadState(options, {
    describe: errorText,
    notify: title => toast.add({ title, color: 'error' })
  })
  if (getCurrentInstance()) onBeforeUnmount(state.invalidate)
  return state
}
