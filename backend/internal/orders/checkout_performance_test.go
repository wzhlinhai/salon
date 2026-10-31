package orders_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) performanceSetting(key string, value any) {
	s.t.Helper()
	s.call("change_setting", map[string]any{"key": key, "value": value, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
}
func (s *fixture) performanceList() map[string]any {
	s.t.Helper()
	day := settings.Date(time.Now()).Format(time.DateOnly)
	return s.call("list_performance", map[string]any{"date_from": day, "date_to": day, "limit": 200}, 200)
}
func withoutSale(entries []any) []any {
	return slices.DeleteFunc(slices.Clone(entries), func(v any) bool {
		return v.(map[string]any)["kind"] == "sale"
	})
}

func (s *fixture) performanceLine(shares ...any) orders.OrderItemInput {
	s.t.Helper()
	in := s.itemInput("业绩"+uuid.NewString(), 0)
	in["level_prices"] = []any{}
	ps := []any{}
	for _, v := range shares {
		ps = append(ps, map[string]any{"position_id": s.position, "required": false, "share_percent": v})
	}
	in["positions"] = ps
	item := s.call("create_item", in, 200)
	assignments := []orders.OrderItemStaffInput{}
	for i := range shares {
		staff := s.Res.OwnerID
		if i > 0 {
			staff = s.employee
		}
		assignments = append(assignments, orders.OrderItemStaffInput{Slot: int16(i + 1), StaffID: staff})
	}
	return orders.OrderItemInput{ItemID: id(item["id"]), Staff: assignments}
}

func TestCheckoutPerformanceMixed(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_ratio_card_bonus", 100)
	s.performanceSetting("performance_ratio_red_packet", 100)
	line := s.performanceLine(70, 30)
	card := s.paymentCard(s.paymentType("业绩卡", nil), "30", "70")
	packet := s.redPacket(s.customer, "40")
	o := s.customerOrder(line, line)
	s.paid(o, payment("cash", "20"), cardPayment(card, "100"), packetPayment(packet, "40"), payment("debt", "40"))
	list := s.performanceList()
	if list["totals"].([]any)[0].(map[string]any)["paid_amount"] != "200.00" || list["totals"].([]any)[0].(map[string]any)["amount"] != "200.00" {
		t.Fatal(list)
	}
	entries := withoutSale(list["entries"].([]any))
	if len(entries) != 20 {
		t.Fatal(entries)
	}
	want := map[string][]string{"cash": {"7.00", "3.00"}, "card_principal": {"10.50", "4.50"}, "card_bonus": {"24.50", "10.50"}, "red_packet": {"14.00", "6.00"}, "debt": {"14.00", "6.00"}}
	perLine := map[string]int{}
	for _, v := range entries {
		e := v.(map[string]any)
		slot := int(e["slot"].(float64))
		if e["paid_amount"] != want[e["method"].(string)][slot-1] || e["amount"] != e["paid_amount"] || e["checkout_id"] == nil || e["settings_snapshot_id"] == nil || e["reversal"] != false || e["kind"] != "item" {
			t.Fatal(e)
		}
		perLine[e["order_item_id"].(string)]++
	}
	if len(perLine) != 2 {
		t.Fatal(perLine)
	}
	for _, n := range perLine {
		if n != 10 {
			t.Fatal(perLine)
		}
	}
	// Repaying debt cannot add performance.
	debt := s.call("list_customer_debts", map[string]any{"customer_id": s.customer}, 200)["items"].([]any)[0].(map[string]any)
	s.call("repay_debt", map[string]any{"debt_id": debt["id"], "amount": "1", "method": "cash"}, 200)
	if !reflect.DeepEqual(list, s.performanceList()) {
		t.Fatal("repayment added performance")
	}
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries")
	r := s.HTTPWithToken(t, s.Owner, "void_order", voidInput(o))
	if r.Status != 409 || r.Detail() != "这张单的挂账已有还款，不能撤单" {
		t.Fatal(r)
	}
	if before != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries") {
		t.Fatal("refused void wrote reversal")
	}
}

func TestCheckoutPerformanceExclusions(t *testing.T) {
	s := open(t)
	line := s.performanceLine(nil, nil)
	counted := s.checkoutLine()
	card := s.countCard(counted, "count", 1, 0)
	o := s.customerOrder(line, counted, line)
	items := lines(o)
	removed := items[2].(map[string]any)
	s.call("remove_order_item", orders.RemoveOrderItemInput{OrderID: id(o["id"]), OrderItemID: id(removed["id"])}, 200)
	s.paid(o, payment("cash", "100"), countPayment(card, items[1].(map[string]any), "100"))
	day := settings.Date(time.Now()).Format(time.DateOnly)
	list := s.call("list_performance", map[string]any{"date_from": day, "date_to": day, "limit": 200, "kind": "item"}, 200)
	entries := list["entries"].([]any)
	if len(entries) != 2 || list["totals"].([]any)[0].(map[string]any)["paid_amount"] != "100.00" {
		t.Fatal(list)
	}
	for _, v := range entries {
		e := v.(map[string]any)
		if e["order_item_id"] != items[0].(map[string]any)["id"] || e["paid_amount"] != "50.00" {
			t.Fatal(e)
		}
	}
}

func TestCheckoutPerformanceSnapshots(t *testing.T) {
	s := open(t)
	line := s.performanceLine(70, 30)
	a := s.customerOrder(line)
	s.performanceSetting("performance_ratio_cash", 20)
	s.performanceSetting("performance_split", "equal")
	b := s.customerOrder(line)
	// Changing project shares must preserve the already-added line.
	edit := s.itemInput("edited", 0)
	edit["id"] = line.ItemID
	edit["active"] = true
	edit["level_prices"] = []any{}
	edit["positions"] = []any{map[string]any{"position_id": s.position, "required": false, "share_percent": 40}, map[string]any{"position_id": s.position, "required": false, "share_percent": 60}}
	s.call("update_item", edit, 200)
	s.call("checkout_orders", checkoutInput(checkoutOrder(a, payment("cash", "100")), checkoutOrder(b, payment("cash", "100"))), 200)
	entries := s.performanceList()["entries"].([]any)
	if len(entries) != 4 {
		t.Fatal(entries)
	}
	for _, v := range entries {
		e := v.(map[string]any)
		isA := e["order_id"] == a["id"]
		wantPaid, wantAmount, wantRatio := "50.00", "10.00", float64(20)
		if isA {
			wantRatio = 100
			wantPaid = "70.00"
			if e["slot"] == float64(2) {
				wantPaid = "30.00"
			}
			wantAmount = wantPaid
		}
		if e["paid_amount"] != wantPaid || e["amount"] != wantAmount || e["ratio"] != wantRatio {
			t.Fatal(e)
		}
	}
}

func TestPerformanceRatioCategories(t *testing.T) {
	s := open(t)
	methods := []string{"cash", "unionpay", "wechat", "alipay", "debt", "red_packet", "card_principal", "card_bonus"}
	for i, m := range methods {
		s.performanceSetting("performance_ratio_"+m, (i+1)*10)
	}
	line := s.checkoutLine()
	c := s.paymentCard(s.paymentType("比例卡", nil), "10", "10")
	p := s.redPacket(s.customer, "10")
	o := s.customerOrder(line)
	s.paid(o, payment("cash", "10"), payment("unionpay", "10"), payment("wechat", "10"), payment("alipay", "10"), payment("debt", "30"), packetPayment(p, "10"), cardPayment(c, "20"))
	entries := withoutSale(s.performanceList()["entries"].([]any))
	if len(entries) != 8 {
		t.Fatal(entries)
	}
	for _, v := range entries {
		e := v.(map[string]any)
		for i, m := range methods {
			if e["method"] == m {
				paid := 10
				if m == "debt" {
					paid = 30
				}
				want := fmt.Sprintf("%.2f", float64(paid*(i+1))/10)
				if e["ratio"] != float64((i+1)*10) || e["amount"] != want {
					t.Fatal(e, want)
				}
			}
		}
	}
}

func TestPerformanceCheckoutBusinessDate(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	o := s.customerOrder(line)
	day := settings.Date(time.Now()).AddDate(0, 0, 5)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.CheckoutOrdersAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, new(checkoutInput(checkoutOrder(o, payment("cash", "100")))), at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var got string
	if err := tx.QueryRow(context.Background(), "SELECT business_date::text FROM performance_entries WHERE order_id=$1", o["id"]).Scan(&got); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if got != day.Format(time.DateOnly) {
		t.Fatal(got, day)
	}
}
