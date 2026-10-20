import { defineConfig } from '@playwright/test'
import { env } from './e2e/env'

// 端到端检查。先运行 scripts/dev.sh up（或 reset），它启动后端、管理后台，并开店建好老板账号。
export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  use: {
    baseURL: env.baseURL,
    screenshot: 'only-on-failure'
  },
  reporter: [['list']]
})
