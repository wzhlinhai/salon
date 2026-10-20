# 管理后台

Nuxt 4 + Nuxt UI（ADR-0007）。纯前端渲染，所有数据通过后端的业务操作接口读写。

```bash
npm install
npm run generate:api   # 从后端生成 OpenAPI 文档和 TypeScript 类型（需要 Go）
npm run dev            # http://localhost:3000，/api 代理到 127.0.0.1:8080
npm run typecheck
```

- 接口类型 `app/utils/api.d.ts` 由 `npm run generate:api` 生成，不要手改。后端接口有变化时重新生成并提交。
- 页面用 `definePageMeta({ permission: '...' })` 声明所需权限。导航只显示有权限的页面，直接访问会跳到无权限页。权限以后端检查为准，页面只是隐藏入口。
- 生产环境用 `NUXT_PUBLIC_API_BASE` 指定后端地址。
