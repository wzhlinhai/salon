# 管理后台

Nuxt 4 + Nuxt UI（ADR-0007）。纯前端渲染，所有数据通过后端的业务操作接口读写。

```bash
npm install
npm run generate:api   # 从后端生成 OpenAPI 文档和 TypeScript 类型（需要 Go）
npm run dev            # http://localhost:3000，/api 代理到 127.0.0.1:8080
npm run lint           # 检查代码格式
npm run typecheck
```

- 接口类型 `app/utils/api.d.ts` 由 `npm run generate:api` 生成，不要手改。后端接口有变化时重新生成并提交。
- 页面用 `definePageMeta({ permission: '...' })` 声明所需权限。导航只显示有权限的页面，直接访问会跳到无权限页。权限以后端检查为准，页面只是隐藏入口。
- 生产环境用 `NUXT_PUBLIC_API_BASE` 指定后端地址。
- 页面里的请求用 `useRequestGuard` 判断结果是否过期（加载用 `next`，编辑窗口里的写操作用 `hold`，打开窗口或换对象时 `next`），卸载时自动作废；游标分页用 `useCursorList`。

- 弹窗表单用 `useModalForm`：`begin` 复位并开启一次有效期，`load` 和 `save` 管理加载、保存及 finally 复位；回调写页面状态前检查传入的 `current()`。关闭、重开或卸载使旧请求失效。
- `USelect` / `SelectItem` 的值不能是空字符串（Nuxt UI 会在运行时报错）；可选项用占位值（如 `__all__`），保存时映射为不发送该字段。
- 顾客详情的卡列表、开卡充值、调整、升级转余额和流水在 `CustomerCards` 中，切换顾客时详情先清空 `customer` 使卡面板卸载并重新挂载，卸载失效会丢弃旧响应，因此面板不逐响应比较 ID，保存后的列表和已展开明细统一由 `refreshCards` 刷新。
