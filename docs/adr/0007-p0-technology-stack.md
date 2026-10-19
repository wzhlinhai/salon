# P0 技术栈：Go 后端 + PostgreSQL + Nuxt 管理后台 + uni-app 小程序

后端用 Go，数据库用 PostgreSQL（行级安全，见 [ADR-0003](0003-multi-tenant-data-model-from-day-one.md)），管理后台用 Nuxt，员工端和顾客端用 uni-app 做成一个微信小程序。收款、团购、AI 各自通过适配层接入，不把任何一家服务商的 SDK 写进业务代码。

| 层 | 选型 | 许可证 | 说明 |
|---|---|---|---|
| 后端语言 | Go | — | 单二进制部署；账务代码用显式 SQL 和强类型，便于审计 |
| HTTP 框架 | Echo v5（v5.4.0，2026-09-27） | MIT | mms 同样使用 v5 |
| 接口定义 | Huma（MIT，有 Echo v5 适配器） | MIT | 从同一份 Go 类型生成 OpenAPI 和 AI 工具的参数定义，HTTP 与 AI 共用 |
| 数据库 | PostgreSQL + 行级安全 | PostgreSQL 协议 | 按租户隔离（ADR-0003） |
| 数据访问 | pgx v5（v5.11.0）+ sqlc | MIT | sqlc 从显式 SQL 生成类型安全代码，不用 ORM 隐藏查询 |
| 测试数据库 | testcontainers-go | MIT | 测试跑在真实 PostgreSQL 容器中 |
| 迁移 | goose（v3.29.0） | MIT | |
| 金额 | shopspring/decimal；数据库用 NUMERIC | MIT | 禁止用浮点表示金额 |
| 认证 | argon2id（密码）+ JWT | 实现时核实 | 沿用 mms 的依赖组合 |
| 管理后台 | Nuxt 4 + Nuxt UI（TypeScript） | MIT | mms 同样使用 |
| 员工端、顾客端 | uni-app（Vue 3，TypeScript），编译到微信小程序 | Apache-2.0 | 一个小程序按角色显示；H5 作为备用 |
| 收款 | 收款适配层，见下文 | 视实现而定 | 具体路径取决于收款方案 |
| 团购核销 | 平台适配层：Prepare / Consume / Cancel / Status，带幂等键 | — | 见下文 |
| AI 模型 | DeepSeek V4.1 Flash（模型名 `deepseek-flash`），通过 DeepSeek 的 OpenAI 兼容接口 | 服务条款 | 用 OpenAI 官方 Go SDK（openai-go，Apache-2.0），地址指向 DeepSeek；不用 DeepSeek 专有功能 |
| 报表图表 | ECharts | Apache-2.0 | |
| Excel 导入导出 | excelize | BSD-3-Clause | |
| 预约日历 | schedule-x 或 vue-cal（待验证能否按员工分列） | MIT | FullCalendar 的资源视图需付费，不采用 |

## 收款

收款通道可插拔：系统定义一套统一的收款接口（发起收款、查询、撤销、退款、对账），每种接入方式各写一个实现。门店在设置里选择启用哪些通道，各通道的商户号和密钥也在设置里填写（ADR-0002）。系统不写死任何一家通道，店主是否选哪一家、用哪种模式，以后都可以改。

**本店收款方式（店主确认）**：现金、银联、微信、支付宝、美团点评团购、抖音团购。其中美团点评和抖音是团购渠道，不是收款通道，见下一节。

已调研过、可以作为实现的通道（不互相排斥，可以同时启用）：

- **现金**：不接外部通道，直接记录。
- **微信 V3**（退款、查单）：`wechatpay-go`（Apache-2.0）。注意：微信官方的服务端 SDK 页面写明已停止更新维护，建议参考其工具类自行接入，因此适配层要能替换。
- **微信 V2 付款码**：没有官方 Go SDK，官方 V2 SDK 也已停止维护。二选一：`gopay`（Apache-2.0，2026-08-22 仍有提交，覆盖 Micropay、授权码换 openid、撤销），或自写。用 gopay 时锁定版本并审读源码。
- **支付宝当面付**：没有官方 Go SDK。用 `gopay` 的 alipay 包，或直接对接支付宝的 OpenAPI。
- **收钱吧**：JSON 接口，MD5 签名，无证书。自写客户端，需要对接人提供 `vendor_sn`、`vendor_key`、`app_id`。

尚未调研、需要时再补的通道：

- **乐刷**（美管加后台配置过的通道之一）：接入方式、费率、对账方式都还没查。
- **银联**：刷卡通常经 POS 终端或收单机构，接入方式还没查。

每种通道的订单号、回调、退款和撤销规则都不同。适配层把它们统一成同一套状态（待支付、支付中、成功、失败、已撤销、已退款），业务代码只面对统一状态。

## 团购

- 美团、点评：先用无限聚核过渡（店主确认）。它的价格和授权方式见调研，服务商的说法（服务商自述）尚未逐条核实。
- 抖音：申请自研商家直连，批下来就绕开聚合服务商。
- 小红书：没有公开的核销接口，先在官方后台手工核销，再在系统里补录。
- 适配层保证替换服务商时业务代码不动。SaaS 阶段必须自己成为美团、抖音的官方服务商：美团规则禁止把对接能力封装后提供给其他软件厂商。

## AI

- 模型：DeepSeek V4.1 Flash，模型名 `deepseek-flash`（店主选定，2026-10-19）。DeepSeek 官方文档确认该名称当前对应 DeepSeek-V4.1-Flash-1017，支持 Tool Calls 和 JSON 输出，上下文 1M tokens。
- 只用 OpenAI 兼容的 Chat Completions 加 tools 协议。换模型只改配置里的 base_url 和模型名。
- DeepSeek 默认开启思考模式。工具调用场景下思考模式的开关、延迟和成本，要用评测集测过再定。
- DeepSeek 的 strict 模式目前是 Beta（要用 beta 地址，且所有字段 required、`additionalProperties: false`）。是否启用，由评测决定。
- 需要"必须调用工具"时，由 Go 侧兜底（未调用则重试或拒答）。
- 自建评测集（30–50 条真实店务问题，配标准答案和期望的工具调用）在上线前建好。以后要换模型或与其他厂商比较，用同一套评测。
- DeepSeek 没有语音识别接口。语音开单不进 P0，以后另选 ASR 服务商。

## 不采用

- **Node / TypeScript 全栈**：考虑过。Node 的一个优势是支付宝官方支持 Node.js SDK。但微信 V2 付款码和收钱吧无论哪种语言都要自写或用社区库，这个优势不足以抵消。账务需要显式 SQL、强类型和精确小数，单店部署一个二进制也更简单。mms 在同一领域验证过 Go + sqlc + RLS 的组合。
- **Java / Spring（fuint 的技术栈）**：体量大，单店不需要。
- **复用 mms 或 fuint 的代码**：两者都是 AGPL-3.0，闭源 SaaS 不能用。只借鉴设计思路，与 [早前决定](../research/2026-10-18-early-discussion.md) 一致。

## 小程序的配套条件

- 顾客端类目：生活服务 > 丽人服务 > 美发（无资质要求）。员工端：商业服务 > 企业管理（无资质要求）。
- P0 的顾客端只查余额、看明细，不做在线充值或购卡，因此不需要预付卡类目。这一点如果改变，要重新评估。
- 提交审核时要给审核员一个员工测试账号。
- 小程序备案、ICP 备案、HTTPS 是上线前的平台门槛，见 `research/miniprogram-launch` 分支的调研。

## 部署与开发环境

- 生产：一个 Go 后端进程 + PostgreSQL + Nuxt 前端。
- 托管：阿里云（店主确认，云服务器 + 托管 PostgreSQL）。国内服务器便于 ICP 备案和小程序域名要求。与模型服务商无关，换云厂商只改部署配置。
- 开发：PostgreSQL 用容器运行。行级安全相关的测试必须跑在真实 PostgreSQL 上，不能用内存替身代替。

## Considered Options

见上文"不采用"一节。

## Consequences

- 后端定义一次，HTTP 接口和 AI 工具（ADR-0001）都从它生成；前端类型从接口定义生成，不手写。
- 支付库维护风险由适配层隔离：替换一个库只动一个实现。
- 团购已定无限聚核（过渡），AI 已定 DeepSeek V4.1 Flash，托管定为阿里云。收款通道做成可选，门店设置里启用哪些不需要现在定。乐刷和银联的接入方式还需要调研。
- 调研中的版本号和许可证是 2026-10-19 查询的结果。实现时要锁定版本，并在 `go.mod`、`package.json` 中记录。
- 复用原则：能用 MIT、Apache-2.0、BSD 许可的开源库就直接用，不重复造轮子。业务规则（账务、业绩、提成、轮牌、开单、草稿）自己写。AGPL 项目（mms、fuint、OCA commission）只借鉴思路，不引入代码。
- 来源：`research/open-source-salon-systems`、`research/payments-and-hardware`、`research/shouqianba-integration`、`research/group-buy-aggregators`、`research/llm-selection`、`research/miniprogram-launch` 分支上 `docs/research/` 下的 2026-10-18 调研文档。
