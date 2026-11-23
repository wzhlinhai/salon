package orders_test

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func dayAt(day string) time.Time {
	d, err := time.ParseInLocation(settings.DateLayout, day, settings.Location)
	if err != nil {
		panic(err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, settings.Location)
}

func (s *fixture) listPerformance(from, to string) map[string]any {
	s.t.Helper()
	return s.call("list_performance", map[string]any{"date_from": from, "date_to": to, "limit": 200}, 200)
}

func (s *fixture) settledStaffSnapshot() string {
	s.t.Helper()
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "orders", "order_items", "order_item_positions", "order_staff_changes", "performance_entries", "guest_visits", "audit_log")
}

func paidStaffInput(orderID, itemID uuid.UUID, staff ...orders.OrderItemStaffInput) orders.SetPaidItemStaffInput {
	return orders.SetPaidItemStaffInput{OrderID: orderID, OrderItemID: itemID, Staff: staff}
}

func (s *fixture) assertPaidStaffRefused(in orders.SetPaidItemStaffInput, status int, detail string) {
	s.t.Helper()
	before := s.settledStaffSnapshot()
	r := s.HTTPWithToken(s.t, s.Owner, "set_paid_item_staff", in)
	if r.Status != status || r.Detail() != detail || before != s.settledStaffSnapshot() {
		s.t.Fatal(r, "refusal must return the rule and leave data unchanged")
	}
}

func performanceByStaffKind(entries []any, staff uuid.UUID, kind string, reversal bool) string {
	for _, raw := range entries {
		e := raw.(map[string]any)
		if e["kind"] == kind && e["reversal"] == reversal && e["staff"] != nil && e["staff"].(map[string]any)["id"] == staff.String() {
			return e["amount"].(string)
		}
	}
	return ""
}

func fenOf(value string) int64 {
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.SplitN(value, ".", 2)
	yuan, _ := strconv.ParseInt(parts[0], 10, 64)
	var cents int64
	if len(parts) == 2 {
		frac := parts[1] + "00"
		cents, _ = strconv.ParseInt(frac[:2], 10, 64)
	}
	fen := yuan*100 + cents
	if negative {
		fen = -fen
	}
	return fen
}

func TestSetPaidItemStaffPerformanceAndCommission(t *testing.T) {
	s := open(t)
	d1, d2 := dayAt("2026-10-01"), dayAt("2026-10-02")
	s.SetNow(d1)
	scheme := map[string]any{"rules": []any{
		map[string]any{"source": "item", "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10"}}},
		map[string]any{"source": "labor", "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "5"}}},
	}, "overrides": []any{}, "excluded_staff": []any{}}
	s.call("save_commission_scheme", map[string]any{"effective_from": "2026-10-01", "scheme": scheme}, 200)
	o := s.customerOrder(s.laborLine("10", nil))
	before := s.paid(o, payment("cash", "100"))
	newStaff, _ := s.Hire("接替员工", "031", "settled-a")
	s.SetNow(d2)
	in := paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: newStaff}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee})
	after := s.call("set_paid_item_staff", in, 200)
	if after["net"] != before["net"] || after["receivable"] != before["receivable"] || !reflect.DeepEqual(after["payments"], before["payments"]) {
		t.Fatal("改人改了钱或支付", after)
	}
	changes := after["staff_changes"].([]any)
	if len(changes) != 1 {
		t.Fatal(changes)
	}
	change := changes[0].(map[string]any)
	if change["by"] != s.Res.OwnerID.String() || change["order_item_id"] != firstLine(o)["id"] || len(change["before"].([]any)) != 2 || len(change["after"].([]any)) != 2 {
		t.Fatal(change)
	}
	entries := withoutSale(s.listPerformance("2026-10-01", "2026-10-02")["entries"].([]any))
	for _, tc := range []struct {
		staff    uuid.UUID
		kind     string
		reversal bool
		want     string
	}{
		{s.Res.OwnerID, "item", false, "70.00"}, {s.Res.OwnerID, "item", true, "70.00"},
		{newStaff, "item", false, "70.00"}, {s.employee, "item", false, "30.00"}, {s.employee, "item", true, "30.00"},
		{s.Res.OwnerID, "labor", false, "7.00"}, {s.Res.OwnerID, "labor", true, "7.00"},
		{newStaff, "labor", false, "7.00"}, {s.employee, "labor", false, "3.00"}, {s.employee, "labor", true, "3.00"},
	} {
		if got := performanceByStaffKind(entries, tc.staff, tc.kind, tc.reversal); got != tc.want {
			t.Fatalf("staff=%s kind=%s reversal=%v got=%q want=%q", tc.staff, tc.kind, tc.reversal, got, tc.want)
		}
	}
	commissions := s.call("list_commissions", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02", "limit": 200}, 200)
	sums := map[string]int64{}
	for _, raw := range commissions["entries"].([]any) {
		c := raw.(map[string]any)
		staffID := c["staff"].(map[string]any)["id"].(string)
		sums[staffID] += fenOf(c["amount"].(string))
	}
	for staffID, want := range map[uuid.UUID]int64{s.Res.OwnerID: 0, s.employee: 315, newStaff: 735} {
		if sums[staffID.String()] != want {
			t.Fatalf("commission staff %s = %d want %d: %v", staffID, sums[staffID.String()], want, commissions)
		}
	}
	if commissions["total"] != "10.50" {
		t.Fatal(commissions)
	}
	s.assertEditAudit("set_paid_item_staff", before, after)
}

func TestSetPaidItemStaffFillsEmptySlot(t *testing.T) {
	s := open(t)
	d1, d2 := dayAt("2026-10-01"), dayAt("2026-10-02")
	s.SetNow(d1)
	in := s.itemInput("两人分配", 0)
	in["level_prices"] = []any{}
	in["positions"] = []any{map[string]any{"position_id": s.position, "required": false, "share_percent": 50}, map[string]any{"position_id": s.position, "required": false, "share_percent": 50}}
	item := s.call("create_item", in, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}}}
	o := s.customerOrder(line)
	s.paid(o, payment("cash", "100"))
	newStaff, _ := s.Hire("补位员工", "032", "settled-b")
	s.SetNow(d2)
	after := s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: s.Res.OwnerID}, orders.OrderItemStaffInput{Slot: 2, StaffID: newStaff}), 200)
	if firstLine(after)["price"] != "100.00" {
		t.Fatal("已结账的单重新计价", after)
	}
	entries := withoutSale(s.listPerformance("2026-10-01", "2026-10-02")["entries"].([]any))
	for _, tc := range []struct {
		staff    uuid.UUID
		reversal bool
		want     string
	}{{s.Res.OwnerID, false, "50.00"}, {newStaff, false, "50.00"}, {s.Res.OwnerID, true, "100.00"}} {
		if got := performanceByStaffKind(entries, tc.staff, "item", tc.reversal); got != tc.want {
			t.Fatalf("staff=%s reversal=%v got=%q want=%q", tc.staff, tc.reversal, got, tc.want)
		}
	}
}

func TestSetPaidItemStaffDoesNotReprice(t *testing.T) {
	s := open(t)
	d1, d2 := dayAt("2026-10-01"), dayAt("2026-10-02")
	s.SetNow(d1)
	o := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	newStaff, _ := s.Hire("无级别接替", "038", "settled-price")
	s.SetNow(d2)
	after := s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: newStaff}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.employee}), 200)
	line := firstLine(after)
	if line["price"] != "150.00" || line["price_source"] != "level" || after["receivable"] != "150.00" {
		t.Fatal("已结账的单重新计价", after)
	}
}

func TestSetPaidItemStaffVoidDoesNotReverseTwice(t *testing.T) {
	s := open(t)
	d1, d2 := dayAt("2026-10-01"), dayAt("2026-10-02")
	s.SetNow(d1)
	s.call("change_setting", map[string]any{"key": "guest_count_split", "value": "equal", "effective_from": "2026-10-01"}, 200)
	line := s.performanceLine(70, 30)
	c := s.countCard(line, "count", 1, 0)
	o := s.customerOrder(line)
	s.paid(o, countPayment(c, firstLine(o), "100"))
	newStaff, _ := s.Hire("撤单接替", "033", "settled-c")
	s.SetNow(d2)
	s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: newStaff}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}), 200)
	changed := withoutSale(s.listPerformance("2026-10-01", "2026-10-02")["entries"].([]any))
	for _, tc := range []struct {
		staff    uuid.UUID
		reversal bool
		want     string
	}{{s.Res.OwnerID, false, "70.00"}, {s.Res.OwnerID, true, "70.00"}, {newStaff, false, "70.00"}, {s.employee, false, "30.00"}, {s.employee, true, "30.00"}} {
		if got := performanceByStaffKind(changed, tc.staff, "consume", tc.reversal); got != tc.want {
			t.Fatalf("consume staff=%s reversal=%v got=%q want=%q", tc.staff, tc.reversal, got, tc.want)
		}
	}
	s.call("void_order", map[string]any{"order_id": o["id"], "reason": "改人后撤单"}, 200)
	list := s.listPerformance("2026-10-01", "2026-10-02")
	for _, total := range withoutSale(list["totals"].([]any)) {
		if fenOf(total.(map[string]any)["amount"].(string)) != 0 {
			t.Fatal(list)
		}
	}
	visits := s.call("list_guest_visits", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02"}, 200)
	if visits["totals"].(map[string]any)["total"] != "0.0000" {
		t.Fatal(visits)
	}
	perStaff := map[string]int64{}
	for _, raw := range withoutSale(list["entries"].([]any)) {
		e := raw.(map[string]any)
		if e["staff"] == nil {
			continue
		}
		amount := fenOf(e["amount"].(string))
		if e["reversal"] == true {
			amount = -amount
		}
		perStaff[e["staff"].(map[string]any)["id"].(string)] += amount
	}
	for staff, total := range perStaff {
		if total != 0 {
			t.Fatalf("员工 %s 改人后撤单未归零: %d", staff, total)
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
		t.Fatal("同一笔记业绩被冲回多次", duplicates)
	}
	var staffChangeReversals, voidReversals int
	if err := tx.QueryRow(context.Background(), `SELECT
  count(*) FILTER (WHERE staff_change_id IS NOT NULL AND reverses_id IS NOT NULL),
  count(*) FILTER (WHERE void_id IS NOT NULL AND reverses_id IS NOT NULL)
 FROM performance_entries WHERE order_id=$1`, id(o["id"])).Scan(&staffChangeReversals, &voidReversals); err != nil {
		t.Fatal(err)
	}
	if staffChangeReversals != 2 || voidReversals != 2 {
		t.Fatal(staffChangeReversals, voidReversals)
	}
	var guestDuplicates int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM (
  SELECT reverses_id FROM guest_visits WHERE order_id=$1 AND reverses_id IS NOT NULL GROUP BY reverses_id HAVING count(*) > 1
) d`, id(o["id"])).Scan(&guestDuplicates); err != nil {
		t.Fatal(err)
	}
	if guestDuplicates != 0 {
		t.Fatal("同一笔客数被冲回多次", guestDuplicates)
	}
}

func TestSetPaidItemStaffRefusals(t *testing.T) {
	s := open(t)
	d1, d2 := dayAt("2026-10-01"), dayAt("2026-10-02")
	s.SetNow(d1)
	open := s.customerOrder(s.line(1, 2, 3))
	lineID := id(firstLine(open)["id"])
	s.assertPaidStaffRefused(paidStaffInput(id(open["id"]), lineID, orders.OrderItemStaffInput{Slot: 1, StaffID: s.employee}), 409, "只能撤已结账的单")

	paid := s.paid(open, payment("cash", "150"))
	s.assertPaidStaffRefused(paidStaffInput(id(paid["id"]), lineID, orders.OrderItemStaffInput{Slot: 1, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.employee}), 409, "做的人没有变化")
	s.assertPaidStaffRefused(paidStaffInput(id(paid["id"]), lineID, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.employee}), 409, "必填岗位还没填人")
	voided := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	voidedLine := id(firstLine(voided)["id"])
	s.void(voided)
	s.assertPaidStaffRefused(paidStaffInput(id(voided["id"]), voidedLine, orders.OrderItemStaffInput{Slot: 1, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.employee}), 409, "单已撤")

	removedOrder := s.customerOrder(s.line(1, 2, 3), s.line(1, 2, 3))
	removedLine := id(firstLine(removedOrder)["id"])
	s.edit("remove_order_item", editInput(removedOrder), 200)
	removedOrder = s.paid(removedOrder, payment("cash", "150"))
	s.assertPaidStaffRefused(paidStaffInput(id(removedOrder["id"]), removedLine, orders.OrderItemStaffInput{Slot: 1, StaffID: s.employee}), 409, "项目已去掉")

	aftersaleOrder := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	aftersaleLine := id(firstLine(aftersaleOrder)["id"])
	s.call("create_aftersale", map[string]any{"order_id": aftersaleOrder["id"], "type": "repair", "reason": "修补"}, 200)
	s.assertPaidStaffRefused(paidStaffInput(id(aftersaleOrder["id"]), aftersaleLine, orders.OrderItemStaffInput{Slot: 1, StaffID: s.employee}), 409, "这张单有售后单，不能再改做的人")

	windowOrder := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	windowLine := id(firstLine(windowOrder)["id"])
	role := s.call("create_role", map[string]any{"name": "零天改人", "permissions": []any{map[string]any{"permission": "order.edit", "scope": "store", "window_days": 0}}}, 200)
	_, windowToken := s.Hire("零天员工", "034", "settled-window", id(role["id"]))
	s.SetNow(d2)
	caller := s.MustCaller(windowToken)
	before := s.settledStaffSnapshot()
	r := s.HTTP(t, &caller, "set_paid_item_staff", paidStaffInput(id(windowOrder["id"]), windowLine, orders.OrderItemStaffInput{Slot: 1, StaffID: s.employee}))
	if r.Status != 403 || r.Detail() != "permission denied" || before != s.settledStaffSnapshot() {
		t.Fatal(r)
	}

	selfOrder := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	selfRole := s.call("create_role", map[string]any{"name": "本人改人", "permissions": []any{map[string]any{"permission": "order.edit", "scope": "self"}}}, 200)
	_, selfToken := s.Hire("本人员工", "035", "settled-self", id(selfRole["id"]))
	self := s.MustCaller(selfToken)
	before = s.settledStaffSnapshot()
	r = s.HTTP(t, &self, "set_paid_item_staff", paidStaffInput(id(selfOrder["id"]), id(firstLine(selfOrder)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: self.StaffID}))
	if r.Status != 403 || r.Detail() != "permission denied" || before != s.settledStaffSnapshot() {
		t.Fatal(r)
	}

	// 当前营业日已封账：把时钟拨到 D4，封 D3 后再回到 D3 操作。
	todaySealedOrder := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	todaySealedLine := id(firstLine(todaySealedOrder)["id"])
	s.SetNow(dayAt("2026-10-04"))
	s.call("seal_day", map[string]any{"date": "2026-10-03"}, 200)
	s.SetNow(dayAt("2026-10-03"))
	before = s.settledStaffSnapshot()
	r = s.HTTPWithToken(t, s.Owner, "set_paid_item_staff", paidStaffInput(id(todaySealedOrder["id"]), todaySealedLine, orders.OrderItemStaffInput{Slot: 1, StaffID: s.employee}))
	if r.Status != 409 || r.Detail() != "该营业日已封账（2026-10-03）" || before != s.settledStaffSnapshot() {
		t.Fatal(r)
	}
}

func TestSetPaidItemStaffOrderDaySealedAllowsChange(t *testing.T) {
	s := open(t)
	s.SetNow(dayAt("2026-10-01"))
	o := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	s.SetNow(dayAt("2026-10-02"))
	s.call("close_day", map[string]any{"date": "2026-10-01", "cash_counted": "150", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	s.call("seal_day", map[string]any{"date": "2026-10-01"}, 200)
	closeBefore := s.call("get_day_close", map[string]any{"date": "2026-10-01"}, 200)
	newStaff, _ := s.Hire("封账后接替", "036", "settled-sealed")
	s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: newStaff}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.employee}), 200)
	closeAfter := s.call("get_day_close", map[string]any{"date": "2026-10-01"}, 200)
	if !reflect.DeepEqual(closeBefore, closeAfter) {
		t.Fatal("改人改了已封账营业日的日结")
	}
	visits := s.call("list_guest_visits", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02"}, 200)
	if visits["totals"].(map[string]any)["total"] != "1.0000" {
		t.Fatal(visits)
	}
}

func TestSetPaidItemStaffAIDraftAndViewQueryCount(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	newStaff, _ := s.Hire("AI 接替", "037", "settled-ai")
	in := paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: newStaff}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.employee})
	before := s.settledStaffSnapshot()
	r := s.AI(t, s.MustCaller(s.Owner), "set_paid_item_staff", in)
	optest.RequireDraftCreated(t, r, before == s.settledStaffSnapshot())

	one := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	many := s.paid(s.customerOrder(s.line(1, 2, 3)), payment("cash", "150"))
	staff := []uuid.UUID{}
	for i := 0; i < 4; i++ {
		member, _ := s.Hire("查询员工", fmt.Sprintf("%03d", 40+i), fmt.Sprintf("settled-q%d", i))
		staff = append(staff, member)
	}
	s.call("set_paid_item_staff", paidStaffInput(id(one["id"]), id(firstLine(one)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: staff[0]}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.employee}), 200)
	for i := 0; i < 4; i++ {
		s.call("set_paid_item_staff", paidStaffInput(id(many["id"]), id(firstLine(many)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: staff[i]}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.employee}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.employee}), 200)
	}
	counter := &paymentQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	counts := []int64{}
	for i, order := range []map[string]any{one, many} {
		counter.all.Store(0)
		result := app.HTTP(t, &caller, "get_order", orders.GetOrderInput{ID: id(order["id"])})
		if result.Status != 200 {
			t.Fatal(result)
		}
		if len(result.Body["staff_changes"].([]any)) != []int{1, 4}[i] {
			t.Fatal(result)
		}
		counts = append(counts, counter.all.Load())
	}
	if counts[0] != counts[1] {
		t.Fatal("staff change view query count grew with history", counts)
	}
}

func TestSetPaidItemStaffScopePermitsInvolvedStaff(t *testing.T) {
	s := open(t)
	d1, d2 := dayAt("2026-10-01"), dayAt("2026-10-02")
	s.SetNow(d1)
	role := s.call("create_role", map[string]any{"name": "本人员工改人", "permissions": []any{map[string]any{"permission": "order.edit", "scope": "self"}}}, 200)
	employee, token := s.Hire("本人在岗", "050", "settled-involved", id(role["id"]))
	line := orders.OrderItemInput{ItemID: s.item, Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: employee}, {Slot: 2, StaffID: s.Res.OwnerID}, {Slot: 3, StaffID: s.Res.OwnerID}}}
	o := s.paid(s.customerOrder(line), payment("cash", "100"))
	newStaff, _ := s.Hire("本人接替", "051", "settled-involved-new")
	s.SetNow(d2)
	caller := s.MustCaller(token)
	r := s.HTTP(t, &caller, "set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: newStaff}, orders.OrderItemStaffInput{Slot: 2, StaffID: s.Res.OwnerID}, orders.OrderItemStaffInput{Slot: 3, StaffID: s.Res.OwnerID}))
	if r.Status != 200 {
		t.Fatal(r)
	}
}
