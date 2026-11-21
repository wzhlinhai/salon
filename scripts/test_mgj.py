"""mgj 工具的合成存档测试；不读取任何真实美管加文件。"""

import contextlib
import io
import json
import sqlite3
import threading
import unittest
import zipfile
from decimal import Decimal
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest import mock
from xml.etree import ElementTree as ET

from mgj import extract, fixture, load, replay, seed
from mgj.__main__ import main
from mgj.client import Client

MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
REL_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"


def _read_workbook(archive):
    workbook = ET.fromstring(archive.read("xl/workbook.xml"))
    rels = ET.fromstring(archive.read("xl/_rels/workbook.xml.rels"))
    targets = {relation.get("Id"): relation.get("Target") for relation in rels}
    sheets = {}
    for sheet in workbook.find(f"{{{MAIN_NS}}}sheets"):
        target = targets[sheet.get(f"{{{REL_NS}}}id")]
        root = ET.fromstring(archive.read("xl/" + target))
        rows = []
        for row in root.findall(f".//{{{MAIN_NS}}}row"):
            values = []
            for cell in row.findall(f"{{{MAIN_NS}}}c"):
                text = cell.find(f"{{{MAIN_NS}}}is/{{{MAIN_NS}}}t")
                values.append(text.text if text is not None and text.text is not None else "")
            rows.append(values)
        sheets[sheet.get("name")] = rows
    return sheets


def read_workbook(path):
    with zipfile.ZipFile(path) as archive:
        return _read_workbook(archive)


def read_workbook_bytes(data):
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        return _read_workbook(archive)


def row_by_key(rows, key):
    for row in rows[1:]:
        if row and row[0] == key:
            return row
    raise AssertionError(f"missing row {key}")


def execute(db, sql, params=()):
    conn = sqlite3.connect(db)
    try:
        conn.execute(sql, params)
        conn.commit()
    finally:
        conn.close()


def execute_many(db, sql, rows):
    conn = sqlite3.connect(db)
    try:
        conn.executemany(sql, rows)
        conn.commit()
    finally:
        conn.close()


def card_row(customer_id, card_id, name, balance, gifted, state="正常"):
    return (customer_id, card_id, name, balance, gifted, state, "synthetic")


class ExtractTests(unittest.TestCase):
    def extract_fixture(self, root, **kwargs):
        db = root / "fixture.sqlite"
        fixture.build_fixture(db, **kwargs)
        out = root / "out"
        report = extract.run(db, out)
        return out, db, report

    def extract_mutated(self, root, mutate, **kwargs):
        db = root / "fixture.sqlite"
        fixture.build_fixture(db, **kwargs)
        mutate(db)
        out = root / "out"
        report = extract.run(db, out)
        return out, db, report

    def test_customers_cards_points_and_summary(self):
        with TemporaryDirectory() as temp:
            out, _, report = self.extract_fixture(Path(temp))
            sheets = read_workbook(out / "import.xlsx")

            customers = {row[0]: row for row in sheets["顾客"][1:]}
            self.assertEqual(customers["C001"][2], "13800131234")
            self.assertEqual(customers["C002"][2], "")
            self.assertEqual(customers["C003"][3], "")

            stored = {row[0]: row for row in sheets["储值卡"][1:]}
            self.assertNotIn("K005", stored)
            self.assertEqual(stored["K001"][6], "50")
            self.assertEqual(stored["K002"][6], "")
            self.assertEqual(stored["K003"][6], "30")
            self.assertEqual(stored["K004"][6], "")
            self.assertEqual(stored["K007"][6], "0")
            self.assertEqual(stored["K008"][6], "")

            self.assertEqual(sheets["汇总"][1], ["6", "6", "7070.10", "250.00", "5"])
            self.assertEqual(report["skipped"]["invalid_phone_blanked"], 1)
            self.assertEqual(report["skipped"]["inactive_card"], 1)
            self.assertEqual(report["skipped"]["points_without_card"], 2)
            self.assertEqual(report["points_without_card_total"], "90")

    def test_money_rounding_median_and_fallback(self):
        with TemporaryDirectory() as temp:
            out, _, _ = self.extract_fixture(Path(temp))
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            items = {item["name"]: item["price"] for item in catalog["items"]}
            types = {card_type["name"]: card_type for card_type in catalog["card_types"]}

            self.assertEqual(items["剪发"], "5569.60")
            stored = row_by_key(read_workbook(out / "import.xlsx")["储值卡"], "K002")
            self.assertEqual(stored[3], "5569.60")
            self.assertEqual(types["历史次卡-洗吹-造型"]["items"][0]["unit_price"], "75.00")
            self.assertEqual(types["历史次卡-护理"]["items"][0]["unit_price"], "88.00")
            self.assertEqual(types["历史次卡-套餐专属项目"]["items"][0]["unit_price"], "40.00")

    def test_discount_mapping_and_missing_rule(self):
        with TemporaryDirectory() as temp:
            out, _, report = self.extract_fixture(Path(temp))
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            discounts = {
                card_type["name"]: card_type.get("discount_percent")
                for card_type in catalog["card_types"]
                if card_type["kind"] == "stored_value"
            }
            self.assertEqual(discounts["储值卡A"], "85.00")
            self.assertEqual(discounts["剪发次卡50-70"], "50.00")
            self.assertIsNone(discounts["零折卡"])
            self.assertIsNone(discounts["十折卡"])
            self.assertIsNone(discounts["无折扣卡"])
            self.assertEqual(report["skipped"]["card_type_without_discount_rule"], 1)

    def test_package_only_item_and_first_hyphen_split(self):
        with TemporaryDirectory() as temp:
            out, _, report = self.extract_fixture(Path(temp))
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            items = {item["name"]: item["price"] for item in catalog["items"]}
            packages = read_workbook(out / "import.xlsx")["次卡套餐"]

            self.assertEqual(items["套餐专属项目"], "0.00")
            self.assertEqual(row_by_key(packages, "P100")[3], "洗吹-造型")
            self.assertEqual(row_by_key(packages, "P200")[2], "历史次卡-套餐专属项目")
            self.assertEqual(row_by_key(packages, "P101")[6], "")
            self.assertNotIn("P400", [row[0] for row in packages[1:]])
            self.assertEqual(report["skipped"]["item_from_package_only"], 1)
            self.assertEqual(report["skipped"]["package_used_up"], 1)

    def test_extract_is_byte_deterministic(self):
        with TemporaryDirectory() as temp:
            out, db, _ = self.extract_fixture(Path(temp))
            second = Path(temp) / "second"
            extract.run(db, second)
            for name in ("catalog.json", "import.xlsx", "extract-report.json"):
                self.assertEqual((out / name).read_bytes(), (second / name).read_bytes())

    def test_orphan_card_is_still_written(self):
        with TemporaryDirectory() as temp:
            out, _, report = self.extract_fixture(Path(temp), orphan=True)
            stored = read_workbook(out / "import.xlsx")["储值卡"]
            self.assertEqual(row_by_key(stored, "K006")[1], "C999")
            self.assertEqual(report["counts"]["stored_cards"], 7)

    def test_missing_independent_views_do_not_fail(self):
        with TemporaryDirectory() as temp:
            _, _, report = self.extract_fixture(Path(temp), independent=False)
            self.assertEqual(report["mgj_reported"], "not available")

    def test_checked_in_go_sample_matches(self):
        with TemporaryDirectory() as temp:
            out, _, _ = self.extract_fixture(Path(temp))
            sample = (
                Path(__file__).resolve().parents[1]
                / "backend/internal/dataio/testdata/mgj-sample.xlsx"
            )
            self.assertEqual((out / "import.xlsx").read_bytes(), sample.read_bytes())

    def test_inactive_card_with_balance_is_imported(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    card_row("C004", "K009", "储值卡A", 381, 0, "来源状态 0/失效标记 1"),
                )
                execute(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    card_row("C004", "K010", "储值卡A", 0, 50, "来源状态 0/失效标记 1"),
                )

            out, _, report = self.extract_mutated(Path(temp), mutate)
            stored = read_workbook(out / "import.xlsx")["储值卡"]
            self.assertEqual(row_by_key(stored, "K009")[3], "381.00")
            self.assertEqual(row_by_key(stored, "K010")[4], "50.00")
            self.assertEqual(report["skipped"]["inactive_card_imported_with_balance"], 2)
            self.assertEqual(report["skipped"]["inactive_card"], 1)
            self.assertEqual(read_workbook(out / "import.xlsx")["汇总"][1][2], "7451.10")
            self.assertEqual(read_workbook(out / "import.xlsx")["汇总"][1][3], "300.00")

    def test_discount_from_proposal_source(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    card_row("C004", "K010", "6.5", 0, 0),
                )
                execute(
                    db,
                    "INSERT INTO ep_shop_proposal_card_types VALUES (?,?,?)",
                    ("6.5", "6.5", "synthetic"),
                )

            out, _, _ = self.extract_mutated(Path(temp), mutate)
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            discounts = {
                card_type["name"]: card_type.get("discount_percent")
                for card_type in catalog["card_types"]
                if card_type["kind"] == "stored_value"
            }
            self.assertEqual(discounts["6.5"], "65.00")

    def test_discount_from_name(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute_many(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    [
                        card_row("C004", "K011", "5.5特惠", 0, 0),
                        card_row("C004", "K012", "7.5", 0, 0),
                    ],
                )

            out, _, report = self.extract_mutated(Path(temp), mutate)
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            discounts = {
                card_type["name"]: card_type.get("discount_percent")
                for card_type in catalog["card_types"]
                if card_type["kind"] == "stored_value"
            }
            self.assertEqual(discounts["5.5特惠"], "55.00")
            self.assertEqual(discounts["7.5"], "75.00")
            self.assertEqual(report["skipped"]["discount_from_name"], 2)

    def test_discount_from_all_real_card_type_names(self):
        with TemporaryDirectory() as temp:
            names = ["5.5", "6.5", "7.5", "5.5特惠", "7.5特惠"]

            def mutate(db):
                execute_many(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    [card_row("C004", f"K03{index}", name, 0, 0) for index, name in enumerate(names)],
                )

            out, _, report = self.extract_mutated(Path(temp), mutate)
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            discounts = {
                card_type["name"]: card_type.get("discount_percent")
                for card_type in catalog["card_types"]
                if card_type["kind"] == "stored_value"
            }
            self.assertEqual(
                {name: discounts[name] for name in names},
                {
                    "5.5": "55.00",
                    "6.5": "65.00",
                    "7.5": "75.00",
                    "5.5特惠": "55.00",
                    "7.5特惠": "75.00",
                },
            )
            self.assertEqual(report["skipped"]["discount_from_name"], 5)

    def test_discount_proposal_view_missing_falls_back_to_name(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    card_row("C004", "K013", "6.5", 0, 0),
                )

            out, _, report = self.extract_mutated(Path(temp), mutate, proposal=False)
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            discounts = {
                card_type["name"]: card_type.get("discount_percent")
                for card_type in catalog["card_types"]
                if card_type["kind"] == "stored_value"
            }
            self.assertEqual(discounts["6.5"], "65.00")
            self.assertEqual(report["skipped"]["discount_from_name"], 1)

    def test_conflicting_discount_duplicate_aborts(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO ep_discount_rule_get_card_type_and_ori_discount VALUES (?,?,?)",
                    ("储值卡A", "9.5", 1),
                )

            with self.assertRaisesRegex(RuntimeError, "储值卡A"):
                self.extract_mutated(Path(temp), mutate)

    def test_conflicting_discount_across_sources_aborts(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO ep_shop_proposal_card_types VALUES (?,?,?)",
                    ("储值卡A", "5", "synthetic"),
                )

            with self.assertRaisesRegex(RuntimeError, "储值卡A"):
                self.extract_mutated(Path(temp), mutate)

    def test_identical_discount_duplicate_is_fine(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO ep_discount_rule_get_card_type_and_ori_discount VALUES (?,?,?)",
                    ("储值卡A", "8.5", 1),
                )

            out, _, _ = self.extract_mutated(Path(temp), mutate)
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            discounts = {
                card_type["name"]: card_type.get("discount_percent")
                for card_type in catalog["card_types"]
                if card_type["kind"] == "stored_value"
            }
            self.assertEqual(discounts["储值卡A"], "85.00")

    def test_conflicting_item_duplicate_aborts(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO ep_api_shair_service_item_get VALUES (?,?,?,?,?)",
                    (1006, "剪发", '{"price": 2}', 1, 1),
                )

            with self.assertRaisesRegex(RuntimeError, "剪发"):
                self.extract_mutated(Path(temp), mutate)

    def test_latest_copy_zero_rows_are_absent(self):
        with TemporaryDirectory() as temp:
            out, _, _ = self.extract_fixture(Path(temp))
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            items = {item["name"]: item["price"] for item in catalog["items"]}
            discounts = {
                card_type["name"]: card_type.get("discount_percent")
                for card_type in catalog["card_types"]
                if card_type["kind"] == "stored_value"
            }
            self.assertNotIn("剪发旧", items)
            self.assertEqual(discounts["储值卡A"], "85.00")

    def test_stored_card_type_name_is_truncated(self):
        with TemporaryDirectory() as temp:
            name = "长" * 45

            def mutate(db):
                execute(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    card_row("C004", "K020", name, 0, 0),
                )

            out, _, _ = self.extract_mutated(Path(temp), mutate)
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            names = {card_type["name"] for card_type in catalog["card_types"]}
            self.assertIn("长" * 40, names)
            self.assertNotIn(name, names)

    def test_stored_card_type_truncation_collision_aborts(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute_many(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    [
                        card_row("C004", "K021", "同" * 40 + "甲", 0, 0),
                        card_row("C004", "K022", "同" * 40 + "乙", 0, 0),
                    ],
                )

            with self.assertRaisesRegex(RuntimeError, "截断后重复"):
                self.extract_mutated(Path(temp), mutate)

    def test_negative_balance_is_skipped(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    card_row("C004", "K023", "储值卡A", -1, 0),
                )

            out, _, report = self.extract_mutated(Path(temp), mutate)
            stored = read_workbook(out / "import.xlsx")["储值卡"]
            self.assertNotIn("K023", [row[0] for row in stored[1:]])
            self.assertEqual(report["skipped"]["negative_balance"], 1)
            self.assertEqual(read_workbook(out / "import.xlsx")["汇总"][1][2], "7070.10")

    def test_invalid_balance_is_skipped(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute_many(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    [
                        card_row("C004", "K024", "储值卡A", "abc", 0),
                        card_row("C004", "K025", "储值卡A", 0, "NaN"),
                        card_row("C004", "K026", "储值卡A", "Infinity", 0),
                        card_row("C004", "K027", "储值卡A", None, None),
                    ],
                )

            out, _, report = self.extract_mutated(Path(temp), mutate)
            stored = read_workbook(out / "import.xlsx")["储值卡"]
            self.assertNotIn("K024", [row[0] for row in stored[1:]])
            self.assertNotIn("K025", [row[0] for row in stored[1:]])
            self.assertNotIn("K026", [row[0] for row in stored[1:]])
            self.assertEqual(row_by_key(stored, "K027")[3], "0.00")
            self.assertEqual(row_by_key(stored, "K027")[4], "0.00")
            self.assertEqual(report["skipped"]["invalid_balance"], 3)

    def test_item_invalid_price_becomes_zero(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute_many(
                    db,
                    "INSERT INTO ep_api_shair_service_item_get VALUES (?,?,?,?,?)",
                    [
                        (1006, "负价", '{"price": -1}', 1, 1),
                        (1007, "非数", '{"price": "NaN"}', 1, 1),
                        (1008, "缺价", '{"price": null}', 1, 1),
                        (1009, "无限", '{"price": "Infinity"}', 1, 1),
                    ],
                )

            out, _, report = self.extract_mutated(Path(temp), mutate)
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            items = {item["name"]: item["price"] for item in catalog["items"]}
            self.assertEqual(items["负价"], "0.00")
            self.assertEqual(items["非数"], "0.00")
            self.assertEqual(items["缺价"], "0.00")
            self.assertEqual(items["无限"], "0.00")
            self.assertEqual(report["skipped"]["invalid_item_price"], 4)

    def test_fractional_points_and_quantity_are_rejected(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO archive_members VALUES (?,?,?,?,?,?)",
                    ("C007", "顾客七", "13500130000", "男", 1.5, "synthetic"),
                )
                execute(
                    db,
                    "INSERT INTO archive_member_cards VALUES (?,?,?,?,?,?,?)",
                    card_row("C007", "K026", "储值卡A", 0, 0),
                )
                execute(
                    db,
                    "INSERT INTO archive_member_packages VALUES (?,?,?,?,?,?,?)",
                    ("C007", "500", "1003-护理", 1.5, 3, "2030-01-31", 5),
                )

            out, _, report = self.extract_mutated(Path(temp), mutate)
            stored = row_by_key(read_workbook(out / "import.xlsx")["储值卡"], "K026")
            self.assertEqual(stored[6], "")
            self.assertNotIn("P500", [row[0] for row in read_workbook(out / "import.xlsx")["次卡套餐"][1:]])
            self.assertEqual(report["skipped"]["invalid_points"], 1)
            self.assertEqual(report["skipped"]["invalid_quantity"], 1)

    def test_date_validation(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute_many(
                    db,
                    "INSERT INTO archive_member_packages VALUES (?,?,?,?,?,?,?)",
                    [
                        ("C004", "501", "1003-护理", 1, 1, "2030-01-31 12:00:00", 5),
                        ("C004", "502", "1003-护理", 1, 1, "2030-01-31T12:00:00", 5),
                        ("C004", "503", "1003-护理", 1, 1, "2030/01/31", 5),
                        ("C004", "504", "1003-护理", 1, 1, "2030-02-30", 5),
                        ("C004", "505", "1003-护理", 1, 1, "2030-1-1", 5),
                    ],
                )

            out, _, report = self.extract_mutated(Path(temp), mutate)
            packages = read_workbook(out / "import.xlsx")["次卡套餐"]
            self.assertEqual(row_by_key(packages, "P501")[6], "2030-01-31")
            self.assertEqual(row_by_key(packages, "P502")[6], "2030-01-31")
            self.assertEqual(row_by_key(packages, "P503")[6], "")
            self.assertEqual(row_by_key(packages, "P504")[6], "")
            self.assertEqual(row_by_key(packages, "P505")[6], "")
            self.assertEqual(report["skipped"]["invalid_date"], 4)

    def test_phone_stored_as_float_is_normalised(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO archive_members VALUES (?,?,?,?,?,?)",
                    ("C008", "顾客八", 13800131234.0, "男", 0, "synthetic"),
                )

            out, _, _ = self.extract_mutated(Path(temp), mutate)
            customers = {row[0]: row for row in read_workbook(out / "import.xlsx")["顾客"][1:]}
            self.assertEqual(customers["C008"][2], "13800131234")

    def test_median_uses_raw_ratios_then_quantises_once(self):
        with TemporaryDirectory() as temp:
            def mutate(db):
                execute(
                    db,
                    "INSERT INTO ep_api_shair_service_item_get VALUES (?,?,?,?,?)",
                    (1006, "微量项目", '{"price": 1}', 1, 1),
                )
                execute_many(
                    db,
                    "INSERT INTO archive_member_packages VALUES (?,?,?,?,?,?,?)",
                    [
                        ("C004", "600", "1006-微量项目", 1000, 4, "2030-01-31", 1000),
                        ("C004", "601", "1006-微量项目", 1000, 5, "2030-01-31", 1000),
                    ],
                )

            out, _, _ = self.extract_mutated(Path(temp), mutate)
            catalog = json.loads((out / "catalog.json").read_text(encoding="utf-8"))
            types = {card_type["name"]: card_type for card_type in catalog["card_types"]}
            self.assertEqual(types["历史次卡-微量项目"]["items"][0]["unit_price"], "0.00")

    def test_row_limit_aborts(self):
        with TemporaryDirectory() as temp:
            with mock.patch.object(extract, "MAX_ROWS_PER_SHEET", 2):
                with self.assertRaisesRegex(RuntimeError, "超过 2 行上限"):
                    self.extract_fixture(Path(temp))

    def test_file_size_limit_aborts(self):
        with TemporaryDirectory() as temp:
            with mock.patch.object(extract, "MAX_UPLOAD_BYTES", 1):
                with self.assertRaisesRegex(RuntimeError, "8 MiB 上限"):
                    self.extract_fixture(Path(temp))

    def test_mgj_reported_uses_allowlist_latest_and_business_summary(self):
        with TemporaryDirectory() as temp:
            out, _, report = self.extract_fixture(Path(temp))
            reported = report["mgj_reported"]
            self.assertEqual(reported["ep_member_get_mem_count"], [{"value": 20}])
            self.assertEqual(
                set(reported["ep_report_shop_treat_surplus"][0]),
                {
                    "套餐金总额",
                    "普通项目总额",
                    "普通项目次数",
                    "赠送金总额",
                    "赠送项目次数",
                    "项目结余总额",
                    "项目结余次数",
                },
            )
            self.assertEqual(
                reported["business_card_daily_summary"],
                {
                    "summary_date": "2026-02-01",
                    "ending_card_balance": "7070.10",
                    "ending_gift_balance": "250.00",
                },
            )
            self.assertEqual(report["package_remaining_total"], "10")
            raw = (out / "extract-report.json").read_text(encoding="utf-8")
            for private in ("13800131234", "13900000000", "phone_like", "capture_id"):
                self.assertNotIn(private, raw)

    def test_make_fixture_refuses_existing_path_without_force(self):
        with TemporaryDirectory() as temp:
            path = Path(temp) / "fixture.sqlite"
            fixture.build_fixture(path)
            with self.assertRaisesRegex(FileExistsError, "--force"):
                fixture.build_fixture(path)
            self.assertTrue(path.exists())
            fixture.build_fixture(path, force=True)

            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                self.assertEqual(main(["make-fixture", str(path)]), 1)
            self.assertIn("--force", stderr.getvalue())
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(main(["make-fixture", str(path), "--force"]), 0)


def totals(customers=6, stored=6, principal="7070.10", bonus="250.00", packages=5):
    return {
        "customers": customers,
        "stored_value_cards": stored,
        "principal": principal,
        "bonus": bonus,
        "count_package_cards": packages,
    }


def zero_totals():
    return totals(customers=0, stored=0, principal="0.00", bonus="0.00", packages=0)


def subtract(left, right):
    return {
        "customers": left["customers"] - right["customers"],
        "stored_value_cards": left["stored_value_cards"] - right["stored_value_cards"],
        "principal": f"{Decimal(left['principal']) - Decimal(right['principal']):.2f}",
        "bonus": f"{Decimal(left['bonus']) - Decimal(right['bonus']):.2f}",
        "count_package_cards": left["count_package_cards"] - right["count_package_cards"],
    }


class StubState:
    def __init__(self):
        self.items = []
        self.card_types = []
        self.requests = []
        self.committed = False
        self.next_id = 1

    def add_item(self, name, price="0.00", kind="service", discountable=True):
        item = {
            "id": f"item-{self.next_id}",
            "name": name,
            "kind": kind,
            "price": price,
            "discountable": discountable,
            "active": True,
            "created_at": "2026-01-01T00:00:00Z",
            "positions": [],
            "level_prices": [],
            "member_prices": [],
            "category": None,
        }
        self.next_id += 1
        self.items.append(item)
        return item

    def add_card_type(self, name, kind, discount_percent=None, items=None):
        item = {
            "id": f"type-{self.next_id}",
            "name": name,
            "kind": kind,
            "validity_kind": "none",
            "validity_date": None,
            "validity_days": None,
            "discount_percent": discount_percent,
            "active": True,
            "created_at": "2026-01-01T00:00:00Z",
            "items": items or [],
        }
        self.next_id += 1
        self.card_types.append(item)
        return item

    def detail(self):
        declared = totals()
        file_totals = totals()
        written = totals() if self.committed else zero_totals()
        rows = [
            {"sheet": "顾客", "row_number": 2, "error": None, "name": "张三", "phone": "13800131234"},
            {
                "sheet": "储值卡",
                "row_number": 5,
                "error": "卡种未配置: 未知卡",
                "source_key": "K999",
                "name": "张三",
                "phone": "13800131234",
            },
            {
                "sheet": "储值卡",
                "row_number": 6,
                "error": "卡种未配置: 未知卡",
                "source_key": "K998",
                "name": "李四",
                "phone": "12345",
            },
            {
                "sheet": "次卡套餐",
                "row_number": 3,
                "error": "同一来源卡号的项目重复",
                "source_key": "P999",
                "name": "王五",
                "phone": "13700139999",
            },
        ]
        return {
            "id": "batch-1",
            "status": "committed" if self.committed else "previewed",
            "file_name": "import.xlsx",
            "declared": declared,
            "file": file_totals,
            "written": written,
            "differences": {
                "declared_minus_file": subtract(declared, file_totals),
                "declared_minus_written": subtract(declared, written),
            },
            "rows": rows,
        }


def stub_handler(state):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            return

        def reply(self, status, payload):
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_POST(self):
            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(length)
            state.requests.append(
                {
                    "path": self.path,
                    "body": body,
                    "authorization": self.headers.get("Authorization"),
                }
            )
            if self.path == "/api/auth/login":
                payload = json.loads(body)
                if payload != {"account": "boss", "password": "pw"}:
                    return self.reply(401, {"detail": "账号或密码不正确"})
                return self.reply(200, {"token": "test-token"})
            if self.headers.get("Authorization") != "Bearer test-token":
                return self.reply(401, {"detail": "unauthenticated"})
            if self.path == "/api/import/preview":
                return self.reply(200, state.detail())
            if not self.path.startswith("/api/ops/"):
                return self.reply(404, {"detail": "not found"})
            operation = self.path[len("/api/ops/") :]
            if operation == "list_items":
                return self.reply(200, {"items": state.items})
            if operation == "create_item":
                payload = json.loads(body)
                return self.reply(200, state.add_item(payload["name"], payload["price"], payload["kind"], payload["discountable"]))
            if operation == "list_card_types":
                return self.reply(200, {"items": state.card_types})
            if operation == "create_card_type":
                payload = json.loads(body)
                names = {item["id"]: item["name"] for item in state.items}
                items = [
                    {
                        **item,
                        "item_name": names.get(item["item_id"], ""),
                    }
                    for item in payload.get("items", [])
                ]
                return self.reply(
                    200,
                    state.add_card_type(
                        payload["name"], payload["kind"], payload.get("discount_percent"), items
                    ),
                )
            if operation == "commit_import":
                state.committed = True
                return self.reply(200, state.detail())
            if operation == "get_import_batch":
                return self.reply(200, state.detail())
            return self.reply(404, {"detail": "unknown operation"})

    return Handler


class ReplayStubState:
    """回放测试用的内存后端：只实现 replay 会调用的操作。"""

    def __init__(self):
        self.positions = []
        self.levels = []
        self.staff = []
        self.roles = [
            {"id": "role-owner", "name": "老板", "is_owner": True},
            {"id": "role-manager", "name": "店长", "is_owner": False},
            {"id": "role-stylist", "name": "发型师", "is_owner": False},
            {"id": "role-assistant", "name": "助理", "is_owner": False},
            {"id": "role-front", "name": "前台", "is_owner": False},
        ]
        self.items = []
        self.item_categories = []
        self.requests = []
        self.settings = {"business_date": "2026-11-15"}
        self.import_detail = None
        self.import_committed = False
        self.import_row_error = None
        self.cards = {}
        self.card_discounts = {"储值卡A": Decimal("0.85")}
        self.orders = {}
        self.deals = []
        self.redemptions = {}
        self.open_order_calls = 0
        self.checkout_failures = {}
        self.malformed_add_order_index = None
        self.fail_operation = None
        self.initial_staff_report_rows = []
        self.staff_report_rows = []
        self.staff_report_calls = 0
        self.commission_entries = []
        self.next_id = 1

    def _id(self, prefix):
        value = f"{prefix}-{self.next_id}"
        self.next_id += 1
        return value

    def load_catalog(self, catalog):
        for item in catalog.get("items", []):
            self.add_item(item["name"], item["price"])

    def add_item(self, name, price, kind="service", discountable=True):
        item = {
            "id": self._id("item"),
            "name": name,
            "kind": kind,
            "price": price,
            "discountable": discountable,
            "labor_fee_amount": None,
            "labor_fee_percent": None,
            "duration_minutes": 0,
            "bookable": True,
            "active": True,
            "created_at": "2026-01-01T00:00:00Z",
            "positions": [],
            "level_prices": [],
            "member_prices": [],
            "category": None,
        }
        self.items.append(item)
        return item

    def position_by_name(self, name):
        return next((row for row in self.positions if row["name"] == name), None)

    def level_by_name(self, name):
        return next((row for row in self.levels if row["name"] == name), None)

    def staff_view(self, row):
        level = self.level_by_name(row["level"])
        position = self.position_by_name(row["position"])
        return {
            "id": row["id"],
            "name": row["name"],
            "number": row["number"],
            "level": {"id": level["id"], "name": level["name"]} if level else None,
            "position": {"id": position["id"], "name": position["name"]} if position else None,
            "roles": [{"id": role, "name": role} for role in row["roles"]],
            "active": True,
        }

    def item_by_id(self, item_id):
        return next((item for item in self.items if item["id"] == item_id), None)

    def order_view(self, order):
        intended = sum((Decimal(item["price"]) for item in order["items"]), Decimal("0.00"))
        net = order.get("net", format(intended.quantize(Decimal("0.01")), "f"))
        return {
            **order,
            "receivable": format(intended.quantize(Decimal("0.01")), "f"),
            "net": net,
            "discount": order.get("discount", "0.00"),
        }

    def preview(self, body):
        rest = body.split(b"\r\n\r\n", 1)[1]
        data = rest.rsplit(b"\r\n--", 1)[0]
        sheets = read_workbook_bytes(data)
        rows = []
        for row in sheets["顾客"][1:]:
            source_key = row[0]
            entity = "customer-" + source_key
            rows.append(
                {
                    "kind": "customer",
                    "source_key": source_key,
                    "entity_id": entity,
                    "error": self.import_row_error,
                }
            )
        for row in sheets["储值卡"][1:]:
            source_key = row[0]
            entity = "card-" + source_key
            self.cards[entity] = {
                "principal": Decimal(row[3]),
                "bonus": Decimal(row[4]),
                "card_type": row[2],
            }
            rows.append(
                {
                    "kind": "stored_value",
                    "source_key": source_key,
                    "entity_id": entity,
                    "error": self.import_row_error,
                }
            )
        self.import_detail = {
            "id": "batch-replay",
            "status": "previewed",
            "rows": rows,
        }
        return self.import_detail

    def detail(self):
        return {
            **self.import_detail,
            "status": "committed" if self.import_committed else "previewed",
        }

    def open_order(self, payload):
        self.open_order_calls += 1
        order_id = self._id("order")
        order = {
            "id": order_id,
            "index": self.open_order_calls,
            "customer_id": payload.get("customer_id") or self._id("guest"),
            "gender": payload["gender"],
            "designated": payload["designated"],
            "status": "open",
            "business_date": self.settings["business_date"],
            "backfill": False,
            "items": [],
            "payments": [],
            "groupbuy": [],
            "discount": "0.00",
            "net": "0.00",
            "receivable": "0.00",
        }
        self.orders[order_id] = order
        return self.order_view(order)

    def add_order_item(self, payload):
        order = self.orders[payload["order_id"]]
        if self.malformed_add_order_index == order["index"]:
            return self.order_view(order)
        source = payload["item"]
        item = self.item_by_id(source["item_id"])
        staff = []
        for entry in source["staff"]:
            position = self.positions[0] if entry["slot"] == 1 else self.positions[1]
            staff.append(
                {
                    "slot": entry["slot"],
                    "position_id": position["id"],
                    "required": entry["slot"] == 1,
                    "staff_id": entry["staff_id"],
                    "share_percent": None,
                }
            )
        order["items"].append(
            {
                "id": self._id("line"),
                "item_id": item["id"],
                "item_name": item["name"],
                "price": item["price"],
                "price_source": "standard",
                "discountable": item.get("discountable", True),
                "positions": staff,
            }
        )
        return self.order_view(order)

    def reprice(self, payload):
        order = self.orders[payload["order_id"]]
        line = next(item for item in order["items"] if item["id"] == payload["order_item_id"])
        line["price"] = payload["price"]
        line["price_source"] = "manual"
        line["reprice_reason"] = payload["reason"]
        return self.order_view(order)

    def preview_checkout(self, payload):
        order = self.orders[payload["order_id"]]
        card = self.cards.get(payload.get("card_id"))
        rate = self.card_discounts.get(card["card_type"]) if card else None
        net = Decimal("0.00")
        discount_total = Decimal("0.00")
        for item in order["items"]:
            price = Decimal(item["price"])
            discount = Decimal("0.00")
            if (
                rate is not None
                and item.get("price_source") != "manual"
                and item.get("discountable", True)
            ):
                discount = (price * (Decimal("1") - rate)).quantize(Decimal("0.01"))
            item["discount"] = format(discount, "f")
            discount_total += discount
            net += price - discount
        order["discount"] = format(discount_total, "f")
        order["net"] = format(net, "f")
        return self.order_view(order)

    def checkout(self, payload):
        order = self.orders[payload["orders"][0]["order_id"]]
        for entry in payload["orders"][0]["payments"]:
            if entry["method"] != "card":
                order["payments"].append(
                    {
                        "method": entry["method"],
                        "amount": entry["amount"],
                        "card": None,
                    }
                )
                continue
            card = self.cards[entry["card_id"]]
            amount = Decimal(entry["amount"])
            principal = min(amount, card["principal"])
            bonus = amount - principal
            if bonus > card["bonus"]:
                raise RuntimeError("卡余额不足")
            card["principal"] -= principal
            card["bonus"] -= bonus
            order["payments"].append(
                {
                    "method": "card",
                    "amount": format(amount.quantize(Decimal("0.01")), "f"),
                    "card": {
                        "card_id": entry["card_id"],
                        "principal": format(principal.quantize(Decimal("0.01")), "f"),
                        "bonus": format(bonus.quantize(Decimal("0.01")), "f"),
                    },
                }
            )
        for groupbuy in order["groupbuy"]:
            order["payments"].append(
                {
                    "method": "groupbuy",
                    "amount": groupbuy["amount"],
                    "card": None,
                }
            )
        order["status"] = "paid"
        return {"items": [self.order_view(order)]}

    def handle(self, operation, payload):
        if operation == "list_positions":
            return {"items": self.positions}
        if operation == "create_position":
            row = {"id": self._id("position"), "name": payload["name"], "active": True}
            self.positions.append(row)
            return row
        if operation == "list_levels":
            return {"items": self.levels}
        if operation == "create_level":
            row = {"id": self._id("level"), "name": payload["name"], "active": True}
            self.levels.append(row)
            return row
        if operation == "list_roles":
            return {"roles": self.roles}
        if operation == "list_staff":
            return {"staff": [self.staff_view(row) for row in self.staff]}
        if operation == "create_staff":
            row = {
                "id": self._id("staff"),
                "name": payload["name"],
                "number": payload["number"],
                "level": next(
                    item["name"] for item in self.levels if item["id"] == payload["level_id"]
                ),
                "position": next(
                    item["name"] for item in self.positions if item["id"] == payload["position_id"]
                ),
                "roles": payload["role_ids"],
            }
            self.staff.append(row)
            return self.staff_view(row)
        if operation == "update_staff":
            row = next(item for item in self.staff if item["id"] == payload["staff_id"])
            row["name"] = payload["name"]
            row["level"] = next(
                item["name"] for item in self.levels if item["id"] == payload["level_id"]
            )
            row["position"] = next(
                item["name"] for item in self.positions if item["id"] == payload["position_id"]
            )
            return self.staff_view(row)
        if operation == "list_items":
            return {"items": self.items}
        if operation == "update_item":
            item = self.item_by_id(payload["id"])
            replacement = {
                "id": item["id"],
                "created_at": item["created_at"],
                "name": payload["name"],
                "kind": payload["kind"],
                "price": payload["price"],
                "discountable": payload["discountable"],
                "labor_fee_amount": payload.get("labor_fee_amount"),
                "labor_fee_percent": payload.get("labor_fee_percent"),
                "duration_minutes": payload.get("duration_minutes", 0),
                "bookable": payload.get("bookable", True),
                "active": payload["active"],
                "positions": [
                    {
                        "slot": index,
                        "position_id": position["position_id"],
                        "required": position["required"],
                        "share_percent": None,
                    }
                    for index, position in enumerate(payload["positions"], start=1)
                ],
                "level_prices": payload.get("level_prices", []),
                "member_prices": payload.get("member_prices", []),
                "category": next(
                    (
                        {"id": category["id"], "name": category["name"]}
                        for category in self.item_categories
                        if category["id"] == payload.get("category_id")
                    ),
                    None,
                ),
            }
            item.clear()
            item.update(replacement)
            return item
        if operation == "list_item_categories":
            return {"items": self.item_categories}
        if operation == "create_item_category":
            category = {
                "id": self._id("category"),
                "name": payload["name"],
                "performance_ratio": payload.get("performance_ratio", 100),
                "active": True,
            }
            self.item_categories.append(category)
            return category
        if operation == "update_item_category":
            category = next(
                item for item in self.item_categories if item["id"] == payload["id"]
            )
            category.update(
                {
                    "name": payload["name"],
                    "performance_ratio": payload["performance_ratio"],
                    "active": payload["active"],
                }
            )
            return category
        if operation == "create_item":
            return self.add_item(
                payload["name"],
                payload["price"],
                payload.get("kind", "service"),
                payload.get("discountable", True),
            )
        if operation == "get_settings":
            return self.settings
        if operation == "change_setting":
            self.settings[payload["key"]] = payload["value"]
            return {"key": payload["key"], "value": payload["value"]}
        if operation == "save_commission_scheme":
            return {"effective_from": payload["effective_from"], "scheme": payload["scheme"]}
        if operation == "commit_import":
            self.import_committed = True
            return self.detail()
        if operation == "get_import_batch":
            return self.detail()
        if operation == "open_order":
            return self.open_order(payload)
        if operation == "list_open_orders":
            return {
                "items": [order for order in self.orders.values() if order["status"] == "open"]
            }
        if operation == "add_order_item":
            return self.add_order_item(payload)
        if operation == "reprice_order_item":
            return self.reprice(payload)
        if operation == "list_groupbuy_deals":
            return {"items": self.deals}
        if operation == "create_groupbuy_deal":
            item = self.item_by_id(payload["item_id"])
            deal = {
                "id": self._id("deal"),
                "platform": payload["platform"],
                "name": payload["name"],
                "purchase_price": payload["purchase_price"],
                "item_id": item["id"],
                "item_name": item["name"],
                "store_price": item["price"],
                "active": True,
            }
            self.deals.append(deal)
            return deal
        if operation == "redeem_coupon":
            redemption = {
                "id": self._id("redemption"),
                "platform": payload["platform"],
                "coupon_code": payload["coupon_code"],
                "key": payload["key"],
                "status": "redeemed",
            }
            self.redemptions[redemption["id"]] = redemption
            return redemption
        if operation == "link_coupon_order":
            redemption = self.redemptions[payload["redemption_id"]]
            deal = next(item for item in self.deals if item["id"] == payload["deal_id"])
            order = self.orders[payload["order_id"]]
            order["groupbuy"].append(
                {
                    "redemption_id": redemption["id"],
                    "deal_id": deal["id"],
                    "deal_name": deal["name"],
                    "platform": deal["platform"],
                    "amount": deal["store_price"],
                }
            )
            redemption["order_id"] = order["id"]
            return redemption
        if operation == "checkout_orders":
            return self.checkout(payload)
        if operation == "preview_checkout":
            return self.preview_checkout(payload)
        if operation == "get_staff_report":
            self.staff_report_calls += 1
            rows = (
                self.initial_staff_report_rows
                if self.staff_report_calls == 1
                else self.staff_report_rows
            )
            return {"rows": rows, "totals": {}}
        if operation == "list_commissions":
            limit = int(payload.get("limit") or 50)
            before = payload.get("before_id")
            start = 0
            if before:
                for index, entry in enumerate(self.commission_entries):
                    if entry["id"] == before:
                        start = index + 1
                        break
            return {
                "entries": self.commission_entries[start : start + limit],
                "total": "0.00",
                "totals": [],
            }
        raise AssertionError("unknown operation " + operation)


def replay_handler(state):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            return

        def reply(self, status, payload):
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_POST(self):
            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(length)
            state.requests.append({"path": self.path, "body": body})
            if self.path == "/api/auth/login":
                payload = json.loads(body)
                if payload != {"account": "boss", "password": "pw"}:
                    return self.reply(401, {"detail": "账号或密码不正确"})
                return self.reply(200, {"token": "test-token"})
            if self.headers.get("Authorization") != "Bearer test-token":
                return self.reply(401, {"detail": "unauthenticated"})
            if self.path == "/api/import/preview":
                return self.reply(200, state.preview(body))
            if not self.path.startswith("/api/ops/"):
                return self.reply(404, {"detail": "not found"})
            operation = self.path[len("/api/ops/") :]
            payload = json.loads(body) if body else {}
            if state.fail_operation and operation == state.fail_operation[0]:
                return self.reply(
                    state.fail_operation[1], {"detail": state.fail_operation[2]}
                )
            if operation == "checkout_orders":
                order = state.orders[payload["orders"][0]["order_id"]]
                if order["index"] in state.checkout_failures:
                    status = state.checkout_failures[order["index"]]
                    detail = "测试冲突" if status == 409 else "测试 500"
                    return self.reply(status, {"detail": detail})
            return self.reply(200, state.handle(operation, payload))

    return Handler


class ApiTests(unittest.TestCase):
    def start_stub(self, state):
        server = ThreadingHTTPServer(("127.0.0.1", 0), stub_handler(state))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return f"http://127.0.0.1:{server.server_address[1]}"

    def extract_fixture(self, root, **kwargs):
        db = root / "fixture.sqlite"
        fixture.build_fixture(db, **kwargs)
        out = root / "out"
        extract.run(db, out)
        return out

    def create_requests(self, state, operation):
        return [
            json.loads(request["body"])
            for request in state.requests
            if request["path"] == "/api/ops/" + operation
        ]

    def test_seed_creates_exact_payloads_and_is_idempotent(self):
        state = StubState()
        api = self.start_stub(state)
        with TemporaryDirectory() as temp:
            out = self.extract_fixture(Path(temp))
            first = seed.run(api, "boss", "pw", out / "catalog.json")
            second = seed.run(api, "boss", "pw", out / "catalog.json")

            self.assertEqual(len(first["created"]), 12)
            self.assertEqual(second["created"], [])
            self.assertEqual(len(second["existing"]), 12)
            self.assertEqual(second["conflicts"], [])

            items = {payload["name"]: payload for payload in self.create_requests(state, "create_item")}
            self.assertEqual(
                items["护理"],
                {
                    "name": "护理",
                    "kind": "service",
                    "price": "88.00",
                    "discountable": True,
                    "labor_fee_amount": None,
                    "labor_fee_percent": None,
                    "positions": [],
                    "level_prices": [],
                },
            )
            types = {
                payload["name"]: payload for payload in self.create_requests(state, "create_card_type")
            }
            self.assertEqual(types["储值卡A"]["discount_percent"], "85.00")
            self.assertEqual(types["储值卡A"]["items"], [])
            self.assertEqual(types["历史次卡-洗吹-造型"]["kind"], "count")
            self.assertEqual(types["历史次卡-洗吹-造型"]["items"][0]["unit_price"], "75.00")
            self.assertEqual(types["历史次卡-洗吹-造型"]["items"][0]["paid_count"], 1)
            self.assertEqual(types["历史次卡-洗吹-造型"]["items"][0]["gift_count"], 0)

    def test_seed_records_conflicts_without_updating(self):
        state = StubState()
        state.add_item("洗吹-造型", price="999.00")
        state.add_card_type("储值卡A", kind="stored_value", discount_percent="50.00")
        api = self.start_stub(state)
        with TemporaryDirectory() as temp:
            out = self.extract_fixture(Path(temp))
            summary = seed.run(api, "boss", "pw", out / "catalog.json")

            conflicts = {(item["kind"], item["name"]) for item in summary["conflicts"]}
            self.assertIn(("item", "洗吹-造型"), conflicts)
            self.assertIn(("card_type", "储值卡A"), conflicts)
            created_items = {payload["name"] for payload in self.create_requests(state, "create_item")}
            created_types = {
                payload["name"] for payload in self.create_requests(state, "create_card_type")
            }
            self.assertNotIn("洗吹-造型", created_items)
            self.assertNotIn("储值卡A", created_types)

    def test_load_commits_and_writes_report_without_pii(self):
        state = StubState()
        api = self.start_stub(state)
        with TemporaryDirectory() as temp:
            out = self.extract_fixture(Path(temp))
            report, detail = load.run(
                api, "boss", "pw", out / "import.xlsx", out, commit=True
            )
            paths = [request["path"] for request in state.requests]

            self.assertEqual(detail["status"], "committed")
            self.assertEqual(paths.count("/api/ops/commit_import"), 1)
            self.assertEqual(paths.count("/api/ops/get_import_batch"), 2)
            self.assertIn("声明 - 文件", report)
            self.assertIn("声明 - 实际写入", report)
            self.assertIn("| 本金合计 | 7070.10 | 7070.10 | 7070.10 |", report)
            self.assertIn("`卡种未配置: 未知卡`：2 行", report)
            self.assertIn("储值卡 第 5 行", report)
            self.assertIn("inactive_card", report)
            self.assertIn("ep_member_get_mem_count", report)
            self.assertIn("## 与美管加门店余额对比", report)
            self.assertIn("| 储值卡余额 | 7070.10 | 7070.10 | 0.00 |", report)
            self.assertIn("| 赠送金余额 | 250.00 | 250.00 | 0.00 |", report)
            self.assertIn("## 与美管加项目结余次数对比", report)
            self.assertIn("| 项目结余次数 | 10 | 10（普通项目次数=7，赠送项目次数=3） |", report)
            for private in (
                "张三",
                "李四",
                "王五",
                "13800131234",
                "13900000000",
                "phone_like",
                "capture_id",
                "12345",
                "13700139999",
                "K999",
            ):
                self.assertNotIn(private, report)

    def test_load_preview_does_not_commit(self):
        state = StubState()
        api = self.start_stub(state)
        with TemporaryDirectory() as temp:
            out = self.extract_fixture(Path(temp))
            _, detail = load.run(api, "boss", "pw", out / "import.xlsx", out)
            paths = [request["path"] for request in state.requests]

            self.assertEqual(detail["status"], "previewed")
            self.assertNotIn("/api/ops/commit_import", paths)
            self.assertEqual(paths.count("/api/ops/get_import_batch"), 1)

    def test_load_missing_mgj_sources_says_not_available(self):
        state = StubState()
        api = self.start_stub(state)
        with TemporaryDirectory() as temp:
            out = self.extract_fixture(Path(temp), independent=False)
            report, _ = load.run(api, "boss", "pw", out / "import.xlsx", out)

            self.assertIn("## 与美管加门店余额对比\n\nnot available", report)
            self.assertIn("## 与美管加项目结余次数对比\n\nnot available", report)


class ReplayTests(unittest.TestCase):
    def start_stub(self, state):
        server = ThreadingHTTPServer(("127.0.0.1", 0), replay_handler(state))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return f"http://127.0.0.1:{server.server_address[1]}"

    def fixture(self, root, state):
        db = root / "fixture.sqlite"
        fixture.build_fixture(db)
        catalog_dir = root / "catalog"
        extract.run(db, catalog_dir)
        state.load_catalog(
            json.loads((catalog_dir / "catalog.json").read_text(encoding="utf-8"))
        )
        return db

    def scheme_path(self):
        return Path(__file__).resolve().parent / "mgj" / "replay-scheme.example.json"

    def insert_bill(
        self,
        db,
        bill_no,
        occurred_at,
        customer_kind,
        customer_id,
        gender,
        card_type,
        items,
        staff,
        designated,
        payments,
    ):
        execute_many(
            db,
            "INSERT INTO archive_visits (bill_id, bill_no, occurred_at, source, "
            "customer_kind, customer_id, customer_name, mobile, gender, card_type, "
            "counted_visit, visit_seq, items, item_classes, item_count, item_amount, "
            "consume_fee, actual_income, payment_methods, online_platform, staff, "
            "designated, designated_staff, main_employee_id, main_employee_name, "
            "card_balance_before, remark) VALUES ("
            + ",".join("?" for _ in range(27))
            + ")",
            [
                (
                    "bill-" + bill_no,
                    bill_no,
                    occurred_at,
                    "synthetic",
                    customer_kind,
                    customer_id,
                    "合成顾客",
                    "",
                    gender,
                    card_type,
                    1,
                    99,
                    items,
                    "",
                    0,
                    0,
                    0,
                    0,
                    "",
                    "",
                    staff,
                    designated,
                    "",
                    "",
                    "",
                    0,
                    "synthetic",
                )
            ],
        )
        execute_many(
            db,
            "INSERT INTO business_transaction_payments (capture_id, read_key, "
            "source_endpoint, snapshot_status, captured_at, transaction_id, occurred_at, "
            "shop_id, customer_label, transaction_amount_displayed, "
            "posted_amount_displayed, payment_index, payment_method, "
            "payment_amount_displayed, source_class, is_cash_fee, payment_text) VALUES ("
            + ",".join("?" for _ in range(17))
            + ")",
            [
                (
                    "cap-" + bill_no,
                    "read-" + bill_no,
                    "synthetic",
                    "ok",
                    occurred_at,
                    bill_no,
                    occurred_at,
                    "shop",
                    "",
                    amount,
                    amount,
                    index,
                    method,
                    amount,
                    "",
                    0,
                    method,
                )
                for index, (method, amount) in enumerate(payments, start=1)
            ],
        )

    def test_parse_items_and_staff_repeated_out_of_order(self):
        items = replay.parse_items("洗吹-造型 ¥30、洗吹-造型 ¥40、护理 ¥88")
        segments = replay.parse_staff(
            "护理：张岩(指定)、李锦；洗吹-造型：浩宇；洗吹-造型：张岩、李锦"
        )
        matched, mixed = replay.match_staff(items, segments)

        self.assertEqual(
            [row["staff"] for row in matched],
            [["浩宇"], ["张岩", "李锦"], ["张岩", "李锦"]],
        )
        self.assertEqual(
            [row["amount"] for row in matched],
            [Decimal("30.00"), Decimal("40.00"), Decimal("88.00")],
        )
        self.assertTrue(matched[2]["line_designated"])
        self.assertTrue(mixed)

        one, _ = replay.match_staff(
            replay.parse_items("剪发 ¥70"), replay.parse_staff("剪发：张岩")
        )
        self.assertEqual(one[0]["staff"], ["张岩"])

    def test_three_staff_and_four_rejected(self):
        three, _ = replay.match_staff(
            replay.parse_items("剪发 ¥70"),
            replay.parse_staff("剪发：张岩、李锦、浩宇"),
        )
        self.assertEqual(three[0]["staff"], ["张岩", "李锦", "浩宇"])
        with self.assertRaisesRegex(replay.ReplayError, "too_many_staff"):
            replay.match_staff(
                replay.parse_items("剪发 ¥70"),
                replay.parse_staff("剪发：张岩、李锦、浩宇、小周"),
            )

    def test_three_staff_line_replays_to_three_slots(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        item = state.add_item("剪发", "70.00")
        state.positions = [
            {"id": "p1", "name": "大工", "active": True},
            {"id": "p2", "name": "助理", "active": True},
        ]
        staff = {
            name: {"id": "staff-" + name, "name": name}
            for name in ("张岩", "李锦", "浩宇")
        }
        client = Client(api)
        client.login("boss", "pw")
        record = replay.replay_bill(
            client,
            {
                "bill_no": "B-THREE",
                "items": "剪发 ¥70",
                "staff": "剪发：张岩、李锦、浩宇",
                "gender": "女",
                "designated": 0,
                "customer_kind": "散客",
                "customer_id": "",
                "card_type": "",
                "payments": [
                    {"method": "现金", "amount": Decimal("70.00"), "text": "70"}
                ],
            },
            {
                "items_by_name": {"剪发": item},
                "staff_by_name": staff,
                "customer_map": {},
                "card_map": {},
                "deals": {},
            },
        )
        self.assertEqual(record["status"], "replayed")
        add = [
            json.loads(request["body"])
            for request in state.requests
            if request["path"] == "/api/ops/add_order_item"
        ][0]
        self.assertEqual([entry["slot"] for entry in add["item"]["staff"]], [1, 2, 3])

    def test_payment_plan_merges_card_skips_zero_and_maps_groupbuy(self):
        rows = [
            {"method": "卡金", "amount": Decimal("90.00"), "text": "90"},
            {"method": "赠金", "amount": Decimal("20.00"), "text": "20"},
            {"method": "微信支付", "amount": Decimal("0.00"), "text": "0"},
            {"method": "点评支付", "amount": Decimal("80.00"), "text": "80"},
            {"method": "抖音团购", "amount": Decimal("88.00"), "text": "88"},
            {"method": "现金", "amount": Decimal("70.00"), "text": "70"},
        ]
        plan = replay.payment_plan(rows)

        self.assertEqual(plan["card"]["amount"], Decimal("110.00"))
        self.assertEqual(plan["card"]["principal"], Decimal("90.00"))
        self.assertEqual(plan["card"]["bonus"], Decimal("20.00"))
        self.assertEqual(
            plan["methods"], [{"method": "cash", "amount": Decimal("70.00")}]
        )
        self.assertEqual(
            [(row["platform"], row["amount"]) for row in plan["groupbuy"]],
            [("meituan", Decimal("80.00")), ("douyin", Decimal("88.00"))],
        )
        self.assertEqual(plan["total"], Decimal("348.00"))

    def test_payment_plan_merges_same_direct_method(self):
        plan = replay.payment_plan(
            [
                {"method": "微信支付", "amount": Decimal("30.00"), "text": "30"},
                {"method": "微信支付", "amount": Decimal("40.00"), "text": "40"},
            ]
        )
        self.assertEqual(plan["methods"], [{"method": "wechat", "amount": Decimal("70.00")}])

    def test_flag_accepts_numeric_one(self):
        for value in (1, 1.0, "1", "1.0", "1.00"):
            self.assertTrue(replay._flag(value))
        for value in (0, 0.0, "0", "0.0", None, ""):
            self.assertFalse(replay._flag(value))

    def test_coupon_identity_truncates_and_stays_unique(self):
        bill_no = "x" * 600
        code_one, key_one = replay.coupon_identity(bill_no, 1)
        code_two, key_two = replay.coupon_identity(bill_no, 2)
        self.assertLessEqual(len(code_one), 500)
        self.assertLessEqual(len(key_one), 200)
        self.assertNotEqual(code_one, code_two)
        self.assertNotEqual(key_one, key_two)
        self.assertEqual(replay.coupon_identity(bill_no, 1), (code_one, key_one))

    def test_opening_workbook_sums(self):
        with TemporaryDirectory() as temp:
            db = Path(temp) / "fixture.sqlite"
            fixture.build_fixture(db)
            conn = replay._readonly_connect(db)
            try:
                balances = replay.opening_balances(conn, "2026-09")
            finally:
                conn.close()

            self.assertEqual(balances["customers"], {"C001": "男"})
            self.assertEqual(
                balances["cards"][("C001", "储值卡A")],
                {"principal": Decimal("80.00"), "bonus": Decimal("30.00")},
            )
            self.assertEqual(
                balances["cards"][("C001", "无折扣卡")],
                {"principal": Decimal("90.00"), "bonus": Decimal("50.00")},
            )
            sheets, principal, bonus = replay.opening_workbook(balances)
            self.assertEqual(principal, Decimal("170.00"))
            self.assertEqual(bonus, Decimal("80.00"))
            self.assertEqual(sheets[0][2], [["C001", "回放顾客C001", "", "男"]])
            self.assertEqual(sheets[1][2][0][0], "RC001-储值卡A")
            self.assertEqual(sheets[3][2][0], ["1", "2", "170.00", "80.00", "0"])

    def test_negative_opening_balance_is_skipped(self):
        with TemporaryDirectory() as temp:
            root = Path(temp)
            state = ReplayStubState()
            api = self.start_stub(state)
            db = self.fixture(root, state)
            self.insert_bill(
                db,
                "B011",
                "2026-09-11 10:00:00",
                "会员",
                "C001",
                "男",
                "储值卡A",
                "护理 ¥1",
                "护理：张岩",
                0,
                [("卡金", "-100.00")],
            )
            result = replay.run(
                db, "2026-09", api, "boss", "pw", self.scheme_path(), root / "out"
            )
            by_bill = {row["bill_no"]: row for row in result["bills"]}
            self.assertEqual(by_bill["B010"]["reason"], "unknown_card")
            self.assertEqual(by_bill["B011"]["reason"], "invalid_payment_amount")
            self.assertEqual(result["opening"]["skipped"]["negative_opening_balance"], 1)
            report = (root / "out" / "replay-report.md").read_text(encoding="utf-8")
            self.assertIn("negative_opening_balance", report)

    def test_scheme_resolution_and_unknown_name_abort(self):
        scheme = json.loads(self.scheme_path().read_text(encoding="utf-8"))["commission"]
        staff = {"张岩": "staff-1", "李锦": "staff-2"}
        items = {"洗吹-造型": "item-1"}
        positions = {"大工": "position-1", "助理": "position-2"}
        categories = {"洗剪类": "category-1"}
        resolved = replay.resolve_scheme(scheme, staff, items, positions, categories)
        self.assertEqual(resolved["rules"][0]["cells"][0]["position_id"], "position-1")

        override_scheme = {
            "rules": scheme["rules"],
            "overrides": [
                {
                    "source": "item",
                    "staff_name": "张岩",
                    "item_name": "洗吹-造型",
                    "percent": "10.00",
                },
                {
                    "source": "item",
                    "position_name": "助理",
                    "category_name": "洗剪类",
                    "fixed_amount": "3.00",
                },
            ],
            "excluded_staff": ["李锦"],
        }
        resolved = replay.resolve_scheme(
            override_scheme, staff, items, positions, categories
        )
        self.assertEqual(resolved["overrides"][0]["staff_id"], "staff-1")
        self.assertEqual(resolved["overrides"][0]["item_id"], "item-1")
        self.assertEqual(resolved["overrides"][1]["position_id"], "position-2")
        self.assertEqual(resolved["overrides"][1]["category_id"], "category-1")
        self.assertEqual(resolved["excluded_staff"], ["staff-2"])

        bad = {
            "rules": [
                {
                    "source": "item",
                    "base": "paid",
                    "by_position": True,
                    "cells": [{"position_name": "不存在", "percent": "10.00"}],
                }
            ]
        }
        with self.assertRaisesRegex(RuntimeError, "不存在的岗位"):
            replay.resolve_scheme(bad, staff, items, positions, categories)
        unknown_category = {
            "rules": scheme["rules"],
            "overrides": [
                {
                    "source": "item",
                    "position_name": "助理",
                    "category_name": "不存在",
                    "fixed_amount": "3.00",
                }
            ],
        }
        with self.assertRaisesRegex(RuntimeError, "不存在的类别：不存在"):
            replay.resolve_scheme(
                unknown_category, staff, items, positions, categories
            )

    def test_duplicate_names_abort(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        client = Client(api)
        client.login("boss", "pw")

        state.positions = [
            {"id": "p1", "name": "大工", "active": True},
            {"id": "p2", "name": "大工", "active": True},
        ]
        with self.assertRaisesRegex(RuntimeError, "岗位名称重复：大工"):
            replay.setup_positions(client)

        state.items = []
        state.add_item("洗吹-造型", "120.00")
        state.add_item("洗吹-造型", "120.00")
        with self.assertRaisesRegex(RuntimeError, "项目名称重复：洗吹-造型"):
            replay.setup_items(client, {"大工": "p1", "助理": "p2"})

        state.staff = [
            {"id": "s1", "name": "张岩", "number": "1", "level": "首席", "position": "大工", "roles": []},
            {"id": "s2", "name": "张岩", "number": "2", "level": "首席", "position": "大工", "roles": []},
        ]
        state.levels = [{"id": "l1", "name": "首席", "active": True}]
        state.positions = [{"id": "p1", "name": "大工", "active": True}]
        with self.assertRaisesRegex(RuntimeError, "员工名称重复：张岩"):
            replay.setup_staff(client, [], {"首席": "l1"}, {"大工": "p1"})

    def test_category_name_resolves_existing_store_category(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        client = Client(api)
        client.login("boss", "pw")
        state.item_categories = [
            {
                "id": "category-existing",
                "name": "门店旧类别",
                "performance_ratio": 100,
                "active": True,
            }
        ]
        positions = {"大工": "p1", "助理": "p2"}
        _, by_name = replay.setup_categories(client, [], {}, positions)
        resolved = replay.resolve_scheme(
            {
                "rules": [],
                "overrides": [
                    {
                        "source": "item",
                        "position_name": "助理",
                        "category_name": "门店旧类别",
                        "fixed_amount": "3.00",
                    }
                ],
            },
            {},
            {},
            positions,
            {name: category["id"] for name, category in by_name.items()},
        )
        self.assertEqual(resolved["overrides"][0]["category_id"], "category-existing")

    def test_item_setup_uses_three_slots(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        client = Client(api)
        client.login("boss", "pw")
        state.add_item("剪发", "88.00")
        items = replay.setup_items(client, {"大工": "p1", "助理": "p2"})
        positions = items["剪发"]["positions"]
        self.assertEqual(
            [row["position_id"] for row in positions], ["p1", "p2", "p2"]
        )
        self.assertEqual([row["required"] for row in positions], [True, False, False])

    def test_categories_are_created_and_assigned_idempotently(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        client = Client(api)
        client.login("boss", "pw")
        hair = state.add_item("剪发", "88.00")
        hair["labor_fee_amount"] = "5.00"
        hair["duration_minutes"] = 45
        hair["bookable"] = False
        hair["level_prices"] = [{"level_id": "level-1", "price": "99.00"}]
        hair["member_prices"] = [{"card_type_id": "card-1", "price": "79.00"}]
        state.add_item("染发", "288.00")
        state.add_item("护理", "88.00")
        items = {item["name"]: item for item in state.items}
        categories = [
            {
                "name": "洗剪类",
                "performance_ratio": 90,
                "items": ["剪发", "染发"],
            }
        ]
        positions = {"大工": "p1", "助理": "p2"}
        first, by_name = replay.setup_categories(client, categories, items, positions)
        self.assertEqual(by_name["洗剪类"]["performance_ratio"], 90)
        self.assertEqual(first["剪发"]["category"]["name"], "洗剪类")
        self.assertEqual(first["染发"]["category"]["name"], "洗剪类")
        self.assertIsNone(first["护理"]["category"])
        creates = [
            request
            for request in state.requests
            if request["path"] == "/api/ops/create_item_category"
        ]
        self.assertEqual(len(creates), 1)
        updates = [
            json.loads(request["body"])
            for request in state.requests
            if request["path"] == "/api/ops/update_item"
        ]
        self.assertEqual({payload["name"] for payload in updates}, {"剪发", "染发"})
        for payload in updates:
            self.assertIn("price", payload)
            self.assertIn("discountable", payload)
            self.assertIn("labor_fee_amount", payload)
            self.assertIn("duration_minutes", payload)
            self.assertIn("bookable", payload)
            self.assertEqual(len(payload["positions"]), 3)
            self.assertEqual(payload["category_id"], by_name["洗剪类"]["id"])
        self.assertEqual(first["剪发"]["labor_fee_amount"], "5.00")
        self.assertEqual(first["剪发"]["duration_minutes"], 45)
        self.assertFalse(first["剪发"]["bookable"])
        self.assertEqual(first["剪发"]["level_prices"], [{"level_id": "level-1", "price": "99.00"}])
        self.assertEqual(first["剪发"]["member_prices"], [{"card_type_id": "card-1", "price": "79.00"}])
        self.assertEqual([row["position_id"] for row in first["剪发"]["positions"]], ["p1", "p2", "p2"])
        state.requests.clear()
        second, _ = replay.setup_categories(client, categories, first, positions)
        self.assertEqual(
            [request["path"] for request in state.requests].count("/api/ops/update_item"),
            0,
        )
        self.assertEqual(
            [
                request["path"]
                for request in state.requests
                if request["path"] == "/api/ops/create_item_category"
            ],
            [],
        )
        self.assertEqual(second["剪发"]["category"]["name"], "洗剪类")

    def test_category_setup_rejects_duplicate_and_unknown_items(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        client = Client(api)
        client.login("boss", "pw")
        state.add_item("剪发", "88.00")
        items = {"剪发": state.items[0]}
        positions = {"大工": "p1", "助理": "p2"}
        with self.assertRaisesRegex(RuntimeError, "类别项目重复：剪发"):
            replay.setup_categories(
                client,
                [
                    {"name": "甲", "performance_ratio": 90, "items": ["剪发"]},
                    {"name": "乙", "performance_ratio": 90, "items": ["剪发"]},
                ],
                items,
                positions,
            )
        with self.assertRaisesRegex(RuntimeError, "类别引用了不存在的项目：不存在"):
            replay.setup_categories(
                client,
                [{"name": "甲", "performance_ratio": 90, "items": ["不存在"]}],
                items,
                positions,
            )

    def test_category_setup_updates_ratio_and_reactivates(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        client = Client(api)
        client.login("boss", "pw")
        item = state.add_item("剪发", "88.00")
        state.item_categories = [
            {
                "id": "category-old",
                "name": "洗剪类",
                "performance_ratio": 80,
                "active": False,
            }
        ]
        positions = {"大工": "p1", "助理": "p2"}
        items, by_name = replay.setup_categories(
            client,
            [{"name": "洗剪类", "performance_ratio": 90, "items": ["剪发"]}],
            {"剪发": item},
            positions,
        )
        self.assertEqual(by_name["洗剪类"]["performance_ratio"], 90)
        self.assertTrue(by_name["洗剪类"]["active"])
        self.assertEqual(items["剪发"]["category"]["id"], "category-old")
        updates = [
            json.loads(request["body"])
            for request in state.requests
            if request["path"] == "/api/ops/update_item_category"
        ]
        self.assertEqual(
            updates,
            [
                {
                    "id": "category-old",
                    "name": "洗剪类",
                    "performance_ratio": 90,
                    "active": True,
                }
            ],
        )
        state.requests.clear()
        replay.setup_categories(
            client,
            [{"name": "洗剪类", "performance_ratio": 90, "items": ["剪发"]}],
            items,
            positions,
        )
        self.assertEqual(
            [
                request["path"]
                for request in state.requests
                if request["path"] == "/api/ops/update_item_category"
            ],
            [],
        )
    def test_full_performance_split_is_passed_through(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        client = Client(api)
        client.login("boss", "pw")
        replay.apply_settings(client, {"performance_split": "full"}, "2026-11-15")
        request = [
            json.loads(item["body"])
            for item in state.requests
            if item["path"] == "/api/ops/change_setting"
        ]
        self.assertEqual(
            request,
            [
                {
                    "key": "performance_split",
                    "value": "full",
                    "effective_from": "2026-11-15",
                }
            ],
        )

    def test_unknown_scheme_name_aborts_before_replay(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        with TemporaryDirectory() as temp:
            root = Path(temp)
            db = self.fixture(root, state)
            scheme = json.loads(self.scheme_path().read_text(encoding="utf-8"))
            scheme["commission"]["rules"][0]["cells"][0]["position_name"] = "不存在"
            scheme_path = root / "scheme.json"
            scheme_path.write_text(json.dumps(scheme, ensure_ascii=False), encoding="utf-8")

            with self.assertRaisesRegex(RuntimeError, "不存在的岗位"):
                replay.run(db, "2026-09", api, "boss", "pw", scheme_path, root / "out")
            payload = json.loads(
                (root / "out" / "replay-result.json").read_text(encoding="utf-8")
            )
            self.assertEqual(payload["blockers"][0]["phase"], "phase 1")
            self.assertEqual(payload["blockers"][0]["op"], "resolve_scheme")
            paths = [request["path"] for request in state.requests]
            self.assertNotIn("/api/ops/open_order", paths)

    def test_setup_api_error_writes_blockers(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        state.fail_operation = (
            "change_setting",
            409,
            "生效日期不能早于今天（2026-11-15）",
        )
        with TemporaryDirectory() as temp:
            root = Path(temp)
            db = self.fixture(root, state)
            with self.assertRaisesRegex(RuntimeError, "生效日期不能早于今天"):
                replay.run(
                    db, "2026-09", api, "boss", "pw", self.scheme_path(), root / "out"
                )
            payload = json.loads(
                (root / "out" / "replay-result.json").read_text(encoding="utf-8")
            )
            report = (root / "out" / "replay-report.md").read_text(encoding="utf-8")
            self.assertEqual(payload["blockers"][0]["phase"], "phase 1")
            self.assertEqual(payload["blockers"][0]["op"], "change_setting")
            self.assertEqual(
                payload["blockers"][0]["detail"],
                "生效日期不能早于今天（2026-11-15）",
            )
            self.assertIn("### 阻塞", report)
            self.assertIn("phase 1", report)

    def test_rerun_guard_writes_blocker(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        state.initial_staff_report_rows = [
            {
                "staff": {"id": "staff-1", "name": "张岩"},
                "item_performance": "1.00",
                "cash_performance": "1.00",
                "card_performance": "0.00",
            }
        ]
        with TemporaryDirectory() as temp:
            root = Path(temp)
            db = self.fixture(root, state)
            with self.assertRaisesRegex(RuntimeError, "replay 需要刚 reset 的开发库"):
                replay.run(
                    db, "2026-09", api, "boss", "pw", self.scheme_path(), root / "out"
                )
            payload = json.loads(
                (root / "out" / "replay-result.json").read_text(encoding="utf-8")
            )
            self.assertEqual(payload["blockers"][0]["phase"], "phase 2")
            self.assertEqual(payload["blockers"][0]["op"], "rerun_guard")
            paths = [request["path"] for request in state.requests]
            self.assertNotIn("/api/ops/open_order", paths)

    def test_already_imported_blocks(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        state.import_row_error = "已导入"
        with TemporaryDirectory() as temp:
            root = Path(temp)
            db = self.fixture(root, state)
            with self.assertRaisesRegex(RuntimeError, "replay 需要刚 reset 的开发库"):
                replay.run(
                    db, "2026-09", api, "boss", "pw", self.scheme_path(), root / "out"
                )
            payload = json.loads(
                (root / "out" / "replay-result.json").read_text(encoding="utf-8")
            )
            self.assertEqual(payload["blockers"][0]["phase"], "phase 2")
            self.assertEqual(payload["blockers"][0]["op"], "import_fresh_check")
            paths = [request["path"] for request in state.requests]
            self.assertNotIn("/api/ops/open_order", paths)

    def test_card_discount_mismatch_skips_checkout(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        with TemporaryDirectory() as temp:
            root = Path(temp)
            db = self.fixture(root, state)
            self.insert_bill(
                db,
                "B011",
                "2026-09-11 10:00:00",
                "会员",
                "C001",
                "男",
                "储值卡A",
                "洗吹-造型 ¥120",
                "洗吹-造型：张岩",
                0,
                [("卡金", "120.00")],
            )
            self.insert_bill(
                db,
                "B012",
                "2026-09-12 10:00:00",
                "散客",
                "G012",
                "女",
                "",
                "护理 ¥50",
                "护理：李锦",
                0,
                [("现金", "50.00")],
            )
            result = replay.run(
                db, "2026-09", api, "boss", "pw", self.scheme_path(), root / "out"
            )
            failures = {row["bill_no"]: row for row in result["bills"] if row["status"] == "failed"}
            self.assertEqual(failures["B011"]["reason"], "card_discount_mismatch")
            self.assertIn("美管加 120.00 本系统 102.00 打折行 洗吹-造型", failures["B011"]["detail"])
            self.assertEqual(
                {row["bill_no"]: row["status"] for row in result["bills"]}["B012"],
                "replayed",
            )

    def test_replay_failure_isolation_and_report_without_pii(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        state.checkout_failures[2] = 409
        state.staff_report_rows = [
            {
                "staff": {"id": "staff-1", "name": "张岩"},
                "item_performance": "0.10",
                "cash_performance": "0.00",
                "card_performance": "0.10",
            }
        ]
        state.commission_entries = [
            {
                "id": "commission-1",
                "source": "item",
                "staff": {"id": "staff-1", "name": "张岩"},
                "amount": "0.10",
            }
        ]
        with TemporaryDirectory() as temp:
            root = Path(temp)
            db = self.fixture(root, state)
            result = replay.run(
                db,
                "2026-09",
                api,
                "boss",
                "pw",
                self.scheme_path(),
                root / "out",
            )
            report = (root / "out" / "replay-report.md").read_text(encoding="utf-8")
            payload = json.loads(
                (root / "out" / "replay-result.json").read_text(encoding="utf-8")
            )

            self.assertEqual(result["bills_total"], 10)
            self.assertEqual(result["bills_selected"], 10)
            self.assertEqual(result["bills_replayed"], 9)
            self.assertEqual(result["bills_failed"], 1)
            self.assertEqual(result["failure_counts"]["api_409"]["examples"], ["B002"])
            self.assertEqual(payload["failures"][0]["detail"], "测试冲突")
            self.assertEqual(payload["bills"][1]["detail"], "测试冲突")
            self.assertEqual(
                {row["bill_no"]: row["status"] for row in payload["bills"]}["B010"],
                "replayed",
            )
            self.assertIn("api_409", report)
            self.assertIn("B002", report)
            self.assertIn("223.90", report)
            self.assertIn("99.90", report)
            self.assertIn("| 合计 |", report)
            self.assertEqual(payload["payments"]["totals"]["difference"], "0.00")
            zhang = next(row for row in payload["performance"] if row["staff"] == "张岩")
            self.assertEqual(zhang["diff_total"], "223.90")
            self.assertEqual(payload["performance_totals"]["diff_total"], "803.90")
            self.assertEqual(payload["commission_totals"]["difference"], "229.90")
            for private in (
                "张三",
                "李四",
                "王五",
                "赵六",
                "顾客五",
                "顾客六",
                "合成顾客",
                "13800131234",
                "13900130000",
                "13600130000",
                "13700139999",
                "C001",
            ):
                self.assertNotIn(private, report)
                self.assertNotIn(private, json.dumps(payload, ensure_ascii=False))

    def test_per_bill_500_and_runtime_errors_are_isolated(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        state.malformed_add_order_index = 2
        state.checkout_failures[3] = 500
        with TemporaryDirectory() as temp:
            root = Path(temp)
            db = self.fixture(root, state)
            result = replay.run(
                db, "2026-09", api, "boss", "pw", self.scheme_path(), root / "out"
            )
            by_bill = {row["bill_no"]: row for row in result["bills"]}
            self.assertEqual(by_bill["B002"]["reason"], "error_RuntimeError")
            self.assertEqual(by_bill["B003"]["reason"], "api_500")
            self.assertEqual(by_bill["B003"]["detail"], "测试 500")
            self.assertEqual(by_bill["B004"]["status"], "replayed")

    def test_limit_excluded_bills_land_in_unreplayed(self):
        state = ReplayStubState()
        api = self.start_stub(state)
        with TemporaryDirectory() as temp:
            root = Path(temp)
            db = self.fixture(root, state)
            execute(
                db,
                'INSERT INTO ep_super_operation_report_search__t3 '
                '(is_latest_copy, "员工项目业绩流水·消费时间", "员工项目业绩流水·流水单号", '
                '"员工项目业绩流水·总业绩", "员工项目业绩流水·现金类业绩", '
                '"员工项目业绩流水·卡扣业绩", "员工项目业绩流水·员工姓名") '
                "VALUES (1, '2026-09-10', 'B010', '1.00', '1.00', '0.00', '')",
            )
            result = replay.run(
                db,
                "2026-09",
                api,
                "boss",
                "pw",
                self.scheme_path(),
                root / "out",
                limit=5,
            )
            self.assertEqual(result["bills_total"], 10)
            self.assertEqual(result["bills_selected"], 5)
            replayed = {
                row["bill_no"] for row in result["bills"] if row["status"] == "replayed"
            }
            self.assertEqual(replayed, {"B001", "B002", "B003", "B004", "B005"})
            conn = replay._readonly_connect(db)
            try:
                source = replay._source_performance(conn, "2026-09", replayed)
            finally:
                conn.close()
            self.assertEqual(source["unreplayed"]["张岩"], Decimal("124.00"))
            self.assertEqual(source["unreplayed"]["李锦"], Decimal("164.00"))
            self.assertEqual(source["unreplayed"]["浩宇"], Decimal("168.00"))
            rows = {row["staff"]: row for row in result["performance"]}
            self.assertEqual(rows["张岩"]["mgj_month_total"], "170.00")
            self.assertEqual(rows["李锦"]["mgj_month_total"], "128.00")
            self.assertEqual(rows["浩宇"]["mgj_month_total"], "120.00")
            self.assertEqual(result["performance_empty_staff"], 1)
            self.assertTrue(any("本次只回放前 5 张单" in note for note in result["notes"]))

    def test_report_totals_and_differences_with_decimal(self):
        record = {
            "bill_no": "B1",
            "status": "replayed",
            "reason": None,
            "detail": "",
            "order_id": "order-1",
            "repriced_lines": 1,
            "mixed_designation": False,
            "gender_defaulted": False,
            "source_payments": [
                {"method": "现金", "amount": Decimal("0.30")},
                {"method": "赠金", "amount": Decimal("0.20")},
            ],
            "our_payments": [{"method": "cash", "amount": "0.10"}],
            "our_groupbuy": [],
        }
        result = replay.build_result(
            "2026-09",
            "2026-11-15",
            [{}],
            [record],
            {"customers": {}, "cards": {}, "skipped": {}},
            {
                "imported": False,
                "batch_id": None,
                "error_rows": 0,
                "rerun_suspected": False,
                "rerun_reason": "",
            },
            {
                "rows": {
                    "张岩": {
                        "total": Decimal("0.30"),
                        "cash": Decimal("0.30"),
                        "card": Decimal("0.00"),
                    }
                },
                "invalid_numbers": 1,
                "empty_staff": 0,
            },
            {
                "张岩": {
                    "total": Decimal("0.10"),
                    "cash": Decimal("0.10"),
                    "card": Decimal("0.00"),
                }
            },
            {
                "张岩": {
                    "item_commission": Decimal("0.30"),
                    "card_sales_performance": Decimal("0.00"),
                    "card_sales_commission": Decimal("0.00"),
                }
            },
            {
                "张岩": {
                    "item": Decimal("0.10"),
                    "consume": Decimal("0.00"),
                    "labor": Decimal("0.00"),
                }
            },
            ["张岩"],
            {},
            [],
            1,
        )
        cash = next(row for row in result["payments"]["rows"] if row["method"] == "现金")
        self.assertEqual(cash["difference"], "0.20")
        self.assertEqual(result["payments"]["totals"]["source_total"], "0.50")
        self.assertEqual(result["payments"]["totals"]["ours_total"], "0.10")
        self.assertEqual(result["payments"]["totals"]["difference"], "0.40")
        self.assertEqual(result["performance_totals"]["diff_total"], "0.20")
        self.assertEqual(result["commission_totals"]["difference"], "0.20")
        self.assertEqual(result["performance_invalid_numbers"], 1)
        self.assertEqual(result["commission_invalid_numbers"], 1)
        self.assertIn("| 合计 |", replay.render_report(result))

    def test_month_performance_uses_item_total_instead_of_per_row_total(self):
        with TemporaryDirectory() as temp:
            db = Path(temp) / "fixture.sqlite"
            fixture.build_fixture(db)
            execute(
                db,
                'INSERT INTO ep_super_operation_report_search__t3 '
                '(is_latest_copy, "员工项目业绩流水·消费时间", "员工项目业绩流水·流水单号", '
                '"员工项目业绩流水·总业绩", "员工项目业绩流水·现金类业绩", '
                '"员工项目业绩流水·卡扣业绩", "员工项目业绩流水·员工姓名") '
                "VALUES (1, '2026-08-01', 'B100', '50.00', '50.00', '0.00', '测试员')",
            )
            execute_many(
                db,
                "INSERT INTO business_employee_commission_monthly "
                "(period_month, employee_label, item_total, item_commission, "
                "card_sales_performance, card_sales_commission, captured_at, "
                "source_record_id) VALUES (?,?,?,?,?,?,?,?)",
                [
                    (
                        "2026-08",
                        "77 号 首席 [测试员]",
                        "999.00",
                        "99.00",
                        "0.00",
                        "0.00",
                        "2026-08-01",
                        "r1",
                    ),
                    (
                        "2026-08",
                        "77 号 首席 [测试员]",
                        "100.00",
                        "10.00",
                        "0.00",
                        "0.00",
                        "2026-08-31",
                        "r2",
                    ),
                ],
            )
            conn = replay._readonly_connect(db)
            try:
                source = replay._source_performance(conn, "2026-08", {"B100"})
                employees = replay.monthly_employees(conn, "2026-08")
            finally:
                conn.close()
            self.assertEqual(source["rows"]["测试员"]["total"], Decimal("50.00"))
            self.assertEqual(source["unreplayed"], {})
            self.assertEqual(
                employees["commission"]["测试员"]["item_total"], Decimal("100.00")
            )
            result = replay.build_result(
                "2026-08",
                "2026-11-15",
                [],
                [],
                {"customers": {}, "cards": {}, "skipped": {}},
                {
                    "imported": False,
                    "batch_id": None,
                    "error_rows": 0,
                    "rerun_suspected": False,
                    "rerun_reason": "",
                },
                source,
                {
                    "测试员": {
                        "total": Decimal("100.00"),
                        "cash": Decimal("50.00"),
                        "card": Decimal("50.00"),
                    }
                },
                employees["commission"],
                {},
                ["测试员"],
                {},
                [],
                0,
            )
            row = next(item for item in result["performance"] if item["staff"] == "测试员")
            self.assertEqual(row["mgj_total"], "50.00")
            self.assertEqual(row["mgj_month_total"], "100.00")
            self.assertEqual(row["diff_month_total"], "0.00")
            self.assertEqual(row["diff_total"], "-50.00")
            self.assertTrue(result["performance_month_available"])
            self.assertEqual(result["performance_totals"]["mgj_month_total"], "100.00")
            self.assertEqual(result["performance_totals"]["diff_month_total"], "0.00")
            report = replay.render_report(result)
            self.assertIn("美管加月度总业绩（扣未回放）", report)
            self.assertIn("逐行流水总业绩（参考）", report)
            self.assertIn("business_employee_commission_monthly.item_total（整月，不去重）", report)
            self.assertIn("逐行流水来源 ep_super_operation_report_search__t3", report)
            self.assertNotIn("美管加业绩来源是 ep_super_operation_report_search__t3", report)

    def test_month_performance_subtracts_unreplayed_bills(self):
        with TemporaryDirectory() as temp:
            db = Path(temp) / "fixture.sqlite"
            fixture.build_fixture(db)
            execute_many(
                db,
                'INSERT INTO ep_super_operation_report_search__t3 '
                '(is_latest_copy, "员工项目业绩流水·消费时间", "员工项目业绩流水·流水单号", '
                '"员工项目业绩流水·总业绩", "员工项目业绩流水·现金类业绩", '
                '"员工项目业绩流水·卡扣业绩", "员工项目业绩流水·员工姓名") '
                "VALUES (1,?,?,?,?,?,?)",
                [
                    ("2026-08-01", "B100", "50.00", "50.00", "0.00", "测试员"),
                    ("2026-08-02", "B101", "30.00", "30.00", "0.00", "测试员"),
                ],
            )
            execute(
                db,
                "INSERT INTO business_employee_commission_monthly "
                "(period_month, employee_label, item_total, item_commission, "
                "card_sales_performance, card_sales_commission) "
                "VALUES ('2026-08', '77 号 首席 [测试员]', '100.00', '10.00', '0.00', '0.00')",
            )
            conn = replay._readonly_connect(db)
            try:
                source = replay._source_performance(conn, "2026-08", {"B100"})
                employees = replay.monthly_employees(conn, "2026-08")
            finally:
                conn.close()
            self.assertEqual(source["rows"]["测试员"]["total"], Decimal("50.00"))
            self.assertEqual(source["unreplayed"]["测试员"], Decimal("30.00"))
            result = replay.build_result(
                "2026-08",
                "2026-11-15",
                [],
                [],
                {"customers": {}, "cards": {}, "skipped": {}},
                {
                    "imported": False,
                    "batch_id": None,
                    "error_rows": 0,
                    "rerun_suspected": False,
                    "rerun_reason": "",
                },
                source,
                {
                    "测试员": {
                        "total": Decimal("50.00"),
                        "cash": Decimal("50.00"),
                        "card": Decimal("0.00"),
                    }
                },
                employees["commission"],
                {},
                ["测试员"],
                {},
                [],
                0,
            )
            row = next(item for item in result["performance"] if item["staff"] == "测试员")
            self.assertEqual(row["mgj_month_total"], "70.00")
            self.assertEqual(row["diff_month_total"], "20.00")
            self.assertEqual(result["performance_totals"]["mgj_month_total"], "70.00")
            self.assertEqual(result["performance_totals"]["diff_month_total"], "20.00")
            self.assertEqual(result["commission"][0]["mgj_ratio"], "10.00%")
            self.assertEqual(result["commission_totals"]["mgj_ratio"], "10.00%")

    def test_missing_item_total_falls_back_to_per_row(self):
        with TemporaryDirectory() as temp:
            db = Path(temp) / "monthly.sqlite"
            conn = sqlite3.connect(db)
            try:
                conn.execute(
                    "CREATE TABLE business_employee_commission_monthly ("
                    "period_month TEXT, employee_label TEXT, item_commission TEXT, "
                    "card_sales_performance TEXT, card_sales_commission TEXT, "
                    "captured_at TEXT, source_record_id TEXT)"
                )
                conn.execute(
                    "INSERT INTO business_employee_commission_monthly VALUES "
                    "('2026-08', '77 号 首席 [测试员]', '10.00', '0.00', '0.00', "
                    "'2026-08-31', 'r1')"
                )
                conn.commit()
                employees = replay.monthly_employees(conn, "2026-08")
            finally:
                conn.close()
            self.assertNotIn("item_total", employees["commission"]["测试员"])
            result = replay.build_result(
                "2026-08",
                "2026-11-15",
                [],
                [],
                {"customers": {}, "cards": {}, "skipped": {}},
                {
                    "imported": False,
                    "batch_id": None,
                    "error_rows": 0,
                    "rerun_suspected": False,
                    "rerun_reason": "",
                },
                {
                    "rows": {
                        "测试员": {
                            "total": Decimal("50.00"),
                            "cash": Decimal("50.00"),
                            "card": Decimal("0.00"),
                        }
                    },
                    "unreplayed": {},
                    "invalid_numbers": 0,
                    "empty_staff": 0,
                },
                {
                    "测试员": {
                        "total": Decimal("100.00"),
                        "cash": Decimal("50.00"),
                        "card": Decimal("50.00"),
                    }
                },
                employees["commission"],
                {},
                ["测试员"],
                {},
                [],
                0,
            )
            row = next(item for item in result["performance"] if item["staff"] == "测试员")
            self.assertIsNone(row["mgj_month_total"])
            self.assertIsNone(row["diff_month_total"])
            self.assertFalse(result["performance_month_available"])
            self.assertIsNone(result["performance_totals"]["mgj_month_total"])
            self.assertIsNone(result["performance_totals"]["diff_month_total"])
            self.assertEqual(result["commission"][0]["mgj_ratio"], "20.00%")
            self.assertEqual(result["commission_totals"]["mgj_ratio"], "20.00%")
            report = replay.render_report(result)
            self.assertIn("美管加月度汇总不可用，业绩只按逐行流水对照", report)
            self.assertNotIn("美管加月度业绩对照使用", report)

    def test_null_item_total_falls_back_to_per_row(self):
        with TemporaryDirectory() as temp:
            db = Path(temp) / "monthly.sqlite"
            conn = sqlite3.connect(db)
            try:
                conn.execute(
                    "CREATE TABLE business_employee_commission_monthly ("
                    "period_month TEXT, employee_label TEXT, item_total TEXT, "
                    "item_commission TEXT, card_sales_performance TEXT, "
                    "card_sales_commission TEXT, captured_at TEXT, source_record_id TEXT)"
                )
                conn.execute(
                    "INSERT INTO business_employee_commission_monthly VALUES "
                    "('2026-08', '77 号 首席 [测试员]', NULL, '10.00', '0.00', '0.00', "
                    "'2026-08-31', 'r1')"
                )
                conn.commit()
                employees = replay.monthly_employees(conn, "2026-08")
            finally:
                conn.close()
            self.assertNotIn("item_total", employees["commission"]["测试员"])
            self.assertEqual(employees["invalid_numbers"], 0)
            self.assertEqual(employees["item_total_invalid_numbers"], 0)
            result = replay.build_result(
                "2026-08",
                "2026-11-15",
                [],
                [],
                {"customers": {}, "cards": {}, "skipped": {}},
                {
                    "imported": False,
                    "batch_id": None,
                    "error_rows": 0,
                    "rerun_suspected": False,
                    "rerun_reason": "",
                },
                {
                    "rows": {
                        "测试员": {
                            "total": Decimal("50.00"),
                            "cash": Decimal("50.00"),
                            "card": Decimal("0.00"),
                        }
                    },
                    "unreplayed": {},
                    "invalid_numbers": 0,
                    "empty_staff": 0,
                },
                {
                    "测试员": {
                        "total": Decimal("100.00"),
                        "cash": Decimal("50.00"),
                        "card": Decimal("50.00"),
                    }
                },
                employees["commission"],
                {},
                ["测试员"],
                {},
                [],
                employees["invalid_numbers"],
                employees["item_total_invalid_numbers"],
            )
            row = next(item for item in result["performance"] if item["staff"] == "测试员")
            self.assertIsNone(row["mgj_month_total"])
            self.assertIsNone(row["diff_month_total"])
            self.assertFalse(result["performance_month_available"])
            self.assertEqual(result["performance_item_total_invalid_numbers"], 0)
            self.assertIsNone(result["performance_totals"]["mgj_month_total"])
            self.assertIsNone(result["performance_totals"]["diff_month_total"])
            self.assertEqual(result["commission"][0]["mgj_ratio"], "20.00%")
            self.assertEqual(result["commission_totals"]["mgj_ratio"], "20.00%")
            report = replay.render_report(result)
            self.assertIn("美管加月度汇总不可用，业绩只按逐行流水对照", report)
            self.assertNotIn("美管加月度业绩对照使用", report)

    def test_unreplayed_name_without_monthly_staff_is_surfaced(self):
        result = replay.build_result(
            "2026-08",
            "2026-11-15",
            [],
            [],
            {"customers": {}, "cards": {}, "skipped": {}},
            {
                "imported": False,
                "batch_id": None,
                "error_rows": 0,
                "rerun_suspected": False,
                "rerun_reason": "",
            },
            {
                "rows": {
                    "测试员": {
                        "total": Decimal("50.00"),
                        "cash": Decimal("50.00"),
                        "card": Decimal("0.00"),
                    }
                },
                "unreplayed": {"外部员工": Decimal("30.00")},
                "invalid_numbers": 0,
                "empty_staff": 0,
            },
            {
                "测试员": {
                    "total": Decimal("50.00"),
                    "cash": Decimal("50.00"),
                    "card": Decimal("0.00"),
                }
            },
            {
                "测试员": {
                    "item_total": Decimal("100.00"),
                    "item_commission": Decimal("10.00"),
                    "card_sales_performance": Decimal("0.00"),
                    "card_sales_commission": Decimal("0.00"),
                }
            },
            {},
            ["测试员"],
            {},
            [],
            0,
        )
        self.assertEqual(result["performance_unmatched_unreplayed"]["names"], 1)
        self.assertEqual(result["performance_unmatched_unreplayed"]["total"], "30.00")
        self.assertEqual(result["performance"][0]["mgj_month_total"], "100.00")
        report = replay.render_report(result)
        self.assertIn(
            "未回放单中有 1 个员工名对不上月度员工，合计 ¥30.00 未扣除", report
        )

    def test_commission_ratio_uses_month_item_total(self):
        result = replay.build_result(
            "2026-08",
            "2026-11-15",
            [],
            [],
            {"customers": {}, "cards": {}, "skipped": {}},
            {
                "imported": False,
                "batch_id": None,
                "error_rows": 0,
                "rerun_suspected": False,
                "rerun_reason": "",
            },
            {
                "rows": {
                    "测试员": {
                        "total": Decimal("50.00"),
                        "cash": Decimal("50.00"),
                        "card": Decimal("0.00"),
                    }
                },
                "unreplayed": {},
                "invalid_numbers": 0,
                "empty_staff": 0,
            },
            {
                "测试员": {
                    "total": Decimal("100.00"),
                    "cash": Decimal("50.00"),
                    "card": Decimal("50.00"),
                }
            },
            {
                "测试员": {
                    "item_total": Decimal("100.00"),
                    "item_commission": Decimal("10.00"),
                    "card_sales_performance": Decimal("0.00"),
                    "card_sales_commission": Decimal("0.00"),
                }
            },
            {
                "测试员": {
                    "item": Decimal("5.00"),
                    "consume": Decimal("0.00"),
                    "labor": Decimal("0.00"),
                }
            },
            ["测试员"],
            {},
            [],
            0,
        )
        self.assertEqual(result["commission"][0]["mgj_ratio"], "10.00%")
        self.assertEqual(result["commission_totals"]["mgj_ratio"], "10.00%")
        self.assertEqual(result["performance"][0]["mgj_month_total"], "100.00")

    def test_commission_pagination_over_200(self):
        state = ReplayStubState()
        state.commission_entries = [
            {
                "id": f"commission-{index}",
                "source": "item",
                "staff": {"id": "staff-1", "name": "张岩"},
                "amount": "1.00",
            }
            for index in range(205)
        ]
        api = self.start_stub(state)
        client = Client(api)
        client.login("boss", "pw")
        entries = replay._all_commissions(client, "2026-11-15")
        self.assertEqual(len(entries), 205)
        self.assertEqual(
            [request["path"] for request in state.requests].count("/api/ops/list_commissions"),
            2,
        )

    def test_invalid_source_number_counts(self):
        with TemporaryDirectory() as temp:
            db = Path(temp) / "fixture.sqlite"
            fixture.build_fixture(db)
            execute(
                db,
                'INSERT INTO ep_super_operation_report_search__t3 '
                '(is_latest_copy, "员工项目业绩流水·消费时间", "员工项目业绩流水·流水单号", '
                '"员工项目业绩流水·总业绩", "员工项目业绩流水·现金类业绩", '
                '"员工项目业绩流水·卡扣业绩", "员工项目业绩流水·员工姓名") '
                "VALUES (1, '2026-09-01', 'B001', '', 'bad', '1.00', '张岩')",
            )
            execute(
                db,
                "INSERT INTO business_employee_commission_monthly "
                "(period_month, employee_label, item_total, item_commission, "
                "card_sales_performance, card_sales_commission) "
                "VALUES ('2026-09', '99 号 首席 [测试员]', 'bad', '', '', 'bad')",
            )
            conn = replay._readonly_connect(db)
            try:
                performance = replay._source_performance(conn, "2026-09", {"B001"})
                employees = replay.monthly_employees(conn, "2026-09")
            finally:
                conn.close()
            self.assertEqual(performance["invalid_numbers"], 2)
            self.assertEqual(employees["invalid_numbers"], 3)
            self.assertEqual(employees["item_total_invalid_numbers"], 1)
            self.assertEqual(
                employees["commission"]["测试员"]["item_total"], Decimal("0.00")
            )
            result = replay.build_result(
                "2026-09",
                "2026-11-15",
                [],
                [],
                {"customers": {}, "cards": {}, "skipped": {}},
                {
                    "imported": False,
                    "batch_id": None,
                    "error_rows": 0,
                    "rerun_suspected": False,
                    "rerun_reason": "",
                },
                performance,
                {},
                employees["commission"],
                {},
                [row["name"] for row in employees["staff"]],
                employees["skipped"],
                [],
                employees["invalid_numbers"],
                employees["item_total_invalid_numbers"],
            )
            self.assertEqual(result["commission_invalid_numbers"], 3)
            self.assertEqual(result["performance_item_total_invalid_numbers"], 1)
            report = replay.render_report(result)
            self.assertIn("美管加月度业绩数字为空或无法解析：1 个，按 0 计。", report)
            self.assertIn("美管加提成/卡销售数字为空或无法解析：3 个，按 0 计。", report)
            self.assertIn("员工姓名为空（含未回放单）", report)

    def test_report_difference_uses_decimal(self):
        self.assertEqual(replay._diff(Decimal("0.30"), Decimal("0.10")), Decimal("0.20"))
        self.assertEqual(
            replay.money.money(replay._diff(Decimal("0.30"), Decimal("0.10"))), "0.20"
        )

if __name__ == "__main__":
    unittest.main()
