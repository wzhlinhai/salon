package reports_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestReportPermissionsAndRanges(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000051", nil)
	f.now("2026-11-02", 12)
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	card := f.openCard(customer, f.stored, "100.00", "0", payment("cash", "100"))
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, false, "male", f.item), f.cardPayment(id(card["id"]), "50"), payment("cash", "50"))
	countCard := f.openCard(customer, f.count, "0", "0", payment("cash", "300"))
	countOrder := f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item)
	f.checkout(countOrder, f.countPayment(id(countCard["id"]), id(f.line(countOrder, 0)["id"]), "100"))
	refunded := f.openCard(customer, f.stored, "50.00", "0", payment("cash", "50"))
	quote := f.call("preview_card_refund", map[string]any{"card_id": refunded["id"]}, 200)
	f.call("refund_card", map[string]any{"card_id": refunded["id"], "method": "cash", "amount": quote["amount"]}, 200)
	voided := f.checkout(f.order(customer, []uuid.UUID{f.staffA}, false, "male", f.item), payment("cash", "100"))
	f.call("void_order", map[string]any{"order_id": voided["id"], "reason": "录入错误"}, 200)

	owner := f.MustCaller(f.Owner)
	cases := []struct {
		name       string
		permission string
		args       map[string]any
	}{
		{"get_business_report", "report.view", map[string]any{"from": "2026-11-02", "to": "2026-11-02", "granularity": "day"}},
		{"get_staff_report", "performance.view", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}},
		{"get_item_report", "report.view", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}},
		{"get_card_report", "report.view", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}},
		{"get_customer_source_report", "report.view", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}},
	}
	for _, tc := range cases {
		denied := owner
		denied.Permissions = map[ops.Permission]ops.Grant{}
		for key, grant := range owner.Permissions {
			if string(key) != tc.permission {
				denied.Permissions[key] = grant
			}
		}
		for _, ch := range f.Channels() {
			got := ch.Call(t, denied, tc.name, tc.args)
			if got.Status != 403 || got.Detail() != "permission denied" {
				t.Fatalf("%s %s: %d %q", ch.Name, tc.name, got.Status, got.Detail())
			}
		}
	}
	for _, tc := range []struct {
		name   string
		args   map[string]any
		detail string
	}{
		{"get_business_report", map[string]any{"from": "2026-01-01", "to": "2026-04-04", "granularity": "day"}, "查询范围最多 92 天"},
		{"get_business_report", map[string]any{"from": "2025-01", "to": "2026-02", "granularity": "month"}, "查询范围最多 12 个月"},
		{"get_business_report", map[string]any{"from": "2026-13", "to": "2026-11", "granularity": "month"}, "请填写有效的月份"},
		{"get_business_report", map[string]any{"from": "2026-12", "to": "2026-11", "granularity": "month"}, "开始月份不能晚于结束月份"},
		{"get_staff_report", map[string]any{"from": "2026-04-04", "to": "2026-01-01"}, "开始营业日不能晚于结束营业日"},
		{"get_item_report", map[string]any{"from": "2026-01-01", "to": "2026-04-04"}, "查询范围最多 92 天"},
		{"get_card_report", map[string]any{"from": "2026-01-01", "to": "2026-04-04"}, "查询范围最多 92 天"},
		{"get_customer_source_report", map[string]any{"from": "2026-01-01", "to": "2026-04-04"}, "查询范围最多 92 天"},
	} {
		for _, ch := range f.Channels() {
			got := ch.Call(t, owner, tc.name, tc.args)
			if got.Status != 409 || got.Detail() != tc.detail {
				t.Fatalf("%s %s: %d %q", ch.Name, tc.name, got.Status, got.Detail())
			}
		}
	}

	other := owner
	other.StoreID = uuid.New()
	for _, tc := range cases {
		got := f.HTTP(t, &other, tc.name, tc.args)
		if got.Status != 200 {
			t.Fatal(tc.name, got)
		}
		if tc.name == "get_business_report" {
			for _, raw := range got.Body["rows"].([]any) {
				row := raw.(map[string]any)
				for _, field := range []string{"service_revenue", "sales_revenue", "card_principal", "card_bonus", "count_amount", "groupbuy_deduct", "new_debt", "debt_repayment", "aftersale_refund", "guest_total", "guest_designated", "guest_not_designated", "guest_male", "guest_female"} {
					if row[field] != "0.00" && row[field] != "0.0000" {
						t.Fatalf("%s other store: %s=%v", tc.name, field, row[field])
					}
				}
				for _, field := range []string{"on_site", "non_site"} {
					for _, lineRaw := range row[field].([]any) {
						if lineRaw.(map[string]any)["amount"] != "0.00" {
							t.Fatalf("%s other store: %#v", tc.name, row)
						}
					}
				}
				if row["checkout_count"].(float64) != 0 || row["void_count"].(float64) != 0 {
					t.Fatalf("%s other store: %#v", tc.name, row)
				}
			}
			continue
		}
		if tc.name == "get_customer_source_report" {
			for _, raw := range got.Body["rows"].([]any) {
				row := raw.(map[string]any)
				if row["new_customers"].(float64) != 0 || row["returning"].(float64) != 0 || row["revenue"] != "0.00" {
					t.Fatalf("%s other store: %#v", tc.name, row)
				}
			}
			if got.Body["unidentified_orders"].(float64) != 0 {
				t.Fatalf("%s other store: %#v", tc.name, got.Body)
			}
			continue
		}
		if len(got.Body["rows"].([]any)) != 0 {
			t.Fatalf("%s other store: %#v", tc.name, got.Body)
		}
	}
}
