# 后端

Go 服务，技术栈见 [ADR-0007](../docs/adr/0007-p0-technology-stack.md)。

## 命令

需要 Go 1.27 和 Docker（测试用 testcontainers 启动 PostgreSQL）。

```bash
go test ./...                                  # 每个测试包启动一个全新的 PostgreSQL 容器
go tool sqlc generate                          # 修改 internal/db/queries 或迁移后重新生成
DATABASE_URL=postgres://... go run ./cmd/salon migrate up
DATABASE_URL=postgres://... go run ./cmd/salon serve   # HTTP_ADDR 默认 :8080，健康检查 GET /healthz
```

## 租户隔离（ADR-0003、ADR-0005）

- 业务代码只通过 `db.InTenant` 访问数据。它在事务里 `SET LOCAL ROLE salon_app` 并设置 `app.tenant_id`，事务内的查询只能读写该租户的行。
- 新的业务表必须：
  - 有 `tenant_id uuid NOT NULL` 和 `store_id uuid NOT NULL`；
  - 有 `FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)`；
  - `ENABLE ROW LEVEL SECURITY`，并建与 `stores` 相同写法的 `tenant_isolation` 策略（`USING` 和 `WITH CHECK`）；
  - `GRANT` 给 `salon_app`。

  `TestEveryTableIsTenantIsolated` 会检查这几条。
- 行级安全只按租户隔离。同一租户内门店之间的隔离由业务接口的权限检查负责。

## 业务操作（ADR-0001、ADR-0004）

- 每个业务操作在所属模块里用 `ops.Operation` 声明一次（示例：`internal/stores`），在 `httpapi.New` 中注册。
  - HTTP：`POST /api/ops/<操作名>`，请求体就是参数类型。OpenAPI 文档在 `/openapi.json`，`x-permission` 和 `x-ai` 标明所需权限和 AI 调用方式。
  - AI：`Registry.Tools(caller)` 给出该员工能用的工具，参数 schema 取自同一份 OpenAPI 请求体；`Registry.CallTool` 用同一份 schema 校验参数。
- 权限检查只在 `ops` 里做：HTTP 和 AI 调用都经过同一条路径，先检查权限，再在调用者租户的事务里执行。操作处理函数里不再检查权限。
- 写操作只能声明为 `AIDraft`（AI 调用只产生草稿）或 `AINone`（不向 AI 开放），声明为 AI 可直接执行时启动即失败。草稿（P0-29）实现之前，AI 调用写操作一律被拒绝。
- 测试用 `optest` 对 HTTP 和 AI 两条路径跑同一组用例。
