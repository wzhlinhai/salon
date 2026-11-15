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

from mgj import extract, fixture, load, seed
from mgj.__main__ import main

MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
REL_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"


def read_workbook(path):
    with zipfile.ZipFile(path) as archive:
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


if __name__ == "__main__":
    unittest.main()
