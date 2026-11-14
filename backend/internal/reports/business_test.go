package reports_test

import (
	"reflect"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/money"

	"github.com/google/uuid"
)

func TestBusinessReportMatchesClosing(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000001", nil)

	f.now("2026-11-02", 10)
	backfill := f.backfillOrder(customer, []uuid.UUID{f.staffA}, true, "female", "2026-11-01", f.item)
	backfillPaid := f.checkout(backfill, payment("cash", "100"))

	f.now("2026-11-02", 12)
	card := f.openCard(customer, f.stored, "200.00", "20.00", payment("cash", "200"))
	_ = f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), f.cardPayment(id(card["id"]), "50"), payment("cash", "50"))
	debtPaid := f.checkout(f.order(customer, []uuid.UUID{f.staffB}, false, "male", f.item), payment("debt", "30"), payment("cash", "70"))
	debtID := id(paymentOf(t, debtPaid, "debt")["debt_id"])
	f.call("repay_debt", map[string]any{"debt_id": debtID, "amount": "10.00", "method": "cash"}, 200)
	aftersale := f.call("create_aftersale", map[string]any{"order_id": backfillPaid["id"], "type": "partial_refund", "reason": "退款", "refund_lines": []any{payment("cash", "10.00")}}, 200)
	f.call("approve_aftersale", map[string]any{"id": aftersale["id"]}, 200)
	refunded := f.openCard(customer, f.stored, "100.00", "0", payment("cash", "100"))
	quote := f.call("preview_card_refund", map[string]any{"card_id": refunded["id"]}, 200)
	f.call("refund_card", map[string]any{"card_id": refunded["id"], "method": "cash", "amount": quote["amount"]}, 200)

	toVoid := f.checkout(f.order(customer, []uuid.UUID{f.staffA}, false, "female", f.item, f.goods), f.cardPayment(id(card["id"]), "50"), payment("cash", "100"))
	f.now("2026-11-03", 12)
	f.call("void_order", map[string]any{"order_id": toVoid["id"], "reason": "录入错误"}, 200)
	f.checkout(f.order(customer, []uuid.UUID{f.staffB}, true, "male", f.goods), payment("cash", "50"))
	f.call("register_historical_debt", map[string]any{"customer_id": customer, "amount": "25.00", "incurred_on": "2026-11-03"}, 200)

	report := f.call("get_business_report", map[string]any{"from": "2026-10-31", "to": "2026-11-04", "granularity": "day"}, 200)
	rows := report["rows"].([]any)
	if len(rows) != 5 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].(map[string]any)["period"] != "2026-10-31" || rows[4].(map[string]any)["period"] != "2026-11-04" {
		t.Fatal(rows)
	}
	for _, raw := range rows {
		row := raw.(map[string]any)
		day := row["period"].(string)
		closing := f.call("preview_day_close", map[string]any{"date": day}, 200)
		for _, pair := range [][2]string{{"on_site", "on_site"}, {"non_site", "non_site"}, {"guest_total", "guest_visits"}, {"checkout_count", "checkout_count"}, {"void_count", "void_count"}} {
			if !reflect.DeepEqual(row[pair[0]], closing[pair[1]]) {
				t.Fatalf("%s %s: report %#v closing %#v", day, pair[0], row[pair[0]], closing[pair[1]])
			}
		}
	}
	assertBusinessTotals(t, rows, report["totals"].(map[string]any))
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["period"] == "2026-11-02" && row["new_debt"].(string) != "30.00" {
			t.Fatalf("new debt 11-02 = %v", row["new_debt"])
		}
		if row["period"] == "2026-11-03" && row["new_debt"].(string) != "25.00" {
			t.Fatalf("historical new debt 11-03 = %v", row["new_debt"])
		}
	}
	item := f.call("get_item_report", map[string]any{"from": "2026-10-31", "to": "2026-11-04"}, 200)
	if item["totals"].(map[string]any)["paid"] != report["totals"].(map[string]any)["service_revenue"] {
		t.Fatalf("item paid %v, service revenue %v", item["totals"], report["totals"])
	}

	month := f.call("get_business_report", map[string]any{"from": "2026-10", "to": "2026-11", "granularity": "month"}, 200)
	months := month["rows"].([]any)
	if len(months) != 2 {
		t.Fatal(months)
	}
	if months[0].(map[string]any)["period"] != "2026-10" || months[1].(map[string]any)["period"] != "2026-11" {
		t.Fatal(months)
	}
	assertBusinessTotals(t, months, month["totals"].(map[string]any))
}

func paymentOf(t *testing.T, order map[string]any, method string) map[string]any {
	t.Helper()
	for _, raw := range order["payments"].([]any) {
		p := raw.(map[string]any)
		if p["method"] == method {
			return p
		}
	}
	t.Fatalf("payment %s not found", method)
	return nil
}

func assertBusinessTotals(t *testing.T, rows []any, totals map[string]any) {
	t.Helper()
	for _, field := range []string{"on_site", "non_site"} {
		sum := map[string]money.SignedTotal{}
		for _, raw := range rows {
			for _, lineRaw := range raw.(map[string]any)[field].([]any) {
				line := lineRaw.(map[string]any)
				method := line["method"].(string)
				sum[method] = sum[method].Add(changeOf(t, line["amount"].(string)))
			}
		}
		for _, lineRaw := range totals[field].([]any) {
			line := lineRaw.(map[string]any)
			if sum[line["method"].(string)].Format() != line["amount"] {
				t.Fatalf("%s %s total = %s, want %s", field, line["method"], line["amount"], sum[line["method"].(string)].Format())
			}
		}
	}
	for _, field := range []string{"service_revenue", "sales_revenue", "card_principal", "card_bonus", "count_amount", "groupbuy_deduct", "new_debt", "debt_repayment", "aftersale_refund"} {
		sum := money.SignedTotal{}
		for _, raw := range rows {
			sum = sum.Add(changeOf(t, raw.(map[string]any)[field].(string)))
		}
		if sum.Format() != totals[field] {
			t.Fatalf("%s total = %s, want %s", field, totals[field], sum.Format())
		}
	}
	for _, field := range []string{"guest_total", "guest_designated", "guest_not_designated", "guest_male", "guest_female"} {
		var sum int64
		for _, raw := range rows {
			sum += shareScaled(t, raw.(map[string]any)[field].(string))
		}
		if got := totals[field].(string); shareScaled(t, got) != sum {
			t.Fatalf("%s total = %s, want %d", field, got, sum)
		}
	}
	for _, field := range []string{"checkout_count", "void_count"} {
		var sum int64
		for _, raw := range rows {
			sum += int64(raw.(map[string]any)[field].(float64))
		}
		if int64(totals[field].(float64)) != sum {
			t.Fatalf("%s total = %v, want %d", field, totals[field], sum)
		}
	}
}
