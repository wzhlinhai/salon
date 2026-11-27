# 开源 personal agent 怎么"监听事件、决定要不要动、然后动"（2026-11-27）

问题：开源的 personal agent / proactive agent 在机制层面怎么触发、准入、去重、静默、存状态；它们各自的设计思想是什么；对我们（ADR-0012 草案）有什么可借。

范围：只补 [2026-11-20 的调研](2026-11-20-proactive-agents.md) 没有的内容。那篇已覆盖 dsh-superagent 的设计概念、LangChain ambient agents、Salesforce、Shopify、学术基准、打扰成本研究，这里不重复；dsh-superagent 只补源码层面"事件怎么进来、怎么准入"。

所有仓库、文档读于 2026-11-27，读的是默认分支当时的版本，链接未固定到提交。没有克隆仓库，没有运行任何下载的代码。

## 结论先说

1. **"先用确定的东西挡，再叫醒模型"已在三个项目里落成具体接口。** Hermes 的 `wakeAgent: false`、OpenClaw 的 `trigger.script` 返回 `fire: false`、DeepSeek 的 Gate 都是"模型之前有一道不花 token 的门"。门返回 `fire / message / state`，状态由门自己带（OpenClaw 上限 16 KB）。
2. **去重靠"确定性的键"，不靠模型记性。** DeepSeek 的触发 ID 由来源对象和版本拼成，重复到达直接返回 `duplicate`；LangGraph 示例把 Gmail 线程 ID 取 md5 当 thread ID；Activepieces/n8n 的轮询触发器把"上次时间戳或最后一个 ID"存起来。也有反例：LangGraph 示例里"已处理邮件检查"至今是 `TODO`；OpenClaw 迁移掉 heartbeat 时明说"心跳专用的去重被删掉了，重复更新可能被送达"。
3. **"无事不报"有三种做法，可靠性依次递减：** 门返回"不叫醒"（Hermes/OpenClaw，零 token）；让模型回一个暗号（Hermes `[SILENT]`、OpenClaw `NO_REPLY`，已经花了 token）；再让第二个模型判断要不要通知（Khoj，且出错时默认通知）。
4. **打扰收敛靠一组很小的参数：冷却、次数上限、过期、活跃时段、失败告警阈值。** OpenClaw 的 standing intent 默认冷却 24 小时、最多触发 3 次、90 天过期；失败告警默认 1 小时冷却、同一失败一天最多再报一次。
5. **放权普遍是"人批准后才生效"或"先只记录"，且批准有过期。** DeepSeek `observe` 只记录 `would_accept`；LangGraph 的 `interrupt` 给 accept / edit / ignore / response 四种回应；n8n 的人工审核有 1 小时 / 1 天 / 7 天三档过期；Activepieces 的审批是**发布时**审批，不是动作时审批。
6. **记忆和"对用户的了解"的积累，在个人助理类项目里是让 AI 自己改文件或记忆块**（Hermes 的 `MEMORY.md`/`USER.md`、Letta 的 memory blocks 和 sleep-time、LangGraph 示例把用户的 edit/ignore 回写成偏好）。这条与我们"AI 不改业务数据、规则由店主定"的约束冲突，只能借"反馈留痕"这一半。

## 逐项目

每项统一回答：许可证、触发来源、规则还是模型判断、怎么不打扰、动作要不要人确认、状态存哪里、读到的程度；再补设计理念、概念模型、放权与记忆。

### 1. OpenClaw（原 Warelay → Clawdbot → Moltbot）

| 问题 | 答案 |
|---|---|
| 许可证 | MIT（仓库元数据 `MIT`，[README 徽章](https://github.com/openclaw/openclaw/blob/main/README.md)） |
| 触发来源 | 见下方"触发器" |
| 谁判断 | 两层：可选的条件脚本（确定性）→ 模型。无条件脚本时直接是模型 |
| 不打扰 | `activeHours`、`idleOnly`、`NO_REPLY`、条件脚本 `fire:false`、standing intent 的冷却与次数上限、失败告警冷却 |
| 动作 | 自主执行为主；批准用在"创建常驻监视进程"和工具执行（`exec` 的 `ask: "always"`）；standing order 里写明哪些要人签字 |
| 状态 | 共享 SQLite 状态库（也支持 PostgreSQL 引擎）；每个 agent 一个库；MEMORY.md 等工作区文件 |
| 读到的程度 | 官方文档（仓库 `docs/`）为主；没读调度器源码 |

**触发器**（[schedules.md](https://github.com/openclaw/openclaw/blob/main/docs/automation/cron-jobs/schedules.md)）：

- 五种 schedule：`at`（一次）、`every`、`cron`、`on-exit`（被监视命令退出时触发）、`stream`（受监督的长驻命令输出的行，按 250 ms 静默合批）。
- 整点的 cron 会自动错开最多 5 分钟防止尖峰；`--exact` 可关。
- `stream` 同一时刻只留一个进行中的触发和一个待处理批次，期间到达的行合并进这个批次，**至少隔 30 秒**才再触发。
- 外部 HTTP：`POST /hooks/agent`，`hooks.enabled`、专用 token、`allowedAgentIds`；请求头 `Idempotency-Key` 丢响应后重放同一次运行而不重复执行；返回 200 只表示"已受理"，不代表模型已完成（[webhooks.md](https://github.com/openclaw/openclaw/blob/main/docs/automation/cron-jobs/webhooks.md)）。
- 聊天渠道消息：渠道是插件，进来的消息在内部发 `message:received` 等事件，这些是**只能观察、不能拦截**的异步事件（[event-types.md](https://github.com/openclaw/openclaw/blob/main/docs/automation/hooks/event-types.md)）。
- 邮件：Gmail Pub/Sub、IMAP 插件。

**条件脚本**（同一页"Event triggers"）：给 `every`/`cron`/`stream` 加 `--trigger-script`，返回 `{ fire, message?, state? }`；`trigger.state` 是上次返回的状态（≤ 16 KB）；`fire:false` 只记状态，不产生运行记录、不调模型；**payload 失败时本次返回的 state 不落盘**，下次还会再判断一遍；每次判断限 30 秒、最多 5 次工具调用；触发间隔下限 30 秒。官方示例是"PR 的 CI 检查集合是否变化"，比较的是每个检查的身份而不是汇总状态。

**心跳已被取消。** 以前的 heartbeat 是每 30 分钟叫醒 agent 读 `HEARTBEAT.md`，没事回 `HEARTBEAT_OK`。现在文档写明它被迁移成普通 automation 任务，用 `NO_REPLY` 表示无事；迁移说明里有那句"心跳专用的去重和无路由跳过被删除，重复更新可以被送达"（[heartbeat.md](https://github.com/openclaw/openclaw/blob/main/docs/gateway/heartbeat.md)）。默认创建一个"Proactive check"任务但**默认关闭**，开了之后每 30 分钟一次、等空闲、发给主人私聊。

**Standing intent**（"事件条件的未来动作"，[文档](https://github.com/openclaw/openclaw/blob/main/docs/concepts/standing-intents.md)）：存在 agent 的 SQLite 里，在"符合条件的互动回复前"检查关键词；默认冷却 24 小时、最多触发 3 次、90 天过期；只有已认证渠道上的主人能创建；"到点"类要求改用定时任务，两者不混。

**设计理念**（[VISION.md](https://github.com/openclaw/openclaw/blob/main/VISION.md)）：自称"the AI that actually does things / on your devices, in your channels, with your rules"；优先级第一条是"Security and safe defaults"；"Show what ran, failed, or was canceled, and how to recover"。**概念模型**：Gateway、channel 插件、agent、session、automation（job）、scratch、standing order、standing intent、skills、`MEMORY.md`/`SOUL.md`/`USER.md` 等工作区文件。**分工与放权**：[standing orders](https://github.com/openclaw/openclaw/blob/main/docs/automation/standing-orders.md) 把"永久授权"写成程序：每个程序有 Scope、Trigger、Approval gate、Escalation 四项，写在 `AGENTS.md` 里每次会话注入；日常只处理例外和审批。**记忆**：工作区 Markdown 文件加任务便笺（scratch，上限 256 KiB，会进入提示词，文档明确说别放密钥）。**收敛**：失败连续计数 + 冷却 + 错误退避；`openclaw doctor` 修复迁移。

### 2. Letta（原 MemGPT）

| 问题 | 答案 |
|---|---|
| 许可证 | 旧服务端仓库 Apache-2.0；现在的源码在 letta-code，Apache-2.0（仓库元数据） |
| 触发来源 | sleep-time：前台每 N 轮对话后；letta-code 的"做梦"：每 N 步或上下文压缩时；定时：cron/一次性 |
| 谁判断 | 计数器（确定性）决定何时叫醒；醒来的后台 agent 自己判断改什么记忆 |
| 不打扰 | sleep-time agent 不对用户说话，只改记忆；没有"提醒用户"的设计 |
| 动作 | 直接改记忆块；可选"agent reviews before applying"，文档说明不会问人 |
| 状态 | 旧服务端：数据库（含 Postgres 的 advisory lock 做调度器选主）；letta-code：本地 `~/.letta/crons.json`、MemFS（git 管理的记忆目录）；云端定时存在 Letta Cloud |
| 读到的程度 | 归档分支源码（`sleeptime_multi_agent_v4.py`、`jobs/scheduler.py`）、letta-code 源码与 README、官方文档的 scheduling 页（"heartbeat"的细节该页没写，未查证） |

- **sleep-time 的机制**：[`SleeptimeMultiAgentV4`](https://github.com/letta-ai/letta/blob/archive/letta/groups/sleeptime_multi_agent_v4.py) 在前台 `step` 结束后调用 `run_sleeptime_agents`，用 `sleeptime_agent_frequency` 和一个持久化的轮数计数器决定"每 N 轮跑一次"，把上次处理到的消息 ID 记下来，只把新对话喂给后台 agent，后台 agent 在一个独立的 Run 里异步执行。它做的是**记忆整理**，不是给用户的提醒。
- **letta-code 版**：[`post-turn-reflection.ts`](https://github.com/letta-ai/letta-code/blob/main/src/cli/helpers/post-turn-reflection.ts) 有 `off | compaction-event | step-count` 三档触发。
- **调度**：旧服务端 [`scheduler.py`](https://github.com/letta-ai/letta/blob/archive/letta/jobs/scheduler.py) 用 APScheduler，多实例时用 `pg_try_advisory_lock` 选出唯一的调度实例，其余实例定时重试抢锁。新版 `letta cron add --cron "0 9 * * 1-5" --prompt ...`，每次运行默认**新开一个会话**，cron 按 UTC 算，本地任务"只在 CLI/桌面会话开着时运行"（[文档](https://docs.letta.com/letta-code/scheduling)）。
- **理念**（[letta-code README](https://github.com/letta-ai/letta-code/blob/main/README.md)）："agents that are more like people than tools"，靠重写自己的记忆、技能、提示词随时间学习，也可以做"always-on agents that work proactively"。**概念**：memory block、MemFS、skills、subagent、channel、hooks、permissions。**放权**：权限模式 + 自动批准/拒绝列表。**记忆**：记忆块 + git 跟踪的 MemFS，可同步到自己的 GitHub 仓库。

### 3. Pi（earendil-works/pi，原 badlogic/pi-mono）

| 问题 | 答案 |
|---|---|
| 许可证 | MIT（仓库元数据；`packages/durable/package.json` 同） |
| 触发来源 | 没有定时/外部事件触发器。`pi.on(...)` 是**一次对话内部**的生命周期事件；`pi.events` 是扩展之间的进程内事件总线 |
| 谁判断 | 扩展的处理器代码（确定性）；可拦截的只有少数事件 |
| 动作 | `tool_call` 处理器返回 `{ block: true, reason }` 即拦下工具调用，也可以原地改参数 |
| 状态 | 扩展用 `pi.appendEntry()` 存进会话；`pi-durable`（实验性）可用 SQLite/JSONL 存储 |
| 读到的程度 | 官方文档与 README；没读实现源码 |

- [extensions.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md)：事件分成"只通知"、"原地改"、"链式变换"、"门（gate）"几种组合方式；`tool_call` 在工具执行前触发，**可拦截**，`event.input` 可改，且文档要求"门类处理器在无法做出安全判断时必须失败关闭（fail closed）"。`pi.events.on/emit` 就是一个内存里的 pub/sub，不跨进程、不落盘。
- [pi-durable](https://github.com/earendil-works/pi/blob/main/packages/durable/README.md)：对话、模型回合、工具调用先提交到存储再展示；崩溃后重开可续跑；`submit` 带相同 `requestId` 返回已有提交而不重复；"信号（signal）"是从外部送给睡眠任务的带类型 JSON（比如 webhook 结果、人的回答），重复到达返回 `duplicate`，先于睡眠到达的信号会被保留。它**没有**定时器触发或事件源，只有"任务睡在某个键上等外部信号"。
- **理念**（[README](https://github.com/earendil-works/pi/blob/main/README.md)）："a minimal, extensible agent harness"，"skips features like sub-agents and plan mode"。与主动提醒关系不大，价值在"拦截点"和"先落盘再展示、请求号幂等"这两个写法。

### 4. DeepSeek dsh-superagent-prototype（只补源码层面）

许可证 MIT（[仓库元数据](https://github.com/deepseek-ai/dsh-superagent-prototype)）。读了源码。

- **事件怎么进来**：GitHub 是**轮询**，`pollSeconds` 默认 60、范围 60–3600，且以 GitHub 要求的更长间隔为准（[github.ts](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/src/sources/github.ts)）；飞书是实时事件流消费 + 聊天历史回补 + 订阅线程轮询三条循环（[feishu-loops.ts](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/src/sources/feishu-loops.ts) 文件头注释；具体传输协议未细查）。
- **幂等键**：触发 ID 由来源 + 对象地址 + 评论 ID + **更新时间版本**拼成（`key('github-comment', html_url, id, version)`）；轮询游标比开始时间**往回退 5 秒**防漏，靠"确定性 ID 去重重叠部分"（[poll.ts](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/src/sources/poll.ts)）；已无活跃工单的线程在最后一次动静后再轮询 14 天就停。
- **进来后**（[intake.ts](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/src/core/intake.ts)）：`receive()` 返回 `stored | duplicate | filtered`；先查 ID 已存在则 `duplicate`；再按来源范围过滤，**被过滤的也存下来并记录原因代码**（`saveDisposition({filtered:{code}})`）；再按线程订阅分发给相关工单，没有工单认领的才排进 Eval。
- **准入矩阵**（[admission.ts](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/src/admission.ts)）：按"发送人是否在名单 × 是否点名机器人"分三格（`listedUnaddressed`、`unlistedAddressed`、`unlistedUnaddressed`），每格取 `explicit | inferred | ignore`；默认只有"名单内人未点名"算 `inferred`，其余 `ignore`。
- **Gate 规则长什么样**（[policy.ts](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/src/policy.ts)）：规则是 `{ id, name, enabled, instruction }`，`instruction` 是**一段自然语言**（默认带"文案修正""视觉小修"两条）；模型必须对**每条启用的规则恰好给出一个 `matched` 判断**，漏一条或多一条直接抛错；接受还要求 `intent_evidence`、方向已定（无待定设计决策）、至少命中一条规则；`inferred` 来源且模式不是 `auto` 时，不接单，记成 `would_accept: true` 并附 `proposal`，原因码 `observed`。这就是 observe 的实现。
- **状态**：SQLite（`node:sqlite`，WAL + `synchronous=FULL`），聚合以 JSON 行存，外加 `kv` 表；Outbox 把出站消息作为"回执"和重试任务**在同一个原子事务里登记**，重复消息 ID 忽略，退避 5 秒到 5 分钟（[store.ts](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/src/app/store.ts)、[outbox.ts](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/src/app/outbox.ts)）。
- **健康**：GitHub 轮询连续失败达到 `failuresBeforeError`（1–20）次才报异常，"一次网络中断不报"。
- **理念原文**（[000-philosophy.md](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/000-philosophy.md)）："Take as little human attention as possible, and give as much value as possible."；"Work nobody asked for needs a rule the team wrote, and starts in a mode that only records what the agent would have done."；"Start narrow."；"When the agent is noisy, make its prompts shorter and stricter, or pause it. Do not add more messages to explain the noise."。

### 5. Khoj（khoj-ai/khoj，AGPL-3.0）

读了源码和官方文档页。自我定位 "Your AI second brain"（[README](https://github.com/khoj-ai/khoj/blob/master/README.md)）。

- **触发**：用户在自动化页面或对话里定义"查询 + cron 表达式"。后端用 APScheduler `CronTrigger.from_crontab(crontime, 用户时区)`，`trigger.jitter = 60`，`max_instances=2`，"允许第二个实例杀掉持有过期锁的上一个实例"；任务存在 Django 作业库（`DjangoJobStore`）。**不允许每 N 分钟**：分钟位不是数字时随机取一个分钟，分散负载（[routers/helpers.py](https://github.com/khoj-ai/khoj/blob/master/src/khoj/routers/helpers.py)、[main.py](https://github.com/khoj-ai/khoj/blob/master/src/khoj/main.py)）。
- **跑的时候**：按时间检查"下次运行时间是否在未来"以跳过重复；对话已被删则**删除这个自动化**；执行后经 `to_notify_or_not` 提示词让**第二次模型调用**输出 `{reason, decision: Yes|No}`，只在满足用户条件时才通知。提示词示例就是"晚上查明天下雨吗，只有要带伞才通知"（[prompts.py](https://github.com/khoj-ai/khoj/blob/master/src/khoj/processor/conversation/prompts.py)）。**判断出错时默认通知**（代码里 `except` 返回 `True`）。
- **通知**：邮件（自建需配 Resend），文档页明确写了这一点（[automations.md](https://github.com/khoj-ai/khoj/blob/master/documentation/docs/features/automations.md)）。
- **读到的程度**：源码；没有读到去重表或"上次通知"记录的位置。

### 6. n8n 与 Activepieces

**n8n**（许可证：Sustainable Use License，`.ee.` 文件另需企业许可，[LICENSE.md](https://github.com/n8n-io/n8n/blob/master/LICENSE.md)；GitHub 元数据显示 `NOASSERTION`，不是 OSI 开源）。

- **触发器**：Schedule Trigger 节点（interval 或自定义 cron，节点内可加多个间隔，[ScheduleTrigger.node.ts](https://github.com/n8n-io/n8n/blob/master/packages/nodes-base/nodes/Schedule/ScheduleTrigger.node.ts)）；Webhook 节点；应用事件（如 Gmail Trigger）。定时任务由 `ScheduledTaskManager` 用 `cron` 库按时区注册。
- **轮询去重**：Gmail Trigger 标 `polling: true`，用 `getWorkflowStaticData('node')` 存 `lastTimeChecked`，首次取"现在"，并用 `scannedPossibleDuplicates` 去掉边界上的重复（[GmailTrigger.node.ts](https://github.com/n8n-io/n8n/blob/master/packages/nodes-base/nodes/Google/Gmail/GmailTrigger.node.ts)）。`TriggersAndPollers` 还记录同一轮询"进行中"的数量，用来发现重叠的轮询。
- **AI Agent 节点**："Generates an action plan and executes it. Can use external tools."，记忆/模型/工具是子节点。**人工审核**：agent 的工具调用可以配成"先让人审"，渠道限 Slack / Telegram / Discord，审批请求有**过期**档位 3600 / 86400 / 604800 秒（[agent-hitl.schema.ts](https://github.com/n8n-io/n8n/blob/master/packages/%40n8n/api-types/src/agents/agent-hitl.schema.ts)）。
- 读到的程度：源码片段；状态存在数据库的哪张表未查证。

**Activepieces**（MIT，但 `packages/ee/` 和 `packages/server/api/src/app/ee` 另有许可，[LICENSE](https://github.com/activepieces/activepieces/blob/main/LICENSE)）。

- **轮询触发器**（[文档](https://github.com/activepieces/activepieces/blob/main/docs/build-pieces/piece-reference/triggers/polling-trigger.mdx)）：启用时把"最后时间戳或最近条目 ID"存进 store；每 **5 分钟**跑一次，拉增量，返回新条目数组。官方轮询库有两个去重策略：`TIMEBASED`（按条目时间戳，`lastPoll` 存 store，缺失则报错）和 `LAST_ITEM`（按最后一个 ID，[polling/index.ts](https://github.com/activepieces/activepieces/blob/main/packages/pieces/common/src/lib/polling/index.ts)）。
- **队列**：BullMQ 风格的 job queue，带"轮询独占"和"僵尸轮询"拦截器，说明同一触发器同时只允许一个轮询在跑（文件名可见，内部实现未读）。
- **Agent 与规则的分工**：文档的话是"If you can write the rule in one sentence without saying 'depends', it's a condition, not an agent."，"The flow handles what's certain, the agent handles the rest."（[in-flows.mdx](https://github.com/activepieces/activepieces/blob/main/docs/agents/in-flows.mdx)）。
- **审批**：[Flow Approvals](https://github.com/activepieces/activepieces/blob/main/docs/flows/flow-approvals.mdx) 只在**敏感项目发布流程时**要审批，管理员绕过，审批期间旧版本继续运行；这不是对每次动作的审批，企业/云版功能。

### 7. LangGraph / agents-from-scratch

许可证 MIT（两个仓库的元数据）。读了示例源码与官方 cron 文档；LangGraph Platform 的服务端实现不在开源仓库里，cron 定义存哪里文档没写（未查证）。

- **邮件怎么进来**：不是推送，是**轮询脚本**。[`setup_cron.py`](https://github.com/langchain-ai/agents-from-scratch/blob/main/src/email_assistant/tools/gmail/setup_cron.py) 用 `client.crons.create("cron", schedule="*/10 * * * *", input=...)` 每 10 分钟跑一次摄取图；[`run_ingest.py`](https://github.com/langchain-ai/agents-from-scratch/blob/main/src/email_assistant/tools/gmail/run_ingest.py) 拉最近 N 分钟（`after:` 查询）的邮件，每封邮件建一个 run。
- **幂等**：thread ID = `uuid(md5(Gmail threadId))`，同线程再来就是同一个 thread；已有 run 先删再建，`multitask_strategy="rollback"`。同一文件的"检查是否已处理过"仍是 `# TODO: Add check for already processed emails`。
- **规则还是模型**：进来后先 LLM **分诊**成 `respond | notify | ignore`（[email_assistant_hitl_memory_gmail.py](https://github.com/langchain-ai/agents-from-scratch/blob/main/src/email_assistant/email_assistant_hitl_memory_gmail.py)）；两层之间没有确定性规则的门。
- **人工审核**：`interrupt([request])` 暂停；`notify` 类让人选 `response` 或 `ignore`；工具调用类让人选 `accept | edit | ignore | response`。**用户的 edit 和 ignore 会触发 `update_memory`**，把偏好写进 store 的 `triage_preferences` 等命名空间，下次分诊提示词读它。
- **cron 文档**（[docs.langchain.com](https://docs.langchain.com/langsmith/cron-jobs)）：所有 cron **按 UTC**；无状态 cron 每次新建线程，`on_run_completed` 默认 `delete`，可设 `keep`；要求用完删除 cron 避免模型费用；cron 在后台运行，不影响正常调用。
- 理念见 [2026-11-20 那篇](2026-11-20-proactive-agents.md) 的 LangChain 一节（通知/提问/审核三种介入）。

### 8. Home Assistant（Apache-2.0，仓库元数据）

读了官方文档页；没读自动化引擎源码。

- **自动化三段式**：`trigger`（何时）→ `condition`（是否满足）→ `action`。触发器包含 state、numeric_state、time、time_pattern、webhook、template、sentence 等；文档强调"A trigger reacts at a specific moment, not while something is true"（[trigger 文档](https://www.home-assistant.io/docs/automation/trigger/)）。
- **事件总线**：`hass.bus`，"Home Assistant is driven by events"，`async_listen` 可带 `event_filter` 提前丢弃（[内部文档](https://contributors.home-assistant.io/core/internals/events/)）。`state_changed` 的字段文档里没列，未查证。
- **重入控制**：`mode` 取 `single`（默认，忽略新触发并记警告）、`restart`、`queued`、`parallel`；`max` 默认 10，`max_exceeded` 控制日志级别（[modes](https://www.home-assistant.io/docs/automation/modes/)）。
- **LLM 在自动化里怎么用**：动作 `ai_task.generate_data`，可带 `structure`（字段 + 选择器）得到结构化结果，写进 `response_variable`，后面的动作再用；它"不进行对话，也不控制设备"（[AI Task](https://www.home-assistant.io/integrations/ai_task/)）。触发器和条件仍然是确定性的，LLM 只是其中一个动作。
- **权限陷阱**：对话代理默认只能碰"暴露"的实体，但选了 Home Assistant 工具集的代理**能碰所有实体**，包括没暴露的；锁、报警面板、脚本不会被自动暴露（[文档](https://www.home-assistant.io/voice_control/voice_remote_expose_devices/)）。
- 状态存储：未查证。

### 9. 其它：Hermes Agent

选它是因为它是现在最有影响力的开源 personal agent 之一（MIT，仓库元数据约 26.6 万星，Nous Research）。读了 README、`cron/AGENTS.md`、cron 与 memory 用户文档。

- **触发**：内置 cron，`hermes cron create "every 5m" ...`；调度可写时长、"every monday 9am"、5 位 cron、ISO 一次性。
- **模型之前的门**：任务可挂一个**预检脚本**（必须是文件，不能是命令行），最后一行输出 `{"wakeAgent": false}` 则**整次不调模型**；`no_agent=True` 时脚本就是整个任务，脚本标准输出原样发送，**空输出 = 静默**，非零退出会发错误告警（[cron.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/cron.md)）。
- **模型之后的静默**：回复含 `[SILENT]` 则抑制发送，但输出仍存到 `~/.hermes/cron/output/` 供审计；**失败的任务永远送达**，只有成功才可静默；连残缺的暗号（`[SLENT]`、被截断的 `[SIL`）也算静默。
- **可靠性**（[cron/AGENTS.md](https://github.com/NousResearch/hermes-agent/blob/main/cron/AGENTS.md)）：`tick()` 在派发**之前**推进 `next_run_at`（崩溃时至多一次），同时标 `pending_slot`，死掉的持有者只恢复一次；补跑窗口 = 周期的一半，夹在 120 秒到 2 小时；每个 profile 一把 tick 锁防重复；配置不合格的任务只发一次告警且**不调模型**；`failure_streak` 连续失败计数。
- **状态**：`~/.hermes/cron/jobs.json`（文件）+ 输出目录。**记忆**：`MEMORY.md`（2200 字符）和 `USER.md`（1375 字符）存在 `~/.hermes/memories/`，会话开始时冻结注入提示词，由 agent 自己用 `memory` 工具增删；满了不自动压缩，报错让 agent 自己腾位置（[memory.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/memory.md)）。
- **理念**（[README](https://github.com/NousResearch/hermes-agent/blob/main/README.md)）："the only agent with a built-in learning loop"：自动从经验创建技能、改进技能、用 Honcho 做"用户模型"。

未单独写：Leon、Mem0、Agent Zero 等。Mem0 是记忆层库，不是触发机制（Apache-2.0，仓库元数据），本次未读；Leon、Agent Zero 本次未读，不下结论。

## 对比一览

| 项目 | 事件进来 | 门（模型之前） | 无事不报 | 去重 | 要不要人确认 | 状态 |
|---|---|---|---|---|---|---|
| OpenClaw | cron / stream / webhook / 渠道消息 / IMAP | `trigger.script` 返回 `fire` | `NO_REPLY`、`fire:false` | `Idempotency-Key`；standing intent 冷却和次数 | 常驻监视需批准；工具 `ask` | SQLite / PostgreSQL |
| Letta | 每 N 轮 / 压缩 / cron | 计数器 | 不向用户说话 | 记"最后处理的消息 ID" | 否（直接改记忆） | 数据库 / `crons.json` / MemFS |
| Pi | 对话内事件 | `tool_call` 可拦 | 无 | `requestId`（durable） | 扩展自己写 | 会话条目 / SQLite |
| DeepSeek | 轮询 + 事件流 | 来源范围 + 准入矩阵 + 规则判断 | observe 只记录 | 确定性触发 ID | 是（observe→auto） | SQLite |
| Khoj | cron | 无 | 第二次模型判断 | 时间检查 | 否 | Django 作业库 |
| n8n | 定时 / webhook / 轮询 | 节点参数 | 无 | 静态数据 + 可能重复列表 | 工具审核（带过期） | 工作流静态数据 |
| Activepieces | 定时 / webhook / 5 分钟轮询 | 流程条件 | 无 | 时间戳或最后 ID | 发布时审批 | store |
| LangGraph 示例 | 10 分钟 cron 轮询 | 无（直接 LLM 分诊） | 分诊为 ignore | 线程 ID = md5(邮件线程) | `interrupt` 四种回应 | LangGraph store |
| Home Assistant | 事件总线、定时、webhook | `condition` | 条件不满足即不动作 | `mode` 控制重入 | 否 | 未查证 |
| Hermes | cron | `wakeAgent` / 空输出 | `[SILENT]`、空输出 | 提前推进 + 执行台账 | 危险命令走审批（本次未读） | `jobs.json` |

## 对我们的启发

### 思想层面可以借鉴

1. **一句话能说清就是规则，不是模型**（Activepieces）。与 ADR-0012 "规则决定提醒，模型只写话"同向，可以直接作为判断标准写进 ADR：不能写成确定条件的东西，第一版不做。
2. **注意力是预算**（DeepSeek）：每个提醒必须比它占用的注意力更有价值，否则删掉；吵了就收紧规则，不要多加解释消息。
3. **先观察再放权，且观察记录要能回答"为什么没提醒"**（DeepSeek 连被过滤的触发也留记录和原因码）。
4. **永久授权写成"范围、触发、批准门、升级条件"四项**（OpenClaw standing orders）。每条提醒规则可以用同一个模板写：看什么、何时、提醒谁、是否带草稿、什么情况转给老板。
5. **用户的纠正是数据**（LangGraph 把 edit/ignore 记下来）。我们只记"忽略/修改/确认"的次数，供老板看，不让 AI 自己改规则。
6. **安全和可恢复优先于功能**（OpenClaw VISION 第一条；失败要能看见、能恢复）。

### 机制层面可以借鉴（在我们这里怎么落）

| 借鉴 | 来源 | 在我们这里 |
|---|---|---|
| 规则是 Go 函数，返回"是否触发 + 对象 + 事实 + 新状态"，不调模型 | OpenClaw `fire/message/state`、Hermes `wakeAgent` | 每条规则一个 `func(ctx, tx, store, bizDay) []Hit`；`Hit` 只带记录编号（不带姓名、手机号）；模型只在有 `Hit` 时被叫醒写话 |
| 确定性去重键 + 唯一约束 | DeepSeek 触发 ID、LangGraph 线程 ID | `UNIQUE (rule_id, store_id, subject_type, subject_id, business_day)`；插入冲突即 `duplicate`；重启后重跑同一天检查不会重复 |
| 事件和业务数据同一事务 | DeepSeek Outbox（回执 + 重试任务同一原子事务）、pi-durable"先提交再展示" | 与 ADR-0012 的"业务事件表同事务写入"一致，不用改；Outbox 思路留给以后推送 |
| observe 记录 `would_accept` + 规则版本 + 命中依据 | DeepSeek `policy.ts` | 观察档写一行：规则 ID 和版本、命中对象、命中的条件、本来的收件人和文案、被过滤的原因码 |
| 冷却 / 最大次数 / 过期三个参数 | OpenClaw standing intent | 规则表加三列：同一对象冷却营业日数、最多提醒几次、几天后不再提 |
| 失败不要静默，也不要刷屏 | OpenClaw `failureAlert`、DeepSeek `failuresBeforeError`、Hermes `blocked_config` | 规则执行失败连续 N 次才记为异常；同一异常 1 天最多提一次；"配置不合格"不调模型 |
| 调度选主 / 防重入 | Letta `pg_try_advisory_lock`、HA `mode: single`、Hermes 每 home 一把 tick 锁 | 现有 `time.Ticker` 循环里，按门店跑检查前取 `pg_try_advisory_xact_lock(规则, 门店)`；上一轮未结束就跳过 |
| 重启后补跑要有窗口 | Hermes 补跑窗口、OpenClaw 遇到过期任务重排而不是立刻重放 | 以"营业日 + 规则 + 对象"为键，重启后重跑当天检查是幂等的，补跑不会重复；超过营业日则放弃，不跨日补发 |
| 审批有过期 | n8n 1 小时/1 天/7 天 | 与 ADR-0008 的"当天有效"一致 |
| 门返回的状态要在后续步骤成功后才落盘 | OpenClaw：payload 失败则 `state` 不保存 | 需要"上次观察值"的规则（如日结差额变化）：状态更新与提醒写入同一事务，失败一起回滚 |
| `AI Task` 式调用：无状态、带结构化输出 | Home Assistant `ai_task.generate_data` | 起草 = 一次无会话的模型调用，输入是去标识的事实，输出是固定结构（文案、草稿参数），由服务端校验后写草稿表 |

### 不适合我们

| 做法 | 为什么不适合 |
|---|---|
| 让模型定时醒来读上下文自己决定（OpenClaw 旧心跳、`/loop`、Letta 自管理定时） | 每次醒来都耗 token；今天报明天不报没法测试；OpenClaw 自己也把它降级成普通任务。ADR-0012 已否决 |
| 模型回暗号表示无事（`NO_REPLY`、`[SILENT]`） | 已经花了 token，且暗号被模型写歪就变成打扰（Hermes 专门写了一堆容错）。我们的"无事"由规则返回空决定 |
| 第二个模型判断要不要通知（Khoj） | 加一次调用，且它出错时默认通知，与"宁可少打扰"相反 |
| AI 自己改记忆、技能、提示词（Hermes、Letta sleep-time） | 与 ADR-0004"AI 不改数据"冲突；老板看不见也审计不了。我们的"了解"只记人工操作的统计 |
| 本地文件做状态（`jobs.json`、`crons.json`、scratch） | 单用户本机进程；我们是多租户 + PostgreSQL，状态必须带租户和门店并留 audit_log |
| 直接执行的"自主"模式（OpenClaw、Hermes、Letta） | ADR-0004 已定"AI 只起草" |
| 通用工作流引擎（n8n、Activepieces）整个搬进来 | n8n 许可证不是 OSI 开源；Activepieces 有 `ee` 目录；我们的规则要贴业务表，而不是配节点 |
| LLM 工具默认能碰所有东西（HA 的 Home Assistant 工具集可碰所有实体） | 反面教材。我们已规定：AI 用收件人本人权限，看不到的数据不进提醒 |
| 只能观察不能拦截的消息事件（OpenClaw `message:*`、Pi `pi.events`） | 它们不跨请求、不落库；我们要的是落库的业务事件 |
| 把整个事件流交给"准入矩阵"判断（DeepSeek 的人/名单/点名） | 那是"谁有资格给 agent 派活"的问题；我们的触发来自系统自己的写操作，收件人由权限决定，不需要这张矩阵 |

### 对 ADR-0012 草案的修改建议（只列建议，没改文件）

1. **写明幂等键。** 现有"同一规则、同一对象、同一营业日最多一条"已对，但要补成表上的唯一约束，列出键的组成；同一事务里插入冲突视为 `duplicate`。
2. **观察记录的字段要写进 ADR。** 至少：规则版本、命中条件、本来的收件人和文案、以及"被过滤/被去重/被打扰上限拦下"的原因码。DeepSeek 把被过滤的也留下来，这样老板问"为什么没提醒我"有答案。
3. **规则表加三个参数：** 同一对象的冷却营业日数、最多提醒次数、过期天数（OpenClaw standing intent 的 24 小时 / 3 次 / 90 天是现成参照）；现有"每人每天条数上限"保留。
4. **补"失败怎么办"一节。** 现在只说了成功路径。建议：(a) 模型写文案失败时，仍发一条规则自带的固定模板提醒（不依赖模型）；(b) 规则执行连续失败达到阈值才提示老板，同一异常一天最多一次；(c) 配置不合格不调模型。
5. **补多实例与重启。** 定时检查要有防重入（按规则+门店的 advisory lock）和"重启后同一营业日重跑是幂等的、不跨日补发"。工资自动锁定如果已有类似做法，直接引用。
6. **规则状态与提醒同事务。** 需要"上次值"的规则（日结差额变化）把状态存在规则状态表里，随提醒写入同事务更新。
7. **提醒的失效条件（我们自己的推断，不是从这些项目读到的）。** 提醒对应的条件消失后（如日结已完成）应自动标记失效并从首页移除；否则"当天有效"的草稿会留在一条已过时的提醒下。
8. **写明模型只写话、不决定"报不报"**，并且模型调用失败时 fail closed（不报）还是降级为固定文案要选一个；建议降级为固定文案，因为提醒本身是规则决定的。
9. **以后要推送时再引入"活跃时段"。** OpenClaw 的 `activeHours` 只在往外发消息时才有用；第一版只在系统内，不需要。
10. **考虑"同一规则连续被忽略 N 次自动降到观察档"**，对应 2026-11-20 那篇的"长期不理会的自动降级"，ADR 里目前没有落到字段。

## 补充：闭源的 personal agent 产品（2026-11-27）

两者都不开源，没有代码可读；只读了官方页面。

**Claude Duet**（Anthropic，2026-10-21 研究预览，[官方介绍](https://claude.com/resources/articles/claude-duet)）

- 面向"事情多到记不过来的专业人士和小老板"，以工作为主，个人杂事较弱。
- 主动的方式：每天早上一份简报；每隔几小时检查已连接的账户（原文说不持续检查是为了不耗光额度）；只在需要人关注时才发消息；人可以设定收哪类信息、多久一次。
- 发消息、付款、签文件、删除之前先给人看具体动作，等批准；交易证券、账户间转账即使批准也不做。
- 记忆、技能、连接的应用沿用 Claude 账户；它看不到密码和登录令牌；最敏感的信息（卡号、证件号）由分类器拦下。
- 官方写明的局限："might misread a thread or miscalibrate what's urgent"。

**Grok Bot**（xAI，2026-08 测试版，[官方指南](https://x.ai/bot/guides/grok-bot-101)）

- 每个用户一台云端电脑，bot 可以自己定时，也可以监听其它应用的事件。
- 不是一个助手，而是一组有名字、分工不同的 bot，可以互相求助，也可以按例行流程接力。
- 人确认：验证码和两步验证交还给人；示例里 bot 起草后等人批准才动手；另有一个审核代理判断允许、拦下或交给人。
- 权限靠"权限 + 审核代理 + 允许/禁止名单"，规则用自然语言写（"the rules are a prompt"），官方提醒这依赖模型守规矩，不是绝对保障。同一账户的 bot 共用一台电脑，一个 bot 登录过的网站其它 bot 也能用。

对我们的含义：Duet 的"定期检查 + 每日简报 + 只在需要时说话 + 外发和付款前必须批准"与 ADR-0012 同向；Grok Bot 用提示词当权限规则、bot 共用登录状态，是我们不学的地方（我们的权限在操作层用代码检查）。

## 课程与可跑的示例代码

- LangChain Academy《Building Ambient Agents with LangGraph》（免费），配套代码 [langchain-ai/agents-from-scratch](https://github.com/langchain-ai/agents-from-scratch)（MIT）：一步步做一个邮件助理，依次讲智能体基础、评测、人工审核、记忆；本文第 7 节读的就是它。
- [DAIR.AI Academy · Ambient Agents](https://academy.dair.ai/courses/ambient-agents)：1 课时，是否免费页面没写，未读内容。
- 本文读过源码的开源项目本身也是现成教材：DeepSeek dsh-superagent（事件进入、准入、observe、Outbox）、Hermes Agent 的 `cron/AGENTS.md`（调度可靠性）。

## 补充：Gemini 调研的核对（2026-11-27）

店主让 Gemini 另做了一份调研（提示词在 `.dev/gemini-research/personal-agents-prompt.md`）。Claude 抽查结果：

- 引用本店数据（功能清单）全部对得上：预约、库存、审批、短信一年都是 0；事后改单约 1,100 次、手工调卡约 400 次、删单 108 次、开卡事后补业绩归属人 94 次、团购 3,405 单、120 人次卡或套餐有余量、工资表 12/12 个月锁定。抖音一小时撤销窗口也与功能清单一致。
- 已核实：CopilotKit/OpenDuet（MIT）、thunlp/ProactiveAgent（Apache-2.0）、huginn/huginn（MIT）、BasedHardware/omi（MIT）都存在（GitHub 元数据）；Viva Insights 的 Briefing 邮件和 Digest 邮件确实暂停了（[Briefing 页](https://learn.microsoft.com/en-us/viva/insights/personal/Briefing/be-overview)、[服务说明](https://learn.microsoft.com/en-us/viva/insights/personal/Overview/mya-service-description)，Digest 自 2024-04-01 起）。
- 有出入：它引的 Shopify 帮助页上没有"首页最多 5 张卡"和"未经批准不会应用修改"这两句；它把清华 Proactive Agent 的基准写成 ProAgentBench，那是另一篇 2026 年的论文（原论文是 ProactiveBench）；"被打断后 23 分 15 秒才回到状态"出自 Gloria Mark 的采访，不是它引的 CHI 2008 论文。美业的爽约率、复购率、短信打开率多来自厂商或二手文章，它自己也标了二手，只能看个大概。
- 采纳进 ADR-0012 的：事实由代码显示、不经模型转述；不急的事合并成简报、只算一条；会损失钱的提醒不占每日上限；提醒放在每天必开的页面；观察档给老板看汇总加抽样；第一批规则按本店数据挑（日结异常、团购券未关联、发型师回访清单、月底锁定前检查），并且要同时有帮员工的，免得员工把它当监工。
- 已经覆盖、不用改的：草稿确认时按当时数据重新检查（确认就是重新执行操作，操作层照常检查）；后台任务用收件人本人权限查数据。
