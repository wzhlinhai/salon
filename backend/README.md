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
scripts/mutchecks.py --since <git-ref> [--jobs N]          # 不带清单：逐份运行 .dev/codex/*/manifest.py 的受影响项
DATABASE_URL=postgres://... go run ./cmd/salon migrate up
DATABASE_URL=postgres://... BOOTSTRAP_PASSWORD=... go run ./cmd/salon bootstrap \
  -tenant 本店 -store 本店 -owner-name 姓名 -owner-number 001 -owner-account boss
DATABASE_URL=postgres://... JWT_SECRET=至少32字节 go run ./cmd/salon serve
```

`mutchecks.py --since <git-ref>` 只选中该引用与工作树（含未跟踪文件）之间受影响的项，可与名称和 `--locate` 组合。保护文件、定义 guard 的包内测试文件或 e2e spec 改动会选中对应项；查询、迁移目录内任意文件改动会选中该目录的保护。每轮可运行受影响项，同时不带 `--since` 运行 `--locate` 检查整份清单的原文匹配。手动使用 `mutcheck.sh` 时测试选择只通过 `--run` 传入；省略它仍运行完整包。

`serve` 的可选环境变量：`HTTP_ADDR`（默认 `:8080`）、`TOKEN_TTL`（默认 `12h`）。健康检查 `GET /healthz`，登录 `POST /api/auth/login`。

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
- 权限检查只在 `ops` 里做：HTTP 和 AI 调用都经过同一条路径，先检查权限，再在调用者租户的事务里执行。操作处理函数里不再检查权限。
- 写操作只能声明为 `AIDraft`（AI 调用只产生草稿）或 `AINone`（不向 AI 开放），声明为 AI 可直接执行时启动即失败。草稿（P0-29）实现之前，AI 调用写操作一律被拒绝。
- 测试用 `optest` 对 HTTP 和 AI 两条路径跑同一组用例。草稿（P0-29）实现之前，写操作在 AI 路径上走不到业务规则（草稿类返回"需要草稿"，`AINone` 对 AI 不存在），所以写操作的业务规则只在 HTTP 路径上测，另用一个测试确认 AI 路径对这些写操作全部拒绝且数据不变。

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

- 设置值支持 time（时刻）、time_range（时段）、choice（Options 中的字符串）和 integer（Min/Max 内的非负整数）。`member_price_rule` 默认会员价优先，也可取较低价；`points_per_recharge_yuan` 为每积一分所需充值本金元数，默认 0（不积分），范围 0–100000。开卡、充值和升级补差价按当日规则积分，赠送金、转余额和调整余额不积分。`guest_count_split` 默认将客次记给第一个岗位的员工，也可选择单上员工均分，当天可生效。`performance_split` 默认按岗位比例分多人业绩，也可选择均分，项目没设岗位比例时均分；`performance_ratio_*` 为八种支付方式各自的 0–100 业绩比例，本金、现金、银联、微信、支付宝和挂账默认 100%，赠送金和红包默认 0%；购买次数、赠送次数的耗卡业绩比例默认分别为 100%、0%，均可当天生效。退卡赠送金 `card_refund_bonus` 默认不退、作废，可选择扣回未被撤单退回的消费赠送金；`card_refund_fee_percent` 为 0–100 的退款手续费百分比，默认 0，当天可生效。撤单授权窗口 `void_window_minutes` 为 0（默认关闭）或 1–60 分钟，当天可生效。
- 设置项在 `internal/settings` 的 `Catalogue` 里声明：名称、值的形式、系统默认做法和校验。新的业务规则（例如各项业务的做法选择）在这里加一项，再给 `Rules` 加一个读取它的方法。
- 每次修改都在 `setting_changes` 追加一行：改前、改后、生效日期、修改人、修改时间。`salon_app` 只有 SELECT 和 INSERT 权限。某个营业日执行的值，取生效日期不晚于这一天的最后一次修改；没有修改，或者改成了"恢复默认"，就用系统当前的默认做法。生效日期不能早于今天；划分营业日的设置（`fromTomorrow`，例如营业日结束时间）最早明天生效。同一项设置的修改用咨询锁排队，改前值才接得上前一次的改后值。
- 业务模块不直接查设置表：
  - `settings.BusinessDate` 算一个时刻属于哪个营业日，营业日 D 在第二天"营业日结束时间"截止，结束时间取 D 那天执行的设置；
  - `settings.Effective` 取某个营业日执行的设置；
  - 单据下单时调用 `settings.TakeSnapshot`，把返回的 `Snapshot.ID` 存进单据，开单直接使用同时返回的 `BusinessDate` 和 `Rules`，之后计算用 `settings.LoadSnapshot` 读回的规则，这样以后改设置不会影响已下的单；撤单授权窗口是例外，按调用时的营业日用 `settings.Effective` 读取。内容相同的快照共用一行。

## 卡

开卡锁序为按 UUID 排序的业绩员工 FOR SHARE → 本店顾客 FOR NO KEY UPDATE（串行判定首购，并检查查看范围和未合并）→ 卡类型 FOR SHARE，再新建卡、复制次数和追加流水。其他顾客引用仍用 FOR SHARE。顾客合并为顾客 → 单 → 欠款 → 卡 → 红包；不会回锁员工，员工变更也不请求顾客锁，因此开卡不会引入反向锁序。顾客 NO KEY UPDATE 与卡插入外键的 KEY SHARE 兼容。

退卡只退整张卡，需要 card.adjust。本金加剩余购买次数乘以随卡单次金额，按规则扣回已用赠送金，扣回额至多为本金与购买次数折算的可退款金额，再减四舍五入到分的百分比手续费；赠送次数不退款，剩余赠送金一律作废。规则取退卡调用的营业日，不用单据快照；确认报价必须与锁内重算金额一致。登记现金、银联、微信或支付宝方式，不实际付款。余额写一条 refund 流水，每个卡项目写一条 refund 次数流水，即使原值为零也保留，全部归零；积分不变，卡状态改为 refunded。卡修改（充值、调整、升级及自动升级、转余额双方）、显式结账及试算、自动选卡和撤单退回均拒绝已退卡。退卡不改单据、欠款、红包，不扣业绩或提成，不处理原路退回（#43）。

## 单据

开单必须选择男客或女客、指定或非指定；未提供顾客时自动创建未识别顾客，由开单权限覆盖。开单保存规则快照和营业日，后续加项目读取快照规则。项目行保留添加时的项目名称，并复制项目岗位，允许必填岗位暂空；级别价来自第一个岗位的员工，没有员工或级别价时取标准价。多人贡献只在项目行记录一次价格，应收按项目行求和。

锁按顾客 → 单 → 项目 → 按 UUID 排序的员工取得：已有顾客、项目和员工只检查状态，用 FOR SHARE；加项目在读改前视图前用 FOR NO KEY UPDATE 锁单。单仅授予 customer_id、status、merged_into、checkout_id 列的 UPDATE，顾客合并仍可改挂 customer_id。项目行和岗位保留原记录：项目行仅授予 order_id、价格、来源、级别、改价原因及去掉标记列的 UPDATE，岗位仅授予 staff_id 的 UPDATE；三张表都不允许 DELETE。

改单需要 order.edit，时间窗口按单的 business_date 与当前营业日比较；本人范围包含 opened_by 或任意项目行岗位上的员工，去掉的行也算出现过。去掉项目只标记，不计入应收。换岗位员工提交完整分配，未列出的岗位清空；仅在 slot 1 员工改变且项目行未改价时按单的规则快照重新取级别价或标准价。改价需要 order.reprice 和原因，改后不再随员工重取价。改单锁顺序为单 → 项目（仅重取价）→ 按 UUID 排序的员工。

合并单需要 order.edit，对每张单检查时间窗口和本人范围，只允许同一顾客、同一营业日的挂单合并。全部项目行（包括去掉的行）移到保留单，其余单保留，标为 merged 并以 merged_into 引用保留单；保留单的其他属性不变。客次按未并入其他单的单计，已合并单不单独计客次。每个已有单的写操作统一经过 `internal/orders.change`：在本店按 UUID 以 FOR NO KEY UPDATE 锁住全部单，检查操作所需状态（改单和结账为 open，撤单为 paid），再按操作声明的 `env.Grant` 检查营业日窗口和本人范围，读取改前视图、执行修改、读取改后视图并逐单写日志。加项目使用 order.create，改价使用 order.reprice，这两项按权限目录均无窗口或本人范围限制；其他改单使用 order.edit。已合并单拒绝写入。合并按 UUID 字符串排序逐张锁单，不锁顾客、项目或员工；顾客合并也在改挂前按 UUID 锁住该顾客的单；其他改单先锁单，再锁项目及按 UUID 排序的员工。

结账需要 payment.collect，一张或几张挂单通过 change 按 UUID 锁单后一起登记支付；有效项目行的必填岗位须有员工，各单的支付合计须等于各自实收（应收减持卡折扣）。结账后单标为 paid 并引用同一个 checkout_id，拒绝再次结账、加项目、改单、改价和合并。结账的营业日取调用时刻的 settings.BusinessDate，各单保留原顾客和营业日。checkouts 和 order_payments 仅授予 SELECT、INSERT，支付只追加；挂账是支付方式 debt。卡余额支付为 card，每笔通过 order_payments.card_ledger_id 关联一条 consume 卡流水，按单的规则快照中的 card_deduction_order 拆扣本金、赠送金，不足则整次结账回滚；不累计积分。扣次为 count，每笔支付一个有效项目行、一行扣一次，金额等于该行实收；与卡余额支付共用本店、查看范围、有效期和用别人卡的检查，但允许次卡。每笔通过 card_count_ledger_id 关联一条 consume 次数流水，按单的规则快照 card_count_order（默认 paid_first，可选 gift_first）扣购买次数或赠送次数，不决定计价卡。卡余额支付的卡须本店发行、持卡顾客在调用者查看范围内、非次卡、未过期（到期当天仍可用），用别人卡须单的快照中 others_card_payment 允许。

红包是独立于卡的一次性对象，卖红包记录售价和一到十名本店在职业绩归属人，送红包不记录售价及业绩归属人；不记录收款方式。面额须大于零，有效期不早于发放营业日，未识别顾客也能领取。结账只用单上顾客自己的红包，一张单可用多个，每笔抵扣不超过面额，一次用掉、不找零；红包不参与计价或选卡。未用红包可撤回，过期也可撤回，卖的红包撤回不记退款。状态为 available、used、revoked、expired，过期按当前营业日判断，到期当天仍可用。撤回先以 FOR NO KEY UPDATE 锁红包，再无锁检查顾客查看范围；发放先共享锁顾客，再按 UUID 共享锁业绩归属人。顾客合并在单、欠款和卡之后按 UUID 锁红包并改挂，与结账的卡→红包一致，结账或撤回不回锁顾客。

欠款是顾客欠门店的一笔钱，分为每笔挂账支付生成的单上欠款和不关联单的历史欠款。未识别顾客（没有手机号）不能结账挂账，历史欠款仍可登记。还款针对一笔欠款，可部分还但不能超过未还，仅支持现金、银联、微信、支付宝；欠款与还款只追加，顾客合并仅改挂欠款的 customer_id。登记历史欠款需要 data.import_export，沿用 customers.LockReference 的顾客共享锁和查看范围。还款需要 payment.collect，先以 FOR NO KEY UPDATE 锁欠款，再无锁检查顾客查看范围并读取已还；营业日取 settings.BusinessDate。合并先锁顾客，再改挂单、欠款；结账持单锁时无锁读取手机号，避免与合并倒置锁序，补手机号只会使顾客变为已识别。合并更新单等待结账提交，因此随后更新欠款能包含新挂账。还款只锁欠款，不回锁顾客，与合并没有锁环。list_customer_debts 的 totals 和 debt_summary 只统计未作废欠款，debt_summary 仅返回本店汇总，单上欠款金额恒等于 method=debt 支付合计，未作废欠款的金额恒等于已还加未还。

撤单需要 order.void，只撤已结账的单，时间窗口按原单 business_date 与当前 settings.BusinessDate 比较，必须填写原因。写 order_voids 反向单，记原单、原因、撤单营业日及撤单人；原单、项目、支付和消费流水保留，原单标为 voided，checkout_id 不变。卡余额按原消费拆分退回本金及赠送金，次数按原消费退回购买及赠送次数，各追加 void 流水并关联原消费与反向单；不检查卡有效期或持卡顾客查看范围；卡已退时整次撤单拒绝。单上挂账随原单作废，不计未还和 debt_summary，已有任何还款则拒绝撤单；作废欠款不能还款。用掉的红包清空当前支付引用，恢复可用（过期则显示 expired）。历史支付保留，删除 order_payments_red_packet 历史唯一索引，当前支付由 red_packets.order_payment_id UNIQUE 和红包锁保护；若红包已有多次历史支付，00024 Down 明确拒绝恢复旧索引。一起结账的单逐张撤；现金、银联、微信、支付宝仅由反向单记录撤单。锁顺序为单 → 按 UUID 的欠款 → 按 UUID 的卡 → 按 UUID 的红包（均 FOR NO KEY UPDATE），与顾客合并的顾客 → 单 → 欠款 → 卡 → 红包及结账的单 → 卡 → 红包一致；还款只锁欠款，不回锁单或顾客。开启撤单授权窗口后，`open_void_window` 要求本人重新输入登录密码（与登录共用锁定），窗口内只有开启人能撤，过期须重开。撤单按调用时的营业日读取当前设置，不读原单快照；关闭设置跳过窗口检查，缩短设置不影响已开窗口。

选卡规则 card_selection 从单的规则快照读取，默认 manual（手动选卡）要求每笔卡余额支付指定 card_id。by_discount（按折扣率）或 by_balance（按余额）允许每张单至多一笔未指定卡的卡余额支付。候选只列本店发行且属于单上顾客的卡，排除同单任意支付已指定的卡；与显式卡一起按 UUID 锁定后重新检查归属、营业日有效期、非次卡和本金加赠送金余额大于零。按折扣率逐张用其类型计价，按这张单实收从低到高；按余额按当前合计余额从少到多。同序先到期日（不限最后），再开卡时间，再 UUID。未指定支付按排序逐卡扣款，一张不够接着扣，每张使用的卡在原支付位置各追加一笔支付和消费流水；一起结账时按输入顺序逐单处理，每单在前面各单扣款后重新按当前余额过滤、排序并选计价卡，再计价、校验和写入；已扣空的卡不再参与后续单的计价。合计不足整次回滚。只给实际扣款或扣次的卡写日志。第一笔卡余额支付为未指定时，排序第一张是计价卡；其他卡只扣钱。扣次仍须指定卡，不参与选卡。preview_checkout 可传 auto_card 按同一快照规则只读选取计价卡，与 card_id 互斥；输出保留单据字段并返回 pricing_card_id（不传卡为空），不检查支付余额是否足够。

结账锁顺序为按 UUID 排序的单 → 卡（FOR NO KEY UPDATE）→ 红包（按 UUID，FOR NO KEY UPDATE）→ 计价卡及候选卡类型（FOR SHARE）→ 有计价卡的单上的有效项目（FOR SHARE）。没有计价卡的单不加载计价数据、不锁项目。每张单第一笔卡余额支付的卡是计价卡；按该卡类型和单的规则快照读取会员价，与行上已记录价格比较，不重取标准价和级别价。改过价的行保持原价；不打折项目行既不取会员价，也不享受持卡折扣，按行上已记录价格扣卡；取会员价的行不打折，其余可打折行按卡类型 discount_percent 逐行四舍五入算持卡折扣。结账将新价格、来源、卡类型和折扣写回变动的有效行，去掉的行不改。preview_checkout 需要 payment.collect，HTTP 和 AI 均可直接试算同一套计价规则；它使用只读事务，不加业务行锁、不检查必填岗位或支付、不写任何数据。

## 操作日志

- `audit_log` 只追加，`salon_app` 只有 SELECT 和 INSERT 权限。日志与业务操作在同一事务里写入，任何一步失败都会一起回滚。同一员工或角色的修改用行锁排队，在读取改前快照前加锁，日志的改前才接得上前一次的改后。
- 当前记录角色的新建、修改、删除，以及员工的新建、档案修改、登录设置、停用、恢复和角色分配；保留改前改后，删除角色时还保留持有员工的 ID、姓名。`list_audit_log` 需要操作日志权限，可按目标 ID 查询。每条日志记录目标种类及其关联顾客；`list_audit_log` 按调用者的顾客查看范围过滤关联顾客的日志。提成方案（`commission_scheme`，不关联顾客）、卡类型、开卡、充值、开单、加项目、去掉项目、换岗位员工、改价、合并单、结账已记录日志。新增关联顾客的种类（例如单）时，添加接收顾客 ID 的构造函数，并扩展迁移中的 CHECK 列表。
- 新操作在成功路径末尾用同一个 `env` 调用 `audit.Record`，传入不含密码或密码哈希的返回值视图；新建前、删除后传 nil。手机号字段的 JSON 键名必须是 `phone`，`audit.Record` 遇到别的键名会拒绝写入（读取时只按这个键名打码）。

## 共享修改与引用检查

卡的充值、调整、升级和转余额统一经过 `internal/cards.change`，它按 UUID 锁卡、检查顾客查看范围、读取改前改后视图并写日志。一次修改只取一次营业日；业绩员工在锁卡前检查。余额由卡视图读取，余额流水只在 `writeBalance` 计算前值、变动和后值，积分流水只在 `writePoints` 计算。自动升级沿用已持有的卡锁并保留独立日志。

跨模块引用检查由数据所属模块提供：`customers.LockReference` 检查本店、查看范围和未合并；`staff.LockAssignable` 按 UUID 锁住本店在职员工，交接来源用允许离职的 `staff.LockReferences`；`catalog.LockReference` 检查本店启用项目。只检查引用均用 FOR SHARE。报价 `catalog.Quote` 自己锁住读取的项目和员工；项目行岗位分配及是否重取价由 `catalog.QuoteLine` 负责。

测试调用和并发请求等待共用 `optest.App.Call`、`optest.WaitResult`、`optest.AssertWaiting`；事务 SQL、提交回滚和固定行顺序的表快照共用 `dbtest` 的辅助函数。各业务包只准备自己的业务数据。

## 业绩

performance_entries.member 在非 sale 时必填，既有非销售行迁移为 false；sale 必须 NULL，由 CHECK 保护。RecordCheckout 把同一张单的判断写入 item、consume、labor 的所有记录；ReversePerformanceOrder 原样复制。list_performance 暴露这个可空布尔值，与提成解释一致。

客数存于独立的 `guest_visits` 追加表。每张未合并的结账单计一个客次，一起结账仍逐单计。归属按开单规则快照的 `guest_count_split`：`first_slot` 取行序中首个有员工的有效项目行内最低已填岗位员工；`equal` 按行序、岗位序首次出现的不同员工均分。去掉的行不参与。份额向下取整到四位小数，余数全部给第一名员工，合计恰为 1；无人时记 staff 为空的门店记录、份额 1。复制单上的指定和男客、女客属性。撤单以撤单营业日追加正份额的冲回记录，统计时计负，原记录不改；每条仅冲回一次。`list_guest_visits` 按营业日、员工和本人或全店范围返回分页明细及完整范围的客数、指定、非指定、男客、女客净合计，只做一条明细和一条合计查询。表采用租户组合外键、RLS，仅授予 SELECT、INSERT。

`internal/performance` 拥有业绩表、纯分配函数和 `list_performance`。结账在支付写入后、标记已结账前，在已有单锁的事务内按各单规则快照追加记录；撤单在已有单锁的事务内逐条追加冲回，不改原记录，不另记操作日志。结账记录用结账营业日，冲回用撤单营业日。

仅有效且非扣次支付的项目行参与。卡余额从消费流水拆出本金和赠送金。支付方式金额按各行实收比例分摊，以分为单位先向下取整，再按项目行顺序、现金、银联、微信、支付宝、挂账、红包、本金、赠送金的固定顺序，在行和支付方式都缺分的格子补分，保留两边合计。均分或没设岗位比例时按已填员工岗位均分；按岗位比例时仅对已填岗位的权重重新归一。岗位分配先向下取整，余分给最前的已填岗位。无人或已填岗位权重合计为零时记门店未分配业绩；同一员工占两岗位分别留存。实收份额乘该方式的业绩比例，用 money 的四舍五入到分计算业绩。

耗卡业绩（consume）按扣次支付所关联次数流水的购买次数或赠送次数，取 card_items 随卡留存的单次金额，乘单的快照比例分给岗位员工；单次金额为零时不记。手工费（labor）对所有有效项目行按次计，包括扣次行，取加项目时留存的固定金额，或结账项目行价格（折扣前，含会员价）的百分比，四舍五入到分后按相同岗位规则分配；无人或已填岗位权重和为零时不记门店手工费。零手工费不记。

销售业绩（sale）与其他业绩共用 performance_entries，在来源事务中追加。开卡（card_open）计本金加随卡 card_items 的 unit_price × paid_count_opened 之和，次卡本金为零，套餐计本金加购买次数折算额；赠送金、赠送次数不计。充值（card_recharge）和手动升级补差价（card_upgrade）只计本金；充值触发的自动升级不另记。卖红包（red_packet_sold）计售价，送红包不计。多人按输入的业绩归属人顺序均分，先向下取整到分，余数全给第一人；零值和零份额不写。销售没有支付方式，不应用门店支付比例，ratio 固定 100、amount 等于 paid_amount。营业日取卡流水或红包的营业日。来源分别以租户组合外键关联 card_ledger_id 或 red_packet_id；销售的单、项目行、结账、设置快照和岗位均为空，purchase 保留 first 或 repeat。

首购是顾客在本店第一张卡的开卡，任何状态（包括已退）和顾客合并移入的既有卡都算此前已有卡；其余开卡、充值、补差价和卖红包都是复购。开卡先以 FOR NO KEY UPDATE 锁顾客，再检查是否有既有卡，同顾客的并发开卡由此串行，不能同时判为首购。调整、转余额、消费、退款和撤单退回不记销售；撤回卖出的红包不改销售。此步不冲回销售，退款扣业绩属于 #48；ReverseOrder 仍只按 order_id 冲回结账业绩。

读操作按不超过相隔 92 天的营业日范围查询，`performance.view` 本人范围仅返回本人记录，全店范围包括未分配记录，可再筛员工或 kind（item、consume、labor、sale）。明细倒序游标分页，合计始终按 item、consume、labor、sale 顺序返回四种金额，使用整个筛选范围，原记录减去冲回，可为负数。表有租户 RLS 和组合外键，仅授予 salon_app SELECT、INSERT。

## 提成

`internal/commission` 保存提成方案与版本，并从业绩记录计算提成，不含阶梯。整体方案含 rules、overrides、excluded_staff。每种来源 item、consume、labor、sale 至多一条规则；项目业绩可选 line_price（原价）、paid（折后价）、performance、cash_performance、card_performance，其他来源只允许 performance。规则按岗位、指定或非指定、会员或散客的开关决定格子键，每个键恰在相应开关开启时出现，格子键不重复；销售业绩不允许这些区分。格子可为空表，未填格子不计提成；每格恰有比例（0–100）或非负固定金额之一，使用 money 的精确字符串类型。

覆盖比例至少指定员工或项目，允许员工 × 项目、项目、员工三种覆盖，计算按此顺序优先于规则默认。每个来源、员工、项目组合不能重复，来源必须在同一方案内有规则；销售业绩不能有项目覆盖。覆盖值也是比例或固定金额之一。不计提成员工不能重复。所有岗位、项目、员工引用由所属包一类一次批量查询校验本店归属，允许离职或停用，不加行锁：这些表无 DELETE 权限，状态也不影响合法性。会员含义为结账时顾客名下有有效卡，结账时留存会员判断，不在查询时重新判断。

`commission_scheme_versions` 只追加，salon_app 仅有 SELECT、INSERT，租户 RLS 与门店、修改人的组合外键保护租户隔离。保存先获取每店独立的事务咨询锁（`/commission_scheme`，与设置项锁区分），再取营业日、最新版本、校验日期与方案、分配 latest + 1 版本号并写入。同日可多次保存，生效日期不能早于 settings.BusinessDate 算出的今天，也不能早于最新版本。旧版本不改不删，规则按 item、consume、labor、sale 归一排序，格子和覆盖保持输入顺序。

`save_commission_scheme`（Write、AIDraft）、`list_commission_scheme_versions` 和 `get_commission_scheme`（Read、AIDirect）均要求 settings.manage。列表版本号倒序、limit / before_version 翻页，含修改人名称而无方案内容；查询参数必须且只能指定版本号或营业日，保存与查询返回同样的完整名称视图。按营业日选择 effective_from ≤ 日期的最大生效日期，再取版本号最高的一版，即同日最后保存的方案。导出的 VersionInForce 还接受可选 saved-no-later-than 时刻，先排除该时刻之后保存的版本，供计算按业绩写入时刻选版。名称按员工、岗位、项目批量读取，查询数不随引用数量增加。

保存与 audit.Record 在同一事务，目标 commission_scheme，action 为 save_commission_scheme，before 为上一个最新版本视图（首次 nil），after 为新版本视图。日志不关联顾客，不包含手机号；日志查询对这一种类额外要求 settings.manage，避免操作日志权限泄露方案里的员工覆盖。业务规则通过 ops.Rule，沿用仓库统一的 HTTP 409；请求格式、金额和百分比格式校验为 422。

提成计算以一次结账内的（kind、order_item_id、slot）为一笔，将同笔全部支付方式的业绩记录合并；同一员工占两个岗位仍算两笔。sale 每条业绩单独一笔。staff_id 为空的门店未分配记录不产生提成。导出 Commissions(ctx, q, storeID, from, to, staffID) 供后续月锁、员工端和解释功能调用，提成不另存表；零金额也返回解释。

版本只批量读取一次，由 selectVersion 这个纯函数统一选择：effective_from 不晚于业绩营业日，saved_at 不晚于单位内最早 created_at，取最大生效日期，再取最高版本号。VersionInForce 和 get_commission_scheme 的日期查询也共用它。无版本或员工在 excluded_staff 中不计提成。先查员工 × 项目覆盖，再查项目、员工覆盖，最后按岗位、指定、会员键找规则格子；覆盖即使没有匹配格子仍然生效，但必须有该来源规则。项目覆盖仅作用于 item、consume、labor。没有命中项不产生提成。

item 基数按单位汇总：paid 为 paid_amount 之和；performance 为 amount 之和；cash_performance 只含现金、银联、微信、支付宝、挂账；card_performance 只含本金、赠送金。红包仅在 performance 中。line_price 为结账时 order_items.price（折扣前，含级别价、会员价与改价）乘单位实收 / 本次结账该项目行全部 item 记录的实收，分母包括门店未分配记录。consume、labor、sale 基数均为单位 amount 之和。分到员工及乘比例都通过 money 四舍五入到分，固定金额每笔一次，基数为零仍计固定金额。

冲回按（void_id、kind、order_item_id、slot）组成新笔，逐个 reverses_id 批量取得原记录，使用原单位的版本、命中项、基数与金额，金额取负，解释带原单位 ID；营业日为冲回日，因此归入冲回所在月。

结账在支付写入后、业绩写入前，以 cards.HasValidCard 为每张单读取一次会员判断：本店、该单顾客、active，复用卡支付 checkExpiry 的到期日规则，到期当天有效；不限卡种和余额。这是一个当时已提交状态的快照，不依赖该结果扣余额、次数或改变卡，卡随后开通、调整或退卡不应重算历史会员事实，所以无需新增行锁。该读取位于已有的单 → 卡 → 红包 → 卡类型 → 项目锁之后，不回锁顾客或其他卡，不逆转结账锁顺序。

list_commissions 是 Read / AIDirect，要求 performance.view。本人与全店范围沿用 list_performance；本人范围叠加他人的 staff_id 时为空，不能扩大范围。按营业日范围（相隔最多 92 天）、staff_id、source 筛选。以营业日降序、单位最早 created_at 降序、稳定单位 ID 降序排列；单位 ID 为该笔最小业绩记录 ID，limit / before_id 翻页。total 与按 item、consume、labor、sale 的 totals 使用完整筛选范围，原笔减冲回，不受分页影响。entries、explanation、totals 都在同一权限范围内，不含任何顾客字段。

加载始终为七条批查询：范围业绩、冲回原记录、项目行事实、全部方案版本、员工名称、岗位名称、项目名称。项目行事实只读取 order_items.price / item_id、orders.designated 与业绩实收之和，不回读单据重算价格、支付或业绩。查询数不随单位数增长，由 TestCommissionReadQueryCount 通过 HTTP 与 AI 验证。
