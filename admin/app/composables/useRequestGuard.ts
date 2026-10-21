// 请求有效期：next 作废之前的请求并开始新一轮，hold 取得当前这一轮的检查；在组件里调用时，卸载即作废全部请求。
export function useRequestGuard(): { next: () => () => boolean, hold: () => () => boolean, invalidate: () => void } {
  let generation = 0
  const invalidate = () => {
    generation++
  }
  const hold = () => {
    const current = generation
    return () => current === generation
  }
  const next = () => {
    invalidate()
    return hold()
  }
  if (getCurrentInstance()) onBeforeUnmount(invalidate)
  return { next, hold, invalidate }
}
