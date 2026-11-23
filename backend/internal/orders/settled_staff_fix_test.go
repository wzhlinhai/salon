package orders_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func staffOf(entry map[string]any) uuid.UUID {
	if entry["staff"] == nil {
		return uuid.Nil
	}
	return id(entry["staff"].(map[string]any)["id"])
}

func orderEntries(entries []any, orderID uuid.UUID) []any {
	out := []any{}
	for _, raw := range entries {
		entry := raw.(map[string]any)
		if entry["order_id"] == orderID.String() {
			out = append(out, entry)
		}
	}
	return out
}

func perfNet(entries []any, staff uuid.UUID, kind string) int64 {
	var total int64
	for _, raw := range entries {
		entry := raw.(map[string]any)
		if entry["kind"] != kind || staffOf(entry) != staff {
			continue
		}
		amount := fenOf(entry["amount"].(string))
		if entry["reversal"] == true {
			amount = -amount
		}
		total += amount
	}
	return total
}

func assertPerf(t *testing.T, entries []any, staff uuid.UUID, kind, day string, reversal bool, want string) {
	t.Helper()
	found := 0
	for _, raw := range entries {
		entry := raw.(map[string]any)
		if entry["kind"] != kind || entry["business_date"] != day || entry["reversal"] != reversal || staffOf(entry) != staff {
			continue
		}
		found++
		if entry["amount"] != want {
			t.Fatalf("%s %s %s reversal=%v amount = %v, want %s", staff, kind, day, reversal, entry["amount"], want)
		}
	}
	if found == 0 {
		t.Fatalf("missing performance: staff=%s kind=%s day=%s reversal=%v", staff, kind, day, reversal)
	}
}

func shareUnits(value string) int64 {
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.SplitN(value, ".", 2)
	whole := int64(0)
	for _, r := range parts[0] {
		whole = whole*10 + int64(r-'0')
	}
	fraction := "0000"
	if len(parts) == 2 {
		fraction = (parts[1] + "0000")[:4]
	}
	frac := int64(0)
	for _, r := range fraction {
		frac = frac*10 + int64(r-'0')
	}
	total := whole*10000 + frac
	if negative {
		total = -total
	}
	return total
}

func guestNet(entries []any, staff uuid.UUID) int64 {
	var total int64
	for _, raw := range entries {
		entry := raw.(map[string]any)
		if staffOf(entry) != staff {
			continue
		}
		share := shareUnits(entry["share"].(string))
		if entry["reversal"] == true {
			share = -share
		}
		total += share
	}
	return total
}

func assertGuest(t *testing.T, entries []any, staff uuid.UUID, day string, reversal bool, want string) {
	t.Helper()
	found := 0
	for _, raw := range entries {
		entry := raw.(map[string]any)
		if entry["business_date"] != day || entry["reversal"] != reversal || staffOf(entry) != staff {
			continue
		}
		found++
		if entry["share"] != want {
			t.Fatalf("%s guest %s reversal=%v share = %v, want %s", staff, day, reversal, entry["share"], want)
		}
	}
	if found == 0 {
		t.Fatalf("missing guest visit: staff=%s day=%s reversal=%v", staff, day, reversal)
	}
}

func (s *fixture) guests(from, to string) []any {
	s.t.Helper()
	return s.call("list_guest_visits", map[string]any{"date_from": from, "date_to": to, "limit": 200}, 200)["entries"].([]any)
}

func commissionScheme(percent string) map[string]any {
	return map[string]any{"rules": []any{
		map[string]any{"source": "item", "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": percent}}},
	}, "overrides": []any{}, "excluded_staff": []any{}}
}

func (s *fixture) twoSlotItem(name string) (uuid.UUID, orders.OrderItemInput) {
	s.t.Helper()
	in := s.itemInput(name+" "+uuid.NewString()[:8], 0)
	in["level_prices"] = []any{}
	in["positions"] = []any{
		map[string]any{"position_id": s.position, "required": false},
		map[string]any{"position_id": s.position, "required": false},
	}
	item := s.call("create_item", in, 200)
	return id(item["id"]), orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}, {Slot: 2, StaffID: s.employee}}}
}

func (s *fixture) staffChangeDay(changeID uuid.UUID) string {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	defer dbtest.Rollback(s.t, tx)
	var day string
	if err := tx.QueryRow(context.Background(), "SELECT business_date::text FROM order_staff_changes WHERE id=$1", changeID).Scan(&day); err != nil {
		s.t.Fatal(err)
	}
	return day
}

func TestSetPaidItemStaffUsesTodayForReversalsAndCommissionVersion(t *testing.T) {
	s := open(t)
	s.SetNow(dayAt("2026-10-01"))
	v1 := s.call("save_commission_scheme", map[string]any{"effective_from": "2026-10-01", "scheme": commissionScheme("10")}, 200)
	o := s.customerOrder(s.laborLine("10", nil))
	s.paid(o, payment("cash", "100"))
	dayOneBefore := s.listPerformance("2026-10-01", "2026-10-01")
	guestsOneBefore := s.call("list_guest_visits", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01", "limit": 200}, 200)
	newStaff, _ := s.Hire("今天接替", "060", "settled-today")
	s.SetNow(dayAt("2026-10-02"))
	v2 := s.call("save_commission_scheme", map[string]any{"effective_from": "2026-10-02", "scheme": commissionScheme("20")}, 200)
	after := s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: newStaff}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
	if after["net"] != "100.00" || after["receivable"] != "100.00" {
		t.Fatal("改人改了钱", after)
	}
	change := after["staff_changes"].([]any)[0].(map[string]any)
	if got := s.staffChangeDay(id(change["id"])); got != "2026-10-02" {
		t.Fatalf("order_staff_changes.business_date = %s", got)
	}
	entries := orderEntries(s.listPerformance("2026-10-01", "2026-10-02")["entries"].([]any), id(o["id"]))
	for _, tc := range []struct {
		staff    uuid.UUID
		kind     string
		day      string
		reversal bool
		want     string
	}{
		{s.Res.OwnerID, "item", "2026-10-01", false, "70.00"}, {s.Res.OwnerID, "item", "2026-10-02", true, "70.00"},
		{s.employee, "item", "2026-10-01", false, "30.00"}, {s.employee, "item", "2026-10-02", true, "30.00"},
		{s.employee, "item", "2026-10-02", false, "30.00"},
		{newStaff, "item", "2026-10-02", false, "70.00"},
		{s.Res.OwnerID, "labor", "2026-10-01", false, "7.00"}, {s.Res.OwnerID, "labor", "2026-10-02", true, "7.00"},
		{s.employee, "labor", "2026-10-01", false, "3.00"}, {s.employee, "labor", "2026-10-02", true, "3.00"},
		{s.employee, "labor", "2026-10-02", false, "3.00"},
		{newStaff, "labor", "2026-10-02", false, "7.00"},
	} {
		assertPerf(t, entries, tc.staff, tc.kind, tc.day, tc.reversal, tc.want)
	}
	if !reflect.DeepEqual(dayOneBefore, s.listPerformance("2026-10-01", "2026-10-01")) {
		t.Fatal("改人改了原单营业日的业绩")
	}
	guestsOneAfter := s.call("list_guest_visits", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01", "limit": 200}, 200)
	if !reflect.DeepEqual(guestsOneBefore, guestsOneAfter) {
		t.Fatal("改人改了原单营业日的客数")
	}
	guests := orderEntries(s.guests("2026-10-01", "2026-10-02"), id(o["id"]))
	assertGuest(t, guests, s.Res.OwnerID, "2026-10-02", true, "1.0000")
	assertGuest(t, guests, newStaff, "2026-10-02", false, "1.0000")
	for _, gross := range []struct {
		staff uuid.UUID
		day   string
		want  int64
	}{{s.Res.OwnerID, "2026-10-02", -10000}, {newStaff, "2026-10-02", 10000}} {
		var got int64
		for _, raw := range guests {
			entry := raw.(map[string]any)
			if entry["business_date"] == gross.day && staffOf(entry) == gross.staff {
				share := shareUnits(entry["share"].(string))
				if entry["reversal"] == true {
					share = -share
				}
				got += share
			}
		}
		if got != gross.want {
			t.Fatalf("%s guest net on %s = %d, want %d", gross.staff, gross.day, got, gross.want)
		}
	}
	commissions := s.call("list_commissions", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02", "limit": 200}, 200)
	ownerReversal, newRecord := false, false
	for _, raw := range commissions["entries"].([]any) {
		c := raw.(map[string]any)
		staff := staffOf(c)
		version := c["explanation"].(map[string]any)["version"].(map[string]any)
		switch {
		case staff == s.Res.OwnerID && c["reversal"] == true && c["source"] == "item":
			ownerReversal = true
			if c["amount"] != "-7.00" || version["effective_from"] != "2026-10-01" || version["id"] != v1["id"] {
				t.Fatalf("冲回没有沿用原方案版本: %v", c)
			}
		case staff == newStaff && c["reversal"] == false && c["source"] == "item":
			newRecord = true
			if c["amount"] != "14.00" || version["effective_from"] != "2026-10-02" || version["id"] != v2["id"] {
				t.Fatalf("新记录没有用今天生效的方案版本: %v", c)
			}
		}
	}
	if !ownerReversal || !newRecord {
		t.Fatalf("commissions missing entries: %v", commissions)
	}
}

func TestSetPaidItemStaffGuestSplits(t *testing.T) {
	t.Run("equal", func(t *testing.T) {
		s := open(t)
		s.SetNow(dayAt("2026-10-01"))
		s.call("change_setting", map[string]any{"key": "guest_count_split", "value": "equal", "effective_from": "2026-10-01"}, 200)
		o := s.customerOrder(s.performanceLine(70, 30))
		s.paid(o, payment("cash", "100"))
		dayOneBefore := s.guests("2026-10-01", "2026-10-01")
		newStaff, _ := s.Hire("均分接替", "061", "settled-equal")
		s.SetNow(dayAt("2026-10-02"))
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: newStaff}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
		if !reflect.DeepEqual(dayOneBefore, s.guests("2026-10-01", "2026-10-01")) {
			t.Fatal("改人改了原单营业日的客数")
		}
		guests := orderEntries(s.guests("2026-10-01", "2026-10-02"), id(o["id"]))
		for _, tc := range []struct {
			staff    uuid.UUID
			reversal bool
			want     string
		}{
			{s.Res.OwnerID, true, "0.5000"}, {s.employee, true, "0.5000"},
			{newStaff, false, "0.5000"}, {s.employee, false, "0.5000"},
		} {
			assertGuest(t, guests, tc.staff, "2026-10-02", tc.reversal, tc.want)
		}
		for _, tc := range []struct {
			staff uuid.UUID
			want  int64
		}{{s.Res.OwnerID, -5000}, {newStaff, 5000}, {s.employee, 0}} {
			var got int64
			for _, raw := range guests {
				entry := raw.(map[string]any)
				if entry["business_date"] != "2026-10-02" || staffOf(entry) != tc.staff {
					continue
				}
				share := shareUnits(entry["share"].(string))
				if entry["reversal"] == true {
					share = -share
				}
				got += share
			}
			if got != tc.want {
				t.Fatalf("%s guest net on change day = %d, want %d", tc.staff, got, tc.want)
			}
		}
	})

	t.Run("first slot unchanged when another slot changes", func(t *testing.T) {
		s := open(t)
		s.SetNow(dayAt("2026-10-01"))
		o := s.customerOrder(s.performanceLine(70, 30))
		s.paid(o, payment("cash", "100"))
		newStaff, _ := s.Hire("非首岗接替", "062", "settled-first")
		s.SetNow(dayAt("2026-10-02"))
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: s.Res.OwnerID}, orders.OrderItemStaffInput{Slot: 2, StaffID: newStaff}), 200)
		guests := s.guests("2026-10-02", "2026-10-02")
		if len(guests) != 0 {
			t.Fatalf("first_slot 下改非第一岗位重记了客数: %v", guests)
		}
		if total := s.call("list_guest_visits", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02"}, 200)["totals"].(map[string]any)["total"]; total != "1.0000" {
			t.Fatalf("guest total = %v", total)
		}
	})
}

func TestSetPaidItemStaffConsecutiveAndTwoLines(t *testing.T) {
	t.Run("same line A to B to C", func(t *testing.T) {
		s := open(t)
		s.SetNow(dayAt("2026-10-01"))
		o := s.customerOrder(s.performanceLine(70, 30))
		s.paid(o, payment("cash", "100"))
		b, _ := s.Hire("连续乙", "063", "settled-chain-b")
		c, _ := s.Hire("连续丙", "064", "settled-chain-c")
		s.SetNow(dayAt("2026-10-02"))
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: b}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
		s.SetNow(dayAt("2026-10-03"))
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: c}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
		entries := orderEntries(s.listPerformance("2026-10-01", "2026-10-03")["entries"].([]any), id(o["id"]))
		for staff, want := range map[uuid.UUID]int64{s.Res.OwnerID: 0, b: 0, c: 7000} {
			if got := perfNet(entries, staff, "item"); got != want {
				t.Fatalf("%s item net = %d, want %d", staff, got, want)
			}
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		defer dbtest.Rollback(t, tx)
		var duplicates int
		if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM (
  SELECT reverses_id FROM performance_entries WHERE order_id=$1 AND reverses_id IS NOT NULL GROUP BY reverses_id HAVING count(*) > 1
) d`, id(o["id"])).Scan(&duplicates); err != nil {
			t.Fatal(err)
		}
		if duplicates != 0 {
			t.Fatal("同一笔原记录被冲回多次", duplicates)
		}
		var reversals, newRecords int
		if err := tx.QueryRow(context.Background(), `SELECT
  count(*) FILTER (WHERE staff_change_id IS NOT NULL AND reverses_id IS NOT NULL),
  count(*) FILTER (WHERE staff_change_id IS NOT NULL AND reverses_id IS NULL)
 FROM performance_entries WHERE order_id=$1 AND kind='item'`, id(o["id"])).Scan(&reversals, &newRecords); err != nil {
			t.Fatal(err)
		}
		if reversals != 4 || newRecords != 4 {
			t.Fatalf("staff change reversals=%d new=%d", reversals, newRecords)
		}
	})

	t.Run("two lines change once each", func(t *testing.T) {
		s := open(t)
		s.SetNow(dayAt("2026-10-01"))
		s.call("change_setting", map[string]any{"key": "guest_count_split", "value": "equal", "effective_from": "2026-10-01"}, 200)
		first := s.performanceLine(100)
		first.Staff = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}}
		second := s.performanceLine(100)
		second.Staff = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}
		o := s.customerOrder(first, second)
		s.paid(o, payment("cash", "200"))
		b, _ := s.Hire("两行乙", "065", "settled-lines-b")
		d, _ := s.Hire("两行丁", "066", "settled-lines-d")
		s.SetNow(dayAt("2026-10-02"))
		ls := lines(o)
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(ls[0].(map[string]any)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: b}), 200)
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(ls[1].(map[string]any)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: d}), 200)
		entries := orderEntries(s.listPerformance("2026-10-01", "2026-10-02")["entries"].([]any), id(o["id"]))
		for staff, want := range map[uuid.UUID]int64{s.Res.OwnerID: 0, s.employee: 0, b: 10000, d: 10000} {
			if got := perfNet(entries, staff, "item"); got != want {
				t.Fatalf("%s item net = %d, want %d", staff, got, want)
			}
		}
		guests := orderEntries(s.guests("2026-10-01", "2026-10-02"), id(o["id"]))
		for staff, want := range map[uuid.UUID]int64{s.Res.OwnerID: 0, s.employee: 0, b: 5000, d: 5000} {
			if got := guestNet(guests, staff); got != want {
				t.Fatalf("%s guest net = %d, want %d", staff, got, want)
			}
		}
		for _, tc := range []struct {
			staff uuid.UUID
			want  int64
		}{{s.Res.OwnerID, -5000}, {s.employee, -5000}, {b, 5000}, {d, 5000}} {
			var got int64
			for _, raw := range guests {
				entry := raw.(map[string]any)
				if entry["business_date"] != "2026-10-02" || staffOf(entry) != tc.staff {
					continue
				}
				share := shareUnits(entry["share"].(string))
				if entry["reversal"] == true {
					share = -share
				}
				got += share
			}
			if got != tc.want {
				t.Fatalf("%s guest net on change day = %d, want %d", tc.staff, got, tc.want)
			}
		}
	})
}

func TestSetPaidItemStaffAftersaleOnlyReversesCurrentRecords(t *testing.T) {
	s := open(t)
	s.SetNow(dayAt("2026-10-01"))
	o := s.customerOrder(s.performanceLine(70, 30))
	s.paid(o, payment("cash", "100"))
	b, _ := s.Hire("售后乙", "067", "settled-aftersale")
	s.SetNow(dayAt("2026-10-02"))
	s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: b}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
	refund := s.call("create_aftersale", map[string]any{"order_id": o["id"], "type": "partial_refund", "reason": "改人后退款", "refund_lines": []any{payment("cash", "40")}}, 200)
	s.call("approve_aftersale", map[string]any{"id": refund["id"], "deduct_performance": true, "deduct_commission": true}, 200)
	entries := orderEntries(s.listPerformance("2026-10-01", "2026-10-02")["entries"].([]any), id(o["id"]))
	if got := perfNet(entries, s.Res.OwnerID, "item"); got != 0 {
		t.Fatalf("原员工 A 净额 = %d, want 0", got)
	}
	if got := perfNet(entries, b, "item"); got != 4200 {
		t.Fatalf("新员工 B 净额 = %d, want 4200", got)
	}
	if got := perfNet(entries, s.employee, "item"); got != 1800 {
		t.Fatalf("其他员工净额 = %d, want 1800", got)
	}
	for _, tc := range []struct {
		staff uuid.UUID
		want  string
	}{{b, "28.00"}, {s.employee, "12.00"}} {
		found := 0
		for _, raw := range entries {
			entry := raw.(map[string]any)
			if staffOf(entry) != tc.staff || entry["kind"] != "item" || entry["reversal"] != true || entry["aftersale_id"] == nil {
				continue
			}
			found++
			if entry["amount"] != tc.want {
				t.Fatalf("%s 售后冲回金额 = %v, want %s", tc.staff, entry["amount"], tc.want)
			}
		}
		if found != 1 {
			t.Fatalf("%s 售后冲回条数 = %d", tc.staff, found)
		}
	}
	for _, raw := range entries {
		entry := raw.(map[string]any)
		if staffOf(entry) == s.Res.OwnerID && entry["aftersale_id"] != nil {
			t.Fatal("售后冲回了改人已冲回的原记录", entry)
		}
	}
}

func TestSetPaidItemStaffItemReportFullSplit(t *testing.T) {
	t.Run("card and groupbuy keep collected amount", func(t *testing.T) {
		for _, method := range []string{"card", "groupbuy"} {
			t.Run(method, func(t *testing.T) {
				s := open(t)
				s.SetNow(dayAt("2026-10-01"))
				s.call("change_setting", map[string]any{"key": "performance_split", "value": "full", "effective_from": "2026-10-01"}, 200)
				itemID, line := s.twoSlotItem("全额" + method)
				var o map[string]any
				if method == "card" {
					card := s.paymentCard(s.paymentType("全额卡", nil), "100", "0")
					o = s.paid(s.customerOrder(line), cardPayment(card, "100"))
				} else {
					s.Groupbuy.Register("douyin", fakeprovider.New("douyin", s.Now()))
					deal := s.call("create_groupbuy_deal", map[string]any{"platform": "douyin", "name": "全额券" + uuid.NewString(), "purchase_price": "100.00", "item_id": itemID}, 200)
					key := "full-" + uuid.NewString()
					s.call("redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}, 200)
					redemption := s.call("get_redemption", map[string]any{"key": key}, 200)
					opened := s.call("open_groupbuy_order", groupbuy.OpenGroupbuyOrderInput{CustomerID: &s.customer, RedemptionID: id(redemption["id"]), DealID: id(deal["id"]), Gender: "female", Designated: true, Items: []orders.OrderItemInput{line}}, 200)
					o = s.paid(opened["order"].(map[string]any))
				}
				b, _ := s.Hire("全额接替", "068", "settled-full")
				s.SetNow(dayAt("2026-10-02"))
				s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: b}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
				report := s.call("get_item_report", map[string]any{"from": "2026-10-01", "to": "2026-10-02"}, 200)
				row := itemReportRowByID(t, report, itemID)
				if method == "card" && row["card_paid"] != "100.00" {
					t.Fatalf("card_paid = %v", row["card_paid"])
				}
				if method == "groupbuy" && row["groupbuy_paid"] != "100.00" {
					t.Fatalf("groupbuy_paid = %v", row["groupbuy_paid"])
				}
			})
		}
	})

	t.Run("two changes on the same day do not double count", func(t *testing.T) {
		s := open(t)
		s.SetNow(dayAt("2026-10-01"))
		s.call("change_setting", map[string]any{"key": "performance_split", "value": "full", "effective_from": "2026-10-01"}, 200)
		itemID, line := s.twoSlotItem("同日两次")
		card := s.paymentCard(s.paymentType("同日卡", nil), "100", "0")
		o := s.paid(s.customerOrder(line), cardPayment(card, "100"))
		b, _ := s.Hire("同日乙", "069", "settled-full-b")
		c, _ := s.Hire("同日丙", "070", "settled-full-c")
		s.SetNow(dayAt("2026-10-02"))
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: b}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: c}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
		report := s.call("get_item_report", map[string]any{"from": "2026-10-01", "to": "2026-10-02"}, 200)
		row := itemReportRowByID(t, report, itemID)
		if row["card_paid"] != "100.00" {
			t.Fatalf("同日两次改人 card_paid = %v", row["card_paid"])
		}
	})
}

func itemReportRowByID(t *testing.T, report map[string]any, itemID uuid.UUID) map[string]any {
	t.Helper()
	for _, raw := range report["rows"].([]any) {
		row := raw.(map[string]any)
		if row["item_id"] == itemID.String() {
			return row
		}
	}
	t.Fatalf("item %s missing from report %v", itemID, report)
	return nil
}

func TestSetPaidItemStaffMemberCardGroupbuyAndRotation(t *testing.T) {
	t.Run("member commission and card balance", func(t *testing.T) {
		s := open(t)
		s.SetNow(dayAt("2026-10-01"))
		scheme := map[string]any{"rules": []any{
			map[string]any{"source": "item", "base": "performance", "by_position": false, "by_designated": false, "by_member": true, "cells": []any{
				map[string]any{"member": true, "percent": "20"},
				map[string]any{"member": false, "percent": "5"},
			}},
		}, "overrides": []any{}, "excluded_staff": []any{}}
		s.call("save_commission_scheme", map[string]any{"effective_from": "2026-10-01", "scheme": scheme}, 200)
		memberCard := s.paymentCard(s.paymentType("会员余额卡", nil), "200", "0")
		o := s.customerOrder(s.performanceLine(70, 30))
		s.paid(o, cardPayment(memberCard, "100"))
		cardBefore := dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "cards", "card_ledger")
		b, _ := s.Hire("会员接替", "071", "settled-member")
		s.SetNow(dayAt("2026-10-02"))
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: b}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
		if cardBefore != dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "cards", "card_ledger") {
			t.Fatal("改人改了卡余额或卡流水")
		}
		entries := orderEntries(s.listPerformance("2026-10-01", "2026-10-02")["entries"].([]any), id(o["id"]))
		found := false
		for _, raw := range entries {
			entry := raw.(map[string]any)
			if staffOf(entry) == b && entry["kind"] == "item" && entry["reversal"] == false {
				found = true
				if entry["member"] != true || entry["amount"] != "70.00" {
					t.Fatalf("新记录 member=%v amount=%v", entry["member"], entry["amount"])
				}
			}
		}
		if !found {
			t.Fatal("missing new staff performance")
		}
		commissions := s.call("list_commissions", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02", "limit": 200}, 200)
		found = false
		for _, raw := range commissions["entries"].([]any) {
			c := raw.(map[string]any)
			if staffOf(c) == b && c["reversal"] == false && c["source"] == "item" {
				found = true
				if c["amount"] != "14.00" || c["explanation"].(map[string]any)["member"] != true {
					t.Fatalf("会员提成 = %v", c)
				}
			}
		}
		if !found {
			t.Fatal("missing member commission")
		}
	})

	t.Run("groupbuy and rotation unchanged", func(t *testing.T) {
		s := open(t)
		s.SetNow(dayAt("2026-10-01"))
		queue := s.call("create_rotation_queue", map[string]any{"name": "改人牌"}, 200)
		s.call("set_queue_members", map[string]any{"queue_id": queue["id"], "staff_ids": []uuid.UUID{s.Res.OwnerID, s.employee}}, 200)
		itemID, line := s.twoSlotItem("团购")
		s.Groupbuy.Register("douyin", fakeprovider.New("douyin", s.Now()))
		deal := s.call("create_groupbuy_deal", map[string]any{"platform": "douyin", "name": "团购券" + uuid.NewString(), "purchase_price": "100.00", "item_id": itemID}, 200)
		key := "settled-groupbuy-" + uuid.NewString()
		s.call("redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}, 200)
		redemption := s.call("get_redemption", map[string]any{"key": key}, 200)
		opened := s.call("open_groupbuy_order", groupbuy.OpenGroupbuyOrderInput{CustomerID: &s.customer, RedemptionID: id(redemption["id"]), DealID: id(deal["id"]), Gender: "female", Designated: true, Items: []orders.OrderItemInput{line}}, 200)
		o := opened["order"].(map[string]any)
		after := s.call("checkout_orders", checkoutInput(checkoutOrder(o)), 200)
		o = lines(after)[0].(map[string]any)
		rotationBefore := dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "rotation_queues", "rotation_members", "rotation_turns", "rotation_events", "rotation_half_items")
		groupbuyBefore := s.call("get_order", orders.GetOrderInput{ID: id(o["id"])}, 200)["groupbuy"]
		b, _ := s.Hire("团购接替", "072", "settled-groupbuy")
		s.SetNow(dayAt("2026-10-02"))
		s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: b}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
		if !reflect.DeepEqual(groupbuyBefore, s.call("get_order", orders.GetOrderInput{ID: id(o["id"])}, 200)["groupbuy"]) {
			t.Fatal("改人改了团购关联")
		}
		if rotationBefore != dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "rotation_queues", "rotation_members", "rotation_turns", "rotation_events", "rotation_half_items") {
			t.Fatal("改人改了轮牌队列")
		}
	})
}

func TestSetPaidItemStaffReportUsesCurrentStaffOnOriginalDay(t *testing.T) {
	s := open(t)
	s.SetNow(dayAt("2026-10-01"))
	o := s.customerOrder(s.performanceLine(100))
	s.paid(o, payment("cash", "100"))
	b, _ := s.Hire("报表接替", "073", "settled-report")
	s.SetNow(dayAt("2026-10-02"))
	s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: b}), 200)
	report := s.call("get_staff_report", map[string]any{"from": "2026-10-01", "to": "2026-10-01"}, 200)
	rows := map[string]map[string]any{}
	for _, raw := range report["rows"].([]any) {
		row := raw.(map[string]any)
		if row["staff"] != nil {
			rows[row["staff"].(map[string]any)["id"].(string)] = row
		}
	}
	owner := rows[s.Res.OwnerID.String()]
	replacement := rows[b.String()]
	if owner == nil || replacement == nil {
		t.Fatalf("staff report rows = %v", report)
	}
	if owner["item_performance"] != "100.00" || owner["guest_total"] != "1.0000" || owner["service_item_lines"] != float64(0) {
		t.Fatalf("原员工报表 = %v", owner)
	}
	if replacement["item_performance"] != "0.00" || replacement["guest_total"] != "0.0000" || replacement["service_item_lines"] != float64(1) {
		t.Fatalf("改后员工在原营业日的报表 = %v", replacement)
	}
	overview := s.call("list_customer_overview", map[string]any{"limit": 100}, 200)
	found := false
	for _, raw := range overview["items"].([]any) {
		row := raw.(map[string]any)
		if row["customer"].(map[string]any)["id"] != s.customer.String() {
			continue
		}
		staff := row["last_visit_staff"].([]any)
		if len(staff) != 1 || staff[0].(map[string]any)["id"] != b.String() {
			t.Fatalf("上次服务员工 = %v", staff)
		}
		found = true
	}
	if !found {
		t.Fatal("customer missing from overview")
	}
}
