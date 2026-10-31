import withNuxt from './.nuxt/eslint.config.mjs'

export default withNuxt(
  {
    ignores: ['app/utils/api.d.ts', '.nuxt/**', '.output/**', 'playwright-report/**', 'test-results/**', 'blob-report/**']
  },
  {
    rules: {
      // 保留现有模板的行内内容和多属性排版，避免无关的大面积格式变动。
      'vue/singleline-html-element-content-newline': 'off',
      'vue/max-attributes-per-line': 'off',
      // 每行最多一条语句，避免多条操作挤在同一行。
      '@stylistic/max-statements-per-line': ['error', { max: 1 }],
      // 块语句必须换行，避免整段函数或条件分支挤成一行。
      '@stylistic/brace-style': ['error', '1tbs', { allowSingleLine: false }],
      // 函数声明前后留空行，让相邻函数和声明块的边界清晰。
      '@stylistic/padding-line-between-statements': [
        'error',
        { blankLine: 'always', prev: '*', next: 'function' },
        { blankLine: 'always', prev: 'function', next: '*' }
      ]
    }
  },
  {
    files: ['e2e/**/*.ts'],
    rules: {
      // 固定等待不能确认前端回调已执行，保护失效时测试仍可能通过。
      'no-restricted-syntax': ['error', {
        selector: 'CallExpression[callee.property.name="waitForTimeout"]',
        message: '等一个可观察的同步点（例如按钮从 loading 恢复、请求的 finished()、元素出现），不要固定等待'
      }, {
        selector: 'CallExpression[callee.property.name="getByRole"][arguments.length=1][arguments.0.type="Literal"][arguments.0.value="dialog"]',
        message: '对话框按名字定位：getByRole(\'dialog\', { name: \'…\', exact: true })，避免把开发模式的错误浮层当成业务窗口'
      }, {
        selector: 'MemberExpression[computed=false][property.name=/^(__vueParentComponent|__vue_app__|setupState|exposed)$/], MemberExpression[computed=true][property.value=/^(__vueParentComponent|__vue_app__|setupState|exposed)$/], ObjectPattern > Property[key.name=/^(__vueParentComponent|__vue_app__|setupState|exposed)$/]',
        message: '经真实页面操作或 API 触发要测的场景，不要改组件内部状态'
      }]
    }
  },
  {
    files: ['e2e/helpers.ts'],
    rules: {
      // 沿用现有测试请求辅助函数的默认类型，本次只整理格式。
      '@typescript-eslint/no-explicit-any': 'off'
    }
  }
)
