# 后端

Go 服务，技术栈见 [ADR-0007](../docs/adr/0007-p0-technology-stack.md)。

## 命令

需要 Go 1.27 和 Docker（测试用 testcontainers 启动 PostgreSQL，数据目录 `/var/lib/postgresql` 挂载 tmpfs，不留下匿名卷）。本地起整套环境（PostgreSQL、后端、管理后台，并开店建好老板账号）用仓库根目录的 `scripts/dev.sh up|down|reset|status`。

```bash
go test ./...                                  # 每个测试包启动一个全新的 PostgreSQL 容器
go tool sqlc generate                          # 修改 internal/db/queries 或迁移后重新生成
go run ./cmd/salon openapi > openapi.json      # 输出 OpenAPI 文档，不需要数据库
golangci-lint run ./...                        # CI 同样运行，配置见 .golangci.yml
scripts/mutcheck.sh [--run <regex>] <文件> <原文> <替换> [包] # 临时改坏一处，确认有测试失败，然后还原；改查询 SQL 时自动重新生成
scripts/mutcheck.sh --restore                  # 恢复中断的变异验证，清理未发布的备份槽
scripts/mutchecks.py <清单> [--since <git-ref>] [--locate] [名称...] # go 项只运行 guards（含子测试）；每包先跑完整基线
scripts/mutchecks.py --since <git-ref> [--jobs N]          # 不带清单：逐份运行 .dev/codex/*/manifest.py 的受影响项；N 最多 4
DATABASE_URL=postgres://... go run ./cmd/salon migrate up
DATABASE_URL=postgres://... BOOTSTRAP_PASSWORD=... go run ./cmd/salon bootstrap \
  -tenant 本店 -store 本店 -owner-name 姓名 -owner-number 001 -owner-account boss
DATABASE_URL=postgres://... JWT_SECRET=至少32字节 go run ./cmd/salon serve
```

`mutchecks.py --since <git-ref>` 只选中该引用与工作树（含未跟踪文件）之间受影响的项，可与名称和 `--locate` 组合。保护文件、定义 guard 的包内测试文件或 e2e spec 改动会选中对应项；迁移项只在自身文件改动、或改动的迁移涉及它保护的表时选中，查询项只在自身 `.sql` 文件改动、或改动的迁移涉及它查询的表时选中。表从迁移的建表/改表/删表、索引、外键、写入、`DELETE … USING`、逗号 `FROM`、`UPDATE … FROM`、授权语句和策略的 `USING`/`WITH CHECK` 子查询读取；改动的迁移同时比较 ref 与工作树版本（重命名按删除加新增），带引号的名字按表名解析。认不出表的项保守选中，改动的迁移含函数/视图/规则、`DO` 块或无法归属的语句时选中全部迁移和查询项；查询用到迁移里 `CREATE VIEW` 的名字时也保守选中。`stderr` 会输出改动的表以及按表规则跳过的数量。每轮可运行受影响项，同时不带 `--since` 运行 `--locate` 检查整份清单的原文匹配。正式运行默认给每个 go worker 一个 PostgreSQL 容器并通过 `SALON_TEST_PG_DSN` 复用，结束或中断时删除；`--no-shared-pg` 恢复每个 `go test` 自启容器。手动使用 `mutcheck.sh` 时测试选择只通过 `--run` 传入；省略它仍运行完整包。

所有保护验证都要通过 `scripts/gate.py` 运行。`mutchecks.py` 正式运行和单独运行的 `mutcheck.sh` 也会持有与 `gate.py` 相同的 `flock` 重锁（`GATE_HEAVY_LOCK`，否则 `.dev/heavy.lock`），等待时打印 `waiting for heavy lock <path>`；`gate.py` 用 `SALON_HEAVY_LOCK_HELD` 标记父进程已持有的锁，子进程不重复加锁。`--locate` 和内部 `--worker-logs` 进程不加锁。`--jobs` 最多 4（`MUTCHECK_MAX_JOBS` 可调低），超过时打印提示并按上限运行；`--jobs` 大于 1 时每个 go worker 用 `-p max(1, CPU/jobs)` 和同样的 `GOMAXPROCS`，把全部 worker 的编译并行度限制在 CPU 数附近，`--jobs 1` 保持原行为。

保护检查不写用户的默认 Go 构建缓存：`mutchecks.py` 每次正式运行（包括 `--jobs 1`）以及 `--since` 的 `go list` 在 `.dev/mutcheck-gocache/<run>/` 下建立独立 `GOCACHE`，并传给基线、`mutcheck.sh` 和每个副本；只有正式运行开始时清理残留运行目录，成功、失败、异常、Ctrl-C 或 SIGTERM 时删除本次缓存。`--locate` 不运行 go 命令时不创建缓存，也不清理残留目录。基线共享该缓存；每个变异副本另建缓存并硬链接基线条目（含 `xx/<hash>-d/` 可执行目录），跑完一项后删除该变异新增的条目，峰值只随并发副本数和一个变异的编译量增长。所有 `go test` 带 `-trimpath`，不同路径的副本因此共享编译结果。基线前和每项变异前检查缓存所在磁盘剩余空间，低于 20 GB 时用中文消息停止，未运行项计为 `environment` 并以非零码退出，不计为 caught；测试可用 `MUTCHECK_DISK_HEADROOM_BYTES` 调低该下限。单独运行 `mutcheck.sh`（含 `--restore`）且没有设置 `GOCACHE` 时，同样在 `.dev/` 下使用临时缓存并在退出时删除。

`mutcheck.sh` 退出码：0 测试抓到了变异；1 没有抓到；2 用法、匹配、构建错误，或变异让迁移无法执行（清单错误，`mutchecks.py` 计为 failed）；3 测试失败但没有 `--- FAIL` 行（超时、panic 等）；4 PostgreSQL 容器等环境失败（重试一次后仍失败，计为 not run (environment)）。迁移文件被改坏后 `migrate up` 失败会报 `mutcheck: mutated migration does not apply:` 并退出 2，不再重试或计为环境失败。

`serve` 的可选环境变量：`HTTP_ADDR`（默认 `:8080`）、`TOKEN_TTL`（默认 `12h`）。健康检查 `GET /healthz`，登录 `POST /api/auth/login`。AI 对话的环境变量：`AI_API_KEY`（设置后才启用 `/api/ai/chat`；未设置时返回 503「AI 未启用」）、`AI_BASE_URL`（默认 `https://api.deepseek.com`）、`AI_MODEL`（默认 `deepseek-flash`）。密钥不写日志，也不出现在错误或响应里。

`bootstrap` 开一个新租户：门店、内置的老板角色、四个默认角色（初始权限见 ADR-0009）和第一名老板。

## 租户隔离（ADR-0003、ADR-0005）

- 业务代码只通过 `db.InTenant` 访问数据。它在事务里 `SET LOCAL ROLE salon_app` 并设置 `app.tenant_id`，事务内的查询只能读写该租户的行。
- 新的业务表必须：
  - 有 `tenant_id uuid NOT NULL` 和 `store_id uuid NOT NULL`；
  - 有 `FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)`；
  - 引用门店或其他业务表的外键都是组合外键，`tenant_id` 对应被引用表的 `tenant_id`，例如 `FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)`：外键检查绕过行级安全，只引用 `id` 就能指向别的租户的行；
  - `ENABLE ROW LEVEL SECURITY`，并建与 `stores` 相同写法的 `tenant_isolation` 策略（`USING` 和 `WITH CHECK`）；
  - `GRANT` 给 `salon_app`。

  `TestEveryTableIsTenantIsolated` 会检查这几条，组合外键由 `TestBusinessForeignKeysIncludeTenant` 检查。
- 行级安全只按租户隔离。同一租户内门店之间的隔离由业务接口的权限检查负责。

## 业务操作（ADR-0001、ADR-0004）

- 每个业务操作在所属模块里用 `ops.Operation` 声明一次（示例：`internal/stores`），在 `httpapi.New` 中注册。
  - HTTP：`POST /api/ops/<操作名>`，请求体就是参数类型。OpenAPI 文档在 `/openapi.json`，`x-permission` 和 `x-ai` 标明所需权限和 AI 调用方式。
  - AI：`Registry.Tools(caller)` 给出该员工能用的工具，参数 schema 取自同一份 OpenAPI 请求体；`Registry.CallTool` 用同一份 schema 校验参数。
- `Env.Now` 是本次调用的时刻，由 Registry 的时钟在每次调用时读取一次，整个操作只用这一个时刻。
- 权限检查只在 `ops` 里做：HTTP 和 AI 调用都经过同一条路径，先检查权限，再在调用者租户的事务里执行。操作处理函数里不再检查权限。需要多项权限的操作在 `Operation.AlsoRequires` 里声明附加权限，主权限和每一项附加权限都必须同时满足；HTTP、AI 工具列表、工具调用、起草校验、确认和草稿可见性都按这一份声明检查。
- 写操作只能声明为 `AIDraft`（AI 调用只产生草稿）或 `AINone`（不向 AI 开放），声明为 AI 可直接执行时启动即失败。AI 调用 `AIDraft` 时校验参数与本人权限，只创建待确认草稿，不执行业务写入。
- `Read` 操作不写任何表：`ops.invoke` 对 `Read` 操作使用可重复读的只读事务，写入会被 PostgreSQL 拒绝，多次查询看到同一快照。报价查询 `quote_item_price` 为保持价格一致会取 `FOR SHARE` 行锁，只读事务不允许加锁，因此用 `Locking` 标记单独放行；它仍不写表。分阶段的 `verify_coupon`（`Read` + `AINone`）按团购协议验券并追加审计，不向 AI 开放。`Read` + `AIDirect` 的操作都是纯读，可以出现在 AI 对话工具集中。
- 测试用 `optest` 对 HTTP 和 AI 两条路径跑同一组用例。写操作的业务规则在 HTTP 路径上测，另测 AI 路径对 `AIDraft` 创建草稿且业务数据不变，对 `AINone` 仍拒绝。

## 登录与权限（ADR-0009）

- 权限目录在 `internal/perm`。新的业务操作用这里的常量声明所需权限；只要登录即可的操作用 `ops.LoggedIn`。
- 登录凭证（JWT）包含员工、租户和凭证版本。每次请求由 `auth.Service.Authenticate` 从数据库读取员工当前的角色，算出有效权限：各角色取并集，同一项权限的限制取最宽；老板拥有目录中的全部权限。停用员工或收回权限，下一次请求即生效。
- 修改密码、重设账号密码、停用员工后，该员工已签发的登录凭证立即失效。
- 连续 15 次密码错误锁定登录 15 分钟，管理员重设账号密码可立即解锁。`change_my_password` 的原密码和 `open_void_window` 的本人登录密码检查共用这一个失败计数与锁定。
- 有数据范围或时间窗口的操作，处理函数从 `env.Grant` 读取限制并据此过滤。
- 新权限加进 `perm.DefaultRoles` 只影响以后开的店，已有租户的角色不会补上。本地跑端到端测试前先 `scripts/dev.sh reset`。
- 授权规则在 `internal/staff`：只有老板能授予或去掉老板角色；其他人授予、收回的角色和编辑的权限不能超过自己；不能修改权限比自己大的员工的档案、账号或在职状态；在职老板不能变成 0 人（用老板角色的行锁串行检查）。修改另一名员工的操作一律经过 `change`：先锁老板角色排队，再锁员工行，然后做可管理检查、老板底线和改前改后日志。新建员工、编辑或删除角色也先锁同一老板角色排队，确保授权检查期间角色权限不会改变。
- 员工记录不能删除：`salon_app` 没有 `staff` 表的 DELETE 权限。
- 登录时查找账号是唯一不经过 `db.InTenant` 的数据访问：此时还不知道租户，`auth.Service.Login` 以 `salon_app` 角色调用 `auth_find_login`（SECURITY DEFINER 函数，只返回登录所需的字段）。这条查询是手写的，因为 sqlc 推断不出函数返回的列。找到账号后，在 `db.InTenant` 事务里锁住员工并校验密码、更新失败计数。
- 角色列表分两个操作：`list_roles` 含权限配置，需要角色权限管理；`list_role_options` 只有名称，需要员工档案管理，用于给员工分配角色。

## 顾客档案

修改已有顾客的操作一律经过 `internal/customers` 的 `change`（按 ID 或按负责人选中），由它负责加锁、查看范围、已合并检查、改前改后日志和手机号打码。新建顾客用 `created`。

## 门店设置（ADR-0002）

- 设置值支持 time（时刻）、time_range（时段）、choice（Options 中的字符串）、integer（Min/Max 内的非负整数）和 text（单行文字，可为空，最多 Max 个字符）。`member_price_rule` 默认会员价优先，也可取较低价；`points_per_recharge_yuan` 为每积一分所需充值本金元数，默认 0（不积分），范围 0–100000。开卡、充值和升级补差价按当日规则积分，赠送金、转余额和调整余额不积分。`guest_count_split` 默认将客次记给第一个岗位的员工，也可选择单上员工均分，当天可生效。`performance_split` 默认按岗位比例分多人业绩，也可选择均分，项目没设岗位比例时均分；`performance_ratio_*` 为九种支付方式各自的 0–100 业绩比例，本金、现金、银联、微信、支付宝、团购和挂账默认 100%，赠送金和红包默认 0%；购买次数、赠送次数的耗卡业绩比例默认分别为 100%、0%，均可当天生效。退卡赠送金 `card_refund_bonus` 默认不退、作废，可选择扣回未被撤单退回的消费赠送金；`card_refund_fee_percent` 为 0–100 的退款手续费百分比，默认 0，当天可生效。撤单授权窗口 `void_window_minutes` 为 0（默认关闭）或 1–60 分钟，当天可生效。
- 考勤宽限分钟 `attendance_grace_minutes` 默认 0，范围 0–60，当天可生效，仅决定迟到、早退是否计异常。
断网补录：可补录天数 `backfill_max_days` 默认 3，范围 0–31，0 为只能补录当天；按今天执行的设置检查纸单日期。小票设置 `receipt_paper_width` 为 58mm 或 80mm（默认 80mm），`receipt_header`/`receipt_footer` 为最多 60 字的单行文字（抬头留空表示打印门店名称，页尾默认“谢谢惠顾”），`receipt_show_customer_name`、`receipt_show_staff`、`receipt_show_payments`、`receipt_show_card_balance` 为显示/不显示（默认都显示），均可当天生效。
- 轮牌设置：`rotation_order_rule` 新一天队列的初始顺序（先到先排、接昨天顺序、每晚按成员默认顺序重置，默认先到先排）；`rotation_designated_takes_turn` 点客结账后是否占轮排到队尾（默认占轮）；`rotation_skip_when_serving_designated` 队首正在服务点客时是否自动跳过并记录（默认跳过）；`rotation_skip_policy` 被跳过时排到队尾还是保留原位（默认排到队尾）；`rotation_half_rule` 半轮规则（不记、按价格、按项目、按预约，默认不记）；`rotation_half_price_below` 按价格记半轮的实收上限（元，默认 0 表示不按价格）。预约设置：`booking_min_lead_minutes` 顾客自助预约至少提前的分钟数（默认 0，前台代约不检查）；`booking_max_days_ahead` 从今天起最多可约的天数（默认 30）；`booking_default_minutes` 未选项目时的默认服务时长（默认 60）。营业时段复用 `business_hours`，预约必须完整落在开门到关门之间，关门须晚于开门，不支持跨天。规则按营业日生效，收银推进和每次轮牌操作都读取调用时刻执行的设置。
- 设置项在 `internal/settings` 的 `Catalogue` 里声明：名称、值的形式、系统默认做法和校验。新的业务规则（例如各项业务的做法选择）在这里加一项，再给 `Rules` 加一个读取它的方法。
- 每次修改都在 `setting_changes` 追加一行：改前、改后、生效日期、修改人、修改时间。`salon_app` 只有 SELECT 和 INSERT 权限。某个营业日执行的值，取生效日期不晚于这一天的最大者，同一生效日期取最后保存的一次（修改时间，再按 ID）；没有修改，或者改成了"恢复默认"，就用系统当前的默认做法。这条选法只在 `internal/inforce` 写一份，门店设置、提成方案版本和底薪共用，各类历史提供自己的保存顺序（提成方案按版本号；设置和底薪按保存时间、ID），SQL 只按门店读出历史；设置的"恢复默认"只在 settings 的 `resolve` 换成目录默认值。生效日期不能早于今天；划分营业日的设置（`fromTomorrow`，例如营业日结束时间）最早明天生效。同一项设置的修改用咨询锁排队，改前值才接得上前一次的改后值。
- 业务模块不直接查设置表：
  - `settings.BusinessDate` 算一个时刻属于哪个营业日，营业日 D 在第二天"营业日结束时间"截止，结束时间取 D 那天执行的设置；
  - `settings.Effective` 取某个营业日执行的设置；`settings.EffectiveDays` 一次加载设置历史，按输入顺序返回各营业日同规则选择的设置；
  - 单据下单时调用 `settings.TakeSnapshot`，把返回的 `Snapshot.ID` 存进单据，开单直接使用同时返回的 `BusinessDate` 和 `Rules`，之后计算用 `settings.LoadSnapshot` 读回的规则，这样以后改设置不会影响已下的单；撤单授权窗口是例外，按调用时的营业日用 `settings.Effective` 读取。内容相同的快照共用一行。

## 卡

开卡锁序为按 UUID 排序的业绩员工 FOR SHARE → 本店顾客 FOR NO KEY UPDATE（串行判定首购，并检查查看范围和未合并）→ 卡类型 FOR SHARE，再新建卡、复制次数和追加流水。其他顾客引用仍用 FOR SHARE。顾客合并为顾客 → 单 → 欠款 → 卡 → 红包；不会回锁员工，员工变更也不请求顾客锁，因此开卡不会引入反向锁序。顾客 NO KEY UPDATE 与卡插入外键的 KEY SHARE 兼容。

开卡、充值、手动补差价升级和卖红包都要登记顾客实际支付的收款方式：`payments` 数组每项是现金、银联、微信或支付宝之一，金额须大于零，合计必须等于这笔销售计入业绩的金额——开卡为本金加购买次数折算额（`unit_price × paid_count_opened`），充值和手动补差价为本次本金，卖红包为售价；赠送金不计入实际支付。零金额销售（例如购买次数全为赠送、送红包）传空数组，传入非空数组按合计不一致拒绝。收款追加到只追加的 `sale_payments`，以组合外键关联卡流水或红包，二者恰有一个，营业日与来源相同；没有收款方式的历史数据不回填。金额格式或方式错误是 422，合计不一致是 409。数据导入的历史卡不写 `sale_payments`，仍按 `import` 来源处理。

退卡只退整张卡，需要 card.adjust。本金加剩余购买次数乘以随卡单次金额，按规则扣回已用赠送金，扣回额至多为本金与购买次数折算的可退款金额，再减四舍五入到分的百分比手续费；赠送次数不退款，剩余赠送金一律作废。规则取退卡调用的营业日，不用单据快照；确认报价必须与锁内重算金额一致。登记现金、银联、微信或支付宝方式，不实际付款。余额写一条 refund 流水，每个卡项目写一条 refund 次数流水，即使原值为零也保留，全部归零；积分不变，卡状态改为 refunded。卡修改（充值、调整、升级及自动升级、转余额双方）、显式结账及试算、自动选卡和撤单退回均拒绝已退卡。退卡不改单据、欠款、红包，不扣业绩或提成，不处理原路退回（#43）。

## 单据

结账、撤单的支付效果统一经过 `internal/payments`。

`internal/collections` 管理统一收款对象。通道在启动时通过 `RegisterChannel` 注册，实现 `Channel` 的 Initiate、Query、Cancel、Refund、Reconcile；注册同时生成默认 on 的 `collection_channel_<code>` choice 设置（on/off）。现金和银联登记是同步登记通道，不调用支付机构；银联小票号通过结账支付的 `external_ref` 留存。结账按注册的 PaymentMethod 路由，每笔现金、银联支付在同一事务内生成一笔 succeeded 收款，以唯一的 order_payment_id 关联原支付；其余支付沿用原流程。停用只限制新发起，读取收款时按 `collections.business_date` 换算营业日（补录单写入纸单日；为空的历史行按 `created_at` 换算）取生效设置，不使用开单快照；已有收款仍可查询、撤销或退款，撤销/退款的封账护栏也用这个营业日。

收款状态由一张转换表校验：pending → processing/succeeded/failed/cancelled，processing → succeeded/failed/cancelled，succeeded → refunded/cancelled，failed/cancelled/refunded 为终态。撤销和退款先按本店锁收款（FOR NO KEY UPDATE），再检查状态、调用通道、更新并追加事件和 collection 审计；退款原因不能为空。撤单在原有支付冲回后按登记顺序锁收款并撤销，任一步失败则整单回滚；已进入终态的收款会拒绝再次撤销。list_collections、get_collection 为 Read/AIDirect，cancel_collection、refund_collection 为 Write/AIDraft，均需要 payment.collect；视图和日志不关联顾客、不含顾客资料。

Reconcile 按 `collections.business_date`（补录单写入纸单日；为空的历史行按 `created_at` 换算）的营业日、通道和当前状态汇总金额与笔数；它是查询当时的收款汇总，供日结保存快照，不是按退款发生日统计的资金流水。collections 仅可更新 status、updated_at、external_ref；collection_events 与 order_payments 都只追加。

开单必须选择男客或女客、指定或非指定；未提供顾客时自动创建未识别顾客，由开单权限覆盖。开单保存规则快照和营业日，后续加项目读取快照规则。

`open_order` 的 `paper_date` 表示补录单纸单上的营业日：营业日就是这一天，规则快照取这一天生效的设置；日期不能晚于今天、不能早于 `backfill_max_days` 个营业日，且不能是已封账的日子。单据行记 `backfill=true`，所有单据视图（含全量导出的“单据”表“补录单”列）都返回它。结账一张补录单时，收款、业绩、提成来源、卡流水、欠款、红包和客次都归到纸单营业日，结账记录本身也用这一天；一次结账只能有一个有效营业日，补录单不能和别的营业日一起结。补录单不推进当天的轮牌，也不能通过 `assign_turn` 接轮牌；`merge_orders` 拒绝补录单和普通单混合合并。纸单营业日所在的工资结算月已锁定时，开单和结账都拒绝补录（再次检查是因为开单后可能锁月）。撤单仍按撤单当天记录反向单和退回流水；改单/撤单的时间窗口、预览的卡有效期按单自己的营业日判断。`get_receipt`（Read/AIDirect，`payment.collect`）按结账单返回门店名称、当天执行的小票设置、脱敏后的顾客姓名（不返回手机号）、项目与员工、按方式汇总的支付（团购带门店标价）、小票总额，以及本次结账用到卡的结账后余额/剩余次数（取自结账写入的卡流水，重打不随后续充值变化）。顾客不在调用者的查看范围时只把该顾客的姓名置空、卡余额隐藏，其余小票内容仍返回。项目行保留添加时的项目名称，并复制项目岗位，允许必填岗位暂空；级别价来自第一个岗位的员工，没有员工或级别价时取标准价。多人贡献只在项目行记录一次价格，应收按项目行求和。

锁按顾客 → 单 → 项目 → 按 UUID 排序的员工取得：已有顾客、项目和员工只检查状态，用 FOR SHARE；加项目在读改前视图前用 FOR NO KEY UPDATE 锁单。单仅授予 customer_id、status、merged_into、checkout_id 列的 UPDATE，顾客合并仍可改挂 customer_id。项目行和岗位保留原记录：项目行仅授予 order_id、价格、来源、级别、改价原因及去掉标记列的 UPDATE，岗位仅授予 staff_id 的 UPDATE；三张表都不允许 DELETE。

改单需要 order.edit，时间窗口按单的 business_date 与当前营业日比较；本人范围包含 opened_by 或任意项目行岗位上的员工，去掉的行也算出现过。去掉项目只标记，不计入应收。换岗位员工提交完整分配，未列出的岗位清空；仅在 slot 1 员工改变且项目行未改价时按单的规则快照重新取级别价或标准价。改价需要 order.reprice 和原因，改后不再随员工重取价。改单锁顺序为单 → 项目（仅重取价）→ 按 UUID 排序的员工。

合并单需要 order.edit，对每张单检查时间窗口和本人范围，只允许同一顾客、同一营业日的挂单合并。全部项目行（包括去掉的行）移到保留单，其余单保留，标为 merged 并以 merged_into 引用保留单；保留单的其他属性不变。客次按未并入其他单的单计，已合并单不单独计客次。每个已有单的写操作统一经过 `internal/orders.change`：在本店按 UUID 以 FOR NO KEY UPDATE 锁住全部单，检查操作所需状态（改单和结账为 open，撤单为 paid），再按操作声明的 `env.Grant` 检查营业日窗口和本人范围，读取改前视图、执行修改、读取改后视图并逐单写日志。加项目使用 order.create，改价使用 order.reprice，这两项按权限目录均无窗口或本人范围限制；其他改单使用 order.edit。已合并单拒绝写入。合并按 UUID 字符串排序逐张锁单，不锁顾客、项目或员工；顾客合并也在改挂前按 UUID 锁住该顾客的单；其他改单先锁单，再锁项目及按 UUID 排序的员工。

结账需要 payment.collect，一张或几张挂单通过 change 按 UUID 锁单后一起登记支付；有效项目行的必填岗位须有员工，各单的支付合计须等于各自实收（应收减持卡折扣）。结账后单标为 paid 并引用同一个 checkout_id，拒绝再次结账、加项目、改单、改价和合并。结账的营业日取调用时刻的 settings.BusinessDate，各单保留原顾客和营业日。checkouts 和 order_payments 仅授予 SELECT、INSERT，支付只追加；挂账是支付方式 debt。卡余额支付为 card，每笔通过 order_payments.card_ledger_id 关联一条 consume 卡流水，按单的规则快照中的 card_deduction_order 拆扣本金、赠送金，不足则整次结账回滚；不累计积分。扣次为 count，每笔支付一个有效项目行、一行扣一次，金额等于该行实收；与卡余额支付共用本店、查看范围、有效期和用别人卡的检查，但允许次卡。每笔通过 card_count_ledger_id 关联一条 consume 次数流水，按单的规则快照 card_count_order（默认 paid_first，可选 gift_first）扣购买次数或赠送次数，不决定计价卡。卡余额支付的卡须本店发行、持卡顾客在调用者查看范围内、非次卡、未过期（到期当天仍可用），用别人卡须单的快照中 others_card_payment 允许。

红包是独立于卡的一次性对象，卖红包记录售价、一到十名本店在职业绩归属人和实际收款方式，送红包不记录售价、业绩归属人及收款方式。面额须大于零，有效期不早于发放营业日，未识别顾客也能领取。结账只用单上顾客自己的红包，一张单可用多个，每笔抵扣不超过面额，一次用掉、不找零，结账为每个用掉的红包记录改前改后日志；红包不参与计价或选卡。未用红包可撤回，过期也可撤回，卖的红包撤回不记退款。状态为 available、used、revoked、expired，过期按当前营业日判断，到期当天仍可用。撤回先以 FOR NO KEY UPDATE 锁红包，再无锁检查顾客查看范围；发放先共享锁顾客，再按 UUID 共享锁业绩归属人。顾客合并在单、欠款和卡之后按 UUID 锁红包并改挂，与结账的卡→红包一致，结账或撤回不回锁顾客。

欠款是顾客欠门店的一笔钱，分为每笔挂账支付生成的单上欠款和不关联单的历史欠款。未识别顾客（没有手机号）不能结账挂账，历史欠款仍可登记。还款针对一笔欠款，可部分还但不能超过未还，仅支持现金、银联、微信、支付宝；欠款与还款只追加，顾客合并仅改挂欠款的 customer_id。登记历史欠款需要 data.import_export，沿用 customers.LockReference 的顾客共享锁和查看范围。还款需要 payment.collect，先以 FOR NO KEY UPDATE 锁欠款，再无锁检查顾客查看范围并读取已还；营业日取 settings.BusinessDate。合并先锁顾客，再改挂单、欠款；结账持单锁时无锁读取手机号，避免与合并倒置锁序，补手机号只会使顾客变为已识别。合并更新单等待结账提交，因此随后更新欠款能包含新挂账。还款只锁欠款，不回锁顾客，与合并没有锁环。list_customer_debts 的 totals 和 debt_summary 只统计未作废欠款，debt_summary 仅返回本店汇总，单上欠款金额恒等于 method=debt 支付合计，未作废欠款的金额恒等于已还加未还。

撤单需要 order.void，只撤已结账的单，时间窗口按原单 business_date 与当前 settings.BusinessDate 比较，必须填写原因。在单锁之后、欠款和卡及红包锁之前写 order_voids 反向单，记原单、原因、撤单营业日及撤单人；原单、项目、支付和消费流水保留，原单标为 voided，checkout_id 不变。卡余额按原消费拆分退回本金及赠送金，次数按原消费退回购买及赠送次数，各追加 void 流水并关联原消费与反向单；不检查卡有效期或持卡顾客查看范围；卡已退时整次撤单拒绝。单上挂账随原单作废，不计未还和 debt_summary，已有任何还款则拒绝撤单；作废欠款不能还款。用掉的红包清空当前支付引用，恢复可用（过期则显示 expired）。历史支付保留，删除 order_payments_red_packet 历史唯一索引，当前支付由 red_packets.order_payment_id UNIQUE 和红包锁保护；若红包已有多次历史支付，00024 Down 明确拒绝恢复旧索引。一起结账的单逐张撤；现金、银联、微信、支付宝仅由反向单记录撤单。锁顺序为单 → 按 UUID 的欠款 → 按 UUID 的卡 → 按 UUID 的红包 → 按 UUID 的团购券（均 FOR NO KEY UPDATE），与顾客合并的顾客 → 单 → 欠款 → 卡 → 红包 → 团购券及结账的单 → 卡 → 红包 → 团购券一致；还款只锁欠款，不回锁单或顾客。开启撤单授权窗口后，`open_void_window` 要求本人重新输入登录密码（与登录共用锁定），窗口内只有开启人能撤，过期须重开。撤单按调用时的营业日读取当前设置，不读原单快照；关闭设置跳过窗口检查，缩短设置不影响已开窗口。

选卡规则 card_selection 从单的规则快照读取，默认 manual（手动选卡）要求每笔卡余额支付指定 card_id。by_discount（按折扣率）或 by_balance（按余额）允许每张单至多一笔未指定卡的卡余额支付。候选只列本店发行且属于单上顾客的卡，排除同单任意支付已指定的卡；与显式卡一起按 UUID 锁定后重新检查归属、营业日有效期、非次卡和本金加赠送金余额大于零。按折扣率逐张用其类型计价，按这张单实收从低到高；按余额按当前合计余额从少到多。同序先到期日（不限最后），再开卡时间，再 UUID。未指定支付按排序逐卡扣款，一张不够接着扣，每张使用的卡在原支付位置各追加一笔支付和消费流水；一起结账时按输入顺序逐单处理，每单在前面各单扣款后重新按当前余额过滤、排序并选计价卡，再计价、校验和写入；已扣空的卡不再参与后续单的计价。合计不足整次回滚。只给实际扣款或扣次的卡写日志。第一笔卡余额支付为未指定时，排序第一张是计价卡；其他卡只扣钱。扣次仍须指定卡，不参与选卡。preview_checkout 可传 auto_card 按同一快照规则只读选取计价卡，与 card_id 互斥；输出保留单据字段并返回 pricing_card_id（不传卡为空），不检查支付余额是否足够。

结账锁顺序为按 UUID 排序的单 → 卡（FOR NO KEY UPDATE）→ 红包（按 UUID，FOR NO KEY UPDATE）→ 团购券（按 UUID，FOR NO KEY UPDATE）→ 计价卡及候选卡类型（FOR SHARE）→ 有计价卡的单上的有效项目（FOR SHARE）。没有计价卡的单不加载计价数据、不锁项目。每张单第一笔卡余额支付的卡是计价卡；按该卡类型和单的规则快照读取会员价，与行上已记录价格比较，不重取标准价和级别价。改过价的行保持原价；不打折项目行既不取会员价，也不享受持卡折扣，按行上已记录价格扣卡；取会员价的行不打折，其余可打折行按卡类型 discount_percent 逐行四舍五入算持卡折扣。结账将新价格、来源、卡类型和折扣写回变动的有效行，去掉的行不改。preview_checkout 需要 payment.collect，HTTP 和 AI 均可直接试算同一套计价规则；它使用只读事务，不加业务行锁、不检查必填岗位或支付、不写任何数据。

挂单关联团购券后，结账由服务端按关联顺序生成 method=groupbuy 的支付，客户端不提交团购支付。每笔金额取关联时留存的门店标价快照；团购总额超过本单实收时，从最后一笔团购券往前减到实收，不找零、不退款，其余支付是现场补差，可以为零。团购支付不是卡余额支付，不选计价卡、不享受持卡折扣；它也不经过 collections，不生成收款记录，因此团购已付金额不计现场收款。履约项目与团购券对应的项目不比较，按单上实际项目计价和结算。preview_checkout 返回同样的团购支付。撤单保留原支付流水，释放团购券关联，券可再次关联或撤销；业绩按原支付方式冲回。

## 售后

`internal/aftersales` 管理售后单。售后单必须关联一张本店已结账的原单，类型为 `repair`（修补）、`redo`（重做）、`partial_refund`（部分退款）或 `full_refund`（全额退款），原因必填且不能为空白；一张原单可以有多张售后单。状态为 `pending`、`approved`、`rejected`，后两者是终态；每次创建、审批和拒绝追加一条只读的 `aftersale_events`。创建营业日存入 `business_date`，审批和拒绝只改状态、审批人/时间和可选拒绝说明，不删除行。

门店设置 `aftersale_window_days`（默认 30 天，最小 0）限制原单营业日到当前营业日的最长天数；`aftersale_approval_mode` 默认 `self`，为 `other` 时创建人不能审批或拒绝自己的售后单。创建、审批和拒绝都需要 `aftersales.manage`。创建只在事务里对本店原单加 `FOR SHARE` 共享锁，不锁售后单或卡，卡状态和金额在审批时重新校验。审批按 单 → 售后单 → 按 UUID 排序的卡 加锁；审批和拒绝都用 `LockOrder`（`FOR NO KEY UPDATE`）作为同一原单的互斥锁，把同一原单上的并发审批/拒绝串行化，`FOR SHARE` 不会和自身冲突，因此这里必须用更强的锁。

退款由店长在创建时按方式填写金额，方式可以是现金、银联、微信、支付宝或卡本金；退款方式不必与原单一致。可退金额只包括原单真实收到的现金、银联、微信、支付宝和卡本金支付，卡赠送金、扣次、红包、团购和挂账不计入，再减去已经审批通过的退款。部分退款至少一笔正金额；全额退款的合计必须等于当前剩余可退金额。卡本金退款只能退回原单支付过本金的卡，单张卡累计不超过它在原单支付的本金减去此前已审批的卡本金退款，且卡不能是已退状态。创建时校验一次，审批时在锁内重新校验，另一笔售后单在此期间审批通过会导致本次审批被拒绝。

未审批的售后单不生效：创建和拒绝都不改钱、卡或业绩。审批时在同一事务里把退款写入 `aftersale_refunds`，营业日取审批当天；卡本金退款同时追加一条 `card_ledger`（`kind='aftersale_refund'`）把本金加回卡。审批后被撤的原单不能再审批；已有审批通过的退款售后单的原单不能撤单（类似挂账已有还款），但只有待审批售后单不阻止撤单，撤单后该售后单不能再审批。封账后仍可为原单创建售后单，退款记在审批当天营业日。

`aftersale_deduct_performance` 和 `aftersale_deduct_commission` 是两个门店设置，默认都关闭；审批参数可以逐单覆盖，扣提成时必须同时扣业绩。只有退款类型会扣减。扣业绩时按累计退款合计占原单可退金额的比例，对原单的每条项目业绩累计冲回（营业日为审批日，按分向下取整，逐条减去此前已冲回的部分，任何一条都不会超过原业绩；整单退完时每条正好归零），耗卡业绩和手工费不冲回。扣提成时这些反向记录沿用撤单冲回路径参与提成计算；不扣提成时它们仍减少业绩，但从提成单位和累计基数中排除。修补和重做永远不写业绩记录。

`preview_day_close` 和 `close_day` 按审批营业日把已审批退款计入系统账：现金、银联、微信、支付宝减少当天现场应收，卡本金列入非现场并减少当天卡本金净消费。审批在写退款、卡流水或业绩前先调用 `closingguard.Guard` 检查审批营业日未封账，封账和审批因此互斥；审批会改变当天系统账，已保存的日结在封账时仍按原有的“日结后当天账目有变化”规则要求重新日结。

`create_aftersale` 为 Write/AIDraft，`approve_aftersale`、`reject_aftersale` 为 Write/AINone（钱离开门店，必须由人审批），`get_aftersale`、`list_aftersales` 为 Read/AIDirect。列表按营业日范围（`internal/daterange`，最多 92 天）、状态和原单筛选，退款记录、事件和员工名称都按批读取；视图只含原单 ID，不含顾客姓名或手机号。

## 日结与封账

`internal/paymethod` 是收款方式和现场收款划分的唯一清单：现金、银联、微信、支付宝是现场收款，卡本金、卡赠送金、扣次、红包、挂账、团购是非现场收款。以后新增方式只在这里登记；未声明为现场的方式自动列入非现场，不进入现金与到账对账。

`internal/closing` 的 `preview_day_close`（Read/AIDirect）按营业日 D 直接从账本现算系统应收，不读以后生效的设置快照。现场收款按方式为：D 结账的 `order_payments` + D 的欠款还款 + D 的 `sale_payments` − D 的退卡退款 − D 撤单的原单现金、银联、微信、支付宝支付。非现场单列为 D 结账的卡本金、卡赠送金、扣次、红包、挂账、团购，减去 D 撤单原单的同类金额。团购支付只出现在非现场行，不进入现场收款、到账对账或差异；撤单保留原团购支付并在撤单营业日冲回。历史开卡、充值、手动补差价和卖红包有销售业绩金额却没有 `sale_payments` 时列入“未记方式”及笔数；次卡、套餐的历史开卡按本金加购买次数折算额列入。汇总还返回客次净额、结账单数和撤单数；`collections.ReconcileAll` 的全部通道汇总同时保存为通道汇总。

`close_day`（Write/AIDraft，closing.daily）录入现金清点数和银联、微信、支付宝实际到账，要求 D 早于当前营业日且未封账。它保存系统快照、录入值、各现场方式的差异（现金为清点减系统，其余为实际减系统）和可选备注，版本号逐次递增；未封账的 D 重新日结追加新版本，当前版本为最新，旧版本仍可读。`get_day_close` 返回当前版本和全部版本，`list_day_closes` 按 `internal/daterange` 的营业日范围返回每天当前版本和封账状态。`day_closes` 只授予 SELECT、INSERT。

`seal_day`（Write/AIDraft，closing.seal）要求 D 早于当前营业日；从未封账时 D 可为任意过去营业日，之后只能封当前封账日的下一天。D 有日结或完全没有业务（没有开单、结账、收款/退款、卡/欠款/红包/销售收款/团购券核销等带营业日的写入）才能封；D 及之前仍有状态为 open 的挂单时拒绝，提示中列出单 ID。封 D 表示封住 D 及以前全部日期。`unseal_day`（Write/AIDraft，新权限 closing.unseal，默认角色都没有）只能撤销最后一个封账日，原因必填，撤销后回到前一天或从未封账。每次封账和撤销封账追加 `closing_events`（日期、种类、原因、操作人、时刻）并写操作日志，目标种类为 `closing`；日结日志目标为 `day_close`。

封账护栏 `closingguard.Guard` 按门店 `store_id + /closing` 咨询锁工作：业务写入取共享锁，封账和撤销封账取排他锁，因此“某天写入提交”和“封账提交”互斥。它拒绝营业日不晚于当前封账日的写入，提示 `该营业日已封账（YYYY-MM-DD）`。更新旧单的统一路径 `orders.change` 按每张单自己的 business_date 调用护栏，覆盖改单、改价、加项目、合并、结账和撤单；撤销收款按收款创建时刻换算的营业日调用护栏；登记历史欠款按欠款日期调用护栏。团购券关联、取消关联复用 `orders.ChangeOpenOrder`，合并时移动团购券复用 `orders.change`，都按单自己的 business_date 调用护栏；结账生成的 groupbuy 支付属于挂单结账，而封账拒绝所有不晚于 D 的挂单，因此不会写已封账营业日。团购券撤销只释放仍为挂单的券、已结账单拒绝撤销，而挂单不可能存在于已封账营业日，所以不需要额外护栏。团购商品新建/修改不记营业日、不改账务；`record_groupbuy_settlement` 只追加平台月结算额，不进入任何营业日的日结数字，二者都不调用护栏。卡调整、还款、退卡、开卡/充值/升级、红包发放都记录调用时的营业日，不可能是已封账的过去日期，因此不调用护栏。数据导入的卡流水也记当前营业日，不需要护栏；撤销导入前按被删除的卡余额、次数、积分流水和新建顾客/卡的营业日逐个调用护栏。顾客合并只改挂顾客归属，封账行金额和日期不变，因此不调用护栏；P0-18 后合并会同时改挂预约的 customer_id，预约本身不带金额、不影响任何日结行，所以仍不调用护栏。预约的创建、改约、取消、确认、完成、爽约，私约占位的创建和取消，轮牌变化，以及 findOrCreate 新建顾客，都只写预约/占位/轮牌/顾客这些非账务数据，不改现金或非现场收款、业绩、卡、红包、欠款，因此不调用护栏；完成预约只把已有单关联到预约，不改单的支付、营业日或业绩。预约和占位不进入 `DayHasBusiness`：它们没有金额，某天只有预约时仍可按“完全没有业务”封账，不需要日结。撤回红包记录撤回时刻并改变红包状态，但不改任何已封账营业日的账目，因此不调用护栏。团购券核销按 redeemed_at/created_at/revoked_at 换算营业日算作业务；团购平台月结算按结算月录入，不属于任何营业日，不算某天的业务。首次封账可以覆盖更早的上线前历史日子；只有被封的 D 本身需要日结（或完全没有业务）。考勤、轮牌、设置和提成方案不属于账务日期。

| 操作 | 权限 | AI |
| --- | --- | --- |
| preview_day_close | closing.daily | AIDirect |
| close_day | closing.daily | AIDraft |
| get_day_close | closing.daily | AIDirect |
| list_day_closes | closing.daily | AIDirect |
| seal_day | closing.seal | AIDraft |
| unseal_day | closing.unseal | AIDraft |

## 报表

`internal/reports` 提供营业日报/月报、员工业绩分析、项目分析、卡项分析和新客来源报表。五个操作都是 Read / AIDirect，SQL 在 `internal/db/queries/reports.sql` 一次聚合，查询数不随天数、员工数、项目数或卡类型数增长。金额使用 `internal/money`，所有列表按固定顺序返回，并另给合计行/对象。

| 操作 | 权限 | AI | 范围与顺序 |
| --- | --- | --- | --- |
| get_business_report | report.view | AIDirect | 日报 from..to 最多 92 个营业日，逐日返回（无业务的日子为全零行）；月报 from/to 为 YYYY-MM，最多 12 个月，逐月返回；按期间升序 |
| get_staff_report | performance.view | AIDirect | 营业日范围最多 92 天；本人范围只返回本人，传入别人的 staff_id 与查不到一样为空；全店范围返回有业绩、客次或项目行的员工，最后是 staff 为空的未分配行；按员工姓名、ID 升序 |
| get_item_report | report.view | AIDirect | 营业日范围最多 92 天；按项目/商品返回；实收降序，再按名称、ID 升序 |
| get_card_report | report.view | AIDirect | 营业日范围最多 92 天；返回范围内有活动或期末余额/剩余次数不为零的卡类型，按名称、ID 升序 |
| get_customer_source_report | report.view | AIDirect | 营业日范围最多 92 天；每个本店来源一行（启用的来源始终返回，停用的只在有数据时返回），未填来源始终最后；来源按名称、ID 升序；另返回无法归属到顾客的单数 |

日期归属统一按营业日：补录单按纸单营业日计入开单、结账、业绩、客次、卡流水和收款；撤单和售后退款不重写原营业日，而是在撤单/审批营业日冲回或退款。营业日报的现场/非现场收款方式、客数、结账单数和撤单数逐日与 `preview_day_close` 的 `SystemFigures` 完全一致；实现复用同一组账本口径（结账支付、卡本金/赠送金拆分、欠款还款、销售收款、退卡退款、售后退款、撤单和撤单卡拆分），只是改成范围聚合。

营业日报字段：

- 现场收款按现金、银联、微信、支付宝分方式；非现场按卡本金、卡赠送金、扣次、红包、挂账、团购分方式，顺序和名称与 `internal/paymethod` 一致。
- 服务实收是范围内结账单的支付合计，撤单在撤单营业日扣回；销售收款是开卡、充值、补差价、卖红包登记的收款方式合计。
- 耗卡本金/赠送金和扣次金额分别是卡余额支付拆出的本金、赠送金和扣次支付金额；团购抵扣、新增挂账、还款、售后退款分列。新增挂账直接按 `debts.business_date` 汇总（包括历史欠款登记），撤单时在撤单营业日扣回；日结的非现场挂账只来自结账支付，因此两者关系是：`new_debt` = 日结非现场挂账 + 范围内登记的历史欠款；日结本身不变。
- 客数按四位小数返回净额，并分指定、非指定、男客、女客；结账单数按结账营业日，撤单数按撤单营业日。
- 合计对象等于所有明细行逐项相加。

员工业绩分析从 `performance_entries` 取 item、consume、labor、sale 四种业绩净额（原记录减冲回），item 的现金业绩含现金、银联、微信、支付宝、挂账，卡金业绩含本金、赠送金；客数从 `guest_visits` 取；服务项目行数是该员工在范围内已结账有效项目行去重后的净行数（撤单在撤单日扣回）。提成合计直接复用 `commission.Commissions` 对同一范围、同一员工范围的一次批量计算，按员工汇总，不按员工逐行再查。全店范围的 item 业绩合计与 `list_performance` 同范围 item 合计一致。

项目分析按项目/商品汇总有效结账行，撤单在撤单日扣回：次数、原价合计（结账行价格，折扣前）、实收合计（价格减持卡折扣）、其中卡余额支付、扣次支付、团购支付、指定行数，以及 `performance_entries` 的 item 业绩。实收合计与营业日报的服务实收一致（商品也是单据项目，扣次行也计入实收）。

卡项分析按卡类型汇总，并按流水发生时的卡类型归属：用 `card_upgrades.type_before/type_after` 和时间重建每张卡的历史类型，升级后的流水归新类型，升级前的流水归原类型；范围内升级不会把之前的开卡/充值改记到新类型。开卡数/本金/赠送金只统计 `card_ledger` 的 open；数据导入写 `import` 流水，因此导入卡只计入期末余额（和剩余次数），不算开卡。充值、补差价、消费本金/赠送金、扣次次数/金额、退卡数/退款额按范围统计；撤单的卡退回和售后退回的卡本金在发生日抵消消费。期末余额是截至 `to` 每张卡最后一条卡流水的本金加赠送金，按 `to` 当天生效的卡类型归属；期末剩余次数同理取每张卡每个项目最后一条次数流水。

新客来源报表：

- 新客是首次结账营业日落在范围内、且有手机号的未合并顾客；首次结账取该保留顾客及其直接、间接合并进来的全部顾客中最早的结账营业日。顾客合并后按保留顾客统计，已被合并掉的顾客不会单独出现。
- 只要保留顾客本身或任何直接/间接合并进来的顾客带 `import_batch_records` 的 customer 记录，这家顾客就不算新客（历史早于本系统）。
- 每个本店来源返回新客数、这些新客在范围内的实收合计（结账、撤单和已审批售后退款分别按各自营业日净额）、以及回头客数。来源只连接本店的 `customer_sources`；其他门店的来源不会显示，其顾客按未填来源归集。启用的来源始终返回，停用的来源只在有数据时返回；未填来源始终返回，全部为 0 时也保留。
- 回头客是该新客在首次结账营业日之后、查询结束营业日 `to` 之前又有结账的顾客。
- 未识别顾客（没有手机号）的单不计入任何来源，数量另行返回；合计等于所有来源行相加。

## 数据导入导出

`internal/dataio` 从美管加中间 Excel 导入历史顾客、储值卡余额、次卡套餐次数和积分，并导出本店全量数据。历史水单、支付、提成和员工不导入。写操作 `commit_import`、`undo_import` 为 AINone；`list_import_batches`、`get_import_batch` 为 AIDirect。四个操作都需要 `data.import`。上传预览 `POST /api/import/preview`（multipart 字段 `file`）和全量导出 `GET /api/export/all.xlsx` 是普通 Echo 接口，分别需要 `data.import`、`data.export`。两个权限默认只有老板拥有，店长等角色可由老板在角色设置里授予。单文件上限 8 MiB，解压后总大小最多 64 MiB、单表 XML 最多 16 MiB；每张业务表最多 20000 行数据，`汇总` 最多 1000 行。读取使用 excelize 的流式 `Rows`，边读边数，超限或 XML 损坏返回 422 文件错误。

### 中间 Excel 契约

工作簿必须有四张表，首行表头逐字如下；多出的列或空表头会被拒绝。行列从 1 开始，表头在第 1 行。

- `顾客`：`来源顾客ID` | `姓名` | `手机号(可空)` | `性别(男/女/空)`
- `储值卡`：`来源卡号` | `来源顾客ID` | `卡种` | `本金` | `赠送金` | `到期日(YYYY-MM-DD,可空)` | `积分(整数,可空)`
- `次卡套餐`：`来源卡号` | `来源顾客ID` | `卡种` | `项目` | `剩余购买次数` | `剩余赠送次数` | `到期日(可空)`
- `汇总`：`顾客数` | `储值卡数` | `本金合计` | `赠送金合计` | `次卡套餐卡数`

示例：

```text
顾客:      C001 | 张三 | 13800131234 | 男
           C002 | 李四 |             | 女
储值卡:    K001 | C001 | 储值卡A | 1000.50 | 200 | 2029-12-31 | 50
次卡套餐:  K002 | C002 | 剪发十次 | 剪发 | 8 | 2 | 2030-01-31
汇总:      2 | 1 | 1000.50 | 200 | 1
```

- 金额按十进制文本或数字读取，最多两位小数；负数、科学计数法、精确到分以外的浮点或超过单笔上限都按该行错误。
- 手机号按数据库现有格式 `^1[0-9]{10}$`；去空白后不合法是该行错误。手机号相同的顾客链接到已有未合并顾客，文件内重复手机号链接到第一次出现的目标；没有手机号的顾客每次都新建（ADR-0010）。
- `来源顾客ID`、`来源卡号` 在同一文件内标识来源记录。卡行引用的顾客必须来自顾客表且该行没有错误。
- 卡种必须已经在本店配置：储值卡用 `stored_value`；次卡套餐用 `count` 或 `package`，且项目必须属于该卡种的 `card_type_items`。缺失或种类不符时该行错误 `卡种未配置: <卡种>`，不自动建卡种。
- 同一 `来源卡号` 的次卡套餐可以有多行（一个套餐多个项目），但顾客、卡种、到期日必须一致，项目不能重复；同一来源卡号不能同时用于储值卡和次卡套餐。
- 已由另一批 `committed` 批次导入过的 `来源卡号` 再次出现时，该行错误 `已导入`，防止重复累加余额。
- 行错误只跳过该行，不中止整批；错误按工作表、行号和原因返回。`汇总` 只用于核对，不阻止提交。

### 预览、提交、撤销与核对

1. 上传后只写 `import_batches`、`import_batch_rows`，批次状态 `previewed`；顾客、卡、流水都不写。
2. `commit_import` 锁住批次并加门店级咨询锁，按 UUID 锁可能关联的已有顾客，再按手机号排序加咨询锁；如果其间手机号关联到了未预锁的顾客，该行报错并需重新预览。在一个事务里写新顾客、卡、卡项目、`import` 卡流水、`import` 次数流水和 `import` 积分流水；任何一步失败整批回滚。状态改为 `committed`。
3. `undo_import` 只接受 `committed` 批次，删除本批 `import_batch_records` 中标记为 `created` 的顾客、卡、卡项目、流水和积分。链接到的已有顾客以及任何不是本批创建的数据都不动。有外部引用（单据、欠款、红包、安全标签、其他卡、其他流水、调整、升级、退款、转余额、合并、修改等）时返回 409，并列出不能撤销的原因，不删除任何行。
4. 撤销后来源手机号和来源卡号释放，同一文件可重新导入并提交。重新导入已撤销批次的卡不会触发 `已导入`，因为只检查 `committed` 批次。
5. `get_import_batch` 同时返回 `汇总` 声明值、文件有效行合计和实际写入值，以及 `declared_minus_file`、`declared_minus_written` 差额；差额非零只提示，不阻止提交。

撤销由迁移中的 `undo_import_batch(batch_id, store_id)` SECURITY DEFINER 函数执行，函数先校验 `app.tenant_id` 和门店，再检查引用，最后只删除本批记录的行。`salon_app` 仍没有顾客和卡相关表的 DELETE 权限。

导入不会产生开卡、充值或调整流水，`import` 不是销售来源，因此不计入业绩、提成、营业收入或开卡报表。每个批次、提交、撤销和导出都写 `audit_log`，目标种类为 `import_batch` / `data_export`。

### 全量导出

导出查询使用可重复读事务，所有工作表取同一数据库快照。

`GET /api/export/all.xlsx` 用 excelize StreamWriter 输出九张表：`顾客`、`储值卡`、`次卡套餐`、`卡流水`、`单据`、`支付`、`员工`、`项目`、`卡种`。只包含当前门店的数据，金额一律为两位小数文本。`顾客`、`储值卡`、`次卡套餐`、`卡流水`、`单据`、`支付` 按主键（或导出顺序的 `(created_at, id)`）keyset 分页，每页写入 StreamWriter 后再取下一页；`单据` 的项目行按这一页的单批量读取，不再建立全量 map。列和行的顺序与不翻页时相同。`顾客`、`储值卡`、`次卡套餐` 使用与导入契约完全相同的列，来源 ID 为顾客或卡的 UUID，因此可以重新导入另一套系统；没有 `customer.full_phone` 的调用者看到打码手机号；`单据` 一张单的项目行各占一行，没有项目行的单也保留一行。导出文件可被 excelize 读回。

## 操作日志

- `audit_log` 只追加，`salon_app` 只有 SELECT 和 INSERT 权限。日志与业务操作在同一事务里写入，任何一步失败都会一起回滚。同一员工或角色的修改用行锁排队，在读取改前快照前加锁，日志的改前才接得上前一次的改后。
- 当前记录角色的新建、修改、删除，以及员工的新建、档案修改、登录设置、停用、恢复和角色分配；保留改前改后，删除角色时还保留持有员工的 ID、姓名。`list_audit_log` 需要操作日志权限，可按目标 ID 查询。每条日志记录目标种类及其关联顾客；`list_audit_log` 按调用者的顾客查看范围过滤关联顾客的日志。班次（`shift`）、员工班次（`staff_shift`，目标为员工 ID）、补卡与作废补卡（`punch`，目标为打卡 ID）、考勤核准与撤销（`attendance_approval`，目标为核准 ID）均不关联顾客，只需要操作日志权限即可查看；底薪（`base_salary`，目标为员工 ID）、加减项（`payroll_adjustment`，目标为加减项 ID）、工资锁月（`payroll_month`，目标为提成结算 ID，均不关联顾客）、提成方案（`commission_scheme`，不关联顾客）、团购商品（`groupbuy_deal`）、团购月结算（`groupbuy_settlement`）、预约（`appointment`，关联顾客）和私约占位（`appointment_block`，不关联顾客）、卡类型、开卡、充值、开单、加项目、去掉项目、换岗位员工、改价、合并单、结账已记录日志。员工本人打卡（`clock_in`）不写操作日志，打卡行本身即记录。新增关联顾客的种类（例如单）时，添加接收顾客 ID 的构造函数，并扩展迁移中的 CHECK 列表。
- 新操作在成功路径末尾用同一个 `env` 调用 `audit.Record`，传入不含密码或密码哈希的返回值视图；新建前、删除后传 nil。手机号字段的 JSON 键名必须是 `phone`，`audit.Record` 遇到别的键名会拒绝写入（读取时只按这个键名打码）。

## 共享修改与引用检查

卡的充值、调整、升级和转余额统一经过 `internal/cards.change`，它按 UUID 锁卡、检查顾客查看范围、读取改前改后视图并写日志。一次修改只取一次营业日；业绩员工在锁卡前检查。余额由卡视图读取，余额流水只在 `writeBalance` 计算前值、变动和后值，积分流水只在 `writePoints` 计算。自动升级沿用已持有的卡锁并保留独立日志。

跨模块引用检查由数据所属模块提供：`customers.LockReference` 检查本店、查看范围和未合并；`staff.LockAssignable` 按 UUID 锁住本店在职员工，交接来源用允许离职的 `staff.LockReferences`；`catalog.LockReference` 检查本店启用项目。只检查引用均用 FOR SHARE。报价 `catalog.Quote` 自己锁住读取的项目和员工；项目行岗位分配及是否重取价由 `catalog.QuoteLine` 负责。

测试用 `optest.App.SetNow` 固定当前时刻、`Advance` 前移时刻，默认使用真实时间。测试调用和并发请求等待共用 `optest.App.Call`、`optest.WaitResult`、`optest.AssertWaiting(t, pool, done, waiters)`（每 10ms 查询当前数据库等待行锁或咨询锁的会话数，至少达到 waiters 才通过；同一测试已启动的其他等锁请求也计入总数；请求先返回或五秒内未达到预期数量即失败）；事务 SQL、提交回滚和固定行顺序的表快照共用 `dbtest` 的辅助函数。各业务包只准备自己的业务数据。

## 业绩

营业日范围、92 天上限、本人范围求交和 limit 默认值统一在 `internal/daterange`，由 list_performance、list_guest_visits 和 list_commissions 共用。

performance_entries.member 在非 sale 时必填，既有非销售行迁移为 false；sale 必须 NULL，由 CHECK 保护。RecordCheckout 把同一张单的判断写入 item、consume、labor 的所有记录；ReversePerformanceOrder 原样复制。list_performance 暴露这个可空布尔值，与提成解释一致。

客数存于独立的 `guest_visits` 追加表。每张未合并的结账单计一个客次，一起结账仍逐单计。归属按开单规则快照的 `guest_count_split`：`first_slot` 取行序中首个有员工的有效项目行内最低已填岗位员工；`equal` 按行序、岗位序首次出现的不同员工均分。去掉的行不参与。份额向下取整到四位小数，余数全部给第一名员工，合计恰为 1；无人时记 staff 为空的门店记录、份额 1。复制单上的指定和男客、女客属性。撤单以撤单营业日追加正份额的冲回记录，统计时计负，原记录不改；每条仅冲回一次。`list_guest_visits` 按营业日、员工和本人或全店范围返回分页明细及完整范围的客数、指定、非指定、男客、女客净合计，员工条件交集为空时不查询，否则只做一条明细和一条合计查询。表采用租户组合外键、RLS，仅授予 SELECT、INSERT。

`internal/performance` 拥有业绩表、纯分配函数和 `list_performance`。performance 对外给出业绩单位（含基数构成），提成只消费业绩单位；支付方式表只有一份。结账在支付写入后、标记已结账前，在已有单锁的事务内按各单规则快照追加记录；撤单在已有单锁的事务内逐条追加冲回，不改原记录，不另记操作日志。结账记录用结账营业日，冲回用撤单营业日。

仅有效且非扣次支付的项目行参与。performance 在结账事务内读取已登记支付，将卡余额从消费流水拆出本金和赠送金，并按次数流水区分购买次数与赠送次数。支付方式金额按各行实收比例分摊，以分为单位先向下取整，再按项目行顺序、现金、银联、微信、支付宝、团购、挂账、红包、本金、赠送金的固定顺序，在行和支付方式都缺分的格子补分，保留两边合计。均分或没设岗位比例时按已填员工岗位均分；按岗位比例时仅对已填岗位的权重重新归一。岗位分配先向下取整，余分给最前的已填岗位。无人或已填岗位权重合计为零时记门店未分配业绩；同一员工占两岗位分别留存。实收份额乘该方式的业绩比例，用 money 的四舍五入到分计算业绩。团购按 `performance_ratio_groupbuy` 计项目业绩，但计入 performance 基数，不计入 cash_performance 或 card_performance。

耗卡业绩（consume）按扣次支付所关联次数流水的购买次数或赠送次数，取 card_items 随卡留存的单次金额，乘单的快照比例分给岗位员工；单次金额为零时不记。手工费（labor）对所有有效项目行按次计，包括扣次行，取加项目时留存的固定金额，或结账项目行价格（折扣前，含会员价）的百分比，四舍五入到分后按相同岗位规则分配；无人或已填岗位权重和为零时不记门店手工费。零手工费不记。

销售业绩（sale）与其他业绩共用 performance_entries，在来源事务中追加。开卡（card_open）计本金加随卡 card_items 的 unit_price × paid_count_opened 之和，次卡本金为零，套餐计本金加购买次数折算额；赠送金、赠送次数不计。充值（card_recharge）和手动升级补差价（card_upgrade）只计本金；充值触发的自动升级不另记。卖红包（red_packet_sold）计售价，送红包不计。多人按输入的业绩归属人顺序均分，先向下取整到分，余数全给第一人；零值和零份额不写。销售没有支付方式，不应用门店支付比例，ratio 固定 100、amount 等于 paid_amount。营业日取卡流水或红包的营业日。来源分别以租户组合外键关联 card_ledger_id 或 red_packet_id；销售的单、项目行、结账、设置快照和岗位均为空，purchase 保留 first 或 repeat。

首购是顾客在本店第一张卡的开卡，任何状态（包括已退）和顾客合并移入的既有卡都算此前已有卡；其余开卡、充值、补差价和卖红包都是复购。开卡先以 FOR NO KEY UPDATE 锁顾客，再检查是否有既有卡，同顾客的并发开卡由此串行，不能同时判为首购。调整、转余额、消费、退款和撤单退回不记销售；撤回卖出的红包不改销售。此步不冲回销售，退款扣业绩属于 #48；ReverseOrder 仍只按 order_id 冲回结账业绩。

读操作按不超过相隔 92 天的营业日范围查询，`performance.view` 本人范围仅返回本人记录，全店范围包括未分配记录，可再筛员工或 kind（item、consume、labor、sale）。明细倒序游标分页，合计始终按 item、consume、labor、sale 顺序返回四种金额，使用整个筛选范围，原记录减去冲回，可为负数。表有租户 RLS 和组合外键，仅授予 salon_app SELECT、INSERT。

## 提成

`internal/commission` 保存提成方案与版本，并从 performance 提供的业绩单位计算提成，支持整笔跳档和超额累进。整体方案含 rules、overrides、excluded_staff。每种来源 item、consume、labor、sale 至多一条规则；项目业绩可选 line_price（原价）、paid（折后价）、performance、cash_performance、card_performance，其他来源只允许 performance。规则按岗位、指定或非指定、会员或散客的开关决定格子键，每个键恰在相应开关开启时出现，格子键不重复；销售业绩不允许这些区分。格子可为空表，未填格子不计提成；每格恰有比例（0–100）、非负固定金额或阶梯之一，使用 money 的精确字符串类型。

覆盖比例至少指定员工或项目，允许员工 × 项目、项目、员工三种覆盖，计算按此顺序优先于规则默认。每个来源、员工、项目组合不能重复，来源必须在同一方案内有规则；销售业绩不能有项目覆盖。覆盖值也是比例或固定金额之一。不计提成员工不能重复。所有岗位、项目、员工引用由所属包一类一次批量查询校验本店归属，允许离职或停用，不加行锁：这些表无 DELETE 权限，状态也不影响合法性。会员含义为结账时顾客名下有有效卡，结账时留存会员判断，不在查询时重新判断。

`commission_scheme_versions` 只追加，salon_app 仅有 SELECT、INSERT，租户 RLS 与门店、修改人的组合外键保护租户隔离。保存先获取每店独立的事务咨询锁（`/commission_scheme`，与设置项锁区分），再取营业日、最新版本、校验日期与方案、分配 latest + 1 版本号并写入。同日可多次保存，生效日期不能早于 settings.BusinessDate 算出的今天，也不能早于最新版本。旧版本不改不删，规则按 item、consume、labor、sale 归一排序，格子和覆盖保持输入顺序。

`save_commission_scheme`（Write、AIDraft）、`list_commission_scheme_versions` 和 `get_commission_scheme`（Read、AIDirect）均要求 settings.manage。列表版本号倒序、limit / before_version 翻页，含修改人名称而无方案内容；查询参数必须且只能指定版本号或营业日，保存与查询返回同样的完整名称视图。按营业日选择 effective_from ≤ 日期的最大生效日期，同日取版本号最高的一版，即同日最后保存的方案（版本号在同一把锁里递增分配）。导出的 VersionInForce 还接受可选 saved-no-later-than 时刻，先排除该时刻之后保存的版本，供计算按业绩写入时刻选版。名称按员工、岗位、项目批量读取，查询数不随引用数量增加。

保存与 audit.Record 在同一事务，目标 commission_scheme，action 为 save_commission_scheme，before 为上一个最新版本视图（首次 nil），after 为新版本视图。日志不关联顾客，不包含手机号；日志查询对这一种类额外要求 settings.manage，避免操作日志权限泄露方案里的员工覆盖。业务规则通过 ops.Rule，沿用仓库统一的 HTTP 409；请求格式、金额和百分比格式校验为 422。

performance 以一次结账内的（kind、order_item_id、slot）为一笔，将同笔全部支付方式的业绩记录合并；同一员工占两个岗位仍算两笔。sale 每条业绩单独一笔。staff_id 为空的门店未分配记录不产生提成。

`internal/commission` 导出 Commissions(ctx, q, storeID, from, to, staffID) 供后续月锁、员工端和解释功能调用，未锁定时提成现算；零金额也返回解释。

版本只批量读取一次，由 selectVersion 这个纯函数统一选择：saved_at 不晚于单位内最早 created_at 的版本中，按 inforce 取 effective_from 不晚于业绩营业日的最大生效日期，同日取版本号最高的一版，即最后保存的方案（版本号在锁内递增分配）。VersionInForce 和 get_commission_scheme 的日期查询也共用它。无版本或员工在 excluded_staff 中不计提成。先查员工 × 项目覆盖，再查项目、员工覆盖，最后按岗位、指定、会员键找规则格子；覆盖即使没有匹配格子仍然生效，但必须有该来源规则。项目覆盖仅作用于 item、consume、labor。没有命中项不产生提成。

item 基数按单位汇总：paid 为 paid_amount 之和；performance 为 amount 之和；cash_performance 只含现金、银联、微信、支付宝、挂账；card_performance 只含本金、赠送金。红包仅在 performance 中。line_price 为结账时 order_items.price（折扣前，含级别价、会员价与改价）乘单位实收 / 本次结账该项目行全部 item 记录的实收，分母包括门店未分配记录。consume、labor、sale 基数均为单位 amount 之和。分到员工及乘比例都通过 money 四舍五入到分，固定金额每笔一次，基数为零仍计固定金额。

冲回按（void_id、kind、order_item_id、slot）组成新笔，由 performance 批量取得原记录并关联原业绩单位，使用原单位的版本、命中项和基数，解释带原单位 ID；营业日为冲回日，因此归入冲回所在月。非阶梯格子与覆盖比例沿用原金额取负；阶梯使用冲回月及冲回位置的累计重新计算，不能直接取负原提成。

结账在支付写入后、业绩写入前，以 cards.HasValidCard 为每张单读取一次会员判断：本店、该单顾客、active，复用卡支付 checkExpiry 的到期日规则，到期当天有效；不限卡种和余额。这是一个当时已提交状态的快照，不依赖该结果扣余额、次数或改变卡，卡随后开通、调整或退卡不应重算历史会员事实，所以无需新增行锁。该读取位于已有的单 → 卡 → 红包 → 团购券 → 卡类型 → 项目锁之后，不回锁顾客或其他卡，不逆转结账锁顺序。

list_commissions 是 Read / AIDirect，要求 performance.view。本人与全店范围沿用 list_performance；本人范围叠加他人的 staff_id 时为空，不能扩大范围。按营业日范围（相隔最多 92 天）、staff_id、source 筛选。以营业日降序、单位最早 created_at 降序、稳定单位 ID 降序排列；单位 ID 为该笔最小业绩记录 ID，limit / before_id 翻页。total 与按 item、consume、labor、sale 的 totals 使用完整筛选范围，原笔减冲回，不受分页影响。entries、explanation、totals 都在同一权限范围内，不含任何顾客字段。

explain_commission 是 Read / AIDirect，要求 performance.view，按提成 ID 复用 list_commissions 的同一份计算结果，返回该笔提成的来源（单、项目行、岗位、卡流水或红包）、计提基数、命中规则/覆盖、方案版本、阶梯解释和业绩记录。本人范围只能查自己的提成，查他人的提成与他人不存在的 ID 一样返回 404，不泄露是否存在。

加载将请求范围扩至 date_from 所在月首日到 date_to 所在月末日，员工过滤仍在业绩查询中应用；计算后只返回原请求营业日范围，解释和 totals 也仅针对这些输出。全部按批查询（查询数不随行数增长），包括：完整月份业绩、冲回原记录、项目行事实、全部方案版本、提成结算状态与已结算提成、员工名称、岗位名称、项目名称。performance 加载项目行事实，只读取 order_items.price / item_id、orders.designated 与业绩实收之和，不回读单据重算价格、支付或业绩。查询数不随单位数增长，由 TestCommissionReadQueryCount 通过 HTTP 与 AI 验证。

阶梯格子 tiers = {mode: whole | progressive, levels: [{from: money.Amount, percent: money.Percent}]}，档内只允许比例，至少两档，首档从 0.00 开始，from 严格递增，达到起点（含等号）适用。覆盖比例仅允许比例或固定金额。

累计分组是（员工、来源、business_date 的日历月）；加载的完整月份内所有业绩单位均参与，无论匹配另一格、非阶梯格、覆盖比例、无匹配、无版本或不计提成员工。按当前阶梯规则的 base 从业绩单位的基数构成选择贡献，item 的各基数种类按需分别累计。冲回贡献为所冲原单位同种类基数的负数。顺序为营业日、单位最早 created_at、单位 ID 升序，恰为列表倒序。

whole（整笔跳档）使用截至查询时完整月份的总累计 T，包括本笔之后和请求日期范围之外的单位。按 from ≤ T 的最高档对本笔完整基数乘比例，四舍五入到分；T < 0 用首档。后来结账可提升月初单位提成，冲回可降低 T；方案仍使用单位写入时的版本，修改规则不改变历史命中项。

progressive（超额累进）从本笔前累计 c 开始，把 [c, c + base] 在档位起点处分段，各段乘比例并分别四舍五入到分后相加。零以下用首档；冲回覆盖 [c + base, c]，分段基数与提成均为负。同月原笔与冲回使当月净累计恢复；跨月冲回只减少冲回月累计。月中结果截至查询时，月底由 #39 锁定。

命中阶梯格子时 explanation 才含 tier：mode、month（YYYY-MM）、base_kind、whole 的有符号 cumulative 或 progressive 的 before/after、累计后达到的 level（index 从 0 起，含 from/percent），以及各段 parts（from、percent、有符号 base/amount）。whole 恰有一段；Commission.Amount 等于所有分段金额之和。原有 base、match（含完整 tiers）、version 保持原含义。

## 工资

`list_payroll_commissions` 使用 `payroll.manage` 查看员工当月的提成明细，与工资表采用相同数据，不受业绩本人范围限制。全新开发环境的门店从上个月开店，便于验证锁月。

`internal/payrollmonth` 统一结算月：Month 以 UTC 月首表示营业日所属的自然月，输入输出仍使用 YYYY-MM；Check 解析并以 settings.BusinessDate(stores.created_at) 的月份为开店月下限，允许未来月份；Guard 保护写入，Lock 创建锁定记录 Settlement，GetSettlement 读取记录，List 列出各月状态与可锁定标记。`internal/payroll` 保存底薪、加减项和工资表，`internal/commission` 保存提成结算明细；两者依赖 payrollmonth，payrollmonth 不依赖它们。

`staff_base_salaries` 按员工追加计薪方式（base 底薪 + 提成，guarantee 保底）、非负金额、生效月、保存人和保存时刻。旧记录不改不删。BaseSalariesInForce 一次查询读出本店全部底薪记录，按员工用 inforce 选出 effective_month ≤ 结算月的最大生效月，同月取 saved_at 最新的一条；时刻也相同时取 ID 最大的。未设置返回 null，客户端按 base、0 显示。所有员工包括离职员工按编号返回。

`payroll_adjustments` 记录结算月、员工、自由名称、非零有符号金额及备注；名称去首尾空白后不能空，最多 20 字，备注最多 100 字。允许离职员工；删除是真正 DELETE，日志保留删除前完整视图。锁定后的月份不能添加、删除加减项。

底薪、加减项与锁月共用 payrollmonth 内的 LockPayroll 门店级咨询锁（store_id + /payroll）。设置底薪锁序：Parse → Guard（咨询锁 → 开店月 → 未锁定）→ staff.LockReferences 的员工 FOR SHARE → 读取执行底薪 → 追加；添加加减项：Parse → Guard → 名称与金额检查 → 员工 FOR SHARE → 插入；删除：读加减项 → 咨询锁 → 开店月 → 未锁定 → 删除（0 行即不存在）。三条写路径统一由 Guard 检查开店月下限与未锁定。锁定后不能再设置底薪，也不能添加或删除加减项。

set_base_salary、add_payroll_adjustment、delete_payroll_adjustment 是 Write / AIDraft；list_base_salaries、list_payroll_adjustments 是 Read / AIDirect，全部要求 payroll.manage。底薪列表覆盖全店在职与离职员工；加减项按月、可选员工筛选，以 created_at、id 倒序游标分页。姓名批量查询，查询数不随行数增长。

写入与 audit.Record 同事务；底薪目标 base_salary 的 target_id 是员工 ID，before 是该月原执行视图（首次 nil），after 是新设置；加减项目标 payroll_adjustment 的 ID 是该条 ID，新增 before nil，删除 after nil。视图与日志无顾客或手机号，日志读取额外要求 payroll.manage。

两表有租户 RLS、tenant_id/id 唯一约束，以及门店、员工、保存人或创建人的租户组合外键。salon_app 对 staff_base_salaries 仅有 SELECT、INSERT，对 payroll_adjustments 仅有 SELECT、INSERT、DELETE，没有 UPDATE。月份在数据库约束为月首；计薪方式、金额、名称、备注也有 CHECK。

`commission_settlements` 每店每月唯一，月份 CHECK 为月首，记录 settled_by / settled_at。`settled_commissions` 保存每笔提成的金额、冲回标识、解释和排序时刻 unit_created_at；月份与营业日所在月有 CHECK，同一店的提成 ID 唯一，结算通过 (tenant_id, store_id, settlement_id, month) → commission_settlements (tenant_id, store_id, id, month) 的组合外键同时绑定租户、门店与月份；payroll_lines 通过 (tenant_id, store_id, settlement_id) → commission_settlements (tenant_id, store_id, id) 绑定结算所属门店。父表只保留这两组供引用的组合唯一键，去掉不含门店的旧组合唯一键。两表与 `payroll_lines` 均有租户 RLS、门店与员工的租户组合外键，仅授予 salon_app SELECT、INSERT，不允许 UPDATE 或 DELETE。

Commissions 通过一条 commission_settlements LEFT JOIN settled_commissions 语句同时读取范围内的结算月份与已保存明细，以同一 READ COMMITTED 语句快照构造锁定月份集合、替换对应月的现算结果，避免锁定事务在两个查询之间提交时重复拼入现算与存储明细。空结算月仍返回月份标记，员工过滤置于 JOIN 条件中，即使该员工没有保存明细也不丢失锁定标记。未锁定月仍现算。恢复 explanation 和 unit_created_at，统一按原日期范围过滤、按营业日 / createdAt / ID 倒序排序，再批量解析员工、岗位与项目的当前名称。金额、方案版本、计算解释固定；名字可更新。list_commissions 的来源筛选、合计、分页及本人范围继续在同一输出路径执行，锁定不会扩大数据范围。

工资表按月批量加载员工、BaseSalariesInForce、整月 Commissions 与当月全部加减项。员工创建时刻的营业月不晚于当月，且仍在职或离职时刻的营业月不早于当月时，当月计整月底薪；入职、离职均不按天折算。日期转换用 settings.BusinessDates（BusinessDate 是它的单个时刻形式，规则只有一份，默认值取设置目录），设置历史只批量取一次。没有设置按 base、0；非当月在职的 applied_amount 为 0。员工有执行底薪（在职且金额 > 0）、任一提成记录（含零提成）或任一加减项时列入，按编号、ID 排序，离职员工亦可出现。

工资表行保存 mode、applied_amount、base_pay、item_commission、consume_commission、labor_commission、sale_commission、commission_total、guarantee_top_up、adjustments_total、total。四类提成按有符号 Commission.Amount 求和；加减项按有符号金额求和。payroll_lines 的 CHECK 保证 commission_total 等于四类之和，total = base_pay + commission_total + guarantee_top_up + adjustments_total；base 模式 base_pay = applied_amount 且补差 0；guarantee 模式 base_pay = 0 且补差 = greatest(0, applied_amount - commission_total)。mode 只允许 base / guarantee，applied_amount 非负。结算与员工唯一。全店 totals 对每个金额列按工资表行逐列相加。

list_payroll_months、get_payroll 是 Read / AIDirect，lock_payroll_month 是 Write / AIDraft，三个都要求 payroll.manage。状态从当前营业月到开店月倒序，含锁定人、时刻与 lockable；get_payroll 对开放月每次重算（允许未来月），锁定月从 payroll_lines 读取、名称在读取时解析。

锁定顺序：payrollmonth.Lock（咨询锁 → Check → GetSettlement 检查未锁定 → settings.BusinessDate 检查月份已结束 → 检查前月已锁定，开店月免此项 → 插入锁定记录并返回 Settlement）→ commission.Settle 保存每笔提成结果 → 现算工资表（此时 Commissions 读刚存下的提成，工资表各列与提成明细出自同一份数据）→ 保存工资表行 → 读回锁定视图 → audit.Record。所有步骤同一事务，失败全部回滚并提示，可重试，无后台补算。锁定不可撤销；错误以后月加减项更正。加减项不复制，原表在锁后禁止增加或删除。底薪与加减项写操作在检查开店月和锁定状态前取同一咨询锁，锁定期间请求会等待，提交后再检查锁定。

锁月日志 action 为 lock_payroll_month，目标 payroll_month，无关联顾客，before 为 nil，after 为返回的完整工资表；list_audit_log 额外要求 payroll.manage 才能读取。三个接口、提成查询与工资日志不输出顾客或手机号；工资接口的整店工资视图由 payroll.manage 授权，提成查询仍需 performance.view 并保留 self 范围。

## 考勤

`internal/attendance` 保存考勤（attendance）的班次（shift）与员工班次（staff shift）。`shifts` 保存名称、分钟精度的上班和下班时间、创建人和时刻；上班早于下班，不跨午夜。班次不能改名称、时间或删除，要改时间就新建班次并改指定。salon_app 只有 SELECT、INSERT 和列级 UPDATE (active)，没有整表 UPDATE 或 DELETE。

`staff_shifts` 只追加员工、可空班次、生效日期、保存人与时刻，salon_app 仅有 SELECT、INSERT。两表都有租户 RLS、tenant_id/id 唯一键和租户组合外键。StaffShiftTimelines 一次查询加载本店全部指定及关联班次时间，批量读取姓名，返回按员工分组的 inforce.Timeline。某营业日取 effective_from 不晚于当天的最大者，同日取 saved_at 最新、再取 ID 最大者；upcoming 每个未来生效日只取最后保存的一条，按日期升序。NULL 表示从该日起无班次，也参与生效选择。停用的班次不能再指定，已有指定仍执行直到改指定；离职员工仍列出。

set_staff_shift 的锁序为门店咨询锁（store_id + /staff_shift）→ settings.BusinessDate(env.Now) 检查生效日不早于今天 → staff.LockAssignable 的员工 FOR SHARE → 本店班次 FOR SHARE 并检查在用 → 读取生效日的改前视图 → 追加记录 → 日志。咨询锁使日志的 before 接上前一次 after；deactivate_shift 以 FOR NO KEY UPDATE 锁班次后检查并更新 active，与指定的共享锁互斥。

create_shift、deactivate_shift、set_staff_shift 是 Write / AIDraft，list_shifts、list_staff_shifts 是 Read / AIDirect，五个操作均要求 attendance.manage。list_shifts 在用优先，再按上班时间、名称排序；list_staff_shifts 包含在职及离职员工，按编号、ID 排序。姓名与历史均按批查询。三条写路径与 audit.Record 同事务，目标分别为 shift（班次 ID）和 staff_shift（员工 ID），记录完整 before/after；首次 before 为 nil。视图与日志不含顾客或手机号，考勤日志只需操作日志权限，不附加工资或设置权限。

`punches` 保存打卡时刻、按 settings.BusinessDate(at) 归属的营业日、来源 self/manual、原因、创建人与可空的作废信息。self 必须无原因且创建人为本人；manual 原因非空且不超过 100 字。作废三列必须同空或同非空，只能作废 manual，作废原因也须非空且不超过 100 字。`attendance_approvals` 保存员工营业日的核准原因、核准人与时刻，撤销人和时刻必须同空或同非空；部分唯一索引保证每人每天最多一个有效核准。两表有租户 RLS、tenant_id/id 唯一键、门店和员工的租户组合外键。salon_app 只有 SELECT、INSERT，punches 仅可 UPDATE (voided_by, voided_at, void_reason)，attendance_approvals 仅可 UPDATE (revoked_by, revoked_at)，均无 DELETE。

所有打卡与核准写入先拿每员工咨询锁 LockStaffAttendance（store_id + /attendance/ + staff_id）。clock_in 锁后查最新有效打卡，距 env.Now 不足 60 秒返回原记录，正好 60 秒可再打。本人打卡不写操作日志，打卡行本身即记录。add_punch 锁后拒绝未来时刻，再用 staff.LockReferences 对本店员工 FOR SHARE（允许离职），插入时按时刻计算营业日。void_punch/revoke_attendance_approval 先按本店 ID 无锁读取获知员工，再拿咨询锁，再 FOR NO KEY UPDATE 锁记录、检查状态、更新。approve_attendance 先咨询锁，再员工 FOR SHARE，再检查没有有效核准且当天当前有异常，最后插入。原因均 trim，补卡、作废、核准必填原因；撤销核准无需原因。员工自己打的卡不能作废，作废补卡仍保留并返回。经理写入与日志同事务，audit targets 为 punch、attendance_approval，不关联顾客，不附加工资或设置日志过滤。

统计每次读取现算，不存结果，不检查开店月下限或工资锁月。按 business_date 所在 YYYY-MM 用 payrollmonth.Month 取月首末。月份打卡（包括已作废）、核准历史、StaffShiftTimelines、settings.EffectiveDays、员工姓名均批量查询，查询数不随员工、天数或打卡数增长。出勤为至少一张有效卡；营业日首张有效卡为上班，至少两张时末张为下班，中间仅记录。比较在 settings.Location 下截去秒；迟到为上班分钟晚于班次上班加宽限，分钟数从班次上班算；早退为下班分钟早于班次下班减宽限，分钟数到班次下班算。宽限仅决定是否计异常。过去营业日恰一张有效卡算缺卡，并仍检查迟到、没有下班卡因此不算早退；今天尚未结束不缺卡，无卡不算缺卡，不判断缺勤。无执行班次也能打卡、计出勤，不算迟到早退。明细按营业日升序，含有任何打卡（包括仅作废卡）或有效核准的日子；当天打卡按时刻、ID 排序。有效核准覆盖当天全部异常，明细仍显示原始异常，统计排除迟到、早退、缺卡次数及分钟，已核准次数按有效核准的营业日计。补卡或作废后重新计算，已有核准保留。统计员工按编号、ID 排序，包含在职员工及月内有任何打卡或核准历史的离职员工。

| 操作 | 权限 | AI |
| --- | --- | --- |
| clock_in | attendance.clock | AINone |
| add_punch | attendance.manage | AIDraft |
| void_punch | attendance.manage | AIDraft |
| approve_attendance | attendance.manage | AIDraft |
| revoke_attendance_approval | attendance.manage | AIDraft |
| list_my_attendance | attendance.clock | AIDirect |
| get_staff_attendance | attendance.manage | AIDirect |
| list_attendance_summary | attendance.manage | AIDirect |

list_my_attendance 固定查询调用者本人；get_staff_attendance 可查本店在职及离职员工；list_attendance_summary 返回本店月度统计。写操作返回的员工引用及日志均只含姓名与标识，不含手机号。已离职账号由登录鉴权拒绝本人打卡，历史查询与店长补卡仍可用。P0 不校验位置或 Wi-Fi。

## 轮牌

`internal/rotation` 管理电子轮牌。`rotation_queues` 是门店的队列（例如剪发牌、助理牌），名称在门店内唯一，可停用；`rotation_members` 记录队列成员和默认顺序，一名员工可以在多个队列。`rotation_turns` 是某个队列某个营业日的有序名单，状态为 off（未到岗）、waiting（等待）、serving（服务中）、resting（临时休息）或 covered（盖牌）；每行记位置、半轮标记、休息结束时刻和正在服务的单。唯一键保证同一员工每天在一个队列只有一行、位置在队列内唯一。`rotation_events` 只追加、不修改不删除，记录每次加入、离岗、接客、释放、跳过、自动跳过、盖牌、取消盖牌、休息、休息结束、拖动和结账推进，跳过与拖动必须带原因。`rotation_half_items` 保存按项目记半轮的项目。

所有表都有租户 RLS、`tenant_id`/`store_id`、引用门店和其他业务表的组合外键。`salon_app` 对队列和轮牌行有 SELECT、INSERT、UPDATE，对成员和半轮项目另有 DELETE，对事件只有 SELECT、INSERT，因此事件永远只追加。

顺序规则在第一次访问某个营业日时生效：先到先排的当天名单为空，员工到岗时依次排到队尾；接昨天顺序按该队列在当天之前最近一个有已持久化 `rotation_turns` 的营业日结束后各员工的位置预排（只查看过、未写入的营业日不改变顺序），新成员按默认顺序排在队尾；每晚重置按成员的默认顺序预排。预排的员工都是 off，到岗后变成 waiting 并保留位置。到岗、离岗都留事件；离岗保留当天位置但不再接客，服务中的员工不能离岗。

`get_rotation` 是纯读：首次读取某个营业日时按顺序规则在内存中算出初始名单，休息到点的员工显示为 waiting，但不写 `rotation_turns` 或 `rotation_events`。初始名单和 rest_end 改由该营业日的第一次写操作（任一轮牌操作或结账推进）在同一个事务里、应用自己的修改前持久化；rest_end 事件的 `created_at` 记实际休息结束时刻 `rest_until`，不是后来写入的时刻。`rotation_events.created_at` 是事件发生时刻，读取按 `(business_date, created_at, id)` 排序；仓库内没有 since 游标，外部轮询请记录 `id` 或自行按事件时刻处理。`assign_turn` 不带员工时取位置最靠前的 waiting；serving、resting、covered、off 都不接客。队首正在服务点客且 `rotation_skip_when_serving_designated` 为 yes 时，记录一条原因为“点客服务中”的 auto_skip，并按 `rotation_skip_policy` 排到队尾或保留原位。带员工时是点客，员工必须是 waiting，标记 serving 和是否指定。`release_turn` 撤销接客、不消耗轮牌。`skip_staff` 必须从固定原因中选择，选“其他”还要写说明；`cover_turn` 盖牌后保留位置但不能接客，`uncover_turn` 恢复等待；`rest_staff` 休息 1–240 分钟，到点后读取显示 waiting，下一次写操作记 rest_end；`drag_turn` 把等待员工拖到指定位置并必须写原因。

停用的队列仍可查看和释放正在服务的员工；`update_rotation_queue` 可以改名并重新启用，`join_rotation`、`assign_turn` 等新的接客操作仍拒绝停用队列。自动选人和点名都只接受当前队列成员且仍在职的员工，成员被移除或员工停用后，当天仍是 waiting 的行不会再被分配。

每次改位置都在同一事务里先锁队列行，再按位置和员工 ID 稳定顺序 `FOR UPDATE` 锁住当天全部轮牌行；写回时先把位置整体加一个偏移量，再写成 1..n 的连续位置，避免唯一约束瞬时冲突。并发 `assign_turn` 因此不会把同一名员工分给两位顾客。

结账成功后，`orders` 在同一事务里调用 `rotation.AdvanceAfterCheckout`。它找出这张单上的服务员工，按队列 ID 顺序锁住队列和各行所属营业日的轮牌行；只推进 `serving_order_id` 指向这张单的行，跨营业日也按行自己的营业日推进，规则仍取结账时刻的营业日。点客与否取轮牌行记录的 `serving_designated`，不看单据上的指定标记。点客且 `rotation_designated_takes_turn=no` 时只恢复 waiting、不改变位置，记 advance_designated_no_turn；否则按 `rotation_half_rule` 判断整轮或半轮：整轮直接排到队尾并清除半轮标记；半轮第一次只标记 half_pending、不动位置，第二次才排到队尾并清除标记。按预约记半轮要求前台在结账前完成预约并把这张单关联到预约；只关联待确认或已确认的预约、或结账后才完成，都按整轮。事件写入 order_id 和 designated。单上没有正在服务的员工时不做任何事；撤单不反向推进轮牌（不在本任务范围）。

`get_rotation_stats` 从 advance_whole、advance_half 和 advance_designated_no_turn 事件按员工统计点客与轮牌接客数，两者分开合计；同一员工在两个队列服务同一张单只记一次，按员工和单去重。`list_rotation_events` 按日期范围和可选队列返回全部只追加记录；日期范围和统计都不得晚于今天的营业日。`get_rotation` 的轮牌行按批读取，查询数不随队列人数增长。

| 操作 | 权限 | AI |
| --- | --- | --- |
| create_rotation_queue | queue.operate | AIDraft |
| update_rotation_queue | queue.operate | AIDraft |
| set_queue_members | queue.operate | AIDraft |
| list_rotation_queues | queue.view | AIDirect |
| get_rotation | queue.view | AIDirect |
| join_rotation | queue.operate | AIDraft |
| leave_rotation | queue.operate | AIDraft |
| assign_turn | queue.operate | AIDraft |
| release_turn | queue.operate | AIDraft |
| skip_staff | queue.operate | AIDraft |
| cover_turn | queue.operate | AIDraft |
| uncover_turn | queue.operate | AIDraft |
| rest_staff | queue.operate | AIDraft |
| drag_turn | queue.operate | AIDraft |
| set_half_turn_items | queue.operate | AIDraft |
| list_rotation_events | queue.view | AIDirect |
| get_rotation_stats | queue.view | AIDirect |

老板拥有全部权限；店长和前台可以操作轮牌，发型师和助理只能查看。队列、成员和半轮项目配置写入 `audit_log`，目标为 `rotation_queue`（半轮项目清单以门店 ID 为目标）；轮牌事件本身仍是只追加的操作记录，不写 `audit_log`。

## 预约与占位

`internal/booking` 管理预约和私约占位。`appointments` 同时保存两种记录：`kind=appointment` 是员工代顾客登记的预约，必须有顾客；`kind=block` 是员工自己登记的私约占位，不关联顾客。状态为 pending、confirmed、completed、cancelled、no_show；代约和私约占位新建即 confirmed，待确认留给 P0-19 顾客自助预约。状态只能按 pending → confirmed/cancelled、confirmed → completed/cancelled/no_show 变化，私约占位只能 confirmed → cancelled。

营业时段复用 `business_hours`，另有三项设置：`booking_min_lead_minutes` 提前量（前台代约不检查，导出 `booking.CheckLeadTime` 供 P0-19）、`booking_max_days_ahead` 可约天数和 `booking_default_minutes` 未选项目时的默认时长。预约必须提供顾客、顾客手机号或姓名；三者都没有时拒绝。预约开始必须在今天及以后、不早于当前时刻 5 分钟以上，结束必须晚于开始且与开始同一天、完整落在营业时段内。结束时间默认取所选项目 `duration_minutes` 之和，未选项目或项目都未设时长时取门店默认时长，前台传 `ends_at` 可手改。项目新增 `duration_minutes`（0–600，0 表示未设置）和 `bookable`（默认可以）；预约只接受启用且可约的项目。

私约占位只检查同一员工的时段冲突和 `ends_at > starts_at`，最长 24 小时，结束时间必须在当前时刻之后；不检查营业时段、可约天数或是否跨天，也不要求顾客。同一员工的 pending/confirmed 记录时段不能重叠，`appointments` 用 `tstzrange` 上的 PostgreSQL EXCLUDE 约束（btree_gist）兜底并发；写入前先查冲突，以便提示里带出冲突记录 ID。相切边界允许，已取消、已完成、爽约的记录不占时段。`appointment_items` 保存项目及顺序；`appointment_events` 只追加，每次创建、改约、确认、完成、取消、爽约都记录改前改后的时间、发型师、原因、说明和操作人。取消预约必须选择顾客取消、门店取消、改约或其他，选其他要写说明；取消私约占位不需要原因。

| 操作 | 权限 | AI |
| --- | --- | --- |
| create_appointment | booking.manage | AIDraft |
| reschedule_appointment | booking.manage | AIDraft |
| cancel_appointment | booking.manage | AIDraft |
| confirm_appointment | booking.manage | AIDraft |
| complete_appointment | booking.manage | AIDraft |
| mark_no_show | booking.manage | AIDraft |
| create_block | booking.manage | AIDraft |
| cancel_block | booking.manage | AIDraft |
| list_appointments | booking.manage | AIDirect |
| get_appointment | booking.manage | AIDirect |

发型师的 booking.manage 只有本人范围：只能查看、创建、改约、取消和登记自己的预约或占位，其他员工的记录按不存在处理；店长和前台为全店范围。完成预约可关联同一顾客的一张单；轮牌“按预约”用这条关联把已完成的预约记半轮，因此前台必须在结账前完成预约并关联这张单，否则这张单按整轮推进。`list_appointments` 按营业日范围（最多 92 天）和可选员工筛选，项目、顾客、员工名称都按批读取；`get_appointment` 同时返回项目和全部事件。预约与占位写入 `audit_log`，目标分别为 `appointment` 和 `appointment_block`。

轮牌自动分配（不指定员工）时，`rotation.assign_turn` 一次询问 `booking.OccupiedStaff`：等待中的员工若在当前时刻被预约或私约占位覆盖，就跳过并记录一条原因为“预约/私约占位中”的 auto_skip，不改变其位置；点客不受影响。`rotation_half_rule=by_appointment` 时，结账的单关联了已完成的预约记半轮，否则记整轮。

## 团购适配层

`internal/groupbuy.Provider` 统一验券、核销、撤销、查状态，四个动作均带幂等键。
`httpapi.Config.Groupbuy` 注入按 platform 注册的 `Providers`；未配置的平台拒绝执行。
本任务没有服务商协议文档，因此不提供无限聚核 HTTP 实现，也不在生产注册测试适配器。

`verify_coupon`、`redeem_coupon`、`revoke_coupon` 均为 AINone；`get_redemption` 为 AIDirect。

开发环境可用 `GROUPBUY_FAKE_PLATFORMS=meituan,douyin` 启动内存假平台（`fakeprovider`），用于手工联调和端到端测试：没有脚本响应时，没见过的券码验为 unused，核销/撤销/查询按内存状态机返回，核销时刻取启动时刻；平台协议不返回购买价或平台商品 ID，这些仍由团购商品和核销记录留存。启动时打印一行警告。`scripts/dev.sh up|reset` 会为本地后端设置它，生产环境必须保持未设置。
四者均要求 `groupbuy.redeem`；`verify_coupon`、`redeem_coupon`、`revoke_coupon` 记录无关联顾客的 coupon_redemption 审计，`get_redemption` 只读本店记录，不写审计。
核销本身不写订单；核销成功后可以另行关联挂单，也可以暂不关联。

团购使用 `ops.Operation.Phased`：处理函数在事务外执行，每段数据库操作由 `env.Transact`
开启独立的租户事务，调用者和 `env.Now` 保持不变；普通操作仍在原来的单事务中执行。
核销先以门店、平台和券码的咨询锁排队；同券已有 pending、unknown 或 redeemed 记录时，新幂等键被拒绝，必须使用原键查询或重试，failed 或 revoked 后允许新键。然后提交 pending 再调平台，更新前用 FOR NO KEY UPDATE 重读。
已有 pending/unknown 必须先 Query，明确 unused 才用同键 Redeem；不确定结果保存 unknown 并返回 409。
平台失败或结果未知时，也在保存状态的事务中记录改前、改后及不含平台内部错误的提示；日志写入失败时状态更新回滚，原 pending 可供重试恢复。
撤销先 Query 以恢复上次未知结果，调用平台的键为 revoke: 加原键；抖音超过平台核销时刻一小时拒绝。
所有进程并发和崩溃恢复最终依赖 Provider 对同键动作的幂等保证；事务不跨外部调用持锁。

### 团购开单与业绩

`groupbuy_deals` 是本店维护的团购商品映射：每个（门店、平台、名称）一条，记录平台购买价和对应的门店项目；平台不能修改，改名、改价、换项目或停用都不改已关联团购券的快照。`create_groupbuy_deal`、`update_groupbuy_deal` 为 Write / AIDraft，复用 `settings.manage`，写 groupbuy_deal 操作日志；`list_groupbuy_deals` 为 Read / AIDirect，登录即可读。订单和试算视图中的券平台取核销记录的 platform，不取团购商品当前值；商品名称仍取当前值。

`open_groupbuy_order(redemption_id, deal_id, customer_id?, gender, designated, items)` 用 `AlsoRequires` 同时要求 `order.create` 和 `groupbuy.redeem`，为已核销且未关联的团购券在同一个事务里开单并关联：先锁住团购商品并在锁内校验启用状态和项目覆盖，再按 `open_order` 的规则开单（复用同一处理函数和审计），然后用 `link_coupon_order` 的规则锁券、校验并写入购买价和门店标价；任一步失败都整次回滚，不留下单据或关联。开单项目必须包含团购商品映射的项目，否则返回「开单项目必须包含团购商品对应的项目」；核销状态和已关联提示与关联操作相同。开单成功后重新读取单据视图，返回的 `order.groupbuy` 已包含刚关联的券。草稿预览用同一组只读校验检查商品、项目覆盖和核销状态，提示文字与操作相同。该操作是 Write / AIDraft、也是普通 HTTP 操作，AI 只起草，不调用验券或核销。

`link_coupon_order(redemption_id, deal_id, order_id)` 由 `groupbuy.redeem` 和 `order.create` 共同授权，通过 `orders.ChangeOpenOrder` 复用挂单锁和挂单检查；`order.create` 没有范围和窗口限制。它要求券是本店已核销且未关联的券，单是本店挂单，团购商品启用且平台一致；关联时把平台购买价和团购商品对应项目的标准价复制到 `coupon_redemptions.purchase_price`、`store_price`，记 `linked_at` 和 `order_id`。`unlink_coupon_order` 只在单仍是挂单时允许，只清空 `order_id`、`linked_at` 和当前支付指针，保留购买价和门店标价快照；之后重新关联会按新选择的团购商品覆盖快照。锁顺序为单 → 团购券 → 团购商品。一张券最多关联一张单，一张单可以关联多张券；`revoke_coupon` 在券仍关联挂单或已结账单时拒绝，必须先取消关联或撤单。关联和取消关联都写 coupon_redemption 和单的操作日志；两个操作均为 Write / AIDraft。

结账时服务端按 `linked_at, id` 顺序为单上每张券生成一笔 `order_payments.method='groupbuy'`，金额取 `store_price`；`order_payments.coupon_redemption_id` 记录来源，团购总额超过本单实收时从最后一笔往前减到实收。客户端只提交现场补差，合计须等于实收减团购总额，可为零。团购支付不选计价卡、不参与持卡折扣，也不进入 collections，因此不生成收款记录、不计现场收款；挂账只由 method=debt 产生，不包含团购。`preview_checkout` 按同一规则返回团购支付。撤单保留 groupbuy 支付历史，释放券的 `order_id`、`linked_at` 和当前支付指针但保留购买价/标价快照并写 coupon_redemption 日志，券可重新关联或撤销；业绩按原支付方式冲回。平台在撤销请求期间先撤销、券随后被关联时，以平台结果为准：挂单上的券同时释放，已结账订单上的券保留支付历史并返回 409 提示撤单或售后。

`groupbuy_settlements` 只追加，按门店、平台、YYYY-MM 保存平台结算额，最新一条为当前值。`record_groupbuy_settlement` 为 Write / AIDraft，复用 `closing.daily`，写 groupbuy_settlement 操作日志；`get_groupbuy_month` 为 Read / AIDirect，复用 `report.view`。月度视图用 `settings.BusinessDates(redeemed_at)` 归属营业日，窗口从当月首日本地零点前两天到下月首日本地零点后两天，按结算月筛选并排除已撤销券；购买价合计包含当月所有有快照的非撤销券。分类返回已关联已结账、已关联挂单、未关联但有购买价快照，以及未关联且无快照的笔数，再给出当前结算额、差额（结算额减购买价合计）和录入历史。

## AI 草稿

`internal/drafts` 保存草稿和只追加的事件。工具的可选 `summary`、`utterance` 为草稿元数据，从业务参数中分离后仍按原 schema 校验；也可用 `create_draft` 显式提供 operation、params、utterance、summary。原始参数不可改，修改记录保留前后参数。

`list_drafts`、`get_draft` 仅返回本人发起或当前有目标操作权限的草稿；修改、拒绝采用相同范围。读路径是纯读：`pending` 且 `now >= expires_at` 的草稿在返回时计算为 `expired`（`decided_at` 仍为空），不写状态或事件；期限用 `settings.BusinessDayDeadline` 按创建营业日的结束设置计算。确认、修改、拒绝这张草稿时才持久化过期和 `expired` 事件并返回原来的“草稿已过期，请重新起草”；`create_draft` 也会批量过期本店到期的 pending 草稿。确认锁草稿后重读状态、检查期限、通过 ops 统一路径重新检查确认人的权限及参数，再以确认人身份执行原 Handle；成功后与状态、事件一并提交。业务失败保留 pending。

`draft_dual_confirm` 为 off/on，默认 off，可当天生效；on 时 adjust_card、void_order、refund_card、reprice_order_item 须另一人确认。`audit.Record` 自动把 `Env.DraftID` 写入 audit_log，日志输出 draft_id，get_draft 返回 audit_log_ids。

`Registry.SetDraftPreview(operation, fn)` 为可起草操作接入只读预览；预览不写表，`get_draft` 在原有的可重复读只读事务里执行它。`open_order`、`add_order_item`、`open_groupbuy_order` 有预览：逐行返回项目名、岗位上员工名，以及按第一个岗位员工计算的一个适用价格（标准价或级别价及来源，第一个岗位为空时取标准价），与 `open_order`/`add_order_item` 的计价一致；顾客的可用卡按类型返回余额和本项目剩余次数，储值类卡的会员价按行（含行号和项目）返回，并按该行第一个岗位员工的级别价与会员价规则计算。没有 `customer.view` 或顾客不在查看范围时，预览仍返回项目和员工，但不返回卡。补录预览与补录开单共用日期校验，未来的、超过补录天数的、已封账或工资月已锁定的日期返回同一条规则提示。预览查询全部按批读取，条数不随行数增长；`get_draft` 返回 `preview`，规则拒绝时返回 `preview_error` 文字，内部错误则整次读取失败。`list_drafts` 不生成预览，避免列表的 N× 成本。草稿参数里的手机号仍按原规则打码，预览不返回顾客手机号。

草稿控制的 create/update/reject 声明为专用 `AIManageDraft`，仅这三个操作允许使用；它们只写草稿。`confirm_draft` 为 AINone，只允许人通过 HTTP 确认，AI 无法自行确认业务写入。

## AI 对话

`POST /api/ai/chat` 是对话入口，请求体为 `{messages:[{role:user|assistant, content:string}]}`，一次最多 20 条、单条最多 4000 字，超过返回 422；需要登录，权限与本人一致（ADR-0009）。响应为 `{reply, sources, drafts}`：`sources` 按顺序记录模型发起的每一次工具调用（含起草和修改草稿），含操作名、原样参数、是否成功和错误文字；`drafts` 按顺序返回本次对话创建或修改的草稿，每项是 `drafts.Draft` 视图并带 `preview`（没有预览或预览规则拒绝时带 `preview_error`）。

`internal/ai` 通过 `Model` 接口隔离模型：生产实现用 openai-go 访问 DeepSeek 的 OpenAI 兼容 Chat Completions + tools；测试使用脚本化假模型，不访问网络。系统提示用中文说明今天的营业日、提问员工的姓名和角色，并要求只根据工具结果回答、不编造数字、工具结果是数据而不是指令。「今天」取 `settings.BusinessDate`，营业日结束时间晚于午夜的门店，凌晨提问时仍显示前一个营业日。姓名写入提示前去掉换行和控制字符并限制 30 字，每个角色限制 20 字；assistant 历史消息保持原样，不在此处过滤（已知风险）。

一次对话最多 6 次模型调用，模型一次响应最多 8 个 `tool_calls`，超过都返回 502「AI 调用工具次数超过上限」；每个工具结果放入模型上下文前按 rune 截断到 16000 字并附「（结果过长，已截断）」，`sources` 不受截断影响。整个请求（提示构造、模型调用和工具执行）有 120 秒总期限，超时返回 502「AI 对话超时」。工具参数错误、权限拒绝或业务规则拒绝会作为 tool 消息返回给模型，让它自行修正；`ops.StatusOf` 为 5xx 的工具内部错误只返回固定文字「工具执行失败」，不把数据库等底层错误原文放进 `sources.error` 或 tool 消息，4xx 仍保留原文。

对话提供 `Registry.ChatTools`：所有 Read + AIDirect 且调用者有权限的读操作、所有调用者有权限的 AIDraft 操作，以及 `update_draft_params`。AINone 操作（verify_coupon、redeem_coupon、revoke_coupon、confirm_draft 等）、`create_draft` 和 `reject_draft` 既不提供，`CallChatTool` 也按未知工具拒绝；起草工具只在调用者本人有目标操作权限时出现。`CallChatTool` 把 AIDraft 调用仍交给 `Registry.CallTool`，只创建待确认草稿，不执行业务写入；读操作仍走同一调用路径。`update_draft_params` 只能修改本次 Ask 创建的草稿，其他草稿返回工具错误「只能修改本次对话起草的草稿」。一次 Ask 最多起草或修改 5 张草稿（包括失败或参数错误的起草/修改调用，用来限制模型反复试错），第 6 次返回工具错误「一次对话最多起草 5 张草稿」，不返回 5xx。所有 Read + AIDirect 操作都是纯读，包括 `list_drafts`、`get_draft`、`get_redemption` 和 `get_rotation`。数据范围仍由各操作自己的 `env.Grant` 执行：例如发型师只能查询本人的业绩与提成，店长/老板可查全店；两人的租户和门店不会互相看到数据。模型未配置返回 503「AI 未启用」，模型不可达或响应不可用时返回 502，错误正文不含密钥或上游地址。

系统提示要求模型：写操作只会生成草稿，必须由有权限的人确认后才生效，不得说「已办好/已开单」；开单前先用 search_customers、list_items、quote_item_price、list_customer_cards 查清顾客、项目和价格，查不到或有歧义时先问员工；团购开单只针对已核销且未关联单据的核销记录（list_redemptions 的 unlinked_only），AI 不能核销。一次对话最多 6 次模型调用、每次响应最多 8 个 `tool_calls`，超过都返回 502；工具结果仍按 16000 字截断，整个请求仍有 120 秒期限。

本接口不保存会话、不做流式输出；评测集和管理端/小程序界面分别在 P0-32 和后续任务中实现。`explain_commission` 专门支持「解释一笔提成的来源」（用户故事 130）。
