# 美管加存档导入工具（ticket #221 第 1 步）

只用 Python 标准库，把美管加只读存档变成：

1. 门店初始化数据 `catalog.json`（项目 item、卡类型 card type），通过后端 HTTP API 幂等写入；
2. 本店 `dataio` 接受的中间 Excel `import.xlsx`，上传预览、按需提交；
3. 提取报告 `extract-report.json` 和核对报告 `import-report.md`。

## 隐私

工具只以 `file:...?mode=ro` 只读打开传入的 SQLite，不复制、不修改。报告只写条数、金额、卡种/项目名称、工作表名和行号；不写顾客姓名、手机号、来源顾客 ID 或来源卡号。错误行按“工作表 + 行号”举例，不输出接口返回的 `name`、`phone`、`source_key`。测试和本地冒烟只用 `make-fixture` 生成的合成存档。

## 命令

在 `scripts/` 下运行：

```bash
python3 -m mgj extract --db <archive.sqlite> --out <dir>
python3 -m mgj seed --api http://localhost:8080 --account boss --password owner-password --catalog <dir>/catalog.json
python3 -m mgj load --api http://localhost:8080 --account boss --password owner-password --xlsx <dir>/import.xlsx --out <dir> [--commit]
python3 -m mgj make-fixture <fixture.sqlite> [--force] [--orphan] [--no-independent]
```

`extract` 写 `<dir>/catalog.json`、`<dir>/import.xlsx`、`<dir>/extract-report.json`。`seed` 打印 `{created, existing, conflicts}`。`load` 写 `<dir>/import-report.md`。

## 来源表与列

- `archive_members`：`customer_id, name, mobile, gender, current_points`。
- `archive_member_cards`：`customer_id, card_id, card_name, card_balance, gifted_balance, card_state`。
- `archive_member_packages`：`customer_id, package_instance_id, item_name, remaining_quantity, remaining_amount, valid_until, total_quantity`。
- `ep_api_shair_service_item_get`（`is_latest_copy=1`）：`itemId, name, serviceItemPrice, status`。
- `ep_discount_rule_get_card_type_and_ori_discount`（`is_latest_copy=1`）：`cardTypeName, discount`。
- `ep_shop_proposal_card_types`（可缺）：`cardtypename, discount`，按 distinct 读取，不用 `is_latest_copy`。
- 可选 `ep_member_get_mem_count`：只取 `value`，有 `is_latest_copy` 时只取 1。
- 可选 `ep_report_shop_treat_surplus`：只取 `套餐金总额, 普通项目总额, 普通项目次数, 赠送金总额, 赠送项目次数, 项目结余总额, 项目结余次数`，有 `is_latest_copy` 时只取 1。
- 可选 `business_card_daily_summary`：只取 `summary_date, ending_card_balance, ending_gift_balance`，取 `summary_date` 最大的一行。
- 其他 `ep_*` 视图/列的元数据（`capture_id`、`capture_at`、`phone_like` 等）一律不进入报告。

## 目录决策

### 项目

- 来源项目表的最新副本生成项目：`kind=service`、`discountable=true`、无岗位、无级别价、无会员价（第 2 步再加）。
- 项目名截断到 40 个字符；按截断后的名称去重，先出现的 `itemId` 生效。完全相同的重复名称计入 `duplicate_item_name`；同名但价格不同直接中止提取并指出项目名。
- 套餐引用了项目表里没有的 `itemId` 时，用 `item_name` 第一个 `-` 之后的名称创建项目，价格 `0.00`，每个新项目计一次 `item_from_package_only`。
- `serviceItemPrice` 是 JSON 文本，价格取键 `price`；缺价、负数、NaN/inf 或无法解析时按 `0.00` 并计 `invalid_item_price`，不中止提取。

### 卡类型

- 储值卡卡类型名截断到 40 个字符；截断后两个不同原名相同则中止提取并指出截断名。
- `card_state=正常` 的卡按原样导入；非 `正常` 的卡在本金或赠送金大于 0 时也导入并计 `inactive_card_imported_with_balance`，两项都为 0 才跳过并计 `inactive_card`。
- 每个不同的 `card_name` 生成一个 `stored_value` 卡类型，`validity_kind=none`。
- 折扣率来源依次为：`ep_discount_rule_get_card_type_and_ori_discount`（最新副本）、`ep_shop_proposal_card_types`（distinct）、名称推导。名称匹配 `^([1-9](\.[0-9])?)(折|特惠)?$` 时从开头数字推导并计 `discount_from_name`；仍没有才不设折扣率并计 `card_type_without_discount_rule`。
- 美管加折扣单位是“折”：`5` → 折扣率 `50.00`，`8.5` → `85.00`；`0`、空、`>=10` 都按不设折扣率处理。两个来源或同一来源内同名不同折扣率时中止提取并指出卡类型名。
- 每个有导入套餐的项目生成一个次卡卡类型 `历史次卡-<项目名>`（截断 40 字符），`kind=count`、`validity_kind=none`、一个项目、`paid_count=1`、`gift_count=0`。
- 次卡单次金额取该项目所有导入套餐实例 `remaining_amount / remaining_quantity` 的原始比值中位数，最后只按 `ROUND_HALF_UP` 量化一次到分；没有正的剩余金额时回退到项目价格。

### 中间 Excel

- `顾客`：每条 `archive_members` 一行。手机号先从浮点/文本 `.0` 还原为整数数字，再去掉全部空白；必须匹配 `^1[0-9]{10}$`，否则写空并计 `invalid_phone_blanked`；顾客仍保留。性别只接受 `男`/`女`，其他写空。积分必须是整数，否则写空并计 `invalid_points`。
- `储值卡`：导入的卡一行，`来源卡号=card_id`，金额为 `card_balance`/`gifted_balance`，到期日空。空金额按 0；负金额计 `negative_balance` 并跳过；非空且无法解析、NaN/inf 计 `invalid_balance` 并跳过。顾客的 `current_points` 只写在按 `card_id` 排序的第一张导入卡上；有积分但没有导入卡的顾客计 `points_without_card`，积分合计写入报告的 `points_without_card_total`。
- 顾客不存在于 `archive_members` 的卡仍写出；后端会在预览/提交时报告该行错误。
- `次卡套餐`：`remaining_quantity > 0` 的套餐一行，`来源卡号='P'+package_instance_id`，`卡种=历史次卡-<项目名>`，`项目=项目名`，剩余赠送次数 `0`。数量必须是整数，否则计 `invalid_quantity` 并跳过。
- `valid_until` 为空时写空；非空时必须是 `YYYY-MM-DD`，可带空格或 `T` 分隔的时间部分，否则写空并计 `invalid_date`。
- `remaining_quantity <= 0` 计 `package_used_up`。
- 每张工作表超过 20000 条数据行或生成的 `import.xlsx` 超过 8 MiB 时中止提取并给出明确错误。
- `汇总` 是实际写入文件的行数和金额合计：顾客数、储值卡数、本金合计、赠送金合计、次卡套餐卡数。

金额一律用 `decimal.Decimal` 从数据库文本/数字表示读取，量化到 `0.01`（`ROUND_HALF_UP`），不用浮点做合计。

## 提取报告原因

`extract-report.json` 的 `skipped` 只列非零项，可能包括：

`inactive_card`、`inactive_card_imported_with_balance`、`package_used_up`、`invalid_phone_blanked`、`item_from_package_only`、`card_type_without_discount_rule`、`discount_from_name`、`points_without_card`、`duplicate_item_name`、`invalid_item_price`、`invalid_discount`、`empty_card_name`、`item_without_name`、`empty_package_item_name`、`invalid_points`、`invalid_quantity`、`invalid_balance`、`negative_balance`、`invalid_date`。

## seed

- 登录 `POST /api/auth/login`，之后用 `Authorization: Bearer <token>`。
- 用 `list_items`、`list_card_types` 按名称精确查找；只创建缺失项，不更新已有项。
- 同名项目的 `kind`、`price`、`discountable` 不一致，或同名卡类型的 `kind`、`validity_kind`、折扣率 `discount_percent`、项目/次数/单次金额不一致时，记入 `conflicts`，不调用 `update_*`。
- 第二次运行不应再创建任何东西。

## load

- `POST /api/import/preview`（multipart 字段 `file`），再 `get_import_batch`；带 `--commit` 时调用 `commit_import` 后再次 `get_import_batch`。
- `import-report.md` 含批次 ID/状态、声明/文件/实际写入三组合计、两个差额块、按错误文本分组的错误行（最多 10 个“工作表 + 行号”例子）、提取跳过项、`与美管加门店余额对比`、`与美管加项目结余次数对比`，以及 `mgj_reported` 独立统计。
- 门店余额对比用 `business_card_daily_summary` 最新快照的 `ending_card_balance`/`ending_gift_balance` 对比实际写入本金/赠送金，差额按 Decimal 两位小数显示；项目结余次数对比用 `ep_report_shop_treat_surplus` 的 `项目结余次数`（并列出普通项目次数、赠送项目次数）对比 `package_remaining_total`。来源缺失时显示 `not available`。
- 报告不含任何顾客姓名或手机号。

## 确定性与测试

`import.xlsx` 用 `zipfile` 写最小 OOXML，单元格都是 inline string，ZIP 时间戳固定为 1980-01-01，条目不压缩（ZIP_STORED），两个相同输入产生完全相同的字节。

```bash
python3 -m unittest discover -s scripts -p 'test_*.py'
go test ./internal/dataio/...
```

`scripts/test_mgj.py` 用合成存档覆盖手机号、积分、折扣、中位数、套餐拆分、孤儿卡、确定性和 HTTP 请求；`backend/internal/dataio/mgj_import_test.go` 用 `parseWorkbook` 读取 `testdata/mgj-sample.xlsx`。Python 测试会重新生成该样本并断言字节一致。

`make-fixture` 默认不覆盖已存在的路径，必须显式加 `--force`；这样不会误删上一次的 fixture。
