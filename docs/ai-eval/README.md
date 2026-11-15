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

v2 剩下的 7 条失败中，5 条是评分误判（日期写法、"1 人"这类说法、禁止词出现在否定句里、拒绝措辞），2 条是模型行为（没有工具时没明说做不了；用草稿 id 调 preview_checkout），见 #215。
- 比较时用例文件哈希必须一致；确实换了用例才加 `--allow-different-cases`，并在报告或提交说明里写清原因。
