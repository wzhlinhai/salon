# AI 评测报告

这里存放 AI 对话评测的真实模型报告（P0-32，ADR-0007）。报告由 `backend/cmd/aieval run --out` 生成，换模型、改提示词或改工具集前后各跑一次，再用 `aieval compare` 比较。

- 文件名建议：`<日期>-<模型名>-before.json`、`<日期>-<模型名>-after.json`。
- 报告只包含模型名、base URL host、提示词指纹、git commit、用例文件哈希和逐条结果，不包含 API 密钥。
- 用例和固定门店在 `backend/internal/ai/eval/cases.json`（40 条）。

## 已有报告

| 文件 | commit | 模型 | 总体通过 | 说明 |
|---|---|---|---|---|
| `20261114-deepseek-v4.1-flash-v1.json` | a5f74ba | deepseek-v4.1-flash（OpenCode Go） | 57.5%（23/40） | 首次真实基线，暴露评分、用例和两个产品缺陷（#210） |
| `20261115-deepseek-v4.1-flash-v2.json` | 5a1f132 | 同上 | 82.0%（32/39，另 1 条已知缺口） | #210 校准之后；用例文件已换，与 v1 比较须加 `--allow-different-cases` |
| `20261115-deepseek-v4.1-flash-v3.json` | 68fca6b | 同上 | 84.6%（33/39） | #215 第 2 轮之后；回答 94.9%。暴露 set_order_item_staff 整体替换未说明（第 3 轮修） |
| `20261115-deepseek-v4.1-flash-v4.json` | 22f2157 | 同上 | 92.3%（36/39） | #215 第 3 轮之后；改助理时模型照原样带上大工（新工具说明生效） |

v2 剩下的 7 条失败中，5 条是评分误判（日期写法、"1 人"这类说法、禁止词出现在否定句里、拒绝措辞），2 条是模型行为（没有工具时没明说做不了；用草稿 id 调 preview_checkout），见 #215。
- 比较时用例文件哈希必须一致；确实换了用例才加 `--allow-different-cases`，并在报告或提交说明里写清原因。

v4 剩下 3 条失败都不是产品缺陷，暂不处理，下次调整评测时可一并考虑：
- read-revenue-today：网关这次返回"模型不可用"，属于临时故障。
- refuse-assistant-revenue：模型回答"查不到 / 看不到"，拒绝是对的，但关键词表里只有"查不了"。
- draft-create-appointment：固定门店数据里王姐明天 12:00 已有预约（供改约、取消用例使用），模型先问是改约还是新增，这是合理追问；同一用例在 v3 通过。
