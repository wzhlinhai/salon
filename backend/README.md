# 后端

Go 服务，技术栈见 [ADR-0007](../docs/adr/0007-p0-technology-stack.md)。

## 命令

需要 Go 1.27 和 Docker（测试用 testcontainers 启动 PostgreSQL）。本地起整套环境（PostgreSQL、后端、管理后台，并开店建好老板账号）用仓库根目录的 `scripts/dev.sh up|down|reset|status`。

```bash
go test ./...                                  # 每个测试包启动一个全新的 PostgreSQL 容器
go tool sqlc generate                          # 修改 internal/db/queries 或迁移后重新生成
go run ./cmd/salon openapi > openapi.json      # 输出 OpenAPI 文档，不需要数据库
golangci-lint run ./...                        # CI 同样运行，配置见 .golangci.yml
scripts/mutcheck.sh <文件> <原文> <替换> [包]   # 临时改坏一处，确认有测试失败，然后还原
DATABASE_URL=postgres://... go run ./cmd/salon migrate up
DATABASE_URL=postgres://... BOOTSTRAP_PASSWORD=... go run ./cmd/salon bootstrap \
  -tenant 本店 -store 本店 -owner-name 姓名 -owner-number 001 -owner-account boss
DATABASE_URL=postgres://... JWT_SECRET=至少32字节 go run ./cmd/salon serve
```

`serve` 的可选环境变量：`HTTP_ADDR`（默认 `:8080`）、`TOKEN_TTL`（默认 `12h`）。健康检查 `GET /healthz`，登录 `POST /api/auth/login`。

`bootstrap` 开一个新租户：门店、内置的老板角色、四个默认角色（初始权限见 ADR-0009）和第一名老板。

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
- 测试用 `optest` 对 HTTP 和 AI 两条路径跑同一组用例。草稿（P0-29）实现之前，写操作在 AI 路径上走不到业务规则（草稿类返回"需要草稿"，`AINone` 对 AI 不存在），所以写操作的业务规则只在 HTTP 路径上测，另用一个测试确认 AI 路径对这些写操作全部拒绝且数据不变。

## 登录与权限（ADR-0009）

- 权限目录在 `internal/perm`。新的业务操作用这里的常量声明所需权限；只要登录即可的操作用 `ops.LoggedIn`。
- 登录凭证（JWT）只有员工和租户。每次请求由 `auth.Service.Authenticate` 从数据库读取员工当前的角色，算出有效权限：各角色取并集，同一项权限的限制取最宽；老板拥有目录中的全部权限。停用员工或收回权限，下一次请求即生效。
- 有数据范围或时间窗口的操作，处理函数从 `env.Grant` 读取限制并据此过滤。
- 授权规则在 `internal/staff`：只有老板能授予或去掉老板角色；其他人授予、收回的角色和编辑的权限不能超过自己；不能修改权限比自己大的员工的档案、账号或在职状态；在职老板不能变成 0 人（用老板角色的行锁串行检查）。
- 员工记录不能删除：`salon_app` 没有 `staff` 表的 DELETE 权限。
- 登录是唯一不经过 `db.InTenant` 的数据访问：登录时还不知道租户，`auth.Service.Login` 以 `salon_app` 角色调用 `auth_find_login`（SECURITY DEFINER 函数，只返回登录所需的字段）。这条查询是手写的，因为 sqlc 推断不出函数返回的列。
- 角色列表分两个操作：`list_roles` 含权限配置，需要角色权限管理；`list_role_options` 只有名称，需要员工档案管理，用于给员工分配角色。
