package reports_test

import (
	"testing"

	"github.com/google/uuid"
)

func TestItemAndCardCountVoidNetting(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000071", nil)
	f.now("2026-11-02", 12)
	count := f.openCard(customer, f.count, "0", "0", payment("cash", "300"))
	order := f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item)
	line := id(f.line(order, 0)["id"])
	paid := f.checkout(order, f.countPayment(id(count["id"]), line, "100"))
	f.now("2026-11-03", 12)
	f.call("void_order", map[string]any{"order_id": paid["id"], "reason": "录入错误"}, 200)

	item := f.call("get_item_report", map[string]any{"from": "2026-11-02", "to": "2026-11-03"}, 200)
	row := item["rows"].([]any)[0].(map[string]any)
	if row["times"].(float64) != 0 || row["paid"].(string) != "0.00" || row["count_paid"].(string) != "0.00" {
		t.Fatal(row)
	}
	card := f.call("get_card_report", map[string]any{"from": "2026-11-02", "to": "2026-11-03"}, 200)
	cardRow := card["rows"].([]any)[0].(map[string]any)
	if cardRow["count_used"].(float64) != 0 || cardRow["count_amount"].(string) != "0.00" {
		t.Fatal(cardRow)
	}
}

func TestCardReportNetsVoidAndAftersale(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000072", nil)
	f.now("2026-11-02", 12)
	principalCard := f.openCard(customer, f.stored, "100.00", "0", payment("cash", "100"))
	paid := f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), f.cardPayment(id(principalCard["id"]), "100"))
	aftersale := f.call("create_aftersale", map[string]any{"order_id": paid["id"], "type": "partial_refund", "reason": "退回卡本金", "refund_lines": []any{map[string]any{"method": "card_principal", "amount": "100.00", "card_id": principalCard["id"]}}}, 200)
	f.call("approve_aftersale", map[string]any{"id": aftersale["id"]}, 200)
	bonusCard := f.openCard(customer, f.stored, "0", "100.00")
	toVoid := f.checkout(f.order(customer, []uuid.UUID{f.staffB}, false, "male", f.item), f.cardPayment(id(bonusCard["id"]), "100"))
	f.now("2026-11-03", 12)
	f.call("void_order", map[string]any{"order_id": toVoid["id"], "reason": "录入错误"}, 200)

	report := f.call("get_card_report", map[string]any{"from": "2026-11-02", "to": "2026-11-03"}, 200)
	row := report["rows"].([]any)[0].(map[string]any)
	if row["consumed_principal"].(string) != "0.00" || row["consumed_bonus"].(string) != "0.00" || row["ending_balance"].(string) != "200.00" {
		t.Fatal(row)
	}
}

func TestCustomerSourceRevenueAndUnidentifiedNetVoids(t *testing.T) {
	f := open(t)
	source := id(f.call("create_customer_source", map[string]any{"name": "退款渠道"}, 200)["id"])
	customer := f.customer("新客", "13800000073", &source)
	unknown := f.customer("未识别", "", nil)
	f.now("2026-11-01", 12)
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	f.now("2026-11-02", 12)
	second := f.checkout(f.order(customer, []uuid.UUID{f.staffA}, false, "male", f.item), payment("cash", "100"))
	unknownOrder := f.checkout(f.order(unknown, []uuid.UUID{f.staffB}, false, "female", f.item), payment("cash", "100"))
	f.now("2026-11-03", 12)
	f.call("void_order", map[string]any{"order_id": second["id"], "reason": "录入错误"}, 200)
	f.call("void_order", map[string]any{"order_id": unknownOrder["id"], "reason": "录入错误"}, 200)

	report := f.call("get_customer_source_report", map[string]any{"from": "2026-11-01", "to": "2026-11-03"}, 200)
	row := sourceRows(report["rows"].([]any))["退款渠道"]
	if row == nil || row["new_customers"].(float64) != 1 || row["returning"].(float64) != 1 || row["revenue"].(string) != "100.00" {
		t.Fatal(row)
	}
	if report["unidentified_orders"].(float64) != 0 {
		t.Fatal(report["unidentified_orders"])
	}
}
