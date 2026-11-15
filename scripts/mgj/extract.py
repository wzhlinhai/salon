"""读取美管加只读存档，生成项目/卡类型目录、中间 Excel 和提取报告。"""

import json
import re
import sqlite3
from datetime import date
from decimal import Decimal, InvalidOperation
from pathlib import Path

from . import money, xlsx

CUSTOMER_HEADERS = ["来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"]
STORED_HEADERS = ["来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"]
COUNT_HEADERS = ["来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"]
SUMMARY_HEADERS = ["顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"]

PHONE_PATTERN = re.compile(r"^1[0-9]{10}$")
DISCOUNT_NAME_PATTERN = re.compile(r"^([1-9](?:\.[0-9])?)(?:折|特惠)?$")
DATE_PATTERN = re.compile(r"^(\d{4}-\d{2}-\d{2})(?:[ T]\S.*)?$")

MAX_NAME = 40
MAX_ROWS_PER_SHEET = 20000
MAX_UPLOAD_BYTES = 8 << 20

REPORT_ALLOWLISTS = {
    "ep_member_get_mem_count": ("value",),
    "ep_report_shop_treat_surplus": (
        "套餐金总额",
        "普通项目总额",
        "普通项目次数",
        "赠送金总额",
        "赠送项目次数",
        "项目结余总额",
        "项目结余次数",
    ),
    "business_card_daily_summary": (
        "summary_date",
        "ending_card_balance",
        "ending_gift_balance",
    ),
}


def _readonly_uri(path):
    return Path(path).resolve().as_uri() + "?mode=ro"


def _text(value):
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8")
    return str(value)


def _id_text(value):
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    return _text(value).strip()


def _truncate(value, limit=MAX_NAME):
    return value[:limit]


def _has_table(conn, name):
    row = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type IN ('table','view') AND name = ?", (name,)
    ).fetchone()
    return row is not None


def _columns(conn, name):
    if not _has_table(conn, name):
        return None
    return [row[1] for row in conn.execute(f'PRAGMA table_info("{name}")').fetchall()]


def _required_rows(conn, name, columns, where="", order=""):
    if not _has_table(conn, name):
        raise RuntimeError(f"存档缺少表：{name}")
    clause = f" WHERE {where}" if where else ""
    ordering = f" ORDER BY {order}" if order else ""
    return conn.execute(f'SELECT {columns} FROM "{name}"{clause}{ordering}').fetchall()


def _normalise_phone(value, bump):
    if isinstance(value, float) and value.is_integer():
        text = str(int(value))
    elif isinstance(value, int):
        text = str(value)
    else:
        text = _text(value)
    if re.fullmatch(r"[0-9]+\.0+", text.strip()):
        text = text.strip().split(".", 1)[0]
    phone = "".join(text.split())
    if phone == "":
        return ""
    if PHONE_PATTERN.match(phone):
        return phone
    bump("invalid_phone_blanked")
    return ""


def _gender(value):
    value = _text(value).strip()
    return value if value in ("男", "女") else ""


def _date(value, bump):
    text = _text(value).strip()
    if text == "":
        return ""
    match = DATE_PATTERN.fullmatch(text)
    if match is None:
        bump("invalid_date")
        return ""
    candidate = match.group(1)
    try:
        date.fromisoformat(candidate)
    except ValueError:
        bump("invalid_date")
        return ""
    return candidate


def _points(value, bump):
    text = _text(value).strip()
    if text == "":
        return 0
    try:
        number = money.decimal_from(value)
    except (InvalidOperation, ValueError):
        bump("invalid_points")
        return None
    if not number.is_finite() or number != number.to_integral_value():
        bump("invalid_points")
        return None
    return int(number)


def _quantity(value, bump):
    text = _text(value).strip()
    if text == "":
        return 0
    try:
        number = money.decimal_from(value)
    except (InvalidOperation, ValueError):
        bump("invalid_quantity")
        return None
    if not number.is_finite() or number != number.to_integral_value():
        bump("invalid_quantity")
        return None
    return int(number)


def _balance(value, bump):
    text = _text(value).strip()
    if text == "":
        return Decimal("0.00"), None
    try:
        number = money.decimal_from(value)
    except (InvalidOperation, ValueError):
        return None, "invalid_balance"
    if not number.is_finite():
        return None, "invalid_balance"
    if number < 0:
        return None, "negative_balance"
    return money.quantize(number), None


def _service_price(value, bump):
    try:
        payload = json.loads(_text(value))
        price = money.decimal_from(payload["price"])
        if not price.is_finite() or price < 0:
            raise InvalidOperation("invalid price")
        return money.quantize(price)
    except (ValueError, KeyError, TypeError, InvalidOperation):
        bump("invalid_item_price")
        return Decimal("0.00")


def _discount_percent(value, bump):
    text = _text(value).strip()
    if text == "":
        return None
    try:
        discount = money.decimal_from(text)
    except (InvalidOperation, ValueError):
        bump("invalid_discount")
        return None
    if not discount.is_finite():
        bump("invalid_discount")
        return None
    if discount <= 0 or discount >= 10:
        return None
    return money.money(discount * 10)


def _discount_from_name(name):
    match = DISCOUNT_NAME_PATTERN.match(name)
    if match is None:
        return None
    return money.money(Decimal(match.group(1)) * 10)


def _split_package_item(value):
    text = _text(value).strip()
    if "-" not in text:
        return "", text
    item_id, name = text.split("-", 1)
    return _id_text(item_id), name.strip()


def _report_value(value):
    if value is None:
        return None
    if isinstance(value, bool):
        return int(value)
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        number = Decimal(str(value))
        if not number.is_finite():
            return str(value)
        return int(value) if value.is_integer() else value
    return _text(value).strip()


def _report_view(conn, view):
    columns = _columns(conn, view)
    if columns is None:
        return "not available"
    allowlist = REPORT_ALLOWLISTS[view]
    selected = [column for column in allowlist if column in columns]
    if not selected:
        return "not available"
    clause = (
        ' WHERE CAST("is_latest_copy" AS INTEGER) = 1'
        if view.startswith("ep_") and "is_latest_copy" in columns
        else ""
    )
    projection = ", ".join(f'"{column}"' for column in selected)
    rows = conn.execute(f'SELECT {projection} FROM "{view}"{clause}').fetchall()
    out = []
    for row in rows:
        values = {}
        for name, value in zip(selected, row):
            if value is not None:
                values[name] = _report_value(value)
        if values:
            out.append(values)
    return out


def _business_summary(conn):
    rows = _report_view(conn, "business_card_daily_summary")
    if not isinstance(rows, list) or not rows:
        return "not available"
    latest = max(rows, key=lambda row: str(row.get("summary_date", "")))
    return {
        "summary_date": latest.get("summary_date"),
        "ending_card_balance": latest.get("ending_card_balance"),
        "ending_gift_balance": latest.get("ending_gift_balance"),
    }


def _discount_source(conn, view, name_column, latest_only, bump):
    columns = _columns(conn, view)
    if columns is None or name_column not in columns or "discount" not in columns:
        return {}
    clause = (
        ' WHERE CAST("is_latest_copy" AS INTEGER) = 1'
        if latest_only and "is_latest_copy" in columns
        else ""
    )
    rows = conn.execute(
        f'SELECT "{name_column}", "discount" FROM "{view}"{clause} ORDER BY "{name_column}", "discount"'
    ).fetchall()
    grouped = {}
    for name, discount in rows:
        name = _text(name).strip()
        grouped.setdefault(name, set()).add(_discount_percent(discount, bump))
    out = {}
    for name, values in grouped.items():
        if len(values) != 1:
            raise RuntimeError(f"卡类型折扣率冲突：{name}")
        out[name] = next(iter(values))
    return out


def _merge_discounts(target, source):
    for name, discount in source.items():
        if name in target and target[name] != discount:
            raise RuntimeError(f"卡类型折扣率冲突：{name}")
        target[name] = discount


def _resolve_package_item(item_id, name, items_by_id, items_by_name, items, bump):
    item = items_by_id.get(item_id) if item_id else None
    if item is not None:
        return item
    item = items_by_name.get(name)
    if item is not None:
        if item_id:
            items_by_id[item_id] = item
        return item
    item = {"item_id": item_id or name, "name": name, "price": Decimal("0.00")}
    if item_id:
        items_by_id[item_id] = item
    items_by_name[name] = item
    items.append(item)
    bump("item_from_package_only")
    return item


def run(db_path, out_dir):
    """读取 db_path，写出目录下三个文件，并返回提取报告。"""
    output = Path(out_dir)
    output.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(_readonly_uri(db_path), uri=True)
    try:
        return _extract(conn, output)
    finally:
        conn.close()


def _extract(conn, output):
    skipped = {}

    def bump(reason, count=1):
        skipped[reason] = skipped.get(reason, 0) + count

    customers = []
    points_by_customer = {}
    for customer_id, name, mobile, gender, current_points in _required_rows(
        conn,
        "archive_members",
        "customer_id, name, mobile, gender, current_points",
        order="customer_id",
    ):
        customer_id = _id_text(customer_id)
        customers.append(
            [
                customer_id,
                _text(name).strip(),
                _normalise_phone(mobile, bump),
                _gender(gender),
            ]
        )
        points_by_customer[customer_id] = _points(current_points, bump)

    stored = []
    stored_type_names = []
    stored_full_names = {}
    first_rows_by_customer = {}
    for customer_id, card_id, card_name, card_balance, gifted_balance, state in _required_rows(
        conn,
        "archive_member_cards",
        "customer_id, card_id, card_name, card_balance, gifted_balance, card_state",
        order="customer_id, card_id",
    ):
        balance, reason = _balance(card_balance, bump)
        if reason is not None:
            bump(reason)
            continue
        gifted, reason = _balance(gifted_balance, bump)
        if reason is not None:
            bump(reason)
            continue
        inactive = _text(state).strip() != "正常"
        if inactive:
            if balance == 0 and gifted == 0:
                bump("inactive_card")
                continue
            bump("inactive_card_imported_with_balance")
        customer_id = _id_text(customer_id)
        full_name = _text(card_name).strip()
        if full_name == "":
            bump("empty_card_name")
        card_name = _truncate(full_name)
        if full_name:
            previous = stored_full_names.get(card_name)
            if previous is not None and previous != full_name:
                raise RuntimeError(f"储值卡卡类型名称截断后重复：{card_name}")
            stored_full_names[card_name] = full_name
            if card_name not in stored_type_names:
                stored_type_names.append(card_name)
        index = len(stored)
        stored.append(
            [
                _id_text(card_id),
                customer_id,
                card_name,
                money.money(balance),
                money.money(gifted),
                "",
                "",
            ]
        )
        first_rows_by_customer.setdefault(customer_id, []).append(index)

    points_without_card = 0
    for customer_id, points in points_by_customer.items():
        rows = first_rows_by_customer.get(customer_id)
        if rows:
            if points is not None:
                stored[rows[0]][6] = str(points)
        elif points is not None and points > 0:
            bump("points_without_card")
            points_without_card += points

    items = []
    items_by_id = {}
    items_by_name = {}
    for item_id, name, price_json in _required_rows(
        conn,
        "ep_api_shair_service_item_get",
        "itemId, name, serviceItemPrice",
        "CAST(is_latest_copy AS INTEGER) = 1",
        order="itemId, name",
    ):
        item_id = _id_text(item_id)
        name = _text(name).strip()
        if name == "":
            bump("item_without_name")
            continue
        name = _truncate(name)
        price = _service_price(price_json, bump)
        existing = items_by_name.get(name)
        if existing is not None:
            if price != existing["price"]:
                raise RuntimeError(f"项目名称重复且价格不同：{name}")
            bump("duplicate_item_name")
            if item_id:
                items_by_id.setdefault(item_id, existing)
            continue
        item = {"item_id": item_id, "name": name, "price": price}
        items.append(item)
        items_by_name[name] = item
        if item_id:
            items_by_id[item_id] = item

    discounts = {}
    _merge_discounts(
        discounts,
        _discount_source(
            conn,
            "ep_discount_rule_get_card_type_and_ori_discount",
            "cardTypeName",
            True,
            bump,
        ),
    )
    _merge_discounts(
        discounts,
        _discount_source(conn, "ep_shop_proposal_card_types", "cardtypename", False, bump),
    )

    packages = []
    count_types = {}
    package_cards = 0
    package_remaining_total = 0
    for customer_id, instance_id, item_name, remaining_quantity, remaining_amount, valid_until in _required_rows(
        conn,
        "archive_member_packages",
        "customer_id, package_instance_id, item_name, remaining_quantity, remaining_amount, valid_until",
        order="customer_id, package_instance_id",
    ):
        quantity = _quantity(remaining_quantity, bump)
        if quantity is None:
            continue
        if quantity <= 0:
            bump("package_used_up")
            continue
        item_id, package_name = _split_package_item(item_name)
        if package_name == "":
            bump("empty_package_item_name")
            continue
        item = _resolve_package_item(
            item_id, _truncate(package_name), items_by_id, items_by_name, items, bump
        )
        item_name = item["name"]
        type_name = _truncate("历史次卡-" + item_name)
        packages.append(
            [
                "P" + _id_text(instance_id),
                _id_text(customer_id),
                type_name,
                item_name,
                str(quantity),
                "0",
                _date(valid_until, bump),
            ]
        )
        package_cards += 1
        package_remaining_total += quantity
        info = count_types.setdefault(
            type_name, {"item_name": item_name, "item_price": item["price"], "units": []}
        )
        amount = money.raw_decimal_or_none(remaining_amount)
        if amount is not None and amount > 0:
            info["units"].append(amount / Decimal(quantity))

    card_types = []
    for name in stored_type_names:
        full_name = stored_full_names[name]
        if full_name in discounts:
            discount = discounts[full_name]
        else:
            discount = _discount_from_name(full_name)
            if discount is not None:
                bump("discount_from_name")
            else:
                bump("card_type_without_discount_rule")
        card_types.append(
            {
                "name": name,
                "kind": "stored_value",
                "validity_kind": "none",
                "discount_percent": discount,
            }
        )
    for type_name, info in count_types.items():
        unit_price = money.median(info["units"])
        if unit_price is None:
            unit_price = info["item_price"]
        card_types.append(
            {
                "name": type_name,
                "kind": "count",
                "validity_kind": "none",
                "discount_percent": None,
                "items": [
                    {
                        "item_name": info["item_name"],
                        "paid_count": 1,
                        "gift_count": 0,
                        "unit_price": money.money(unit_price),
                    }
                ],
            }
        )
    card_types.sort(key=lambda value: value["name"])

    catalog_items = [{"name": item["name"], "price": money.money(item["price"])} for item in items]
    catalog_items.sort(key=lambda value: value["name"])

    principal = sum((Decimal(row[3]) for row in stored), Decimal("0"))
    bonus = sum((Decimal(row[4]) for row in stored), Decimal("0"))
    summary = [
        str(len(customers)),
        str(len(stored)),
        money.money(principal),
        money.money(bonus),
        str(package_cards),
    ]
    sheets = [
        ("顾客", CUSTOMER_HEADERS, customers),
        ("储值卡", STORED_HEADERS, stored),
        ("次卡套餐", COUNT_HEADERS, packages),
        ("汇总", SUMMARY_HEADERS, [summary]),
    ]
    for name, _headers, rows in sheets:
        if len(rows) > MAX_ROWS_PER_SHEET:
            raise RuntimeError(f"工作表 {name} 超过 {MAX_ROWS_PER_SHEET} 行上限")
    workbook = xlsx.workbook_bytes(sheets)
    if len(workbook) > MAX_UPLOAD_BYTES:
        raise RuntimeError("生成的 import.xlsx 超过 8 MiB 上限")

    reported = {
        "ep_member_get_mem_count": _report_view(conn, "ep_member_get_mem_count"),
        "ep_report_shop_treat_surplus": _report_view(conn, "ep_report_shop_treat_surplus"),
        "business_card_daily_summary": _business_summary(conn),
    }
    mgj_reported = (
        "not available"
        if all(value == "not available" for value in reported.values())
        else reported
    )
    report = {
        "counts": {
            "members": len(customers),
            "stored_cards": len(stored),
            "packages": len(packages),
            "items": len(items),
            "card_types": len(card_types),
        },
        "skipped": dict(sorted(skipped.items())),
        "points_without_card_total": str(points_without_card),
        "package_remaining_total": str(package_remaining_total),
        "mgj_reported": mgj_reported,
    }

    catalog = {"items": catalog_items, "card_types": card_types}
    (output / "catalog.json").write_text(
        json.dumps(catalog, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    (output / "import.xlsx").write_bytes(workbook)
    (output / "extract-report.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    return report
