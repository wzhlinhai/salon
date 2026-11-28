# AI 评测报告

这里存放 AI 评测的真实模型报告（P0-32、ADR-0007、ADR-0015）。对话评测报告由 `backend/cmd/aieval run --out` 生成；门店 AI 的分类器评测和运行评测由 `backend/cmd/aieval classifier`、`backend/cmd/aieval agent` 生成。换模型、改提示词或改工具集前后各跑一次，对话评测再用 `aieval compare` 比较。

- 文件名建议：`<日期>-<模型名>-before.json`、`<日期>-<模型名>-after.json`。
- 报告只包含模型名、base URL host、提示词指纹、git commit、用例文件哈希和逐条结果，不包含 API 密钥。
- 用例和固定门店在 `backend/internal/ai/eval/cases.json`（40 条）。
- 分类器用例在 `backend/internal/storeai/testdata/classifier_cases.json`，门店运行用例在 `backend/internal/storeai/testdata/agent_cases.json`；两份 cassette 也在同一目录。

## 已有报告

| 文件 | commit | 模型 | 总体通过 | 说明 |
|---|---|---|---|---|
| `20261114-deepseek-v4.1-flash-v1.json` | a5f74ba | deepseek-v4.1-flash（OpenCode Go） | 57.5%（23/40） | 首次真实基线，暴露评分、用例和两个产品缺陷（#210） |
| `20261115-deepseek-v4.1-flash-v2.json` | 5a1f132 | 同上 | 82.0%（32/39，另 1 条已知缺口） | #210 校准之后；用例文件已换，与 v1 比较须加 `--allow-different-cases` |
| `20261115-deepseek-v4.1-flash-v3.json` | 68fca6b | 同上 | 84.6%（33/39） | #215 第 2 轮之后；回答 94.9%。暴露 set_order_item_staff 整体替换未说明（第 3 轮修） |
| `20261115-deepseek-v4.1-flash-v4.json` | 22f2157 | 同上 | 92.3%（36/39） | #215 第 3 轮之后；改助理时模型照原样带上大工（新工具说明生效） |
| `20261127-deepseek-v4.1-flash-classifier.json` | #396 B2 修复前 | deepseek-v4.1-flash（OpenCode Go） | 84.2%（16/19） | #396 B2 修复前的分类器真实基线；漏拦已撤回重复、完整姓名、已关联还标 now |
| `20261127-deepseek-v4.1-flash-agent.json` | #396 B2 修复前 | 同上 | 16.7%（1/6） | #396 B2 修复前的门店运行基线，按当时的严格序列口径；多数失败是额外只读调用或员工没走 request 流程 |
| `20261127-deepseek-v4.1-flash-classifier-v2.json` | #396 B2 修复后 | 同上 | 95.0%（19/20） | 剩下 duplicate-withdrawn-allowed：抖音券未关联、离撤销不到 1 小时却标 daily，模型要求改 now，判得合理，下次调用例时改档位 |
| `20261128-deepseek-v4.1-flash-agent.json` | #396 B2 修复后 | 同上 | 100%（6/6） | 新打分口径：忽略只读调用、写工具必须成功；同日前一次跑有 4 条网关返回「AI 暂时不可用」，已丢弃重跑 |

v2 剩下的 7 条失败中，5 条是评分误判（日期写法、"1 人"这类说法、禁止词出现在否定句里、拒绝措辞），2 条是模型行为（没有工具时没明说做不了；用草稿 id 调 preview_checkout），见 #215。
- 比较时用例文件哈希必须一致；确实换了用例才加 `--allow-different-cases`，并在报告或提交说明里写清原因。

v4 剩下 3 条失败都不是产品缺陷，暂不处理，下次调整评测时可一并考虑：
- read-revenue-today：网关这次返回"模型不可用"，属于临时故障。
- refuse-assistant-revenue：模型回答"查不到 / 看不到"，拒绝是对的，但关键词表里只有"查不了"。
- draft-create-appointment：固定门店数据里王姐明天 12:00 已有预约（供改约、取消用例使用），模型先问是改约还是新增，这是合理追问；同一用例在 v3 通过。

## 门店 AI 评测

分类器和门店运行两套评测共用 `AI_API_KEY`、`AI_BASE_URL`、`AI_MODEL`、`AI_EXTRA_HEADERS`，报告只记录模型名、base URL host、用例文件哈希和逐条结果，不记录密钥、完整地址或扩展头。

```bash
cd backend
# 分类器：真实模型跑一遍，并记录 cassette
AI_API_KEY=... AI_MODEL=... go run ./cmd/aieval classifier --out /tmp/classifier.json --record /tmp/classifier.cassette.json

# 门店运行：真实模型跑一遍，并记录 cassette
AI_API_KEY=... AI_MODEL=... go run ./cmd/aieval agent --out /tmp/agent.json --record /tmp/agent.cassette.json

# 离线重放
go run ./cmd/aieval classifier --replay internal/storeai/testdata/classifier.cassette.json
go run ./cmd/aieval agent --replay internal/storeai/testdata/agent.cassette.json

# 更新 CI 基线 cassette
go test ./internal/storeai -run TestClassifierOracleScoresPerfect -update-cassette
go test ./internal/storeai -run TestAgentOracleScoresPerfect -update-cassette
```

`classifier` 报告每条用例的期望/实际 decision、来源、原因和是否命中；`agent` 报告每条用例的期望/实际工具调用序列和是否命中。模型调用的 system/user 消息必须与 cassette 一致；用例或提示词改了以后，按上面的 `-update-cassette` 命令重录。

手机号取舍：分类器支持可选 `+86`/`0086`/`86` 前缀（前缀与号码之间可有一个空格或横线）和 3-4-4 分隔写法；11 位、以 `1[3-9]` 开头且前后不是数字的独立数字串一律当手机号。宁可误杀（金额、单号、券码也可能被拦），避免把真实手机号发给外部模型；发给分类器模型的 `existing_reminders[].body` 会先把手机号脱敏成 `[手机号]`。

门店运行打分口径：只按顺序比对门店 AI 自己的写工具（`schedule_follow_up`、`create_request`、`hand_over`、`cancel_request`、`remind_me`、`finish`），中间夹的只读调用（Registry 的 Read + AIDirect）忽略；出现期望之外的写工具判失败，并且每个被比对的写工具调用都要有成功的 tool 结果（按 `tool_call_id` 配对，不是错误文本）。因此报告里的 `actual` 只列写工具。`classifier_cases.json` 里的完整姓名用例使用虚构姓名，跟进时刻正好等于撤销时限的用例按「晚于才拦」放行。
