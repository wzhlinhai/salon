"""把项目与卡类型目录按名称幂等地写入后端。"""

import json
from decimal import InvalidOperation

from . import money
from .client import Client


def _same_money(left, right):
    try:
        return money.quantize(left) == money.quantize(right)
    except (InvalidOperation, ValueError):
        return False


def _same_discount(left, right):
    if left is None and right is None:
        return True
    if left is None or right is None:
        return False
    return _same_money(left, right)


def _item_payload(item):
    return {
        "name": item["name"],
        "kind": "service",
        "price": money.money(item["price"]),
        "discountable": True,
        "labor_fee_amount": None,
        "labor_fee_percent": None,
        "positions": [],
        "level_prices": [],
    }


def _item_conflicts(existing, payload):
    reasons = []
    if existing.get("kind", "service") != payload["kind"]:
        reasons.append("kind")
    if not _same_money(existing.get("price"), payload["price"]):
        reasons.append("price")
    if bool(existing.get("discountable", False)) != payload["discountable"]:
        reasons.append("discountable")
    return reasons


def _card_type_payload(card_type, item_ids_by_name):
    if card_type["kind"] == "stored_value":
        payload = {
            "name": card_type["name"],
            "kind": "stored_value",
            "validity_kind": "none",
            "items": [],
        }
        if card_type.get("discount_percent") is not None:
            payload["discount_percent"] = card_type["discount_percent"]
        return payload
    item = card_type["items"][0]
    try:
        item_id = item_ids_by_name[item["item_name"]]
    except KeyError as error:
        raise RuntimeError("目录缺少项目：" + item["item_name"]) from error
    return {
        "name": card_type["name"],
        "kind": "count",
        "validity_kind": "none",
        "items": [
            {
                "item_id": item_id,
                "paid_count": item["paid_count"],
                "gift_count": item["gift_count"],
                "unit_price": money.money(item["unit_price"]),
            }
        ],
    }


def _card_type_conflicts(existing, payload):
    reasons = []
    if existing.get("kind") != payload["kind"]:
        reasons.append("kind")
    if existing.get("validity_kind", "none") != payload["validity_kind"]:
        reasons.append("validity_kind")
    if not _same_discount(existing.get("discount_percent"), payload.get("discount_percent")):
        reasons.append("discount_percent")
    expected_items = payload.get("items", [])
    existing_items = existing.get("items") or []
    if len(existing_items) != len(expected_items):
        reasons.append("items")
        return reasons
    expected_by_id = {str(item["item_id"]): item for item in expected_items}
    existing_by_id = {str(item.get("item_id")): item for item in existing_items}
    if set(expected_by_id) != set(existing_by_id):
        reasons.append("items")
        return reasons
    for item_id, expected in expected_by_id.items():
        existing_item = existing_by_id[item_id]
        if int(existing_item.get("paid_count", 0)) != int(expected["paid_count"]):
            reasons.append("paid_count")
        if int(existing_item.get("gift_count", 0)) != int(expected["gift_count"]):
            reasons.append("gift_count")
        if not _same_money(existing_item.get("unit_price"), expected["unit_price"]):
            reasons.append("unit_price")
    return reasons


def run(api, account, password, catalog_path):
    """登录后只创建缺失的项目和卡类型，返回 created/existing/conflicts 摘要。"""
    with open(catalog_path, encoding="utf-8") as source:
        catalog = json.load(source)
    client = Client(api)
    client.login(account, password)

    items_by_name = {
        item["name"]: item for item in client.op("list_items").get("items", [])
    }
    card_types_by_name = {
        card_type["name"]: card_type
        for card_type in client.op("list_card_types").get("items", [])
    }
    item_ids_by_name = {
        name: item["id"] for name, item in items_by_name.items() if "id" in item
    }

    summary = {"created": [], "existing": [], "conflicts": []}
    for item in catalog.get("items", []):
        payload = _item_payload(item)
        existing = items_by_name.get(item["name"])
        if existing is not None:
            reasons = _item_conflicts(existing, payload)
            if reasons:
                summary["conflicts"].append(
                    {"kind": "item", "name": item["name"], "reasons": reasons}
                )
            else:
                summary["existing"].append({"kind": "item", "name": item["name"]})
            if "id" in existing:
                item_ids_by_name[item["name"]] = existing["id"]
            continue
        created = client.op("create_item", payload)
        items_by_name[item["name"]] = created
        item_ids_by_name[item["name"]] = created["id"]
        summary["created"].append({"kind": "item", "name": item["name"]})

    for card_type in catalog.get("card_types", []):
        payload = _card_type_payload(card_type, item_ids_by_name)
        existing = card_types_by_name.get(card_type["name"])
        if existing is not None:
            reasons = _card_type_conflicts(existing, payload)
            if reasons:
                summary["conflicts"].append(
                    {"kind": "card_type", "name": card_type["name"], "reasons": reasons}
                )
            else:
                summary["existing"].append(
                    {"kind": "card_type", "name": card_type["name"]}
                )
            continue
        created = client.op("create_card_type", payload)
        card_types_by_name[card_type["name"]] = created
        summary["created"].append({"kind": "card_type", "name": card_type["name"]})

    return summary
