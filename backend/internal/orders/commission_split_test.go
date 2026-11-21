package orders_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) clonePerformance(t *testing.T, source any, overrides map[string]any) error {
	t.Helper()
	overrides["id"] = uuid.NewString()
	data, err := json.Marshal(overrides)
	if err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(context.Background(), "INSERT INTO performance_entries SELECT (jsonb_populate_record(NULL::performance_entries,to_jsonb(l)||$2::jsonb)).* FROM performance_entries l WHERE id=$1", source, string(data))
	dbtest.Rollback(t, tx)
	return err
}

func itemRule(base, percent string) map[string]any {
	return map[string]any{"source": "item", "base": base, "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": percent}}}
}

func (s *fixture) saveCommission(rules []any, overrides []any) map[string]any {
	s.t.Helper()
	if overrides == nil {
		overrides = []any{}
	}
	return s.call("save_commission_scheme", map[string]any{"effective_from": settings.Date(time.Now()).Format(settings.DateLayout), "scheme": map[string]any{"rules": rules, "overrides": overrides, "excluded_staff": []any{}}}, 200)
}

func (s *fixture) splitLine(name, price, category string, shares []any, staff []uuid.UUID) orders.OrderItemInput {
	s.t.Helper()
	in := s.itemInput(name, 0)
	in["price"] = price
	in["level_prices"] = []any{}
	if category != "" {
		in["category_id"] = category
	}
	positions := []any{}
	for _, share := range shares {
		positions = append(positions, map[string]any{"position_id": s.position, "required": false, "share_percent": share})
	}
	in["positions"] = positions
	item := s.call("create_item", in, 200)
	assignments := []orders.OrderItemStaffInput{}
	for i, id := range staff {
		assignments = append(assignments, orders.OrderItemStaffInput{Slot: int16(i + 1), StaffID: id})
	}
	return orders.OrderItemInput{ItemID: id(item["id"]), Staff: assignments}
}

func commissionBySlot(t *testing.T, got map[string]any) map[int16]map[string]any {
	t.Helper()
	out := map[int16]map[string]any{}
	for _, raw := range got["entries"].([]any) {
		entry := raw.(map[string]any)
		slot := int16(entry["explanation"].(map[string]any)["origin"].(map[string]any)["slot"].(float64))
		out[slot] = entry
	}
	return out
}

func (s *fixture) readAll(t *testing.T, op string, in any) []map[string]any {
	t.Helper()
	caller := s.MustCaller(s.Owner)
	out := make([]map[string]any, 0, 2)
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, op, in)
		if r.Status != 200 {
			t.Fatalf("%s %s: %d %v %v", ch.Name, op, r.Status, r.Body, r.Err)
		}
		out = append(out, r.Body)
	}
	return out
}

func (s *fixture) readBody(t *testing.T, op string, in any) map[string]any {
	t.Helper()
	bodies := s.readAll(t, op, in)
	for _, body := range bodies[1:] {
		if !reflect.DeepEqual(bodies[0], body) {
			t.Fatalf("%s: HTTP and AI bodies differ", op)
		}
	}
	return bodies[0]
}

func (s *fixture) performanceEntriesRead(t *testing.T, kind string) []map[string]any {
	t.Helper()
	day := settings.Date(time.Now()).Format(settings.DateLayout)
	body := s.readBody(t, "list_performance", map[string]any{"date_from": day, "date_to": day, "limit": 200})
	out := []map[string]any{}
	for _, raw := range body["entries"].([]any) {
		entry := raw.(map[string]any)
		if entry["kind"] == kind {
			out = append(out, entry)
		}
	}
	return out
}

func TestCommissionSplitFullEqual(t *testing.T) {
	t.Run("two staff", func(t *testing.T) {
		s := open(t)
		s.performanceSetting("performance_split", "full")
		s.performanceSetting("commission_split", "equal")
		s.saveCommission([]any{itemRule("performance", "50")}, nil)
		line := s.splitLine("两人提成", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
		s.paid(s.customerOrder(line), payment("cash", "100"))
		perf := s.performanceEntriesRead(t, "item")
		if len(perf) != 2 || perf[0]["paid_amount"] != "100.00" || perf[1]["paid_amount"] != "100.00" || perf[0]["amount"] != "100.00" || perf[1]["amount"] != "100.00" {
			t.Fatal(perf)
		}
		got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
		if got["total"] != "50.00" || len(got["entries"].([]any)) != 2 {
			t.Fatal(got)
		}
		for _, raw := range got["entries"].([]any) {
			entry := raw.(map[string]any)
			explanation := entry["explanation"].(map[string]any)
			base := explanation["base"].(map[string]any)
			performance := explanation["entries"].([]any)[0].(map[string]any)
			if entry["amount"] != "25.00" || base["amount"] != "50.00" || performance["commission_paid_amount"] != "50.00" || performance["commission_amount"] != "50.00" {
				t.Fatal(entry)
			}
		}
	})

	t.Run("three staff remainder", func(t *testing.T) {
		s := open(t)
		third, _ := s.Hire("第三员工", "003", "third")
		s.performanceSetting("performance_split", "full")
		s.performanceSetting("commission_split", "equal")
		s.saveCommission([]any{itemRule("performance", "100")}, nil)
		line := s.splitLine("三人提成", "100.00", "", []any{nil, nil, nil}, []uuid.UUID{s.Res.OwnerID, s.employee, third})
		s.paid(s.customerOrder(line), payment("cash", "100"))
		got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
		if got["total"] != "100.00" || len(got["entries"].([]any)) != 3 {
			t.Fatal(got)
		}
		want := map[int16]string{1: "33.34", 2: "33.33", 3: "33.33"}
		bySlot := commissionBySlot(t, got)
		for slot, amount := range want {
			entry := bySlot[slot]
			if entry == nil || entry["amount"] != amount || entry["explanation"].(map[string]any)["entries"].([]any)[0].(map[string]any)["commission_paid_amount"] != amount {
				t.Fatalf("slot %d: %v", slot, entry)
			}
		}
	})
}

func TestCommissionSplitByPosition(t *testing.T) {
	t.Run("shares", func(t *testing.T) {
		s := open(t)
		s.performanceSetting("performance_split", "full")
		s.performanceSetting("commission_split", "by_position")
		s.saveCommission([]any{itemRule("performance", "100")}, nil)
		line := s.splitLine("岗位提成", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
		s.paid(s.customerOrder(line), payment("cash", "100"))
		bySlot := commissionBySlot(t, s.readBody(t, "list_commissions", commissionArgs(s.Now())))
		if bySlot[1]["amount"] != "70.00" || bySlot[2]["amount"] != "30.00" {
			t.Fatal(bySlot)
		}
	})

	t.Run("missing share falls back to equal", func(t *testing.T) {
		s := open(t)
		s.performanceSetting("performance_split", "full")
		s.performanceSetting("commission_split", "by_position")
		s.saveCommission([]any{itemRule("performance", "100")}, nil)
		line := s.splitLine("缺岗位比例", "100.00", "", []any{nil, nil}, []uuid.UUID{s.Res.OwnerID, s.employee})
		s.paid(s.customerOrder(line), payment("cash", "100"))
		bySlot := commissionBySlot(t, s.readBody(t, "list_commissions", commissionArgs(s.Now())))
		if bySlot[1]["amount"] != "50.00" || bySlot[2]["amount"] != "50.00" {
			t.Fatal(bySlot)
		}
	})
}

func TestCommissionSplitSameUnchanged(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "same")
	s.saveCommission([]any{itemRule("performance", "100")}, nil)
	line := s.splitLine("跟业绩一样", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	s.paid(s.customerOrder(line), payment("cash", "100"))
	bySlot := commissionBySlot(t, s.readBody(t, "list_commissions", commissionArgs(s.Now())))
	if bySlot[1]["amount"] != "100.00" || bySlot[2]["amount"] != "100.00" {
		t.Fatal(bySlot)
	}
}

func TestCommissionSplitCategoryRatioSingleRounding(t *testing.T) {
	s := open(t)
	category := s.call("create_item_category", map[string]any{"name": "提成类别", "performance_ratio": 90}, 200)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "equal")
	s.performanceSetting("performance_ratio_card_bonus", 50)
	s.saveCommission([]any{itemRule("performance", "100")}, nil)
	line := s.splitLine("提成舍入", "0.06", category["id"].(string), []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	card := s.paymentCard(s.paymentType("提成赠送卡", nil), "0", "0.06")
	s.paid(s.customerOrder(line), cardPayment(card, "0.06"))
	perf := s.performanceEntriesRead(t, "item")
	if len(perf) != 2 || perf[0]["amount"] != "0.03" || perf[1]["amount"] != "0.03" {
		t.Fatal(perf)
	}
	got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
	if got["total"] != "0.02" || len(got["entries"].([]any)) != 2 {
		t.Fatal(got)
	}
	for _, raw := range got["entries"].([]any) {
		entry := raw.(map[string]any)
		performance := entry["explanation"].(map[string]any)["entries"].([]any)[0].(map[string]any)
		if entry["amount"] != "0.01" || entry["explanation"].(map[string]any)["base"].(map[string]any)["amount"] != "0.01" || performance["commission_paid_amount"] != "0.03" || performance["commission_amount"] != "0.01" {
			t.Fatal(entry)
		}
	}
}

func TestCommissionSplitLinePrice(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "equal")
	s.saveCommission([]any{itemRule("line_price", "10")}, nil)
	line := s.splitLine("原价基数", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	s.paid(s.customerOrder(line), payment("cash", "100"))
	got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
	if got["total"] != "10.00" || len(got["entries"].([]any)) != 2 {
		t.Fatal(got)
	}
	for _, raw := range got["entries"].([]any) {
		base := raw.(map[string]any)["explanation"].(map[string]any)["base"].(map[string]any)
		if base["amount"] != "50.00" || base["line_price"] != "100.00" || base["unit_paid"] != "50.00" || base["line_paid"] != "100.00" {
			t.Fatal(base)
		}
	}
}

func TestCommissionLinePaidUsesCommissionShares(t *testing.T) {
	s := open(t)
	line := s.splitLine("原价分母", "300.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	o := s.paid(s.customerOrder(line), payment("cash", "300"))
	lineID := id(lines(o)[0].(map[string]any)["id"])
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "RESET ROLE")
	dbtest.Exec(t, tx, `UPDATE performance_entries SET paid_amount=100,commission_paid_amount=70,amount=100,commission_amount=70 WHERE order_id=$1 AND kind='item'`, o["id"])
	dbtest.Exec(t, tx, "SET LOCAL ROLE salon_app")
	facts, err := dbgen.New(tx).PerformanceLineFacts(context.Background(), dbgen.PerformanceLineFactsParams{StoreID: s.Res.StoreID, Ids: []uuid.UUID{lineID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].LinePaid != "140.00" {
		t.Fatal(facts)
	}
	dbtest.Rollback(t, tx)
}

func TestCommissionSplitBases(t *testing.T) {
	for _, tc := range []struct {
		name, base, method string
	}{{"paid", "paid", "cash"}, {"cash performance", "cash_performance", "cash"}, {"card performance", "card_performance", "card"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			s.performanceSetting("performance_split", "full")
			s.performanceSetting("commission_split", "equal")
			s.saveCommission([]any{itemRule(tc.base, "100")}, nil)
			line := s.splitLine("基数"+tc.name, "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
			if tc.method == "cash" {
				s.paid(s.customerOrder(line), payment("cash", "100"))
			} else {
				card := s.paymentCard(s.paymentType("基数卡", nil), "100", "0")
				s.paid(s.customerOrder(line), cardPayment(card, "100"))
			}
			got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
			if got["total"] != "100.00" || len(got["entries"].([]any)) != 2 {
				t.Fatal(got)
			}
			for _, raw := range got["entries"].([]any) {
				base := raw.(map[string]any)["explanation"].(map[string]any)["base"].(map[string]any)
				if base["amount"] != "50.00" || base["kind"] != tc.base {
					t.Fatal(base)
				}
			}
		})
	}
}

func TestCommissionSplitFixedAmountPerPerson(t *testing.T) {
	s := open(t)
	category := s.call("create_item_category", map[string]any{"name": "固定提成类别", "performance_ratio": 100}, 200)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "equal")
	overrides := []any{map[string]any{"source": "item", "position_id": s.position.String(), "category_id": category["id"], "fixed_amount": "3.00"}}
	s.saveCommission([]any{map[string]any{"source": "item", "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{}}}, overrides)
	line := s.splitLine("固定提成", "100.00", category["id"].(string), []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	s.paid(s.customerOrder(line), payment("cash", "100"))
	got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
	if got["total"] != "6.00" || len(got["entries"].([]any)) != 2 {
		t.Fatal(got)
	}
	for _, raw := range got["entries"].([]any) {
		if raw.(map[string]any)["amount"] != "3.00" {
			t.Fatal(raw)
		}
	}
}

func TestCommissionSplitConsumeAndLabor(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "equal")
	rules := []any{}
	for _, source := range []string{"item", "consume", "labor"} {
		rules = append(rules, map[string]any{"source": source, "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "100"}}})
	}
	s.saveCommission(rules, nil)
	in := s.itemInput("耗卡提成", 0)
	in["level_prices"] = []any{}
	in["labor_fee_amount"] = "10.00"
	in["positions"] = []any{map[string]any{"position_id": s.position, "required": false, "share_percent": 70}, map[string]any{"position_id": s.position, "required": false, "share_percent": 30}}
	item := s.call("create_item", in, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}, {Slot: 2, StaffID: s.employee}}}
	card := s.countCard(line, "count", 1, 0)
	o := s.customerOrder(line)
	s.paid(o, countPayment(card, firstLine(o), "100"))
	got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
	if got["total"] != "110.00" || len(got["entries"].([]any)) != 4 {
		t.Fatal(got)
	}
	seen := map[string]map[int16]string{}
	for _, raw := range got["entries"].([]any) {
		entry := raw.(map[string]any)
		source := entry["source"].(string)
		slot := int16(entry["explanation"].(map[string]any)["origin"].(map[string]any)["slot"].(float64))
		if seen[source] == nil {
			seen[source] = map[int16]string{}
		}
		seen[source][slot] = entry["amount"].(string)
	}
	if !reflect.DeepEqual(seen["consume"], map[int16]string{1: "50.00", 2: "50.00"}) || !reflect.DeepEqual(seen["labor"], map[int16]string{1: "7.00", 2: "3.00"}) {
		t.Fatal(seen)
	}
}

func TestCommissionSplitTiers(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "equal")
	tiers := map[string]any{"mode": "whole", "levels": []any{map[string]any{"from": "0.00", "percent": "10"}, map[string]any{"from": "50.00", "percent": "20"}}}
	s.saveCommission([]any{map[string]any{"source": "item", "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"tiers": tiers}}}}, nil)
	line := s.splitLine("阶梯提成", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	s.paid(s.customerOrder(line), payment("cash", "100"))
	got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
	if got["total"] != "20.00" || len(got["entries"].([]any)) != 2 {
		t.Fatal(got)
	}
	for _, raw := range got["entries"].([]any) {
		entry := raw.(map[string]any)
		tier := entry["explanation"].(map[string]any)["tier"].(map[string]any)
		if entry["amount"] != "10.00" || tier["cumulative"] != "50.00" {
			t.Fatal(entry)
		}
	}
}

func TestCommissionSplitInertOutsideFull(t *testing.T) {
	for _, split := range []string{"equal", "by_position"} {
		t.Run(split, func(t *testing.T) {
			run := func(commission string) map[int16]string {
				s := open(t)
				s.performanceSetting("performance_split", split)
				s.performanceSetting("commission_split", commission)
				s.saveCommission([]any{itemRule("performance", "100")}, nil)
				line := s.splitLine("不生效"+split+commission, "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
				s.paid(s.customerOrder(line), payment("cash", "100"))
				out := map[int16]string{}
				for slot, entry := range commissionBySlot(t, s.readBody(t, "list_commissions", commissionArgs(s.Now()))) {
					out[slot] = entry["amount"].(string)
				}
				return out
			}
			if same, equal := run("same"), run("equal"); !reflect.DeepEqual(same, equal) {
				t.Fatal(same, equal)
			}
		})
	}
}

func TestCommissionSplitUsesCheckoutSnapshot(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "same")
	s.saveCommission([]any{itemRule("performance", "100")}, nil)
	line := s.splitLine("设置快照", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	first, beforeChange := s.customerOrder(line), s.customerOrder(line)
	s.paid(first, payment("cash", "100"))
	s.performanceSetting("commission_split", "equal")
	s.paid(beforeChange, payment("cash", "100"))
	afterChange := s.paid(s.customerOrder(line), payment("cash", "100"))
	got := s.readBody(t, "list_commissions", commissionArgs(s.Now()))
	byOrder := map[uuid.UUID]map[int16]string{}
	for _, raw := range got["entries"].([]any) {
		entry := raw.(map[string]any)
		orderID := id(entry["explanation"].(map[string]any)["origin"].(map[string]any)["order_id"])
		slot := int16(entry["explanation"].(map[string]any)["origin"].(map[string]any)["slot"].(float64))
		if byOrder[orderID] == nil {
			byOrder[orderID] = map[int16]string{}
		}
		byOrder[orderID][slot] = entry["amount"].(string)
	}
	if !reflect.DeepEqual(byOrder[id(first["id"])], map[int16]string{1: "100.00", 2: "100.00"}) ||
		!reflect.DeepEqual(byOrder[id(beforeChange["id"])], map[int16]string{1: "100.00", 2: "100.00"}) ||
		!reflect.DeepEqual(byOrder[id(afterChange["id"])], map[int16]string{1: "50.00", 2: "50.00"}) {
		t.Fatal(byOrder)
	}
}

func TestCommissionSplitPerformanceReadsUnchanged(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "equal")
	line := s.splitLine("业绩不变", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	s.paid(s.customerOrder(line), payment("cash", "100"))
	day := settings.Date(time.Now()).Format(settings.DateLayout)
	list := s.readBody(t, "list_performance", map[string]any{"date_from": day, "date_to": day, "limit": 200})
	if list["totals"].([]any)[0].(map[string]any)["amount"] != "200.00" || list["totals"].([]any)[0].(map[string]any)["paid_amount"] != "200.00" {
		t.Fatal(list)
	}
	staff := s.readBody(t, "get_staff_report", map[string]any{"from": day, "to": day})
	for _, raw := range staff["rows"].([]any) {
		row := raw.(map[string]any)
		if row["staff"] != nil && row["item_performance"] != "100.00" {
			t.Fatal(row)
		}
	}
	item := s.readBody(t, "get_item_report", map[string]any{"from": day, "to": day})
	if len(item["rows"].([]any)) != 1 || item["rows"].([]any)[0].(map[string]any)["performance"] != "200.00" {
		t.Fatal(item)
	}
}

func TestVoidCommissionSplitCopiesColumns(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_split", "full")
	s.performanceSetting("commission_split", "equal")
	s.saveCommission([]any{itemRule("performance", "10")}, nil)
	line := s.splitLine("撤单提成", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
	o := s.paid(s.customerOrder(line), payment("cash", "100"))
	s.void(o)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	rows, err := tx.Query(context.Background(), `SELECT e.slot,e.commission_paid_amount::text,e.commission_amount::text,r.commission_paid_amount::text,r.commission_amount::text
 FROM performance_entries e JOIN performance_entries r ON r.reverses_id=e.id AND r.void_id IS NOT NULL
 WHERE e.store_id=$1 AND e.order_id=$2 AND e.kind='item' ORDER BY e.slot`, s.Res.StoreID, o["id"])
	if err != nil {
		t.Fatal(err)
	}
	defer dbtest.Rollback(t, tx)
	type row struct {
		slot                         int16
		originalPaid, originalAmount string
		reversedPaid, reversedAmount string
	}
	rowsOut := []row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.slot, &r.originalPaid, &r.originalAmount, &r.reversedPaid, &r.reversedAmount); err != nil {
			t.Fatal(err)
		}
		rowsOut = append(rowsOut, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	slots := []int16{}
	for _, r := range rowsOut {
		if r.originalPaid != "50.00" || r.originalAmount != "50.00" || r.reversedPaid != r.originalPaid || r.reversedAmount != r.originalAmount {
			t.Fatal(r)
		}
		slots = append(slots, r.slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	if !reflect.DeepEqual(slots, []int16{1, 2}) {
		t.Fatal(slots)
	}
}

func TestCommissionSplitMigrationConstraints(t *testing.T) {
	s := open(t)
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	source := s.performanceEntriesRead(t, "item")[0]["id"]
	for _, override := range []map[string]any{
		{"commission_paid_amount": "101.00", "commission_amount": "101.00"},
		{"commission_paid_amount": "-0.01", "commission_amount": "-0.01"},
		{"commission_amount": "99.99"},
	} {
		s.cloneRefused("performance_entries", source, override, "23514")
	}
	s.paid(s.customerOrder(s.laborLine("10.00", nil)), payment("cash", "100"))
	labor := s.performanceEntriesRead(t, "labor")[0]["id"]
	s.cloneRefused("performance_entries", labor, map[string]any{"commission_paid_amount": "1.00", "commission_amount": "1.00"}, "23514")
	s.paymentCard(s.paymentType("提成迁移卡", nil), "20", "0")
	sale := s.performanceEntriesRead(t, "sale")[0]["id"]
	s.cloneRefused("performance_entries", sale, map[string]any{"commission_paid_amount": "1.00", "commission_amount": "1.00"}, "23514")

	categoryRatio := map[string]any{
		"ratio": 50, "category_ratio": 90, "paid_amount": "100.00", "commission_paid_amount": "100.00",
		"amount": "45.00", "commission_amount": "50.00",
	}
	err := s.clonePerformance(t, source, categoryRatio)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "performance_entries_commission_amount_check" {
		t.Fatal(err)
	}
	categoryRatio["commission_amount"] = "45.00"
	if err := s.clonePerformance(t, source, categoryRatio); err != nil {
		t.Fatal(err)
	}
}

func TestCommissionSplitMigrationDown(t *testing.T) {
	t.Run("refuses split rows", func(t *testing.T) {
		s := open(t)
		s.performanceSetting("performance_split", "full")
		s.performanceSetting("commission_split", "equal")
		line := s.splitLine("回滚提成", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
		s.paid(s.customerOrder(line), payment("cash", "100"))
		dbtest.MigrateDownTo(t, s.Pool, 55)
		err := migrateDown(t, s)
		if err == nil || !strings.Contains(err.Error(), "无法回滚 00055：存在提成分摊与业绩不同的业绩记录") {
			t.Fatal(err)
		}
	})

	t.Run("drops columns when unchanged", func(t *testing.T) {
		s := open(t)
		line := s.splitLine("回滚同样", "100.00", "", []any{70, 30}, []uuid.UUID{s.Res.OwnerID, s.employee})
		s.paid(s.customerOrder(line), payment("cash", "100"))
		dbtest.MigrateDownTo(t, s.Pool, 54)
		var columns int
		if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name='performance_entries' AND column_name IN ('commission_paid_amount','commission_amount')").Scan(&columns); err != nil {
			t.Fatal(err)
		}
		if columns != 0 {
			t.Fatal(columns)
		}
	})
}

func migrateDown(t *testing.T, s *fixture) error {
	t.Helper()
	return db.MigrateDown(context.Background(), s.Pool)
}
