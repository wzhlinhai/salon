package cards_test

import (
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"reflect"
	"testing"
)

func TestCardTypeDiscount(t *testing.T) {
	s := open(t)
	in := typeInput("折扣类型")
	in["discount_percent"] = "88"
	created := s.call("create_card_type", in, 200)
	if created["discount_percent"] != "88.00" {
		t.Fatal(created)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_card_types", map[string]any{})
		if r.Status != 200 || !reflect.DeepEqual(r.Body["items"].([]any)[1], created) {
			t.Fatal(ch.Name, r)
		}
	}
	in["id"], in["active"], in["discount_percent"] = created["id"], true, "90"
	updated := s.call("update_card_type", in, 200)
	if updated["discount_percent"] != "90.00" {
		t.Fatal(updated)
	}
	audits := s.call("list_audit_log", map[string]any{"target_id": created["id"]}, 200)["entries"].([]any)
	if len(audits) != 2 || !reflect.DeepEqual(audits[0].(map[string]any)["before"], created) || !reflect.DeepEqual(audits[0].(map[string]any)["after"], updated) {
		t.Fatal(audits)
	}
	delete(in, "discount_percent")
	cleared := s.call("update_card_type", in, 200)
	if cleared["discount_percent"] != nil {
		t.Fatal(cleared)
	}
	for _, value := range []string{"0", "100"} {
		in["discount_percent"] = value
		for _, op := range []string{"create_card_type", "update_card_type"} {
			before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "card_types", "audit_log")
			args := make(map[string]any, len(in))
			for k, v := range in {
				args[k] = v
			}
			if op == "create_card_type" {
				delete(args, "id")
				delete(args, "active")
			}
			r := s.HTTPWithToken(t, s.Owner, op, args)
			if r.Status != 409 || r.Body["detail"] != "折扣率须大于 0 折、小于 10 折" || before != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "card_types", "audit_log") {
				t.Fatal(r)
			}
		}
	}
}
func TestCountCardDiscountRefused(t *testing.T) {
	s := open(t)
	item := s.call("create_item", map[string]any{"name": "剪发", "price": "100", "discountable": true, "positions": []any{}, "level_prices": []any{}, "labor_fee_amount": nil, "labor_fee_percent": nil}, 200)
	in := map[string]any{"name": "次卡", "kind": "count", "validity_kind": "none", "items": []any{map[string]any{"item_id": item["id"], "paid_count": 1, "gift_count": 0, "unit_price": "100"}}}
	ct := s.call("create_card_type", in, 200)
	in["discount_percent"] = "88"
	for _, op := range []string{"create_card_type", "update_card_type"} {
		if op == "update_card_type" {
			in["id"], in["active"] = ct["id"], true
		}
		before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "card_types", "audit_log")
		args := make(map[string]any, len(in))
		for k, v := range in {
			args[k] = v
		}
		if op == "create_card_type" {
			delete(args, "id")
			delete(args, "active")
		}
		r := s.HTTPWithToken(t, s.Owner, op, args)
		if r.Status != 409 || r.Body["detail"] != "次卡不能设折扣率" || before != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "card_types", "audit_log") {
			t.Fatal(r)
		}
	}
}
