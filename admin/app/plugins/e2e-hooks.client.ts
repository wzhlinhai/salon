// 给 e2e 用的入口：有些竞态（迟到的 401、迟到的 get_me）页面上没有按钮能直接触发，测试要在页面里直接调这两个 composable。
// 只在开发服务器和 e2e 用的构建版（构建时设了 NUXT_PREVIEW_API_TARGET）里挂上，平时构建不挂。
export default defineNuxtPlugin((nuxtApp) => {
  if (!import.meta.dev && !useRuntimeConfig().public.e2eHooks) return
  ;(window as unknown as { __salonE2E: unknown }).__salonE2E = {
    getStore: () => nuxtApp.runWithContext(() => useApi().POST('/api/ops/get_store', { body: {} })),
    loadMe: () => nuxtApp.runWithContext(() => useMe().load())
  }
})
