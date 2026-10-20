// 管理后台（ADR-0007）。纯前端渲染，接口地址通过 NUXT_PUBLIC_API_BASE 配置；
// 开发时把 /api 代理到本地后端。
export default defineNuxtConfig({
  modules: ['@nuxt/ui', '@nuxt/eslint'],
  ssr: false,
  devtools: { enabled: false },
  css: ['~/assets/css/main.css'],
  runtimeConfig: {
    public: { apiBase: '' }
  },
  compatibilityDate: '2026-10-01',
  nitro: {
    devProxy: { '/api': { target: 'http://127.0.0.1:8080/api', changeOrigin: true } }
  },
  eslint: {
    config: {
      stylistic: { indent: 2, quotes: 'single', semi: false, commaDangle: 'never' }
    }
  }
})
