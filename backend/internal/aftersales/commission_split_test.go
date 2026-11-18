package aftersales_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func (f *fixture) commissionTotalBoth(from, to string) map[string]any {
	f.t.Helper()
	caller := f.MustCaller(f.Owner)
	var first map[string]any
	for _, ch := range f.Channels() {
		r := ch.Call(f.t, caller, "list_commissions", map[string]any{"date_from": from, "date_to": to, "limit": 200})
		if r.Status != 200 {
			f.t.Fatal(ch.Name, r)
		}
		if first == nil {
			first = r.Body
		} else if !reflect.DeepEqual(first, r.Body) {
			f.t.Fatal("HTTP and AI bodies differ")
		}
	}
	return first
}

func (f *fixture) commissionSplitOrder() map[string]any {
	f.t.Helper()
	second, _ := f.Hire("员工二", "003", "staff2")
	item := f.call("create_item", map[string]any{
		"name": "分摊提成项目", "price": "100.00", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions":    []any{map[string]any{"position_id": f.position, "required": false, "share_percent": 70}, map[string]any{"position_id": f.position, "required": false, "share_percent": 30}},
		"level_prices": []any{},
	}, 200)
	return f.call("open_order", map[string]any{
		"customer_id": f.customer, "gender": "female", "designated": true,
		"items": []any{map[string]any{"item_id": item["id"], "staff": []any{
			map[string]any{"slot": 1, "staff_id": f.employee}, map[string]any{"slot": 2, "staff_id": second},
		}}},
	}, 200)
}

func (f *fixture) saveCommissionSplitScheme() {
	f.t.Helper()
	f.call("save_commission_scheme", map[string]any{"effective_from": f.today(), "scheme": map[string]any{
		"rules":     []any{map[string]any{"source": "item", "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10.00"}}}},
		"overrides": []any{}, "excluded_staff": []any{},
	}}, 200)
}

func TestAftersaleCommissionSplitReversal(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.setting("performance_split", "full")
	f.setting("commission_split", "equal")
	f.saveCommissionSplitScheme()
	order := f.commissionSplitOrder()
	f.checkout(order, payment("cash", "100"))
	before := f.commissionTotalBoth("2026-10-01", "2026-10-02")
	if before["total"] != "10.00" {
		t.Fatal(before)
	}
	f.now("2026-10-02", 12)
	refund := f.create(id(order["id"]), "partial_refund", "按提成份额退款", payment("cash", "40"))
	f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})
	after := f.commissionTotalBoth("2026-10-01", "2026-10-02")
	if after["total"] != "6.00" {
		t.Fatal(after)
	}
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	rows, err := tx.Query(context.Background(), `SELECT r.slot,r.paid_amount::text,r.amount::text,r.commission_paid_amount::text,r.commission_amount::text
 FROM performance_entries r WHERE r.store_id=$1 AND r.order_id=$2 AND r.kind='item' AND r.aftersale_id IS NOT NULL ORDER BY r.slot`, f.Res.StoreID, order["id"])
	if err != nil {
		t.Fatal(err)
	}
	defer dbtest.Rollback(t, tx)
	n := 0
	for rows.Next() {
		var slot int16
		var paid, amount, commissionPaid, commissionAmount string
		if err := rows.Scan(&slot, &paid, &amount, &commissionPaid, &commissionAmount); err != nil {
			t.Fatal(err)
		}
		if paid != "40.00" || amount != "40.00" || commissionPaid != "20.00" || commissionAmount != "20.00" {
			t.Fatal(slot, paid, amount, commissionPaid, commissionAmount)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatal(n)
	}
}

type reversalColumns struct {
	paid, amount, commissionPaid, commissionAmount string
}

func (f *fixture) pricedItem(name, price string, shares ...int) uuid.UUID {
	f.t.Helper()
	positions := []any{}
	for _, share := range shares {
		positions = append(positions, map[string]any{"position_id": f.position, "required": false, "share_percent": share})
	}
	item := f.call("create_item", map[string]any{
		"name": name, "price": price, "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions": positions, "level_prices": []any{},
	}, 200)
	return id(item["id"])
}

func (f *fixture) originalReversalColumns(orderID uuid.UUID) map[string]reversalColumns {
	f.t.Helper()
	tx := dbtest.AppTx(f.t, f.Pool, f.Res.TenantID)
	rows, err := tx.Query(context.Background(), `SELECT i.item_name,r.slot,r.paid_amount::text,r.amount::text,r.commission_paid_amount::text,r.commission_amount::text
 FROM performance_entries r JOIN order_items i ON i.id=r.order_item_id
 WHERE r.store_id=$1 AND r.order_id=$2 AND r.kind='item' AND r.reverses_id IS NULL`, f.Res.StoreID, orderID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer dbtest.Rollback(f.t, tx)
	out := map[string]reversalColumns{}
	for rows.Next() {
		var name string
		var slot int16
		var c reversalColumns
		if err := rows.Scan(&name, &slot, &c.paid, &c.amount, &c.commissionPaid, &c.commissionAmount); err != nil {
			f.t.Fatal(err)
		}
		out[name+"/"+string(rune('0'+slot))] = c
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) reversalColumnsByLine(orderID uuid.UUID) map[string]reversalColumns {
	f.t.Helper()
	tx := dbtest.AppTx(f.t, f.Pool, f.Res.TenantID)
	rows, err := tx.Query(context.Background(), `SELECT i.item_name,r.slot,COALESCE(sum(r.paid_amount),0)::text,COALESCE(sum(r.amount),0)::text,COALESCE(sum(r.commission_paid_amount),0)::text,COALESCE(sum(r.commission_amount),0)::text
 FROM performance_entries r JOIN order_items i ON i.id=r.order_item_id
 WHERE r.store_id=$1 AND r.order_id=$2 AND r.kind='item' AND r.aftersale_id IS NOT NULL
 GROUP BY i.item_name,r.slot`, f.Res.StoreID, orderID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer dbtest.Rollback(f.t, tx)
	out := map[string]reversalColumns{}
	for rows.Next() {
		var name string
		var slot int16
		var c reversalColumns
		if err := rows.Scan(&name, &slot, &c.paid, &c.amount, &c.commissionPaid, &c.commissionAmount); err != nil {
			f.t.Fatal(err)
		}
		out[name+"/"+string(rune('0'+slot))] = c
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) aftersaleReversalRows(aftersaleID uuid.UUID) map[string]reversalColumns {
	f.t.Helper()
	tx := dbtest.AppTx(f.t, f.Pool, f.Res.TenantID)
	rows, err := tx.Query(context.Background(), `SELECT i.item_name,r.slot,r.paid_amount::text,r.amount::text,r.commission_paid_amount::text,r.commission_amount::text
 FROM performance_entries r JOIN order_items i ON i.id=r.order_item_id
 WHERE r.store_id=$1 AND r.aftersale_id=$2 AND r.kind='item'`, f.Res.StoreID, aftersaleID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer dbtest.Rollback(f.t, tx)
	out := map[string]reversalColumns{}
	for rows.Next() {
		var name string
		var slot int16
		var c reversalColumns
		if err := rows.Scan(&name, &slot, &c.paid, &c.amount, &c.commissionPaid, &c.commissionAmount); err != nil {
			f.t.Fatal(err)
		}
		out[name+"/"+string(rune('0'+slot))] = c
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestAftersaleCommissionSplitPaidRelaxation(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	second, _ := f.Hire("员工二", "003", "staff2")
	f.setting("performance_split", "full")
	f.setting("commission_split", "by_position")
	itemA := f.pricedItem("分摊项目", "51.19", 70, 30)
	itemB := f.pricedItem("补差项目", "0.02", 100)
	order := f.call("open_order", map[string]any{
		"customer_id": f.customer, "gender": "female", "designated": true,
		"items": []any{
			map[string]any{"item_id": itemA, "staff": []any{map[string]any{"slot": 1, "staff_id": f.employee}, map[string]any{"slot": 2, "staff_id": second}}},
			map[string]any{"item_id": itemB, "staff": []any{map[string]any{"slot": 1, "staff_id": f.employee}}},
		},
	}, 200)
	f.checkout(order, payment("cash", "51.21"))
	original := f.originalReversalColumns(id(order["id"]))

	first := f.create(id(order["id"]), "partial_refund", "第一次退款", payment("cash", "25.59"))
	f.approve(id(first["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})
	firstSums := f.reversalColumnsByLine(id(order["id"]))
	wantFirst := map[string]reversalColumns{
		"分摊项目/1": {"25.58", "25.58", "17.90", "17.90"},
		"分摊项目/2": {"25.58", "25.58", "7.67", "7.67"},
	}
	if !reflect.DeepEqual(firstSums, wantFirst) {
		t.Fatal(firstSums)
	}

	secondRefund := f.create(id(order["id"]), "partial_refund", "第二次退款", payment("cash", "0.02"))
	f.approve(id(secondRefund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})
	secondSums := f.reversalColumnsByLine(id(order["id"]))
	wantSecond := map[string]reversalColumns{
		"分摊项目/1": {"25.59", "25.59", "17.92", "17.92"},
		"分摊项目/2": {"25.59", "25.59", "7.67", "7.67"},
		"补差项目/1": {"0.01", "0.01", "0.01", "0.01"},
	}
	if !reflect.DeepEqual(secondSums, wantSecond) {
		t.Fatal(secondSums)
	}
	rows := f.aftersaleReversalRows(id(secondRefund["id"]))
	if got := rows["分摊项目/1"]; got != (reversalColumns{"0.01", "0.01", "0.02", "0.02"}) {
		t.Fatal(got)
	}

	final := f.create(id(order["id"]), "full_refund", "退完", payment("cash", "25.60"))
	f.approve(id(final["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})
	if got := f.reversalColumnsByLine(id(order["id"])); !reflect.DeepEqual(got, original) {
		t.Fatal(got, original)
	}
}
