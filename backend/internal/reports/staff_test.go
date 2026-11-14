package reports_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

func TestStaffReportScopeAndTotals(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000011", nil)
	f.now("2026-11-02", 12)
	f.setting("performance_ratio_cash", 50)
	f.call("save_commission_scheme", map[string]any{"effective_from": "2026-11-02", "scheme": map[string]any{"rules": []any{map[string]any{"source": "item", "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10.00"}}}}, "overrides": []any{}, "excluded_staff": []any{}}}, 200)
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	f.checkout(f.order(customer, []uuid.UUID{f.staffB}, false, "male", f.item), payment("cash", "100"))
	f.checkout(f.order(customer, nil, false, "female", f.loose), payment("cash", "40"))

	report := f.call("get_staff_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	rows := report["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("rows = %d: %#v", len(rows), rows)
	}
	if rows[2].(map[string]any)["staff"] != nil {
		t.Fatalf("unassigned row = %#v", rows[2])
	}
	assertStaffTotals(t, rows, report["totals"].(map[string]any))
	performance := f.call("list_performance", map[string]any{"date_from": "2026-11-02", "date_to": "2026-11-02"}, 200)
	itemTotal := money.SignedTotal{}
	for _, raw := range performance["totals"].([]any) {
		total := raw.(map[string]any)
		if total["kind"] == "item" {
			itemTotal = itemTotal.Add(changeOf(t, total["amount"].(string)))
		}
	}
	rowsTotal := money.SignedTotal{}
	for _, raw := range rows {
		rowsTotal = rowsTotal.Add(changeOf(t, raw.(map[string]any)["item_performance"].(string)))
	}
	if rowsTotal.Format() != itemTotal.Format() {
		t.Fatalf("staff item total %s, performance item total %s", rowsTotal.Format(), itemTotal.Format())
	}

	stylist, token := f.Hire("发型师丙", "004", "staff-c", f.Role("发型师"))
	f.call("update_staff", map[string]any{"staff_id": stylist, "name": "发型师丙", "number": "004", "level_id": nil, "position_id": nil}, 200)
	f.checkout(f.order(customer, []uuid.UUID{stylist}, true, "female", f.item), payment("cash", "100"))
	caller := f.MustCaller(token)
	for _, ch := range f.Channels() {
		self := ch.Call(t, caller, "get_staff_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"})
		if self.Status != 200 {
			t.Fatal(ch.Name, self)
		}
		own := self.Body["rows"].([]any)
		if len(own) != 1 || own[0].(map[string]any)["staff"].(map[string]any)["id"] != stylist.String() || own[0].(map[string]any)["commission_total"].(string) != "5.00" {
			t.Fatal(ch.Name, own)
		}
		other := ch.Call(t, caller, "get_staff_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02", "staff_id": f.staffA})
		if other.Status != 200 || len(other.Body["rows"].([]any)) != 0 {
			t.Fatal(ch.Name, other)
		}
	}
}

func TestStaffReportLinesNetVoids(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000012", nil)
	f.now("2026-11-02", 12)
	paid := f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item, f.goods), payment("cash", "150"))
	f.now("2026-11-03", 12)
	f.call("void_order", map[string]any{"order_id": paid["id"], "reason": "录入错误"}, 200)
	report := f.call("get_staff_report", map[string]any{"from": "2026-11-02", "to": "2026-11-03"}, 200)
	row := report["rows"].([]any)[0].(map[string]any)
	if row["service_item_lines"].(float64) != 0 || row["item_performance"].(string) != "0.00" || row["guest_total"].(string) != "0.0000" {
		t.Fatal(row)
	}
	item := f.call("get_item_report", map[string]any{"from": "2026-11-02", "to": "2026-11-03"}, 200)
	for _, raw := range item["rows"].([]any) {
		if raw.(map[string]any)["performance"].(string) != "0.00" {
			t.Fatal(raw)
		}
	}
}

func assertStaffTotals(t *testing.T, rows []any, totals map[string]any) {
	t.Helper()
	for _, field := range []string{"item_performance", "consume_performance", "labor_performance", "sale_performance", "cash_performance", "card_performance", "commission_total"} {
		sum := money.SignedTotal{}
		for _, raw := range rows {
			sum = sum.Add(changeOf(t, raw.(map[string]any)[field].(string)))
		}
		if sum.Format() != totals[field] {
			t.Fatalf("%s total = %v, want %v", field, totals[field], sum.Format())
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
	var lines float64
	for _, raw := range rows {
		lines += raw.(map[string]any)["service_item_lines"].(float64)
	}
	if totals["service_item_lines"].(float64) != lines {
		t.Fatalf("lines total = %v, want %v", totals["service_item_lines"], lines)
	}
}
