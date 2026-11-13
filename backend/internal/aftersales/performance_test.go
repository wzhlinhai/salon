package aftersales_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

func (f *fixture) performanceTotals(from, to string) map[string]string {
	f.t.Helper()
	body := f.call("list_performance", map[string]any{"date_from": from, "date_to": to, "limit": 200}, 200)
	out := map[string]string{}
	for _, raw := range body["totals"].([]any) {
		row := raw.(map[string]any)
		out[row["kind"].(string)] = row["amount"].(string)
	}
	return out
}

func (f *fixture) performanceEntries(from, to string) []any {
	f.t.Helper()
	return f.call("list_performance", map[string]any{"date_from": from, "date_to": to, "limit": 200}, 200)["entries"].([]any)
}

func (f *fixture) commissionScheme() {
	f.t.Helper()
	rules := []any{}
	for _, source := range []string{"item", "consume", "labor"} {
		base := "performance"
		if source == "item" {
			base = "paid"
		}
		rules = append(rules, map[string]any{"source": source, "base": base, "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10.00"}}})
	}
	f.call("save_commission_scheme", map[string]any{"effective_from": f.today(), "scheme": map[string]any{"rules": rules, "overrides": []any{}, "excluded_staff": []any{}}}, 200)
}

func (f *fixture) commissionTotal(from, to string) map[string]any {
	f.t.Helper()
	return f.call("list_commissions", map[string]any{"date_from": from, "date_to": to, "limit": 200}, 200)
}

func (f *fixture) countCard(lineItemID uuid.UUID, paid int) map[string]any {
	f.t.Helper()
	ct := f.call("create_card_type", map[string]any{
		"name": "次卡", "kind": "count", "validity_kind": "none",
		"items": []any{map[string]any{"item_id": lineItemID, "paid_count": paid, "gift_count": 0, "unit_price": "100.00"}},
	}, 200)
	return f.call("open_card", map[string]any{"customer_id": f.customer, "card_type_id": ct["id"], "principal": "0.00", "bonus": "0.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "100.00")}}, 200)
}

func TestAftersalePerformanceDeduction(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	before := f.performanceTotals("2026-10-01", "2026-10-02")
	if before["item"] != "100.00" || before["labor"] != "10.00" {
		t.Fatal(before)
	}
	f.now("2026-10-02", 12)
	refund := f.create(id(order["id"]), "full_refund", "退款", payment("cash", "100"))
	out := f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": false})
	if out["deduct_performance"] != true || out["deduct_commission"] != false {
		t.Fatal(out)
	}
	after := f.performanceTotals("2026-10-01", "2026-10-02")
	if after["item"] != "0.00" || after["labor"] != "10.00" {
		t.Fatal(after)
	}
	entries := f.performanceEntries("2026-10-02", "2026-10-02")
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	reversal := entries[0].(map[string]any)
	if reversal["reversal"] != true || reversal["kind"] != "item" || reversal["business_date"] != "2026-10-02" || reversal["paid_amount"] != "100.00" || reversal["amount"] != "100.00" || reversal["reverses_id"] == nil || reversal["aftersale_id"] != refund["id"] {
		t.Fatal(reversal)
	}
}

func TestAftersaleCommissionDeduction(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.commissionScheme()
	first := f.order(1)
	f.checkout(first, payment("cash", "100"))
	got := f.commissionTotal("2026-10-01", "2026-10-02")
	if got["total"] != "11.00" {
		t.Fatal(got)
	}
	f.now("2026-10-02", 12)

	// 扣业绩但不扣提成：业绩冲回，提成保持不变。
	refund := f.create(id(first["id"]), "full_refund", "不扣提成", payment("cash", "100"))
	f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": false})
	if got := f.commissionTotal("2026-10-01", "2026-10-02"); got["total"] != "11.00" {
		t.Fatal(got)
	}
	if f.performanceTotals("2026-10-01", "2026-10-02")["item"] != "0.00" {
		t.Fatal("performance not deducted")
	}

	// 扣提成时按撤单冲回路径计入负提成， labor 不受影响。
	second := f.paidOrder(payment("cash", "100"))
	refund = f.create(id(second["id"]), "full_refund", "扣提成", payment("cash", "100"))
	out := f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})
	if out["deduct_commission"] != true {
		t.Fatal(out)
	}
	got = f.commissionTotal("2026-10-01", "2026-10-02")
	if got["total"] != "12.00" {
		t.Fatal(got)
	}
	var reversal bool
	for _, raw := range got["entries"].([]any) {
		row := raw.(map[string]any)
		if row["reversal"] == true {
			reversal = true
			origin := row["explanation"].(map[string]any)["origin"].(map[string]any)
			if row["amount"] != "-10.00" || origin["aftersale_id"] != refund["id"] {
				t.Fatal(row)
			}
		}
	}
	if !reversal {
		t.Fatal("missing commission reversal")
	}

	// 部分退款按比例冲回提成。
	third := f.paidOrder(payment("cash", "100"))
	refund = f.create(id(third["id"]), "partial_refund", "部分退款", payment("cash", "40"))
	f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})
	got = f.commissionTotal("2026-10-01", "2026-10-02")
	if got["total"] != "19.00" {
		t.Fatal(got)
	}
}

func TestAftersaleConsumeAndLaborNotDeducted(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1, 1)
	items := order["items"].([]any)
	secondLine := id(items[1].(map[string]any)["id"])
	card := f.countCard(f.item, 1)
	f.checkout(order, payment("cash", "100"), cardPayment("count", "100", id(card["id"]), &secondLine))
	before := f.performanceTotals("2026-10-01", "2026-10-02")
	if before["item"] != "100.00" || before["consume"] != "100.00" || before["labor"] != "20.00" {
		t.Fatal(before)
	}
	f.now("2026-10-02", 12)
	refund := f.create(id(order["id"]), "full_refund", "退现金", payment("cash", "100"))
	f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": false})
	after := f.performanceTotals("2026-10-01", "2026-10-02")
	if after["item"] != "0.00" || after["consume"] != "100.00" || after["labor"] != "20.00" {
		t.Fatal(after)
	}
	reversals := []map[string]any{}
	for _, raw := range f.performanceEntries("2026-10-02", "2026-10-02") {
		row := raw.(map[string]any)
		if row["reversal"] == true {
			reversals = append(reversals, row)
		}
	}
	if len(reversals) != 1 || reversals[0]["kind"] != "item" {
		t.Fatal(reversals)
	}
}

func TestAftersaleDeductDefaultsAndOverrides(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.commissionScheme()
	f.setting("aftersale_deduct_performance", "on")
	f.setting("aftersale_deduct_commission", "on")

	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	refund := f.create(id(order["id"]), "partial_refund", "默认设置", payment("cash", "40"))
	out := f.approve(id(refund["id"]), nil)
	if out["deduct_performance"] != true || out["deduct_commission"] != true {
		t.Fatal(out)
	}
	if f.performanceTotals("2026-10-01", "2026-10-02")["item"] != "60.00" {
		t.Fatal("default performance deduction missing")
	}

	second := f.paidOrder(payment("cash", "100"))
	refund = f.create(id(second["id"]), "partial_refund", "覆盖设置", payment("cash", "40"))
	f.refused(f.MustCaller(f.Owner), "approve_aftersale", map[string]any{"id": refund["id"], "deduct_performance": false, "deduct_commission": true}, 409, "扣提成时必须同时扣业绩")
	out = f.approve(id(refund["id"]), map[string]any{"deduct_performance": false, "deduct_commission": false})
	if out["deduct_performance"] != false || out["deduct_commission"] != false {
		t.Fatal(out)
	}
	before := f.performanceTotals("2026-10-01", "2026-10-02")
	if before["item"] != "160.00" {
		t.Fatal(before)
	}

	repair := f.create(id(second["id"]), "repair", "修补")
	out = f.approve(id(repair["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})
	if out["deduct_performance"] != false || out["deduct_commission"] != false {
		t.Fatal(out)
	}
	after := f.performanceTotals("2026-10-01", "2026-10-02")
	if before["item"] != after["item"] || before["labor"] != after["labor"] {
		t.Fatal(before, after)
	}
}

func (f *fixture) fixedCommissionScheme() {
	f.t.Helper()
	rules := []any{map[string]any{"source": "item", "base": "paid", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"fixed_amount": "10.00"}}}}
	f.call("save_commission_scheme", map[string]any{"effective_from": f.today(), "scheme": map[string]any{"rules": rules, "overrides": []any{}, "excluded_staff": []any{}}}, 200)
}

func TestAftersaleFixedCommissionReversal(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.fixedCommissionScheme()
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	for _, amount := range []string{"33", "33"} {
		refund := f.create(id(order["id"]), "partial_refund", "固定提成", payment("cash", amount))
		f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})
	}
	got := f.commissionTotal("2026-10-01", "2026-10-02")
	if got["total"] != "3.40" {
		t.Fatal(got)
	}
	var reversed money.SignedTotal
	for _, raw := range got["entries"].([]any) {
		row := raw.(map[string]any)
		if row["reversal"] == true {
			var amount money.Change
			if err := amount.Scan(row["amount"]); err != nil {
				t.Fatal(err)
			}
			reversed = reversed.Add(amount)
		}
	}
	if reversed.Format() != "-6.60" {
		t.Fatal(reversed.Format())
	}
}

func (f *fixture) itemReversalSums(orderID uuid.UUID) map[string][2]string {
	f.t.Helper()
	tx := dbtest.AppTx(f.t, f.Pool, f.Res.TenantID)
	rows, err := tx.Query(context.Background(), `SELECT e.paid_amount::text,e.amount::text,COALESCE(sum(r.paid_amount),0)::text,COALESCE(sum(r.amount),0)::text FROM performance_entries e LEFT JOIN performance_entries r ON r.reverses_id=e.id AND r.aftersale_id IS NOT NULL WHERE e.store_id=$1 AND e.order_id=$2 AND e.kind='item' AND e.reverses_id IS NULL GROUP BY e.id ORDER BY e.paid_amount DESC`, f.Res.StoreID, orderID)
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string][2]string{}
	for rows.Next() {
		var originalPaid, originalAmount, reversedPaid, reversedAmount string
		if err := rows.Scan(&originalPaid, &originalAmount, &reversedPaid, &reversedAmount); err != nil {
			f.t.Fatal(err)
		}
		out[originalPaid] = [2]string{reversedPaid, reversedAmount}
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	dbtest.Rollback(f.t, tx)
	return out
}

func TestAftersalePerformanceCumulativeReversal(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.setting("performance_ratio_cash", 50)
	order := f.order(1, 1, 1)
	for i, price := range []string{"34.00", "33.00", "33.00"} {
		line := order["items"].([]any)[i].(map[string]any)
		f.call("reprice_order_item", map[string]any{"order_id": order["id"], "order_item_id": line["id"], "price": price, "reason": "测试分摊"}, 200)
	}
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	for i, amount := range []string{"33", "33", "34"} {
		typ := "partial_refund"
		if i == 2 {
			typ = "full_refund"
		}
		refund := f.create(id(order["id"]), typ, "累计冲回", payment("cash", amount))
		f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": false})
		sums := f.itemReversalSums(id(order["id"]))
		for _, originalPaid := range []string{"34.00", "33.00"} {
			orig, err := parseMoney(originalPaid)
			if err != nil {
				t.Fatal(err)
			}
			revPaid, err := parseMoney(sums[originalPaid][0])
			if err != nil {
				t.Fatal(err)
			}
			if revPaid.Cmp(orig) > 0 {
				t.Fatalf("paid over-reversed after refund %d: %s > %s", i, revPaid.String(), orig.String())
			}
		}
	}
	sums := f.itemReversalSums(id(order["id"]))
	want := map[string][2]string{"34.00": {"34.00", "17.00"}, "33.00": {"33.00", "16.50"}}
	for originalPaid, expected := range want {
		if sums[originalPaid] != expected {
			t.Fatalf("%s reversed %v, want %v", originalPaid, sums[originalPaid], expected)
		}
	}
}

func parseMoney(s string) (money.Amount, error) {
	var a money.Amount
	err := a.Scan(s)
	return a, err
}
