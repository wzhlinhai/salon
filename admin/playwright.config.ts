import { defineConfig } from '@playwright/test'
import { env } from './e2e/env'

// 端到端检查。先运行 scripts/dev.sh up（或 reset），它启动后端、管理后台，并开店建好老板账号。
export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  // 只有全店顾客分页用例依赖“最新 50 位全是本用例创建”的数据；等默认项目结束后独占运行，避免其他用例建顾客干扰断言。
  projects: [
    { name: 'default', grepInvert: /@store-customer-dataset/ },
    { name: 'store-customer-dataset', grep: /@store-customer-dataset/, dependencies: ['default'] }
  ],
  use: {
    baseURL: env.baseURL,
    screenshot: 'only-on-failure'
  },
  reporter: [['list']]
})
