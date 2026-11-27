// 管理后台（ADR-0007）。纯前端渲染，接口地址通过 NUXT_PUBLIC_API_BASE 配置；
// 开发时把 /api 代理到本地后端。
const env = (globalThis as unknown as {
  process?: { env: Record<string, string | undefined> }
}).process?.env
const devApiTarget = env?.NUXT_DEV_API_TARGET || 'http://127.0.0.1:8080/api'
// 构建版（scripts/dev.sh 的 DEV_ADMIN_MODE=preview，CI 的 e2e 用）没有 devProxy，构建时写进这条代理规则；平时构建不设，不带代理。
const previewApiTarget = env?.NUXT_PREVIEW_API_TARGET

export default defineNuxtConfig({
  modules: ['@nuxt/ui', '@nuxt/eslint'],
  ssr: false,
  devtools: { enabled: false },
  // 宋体第一包是数字、字母和标点，页面标题和大数字几乎都会用到；预加载免得首屏先闪一下黑体。
  app: {
    head: {
      htmlAttrs: { lang: 'zh-CN' },
      link: [{ rel: 'preload', href: '/fonts/serif-0.woff2', as: 'font', type: 'font/woff2', crossorigin: 'anonymous' }]
    }
  },
  css: ['~/assets/css/main.css'],
  ui: {
    // 只做浅色（DESIGN.md）：不跟系统切深色，否则系统开了深色时底色变深、字还是墨色，看不清。
    colorMode: false,
    theme: {
      colors: ['primary', 'secondary', 'success', 'info', 'warning', 'error', 'ai']
    }
  },
  runtimeConfig: {
    public: { apiBase: '', e2eHooks: !!previewApiTarget }
  },
  routeRules: previewApiTarget ? { '/api/**': { proxy: `${previewApiTarget}/**` } } : {},
  compatibilityDate: '2026-10-01',
  nitro: {
    devProxy: { '/api': { target: devApiTarget, changeOrigin: true } }
  },
  eslint: {
    config: {
      stylistic: { indent: 2, quotes: 'single', semi: false, commaDangle: 'never' }
    }
  },
  icon: {
    provider: 'none',
    mode: 'svg',
    clientBundle: {
      // nav.ts holds icon names in a .ts file, so include it in the client scan.
      scan: {
        globInclude: ['**/*.{vue,jsx,tsx,ts,md,mdc,mdx,yml,yaml}']
      }
    }
  }
})
