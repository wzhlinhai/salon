# 后端

Go 服务，技术栈见 [ADR-0007](../docs/adr/0007-p0-technology-stack.md)。

## 命令

需要 Go 1.27 和 Docker（测试用 testcontainers 启动 PostgreSQL）。本地起整套环境（PostgreSQL、后端、管理后台，并开店建好老板账号）用仓库根目录的 `scripts/dev.sh up|down|reset|status`。

```bash
go test ./...                                  # 每个测试包启动一个全新的 PostgreSQL 容器
go tool sqlc generate                          # 修改 internal/db/queries 或迁移后重新生成
go run ./cmd/salon openapi > openapi.json      # 输出 OpenAPI 文档，不需要数据库
golangci-lint run ./...                        # CI 同样运行，配置见 .golangci.yml
scripts/mutcheck.sh <文件> <原文> <替换> [包]   # 临时改坏一处，确认有测试失败，然后还原；改查询 SQL 时自动重新生成
scripts/mutcheck.sh --restore                  # 恢复中断的变异验证，清理未发布的备份槽
scripts/mutchecks.py <清单> [--locate] [名称...] # 运行清单中的保护验证；--locate 只检查原文匹配
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
- 登录凭证（JWT）包含员工、租户和凭证版本。每次请求由 `auth.Service.Authenticate` 从数据库读取员工当前的角色，算出有效权限：各角色取并集，同一项权限的限制取最宽；老板拥有目录中的全部权限。停用员工或收回权限，下一次请求即生效。
- 修改密码、重设账号密码、停用员工后，该员工已签发的登录凭证立即失效。
- 连续 15 次密码错误锁定登录 15 分钟，管理员重设账号密码可立即解锁。
- 有数据范围或时间窗口的操作，处理函数从 `env.Grant` 读取限制并据此过滤。
- 新权限加进 `perm.DefaultRoles` 只影响以后开的店，已有租户的角色不会补上。本地跑端到端测试前先 `scripts/dev.sh reset`。
- 授权规则在 `internal/staff`：只有老板能授予或去掉老板角色；其他人授予、收回的角色和编辑的权限不能超过自己；不能修改权限比自己大的员工的档案、账号或在职状态；在职老板不能变成 0 人（用老板角色的行锁串行检查）。修改另一名员工的操作一律经过 `change`：先锁老板角色排队，再锁员工行，然后做可管理检查、老板底线和改前改后日志。新建员工、编辑或删除角色也先锁同一老板角色排队，确保授权检查期间角色权限不会改变。
- 员工记录不能删除：`salon_app` 没有 `staff` 表的 DELETE 权限。
- 登录时查找账号是唯一不经过 `db.InTenant` 的数据访问：此时还不知道租户，`auth.Service.Login` 以 `salon_app` 角色调用 `auth_find_login`（SECURITY DEFINER 函数，只返回登录所需的字段）。这条查询是手写的，因为 sqlc 推断不出函数返回的列。找到账号后，在 `db.InTenant` 事务里锁住员工并校验密码、更新失败计数。
- 角色列表分两个操作：`list_roles` 含权限配置，需要角色权限管理；`list_role_options` 只有名称，需要员工档案管理，用于给员工分配角色。

## 顾客档案

修改已有顾客的操作一律经过 `internal/customers` 的 `change`（按 ID 或按负责人选中），由它负责加锁、查看范围、已合并检查、改前改后日志和手机号打码。新建顾客用 `created`。

## 门店设置（ADR-0002）

- 设置值支持 time（时刻）、time_range（时段）、choice（Options 中的字符串）和 integer（Min/Max 内的非负整数）。`member_price_rule` 默认会员价优先，也可取较低价；`points_per_recharge_yuan` 为每积一分所需充值本金元数，默认 0（不积分），范围 0–100000。开卡、充值和升级补差价按当日规则积分，赠送金、转余额和调整余额不积分。
- 设置项在 `internal/settings` 的 `Catalogue` 里声明：名称、值的形式、系统默认做法和校验。新的业务规则（例如各项业务的做法选择）在这里加一项，再给 `Rules` 加一个读取它的方法。
- 每次修改都在 `setting_changes` 追加一行：改前、改后、生效日期、修改人、修改时间。`salon_app` 只有 SELECT 和 INSERT 权限。某个营业日执行的值，取生效日期不晚于这一天的最后一次修改；没有修改，或者改成了"恢复默认"，就用系统当前的默认做法。生效日期不能早于今天；划分营业日的设置（`fromTomorrow`，例如营业日结束时间）最早明天生效。同一项设置的修改用咨询锁排队，改前值才接得上前一次的改后值。
- 业务模块不直接查设置表：
  - `settings.BusinessDate` 算一个时刻属于哪个营业日，营业日 D 在第二天"营业日结束时间"截止，结束时间取 D 那天执行的设置；
  - `settings.Effective` 取某个营业日执行的设置；
  - 单据下单时调用 `settings.TakeSnapshot`，把返回的快照 ID 存进单据，之后计算一律用 `settings.LoadSnapshot` 读回的规则，这样以后改设置不会影响已下的单。内容相同的快照共用一行。

## 单据

开单必须选择男客或女客、指定或非指定；未提供顾客时自动创建未识别顾客，由开单权限覆盖。开单保存规则快照和营业日，后续加项目读取快照规则。项目行保留添加时的项目名称，并复制项目岗位，允许必填岗位暂空；级别价来自第一个岗位的员工，没有员工或级别价时取标准价。多人贡献只在项目行记录一次价格，应收按项目行求和。

锁按顾客 → 单 → 项目 → 按 UUID 排序的员工取得：已有顾客、项目和员工只检查状态，用 FOR SHARE；加项目在读改前视图前用 FOR NO KEY UPDATE 锁单。单仅授予 customer_id、status、merged_into 列的 UPDATE，顾客合并仍可改挂 customer_id。项目行和岗位保留原记录：项目行仅授予 order_id、价格、来源、级别、改价原因及去掉标记列的 UPDATE，岗位仅授予 staff_id 的 UPDATE；三张表都不允许 DELETE。

改单需要 order.edit，时间窗口按单的 business_date 与当前营业日比较；本人范围包含 opened_by 或任意项目行岗位上的员工，去掉的行也算出现过。去掉项目只标记，不计入应收。换岗位员工提交完整分配，未列出的岗位清空；仅在 slot 1 员工改变且项目行未改价时按单的规则快照重新取级别价或标准价。改价需要 order.reprice 和原因，改后不再随员工重取价。改单锁顺序为单 → 项目（仅重取价）→ 按 UUID 排序的员工。

合并单需要 order.edit，对每张单检查时间窗口和本人范围，只允许同一顾客、同一营业日的挂单合并。全部项目行（包括去掉的行）移到保留单，其余单保留，标为 merged 并以 merged_into 引用保留单；保留单的其他属性不变。客次按未并入其他单的单计，已合并单不单独计客次。每个已有单的写操作先在本店以 FOR NO KEY UPDATE 锁单并检查 open 状态，再做范围检查及读取改前视图；已合并单拒绝写入。合并按 UUID 字符串排序逐张锁单，不锁顾客、项目或员工；顾客合并也在改挂前按 UUID 锁住该顾客的单；其他改单先锁单，再锁项目及按 UUID 排序的员工。

## 操作日志

- `audit_log` 只追加，`salon_app` 只有 SELECT 和 INSERT 权限。日志与业务操作在同一事务里写入，任何一步失败都会一起回滚。同一员工或角色的修改用行锁排队，在读取改前快照前加锁，日志的改前才接得上前一次的改后。
- 当前记录角色的新建、修改、删除，以及员工的新建、档案修改、登录设置、停用、恢复和角色分配；保留改前改后，删除角色时还保留持有员工的 ID、姓名。`list_audit_log` 需要操作日志权限，可按目标 ID 查询。每条日志记录目标种类及其关联顾客；`list_audit_log` 按调用者的顾客查看范围过滤关联顾客的日志。卡类型、开卡、充值、开单、加项目、去掉项目、换岗位员工、改价、合并单已记录日志。新增关联顾客的种类（例如单）时，添加接收顾客 ID 的构造函数，并扩展迁移中的 CHECK 列表。
- 新操作在成功路径末尾用同一个 `env` 调用 `audit.Record`，传入不含密码或密码哈希的返回值视图；新建前、删除后传 nil。手机号字段的 JSON 键名必须是 `phone`，`audit.Record` 遇到别的键名会拒绝写入（读取时只按这个键名打码）。
