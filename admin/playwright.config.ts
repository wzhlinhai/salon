import { defineConfig } from '@playwright/test'
import { env } from './e2e/env'

// 全套端到端测试须先运行 scripts/dev.sh reset 使用全新数据库，因为 payroll-store 项目会永久锁定上个月。
export default defineConfig({
  testDir: './e2e',
  // 先留存今天的工资提成样本；默认项目会安排未来方案，之后不能回填今天。
  globalSetup: './e2e/payroll.setup.ts',
  timeout: 60_000,
  // 首次打开页面要等 Nuxt 编译和设置加载；5 秒在后端稍慢时会误报。
  expect: { timeout: 10_000 },
  // 收银和团购用例也共用同一套后端与 Postgres；限制并发，避免无关用例的短等待偶发超时。
  workers: 2,
  // 只有全店顾客分页用例依赖“最新 50 位全是本用例创建”的数据；等默认项目结束后独占运行，避免其他用例建顾客干扰断言。
  projects: [
    { name: 'default', grepInvert: /@store-customer-dataset|@payroll-store|@groupbuy-month/ },
    // 工资表用例保存全店提成方案和锁月，必须等默认项目读取提成数据结束。
    { name: 'payroll-store', grep: /@payroll-store/, dependencies: ['default'] },
    { name: 'store-customer-dataset', grep: /@store-customer-dataset/, dependencies: ['default'] },
    // 团购对账是门店级汇总，必须等默认项目里的核销都结束再断言。
    { name: 'groupbuy-month', grep: /@groupbuy-month/, dependencies: ['default'] }
  ],
  use: {
    baseURL: env.baseURL,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure'
  },
  reporter: [['list']]
})
