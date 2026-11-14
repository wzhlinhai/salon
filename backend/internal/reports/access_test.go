package reports_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
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
	refundOrder := f.checkout(f.order(customer, []uuid.UUID{f.staffB}, true, "female", f.item), payment("cash", "100"))
	refund := f.call("create_aftersale", map[string]any{"order_id": refundOrder["id"], "type": "partial_refund", "reason": "退款", "refund_lines": []any{payment("cash", "10.00")}}, 200)
	f.call("approve_aftersale", map[string]any{"id": refund["id"]}, 200)
	voidCard := f.openCard(customer, f.stored, "100.00", "0", payment("cash", "100"))
	voidCardOrder := f.checkout(f.order(customer, []uuid.UUID{f.staffB}, false, "male", f.item), f.cardPayment(id(voidCard["id"]), "100"))
	f.call("void_order", map[string]any{"order_id": voidCardOrder["id"], "reason": "录入错误"}, 200)
	unknown := f.customer("未识别", "", nil)
	f.checkout(f.order(unknown, []uuid.UUID{f.staffB}, false, "female", f.item), payment("cash", "100"))
	mainDebt := f.call("register_historical_debt", map[string]any{"customer_id": customer, "amount": "10.00", "incurred_on": "2026-11-02"}, 200)
	f.call("repay_debt", map[string]any{"debt_id": mainDebt["id"], "amount": "10.00", "method": "cash"}, 200)

	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var otherStore, otherCustomer, otherDebt uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO stores (tenant_id,name) VALUES ($1,'外店') RETURNING id`, f.Res.TenantID).Scan(&otherStore); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `INSERT INTO customers (tenant_id,store_id,phone,created_by) VALUES ($1,$2,'13800000099',$3) RETURNING id`, f.Res.TenantID, otherStore, f.Res.OwnerID).Scan(&otherCustomer); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `INSERT INTO debts (tenant_id,store_id,customer_id,amount,business_date,recorded_by) VALUES ($1,$2,$3,20,'2026-11-02',$4) RETURNING id`, f.Res.TenantID, otherStore, otherCustomer, f.Res.OwnerID).Scan(&otherDebt); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `INSERT INTO debt_repayments (tenant_id,store_id,debt_id,amount,method,business_date,repaid_by) VALUES ($1,$2,$3,20,'cash','2026-11-02',$4)`, f.Res.TenantID, otherStore, otherDebt, f.Res.OwnerID)
	dbtest.Commit(t, tx)

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

	business := f.call("get_business_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02", "granularity": "day"}, 200)
	if got := business["rows"].([]any)[0].(map[string]any)["debt_repayment"]; got != "10.00" {
		t.Fatalf("debt repayment includes foreign store: %v", got)
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
