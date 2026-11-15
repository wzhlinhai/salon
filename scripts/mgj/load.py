"""上传中间 Excel，预览（可选提交），并写核对报告。"""

import json
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP
from pathlib import Path

from .client import Client

TOTAL_FIELDS = [
    ("customers", "顾客数"),
    ("stored_value_cards", "储值卡数"),
    ("principal", "本金合计"),
    ("bonus", "赠送金合计"),
    ("count_package_cards", "次卡套餐卡数"),
]


def _values(totals):
    return {key: totals.get(key, "0.00" if key in ("principal", "bonus") else 0) for key, _ in TOTAL_FIELDS}


def _totals_table(declared, file_totals, written):
    lines = ["| 项目 | 声明（汇总） | 文件有效行 | 实际写入 |", "|---|---:|---:|---:|"]
    for key, label in TOTAL_FIELDS:
        lines.append(
            f"| {label} | {declared.get(key, '')} | {file_totals.get(key, '')} | {written.get(key, '')} |"
        )
    return lines


def _difference_table(title, totals):
    lines = [f"### {title}", "", "| 项目 | 差额 |", "|---|---:|"]
    for key, label in TOTAL_FIELDS:
        lines.append(f"| {label} | {totals.get(key, '')} |")
    return lines


def _error_section(rows):
    groups = {}
    for row in rows:
        error = row.get("error")
        if not error:
            continue
        groups.setdefault(error, []).append((row.get("sheet", ""), row.get("row_number", 0)))
    lines = ["## 错误行", ""]
    if not groups:
        lines.append("无错误行。")
        return lines
    for error in sorted(groups, key=lambda value: (-len(groups[value]), value)):
        lines.append(f"- `{error}`：{len(groups[error])} 行")
        examples = sorted(groups[error], key=lambda value: (str(value[0]), int(value[1] or 0)))[:10]
        for sheet, row_number in examples:
            lines.append(f"  - {sheet} 第 {row_number} 行")
    return lines


def _load_extract_report(xlsx_path, out_dir):
    for path in (Path(out_dir) / "extract-report.json", Path(xlsx_path).parent / "extract-report.json"):
        if path.is_file():
            with path.open(encoding="utf-8") as source:
                return json.load(source)
    return None


def _extract_section(extract_report):
    lines = ["## 提取跳过", ""]
    if extract_report is None:
        lines.append("未找到 extract-report.json。")
        return lines
    skipped = extract_report.get("skipped") or {}
    if skipped:
        for reason in sorted(skipped):
            lines.append(f"- `{reason}`: {skipped[reason]}")
    else:
        lines.append("- 无")
    total = extract_report.get("points_without_card_total")
    if total is not None:
        lines.append(f"- 无卡积分合计: {total}")
    return lines


def _mgj_section(extract_report):
    lines = ["## 美管加独立统计", ""]
    if extract_report is None:
        lines.append("extract-report.json 未找到。")
        return lines
    reported = extract_report.get("mgj_reported")
    if reported == "not available" or reported is None:
        lines.append("not available")
        return lines
    if isinstance(reported, dict):
        for view in (
            "ep_member_get_mem_count",
            "ep_report_shop_treat_surplus",
            "business_card_daily_summary",
        ):
            value = reported.get(view, "not available")
            lines.append(f"- `{view}`: {json.dumps(value, ensure_ascii=False, sort_keys=True)}")
    else:
        lines.append(str(reported))
    return lines


def _decimal(value):
    if value is None:
        return None
    try:
        number = Decimal(str(value))
    except (InvalidOperation, ValueError):
        return None
    return number if number.is_finite() else None


def _money(value):
    return format(value.quantize(Decimal("0.01"), rounding=ROUND_HALF_UP), "f")


def _reported(extract_report):
    if not isinstance(extract_report, dict):
        return None
    reported = extract_report.get("mgj_reported")
    return reported if isinstance(reported, dict) else None


def _balance_section(extract_report, written):
    lines = ["## 与美管加门店余额对比", ""]
    reported = _reported(extract_report)
    summary = reported.get("business_card_daily_summary") if reported else None
    if not isinstance(summary, dict):
        lines.append("not available")
        return lines
    summary_date = summary.get("summary_date")
    if summary_date:
        lines.extend([f"- 快照日期: `{summary_date}`", ""])
    lines.extend(
        [
            "| 口径 | 美管加 | 本系统实际写入 | 差额（美管加 - 写入） |",
            "|---|---:|---:|---:|",
        ]
    )
    rows = [
        ("储值卡余额", summary.get("ending_card_balance"), written.get("principal")),
        ("赠送金余额", summary.get("ending_gift_balance"), written.get("bonus")),
    ]
    for label, reported_value, our_value in rows:
        reported_number = _decimal(reported_value)
        our_number = _decimal(our_value)
        if reported_number is None or our_number is None:
            lines.append(f"| {label} | not available | {our_value} | not available |")
        else:
            lines.append(
                f"| {label} | {_money(reported_number)} | {_money(our_number)} | {_money(reported_number - our_number)} |"
            )
    return lines


def _remaining_section(extract_report):
    lines = ["## 与美管加项目结余次数对比", ""]
    reported = _reported(extract_report)
    rows = reported.get("ep_report_shop_treat_surplus") if reported else None
    if not isinstance(rows, list) or not rows:
        lines.append("not available")
        return lines
    row = rows[0]
    ordinary = row.get("普通项目次数")
    gift = row.get("赠送项目次数")
    total = row.get("项目结余次数")
    ours = extract_report.get("package_remaining_total")
    if ordinary is None or gift is None or total is None or ours is None:
        lines.append("not available")
        return lines
    lines.extend(
        [
            "| 口径 | 本系统导入文件 | 美管加 |",
            "|---|---:|---|",
            f"| 项目结余次数 | {ours} | {total}（普通项目次数={ordinary}，赠送项目次数={gift}） |",
        ]
    )
    return lines


def render(detail, extract_report):
    """把 get_import_batch 的结果和提取报告渲染成不含姓名、手机号的 Markdown。"""
    declared = _values(detail.get("declared") or {})
    file_totals = _values(detail.get("file") or {})
    written = _values(detail.get("written") or {})
    differences = detail.get("differences") or {}
    lines = [
        "# 美管加导入核对报告",
        "",
        f"- 批次: `{detail.get('id', '')}`",
        f"- 状态: `{detail.get('status', '')}`",
        "",
        "## 合计核对",
        "",
    ]
    lines.extend(_totals_table(declared, file_totals, written))
    lines.append("")
    lines.extend(_difference_table("声明 - 文件", differences.get("declared_minus_file") or {}))
    lines.append("")
    lines.extend(
        _difference_table("声明 - 实际写入", differences.get("declared_minus_written") or {})
    )
    lines.append("")
    lines.extend(_error_section(detail.get("rows") or []))
    lines.append("")
    lines.extend(_extract_section(extract_report))
    lines.append("")
    lines.extend(_balance_section(extract_report, written))
    lines.append("")
    lines.extend(_remaining_section(extract_report))
    lines.append("")
    lines.extend(_mgj_section(extract_report))
    lines.append("")
    return "\n".join(lines)


def run(api, account, password, xlsx_path, out_dir, commit=False):
    """上传预览，按需提交，写 import-report.md 并返回报告文本和批次详情。"""
    client = Client(api)
    client.login(account, password)
    preview = client.preview(xlsx_path)
    batch_id = preview["id"]
    detail = client.op("get_import_batch", {"batch_id": batch_id})
    if commit:
        client.op("commit_import", {"batch_id": batch_id})
        detail = client.op("get_import_batch", {"batch_id": batch_id})
    extract_report = _load_extract_report(xlsx_path, out_dir)
    report = render(detail, extract_report)
    output = Path(out_dir)
    output.mkdir(parents=True, exist_ok=True)
    (output / "import-report.md").write_text(report, encoding="utf-8")
    return report, detail
