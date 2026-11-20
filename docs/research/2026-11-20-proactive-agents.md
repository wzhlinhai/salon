# 事件驱动、主动做事的 AI agent：业界做法与研究（2026-11-20）

问题：我们的 AI 现在只在有人提问时才动（ADR-0001、ADR-0004：读操作直接调用，写操作只生成草稿，有权限的人确认后执行）。要让 AI 被业务事件唤醒、主动起草，别人是怎么做的，有哪些已验证的结论？

调查范围：DeepSeek 开源的 dsh-superagent 原型（读了仓库原文）、LangChain、Salesforce、Shopify 的公开设计、主动 agent 的学术基准、人机交互中关于"打扰成本"的研究、美业软件的现状。

## 结论先说

1. **"起草、人确认"加收件箱是业界共识的形态。** LangChain 的 Agent Inbox、DeepSeek 的面板、我们的待确认收件箱是同一个东西。
2. **先用确定的规则决定要不要触发，再让模型做事。** DeepSeek（准入 + 研判两层）、Salesforce（Flow 是 AI 的确定性护栏）、Shopify（Flow 规则执行，Sidekick 只帮写规则）各自独立得出这个结论。
3. **先观察、后放权。** DeepSeek 的接单模式默认 `observe`：只记录"本来会做"，看过记录再切 `auto`。
4. **打扰的代价比漏帮一次更大。** 研究显示主动介入约一半有用、约一成是干扰；限制提问次数能大幅降低注意力成本而几乎不损失效果。
5. **美业几乎没人做主动 agent。** 主流美业软件的 AI 基本是被动接待员（接电话、回短信、约时间）。

## DeepSeek dsh-superagent

来源：[deepseek-ai/dsh-superagent-prototype](https://github.com/deepseek-ai/dsh-superagent-prototype)（2026-10-31 发布，MIT，TypeScript，运行在仍处开发者预览的 DeepSeek Harness 上）。读了 README、docs/000-philosophy、001-domain、002-authorization、007-architecture 的目录与关键段、prompts/admission.md。README 自述是研究原型，部分设计未实现。

它是**写代码的 agent**：监听 GitHub 和飞书，有人找它或团队写的规则允许时，开工单、在独立目录里改代码、提 PR，在原对话回复。

**不直接用它的代码**：领域不同（代码仓库 vs 门店业务）、语言不同（我们是 Go）、依赖的平台仍在频繁变动。

可借鉴的设计：

| 它的概念 | 含义 | 在我们这里 |
|---|---|---|
| Source → Trigger | 事件来源产生不可变事件 | 结账完成、日结有差额、顾客到店、团购券同步、漏打卡、月末等定时事件 |
| Eval：Gate + Assessment | 先确定性准入，再由模型研判 | 代码里的规则（如"日结差额不为 0"）；规则拿不准时才让模型判断 |
| Mandate | 接单的授权依据，记录策略版本 | 草稿上记"是哪条规则、哪个版本让 AI 起草的" |
| Ticket / Channel | 工单，回复写回原对话 | 草稿进待确认收件箱；以后可发微信 |
| 接单模式 off / observe / auto | observe 只记录"本来会接" | 同样三档，但我们的 auto 仍只是"放进收件箱"，不是执行 |
| Outbox | 待发消息在同一事务里登记，后台发送并重试 | 以后 AI 给顾客发消息时用 |
| 幂等 | 同一事件接两次只产生一张工单 | 同一天的日结差额只起草一张说明 |

它的核心原则（000-philosophy）："Take as little human attention as possible, and give as much value as possible."：

- 只在有人必须动手、或结果对人有用时说话；一次只问一个具体问题。
- 面板只显示需要人处理的事，健康状态不占地方。
- AI 太吵时让提示词更短更严，而不是加更多消息解释。
- 团队能用一句话说清的行为写成提示词，不做成设置项。

与 [admin/DESIGN.md](../../admin/DESIGN.md) 的"安静、只标异常"一致。

## 工业界

**LangChain：环境式 agent（ambient agents）**（[博客](https://www.langchain.com/blog/introducing-ambient-agents)，2025-01；[agents-from-scratch](https://github.com/langchain-ai/agents-from-scratch)；[Sequoia 播客](https://sequoiacap.com/podcast/training-data-harrison-chase-2)）

- agent 监听事件流（邮件、定时、回调），不等人打字，可同时处理多个事件。
- 人介入的三种方式：**通知**（只提醒，不动手）、**提问**（缺信息就问，不瞎猜）、**审核**（动手前给人看，可批准、直接改、或给反馈让它改）。
- Agent Inbox 刻意做成邮箱或工单队列，而不是聊天窗口。

**Salesforce Agentforce**（[架构指南](https://architect.salesforce.com/docs/architect/fundamentals/guide/agentic-patterns)；[Salesforce Ben](https://www.salesforceben.com/why-agentforce-needs-event-driven-architecture-and-how-to-implement-it/)）

- 外部系统发 Platform Event → Flow 判断 → 调用 Agent Action。
- "agent 提供意图，Flow 提供流程"；Flow 是 AI 的确定性护栏。

**Shopify Sidekick**（[Shopify 博客](https://www.shopify.com/blog/agentic-ai-tools)；[The AI Economy](https://theaieconomy.substack.com/p/shopify-sidekick-upgrade-agentic-storefronts)）

- 2026 冬季版的 Sidekick Pulse 主动发现转化机会、结账问题、流失风险，另有每周摘要。
- 事件触发交给 Shopify Flow：Sidekick 帮商家写规则，规则自己跑，不是 agent 每次推理。

## 学术研究

| 论文 | 结论 | 读到的程度 |
|---|---|---|
| [Proactive Agent / ProactiveBench](https://huggingface.co/papers/2410.12361)（清华、华为，2024） | 第一个"主动帮忙"基准；微调后 F1 66.47%，仍远不完美 | 摘要 |
| [PROBE](https://www.arxiv.org/pdf/2510.19771)（ICLR 2026） | "观察出需求、提出任务、可靠执行"整条链，最强模型也吃力 | 搜索摘要 |
| [ProAgentBench](https://arxiv.org/html/2602.04482v1)（2026） | 用真实工作数据；思维链反而降低隐式模式识别类任务的表现 | 搜索摘要 |
| [Do Proactive Agents Need an LLM to Decide When to Act?](https://arxiv.org/abs/2605.30152)（2026） | 小图模型做触发判断，每事件约 11 ms，比大模型触发快 4–7 倍、AUC 更高；未确认是否与简单规则比较 | 摘要 |
| [Prospective Memory for Proactive LLM Agents](https://arxiv.org/html/2611.05731v1)（2026-11） | 记住"将来要做的事"，在合适时机执行，同时减少不必要的介入 | 搜索摘要 |

## 人机交互：打扰成本

| 研究 | 结论 | 读到的程度 |
|---|---|---|
| [Assistance or Disruption?](https://www.alphaxiv.org/abs/2502.18658)（CHI '25，N=18） | 398 次主动介入：53.3% 有效，12.1% 是干扰，34.7% 用户没理会 | 搜索摘要 |
| [Ask Less, Ask Better](https://arxiv.org/abs/2610.24411)（2026-10） | 限制只问 1–3 个问题，注意力成本降 36–73%，效果保留 86–94%；提问前对照用户以前的回答再降 13% | 摘要 |
| [ProMemAssist](https://arxiv.org/html/2507.21378v1)（2025） | 日常场景里，多打扰一次的代价往往大于少帮一次 | 搜索摘要 |

## 美业现状

主流美业软件（Fresha、Boulevard、Vagaro、Zenoti、Square）的 AI 基本是被动接待员：接电话、回短信、约时间。主动做事只看到 Vagaro 宣传的"自动填补空档"。来源多为竞品厂商的对比文章，有利益立场，只能看个大概：[agentplace](https://agentplace.io/blog/which-salon-software-has-built-in-ai)、[bellabooking](https://bellabooking.com/compare-feature/ai-features)。

## 对我们的含义

已验证、保持不变：

- 写操作只起草、人确认（ADR-0004）；收件箱是主界面。
- 触发由确定的规则决定，模型只负责起草内容。
- 每条主动规则从 observe 开始。

要补进设计的：

1. **收件箱条目分三种**：通知（只看）、提问（AI 缺一个信息）、审核（确认草稿）。现在只有审核。
2. **打扰预算**：每人每天被主动打扰的次数有上限，超出的合并进摘要；统计每类提醒被理会的比例，长期不理会的自动降级。
3. **起草前先查顾客档案和历史回答**，已有的信息不再问人。
4. **幂等**：每个事件最多产生一张主动草稿。
5. **规则用文字写**：店长的一句话（如"日结差额超过 50 元才提醒我"）作为提示词，不新增设置项。

时机：不在第一个双轨营业日的必需清单内，等待确认收件箱有界面后再做。第一个场景建议"晚上自动核对日结，差额起草成说明"，以 observe 模式跑一两周，看记录准不准再放开。正式设计时写 ADR，引用本文。
