import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'

// 单元测试只测不依赖 Nuxt 自动导入的模块（utils 里显式 import 的那些）；页面和组件靠 e2e。
export default defineConfig({
  resolve: { alias: { '~': fileURLToPath(new URL('./app', import.meta.url)) } },
  test: { include: ['test/unit/**/*.test.ts'] }
})
