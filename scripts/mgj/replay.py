"""把一个美管加营业月的单据回放到本店，并写业绩、提成和支付核对报告。

只读取传入的 SQLite 存档，不复制、不修改；顾客只以来源编号参与匹配，
报告和本店顾客档案都不写真实姓名、手机号。
"""

import hashlib
import json
import re
import sqlite3
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP
from pathlib import Path

from . import extract, money, xlsx
from .client import ApiError, Client

MONTH_PATTERN = re.compile(r"^\d{4}-(0[1-9]|1[0-2])$")
LABEL_PATTERN = re.compile(r"^\s*(\S+)\s*号\s*(.*?)\s*\[(.*?)\]\s*$")
ITEM_LINE_PATTERN = re.compile(
    r"^(.*?)\s*[¥￥]\s*([+-]?[0-9,]+(?:\.[0-9]+)?)\s*元?\s*$"
)

MAIN_POSITION = "大工"
ASSISTANT_POSITION = "助理"
ASSISTANT_LEVEL = "助理"
DEFAULT_STAFF_ROLE = "发型师"
REPRICE_REASON = "美管加回放"
OPTIONAL_COIN = ["¥", "￥", "元", ","]
COUPON_CODE_LIMIT = 500
COUPON_KEY_LIMIT = 200

CARD_METHODS = ("卡金", "赠金")
DIRECT_METHODS = {
    "微信支付": "wechat",
    "支付宝": "alipay",
    "现金": "cash",
    "银联": "unionpay",
}
GROUPBUY_METHODS = {
    "点评支付": ("meituan", "美团"),
    "抖音团购": ("douyin", "抖音"),
}
PAYMENT_ROWS = [
    ("卡本金", "卡金"),
    ("赠送金", "赠金"),
    ("微信", "微信支付"),
    ("支付宝", "支付宝"),
    ("现金", "现金"),
    ("银联", "银联"),
    ("团购-美团", "点评支付"),
    ("团购-抖音", "抖音团购"),
]


class ReplayError(RuntimeError):
    """带计数原因的单据回放失败。"""

    def __init__(self, reason, detail=""):
        super().__init__(reason + (": " + detail if detail else ""))
        self.reason = reason
        self.detail = detail


class PhaseError(RuntimeError):
    """安装或期初阶段的错误；带阶段、操作名和原始详情。"""

    def __init__(self, phase, op, detail, status=None):
        super().__init__(phase + " " + op + ": " + detail)
        self.phase = phase
        self.op = op
        self.detail = detail
        self.status = status


class PhaseClient:
    """给客户端操作标上当前阶段和操作名，供 blocker 报告使用。"""

    def __init__(self, client, phase):
        self.client = client
        self.phase = phase

    def _call(self, op, func):
        try:
            return func()
        except ApiError as error:
            raise PhaseError(self.phase, op, _text(error.detail), error.status) from error

    def login(self, account, password):
        return self._call("login", lambda: self.client.login(account, password))

    def op(self, name, payload=None):
        return self._call(name, lambda: self.client.op(name, payload))

    def preview(self, path):
        return self._call("preview_import", lambda: self.client.preview(path))


def phase_call(phase, op, func):
    """把非 ApiError 也包成 PhaseError；已标注的错误原样抛出。"""
    try:
        return func()
    except PhaseError:
        raise
    except ApiError as error:
        raise PhaseError(phase, op, _text(error.detail), error.status) from error
    except Exception as error:
        raise PhaseError(phase, op, str(error)) from error


def _text(value):
    return extract._text(value)


def _id_text(value):
    return extract._id_text(value)


def _amount_text(value):
    """把带货币符号、千分位或“元”的文本读成 Decimal，空值按零。"""
    if isinstance(value, Decimal):
        return money.quantize(value)
    text = _text(value).strip()
    for token in OPTIONAL_COIN:
        text = text.replace(token, "")
    text = text.strip()
    if text == "":
        return Decimal("0.00")
    try:
        number = Decimal(text)
    except (InvalidOperation, ValueError) as error:
        raise ValueError(text) from error
    if not number.is_finite():
        raise ValueError(text)
    return money.quantize(number)


def _optional_amount(value):
    try:
        return _amount_text(value)
    except (ValueError, InvalidOperation):
        return None


def _change(value):
    try:
        return _amount_text(value)
    except (InvalidOperation, ValueError):
        return Decimal("0.00")


def _source_number(value):
    """读取来源文本数字；空值和无法解析都返回 None，便于计数。"""
    if _text(value).strip() == "":
        return None
    try:
        return _amount_text(value)
    except (InvalidOperation, ValueError):
        return None


def _unique_by_name(rows, kind):
    """按名称建映射；重复名称直接中止并指名。"""
    out = {}
    for row in rows:
        name = _text(row.get("name")).strip()
        if name in out:
            raise RuntimeError(kind + "名称重复：" + name)
        out[name] = row
    return out


def coupon_identity(bill_no, index):
    """生成券号和幂等键；超长时截断并加摘要，保持每单每笔唯一。"""
    raw = "mgj-replay-" + _text(bill_no) + "-" + str(index)
    digest = hashlib.sha1(raw.encode("utf-8")).hexdigest()[:16]
    code = raw if len(raw) <= COUPON_CODE_LIMIT else raw[: COUPON_CODE_LIMIT - 17] + "-" + digest
    key = raw if len(raw) <= COUPON_KEY_LIMIT else raw[: COUPON_KEY_LIMIT - 17] + "-" + digest
    return code[:COUPON_CODE_LIMIT], key[:COUPON_KEY_LIMIT]


def _checksum_name(value):
    """把团购商品名压到接口允许的 200 字以内。"""
    return _text(value).strip()[:200]


def parse_items(value):
    """解析 '设计总监剪发 ¥68、烫发D ¥149'。"""
    items = []
    for raw in _text(value).split("、"):
        raw = raw.strip()
        if raw == "":
            continue
        match = ITEM_LINE_PATTERN.match(raw)
        if match is None:
            raise ReplayError("invalid_item_line", raw)
        name = match.group(1).strip()
        if name == "":
            raise ReplayError("invalid_item_line", raw)
        amount = _amount_text(match.group(2))
        if amount < 0:
            raise ReplayError("invalid_item_line", raw)
        items.append({"name": name, "amount": amount})
    if not items:
        raise ReplayError("missing_items")
    return items


def parse_staff(value):
    """解析 '设计总监剪发：AK、浩宇；烫发D：AK(指定)、浩宇'。"""
    segments = []
    for raw in _text(value).split("；"):
        raw = raw.strip()
        if raw == "":
            continue
        if "：" in raw:
            name, rest = raw.split("：", 1)
        elif ":" in raw:
            name, rest = raw.split(":", 1)
        else:
            raise ReplayError("invalid_staff_line", raw)
        name = name.strip()
        if name == "":
            raise ReplayError("invalid_staff_line", raw)
        staff = []
        for part in rest.split("、"):
            part = part.strip()
            if part == "":
                continue
            designated = part.endswith("(指定)") or part.endswith("（指定）")
            if designated:
                part = part[:-4].strip()
            if part == "":
                raise ReplayError("invalid_staff_line", raw)
            staff.append({"name": part, "designated": designated})
        if not staff:
            raise ReplayError("missing_staff", name)
        segments.append({"item_name": name, "staff": staff})
    if not segments:
        raise ReplayError("missing_staff")
    return segments


def match_staff(items, segments):
    """按项目名出现顺序把员工段匹配到项目行，返回行和是否混合指定。"""
    queues = {}
    for index, item in enumerate(items):
        queues.setdefault(item["name"], []).append(index)
    used = set()
    for segment in segments:
        queue = queues.get(segment["item_name"])
        if not queue:
            raise ReplayError("staff_item_mismatch", segment["item_name"])
        index = queue.pop(0)
        if index in used:
            raise ReplayError("staff_item_mismatch", segment["item_name"])
        used.add(index)
        staff = segment["staff"]
        if len(staff) > 3:
            raise ReplayError("too_many_staff", segment["item_name"])
        items[index]["staff"] = [entry["name"] for entry in staff]
        items[index]["staff_designated"] = [entry["designated"] for entry in staff]
        items[index]["line_designated"] = any(entry["designated"] for entry in staff)
    if len(used) != len(items):
        missing = [item["name"] for index, item in enumerate(items) if index not in used]
        raise ReplayError("missing_staff", "、".join(missing))
    mixed = len({bool(item["line_designated"]) for item in items}) > 1
    return items, mixed


def payment_plan(rows):
    """把美管加支付行合并、映射成本系统结账输入。"""
    plan = {
        "total": Decimal("0.00"),
        "card": None,
        "methods": [],
        "groupbuy": [],
        "source": {},
    }
    principal = Decimal("0.00")
    bonus = Decimal("0.00")
    direct = {}
    for row in rows:
        method = _text(row.get("method")).strip()
        amount = row.get("amount")
        if amount is None:
            raise ReplayError(
                "invalid_payment_amount", method + " " + _text(row.get("text"))
            )
        amount = money.quantize(amount)
        if amount == 0:
            continue
        if amount < 0:
            raise ReplayError("invalid_payment_amount", method + " " + money.money(amount))
        plan["source"][method] = plan["source"].get(method, Decimal("0.00")) + amount
        plan["total"] += amount
        if method == "卡金":
            principal += amount
        elif method == "赠金":
            bonus += amount
        elif method in DIRECT_METHODS:
            mapped = DIRECT_METHODS[method]
            direct[mapped] = direct.get(mapped, Decimal("0.00")) + amount
        elif method in GROUPBUY_METHODS:
            platform, label = GROUPBUY_METHODS[method]
            plan["groupbuy"].append(
                {"platform": platform, "label": label, "amount": amount}
            )
        else:
            raise ReplayError("unknown_payment_method", method)
    if principal + bonus > 0:
        plan["card"] = {
            "principal": principal,
            "bonus": bonus,
            "amount": principal + bonus,
        }
    plan["methods"] = [
        {"method": method, "amount": amount} for method, amount in direct.items()
    ]
    return plan


def _readonly_connect(path):
    return sqlite3.connect(extract._readonly_uri(path), uri=True)


def _rows(conn, sql, params=()):
    try:
        return conn.execute(sql, params).fetchall()
    except sqlite3.OperationalError as error:
        raise RuntimeError(str(error)) from error


def load_month_bills(conn, month):
    """按 occurred_at 顺序读出整月单据及支付行。"""
    visits = _rows(
        conn,
        "SELECT bill_no, occurred_at, customer_kind, customer_id, gender, "
        "card_type, items, staff, designated FROM archive_visits "
        "WHERE occurred_at LIKE ? ORDER BY occurred_at, bill_no",
        (month + "%",),
    )
    payments = _rows(
        conn,
        "SELECT p.transaction_id, p.payment_index, p.payment_method, "
        "p.payment_amount_displayed FROM business_transaction_payments p "
        "JOIN archive_visits v ON v.bill_no = p.transaction_id "
        "WHERE v.occurred_at LIKE ? "
        "ORDER BY p.transaction_id, p.payment_index",
        (month + "%",),
    )
    by_bill = {}
    for transaction_id, index, method, amount_text in payments:
        bill_no = _id_text(transaction_id)
        by_bill.setdefault(bill_no, []).append(
            {
                "index": index,
                "method": _text(method).strip(),
                "text": _text(amount_text).strip(),
                "amount": _optional_amount(amount_text),
            }
        )
    bills = []
    for row in visits:
        bill_no = _id_text(row[0])
        bills.append(
            {
                "bill_no": bill_no,
                "occurred_at": _text(row[1]).strip(),
                "customer_kind": _text(row[2]).strip(),
                "customer_id": _id_text(row[3]),
                "gender": _text(row[4]).strip(),
                "card_type": _text(row[5]).strip(),
                "items": _text(row[6]),
                "staff": _text(row[7]),
                "designated": row[8],
                "payments": by_bill.get(bill_no, []),
            }
        )
    return bills


def opening_balances(conn, month):
    """按整月 卡金/赠金 合计算出每个（顾客、卡种）的期初卡余额。"""
    visits = _rows(
        conn,
        "SELECT bill_no, customer_id, gender, card_type FROM archive_visits "
        "WHERE occurred_at LIKE ? ORDER BY occurred_at, bill_no",
        (month + "%",),
    )
    payments = _rows(
        conn,
        "SELECT p.transaction_id, p.payment_method, p.payment_amount_displayed "
        "FROM business_transaction_payments p "
        "JOIN archive_visits v ON v.bill_no = p.transaction_id "
        "WHERE v.occurred_at LIKE ? "
        "ORDER BY p.transaction_id, p.payment_index",
        (month + "%",),
    )
    by_bill = {}
    for transaction_id, method, amount_text in payments:
        by_bill.setdefault(_id_text(transaction_id), []).append(
            (_text(method).strip(), amount_text)
        )
    customers = {}
    cards = {}
    skipped = {}

    def bump(reason):
        skipped[reason] = skipped.get(reason, 0) + 1

    for bill_no, customer_id, gender, card_type in visits:
        customer_id = _id_text(customer_id)
        gender = _text(gender).strip()
        if customer_id and (
            customer_id not in customers or (customers[customer_id] == "" and gender != "")
        ):
            customers[customer_id] = gender
        for method, amount_text in by_bill.get(_id_text(bill_no), []):
            if method not in CARD_METHODS:
                continue
            amount = _optional_amount(amount_text)
            if amount is None:
                bump("opening_invalid_amount")
                continue
            if amount == 0:
                continue
            if customer_id == "":
                bump("opening_missing_customer")
                continue
            card_type = _text(card_type).strip()
            if card_type == "":
                bump("opening_missing_card_type")
                continue
            entry = cards.setdefault(
                (customer_id, card_type),
                {"principal": Decimal("0.00"), "bonus": Decimal("0.00")},
            )
            if method == "卡金":
                entry["principal"] += amount
            else:
                entry["bonus"] += amount
    kept = {}
    for key, value in cards.items():
        if value["principal"] < 0 or value["bonus"] < 0:
            bump("negative_opening_balance")
            continue
        if value["principal"] + value["bonus"] > 0:
            kept[key] = value
    cards = kept
    with_cards = {customer_id for customer_id, _card_type in cards}
    customers = {
        customer_id: gender
        for customer_id, gender in customers.items()
        if customer_id in with_cards
    }
    return {"customers": customers, "cards": cards, "skipped": skipped}


def opening_workbook(balances):
    """生成只含期初顾客和储值卡的中间 Excel 工作表。"""
    customers = []
    for customer_id in sorted(balances["customers"]):
        customers.append(
            [
                customer_id,
                "回放顾客" + customer_id,
                "",
                balances["customers"][customer_id]
                if balances["customers"][customer_id] in ("男", "女")
                else "",
            ]
        )
    stored = []
    principal_total = Decimal("0.00")
    bonus_total = Decimal("0.00")
    for customer_id, card_type in sorted(balances["cards"]):
        entry = balances["cards"][(customer_id, card_type)]
        principal_total += entry["principal"]
        bonus_total += entry["bonus"]
        stored.append(
            [
                "R" + customer_id + "-" + card_type,
                customer_id,
                card_type,
                money.money(entry["principal"]),
                money.money(entry["bonus"]),
                "",
                "",
            ]
        )
    summary = [
        str(len(customers)),
        str(len(stored)),
        money.money(principal_total),
        money.money(bonus_total),
        "0",
    ]
    sheets = [
        ("顾客", extract.CUSTOMER_HEADERS, customers),
        ("储值卡", extract.STORED_HEADERS, stored),
        ("次卡套餐", extract.COUNT_HEADERS, []),
        ("汇总", extract.SUMMARY_HEADERS, [summary]),
    ]
    return sheets, principal_total, bonus_total


def import_opening(client, sheets, out_dir):
    """上传并提交期初余额，返回来源编号到本店实体编号的映射。"""
    customers = sheets[0][2]
    stored = sheets[1][2]
    if not customers and not stored:
        return {
            "customers": {},
            "cards": {},
            "imported": False,
            "batch_id": None,
            "error_rows": 0,
            "rerun_suspected": False,
            "rerun_reason": "",
        }
    path = Path(out_dir) / "replay-opening.xlsx"
    xlsx.write_workbook(path, sheets)
    preview = client.preview(path)
    batch_id = preview["id"]
    client.op("commit_import", {"batch_id": batch_id})
    detail = client.op("get_import_batch", {"batch_id": batch_id})
    customer_map = {}
    card_map = {}
    error_rows = 0
    already_imported = False
    missing_entity = False
    for row in (detail.get("rows") or []):
        if row.get("error"):
            error_rows += 1
            if "已导入" in _text(row.get("error")):
                already_imported = True
            continue
        entity_id = row.get("entity_id")
        source_key = row.get("source_key")
        if not entity_id or not source_key:
            if row.get("kind") in ("customer", "stored_value"):
                missing_entity = True
            continue
        if row.get("kind") == "customer":
            customer_map[source_key] = entity_id
        elif row.get("kind") == "stored_value":
            card_map[source_key] = entity_id
    expected_customers = {row[0] for row in customers}
    expected_cards = {row[0] for row in stored}
    missing_mapping = (
        not expected_customers.issubset(customer_map)
        or not expected_cards.issubset(card_map)
    )
    rerun_suspected = already_imported or missing_entity or missing_mapping
    reasons = []
    if already_imported:
        reasons.append("导入批次有已导入行")
    if missing_entity or missing_mapping:
        reasons.append("导入行缺少 entity_id")
    return {
        "customers": customer_map,
        "cards": card_map,
        "imported": True,
        "batch_id": batch_id,
        "error_rows": error_rows,
        "rerun_suspected": rerun_suspected,
        "rerun_reason": "；".join(reasons),
    }


def ensure_fresh(client, today):
    """确认当天没有挂单或已结业绩；rerun 必须从刚 reset 的库开始。"""
    orders = client.op("list_open_orders").get("items") or []
    if orders:
        raise ReplayError("replay 需要刚 reset 的开发库", "当天已有挂单")
    report = client.op("get_staff_report", {"from": today, "to": today})
    for row in (report.get("rows") or []):
        if _change(row.get("item_performance")) != 0:
            raise ReplayError("replay 需要刚 reset 的开发库", "当天已有业绩")


def check_import_fresh(imported):
    if imported.get("rerun_suspected"):
        raise ReplayError(
            "replay 需要刚 reset 的开发库", imported.get("rerun_reason", "")
        )


def parse_employee_label(value):
    """解析 '<number> 号 <level> [<name>]'。"""
    match = LABEL_PATTERN.match(_text(value))
    if match is None:
        return None
    number = match.group(1).strip()
    level = match.group(2).strip()
    name = match.group(3).strip()
    if number == "" or level == "" or name == "":
        return None
    return {"number": number, "level": level, "name": name}


def monthly_employees(conn, month):
    """读取当月员工标签、提成和卡销售业绩；标签只用于建员工和报告。"""
    rows = _rows(
        conn,
        "SELECT employee_label, item_commission, card_sales_performance, "
        "card_sales_commission, captured_at, source_record_id "
        "FROM business_employee_commission_monthly "
        "WHERE period_month LIKE ? AND employee_label IS NOT NULL "
        "ORDER BY employee_label, captured_at, source_record_id",
        (month + "%",),
    )
    latest = {}
    for row in rows:
        latest[_text(row[0])] = row
    staff = {}
    by_number = {}
    commission = {}
    skipped = {}
    invalid_numbers = 0
    for (
        label,
        item_commission,
        card_sales_performance,
        card_sales_commission,
        _captured_at,
        _source_record_id,
    ) in latest.values():
        parsed = parse_employee_label(label)
        if parsed is None:
            skipped["employee_label_unparsed"] = skipped.get("employee_label_unparsed", 0) + 1
            continue
        number = parsed["number"]
        previous = by_number.get(number)
        if previous is not None and previous != parsed:
            raise RuntimeError("员工编号对应多个标签：" + number)
        by_number[number] = parsed
        staff[(number, parsed["name"], parsed["level"])] = parsed
        entry = commission.setdefault(
            parsed["name"],
            {
                "item_commission": Decimal("0.00"),
                "card_sales_performance": Decimal("0.00"),
                "card_sales_commission": Decimal("0.00"),
            },
        )
        for key, value in (
            ("item_commission", item_commission),
            ("card_sales_performance", card_sales_performance),
            ("card_sales_commission", card_sales_commission),
        ):
            amount = _source_number(value)
            if amount is None:
                invalid_numbers += 1
                amount = Decimal("0.00")
            entry[key] += amount
    return {
        "staff": sorted(staff.values(), key=lambda row: (row["number"], row["name"])),
        "commission": commission,
        "skipped": skipped,
        "invalid_numbers": invalid_numbers,
    }


def _list(client, name, key, kind):
    return _unique_by_name(client.op(name).get(key) or [], kind)


def setup_positions(client):
    """确保大工、助理两个岗位存在，返回名称到编号的映射。"""
    positions = _list(client, "list_positions", "items", "岗位")
    for name in (MAIN_POSITION, ASSISTANT_POSITION):
        if name not in positions:
            positions[name] = client.op("create_position", {"name": name})
    return {name: positions[name]["id"] for name in (MAIN_POSITION, ASSISTANT_POSITION)}


def setup_levels(client, employees):
    """确保当月员工级别存在，返回名称到编号的映射。"""
    levels = _list(client, "list_levels", "items", "级别")
    for level in sorted({row["level"] for row in employees}):
        if level not in levels:
            levels[level] = client.op("create_level", {"name": level})
    return {name: levels[name]["id"] for name in levels}


def _staff_rows(response):
    return response.get("staff") or []


def setup_staff(client, employees, levels, positions):
    """按月标签幂等地建级别、员工和默认角色，返回姓名到员工视图的映射。"""
    roles = client.op("list_roles").get("roles") or []
    role = next((item for item in roles if item.get("name") == DEFAULT_STAFF_ROLE), None)
    if role is None:
        names = "、".join(item.get("name", "") for item in roles)
        raise RuntimeError("没有默认员工角色 " + DEFAULT_STAFF_ROLE + "；现有角色：" + names)
    staff_rows = _staff_rows(client.op("list_staff"))
    _unique_by_name(staff_rows, "员工")
    existing = {
        _text(row.get("number")): row
        for row in staff_rows
    }
    by_name = {}
    for row in employees:
        number = row["number"]
        name = row["name"]
        level_id = levels[row["level"]]
        position_id = positions[
            ASSISTANT_POSITION if row["level"] == ASSISTANT_LEVEL else MAIN_POSITION
        ]
        current = existing.get(number)
        if current is None:
            current = client.op(
                "create_staff",
                {
                    "name": name,
                    "number": number,
                    "level_id": level_id,
                    "position_id": position_id,
                    "role_ids": [role["id"]],
                },
            )
            existing[number] = current
        else:
            if _text(current.get("name")) != name:
                raise RuntimeError("员工编号 " + number + " 已有不同姓名")
            current_level = (current.get("level") or {}).get("id")
            current_position = (current.get("position") or {}).get("id")
            if current_level != level_id or current_position != position_id:
                current = client.op(
                    "update_staff",
                    {
                        "staff_id": current["id"],
                        "name": name,
                        "number": number,
                        "level_id": level_id,
                        "position_id": position_id,
                    },
                )
                existing[number] = current
        if name in by_name:
            raise RuntimeError("员工姓名重复，无法按姓名回放：" + name)
        by_name[name] = current
    return by_name


def _desired_positions(positions):
    return [
        {"position_id": positions[MAIN_POSITION], "required": True},
        {"position_id": positions[ASSISTANT_POSITION], "required": False},
        {"position_id": positions[ASSISTANT_POSITION], "required": False},
    ]


def _same_positions(current, desired):
    if len(current or []) != len(desired):
        return False
    for left, right in zip(current, desired):
        if left.get("position_id") != right["position_id"]:
            return False
        if bool(left.get("required")) != right["required"]:
            return False
        if left.get("share_percent") is not None:
            return False
    return True


def item_payload(item, positions, category_id=None, keep_category=True):
    """保留项目当前定义，替换岗位列表，并保留或替换类别。"""
    category = (item.get("category") or {}).get("id") if keep_category else category_id
    return {
        "id": item["id"],
        "name": item["name"],
        "kind": item.get("kind", "service"),
        "price": item["price"],
        "discountable": bool(item.get("discountable", True)),
        "labor_fee_amount": item.get("labor_fee_amount"),
        "labor_fee_percent": item.get("labor_fee_percent"),
        "duration_minutes": item.get("duration_minutes", 0),
        "bookable": item.get("bookable", True),
        "positions": _desired_positions(positions),
        "level_prices": [
            {"level_id": price["level_id"], "price": price["price"]}
            for price in item.get("level_prices", [])
        ],
        "member_prices": [
            {"card_type_id": price["card_type_id"], "price": price["price"]}
            for price in item.get("member_prices", [])
        ],
        "category_id": category,
        "active": item.get("active", True),
    }


def setup_items(client, positions):
    """给每个项目补上大工（必填）和两个助理（非必填）岗位。"""
    items = _unique_by_name(client.op("list_items").get("items") or [], "项目")
    desired = _desired_positions(positions)
    for name, item in list(items.items()):
        if _same_positions(item.get("positions"), desired):
            continue
        items[name] = client.op("update_item", item_payload(item, positions))
    return items


def setup_categories(client, categories, items, positions):
    """按名称幂等地建类别、设项目类别；未列出的回放项目不归类。"""
    existing = _unique_by_name(
        client.op("list_item_categories").get("items") or [], "类别"
    )
    by_name = dict(existing)
    configured = set()
    desired = {}
    for entry in categories:
        name = _text(entry.get("name")).strip()
        if name == "":
            raise RuntimeError("类别名称不能为空")
        if name in configured:
            raise RuntimeError("类别名称重复：" + name)
        configured.add(name)
        ratio = int(entry.get("performance_ratio", 100))
        current = existing.get(name)
        if current is None:
            current = client.op(
                "create_item_category", {"name": name, "performance_ratio": ratio}
            )
        elif int(current.get("performance_ratio", 100)) != ratio or not current.get(
            "active", True
        ):
            current = client.op(
                "update_item_category",
                {
                    "id": current["id"],
                    "name": name,
                    "performance_ratio": ratio,
                    "active": True,
                },
            )
        existing[name] = current
        by_name[name] = current
        for value in entry.get("items") or []:
            item_name = _text(value).strip()
            if item_name in desired:
                raise RuntimeError("类别项目重复：" + item_name)
            if item_name not in items:
                raise RuntimeError("类别引用了不存在的项目：" + item_name)
            desired[item_name] = current["id"]
    for name, item in list(items.items()):
        category_id = desired.get(name)
        current = (item.get("category") or {}).get("id")
        if current == category_id:
            continue
        items[name] = client.op(
            "update_item",
            item_payload(item, positions, category_id, keep_category=False),
        )
    return items, by_name


def apply_settings(client, settings_object, effective_from):
    """在开第一张单前按输入 JSON 改门店设置；任何拒绝都中止安装阶段。"""
    for key in sorted(settings_object):
        client.op(
            "change_setting",
            {
                "key": key,
                "value": settings_object[key],
                "effective_from": effective_from,
            },
        )


def _named_id(mapping, name, kind):
    if name is None:
        return None
    if name not in mapping:
        raise RuntimeError("提成方案引用了不存在的" + kind + "：" + _text(name))
    return mapping[name]


def resolve_scheme(
    scheme, staff_by_name, item_by_name, position_by_name, category_by_name
):
    """把提成方案里的名称替换成编号；未知名称直接中止。"""
    if not isinstance(scheme, dict):
        raise RuntimeError("scheme.json 缺少 commission 对象")
    rules = []
    for rule in scheme.get("rules", []):
        cells = []
        for cell in rule.get("cells", []):
            cell = dict(cell)
            if "position_name" in cell:
                if "position_id" in cell:
                    raise RuntimeError("提成格子不能同时写 position_name 和 position_id")
                cell["position_id"] = _named_id(
                    position_by_name, cell.pop("position_name"), "岗位"
                )
            cells.append(cell)
        rules.append({**rule, "cells": cells})
    overrides = []
    for override in scheme.get("overrides", []):
        override = dict(override)
        if "staff_name" in override:
            if "staff_id" in override:
                raise RuntimeError("覆盖比例不能同时写 staff_name 和 staff_id")
            override["staff_id"] = _named_id(
                staff_by_name, override.pop("staff_name"), "员工"
            )
        if "item_name" in override:
            if "item_id" in override:
                raise RuntimeError("覆盖比例不能同时写 item_name 和 item_id")
            override["item_id"] = _named_id(
                item_by_name, override.pop("item_name"), "项目"
            )
        if "position_name" in override:
            if "position_id" in override:
                raise RuntimeError("覆盖比例不能同时写 position_name 和 position_id")
            override["position_id"] = _named_id(
                position_by_name, override.pop("position_name"), "岗位"
            )
        if "category_name" in override:
            if "category_id" in override:
                raise RuntimeError("覆盖比例不能同时写 category_name 和 category_id")
            override["category_id"] = _named_id(
                category_by_name, override.pop("category_name"), "类别"
            )
        overrides.append(override)
    excluded = []
    for entry in scheme.get("excluded_staff", []):
        name = entry.get("staff_name") if isinstance(entry, dict) else entry
        if name is None:
            raise RuntimeError("excluded_staff 的每一项要写员工姓名")
        staff_id = _named_id(staff_by_name, name, "员工")
        if staff_id not in excluded:
            excluded.append(staff_id)
    return {"rules": rules, "overrides": overrides, "excluded_staff": excluded}


def _flag(value):
    text = _text(value).strip()
    if text.lower() in ("true", "是"):
        return True
    try:
        return Decimal(text) == 1
    except (InvalidOperation, ValueError):
        return False


def _gender(value):
    text = _text(value).strip()
    if text == "男":
        return "male", False
    if text == "女":
        return "female", False
    return "male", True


def _new_order_item(order, known):
    fresh = [item for item in order.get("items", []) if item.get("id") not in known]
    if len(fresh) != 1:
        raise RuntimeError("加项目后无法定位新的项目行")
    return fresh[0]


def load_groupbuy_deals(client):
    return {
        (deal["platform"], deal["name"]): deal
        for deal in (client.op("list_groupbuy_deals").get("items") or [])
    }


def _deal_item(client, context, label, amount):
    target = money.money(amount)
    for item in context["items_by_name"].values():
        if item.get("active", True) and money.money(item["price"]) == target:
            return item
    base = "回放团购-" + label + "-" + target
    name = base[:40]
    suffix = 1
    for _attempt in range(100):
        if name not in context["items_by_name"]:
            break
        existing = context["items_by_name"][name]
        if money.money(existing["price"]) == target:
            return existing
        suffix += 1
        name = (base + "-" + str(suffix))[:40]
    else:
        raise RuntimeError("无法为团购金额 " + target + " 创建唯一项目名")
    item = client.op(
        "create_item",
        {
            "name": name,
            "kind": "service",
            "price": target,
            "discountable": False,
            "labor_fee_amount": None,
            "labor_fee_percent": None,
            "positions": [],
            "level_prices": [],
        },
    )
    context["items_by_name"][item["name"]] = item
    return item


def ensure_deal(client, context, platform, label, amount):
    """每个（平台、金额）只建一个团购商品，门店标价等于该金额。"""
    key = (platform, money.money(amount))
    if key in context["deals"]:
        return context["deals"][key]
    name = _checksum_name("美管加回放 " + label + " " + money.money(amount))
    existing = context["deals_by_name"].get((platform, name))
    if existing is not None:
        context["deals"][key] = existing
        return existing
    item = _deal_item(client, context, label, amount)
    deal = client.op(
        "create_groupbuy_deal",
        {
            "platform": platform,
            "name": name,
            "purchase_price": money.money(amount),
            "item_id": item["id"],
        },
    )
    context["deals"][key] = deal
    context["deals_by_name"][(platform, name)] = deal
    return deal


def redeem_and_link(client, deal, order_id, bill_no, index):
    code, key = coupon_identity(bill_no, index)
    redemption = client.op(
        "redeem_coupon",
        {"platform": deal["platform"], "coupon_code": code, "key": key},
    )
    if redemption.get("status") != "redeemed":
        raise ReplayError("groupbuy_redeem_failed", deal["platform"] + " " + code)
    client.op(
        "link_coupon_order",
        {
            "redemption_id": redemption["id"],
            "deal_id": deal["id"],
            "order_id": order_id,
        },
    )
    return redemption


def replay_bill(client, bill, context):
    """回放一张单；任何单张失败都只记录，不抛出到整批。"""
    record = {
        "bill_no": bill["bill_no"],
        "status": "failed",
        "reason": None,
        "detail": "",
        "order_id": None,
        "repriced_lines": 0,
        "mixed_designation": False,
        "gender_defaulted": False,
        "source_payments": bill["payments"],
        "our_payments": [],
        "our_groupbuy": [],
    }
    try:
        items, mixed = match_staff(parse_items(bill["items"]), parse_staff(bill["staff"]))
        record["mixed_designation"] = mixed
        for line in items:
            if line["name"] not in context["items_by_name"]:
                raise ReplayError("unknown_item", line["name"])
            for name in line["staff"]:
                if name not in context["staff_by_name"]:
                    raise ReplayError("unknown_staff", name)
        plan = payment_plan(bill["payments"])
        gender, defaulted = _gender(bill["gender"])
        record["gender_defaulted"] = defaulted
        designated = _flag(bill["designated"])
        source_customer = bill["customer_id"]
        has_card = plan["card"] is not None
        member = (
            bill["customer_kind"] == "会员"
            and has_card
            and source_customer != ""
        )
        if has_card and not member:
            raise ReplayError(
                "unknown_card",
                "非会员单或缺少来源顾客的卡支付无法映射",
            )
        customer_id = None
        card_id = None
        if member:
            customer_id = context["customer_map"].get(source_customer)
            if customer_id is None:
                raise ReplayError("unknown_customer", "来源顾客未映射")
            card_id = context["card_map"].get(
                (source_customer, bill["card_type"])
            )
            if card_id is None:
                raise ReplayError("unknown_card", bill["card_type"] or "卡种为空")

        payload = {
            "gender": gender,
            "designated": designated,
            "items": [],
        }
        if customer_id is not None:
            payload["customer_id"] = customer_id
        order = client.op("open_order", payload)
        record["order_id"] = order["id"]
        known = set()
        for line in items:
            item = context["items_by_name"].get(line["name"])
            staff = []
            for slot, name in enumerate(line["staff"], start=1):
                employee = context["staff_by_name"].get(name)
                staff.append({"slot": slot, "staff_id": employee["id"]})
            order = client.op(
                "add_order_item",
                {
                    "order_id": order["id"],
                    "item": {"item_id": item["id"], "staff": staff},
                },
            )
            fresh = _new_order_item(order, known)
            known.add(fresh["id"])
            if money.money(fresh["price"]) != money.money(line["amount"]):
                order = client.op(
                    "reprice_order_item",
                    {
                        "order_id": order["id"],
                        "order_item_id": fresh["id"],
                        "price": money.money(line["amount"]),
                        "reason": REPRICE_REASON,
                    },
                )
                record["repriced_lines"] += 1
        receivable = money.quantize(order["receivable"])
        if receivable != money.quantize(plan["total"]):
            raise ReplayError(
                "payment_mismatch",
                "美管加 " + money.money(plan["total"]) + " 本系统 " + money.money(receivable),
            )
        for groupbuy in plan["groupbuy"]:
            deal = ensure_deal(
                client,
                context,
                groupbuy["platform"],
                groupbuy["label"],
                groupbuy["amount"],
            )
            redeem_and_link(
                client, deal, order["id"], bill["bill_no"], groupbuy_index(groupbuy, plan)
            )
        if plan["card"] is not None:
            preview = client.op(
                "preview_checkout", {"order_id": order["id"], "card_id": card_id}
            )
            preview_net = money.quantize(preview["net"])
            if preview_net != money.quantize(plan["total"]):
                discounted = [
                    _text(item.get("item_name")).strip()
                    for item in (preview.get("items") or [])
                    if item.get("price_source") != "manual"
                    and _change(item.get("discount")) > 0
                ]
                raise ReplayError(
                    "card_discount_mismatch",
                    "美管加 " + money.money(plan["total"])
                    + " 本系统 " + money.money(preview_net)
                    + " 打折行 " + ("、".join(discounted) or "无"),
                )
        payments = []
        if plan["card"] is not None:
            payments.append(
                {
                    "method": "card",
                    "amount": money.money(plan["card"]["amount"]),
                    "card_id": card_id,
                }
            )
        for method in plan["methods"]:
            payments.append(
                {"method": method["method"], "amount": money.money(method["amount"])}
            )
        checked = client.op(
            "checkout_orders",
            {"orders": [{"order_id": order["id"], "payments": payments}]},
        )
        final = checked["items"][0]
        record["status"] = "replayed"
        record["order_id"] = final["id"]
        record["our_payments"] = final.get("payments", [])
        record["our_groupbuy"] = final.get("groupbuy", [])
        return record
    except ReplayError as error:
        record["reason"] = error.reason
        record["detail"] = error.detail
        return record
    except ApiError as error:
        record["reason"] = "api_" + str(error.status)
        record["detail"] = _text(error.detail)
        return record
    except Exception as error:  # noqa: BLE001 - 单张单的任何异常都不能中断整批
        record["reason"] = "error_" + type(error).__name__
        record["detail"] = str(error)
        return record


def groupbuy_index(groupbuy, plan):
    """为同一张单的多笔团购生成稳定、唯一的券号后缀。"""
    for index, candidate in enumerate(plan["groupbuy"], start=1):
        if candidate is groupbuy:
            return index
    return 1


def _empty_payment_totals():
    return {label: Decimal("0.00") for label, _source in PAYMENT_ROWS}


def _source_payment_totals(record, totals):
    for payment in record["source_payments"]:
        for label, source_method in PAYMENT_ROWS:
            if payment.get("method") == source_method:
                amount = payment.get("amount")
                if amount is not None:
                    totals[label] += money.quantize(amount)


def _our_payment_totals(record, totals):
    for payment in record.get("our_payments", []):
        method = payment.get("method")
        amount = money.quantize(payment.get("amount", "0"))
        if method == "card":
            card = payment.get("card") or {}
            totals["卡本金"] += money.quantize(card.get("principal", "0"))
            totals["赠送金"] += money.quantize(card.get("bonus", "0"))
        elif method == "wechat":
            totals["微信"] += amount
        elif method == "alipay":
            totals["支付宝"] += amount
        elif method == "cash":
            totals["现金"] += amount
        elif method == "unionpay":
            totals["银联"] += amount
    for groupbuy in record.get("our_groupbuy", []):
        label = "团购-美团" if groupbuy.get("platform") == "meituan" else "团购-抖音"
        totals[label] += money.quantize(groupbuy.get("amount", "0"))


def _source_card_split(record):
    principal = Decimal("0.00")
    bonus = Decimal("0.00")
    for payment in record["source_payments"]:
        amount = payment.get("amount")
        if amount is None:
            continue
        if payment.get("method") == "卡金":
            principal += money.quantize(amount)
        elif payment.get("method") == "赠金":
            bonus += money.quantize(amount)
    return principal, bonus


def _our_card_split(record):
    principal = Decimal("0.00")
    bonus = Decimal("0.00")
    for payment in record.get("our_payments", []):
        if payment.get("method") != "card":
            continue
        card = payment.get("card") or {}
        principal += money.quantize(card.get("principal", "0"))
        bonus += money.quantize(card.get("bonus", "0"))
    return principal, bonus


def _source_performance(conn, month, bill_numbers):
    view = "ep_super_operation_report_search__t3"
    columns = extract._columns(conn, view)
    required = {
        "is_latest_copy",
        "员工项目业绩流水·消费时间",
        "员工项目业绩流水·流水单号",
        "员工项目业绩流水·总业绩",
        "员工项目业绩流水·现金类业绩",
        "员工项目业绩流水·卡扣业绩",
        "员工项目业绩流水·员工姓名",
    }
    if columns is None or not required.issubset(set(columns)):
        return None
    rows = _rows(
        conn,
        'SELECT "员工项目业绩流水·员工姓名", "员工项目业绩流水·总业绩", '
        '"员工项目业绩流水·现金类业绩", "员工项目业绩流水·卡扣业绩", '
        '"员工项目业绩流水·流水单号", "员工项目业绩流水·消费时间" '
        'FROM "' + view + '" WHERE CAST("is_latest_copy" AS INTEGER) = 1 '
        'AND "员工项目业绩流水·消费时间" LIKE ?',
        (month + "%",),
    )
    out = {}
    invalid_numbers = 0
    empty_staff = 0
    for staff, total, cash, card, bill_no, _consumed_at in rows:
        if _id_text(bill_no) not in bill_numbers:
            continue
        name = _text(staff).strip()
        if name == "":
            empty_staff += 1
            continue
        entry = out.setdefault(
            name,
            {
                "total": Decimal("0.00"),
                "cash": Decimal("0.00"),
                "card": Decimal("0.00"),
            },
        )
        for key, value in (("total", total), ("cash", cash), ("card", card)):
            amount = _source_number(value)
            if amount is None:
                invalid_numbers += 1
                amount = Decimal("0.00")
            entry[key] += amount
    return {
        "rows": out,
        "invalid_numbers": invalid_numbers,
        "empty_staff": empty_staff,
    }


def _ours_performance(client, today):
    report = client.op("get_staff_report", {"from": today, "to": today})
    out = {}
    for row in (report.get("rows") or []):
        staff = row.get("staff") or {}
        name = _text(staff.get("name")).strip()
        if name == "":
            continue
        out[name] = {
            "total": _change(row.get("item_performance")),
            "cash": _change(row.get("cash_performance")),
            "card": _change(row.get("card_performance")),
        }
    return out


def _all_commissions(client, today):
    entries = []
    before = None
    while True:
        payload = {"date_from": today, "date_to": today, "limit": 200}
        if before is not None:
            payload["before_id"] = before
        page = client.op("list_commissions", payload)
        chunk = (page.get("entries") or [])
        entries.extend(chunk)
        if len(chunk) < 200:
            break
        before = chunk[-1]["id"]
    return entries


def _ours_commissions(entries):
    out = {}
    for entry in entries:
        source = entry.get("source")
        if source not in ("item", "consume", "labor"):
            continue
        staff = entry.get("staff") or {}
        name = _text(staff.get("name")).strip()
        if name == "":
            continue
        row = out.setdefault(
            name,
            {
                "item": Decimal("0.00"),
                "consume": Decimal("0.00"),
                "labor": Decimal("0.00"),
            },
        )
        row[source] += _change(entry.get("amount"))
    return out


def _ratio(numerator, denominator):
    if denominator == 0:
        return "n/a"
    value = (numerator / denominator * Decimal("100")).quantize(
        Decimal("0.01"), rounding=ROUND_HALF_UP
    )
    return format(value, "f") + "%"


def _diff(source, ours):
    return source - ours


def build_result(
    month,
    today,
    bills,
    records,
    balances,
    opening_import,
    source_performance,
    ours_performance,
    monthly_commission,
    ours_commission,
    staff_names,
    employee_skips,
    blockers,
    commission_invalid_numbers,
):
    replayed = [record for record in records if record["status"] == "replayed"]
    failures = [record for record in records if record["status"] != "replayed"]
    failure_counts = {}
    for record in failures:
        reason = record.get("reason") or "unknown"
        entry = failure_counts.setdefault(reason, {"count": 0, "examples": []})
        entry["count"] += 1
        if len(entry["examples"]) < 10 and record["bill_no"] not in entry["examples"]:
            entry["examples"].append(record["bill_no"])

    source_totals = _empty_payment_totals()
    ours_totals = _empty_payment_totals()
    for record in replayed:
        _source_payment_totals(record, source_totals)
        _our_payment_totals(record, ours_totals)
    payment_rows = []
    for label, source_method in PAYMENT_ROWS:
        payment_rows.append(
            {
                "method": label,
                "source_method": source_method,
                "source_total": money.money(source_totals[label]),
                "ours_total": money.money(ours_totals[label]),
                "difference": money.money(_diff(source_totals[label], ours_totals[label])),
            }
        )
    payments_totals = {
        "source_total": money.money(sum(source_totals.values(), Decimal("0.00"))),
        "ours_total": money.money(sum(ours_totals.values(), Decimal("0.00"))),
        "difference": money.money(
            _diff(sum(source_totals.values(), Decimal("0.00")), sum(ours_totals.values(), Decimal("0.00")))
        ),
    }

    source_principal = Decimal("0.00")
    source_bonus = Decimal("0.00")
    ours_principal = Decimal("0.00")
    ours_bonus = Decimal("0.00")
    split_count = 0
    split_examples = []
    for record in replayed:
        card_principal, card_bonus = _source_card_split(record)
        if card_principal + card_bonus == 0:
            continue
        our_principal, our_bonus = _our_card_split(record)
        source_principal += card_principal
        source_bonus += card_bonus
        ours_principal += our_principal
        ours_bonus += our_bonus
        if card_principal != our_principal or card_bonus != our_bonus:
            split_count += 1
            if len(split_examples) < 10:
                split_examples.append(
                    {
                        "bill_no": record["bill_no"],
                        "source_principal": money.money(card_principal),
                        "source_bonus": money.money(card_bonus),
                        "ours_principal": money.money(our_principal),
                        "ours_bonus": money.money(our_bonus),
                    }
                )
    card_split = {
        "source_principal": money.money(source_principal),
        "source_bonus": money.money(source_bonus),
        "ours_principal": money.money(ours_principal),
        "ours_bonus": money.money(ours_bonus),
        "difference_count": split_count,
        "examples": split_examples,
    }

    performance_names = set(staff_names)
    if source_performance is not None:
        performance_names.update(source_performance["rows"])
    performance_names.update(ours_performance)
    performance = []
    for name in sorted(performance_names):
        source = (source_performance or {}).get("rows", {}).get(
            name, {"total": Decimal("0.00"), "cash": Decimal("0.00"), "card": Decimal("0.00")}
        )
        ours = ours_performance.get(
            name, {"total": Decimal("0.00"), "cash": Decimal("0.00"), "card": Decimal("0.00")}
        )
        performance.append(
            {
                "staff": name,
                "mgj_total": money.money(source["total"]),
                "mgj_cash": money.money(source["cash"]),
                "mgj_card": money.money(source["card"]),
                "ours_total": money.money(ours["total"]),
                "ours_cash": money.money(ours["cash"]),
                "ours_card": money.money(ours["card"]),
                "diff_total": money.money(_diff(source["total"], ours["total"])),
                "diff_cash": money.money(_diff(source["cash"], ours["cash"])),
                "diff_card": money.money(_diff(source["card"], ours["card"])),
            }
        )
    performance_totals = {
        key: money.money(sum((Decimal(row[key]) for row in performance), Decimal("0.00")))
        for key in (
            "mgj_total",
            "mgj_cash",
            "mgj_card",
            "ours_total",
            "ours_cash",
            "ours_card",
            "diff_total",
            "diff_cash",
            "diff_card",
        )
    }

    commission_names = set(staff_names)
    commission_names.update(monthly_commission)
    commission_names.update(ours_commission)
    commission = []
    performance_by_name = {row["staff"]: row for row in performance}
    for name in sorted(commission_names):
        source = monthly_commission.get(
            name,
            {
                "item_commission": Decimal("0.00"),
                "card_sales_performance": Decimal("0.00"),
                "card_sales_commission": Decimal("0.00"),
            },
        )
        ours = ours_commission.get(
            name, {"item": Decimal("0.00"), "consume": Decimal("0.00"), "labor": Decimal("0.00")}
        )
        ours_total = ours["item"] + ours["consume"] + ours["labor"]
        perf = performance_by_name.get(name, {})
        commission.append(
            {
                "staff": name,
                "mgj_item_commission": money.money(source["item_commission"]),
                "ours_item": money.money(ours["item"]),
                "ours_consume": money.money(ours["consume"]),
                "ours_labor": money.money(ours["labor"]),
                "ours_total": money.money(ours_total),
                "difference": money.money(_diff(source["item_commission"], ours_total)),
                "mgj_card_sales_performance": money.money(source["card_sales_performance"]),
                "mgj_card_sales_commission": money.money(source["card_sales_commission"]),
                "mgj_ratio": _ratio(
                    source["item_commission"], Decimal(perf.get("mgj_total", "0"))
                ),
                "ours_ratio": _ratio(
                    ours_total, Decimal(perf.get("ours_total", "0"))
                ),
            }
        )
    commission_numeric = (
        "mgj_item_commission",
        "ours_item",
        "ours_consume",
        "ours_labor",
        "ours_total",
        "difference",
        "mgj_card_sales_performance",
        "mgj_card_sales_commission",
    )
    commission_totals = {
        key: money.money(sum((Decimal(row[key]) for row in commission), Decimal("0.00")))
        for key in commission_numeric
    }
    commission_totals["mgj_ratio"] = _ratio(
        Decimal(commission_totals["mgj_item_commission"]),
        Decimal(performance_totals["mgj_total"]),
    )
    commission_totals["ours_ratio"] = _ratio(
        Decimal(commission_totals["ours_total"]),
        Decimal(performance_totals["ours_total"]),
    )

    return {
        "month": month,
        "business_date": today,
        "bills_total": len(bills),
        "bills_selected": len(records),
        "bills_replayed": len(replayed),
        "bills_failed": len(failures),
        "bills": [
            {
                "bill_no": record["bill_no"],
                "status": record["status"],
                "reason": record.get("reason"),
                "detail": record.get("detail", ""),
                "order_id": record.get("order_id"),
                "repriced_lines": record.get("repriced_lines", 0),
                "mixed_designation": bool(record.get("mixed_designation")),
                "gender_defaulted": bool(record.get("gender_defaulted")),
            }
            for record in records
        ],
        "failure_counts": failure_counts,
        "failures": [
            {
                "bill_no": record["bill_no"],
                "reason": record.get("reason"),
                "detail": record.get("detail", ""),
                "order_id": record.get("order_id"),
            }
            for record in failures
        ],
        "repriced_lines": sum(record.get("repriced_lines", 0) for record in records),
        "mixed_designation": sum(
            1 for record in records if record.get("mixed_designation")
        ),
        "gender_defaulted": sum(
            1 for record in records if record.get("gender_defaulted")
        ),
        "opening": {
            "customers": len(balances["customers"]),
            "cards": len(balances["cards"]),
            "principal": money.money(sum(
                (entry["principal"] for entry in balances["cards"].values()), Decimal("0.00")
            )),
            "bonus": money.money(sum(
                (entry["bonus"] for entry in balances["cards"].values()), Decimal("0.00")
            )),
            "skipped": balances["skipped"],
            "imported": opening_import["imported"],
            "batch_id": opening_import["batch_id"],
            "error_rows": opening_import["error_rows"],
        },
        "payments": {
            "rows": payment_rows,
            "totals": payments_totals,
            "card_split": card_split,
        },
        "performance": performance,
        "performance_totals": performance_totals,
        "performance_source_available": source_performance is not None,
        "performance_invalid_numbers": (
            source_performance.get("invalid_numbers", 0) if source_performance else 0
        ),
        "performance_empty_staff": (
            source_performance.get("empty_staff", 0) if source_performance else 0
        ),
        "commission": commission,
        "commission_totals": commission_totals,
        "commission_invalid_numbers": commission_invalid_numbers,
        "employee_label_skips": employee_skips,
        "blockers": blockers,
        "notes": [
            "所有单据都建在本系统当前营业日 " + today + "，没有使用 paper_date 补录；"
            "所以业绩和提成落在今天，而不是 " + month + "。",
            "期初余额按整月卡金/赠金合计建卡，月末归零；单笔本金/赠送金拆分以本系统扣款顺序为准。",
            "指定按单级 designated 回放；美管加按项目行/员工标记，混合指定的单只计数，不改变单级指定。",
            "卡销售业绩（开卡、充值）没有回放；美管加的 card_sales_performance / card_sales_commission 只作参照。",
            "员工角色统一使用默认非老板角色 " + DEFAULT_STAFF_ROLE + "（默认角色：老板、店长、发型师、助理、前台）。",
            "业绩对比使用 get_staff_report 当天的 item_performance / cash_performance / card_performance；"
            "提成对比使用 list_commissions 当天 item/consume/labor 合计。",
            "美管加 item_commission 是整月合计，包含失败或未回放（--limit）的单；提成差额包含这些单。",
            "美管加业绩来源是 ep_super_operation_report_search__t3，只统计已回放流水单号。",
            "提成方案完全来自输入 scheme.json，工具不推导规则。",
            "card_discount_mismatch 是本系统对非手工改价的可打折行套用卡折扣、美管加按行实收造成的规则差异，不是工具 bug。",
            "退款单不回放：负的支付行仍按 invalid_payment_amount 失败。",
        ],
    }


def render_report(result):
    """把结果渲染成不含顾客姓名、手机号的 Markdown。"""
    def cell(value):
        return _text(value).replace("|", "\\|").replace("\n", " ")

    lines = [
        "# 美管加回放对比报告",
        "",
        "- 月份：`" + result["month"] + "`",
        "- 本系统营业日：`" + result["business_date"] + "`",
        "- 单据：整月 " + str(result["bills_total"]) + "，本次 " + str(result["bills_selected"])
        + "，成功 " + str(result["bills_replayed"]) + "，失败 " + str(result["bills_failed"]),
        "- 改价项目行：" + str(result["repriced_lines"]),
        "- 混合指定单：" + str(result["mixed_designation"]),
        "- 性别默认 male：" + str(result["gender_defaulted"]),
        "",
        "## 失败原因",
        "",
    ]
    if result["opening"]["skipped"]:
        lines.insert(
            8,
            "- 期初跳过："
            + "，".join(
                key + " " + str(value)
                for key, value in sorted(result["opening"]["skipped"].items())
            ),
        )
    if result["failure_counts"]:
        lines.extend(["| 原因 | 笔数 | 单号例子（最多 10 个） |", "|---|---:|---|"])
        for reason in sorted(result["failure_counts"]):
            entry = result["failure_counts"][reason]
            lines.append(
                "| `" + reason + "` | " + str(entry["count"]) + " | "
                + "、".join(entry["examples"]) + " |"
            )
    else:
        lines.append("无失败单据。")
    if result["failures"]:
        lines.extend(
            [
                "",
                "### 失败明细",
                "",
                "| 单号 | 原因 | 详情 |",
                "|---|---|---|",
            ]
        )
        for failure in result["failures"][:10]:
            lines.append(
                "| " + cell(failure["bill_no"]) + " | `" + cell(failure["reason"])
                + "` | " + cell(failure["detail"]) + " |"
            )
    lines.extend(
        [
            "",
            "## 支付方式",
            "",
            "| 方式 | 美管加 | 本系统 | 差额（美管加 - 本系统） |",
            "|---|---:|---:|---:|",
        ]
    )
    for row in result["payments"]["rows"]:
        lines.append(
            "| " + row["method"] + " | " + row["source_total"] + " | "
            + row["ours_total"] + " | " + row["difference"] + " |"
        )
    payments_total = result["payments"]["totals"]
    lines.append(
        "| 合计 | " + payments_total["source_total"] + " | "
        + payments_total["ours_total"] + " | " + payments_total["difference"] + " |"
    )
    card = result["payments"]["card_split"]
    lines.extend(
        [
            "",
            "### 卡本金 / 赠送金拆分",
            "",
            "| 口径 | 美管加 | 本系统 |",
            "|---|---:|---:|",
            "| 卡本金 | " + card["source_principal"] + " | " + card["ours_principal"] + " |",
            "| 赠送金 | " + card["source_bonus"] + " | " + card["ours_bonus"] + " |",
            "",
            "拆分不一致的单：" + str(card["difference_count"]) + "。",
        ]
    )
    if card["examples"]:
        lines.extend(
            [
                "",
                "| 单号 | 美管加 本金/赠送金 | 本系统 本金/赠送金 |",
                "|---|---:|---:|",
            ]
        )
        for example in card["examples"]:
            lines.append(
                "| " + example["bill_no"] + " | " + example["source_principal"] + " / "
                + example["source_bonus"] + " | " + example["ours_principal"] + " / "
                + example["ours_bonus"] + " |"
            )
    lines.extend(
        [
            "",
            "## 业绩",
            "",
        ]
    )
    if not result["performance_source_available"]:
        lines.extend(["美管加业绩视图不可用。", ""])
    if result["performance_invalid_numbers"] or result["performance_empty_staff"]:
        lines.extend(
            [
                "美管加业绩数字为空或无法解析："
                + str(result["performance_invalid_numbers"])
                + " 个，按 0 计；员工姓名为空："
                + str(result["performance_empty_staff"])
                + " 行。",
                "",
            ]
        )
    lines.extend(
        [
            "| 员工 | 美管加总业绩 | 现金类 | 卡扣 | 本系统总业绩 | 现金类 | 卡扣 |"
            " 差额总业绩 | 差额现金类 | 差额卡扣 |",
            "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
        ]
    )
    for row in result["performance"]:
        lines.append(
            "| " + row["staff"] + " | " + row["mgj_total"] + " | " + row["mgj_cash"]
            + " | " + row["mgj_card"] + " | " + row["ours_total"] + " | "
            + row["ours_cash"] + " | " + row["ours_card"] + " | " + row["diff_total"]
            + " | " + row["diff_cash"] + " | " + row["diff_card"] + " |"
        )
    performance_total = result["performance_totals"]
    lines.append(
        "| 合计 | " + performance_total["mgj_total"] + " | "
        + performance_total["mgj_cash"] + " | " + performance_total["mgj_card"]
        + " | " + performance_total["ours_total"] + " | "
        + performance_total["ours_cash"] + " | " + performance_total["ours_card"]
        + " | " + performance_total["diff_total"] + " | "
        + performance_total["diff_cash"] + " | " + performance_total["diff_card"] + " |"
    )
    lines.extend(
        [
            "",
            "## 提成",
            "",
            "| 员工 | 美管加项目提成 | 本系统项目 | 耗卡 | 手工费 | 本系统合计 | 差额 |"
            " 美管加卡销售业绩 | 美管加卡销售提成 | 美管加提成/业绩 | 本系统提成/业绩 |",
            "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
        ]
    )
    for row in result["commission"]:
        lines.append(
            "| " + row["staff"] + " | " + row["mgj_item_commission"] + " | "
            + row["ours_item"] + " | " + row["ours_consume"] + " | " + row["ours_labor"]
            + " | " + row["ours_total"] + " | " + row["difference"] + " | "
            + row["mgj_card_sales_performance"] + " | " + row["mgj_card_sales_commission"]
            + " | " + row["mgj_ratio"] + " | " + row["ours_ratio"] + " |"
        )
    commission_total = result["commission_totals"]
    lines.append(
        "| 合计 | " + commission_total["mgj_item_commission"] + " | "
        + commission_total["ours_item"] + " | " + commission_total["ours_consume"]
        + " | " + commission_total["ours_labor"] + " | "
        + commission_total["ours_total"] + " | " + commission_total["difference"]
        + " | " + commission_total["mgj_card_sales_performance"] + " | "
        + commission_total["mgj_card_sales_commission"] + " | "
        + commission_total["mgj_ratio"] + " | " + commission_total["ours_ratio"] + " |"
    )
    lines.extend(
        [
            "",
            "本次失败 " + str(result["bills_failed"])
            + " 单未回放；美管加 item_commission 是整月合计，差额包含这些单。",
        ]
    )
    if result["commission_invalid_numbers"]:
        lines.append(
            "美管加提成/卡销售数字为空或无法解析："
            + str(result["commission_invalid_numbers"])
            + " 个，按 0 计。"
        )
    lines.extend(["", "## 说明", ""])
    for note in result["notes"]:
        lines.append("- " + note)
    if result["employee_label_skips"]:
        lines.append(
            "- 员工标签跳过："
            + "，".join(
                key + " " + str(value)
                for key, value in sorted(result["employee_label_skips"].items())
            )
        )
    if result["blockers"]:
        lines.extend(["", "### 阻塞", ""])
        for blocker in result["blockers"]:
            lines.append(
                "- " + cell(blocker.get("phase", "")) + " `"
                + cell(blocker.get("op", "")) + "`："
                + cell(blocker.get("detail", ""))
            )
    lines.append("")
    return "\n".join(lines)


def _write_result(result, out):
    out = Path(out)
    out.mkdir(parents=True, exist_ok=True)
    (out / "replay-report.md").write_text(render_report(result), encoding="utf-8")
    (out / "replay-result.json").write_text(
        json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def _blocker_result(month, today, bills, blockers, employee_skips):
    balances = {"customers": {}, "cards": {}, "skipped": {}}
    imported = {
        "imported": False,
        "batch_id": None,
        "error_rows": 0,
        "rerun_suspected": False,
        "rerun_reason": "",
    }
    result = build_result(
        month,
        today,
        bills,
        [],
        balances,
        imported,
        None,
        {},
        {},
        {},
        [],
        employee_skips,
        blockers,
        0,
    )
    result["notes"].append("回放已中止，详见阻塞。")
    return result


def run(db_path, month, api, account, password, scheme_path, out_dir, limit=None):
    """执行安装、期初导入、逐单回放并写出报告。"""
    if not MONTH_PATTERN.match(month):
        raise RuntimeError("月份格式是 YYYY-MM")
    if limit is not None and limit < 0:
        raise RuntimeError("--limit 不能是负数")
    with open(scheme_path, encoding="utf-8") as source:
        scheme = json.load(source)
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    conn = _readonly_connect(db_path)
    bills = []
    employees = None
    today = ""
    try:
        try:
            bills = load_month_bills(conn, month)
            balances = opening_balances(conn, month)
            employees = monthly_employees(conn, month)
        except Exception as error:
            raise PhaseError("phase 2", "read_archive", str(error)) from error

        client = Client(api)
        phase1 = PhaseClient(client, "phase 1")
        phase_call("phase 1", "login", lambda: phase1.login(account, password))
        settings = phase_call(
            "phase 1", "get_settings", lambda: phase1.op("get_settings", {})
        )
        today = _text(settings.get("business_date")).strip()
        if today == "":
            raise PhaseError("phase 1", "get_settings", "没有返回 business_date")

        positions = phase_call(
            "phase 1", "setup_positions", lambda: setup_positions(phase1)
        )
        levels = phase_call(
            "phase 1",
            "setup_levels",
            lambda: setup_levels(phase1, employees["staff"]),
        )
        staff_by_name = phase_call(
            "phase 1",
            "setup_staff",
            lambda: setup_staff(phase1, employees["staff"], levels, positions),
        )
        items_by_name = phase_call(
            "phase 1", "setup_items", lambda: setup_items(phase1, positions)
        )
        items_by_name, categories_by_name = phase_call(
            "phase 1",
            "setup_categories",
            lambda: setup_categories(
                phase1, scheme.get("categories") or [], items_by_name, positions
            ),
        )
        phase_call(
            "phase 1",
            "apply_settings",
            lambda: apply_settings(phase1, scheme.get("settings") or {}, today),
        )
        normalized = phase_call(
            "phase 1",
            "resolve_scheme",
            lambda: resolve_scheme(
                scheme.get("commission") or {},
                {name: staff["id"] for name, staff in staff_by_name.items()},
                {name: item["id"] for name, item in items_by_name.items()},
                positions,
                {name: category["id"] for name, category in categories_by_name.items()},
            ),
        )
        phase_call(
            "phase 1",
            "save_commission_scheme",
            lambda: phase1.op(
                "save_commission_scheme",
                {"effective_from": today, "scheme": normalized},
            ),
        )

        phase2 = PhaseClient(client, "phase 2")
        phase_call(
            "phase 2", "rerun_guard", lambda: ensure_fresh(phase2, today)
        )
        sheets, _principal, _bonus = opening_workbook(balances)
        imported = phase_call(
            "phase 2",
            "import_opening",
            lambda: import_opening(phase2, sheets, out),
        )
        phase_call(
            "phase 2",
            "import_fresh_check",
            lambda: check_import_fresh(imported),
        )
        card_map = {}
        for customer_id, card_type in balances["cards"]:
            source_key = "R" + customer_id + "-" + card_type
            if source_key in imported["cards"]:
                card_map[(customer_id, card_type)] = imported["cards"][source_key]
        context = {
            "customer_map": imported["customers"],
            "card_map": card_map,
            "items_by_name": items_by_name,
            "staff_by_name": staff_by_name,
            "deals": {},
            "deals_by_name": phase_call(
                "phase 2",
                "list_groupbuy_deals",
                lambda: load_groupbuy_deals(phase2),
            ),
        }
        selected = bills if limit is None else bills[:limit]
        records = []
        for bill in selected:
            try:
                records.append(replay_bill(client, bill, context))
            except Exception as error:  # noqa: BLE001 - 单张单不能中断整批
                records.append(
                    {
                        "bill_no": bill["bill_no"],
                        "status": "failed",
                        "reason": "error_" + type(error).__name__,
                        "detail": str(error),
                        "order_id": None,
                        "repriced_lines": 0,
                        "mixed_designation": False,
                        "gender_defaulted": False,
                        "source_payments": bill["payments"],
                        "our_payments": [],
                        "our_groupbuy": [],
                    }
                )
        replayed_bills = {
            record["bill_no"] for record in records if record["status"] == "replayed"
        }
        source_performance = _source_performance(conn, month, replayed_bills)
        ours_performance = _ours_performance(client, today)
        ours_commission = _ours_commissions(_all_commissions(client, today))
        result = build_result(
            month,
            today,
            bills,
            records,
            balances,
            imported,
            source_performance,
            ours_performance,
            employees["commission"],
            ours_commission,
            list(staff_by_name),
            employees["skipped"],
            [],
            employees["invalid_numbers"],
        )
        if limit is not None:
            result["notes"].append(
                "本次只回放前 " + str(limit) + " 张单；期初余额仍按整月计算。"
            )
        _write_result(result, out)
        return result
    except PhaseError as error:
        blocker = {
            "phase": error.phase,
            "op": error.op,
            "status": error.status,
            "detail": error.detail,
        }
        result = _blocker_result(
            month,
            today,
            bills,
            [blocker],
            employees["skipped"] if employees else {},
        )
        _write_result(result, out)
        raise RuntimeError(error.phase + " " + error.op + "：" + error.detail) from error
    finally:
        conn.close()
