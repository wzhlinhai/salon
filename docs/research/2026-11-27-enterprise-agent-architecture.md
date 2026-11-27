# 企业里的常驻 agent 怎么搭：门店 agent 和员工 agent 怎么分工（2026-11-27）

问题：一家店有一个"门店 agent"（替店里盯事情），每个员工又各有一个"个人 agent"（替自己问 AI）。别人家的系统里，这两类怎么分收件箱、怎么互相沟通、怎么分权限？要人确认的事怎么停下来、怎么接着做、没人回怎么办？我们的设计和他们比，哪里一样、哪里不一样、有什么没想到。

范围：
- 只看一手资料：官方文章、官方文档、协议规范。每条结论后面标了出处页面。
- 我们的设计指 `.dev/agent-improvement-plan.md` 里"Always-on 骨架"一节，尤其是"收件箱按什么分"和"门店收件箱怎么和员工沟通"两小节。
- 名词：**收件箱**是事件排队的地方，一个收件箱同一时刻只有一个"工人"在处理；**运行**是 agent 醒来干一次活；**会话**是一次运行用到的聊天和工具调用记录；**挂起**是 agent 停在某一步等人，不占资源。

## 先说没读到的

- Salesforce Agentforce 的官方帮助页（`help.salesforce.com` 的 "Agent Execution Context and Data Access by Type"）和开发者文档两次抓取都失败（页面没加载出来、403）。下面只在"没验证"的标注下提它，**不作为依据**。
- 没读：OpenAI 的 guardrails 页、LangGraph 的 supervisor 页、Inngest 的 AgentKit 页、Anthropic 的 *Building effective agents*。这几篇和本题关系较小，没有影响结论，但不能说"都读过"。
- 没有找到任何一份官方资料专门讲"一个组织级 agent 配多个员工个人 agent 的收件箱怎么拆"。Anthropic 原文只讲"每用户一个收件箱"。**门店一个加员工各一个，是我们自己在原文上的推广**，下面所有"别人怎么做"都是旁证，不是对这个拆法的直接背书。这一点要说清楚。

## 结论先说

1. **没有哪家的官方资料直接支持"门店一个、员工各一个"，但没有一家反对，几家的做法能拼出同样的形状。** AutoGen 的 agent 编号是"类型加一个键"，门店和员工只是同一种东西的两个类型；Inngest 的并发键是"每个键同一时刻只跑一件"，对应我们"一个收件箱一个工人"。
2. **我们"两边只发事件、不共享对话"的做法，比 OpenAI 的交接更稳。** OpenAI 的 handoff 会把前面整段对话交给下一个 agent；我们不这么做，所以老板权限下查到的东西不会漏进给员工的消息里。
3. **"门店运行用老板权限"是别人踩过坑的地方。** Microsoft Copilot Studio 的触发器只能用作者的凭证，官方在发布前专门弹警告：用户可能借 agent 用作者的权限。Slack 则把"应用自己的身份"（不跟任何人绑定）和"替某个人干活"分成两种令牌。我们应该让门店运行绑在"老板角色"上而不是某个人的账号上，并且**不让员工写的自由文字原样进门店运行**。
4. **"等人回复"别人都有一个共同做法：每个请求一个编号，回复带着编号回来。** A2A 协议、Temporal、Microsoft Agent Framework 都这样。我们的方案里"回复接回哪个会话"靠会话编号，但没写"回复对应的是哪一次请求""请求已经升级给别人之后原来那位又回了怎么办"。
5. **确认之后"只执行一次"别人专门提醒过。** OpenAI 官方文档说批准不等于只执行一次，崩溃后从旧状态恢复可能重做；LangGraph 说恢复时整个步骤从头再跑。我们的草稿确认要有幂等。
6. **"员工一直不回就升级"，官方资料里没有现成做法。** Temporal 的持久定时器、Anthropic 的跟进定时器给了零件，但"等多久、换谁"没有人写。这是我们要自己定的，别指望抄。
7. **LangChain 把"需要人"分成三种：通知、提问、审核；并给员工一个统一的"收件箱页面"，每条能接受、改、回复或忽略。** 我们已经有通知（情况一）、提问（情况二）、审核（草稿），但员工端还没有这个页面，也没有"忽略/做不了"这种明确的回应。

## 一、收件箱怎么分：门店一个，员工各一个

### 别人怎么做

- **Anthropic 原文**：先每个用户一个收件箱，需要更多并行再拆；一个收件箱同一时刻只有一个工人，"防止两次运行重复做事或互相覆盖"。原文没有讲组织级 agent，唯一的作用域建议就是这一句。来源：[Building always-on agents](https://claude.com/resources/articles/building-always-on-agents)。
- **Inngest**：并发键是一个表达式，算出的值就是"队列名"，每个不同的值各自限流。官方例子是"同一个客户同一时刻不能有两个步骤在跑"。改键只影响新来的活，已经排上的保持原来的键。来源：[Inngest 并发指南](https://www.inngest.com/docs/guides/concurrency)。
- **Microsoft AutoGen**：agent 编号是"类型加键"两部分。多租户时键取自业务数据，例如每个 issue 一个键，运行时没有这个 agent 就当场建一个。来源：[AutoGen Topic and Subscription](https://microsoft.github.io/autogen/stable/user-guide/core-user-guide/core-concepts/topic-and-subscription.html)。
- **Temporal**：官方把"一个长期存在的业务对象（用户账号、设备、订单）一个工作流"列为标准模式，所有状态变化靠信号和更新驱动。来源：[Temporal Durable AI](https://docs.temporal.io/ai)。另有一篇讲"每个 agent 是一个长期运行的工作流、等信号"：[Orchestrating ambient agents with Temporal](https://temporal.io/blog/orchestrating-ambient-agents-with-temporal)。
- **Microsoft Agent Framework 的官方页有一句提醒**：能写成一个函数的事，就别用 AI agent。来源：[Agent Framework Overview](https://learn.microsoft.com/en-us/agent-framework/overview/)。

### 和我们对照

- **一致**：每个"主体"一个排队处，同一时刻只处理一件，是 Anthropic、Inngest、AutoGen、Temporal 四家共同的形状。我们的"门店一个、员工各一个"可以直接理解成：同一种收件箱，键分别是门店编号和员工编号。
- **一致**：我们在计划里写的"门店这一层不让员工等"（结账时问的一句话不该排在老板的周报后面），正是 Inngest 并发键要解决的事，每个键各跑各的，互不阻塞。
- **不同**：官方资料里"键"都是一个业务对象（客户、issue、订单、用户）。我们的"门店"这个键是**一个长期存在的组织**，会被大量事件打到。资料没讲这种键要不要再拆。我们的计划里写了"以后多门店各一个"，这个方向没被反驳。
- **我们没想到**：Inngest 的并发键只管"同时跑几个"，**不管顺序和丢失**；Anthropic 原文靠的是"事件表就是事实来源，换个工人也能重试"。两者合起来才完整：键保证不并行，表保证不丢。我们的计划两样都写了，只是要清楚"并发键"本身不会替我们排队。

### 运行中途又来了新事件怎么办（计划里列为待店主定）

- Anthropic 给三选一：排到下一次、并进当前这次、打断重来。来源同上。
- Inngest 把这三种做成了现成选项：
  - singleton 的 skip：新事件被丢掉，保留正在跑的；
  - singleton 的 cancel：取消正在跑的，用新事件重来，**但已经做完的步骤不会撤销**；
  - debounce：等一段安静期，期间来的新事件替换旧的，最后**只处理最后一个事件，不是全部**。
  来源：[Inngest singleton](https://www.inngest.com/docs/guides/singleton)、[Inngest debounce](https://www.inngest.com/docs/guides/debounce)。
- 对我们有用的警告：debounce 会丢掉前面的事件，只留最后一个。员工连发三句话，Anthropic 的做法是"合并成一次处理"（保留全部），Inngest 的 debounce 是"只留最后一句"。这两个不能混着说。门店收件箱里"券 X 未关联"这类事件，更不能被后来的事件顶掉。

## 二、门店 agent 和员工 agent 怎么沟通

### 别人怎么做

- **OpenAI 的交接（handoff）**：新 agent 接手后"看得到前面的整段对话"，并且交接发生在同一次运行里。可以用输入过滤器裁剪。如果只想给下游结构化的输入、不转交对话，官方建议用"把 agent 当工具"。来源：[Agents SDK handoffs](https://openai.github.io/openai-agents-python/handoffs/)。
- **Anthropic 多 agent 研究系统**：主 agent 给子 agent 派活，每份任务要写清目标、输出格式、可用工具和来源、任务边界；写得含糊，子 agent 会重复搜或漏掉。主 agent 把计划存进记忆，上下文被截断也不丢。来源：[How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)。同一篇还写了：主 agent 同步等子 agent，慢的一个会拖住整体；多 agent 大约比聊天多花 15 倍的 token，只适合价值高的事。
- **Temporal 的 ambient agents 例子**：人给 agent 的反馈通过一个"经纪人"，用信号发过去，是"发了就不管"，不打断正在跑的工作；反馈排在一个队列里带"已处理"标记，agent 可以挑方便的时候再处理。来源：[Orchestrating ambient agents with Temporal](https://temporal.io/blog/orchestrating-ambient-agents-with-temporal)。
- **A2A 协议**：任务有编号，一组相关的任务和消息归在同一个上下文编号下。继续一件事要用同一个任务编号和上下文编号；任务一旦到了终态就不再接收新消息；要引用别的任务，用 `referenceTaskIds`。来源：[A2A 规范](https://a2a-protocol.org/latest/specification/)，第 3.4 节和 4.1.3 节。

### 和我们对照

- **一致**：我们"门店找员工的每件事是员工那边的一个新话题；员工回复接回这个话题"，和 A2A 的"一件事是一个任务、同一个任务编号接着聊、相关任务用引用连起来"是同一个形状。Anthropic 的"接着之前工作的事件接回原会话，其他事件新开"也一样。
- **一致，而且我们更稳**：OpenAI 的 handoff 会带整段对话过去。我们只传事件，不传对话。这在店里很重要：老板权限下门店运行查了全店的工资、提成，如果整段对话原样交给助理的 agent，助理就可能间接看到不该看的。官方文档本身也承认可以用"agent 当工具"那种不转交对话的做法，我们的"只发事件"更接近后者。
- **我们没想到：交代任务要写全。** Anthropic 的经验是派活要写清目标、输出格式、边界。门店往员工收件箱发的事件，现在写的是"券 X 未关联，请处理""顾客 Y 售后，需要你定：约返工还是退款"。这个还不够：员工的运行要知道**要答什么形式**（二选一？给时间？），**到什么时候**，**做不了怎么办**。事件的格式应该固定下这几项。
- **我们没想到：事件要带"这是哪次请求"的编号。** A2A 要求回复带任务编号、编号不匹配就拒收；Temporal 官方的审批示例里，信号处理器只在请求编号匹配当前等待的那一个时才记下决定（官方页的转述，见下一节来源）。我们的计划靠"会话"接回，但同一个员工同一个会话里可能同时有两件事（"券 X"和"顾客 Y"）在等答复，要靠请求编号区分。
- **我们没想到：任务到了终态就不再接收。** A2A 里终态的任务不再接收消息。对应到店里：门店已经因为"没人回"升级给了店长，小王晚了半小时才回"我弄好了"。这条回复该怎么办，计划里没有写。A2A 的做法给了思路：不往已关闭的事上硬接，而是作为新消息引用原来那件事。

## 三、权限：门店运行用谁的身份

### 别人怎么做

- **Copilot Studio 自主运行**：由事件触发的 agent 只能用"agent 作者（制作者）"的凭证。官方在发布前会弹警告，因为"用户可能借这个 agent 用作者的权限访问信息或让它做事"。官方也写了：触发器的消息内容可以带指令，且可能包含敏感信息，agent 可能通过动作把它输出到别处。官方建议缩小触发条件的范围。来源：[Event triggers overview](https://learn.microsoft.com/en-us/microsoft-copilot-studio/authoring-triggers-about)。
- **Slack**：两种令牌。机器人令牌"不绑任何人的身份，只绑应用"，所以安装它的人被停用，应用照样在。用户令牌代表一个工作区成员，"用它写，就像那个人自己做的"。来源：[Slack Tokens](https://docs.slack.dev/authentication/tokens/)。
- **Anthropic 原文**：运行"拿着用户的权限，并且按设计没人在旁边看"，所以每次工具调用要在"自己做、拦下、等批准"三者里选一个。来源同第一节。
- **Salesforce（没验证）**：只有搜索摘要，说"员工用的 agent 用登录者的身份、面向顾客的 agent 用一个专门的 agent 用户"。官方页没抓到，**不作为依据**，只作为"值得以后核实"的线索。

### 和我们对照

- **一致**：员工收件箱用员工本人的权限，对应 Anthropic 的"用户本人的权限"和 Slack 的"用户令牌"。
- **有风险的地方：门店运行用老板权限**，正是 Copilot Studio 警告的情形（没人在旁边、用的是比被服务的人更大的权限）。我们的设计已经做了两条缓解：门店运行不直接给员工写消息，改数据一律要人确认。官方资料说明这两条是对的方向。
- **我们没想到 A：事件里的文字会变成对门店运行的指令。** Copilot Studio 明说触发消息可以带指令。我们的"员工运行把老李的决定作为事件发回门店收件箱"，如果事件里带的是老李的自由文字，门店运行（老板权限）读到它，就等于员工的一句话进了老板权限的运行。更不用说顾客在留言里写了什么。Anthropic 原文也说，较深的检查（例如提示注入）要放到运行里做。所以发回门店的事件应当是**结构化的结论**（选项、日期、编号），不是原话；原话只能当"资料"，不能当"指令"。
- **我们没想到 B：门店运行的身份绑谁。** Slack 的做法是应用自己有身份，不依赖某个人，免得那个人离职后全断。我们写"老板角色"，应该在实现上就是**一个角色，不是某个人的账号**。换老板、老板被停用，门店的后台工作不应该停。

## 四、人机协作：挂起、等审批、恢复

### 别人怎么做

- **Anthropic 原文**：调用被挂起时，本次运行结束，会话停在这一步保存；用户的回答作为事件回来，开一次**新的运行**接着做；等待期间工人照常处理别的事。来源同第一节。
- **OpenAI Agents SDK**：需要批准时，运行暂停，结果里带着待批的调用。运行状态可以存进数据库或队列，晚点再恢复，要把定义的版本和保存的状态配套（版本变了可能读不出来）。**批准让调用可以执行，但不保证只执行一次**：存档前崩溃，用旧存档恢复可能重复外部动作，要用幂等键；结果不确定就记成"未知"，别自动重做。**审批人的身份要由应用自己的登录判断，不能信请求里带的，还要核对他有没有权限批这一次运行和这几个调用。** 来源：[Agents SDK human in the loop](https://openai.github.io/openai-agents-python/human_in_the_loop/)。
- **LangGraph**：恢复要有持久的存档点，并用同一个线程编号；恢复时"整个步骤从头再跑"，所以暂停之前的副作用要尽量幂等；一次恢复多个暂停要把每个暂停的编号对应到它的答复。来源：[LangGraph interrupts](https://docs.langchain.com/oss/python/langgraph/interrupts)。LangChain 的 HITL 页给出三种决定：批准、改参数后执行、拒绝并把理由退回给 agent。来源：[LangChain human-in-the-loop](https://docs.langchain.com/oss/python/langchain/human-in-the-loop)。
- **Microsoft Agent Framework**：向外发请求，等回复，回复按原请求自动送回发请求的那一步；存档点会保存待答的请求，恢复后重新发出。来源：[Workflows human-in-the-loop](https://learn.microsoft.com/en-us/agent-framework/workflows/human-in-the-loop)。
- **Temporal**：等审批可以等几小时、几天甚至无限期，等的时候不占计算资源；信号带着请求编号，只有匹配才记；等待有可配置的超时（示例默认 5 分钟），超时后流程自己带着"超时"结果结束；时间限制是持久的，系统中断也不会丢。来源：[Temporal HITL 示例](https://docs.temporal.io/ai-cookbook/human-in-the-loop-python)。
- **A2A**：任务有"需要输入"这个**中断状态**（和"需要认证"并列），客户端用同一个任务编号和上下文编号继续；服务端可以向客户端登记的地址推送状态变化，登记在任务结束前一直有效。来源：[A2A 规范](https://a2a-protocol.org/latest/specification/)。

### 和我们对照

- **一致**：我们把"草稿"当挂起、"草稿卡片"当交互卡片，确认作为事件回来，新的运行接着原会话。这和 Anthropic、OpenAI、LangGraph、Microsoft 四家都是同一个模型。
- **一致**：我们让门店任务进度记"等谁回复"，并配一个跟进定时器，和 Temporal 的持久定时器、Anthropic 的跟进定时器一个意思。
- **我们没想到 A：确认只能执行一次。** 现在"人确认后后端直接执行就结束了"，改成"确认作为事件回来，新的运行接着做"之后，会多出一个环节：事件重复送达、工人崩溃重试时，会不会把同一张草稿确认两次？OpenAI 和 LangGraph 都专门写了这个坑。草稿有编号和版本号，是天然的幂等键。**结果不确定（执行到一半崩了）要记成"未知"，不要自动重做**，这条是 OpenAI 原文的做法。
- **我们没想到 B：谁有资格确认。** OpenAI 要求审批人的身份和权限由应用自己核对，不能信请求里写的。我们的"门店运行起草，等有权限的人确认"，要核对的是**确认的这个人，对这张草稿里的每一项操作，自己有没有权限**。草稿是老板权限起草的，确认人是助理，不能因为草稿存在就能确认。
- **我们没想到 C：agent 已经升级了，旧的草稿和旧的请求怎么办。** Temporal 的审批示例是只接受"当前等待的那个请求编号"。对应：如果升级给了店长，小王原来那个请求要作废，不然两个人都能确认，会重复。
- **不同**：A2A 把"需要输入"做成**任务的一个状态**，调用方能随时查询。我们把"等谁回复"记在门店任务进度（文件）里。如果门店要在后台页面看"现在有哪些事卡在等谁"，状态最好是结构化的一列，不是藏在文字进度里。这条是推断，没有官方资料直接说。

## 五、升级和跟进：没人回怎么办

### 别人怎么做

- Anthropic 原文：跟进定时器是 agent 给自己定的"过一会儿看有没有回复"；定时器单独存，**从不自己启动运行**，只在到点时往收件箱写一个事件。
- Temporal 官方 HITL 示例：等审批带超时，超时后流程**自己带着超时结果往下走**，由流程决定后面做什么。
- Temporal 的 ambient agents 例子里没有升级，反馈是"发了不管"，也没写没人回怎么办（我读了，确实没有）。
- LangChain 对三种人机交互的描述里，提问和审核都是**等**，没有写升级。来源：[Introducing ambient agents](https://www.langchain.com/blog/introducing-ambient-agents)。

### 和我们对照

- **一致**：我们的"没回就到点再查一次，再提醒、换人、交给店长"，零件都在官方资料里（持久定时器、到点写事件、超时后的分支）。
- **官方没有的**：升级到谁、等多久、升几级，没有任何一家写过现成规则。这是**我们要定的业务规则**（已经列入门店设置的待定项），不要找资料抄。
- **我们没想到：提问和通知的升级强度不同。** LangChain 把"通知"（让用户知道，不要求动作）和"提问"（agent 缺信息，卡住了）分开。通知没人看，一般不用升级；提问没人答，才真的卡着门店的事。"券 X 未关联"其实是提问（要有人去关联），日结简报是通知。事件上标明是哪一种，决定要不要定跟进。

## 六、"收件箱页面"：员工在哪看到、怎么回

### 别人怎么做

- LangChain 的 Agent Inbox 被描述成"邮箱加客服工单系统的结合"，放着所有进行中的 agent 对话。每条中断有四种回应：接受、改参数、回复、忽略，哪些允许由配置开关控制。来源：[Introducing ambient agents](https://www.langchain.com/blog/introducing-ambient-agents)、[agent-inbox 仓库](https://github.com/langchain-ai/agent-inbox)。
- Slack：把应用的对话自动归到线程，用线程编号当会话的钥匙；需要用户动作时，建议用交互元素或链接。来源：[Slack developing agents](https://docs.slack.dev/ai/developing-agents)。

### 和我们对照

- **一致**：我们的"员工那边每件事是一个新话题"就是线程/工单的做法。
- **我们没想到**：
  1. 计划里写了"员工端没有地方看到发给自己的消息"，这是缺口。Agent Inbox 说明这个页面不只是"消息列表"，而是**一个待办加回复**，一条一条能处理。
  2. 回应只有"同意/回复"不够。Agent Inbox 有"忽略"，对应店里就是"我做不了/不是我的事/换人"。有了它，升级不用只靠超时，员工可以主动退回来，门店能立刻换人，不用等半小时。这是推断，资料给的是"有忽略这个选项"。

## 七、定时器和后台运行

- OpenAI 的后台模式（长任务）：可以轮询状态，也可以用 webhook 通知，webhook 处理器要先返回 2xx 再把活交给后台工人；后台模式即使关了存储也会临时存请求内容。来源：[OpenAI background mode](https://developers.openai.com/api/docs/guides/background)。和 Anthropic 的"处理器要快速应答，较深的检查放到运行里"是同一件事，我们的设计已经照做。
- Temporal 用平台自带的调度器代替自写的 cron 和睡眠循环，由服务端持久管理，"不靠代码里随手写的定时器"。来源同第二节。我们的"调度器只往收件箱写事件"是同一思路。
- 额外要注意（Copilot Studio 官方）：每个触发器消息按一次用户消息计费，定时触发太频繁会超配额。来源同第三节。我们用 DeepSeek，按量付费，每五分钟一次的周期检查如果每次都"醒来跑一次模型"，成本会比现在的"规则检查"高很多。门店周期任务应该先用规则判断"有没有事"，有事再醒。这是推断，官方资料只给了"频繁触发会超配额"。

## 对我们设计的建议改动

只列有来源支撑的。每条写了依据。

1. **给每次"门店找员工"的请求一个编号，回复必须带这个编号。** 回复和请求不匹配就不接。依据：A2A 规范 3.4.3（编号不匹配就拒收，用同一个任务编号继续）；Temporal HITL 示例（信号只在请求编号匹配时才记下）。
2. **请求被升级、改派、取消之后，原请求作废；原来那位晚来的回复不能直接生效，作为新消息引用原来那件事交给门店处理。** 依据：A2A（终态任务不再接收，新消息用 `referenceTaskIds` 引用旧任务）；Temporal（只接受当前等待的请求编号）。
3. **草稿确认要有幂等，确认事件重复送达、恢复重试不能执行两次；执行结果不确定的记成"未知"，不自动重做。** 依据：OpenAI Agents SDK human-in-the-loop 页；LangGraph interrupts 页（恢复时整个步骤重跑）。
4. **确认草稿时，核对"确认的这个人"对草稿里每一项操作自己有权限。** 依据：OpenAI Agents SDK human-in-the-loop 页（审批人身份由应用核对，不能信请求里的）。
5. **员工发回门店的事件用结构化结论（选项、日期、编号），不把员工或顾客的原话当指令交给老板权限的运行。** 依据：Copilot Studio 的触发器说明和数据保护提示（触发消息可带指令、可含敏感信息、用作者凭证有被借用的风险）；Anthropic 原文（深层检查如提示注入放在运行里做）。
6. **门店运行的身份写成"老板角色"这个角色本身，不绑某个人的账号。** 依据：Slack Tokens 页（应用自己的身份不绑任何人，安装人被停用也不受影响）。
7. **门店发给员工的事件格式固定：要答什么、什么形式（二选一/给时间）、什么时候要、做不了怎么办。** 依据：Anthropic 多 agent 研究系统（派活要有目标、输出格式、工具和任务边界，写含糊会重复或遗漏）。
8. **事件标明类型：通知、提问、审核。** 提问和审核要定跟进，通知一般不用。依据：LangChain, Introducing ambient agents（三种人机交互）。
9. **员工端做一个待办加回复的页面，回应至少有：同意、改后同意、回复、忽略（我做不了/换人）。** 依据：LangChain Agent Inbox 仓库的四种回应。"忽略"触发立即改派是我们的推断。
10. **"运行中途来事件怎么办"（计划里待定）：员工收件箱用"合并成一次处理"，门店收件箱用"排到下一次"，不要用取消重来。** 依据：Anthropic 原文（批处理、三选一）；Inngest 页（cancel 不撤销已完成步骤，debounce 只留最后一个事件，会丢前面的）。具体选哪个仍是店主的决定，这里只是资料支持的倾向。
11. **每个收件箱的"一个工人"用"键 = 收件箱编号"表达，并且把"不并行"和"不丢事件"当作两件事分别保证。** 依据：Inngest 并发指南（键隔离并发）；Anthropic 原文（事件表是事实来源，换工人重试）。
12. **待答的请求要能在重启后恢复，别只放在内存里。** 依据：Microsoft Agent Framework HITL 页（存档点保存待答请求，恢复后重新发出）；OpenAI 页（待处理的运行要存在服务端）。我们的草稿表和门店任务进度已经是持久的，这条主要是核对"等谁回复"也在持久存储里。

## 资料清单（实际读过的）

| 来源 | 页面 |
|---|---|
| Anthropic | [Building always-on agents](https://claude.com/resources/articles/building-always-on-agents)；[Multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)；[Effective harnesses for long-running agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)（只用来确认"换班靠进度文件交接"，和我们的任务进度做法一致） |
| OpenAI | [handoffs](https://openai.github.io/openai-agents-python/handoffs/)；[human in the loop](https://openai.github.io/openai-agents-python/human_in_the_loop/)；[background mode](https://developers.openai.com/api/docs/guides/background) |
| Google A2A | [规范](https://a2a-protocol.org/latest/specification/)（只读了前 10 万字符，涵盖任务状态、上下文编号、推送通知；后半部分没读） |
| LangChain | [ambient agents](https://www.langchain.com/blog/introducing-ambient-agents)；[agent-inbox](https://github.com/langchain-ai/agent-inbox)；[LangGraph interrupts](https://docs.langchain.com/oss/python/langgraph/interrupts)；[HITL](https://docs.langchain.com/oss/python/langchain/human-in-the-loop) |
| Microsoft | [Agent Framework Overview](https://learn.microsoft.com/en-us/agent-framework/overview/)；[Workflows HITL](https://learn.microsoft.com/en-us/agent-framework/workflows/human-in-the-loop)；[AutoGen topic and subscription](https://microsoft.github.io/autogen/stable/user-guide/core-user-guide/core-concepts/topic-and-subscription.html)；[Copilot Studio event triggers](https://learn.microsoft.com/en-us/microsoft-copilot-studio/authoring-triggers-about) |
| Temporal | [HITL 示例](https://docs.temporal.io/ai-cookbook/human-in-the-loop-python)；[Durable AI](https://docs.temporal.io/ai)；[Ambient agents 博客](https://temporal.io/blog/orchestrating-ambient-agents-with-temporal) |
| Inngest | [并发](https://www.inngest.com/docs/guides/concurrency)；[singleton](https://www.inngest.com/docs/guides/singleton)；[debounce](https://www.inngest.com/docs/guides/debounce) |
| Slack | [Tokens](https://docs.slack.dev/authentication/tokens/)；[Developing agents](https://docs.slack.dev/ai/developing-agents) |
| Salesforce | 没读到官方页，不作依据 |

说明：这些页面是用抓取工具读的，工具会对长页面做摘要，所以引文以页面原文为准；要放进正式文档的，请再对着原页确认一遍字句。
