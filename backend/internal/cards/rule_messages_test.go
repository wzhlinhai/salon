package cards_test

import "testing"

func TestAdjustmentRuleMessages(t *testing.T) {
	s := open(t)
	card, item, _ := s.countCard("count")
	before := upgradeSnapshot(t, s)
	for _, tc := range []struct {
		input  map[string]any
		detail string
	}{
		{map[string]any{"card_type_id": s.cardType}, "卡类型必须同类"},
		{map[string]any{"counts": []any{map[string]any{"item_id": item, "paid": 10, "gift": 3}, map[string]any{"item_id": item, "paid": 10, "gift": 3}}}, "次数目标不能重复或为负"},
	} {
		tc.input["card_id"], tc.input["reason"] = card["id"], "核对"
		if r := s.call("adjust_card", tc.input, 409); r["detail"] != tc.detail {
			t.Fatal(r)
		}
		if upgradeSnapshot(t, s) != before {
			t.Fatal("rejected adjustment changed rows")
		}
	}
}

func TestDuplicateUpgradeRuleMessage(t *testing.T) {
	s := open(t)
	target := s.call("create_card_type", typeInput("高级"), 200)
	input := rule(s.cardType, target["id"], "single_recharge", "100")
	s.call("create_card_upgrade_rule", input, 200)
	before := upgradeSnapshot(t, s)
	if r := s.call("create_card_upgrade_rule", input, 409); r["detail"] != "升级规则已存在" {
		t.Fatal(r)
	}
	if upgradeSnapshot(t, s) != before {
		t.Fatal("rejected rule changed rows")
	}
}

func TestRefundOverflowMessage(t *testing.T) {
	s := open(t)
	item := countItem(s.item("高额项目"))
	item["paid_count"], item["unit_price"] = 1, "99999999.99"
	ct := s.call("create_card_type", countType("高额次卡", "count", item), 200)
	input := s.opening()
	input["card_type_id"], input["principal"], input["bonus"] = ct["id"], "0", "0"
	card := s.call("open_card", input, 200)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "reason": "更正购买次数", "counts": []any{map[string]any{"item_id": item["item_id"], "paid": 2, "gift": 3}}}, 200)
	before := s.refundSnapshot()
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, s.MustCaller(s.Owner), "preview_card_refund", map[string]any{"card_id": card["id"]})
			if r.Status != 409 || r.Detail() != "退款金额超过上限" {
				t.Fatal(r)
			}
		})
	}
	if r := s.call("refund_card", map[string]any{"card_id": card["id"], "method": "cash", "amount": "1"}, 409); r["detail"] != "退款金额超过上限" {
		t.Fatal(r)
	}
	if s.refundSnapshot() != before {
		t.Fatal("rejected refund changed rows")
	}
}
