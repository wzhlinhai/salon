package reports_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

func TestItemReportTotalsTieServiceRevenue(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000021", nil)
	f.now("2026-11-02", 12)
	stored := f.openCard(customer, f.stored, "200.00", "20.00", payment("cash", "200"))
	count := f.openCard(customer, f.count, "0", "0", payment("cash", "300"))

	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	countOrder := f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item)
	countLine := id(f.line(countOrder, 0)["id"])
	f.checkout(countOrder, f.countPayment(id(count["id"]), countLine, "100"))
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), f.cardPayment(id(stored["id"]), "30"), payment("cash", "70"))
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, false, "female", f.goods), payment("cash", "50"))

	report := f.call("get_item_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	rows := report["rows"].([]any)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	service := rows[0].(map[string]any)
	goods := rows[1].(map[string]any)
	if service["item_name"] != "剪发" || service["times"].(float64) != 3 || service["paid"].(string) != "300.00" || service["card_paid"].(string) != "30.00" || service["count_paid"].(string) != "100.00" || service["designated"].(float64) != 3 || service["performance"].(string) != "200.00" {
		t.Fatal(service)
	}
	if goods["item_name"] != "洗发水" || goods["paid"].(string) != "50.00" || goods["performance"].(string) != "50.00" {
		t.Fatal(goods)
	}
	assertItemTotals(t, rows, report["totals"].(map[string]any))
	business := f.call("get_business_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02", "granularity": "day"}, 200)
	serviceRevenue := business["rows"].([]any)[0].(map[string]any)["service_revenue"].(string)
	if report["totals"].(map[string]any)["paid"].(string) != serviceRevenue {
		t.Fatalf("item paid %v, service revenue %v", report["totals"], serviceRevenue)
	}
}

func assertItemTotals(t *testing.T, rows []any, totals map[string]any) {
	t.Helper()
	for _, field := range []string{"line_price", "paid", "card_paid", "count_paid", "groupbuy_paid", "performance"} {
		sum := money.SignedTotal{}
		for _, raw := range rows {
			sum = sum.Add(changeOf(t, raw.(map[string]any)[field].(string)))
		}
		if sum.Format() != totals[field] {
			t.Fatalf("%s total = %v, want %v", field, totals[field], sum.Format())
		}
	}
	var times, designated float64
	for _, raw := range rows {
		times += raw.(map[string]any)["times"].(float64)
		designated += raw.(map[string]any)["designated"].(float64)
	}
	if totals["times"].(float64) != times || totals["designated"].(float64) != designated {
		t.Fatalf("counts total = %v/%v, want %v/%v", totals["times"], totals["designated"], times, designated)
	}
}
