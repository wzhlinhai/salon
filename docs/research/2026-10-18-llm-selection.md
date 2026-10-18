# 大模型选型调研（P0 只读 AI 查询）

- 对应 issue：#4「大模型选型」
- 调研日期：2026-10-18（以下所有价格、模型名、文档内容均为当天读取；价格经常变动，上线前须重新核对）
- 范围：阿里云百炼（通义千问）、DeepSeek 官方开放平台、火山方舟（豆包）。三项内容：工具调用、按 token 计价与月成本估算、中文语音识别。
- 不在范围内：合规要求（生成式 AI 服务备案、算法备案等）。按用户的范围调整，这部分没有调研，本文也不涉及。

## 标注约定

| 标记 | 含义 |
|---|---|
| **[已核实]** | 本人在当天读到了官方文档或定价页的原文，链接附后 |
| **[厂商宣称]** | 来自厂商自己的发布博客、模型卡或产品介绍，没有第三方复现，本文也没有自测 |
| **[未找到]** | 在官方一手资料中没有找到 |
| **[估算]** | 本文自己的假设和计算，不是任何厂商的数据 |

---

## 1. 工具调用（function calling）

### 1.1 能力和接口（都是 [已核实]）

| 项目 | 百炼（千问） | DeepSeek 官方 | 火山方舟（豆包） |
|---|---|---|---|
| 是否支持 | 支持。文档列出的支持模型有千问 Max/Plus/Flash 各系列，以及百炼托管的 deepseek-v4-pro、deepseek-v4.1-flash-1017、Kimi、GLM、MiniMax 等 [1] | `deepseek-flash`（当前是 DeepSeek-V4.1-Flash-1017）和 `deepseek-v4-pro` 都支持 Tool Calls [4] | 支持。推荐模型表中，doubao-seed-evolving、2.1-pro、2.1-lite、2.1-turbo 等都标有「工具调用」「结构化输出」[8] |
| 接口格式 | OpenAI 兼容 Chat Completions、Responses、DashScope [1] | OpenAI 兼容 Chat Completions、Anthropic 格式、Responses API [4][5] | Chat Completions、Responses API；文档中有 Curl/Python/Go/Java 示例 [8][9] |
| 严格模式（按 JSON Schema 约束参数） | Function Calling 文档没有提到 `strict` [1] | 有，**Beta**：要用 `base_url=https://api.deepseek.com/beta`，所有 function 都要设 `strict: true`；object 的所有属性都必须 required，且 `additionalProperties: false`；不支持 `minLength`/`maxLength`/`minItems`/`maxItems` 等 [5] | 有。文档「建议您始终启用严格模式」，前提条件指向「结构化输出(beta)」文档：所有字段都在 required 里，推荐 `additionalProperties: false` [8] |
| 并行工具调用 | `parallel_tool_calls` **默认 false**，需要手动打开 [1] | 文档未单独说明 | `parallel_tool_calls` **默认 true** [8] |
| 强制调用（`tool_choice`） | **限制**：「Qwen 系列模型暂不支持将 tool_choice 设为 "required"：非思考模式下 required 无法保证一定调用工具，思考模式下当前不支持」；思考模式下也不能设成 object 形式 [1] | 文档未单独说明 | 文档未单独说明 |
| 思考模式下的工具调用 | 支持，但有上面的 tool_choice 限制 [1] | 从 V3.2 起支持思考模式下的工具调用 [5] | 文档建议「函数调用场景下，关闭 thinking 会提高效率」[8] |
| 其他官方建议 | 每次传给主模型的工具「建议不超过 20 个」，工具多时先做路由/检索；工具默认只读，高权限操作要人工确认 [1] | Chat Completions 接口不支持在对话中间插入工具调用，要插入的话改用 Anthropic 或 Responses 格式 [5] | 新业务「推荐优选 Seed 2.0 系列」，看重速度可选 `doubao-seed-2-0-mini`；建议准备评测集测准确率；tools 定义里的字段顺序要保持固定 [8] |

百炼文档中「工具默认只读，写操作要人工确认」的建议 [1]，和本项目 ADR 0001「AI 权限按角色限定、P0 只读」的方向一致。

### 1.2 稳定性和中文业务场景表现：官方有没有评测

**结论：三家都没有公开「中文业务场景下工具调用」的专项评测。** 能找到的只有厂商自己发布的通用 Agent/工具基准分数，全部属于 **[厂商宣称]**：

| 来源 | 内容 |
|---|---|
| DeepSeek 更新日志 [6] | DeepSeek-V4-Flash-0731 的 Toolathlon-Verified 为 70.3；V4-Pro 正式版（0813）为 74.1。最新的 V4.1-Flash-1017 公布的是 Automation-Bench 60.5、Agents' Last Exam 35.4 等，这次没有给 Toolathlon 分数 |
| Qwen 官方模型卡（Hugging Face，开源权重 Qwen3.8-Flash-Next）[7] | Toolathlon Verified (Pass@1)：Qwen3.8-Flash-Next 73.5、Qwen3.7-Plus 50.6、DeepSeek-V4-Flash-0731 70.3。模型卡说 API 上的 `qwen3.8-flash` 是「based on Qwen3.8-Flash-Next」的正式版，所以 API 模型的分数并不等于这个开源模型的分数 |
| 字节 Seed 博客（Seed2.1 发布）[10] | 只有定性描述：在 xDailyBench、Doubao Multi-Turn Bench 上「steady performance」，在 Toolathlon、ClawBench 上「remains competitive」，**没有给数字** |

- 稳定性方面（比如 JSON 参数格式错误率、漏调用率），三家都 **[未找到]** 官方数据。官方给的都是「怎么用」：百炼说「大模型生成内容具有不确定性，可能选择错误的工具」[1]；方舟建议「准备评测集……观察准确率是否达到业务预期的上线准确率」[8]。
- 上面的基准都是英文或通用 Agent 任务，没有一项能说明「这周谁的烫染业绩最高」这类中文店务问题的效果。**要知道实际表现，只能自己建一个小评测集来测**（见第 5 节）。

---

## 2. 价格（按 token 计费）

单位都是 **元 / 百万 tokens**，在线推理，标准价，不含活动优惠。

### 2.1 价格表 [已核实]

**DeepSeek 官方** [3]（峰谷计价：北京时间工作日 9:00–12:00、14:00–18:00 是高峰，其余时间是空闲时段，空闲时段价格为高峰时段的一半）

| 模型 | 输入（缓存命中）高峰/空闲 | 输入（未命中）高峰/空闲 | 输出 高峰/空闲 | 上下文 |
|---|---|---|---|---|
| deepseek-flash（V4.1-Flash-1017） | 0.04 / 0.02 | 2 / 1 | 8 / 4 | 1M |
| deepseek-v4-pro（V4-Pro-0813） | 0.30 / 0.15 | 9.0 / 4.5 | 27.0 / 13.5 | 1M |

**阿里云百炼**，华北2（北京）地域 [2]（缓存命中价格另计，见 2.2）

| 模型 | 输入 | 输出 | 备注 |
|---|---|---|---|
| qwen3.8-flash | 0.8 | 2.7 | 不分输入长度档位（0–1M），思考和非思考模式同价 |
| qwen3.7-flash | 0.2（≤32K）/ 0.6（32K–256K）/ 1.2（256K–1M） | 0.8 / 2.4 / 4.8 | 按单次请求的输入长度分档 |
| qwen3.7-plus | 2（≤256K） | 8（≤256K） | 页面标注「原价……（限时8折）」，折扣是临时的 |
| qwen3.8-max | 12 | 36 | |
| 百炼托管的 deepseek-v4.1-flash | 忙时 2 / 闲时 1 | 忙时 8 / 闲时 4 | **百炼的闲时定义和 DeepSeek 官方不同**：22:00–次日 08:00 |
| 免费额度 | 新模型一般各送 100 万 tokens，自开通或模型发布起 90 天内有效，仅北京地域 | | |

**火山方舟** [11]（文档更新时间 2026-10-17）

| 模型 | 输入 | 缓存命中 | 输出 | 备注 |
|---|---|---|---|---|
| doubao-seed-2.1-pro | 6.00 | 1.20 | 30.00 | 输入长度 0–1024K |
| doubao-seed-2.1-turbo | 3.00 | 0.60 | 15.00 | 输入长度 0–256K |
| doubao-seed-2.1-lite | 0.80 | 0.16 | 2.70 | 输入长度 0–1024K |
| doubao-seed-2.0-mini | 0.2（≤32K） | 0.04 | 2.0（≤32K） | 分档计价 |
| doubao-seed-2.0-lite | 0.6（≤32K） | 0.12 | 3.6（≤32K） | 分档计价 |
| 方舟托管的 deepseek-v4-1-flash | 高峰 2.00 / 空闲 1.00 | 0.04 / 0.02 | 8.00 / 4.00 | 高峰时段与 DeepSeek 官方相同 |
| 缓存存储 | 0.017 元/百万 tokens/小时 | | | |

方舟每个模型的免费额度：一次搜索结果提到「每模型 50 万 tokens」，但我抓取的定价文档原文里没有这句话，所以算 **[未核实]**。

### 2.2 缓存 [已核实]

- DeepSeek：每个请求都会自动构建硬盘缓存，后续请求的前缀完整匹配时计为「缓存命中」[12]。命中价约为未命中价的 2%（flash 高峰 0.04 对 2）。
- 百炼：隐式缓存自动开启、无法关闭，命中部分「通常按输入 Token 标准单价的 20%」计费；显式缓存的创建按 125%、命中按 10% 计费，有效期 5 分钟 [13]。
- 方舟：价格表中单独列有缓存命中价和缓存存储价 [11]。

本项目每次请求都会带上固定的系统提示词和工具定义，这部分最适合命中缓存。下面的估算没有算缓存，所以是上限。

### 2.3 月成本粗估 **[估算]**

**假设**（都是我自己定的，不是厂商数据）：

- 一次自然语言查询是一个「模型 → 调工具 → 模型」的循环，平均调用模型 3 次。
- 每次调用的输入约 6K tokens（系统提示词 + 约 15 个只读工具的定义 + 对话 + 工具返回的数据），输出约 300 tokens（非思考模式）。
- 所以每次查询约 18K 输入、1K 输出。
- 低用量：只有店长/前台使用，每天 20 次，每月 600 次 → 1,080 万输入、60 万输出。
- 高用量：15 人每人每天 10 次，每月 4,500 次 → 8,100 万输入、450 万输出。
- 不算缓存折扣；DeepSeek 按高峰价算（偏保守）；百炼不算 8 折优惠。

| 模型 | 低用量（元/月） | 高用量（元/月） |
|---|---|---|
| qwen3.7-flash（≤32K 档） | 2.6 | 19.8 |
| doubao-seed-2.0-mini（≤32K 档） | 3.4 | 25.2 |
| qwen3.8-flash | 10.3 | 77.0 |
| doubao-seed-2.1-lite | 10.3 | 77.0 |
| deepseek-flash（高峰价） | 26.4 | 198.0 |
| qwen3.7-plus（原价） | 26.4 | 198.0 |
| doubao-seed-2.1-turbo | 41.4 | 310.5 |
| doubao-seed-2.1-pro | 82.8 | 621.0 |
| deepseek-v4-pro（高峰价） | 113.4 | 850.5 |
| qwen3.8-max | 151.2 | 1,134.0 |

计算方法：月成本 = 输入百万 tokens × 输入单价 + 输出百万 tokens × 输出单价。例如 deepseek-flash 低用量 = 10.8 × 2 + 0.6 × 8 = 26.4。

结论：在 6–15 人店的用量下，**轻量档模型每月几元到一两百元**；就算用旗舰档，每月也在一千元左右或以内。成本不会是决定因素，**工具调用的准确率才是**。另外几点：

- 打开思考模式会大幅增加输出 token（这个倍数没有官方数据），上表的数字可能要翻几倍。
- 系统提示词和工具定义如果大部分命中缓存，输入成本可以降一半以上。比如 DeepSeek flash，命中价只有未命中价的 2%。

---

## 3. 中文语音识别（为以后的语音开单做准备）

### 3.1 有没有可用的 API

| 厂商 | 结论 |
|---|---|
| DeepSeek | **[未找到]**：官方 API 文档的模型列表只有 `deepseek-flash` 和 `deepseek-v4-pro` 两个文本模型（flash 支持图像理解），没有语音识别 API [3] |
| 百炼 | 有。选型页推荐：实时短语音（如语音消息）用 `qwen-audio-3.1-asr-flash-message`，持续转写用 `qwen-audio-3.1-asr-flash-streaming`，录音文件用 `qwen-audio-3.1-asr-flash-filetrans`；另外还有 Fun-ASR、Qwen3-ASR、Paraformer 系列 [14] |
| 火山（豆包语音） | 有。「豆包流式语音识别模型 2.0」「豆包录音文件识别模型 2.0」[15][16] |

### 3.2 粤语支持 [已核实]

- **百炼**：qwen-audio-3.1/3.0 ASR 各型号、fun-asr-realtime、fun-asr、fun-asr-flash 支持的中文都写明包括「普通话、粤语、吴语、闽南语、客家话……」以及各地口音；Qwen3-ASR 系列支持「普通话、四川话、闽南语、吴语、粤语」；paraformer-v2 和 paraformer-realtime-v2 也列有粤语 [14]。
- **豆包**：产品简介的功能表里，「流式输入」模式的方言部分写的是「方言文本输出：粤语、四川、陕西……」；但「**双向流式只支持中英文、二遍支持中英文及方言**」[15]。也就是说，如果要边说边出字并识别粤语，需要确认用的是哪种模式。

### 3.3 店内噪音

- **豆包** **[厂商宣称]**：产品简介写「口音错误率降低60%，噪声和背景人声下降30%-50%」[15]。没有说明对比基线、测试集，也没说是哪个版本。
- **百炼** **[未找到]**：没有找到噪声鲁棒性的指标。选型页只提到客户端如果需要「实时双工的降噪与回声消除」，可以参考 Realtime API 的 AOQ 协议 [14]。
- 两家都提供热词和上下文提示（百炼 qwen-audio-3.1 系列 [14]；豆包支持平台级和请求级热词纠错，2.0 还支持图片上下文 [15]）。可以把服务项目名（烫、染、护理、技师名）配成热词。
- 吹风机、音乐背景下的实际效果，**只能在店里录样本自己测**。

### 3.4 价格 [已核实]

| 模型 | 价格 | 免费额度 |
|---|---|---|
| 百炼 fun-asr-realtime | 0.00033 元/秒（≈1.19 元/小时）[2] | 10 小时，开通后 90 天内 |
| 百炼 qwen3-asr-flash-realtime | 0.00033 元/秒 [2] | 10 小时 |
| 百炼 qwen-audio-3.0-asr-flash-streaming | 0.00033 元/秒 [2] | 10 小时 |
| 百炼 qwen-audio-3.1-asr-flash-streaming / -message | 按 token：输入 6 元/百万，输出 4.5 元/百万 [2] | 100 万 tokens |
| 百炼 paraformer-realtime-v2 | 0.00024 元/秒（≈0.86 元/小时）[2] | 每月 10 小时，每月 1 日发放，有效期 1 个月 |
| 豆包流式语音识别模型 2.0 | 1 元/小时（后付费）；资源包 30 小时 28 元起 [16] | 20 小时，有效期半年 [17] |
| 豆包录音文件识别模型 2.0 | 0.8 元/小时 [16] | 20 小时，有效期半年 [17] |

qwen-audio-3.1 系列按 token 计费。语音时长和 token 怎么换算，ASR 页面没有写清楚（定价页给的「秒数 × 12.5」是实时语音对话模型的规则），所以 **[未核实]**，下面不估它的成本。

**[估算]** 语音开单：假设每单 15 秒、每天 100–300 单，每月约 12.5–37.5 小时。按 fun-asr-realtime 算约 15–45 元/月，按豆包流式 2.0 算约 12.5–37.5 元/月。

---

## 4. 已核实 / 厂商宣称 / 未找到 汇总

**已核实（官方文档原文）**

- 三家的工具调用接口、strict 模式现状、`parallel_tool_calls` 默认值、Qwen 的 `tool_choice=required` 限制（第 1.1 节）。
- 三家的文本模型价格、峰谷时段、缓存规则、百炼免费额度（第 2 节）。
- 百炼和豆包 ASR 的语种和粤语支持、价格、免费额度；DeepSeek 没有 ASR（第 3 节）。

**厂商宣称（未经独立验证）**

- 各家的 Toolathlon、Automation-Bench 等 Agent 基准分数；Seed2.1「remains competitive」。
- 豆包 ASR「噪声和背景人声下降30%-50%」。

**未找到**

- 任何一家关于中文业务场景工具调用的专项评测，以及工具调用稳定性的官方指标（如格式错误率）。
- 百炼 Function Calling 文档中的 strict 模式说明。
- 百炼 ASR 的噪声鲁棒性指标。
- 方舟「每模型 50 万 tokens 免费额度」的原文出处。
- qwen-audio-3.1 ASR 的语音时长到 token 的换算规则。

---

## 5. 建议

1. **P0 默认选阿里云百炼**，起步模型用 `qwen3.8-flash`，效果不够再升级到 `qwen3.7-plus`。理由：
   - 同一个账号、同一套 OpenAI 兼容接口，除了千问还能直接调用百炼托管的 deepseek-v4.1-flash 和 v4-pro，方便横向对比；
   - 以后做语音开单时，同一平台就有写明支持粤语的实时 ASR（fun-asr-realtime / qwen-audio-3.1）；
   - 成本在每月百元以内。
2. **注意 Qwen 的限制**：`tool_choice` 不能设为 `"required"`，并行调用默认关闭。P0 的只读查询本来就应该让模型自己判断要不要调工具（`auto`），影响不大；但如果后端需要「必须走工具」，要在 Go 侧自己兜底，比如没有调工具就重试或拒答。
3. **在 Go 后端保持与厂商无关**：只用 OpenAI 兼容的 Chat Completions 加 tools 协议，不用某一家的私有特性。三家都支持这个协议，换厂商只需改 base_url 和模型名。
4. **决定性一步是自建评测**：拿 30–50 条真实的中文店务问题（业绩排名、预约冲突、会员余额等），配上标准答案和期望调用的工具及参数，在 `qwen3.8-flash`、`deepseek-flash`、`doubao-seed-2.1-lite` 三个模型上跑，比较工具选择准确率、参数准确率和端到端正确率。一共只需要几元钱。如果 DeepSeek 或豆包明显更好，可以直接换，因为接口相同。
5. 语音开单不进 P0。到时候在店里实际录音（含吹风机噪声和粤语），比较百炼 fun-asr-realtime 和豆包流式 2.0。

### 未验证的假设

- 月成本估算依赖的「每次查询 3 次模型调用、每次约 6K 输入 / 300 输出、每天 20–150 次查询」是我的假设，P0 上线后要用真实日志校正。
- 估算按非思考模式计算。如果评测发现必须开思考模式才够准，成本会成倍增加，但量级应该仍在每月千元以内（这一点也未验证）。
- 假设 P0 的工具数不超过 20 个（百炼的建议上限）。超过就需要做工具路由。
- 厂商基准分数不能代表中文店务场景，本文没有做任何实测。
- 价格是 2026-10-18 当天的标价，百炼 qwen3.7-plus 的 8 折是限时优惠。

---

## 参考来源（均读取于 2026-10-18）

1. 百炼：如何通过 Function Calling 实现工具调用 — https://help.aliyun.com/zh/model-studio/qwen-function-calling
2. 百炼：模型调用价格 — https://help.aliyun.com/zh/model-studio/model-pricing
3. DeepSeek：模型 & 价格 — https://api-docs.deepseek.com/zh-cn/quick_start/pricing
4. DeepSeek：首次调用 API — https://api-docs.deepseek.com/
5. DeepSeek：Tool Calls — https://api-docs.deepseek.com/zh-cn/guides/tool_calls
6. DeepSeek：更新日志 — https://api-docs.deepseek.com/zh-cn/updates
7. Qwen3.8-Flash-Next 模型卡（Qwen 官方 Hugging Face）— https://huggingface.co/Qwen/Qwen3.8-Flash-Next
8. 火山方舟：Function Calling（函数调用）（文档更新时间 2026-10-09）— https://docs.volcengine.com/docs/ark/function-calling?lang=zh
9. 火山方舟：工具调用（Responses API）— https://www.volcengine.com/docs/82379/1958524
10. ByteDance Seed：Seed2.1 Officially Released — https://seed.bytedance.com/en/blog/seed2-1-officially-released-advancing-ai-productivity
11. 火山方舟：模型价格（文档更新时间 2026-10-17）— https://www.volcengine.com/docs/82379/1544106
12. DeepSeek：上下文硬盘缓存 — https://api-docs.deepseek.com/zh-cn/guides/kv_cache
13. 百炼：上下文缓存 — https://help.aliyun.com/zh/model-studio/context-cache
14. 百炼：语音识别（选型）— https://help.aliyun.com/zh/model-studio/asr-model
15. 豆包语音：产品简介（文档更新时间 2026-03-19）— https://www.volcengine.com/docs/6561/1354871
16. 豆包语音：计费说明（文档更新时间 2026-09-29）— https://www.volcengine.com/docs/6561/1359370
17. 豆包语音：计费概述（试用额度）— https://www.volcengine.com/docs/6561/1359369
