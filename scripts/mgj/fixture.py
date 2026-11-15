"""构造与美管加存档同名表、列和取值的合成 SQLite，供测试和本地冒烟使用。"""

import sqlite3
from pathlib import Path

SCHEMA = """
CREATE TABLE archive_members (
    customer_id TEXT PRIMARY KEY,
    name TEXT,
    mobile TEXT,
    gender TEXT,
    current_points INTEGER,
    remark TEXT
);
CREATE TABLE archive_member_cards (
    customer_id TEXT,
    card_id TEXT PRIMARY KEY,
    card_name TEXT,
    card_balance NUMERIC,
    gifted_balance NUMERIC,
    card_state TEXT,
    note TEXT
);
CREATE TABLE archive_member_packages (
    customer_id TEXT,
    package_instance_id TEXT PRIMARY KEY,
    item_name TEXT,
    remaining_quantity NUMERIC,
    remaining_amount NUMERIC,
    valid_until TEXT,
    total_quantity NUMERIC
);
CREATE TABLE ep_api_shair_service_item_get (
    itemId INTEGER,
    name TEXT,
    serviceItemPrice TEXT,
    status INTEGER,
    is_latest_copy INTEGER
);
CREATE TABLE ep_discount_rule_get_card_type_and_ori_discount (
    cardTypeName TEXT,
    discount TEXT,
    is_latest_copy INTEGER
);
"""

INDEPENDENT_SCHEMA = """
CREATE TABLE ep_member_get_mem_count (
    value INTEGER,
    is_latest_copy INTEGER,
    capture_id TEXT,
    phone_like TEXT
);
CREATE TABLE ep_report_shop_treat_surplus (
    套餐金总额 NUMERIC,
    普通项目总额 NUMERIC,
    普通项目次数 NUMERIC,
    赠送金总额 NUMERIC,
    赠送项目次数 NUMERIC,
    项目结余总额 NUMERIC,
    项目结余次数 NUMERIC,
    is_latest_copy INTEGER,
    capture_id TEXT,
    phone_like TEXT
);
CREATE TABLE business_card_daily_summary (
    summary_date TEXT,
    ending_card_balance TEXT,
    ending_gift_balance TEXT,
    capture_id TEXT,
    phone_like TEXT
);
"""

PROPOSAL_SCHEMA = """
CREATE TABLE ep_shop_proposal_card_types (
    cardtypename TEXT,
    discount TEXT,
    note TEXT
);
"""

REPLAY_SCHEMA = """
CREATE TABLE archive_visits (
    bill_id TEXT,
    bill_no TEXT,
    occurred_at TEXT,
    source TEXT,
    customer_kind TEXT,
    customer_id TEXT,
    customer_name TEXT,
    mobile TEXT,
    gender TEXT,
    card_type TEXT,
    counted_visit NUMERIC,
    visit_seq NUMERIC,
    items TEXT,
    item_classes TEXT,
    item_count NUMERIC,
    item_amount NUMERIC,
    consume_fee NUMERIC,
    actual_income NUMERIC,
    payment_methods TEXT,
    online_platform TEXT,
    staff TEXT,
    designated NUMERIC,
    designated_staff TEXT,
    main_employee_id TEXT,
    main_employee_name TEXT,
    card_balance_before NUMERIC,
    remark TEXT
);
CREATE TABLE business_transaction_payments (
    capture_id TEXT,
    read_key TEXT,
    source_endpoint TEXT,
    snapshot_status TEXT,
    captured_at TEXT,
    transaction_id TEXT,
    occurred_at TEXT,
    shop_id TEXT,
    customer_label TEXT,
    transaction_amount_displayed TEXT,
    posted_amount_displayed TEXT,
    payment_index NUMERIC,
    payment_method TEXT,
    payment_amount_displayed TEXT,
    source_class TEXT,
    is_cash_fee NUMERIC,
    payment_text TEXT
);
CREATE TABLE business_employee_commission_monthly (
    capture_id TEXT,
    read_key TEXT,
    source_endpoint TEXT,
    captured_at TEXT,
    window_label TEXT,
    is_stale_fallback NUMERIC,
    source_record_id TEXT,
    period_month TEXT,
    employee_label TEXT,
    source_employee_id TEXT,
    employment_status TEXT,
    base_salary TEXT,
    salary_total TEXT,
    item_total TEXT,
    item_commission TEXT,
    card_sales_performance TEXT,
    card_sales_commission TEXT,
    product_performance TEXT,
    product_commission TEXT,
    package_performance TEXT,
    package_commission TEXT,
    annual_card_performance TEXT,
    annual_card_commission TEXT,
    hidden_commission TEXT,
    recruit_commission TEXT
);
CREATE TABLE ep_super_operation_report_search__t3 (
    is_latest_copy INTEGER,
    "员工项目业绩流水·消费时间" TEXT,
    "员工项目业绩流水·流水单号" TEXT,
    "员工项目业绩流水·总业绩" TEXT,
    "员工项目业绩流水·现金类业绩" TEXT,
    "员工项目业绩流水·卡扣业绩" TEXT,
    "员工项目业绩流水·员工姓名" TEXT
);
"""

MEMBERS = [
    ("C001", "张三", "138 0013 1234", "男", 50, "synthetic"),
    ("C002", "李四", "12345", "女", 30, "synthetic"),
    ("C003", "王五", "", "其他", 70, "synthetic"),
    ("C004", "赵六", "13700139999", "", 0, "synthetic"),
    ("C005", "顾客五", "13900130000", "女", 20, "synthetic"),
    ("C006", "顾客六", "13600130000", "男", 0, "synthetic"),
]

CARDS = [
    ("C001", "K001", "储值卡A", 1000.50, 200, "正常", "synthetic"),
    ("C001", "K002", "储值卡A", 5569.5999999, 0, "正常", "synthetic"),
    ("C002", "K003", "剪发次卡50-70", 500, 50, "正常", "synthetic"),
    ("C002", "K004", "无折扣卡", 0, 0, "正常", "synthetic"),
    ("C005", "K005", "储值卡A", 0, 0, "来源状态 0/失效标记 1", "synthetic"),
    ("C006", "K007", "零折卡", 0, 0, "正常", "synthetic"),
    ("C006", "K008", "十折卡", 0, 0, "正常", "synthetic"),
]

ORPHAN_CARD = ("C999", "K006", "储值卡A", 10, 0, "正常", "synthetic")

PACKAGES = [
    ("C001", "100", "1002-洗吹-造型", 3, 300, "2030-01-31", 10),
    ("C001", "101", "1002-洗吹-造型", 2, 100, "2030-02-30", 10),
    ("C001", "102", "1002-洗吹-造型", 1, 0, "2030-03-01", 10),
    ("C001", "400", "1002-洗吹-造型", 0, 0, "2030-01-31", 10),
    ("C002", "200", "9999-套餐专属项目", 2, 80, "2031-12-31", 5),
    ("C006", "300", "1003-护理", 2, None, "", 5),
]

ITEMS = [
    (1002, "洗吹-造型", '{"price": 120}', 1, 1),
    (1003, "护理", '{"price": "88.00"}', 1, 1),
    (1004, "剪发", '{"price": 5569.5999999}', 1, 1),
    (1004, "剪发旧", '{"price": 1}', 0, 0),
    (1005, "剪发", '{"price": 5569.5999999}', 1, 1),
]

DISCOUNTS = [
    ("储值卡A", "8.5", 1),
    ("储值卡A", "9", 0),
    ("剪发次卡50-70", "5", 1),
    ("零折卡", "0", 1),
    ("十折卡", "10", 1),
]

REPLAY_MONTH = "2026-09"

REPLAY_STAFF = [
    ("8 号 首席 [张岩]", "100.00", "0.00", "0.00"),
    ("21 号 助理 [李锦]", "50.00", "0.00", "0.00"),
    ("9 号 总监 [浩宇]", "80.00", "300.00", "30.00"),
]

REPLAY_BILLS = [
    {
        "bill_no": "B001",
        "occurred_at": "2026-09-01 10:00:00",
        "customer_kind": "会员",
        "customer_id": "C001",
        "gender": "男",
        "card_type": "无折扣卡",
        "items": "洗吹-造型 ¥60、护理 ¥50",
        "staff": "护理：张岩(指定)、李锦；洗吹-造型：张岩",
        "designated": 1,
        "payments": [("卡金", "60.00"), ("赠金", "50.00")],
    },
    {
        "bill_no": "B002",
        "occurred_at": "2026-09-02 10:00:00",
        "customer_kind": "散客",
        "customer_id": "G002",
        "gender": "女",
        "card_type": "",
        "items": "剪发 ¥70",
        "staff": "剪发：张岩(指定)",
        "designated": 1,
        "payments": [("微信支付", "70.00")],
    },
    {
        "bill_no": "B003",
        "occurred_at": "2026-09-03 10:00:00",
        "customer_kind": "散客",
        "customer_id": "G003",
        "gender": "男",
        "card_type": "",
        "items": "洗吹-造型 ¥80",
        "staff": "洗吹-造型：浩宇",
        "designated": 0,
        "payments": [("点评支付", "80.00")],
    },
    {
        "bill_no": "B004",
        "occurred_at": "2026-09-04 10:00:00",
        "customer_kind": "散客",
        "customer_id": "G004",
        "gender": "女",
        "card_type": "",
        "items": "护理 ¥88",
        "staff": "护理：李锦",
        "designated": 0,
        "payments": [("抖音团购", "88.00")],
    },
    {
        "bill_no": "B005",
        "occurred_at": "2026-09-05 10:00:00",
        "customer_kind": "散客",
        "customer_id": "G005",
        "gender": "男",
        "card_type": "",
        "items": "洗吹-造型 ¥30、洗吹-造型 ¥40",
        "staff": "洗吹-造型：张岩、李锦；洗吹-造型：浩宇",
        "designated": 0,
        "payments": [("现金", "70.00")],
    },
    {
        "bill_no": "B006",
        "occurred_at": "2026-09-06 10:00:00",
        "customer_kind": "散客",
        "customer_id": "G006",
        "gender": "其他",
        "card_type": "",
        "items": "洗吹-造型 ¥68、护理 ¥88",
        "staff": "洗吹-造型：浩宇；护理：张岩、李锦",
        "designated": 0,
        "payments": [("支付宝", "156.00")],
    },
    {
        "bill_no": "B007",
        "occurred_at": "2026-09-07 10:00:00",
        "customer_kind": "散客",
        "customer_id": "G007",
        "gender": "男",
        "card_type": "",
        "items": "剪发 ¥100",
        "staff": "剪发：浩宇",
        "designated": 0,
        "payments": [("银联", "100.00")],
    },
    {
        "bill_no": "B008",
        "occurred_at": "2026-09-08 10:00:00",
        "customer_kind": "散客",
        "customer_id": "G008",
        "gender": "女",
        "card_type": "",
        "items": "护理 ¥60",
        "staff": "护理：李锦",
        "designated": 0,
        "payments": [("微信支付", "0.00"), ("现金", "60.00")],
    },
    {
        "bill_no": "B009",
        "occurred_at": "2026-09-09 10:00:00",
        "customer_kind": "会员",
        "customer_id": "C001",
        "gender": "男",
        "card_type": "无折扣卡",
        "items": "护理 ¥30",
        "staff": "护理：张岩",
        "designated": 0,
        "payments": [("卡金", "30.00")],
    },
    {
        "bill_no": "B010",
        "occurred_at": "2026-09-10 10:00:00",
        "customer_kind": "会员",
        "customer_id": "C001",
        "gender": "男",
        "card_type": "储值卡A",
        "items": "洗吹-造型 ¥50、护理 ¥60",
        "staff": "洗吹-造型：张岩；护理：李锦",
        "designated": 0,
        "payments": [("卡金", "80.00"), ("赠金", "30.00")],
    },
]

REPLAY_PERFORMANCE = [
    ("B001", "2026-09-01", "张岩", "85.00", "0.00", "85.00"),
    ("B001", "2026-09-01", "李锦", "25.00", "0.00", "25.00"),
    ("B002", "2026-09-02", "张岩", "70.00", "70.00", "0.00"),
    ("B003", "2026-09-03", "浩宇", "80.00", "0.00", "0.00"),
    ("B004", "2026-09-04", "李锦", "88.00", "0.00", "0.00"),
    ("B005", "2026-09-05", "张岩", "15.00", "15.00", "0.00"),
    ("B005", "2026-09-05", "李锦", "15.00", "15.00", "0.00"),
    ("B005", "2026-09-05", "浩宇", "40.00", "40.00", "0.00"),
    ("B006", "2026-09-06", "浩宇", "68.00", "68.00", "0.00"),
    ("B006", "2026-09-06", "张岩", "44.00", "44.00", "0.00"),
    ("B006", "2026-09-06", "李锦", "44.00", "44.00", "0.00"),
    ("B007", "2026-09-07", "浩宇", "100.00", "100.00", "0.00"),
    ("B008", "2026-09-08", "李锦", "60.00", "60.00", "0.00"),
    ("B009", "2026-09-09", "张岩", "30.00", "0.00", "30.00"),
    ("B010", "2026-09-10", "张岩", "50.00", "0.00", "50.00"),
    ("B010", "2026-09-10", "李锦", "60.00", "0.00", "60.00"),
]


def build_fixture(path, force=False, orphan=False, independent=True, proposal=True):
    """写出合成 SQLite；已存在路径只有 force 时才覆盖。"""
    path = Path(path)
    if path.exists():
        if not force:
            raise FileExistsError(f"fixture 已存在：{path}；如需覆盖请加 --force")
        path.unlink()
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    try:
        conn.executescript(SCHEMA)
        if proposal:
            conn.executescript(PROPOSAL_SCHEMA)
        if independent:
            conn.executescript(INDEPENDENT_SCHEMA)
            conn.execute(
                "INSERT INTO ep_member_get_mem_count VALUES (999, 0, 'cap-old', '13900000000')"
            )
            conn.execute(
                "INSERT INTO ep_member_get_mem_count VALUES (20, 1, 'cap-new', '13800131234')"
            )
            conn.execute(
                "INSERT INTO ep_report_shop_treat_surplus VALUES (1, 2, 3, 4, 5, 6, 7, 0, 'cap-old', '13900000000')"
            )
            conn.execute(
                "INSERT INTO ep_report_shop_treat_surplus VALUES (1000, 2000, 7, 200, 3, 3000, 10, 1, 'cap-new', '13800131234')"
            )
            conn.execute(
                "INSERT INTO business_card_daily_summary VALUES ('2026-01-01', '100.00', '10.00', 'cap-old', '13900000000')"
            )
            conn.execute(
                "INSERT INTO business_card_daily_summary VALUES ('2026-02-01', '7070.10', '250.00', 'cap-new', '13800131234')"
            )
        conn.executemany("INSERT INTO archive_members VALUES (?,?,?,?,?,?)", MEMBERS)
        cards = list(CARDS)
        if orphan:
            cards.append(ORPHAN_CARD)
        conn.executemany("INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)", cards)
        conn.executemany("INSERT INTO archive_member_packages VALUES (?,?,?,?,?,?,?)", PACKAGES)
        conn.executemany("INSERT INTO ep_api_shair_service_item_get VALUES (?,?,?,?,?)", ITEMS)
        conn.executemany(
            "INSERT INTO ep_discount_rule_get_card_type_and_ori_discount VALUES (?,?,?)",
            DISCOUNTS,
        )
        conn.executescript(REPLAY_SCHEMA)
        visits = []
        payments = []
        for index, bill in enumerate(REPLAY_BILLS, start=1):
            visits.append(
                (
                    "bill-" + bill["bill_no"],
                    bill["bill_no"],
                    bill["occurred_at"],
                    "synthetic",
                    bill["customer_kind"],
                    bill["customer_id"],
                    "合成顾客",
                    "",
                    bill["gender"],
                    bill["card_type"],
                    1,
                    index,
                    bill["items"],
                    "",
                    0,
                    0,
                    0,
                    0,
                    "",
                    "",
                    bill["staff"],
                    bill["designated"],
                    "",
                    "",
                    "",
                    0,
                    "synthetic",
                )
            )
            for payment_index, (method, amount) in enumerate(bill["payments"], start=1):
                payments.append(
                    (
                        "cap-" + bill["bill_no"],
                        "read-" + bill["bill_no"],
                        "synthetic",
                        "ok",
                        bill["occurred_at"],
                        bill["bill_no"],
                        bill["occurred_at"],
                        "shop",
                        "",
                        amount,
                        amount,
                        payment_index,
                        method,
                        amount,
                        "",
                        0,
                        method,
                    )
                )
        conn.executemany(
            "INSERT INTO archive_visits VALUES (" + ",".join("?" for _ in range(27)) + ")",
            visits,
        )
        conn.executemany(
            "INSERT INTO business_transaction_payments VALUES ("
            + ",".join("?" for _ in range(17))
            + ")",
            payments,
        )
        conn.executemany(
            'INSERT INTO business_employee_commission_monthly '
            '(period_month, employee_label, item_commission, card_sales_performance, card_sales_commission) '
            "VALUES (?,?,?,?,?)",
            [(REPLAY_MONTH, label, item, card_perf, card_comm) for label, item, card_perf, card_comm in REPLAY_STAFF],
        )
        conn.executemany(
            'INSERT INTO ep_super_operation_report_search__t3 '
            '(is_latest_copy, "员工项目业绩流水·消费时间", "员工项目业绩流水·流水单号", '
            '"员工项目业绩流水·总业绩", "员工项目业绩流水·现金类业绩", '
            '"员工项目业绩流水·卡扣业绩", "员工项目业绩流水·员工姓名") '
            "VALUES (1,?,?,?,?,?,?)",
            [
                (consumed_at, bill_no, total, cash, card, staff)
                for bill_no, consumed_at, staff, total, cash, card in REPLAY_PERFORMANCE
            ],
        )
        conn.commit()
    finally:
        conn.close()
    return path
