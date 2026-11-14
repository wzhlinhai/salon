# AI 评测报告

这里存放 AI 对话评测的真实模型报告（P0-32，ADR-0007）。报告由 `backend/cmd/aieval run --out` 生成，换模型、改提示词或改工具集前后各跑一次，再用 `aieval compare` 比较。

- 文件名建议：`<日期>-<模型名>-before.json`、`<日期>-<模型名>-after.json`。
- 报告只包含模型名、base URL host、提示词指纹、git commit、用例文件哈希和逐条结果，不包含 API 密钥。
- 用例和固定门店在 `backend/internal/ai/eval/cases.json`（40 条）；当前真实基线由后续任务补充。
- 比较时用例文件哈希必须一致；确实换了用例才加 `--allow-different-cases`，并在报告或提交说明里写清原因。
