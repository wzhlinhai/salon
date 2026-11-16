package reports_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func TestItemReportFullSplitKeepsCollectedAmounts(t *testing.T) {
	f := open(t)
	f.setting("performance_split", "full")
	item := id(f.call("create_item", map[string]any{
		"name": "全额剪发", "price": "100.00", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions": []any{
			map[string]any{"position_id": f.position, "required": false},
			map[string]any{"position_id": f.position, "required": false},
		},
		"level_prices": []any{},
	}, 200)["id"])
	customer := f.customer("全额顾客", "13800000071", nil)
	order := f.order(customer, []uuid.UUID{f.staffA, f.staffB}, true, "female", item)
	card := f.openCard(customer, f.stored, "200.00", "0", payment("cash", "200"))
	f.checkout(order, f.cardPayment(id(card["id"]), "100"))

	report := f.call("get_item_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	rows := report["rows"].([]any)
	var row map[string]any
	for _, raw := range rows {
		candidate := raw.(map[string]any)
		if candidate["item_id"] == item.String() {
			row = candidate
		}
	}
	if row == nil {
		t.Fatal(rows)
	}
	if row["paid"] != "100.00" || row["card_paid"] != "100.00" || row["groupbuy_paid"] != "0.00" || row["performance"] != "200.00" {
		t.Fatal(row)
	}
	totals := report["totals"].(map[string]any)
	if totals["paid"] != "100.00" || totals["card_paid"] != "100.00" || totals["performance"] != "200.00" {
		t.Fatal(totals)
	}
	business := f.call("get_business_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02", "granularity": "day"}, 200)
	if business["rows"].([]any)[0].(map[string]any)["service_revenue"] != "100.00" {
		t.Fatal(business)
	}
}

func (f *fixture) fullItem(name string) uuid.UUID {
	f.t.Helper()
	return id(f.call("create_item", map[string]any{
		"name": name, "price": "100.00", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions": []any{
			map[string]any{"position_id": f.position, "required": false},
			map[string]any{"position_id": f.position, "required": false},
		},
		"level_prices": []any{},
	}, 200)["id"])
}

func fullStaffStaff(f *fixture) []uuid.UUID { return []uuid.UUID{f.staffA, f.staffB} }

func (f *fixture) fullCardOrder(customer uuid.UUID, item uuid.UUID) map[string]any {
	f.t.Helper()
	card := f.openCard(customer, f.stored, "200.00", "0", payment("cash", "200"))
	order := f.order(customer, fullStaffStaff(f), true, "female", item)
	f.checkout(order, f.cardPayment(id(card["id"]), "100"))
	return order
}

func (f *fixture) fullGroupbuyOrder(customer uuid.UUID, item uuid.UUID, key string) map[string]any {
	f.t.Helper()
	f.Groupbuy.Register("douyin", fakeprovider.New("douyin", f.Now()))
	deal := f.call("create_groupbuy_deal", map[string]any{"platform": "douyin", "name": "团购" + key, "purchase_price": "100.00", "item_id": item}, 200)
	f.call("redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}, 200)
	redemption := f.call("get_redemption", groupbuy.LookupInput{Key: key}, 200)
	staff := []orders.OrderItemStaffInput{{Slot: 1, StaffID: f.staffA}, {Slot: 2, StaffID: f.staffB}}
	out := f.call("open_groupbuy_order", groupbuy.OpenGroupbuyOrderInput{CustomerID: &customer, RedemptionID: id(redemption["id"]), DealID: id(deal["id"]), Gender: "female", Designated: true, Items: []orders.OrderItemInput{{ItemID: item, Staff: staff}}}, 200)
	order := out["order"].(map[string]any)
	f.checkout(order)
	return order
}

func itemReportRow(t *testing.T, f *fixture, item uuid.UUID) map[string]any {
	t.Helper()
	report := f.call("get_item_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	for _, raw := range report["rows"].([]any) {
		row := raw.(map[string]any)
		if row["item_id"] == item.String() {
			return row
		}
	}
	t.Fatalf("item %s missing from report %v", item, report)
	return nil
}

func TestItemReportFullSplitVoidedCardAndGroupbuy(t *testing.T) {
	f := open(t)
	customer := f.customer("全额撤单顾客", "13800000072", nil)
	byPosition := f.createItem("按岗位项目", "100.00", true)
	card := f.openCard(customer, f.stored, "200.00", "0", payment("cash", "200"))
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", byPosition), f.cardPayment(id(card["id"]), "100"))

	f.setting("performance_split", "full")
	full := f.fullItem("全额撤单项目")
	cardOrder := f.fullCardOrder(customer, full)
	f.call("void_order", map[string]any{"order_id": cardOrder["id"], "reason": "撤单"}, 200)
	groupbuyOrder := f.fullGroupbuyOrder(customer, full, "full-void")
	f.call("void_order", map[string]any{"order_id": groupbuyOrder["id"], "reason": "撤单"}, 200)

	fullRow := itemReportRow(t, f, full)
	if fullRow["card_paid"] != "0.00" || fullRow["groupbuy_paid"] != "0.00" || fullRow["paid"] != "0.00" || fullRow["performance"] != "0.00" || fullRow["times"].(float64) != 0 {
		t.Fatal(fullRow)
	}
	byPositionRow := itemReportRow(t, f, byPosition)
	if byPositionRow["card_paid"] != "100.00" || byPositionRow["performance"] != "100.00" {
		t.Fatal(byPositionRow)
	}
}

func (f *fixture) refundFullOrder(order map[string]any, amount string) {
	f.t.Helper()
	refund := f.call("create_aftersale", map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "退款", "refund_lines": []any{payment("cash", amount)}}, 200)
	f.call("approve_aftersale", map[string]any{"id": refund["id"], "deduct_performance": true}, 200)
}

func TestItemReportFullSplitAftersale(t *testing.T) {
	f := open(t)
	customer := f.customer("全额售后顾客", "13800000073", nil)
	f.setting("performance_split", "full")
	single := f.fullItem("全额单次售后项目")
	f.refundFullOrder(f.fullCardOrder(customer, single), "50.00")
	if row := itemReportRow(t, f, single); row["card_paid"] != "50.00" || row["performance"] != "100.00" {
		t.Fatal(row)
	}

	multiple := f.fullItem("全额多次售后项目")
	order := f.fullCardOrder(customer, multiple)
	f.refundFullOrder(order, "30.00")
	f.refundFullOrder(order, "20.00")
	if row := itemReportRow(t, f, multiple); row["card_paid"] != "50.00" || row["performance"] != "100.00" {
		t.Fatal(row)
	}
}
