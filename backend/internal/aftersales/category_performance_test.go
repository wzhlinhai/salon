package aftersales_test

import "testing"

func (f *fixture) setItemCategory(ratio int) {
	f.t.Helper()
	category := f.call("create_item_category", map[string]any{"name": "售后类别", "performance_ratio": ratio}, 200)
	f.call("update_item", map[string]any{
		"id": f.item, "name": "售后项目", "price": "100.00", "discountable": true,
		"labor_fee_amount": "10.00", "labor_fee_percent": nil,
		"positions":    []any{map[string]any{"position_id": f.position, "required": true}},
		"level_prices": []any{}, "active": true, "category_id": category["id"],
	}, 200)
}

func TestAftersaleReversalUsesCategoryRatio(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.setItemCategory(90)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	before := f.performanceTotals("2026-10-01", "2026-10-01")
	if before["item"] != "90.00" {
		t.Fatal(before)
	}
	f.now("2026-10-02", 12)
	refund := f.create(id(order["id"]), "partial_refund", "部分退款", payment("cash", "40"))
	f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": false})
	after := f.performanceTotals("2026-10-01", "2026-10-02")
	if after["item"] != "54.00" {
		t.Fatal(after)
	}
	var reversal map[string]any
	for _, raw := range f.performanceEntries("2026-10-02", "2026-10-02") {
		row := raw.(map[string]any)
		if row["reversal"] == true {
			reversal = row
		}
	}
	if reversal == nil || reversal["paid_amount"] != "40.00" || reversal["ratio"] != float64(100) || reversal["category_ratio"] != float64(90) || reversal["amount"] != "36.00" {
		t.Fatal(reversal)
	}
}

func TestVoidReversalCopiesCategoryAmounts(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.setItemCategory(90)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	f.call("void_order", map[string]any{"order_id": order["id"], "reason": "撤单"}, 200)
	var itemReversal, laborReversal map[string]any
	for _, raw := range f.performanceEntries("2026-10-02", "2026-10-02") {
		row := raw.(map[string]any)
		if row["reversal"] != true {
			continue
		}
		switch row["kind"] {
		case "item":
			itemReversal = row
		case "labor":
			laborReversal = row
		}
	}
	if itemReversal["paid_amount"] != "100.00" || itemReversal["amount"] != "90.00" || itemReversal["category_ratio"] != float64(90) {
		t.Fatal(itemReversal)
	}
	if laborReversal["amount"] != "10.00" || laborReversal["category_ratio"] != float64(100) {
		t.Fatal(laborReversal)
	}
	if got := f.performanceTotals("2026-10-01", "2026-10-02"); got["item"] != "0.00" || got["labor"] != "0.00" {
		t.Fatal(got)
	}
}
