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
        conn.commit()
    finally:
        conn.close()
    return path
