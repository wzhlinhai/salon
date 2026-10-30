package orders_test

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) laborLine(fixed, percent any) orders.OrderItemInput {
	s.t.Helper()
	input := s.itemInput("手工费"+uuid.NewString(), 0)
	input["level_prices"] = []any{}
	input["labor_fee_amount"], input["labor_fee_percent"] = fixed, percent
	input["positions"] = []any{map[string]any{"position_id": s.position, "required": false, "share_percent": 70}, map[string]any{"position_id": s.position, "required": false, "share_percent": 30}}
	item := s.call("create_item", input, 200)
	return orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}, {Slot: 2, StaffID: s.employee}}}
}

func TestCheckoutConsumeLabor(t *testing.T) {
	for _, kind := range []string{"count", "package"} {
		for _, order := range []string{"paid_first", "gift_first"} {
			t.Run(kind+"/"+order, func(t *testing.T) {
				s := open(t)
				s.performanceSetting("card_count_order", order)
				cash, counted := s.laborLine("10.01", nil), s.laborLine("8.03", nil)
				c := s.countCard(counted, kind, 2, 2)
				s.call("update_card_type", map[string]any{"id": c["card_type_id"], "name": "新单次金额", "kind": kind, "validity_kind": "none", "active": true, "items": []any{map[string]any{"item_id": counted.ItemID, "paid_count": 2, "gift_count": 2, "unit_price": "17"}}}, 200)
				nobody := counted
				nobody.Staff = []orders.OrderItemStaffInput{}
				o := s.customerOrder(cash, counted, nobody, cash)
				ls := lines(o)
				s.call("remove_order_item", orders.RemoveOrderItemInput{OrderID: id(o["id"]), OrderItemID: id(ls[3].(map[string]any)["id"])}, 200)
				// Later catalogue edits and settings do not change the retained line or rule snapshot.
				edit := s.itemInput("手工费改动", 0)
				edit["id"] = counted.ItemID
				edit["active"] = true
				edit["labor_fee_amount"] = "99"
				s.call("update_item", edit, 200)
				s.call("reprice_order_item", map[string]any{"order_id": o["id"], "order_item_id": ls[1].(map[string]any)["id"], "price": "80", "reason": "耗卡基数独立"}, 200)
				s.performanceSetting(settings.KeyPerformanceRatioCountPaid, 13)
				s.performanceSetting(settings.KeyPerformanceRatioCountGift, 27)
				after := s.paid(o, payment("cash", "200"), countPayment(c, ls[1].(map[string]any), "80"))
				if lines(after)[1].(map[string]any)["labor_fee_amount"] != "8.03" {
					t.Fatal(after)
				}
				list := s.performanceList()
				entries := list["entries"].([]any)
				if len(entries) != 9 {
					t.Fatal(list)
				}
				sums := map[string]int64{}
				for _, v := range entries {
					e := v.(map[string]any)
					line := e["order_item_id"]
					k := e["kind"].(string)
					if line == ls[3].(map[string]any)["id"] {
						t.Fatal("removed line", e)
					}
					if line == ls[1].(map[string]any)["id"] && k != "consume" && k != "labor" {
						t.Fatal("count line earned item/sale performance", e)
					}
					if k == "labor" {
						if e["staff"] == nil || e["method"] != "labor" || e["ratio"] != float64(100) || e["paid_amount"] != e["amount"] {
							t.Fatal(e)
						}
						want := "7.01"
						if e["slot"] == float64(2) {
							want = "3.00"
						}
						if line == ls[1].(map[string]any)["id"] {
							want = "5.63"
							if e["slot"] == float64(2) {
								want = "2.40"
							}
						}
						if e["amount"] != want {
							t.Fatal(e, want)
						}
					}
					if k == "consume" {
						method, ratio, amt := "count_paid", float64(100), e["paid_amount"]
						if order == "gift_first" {
							method, ratio, amt = "count_gift", 0, "0.00"
						}
						want := "70.00"
						if e["slot"] == float64(2) {
							want = "30.00"
						}
						if e["method"] != method || e["ratio"] != ratio || e["amount"] != amt || e["paid_amount"] != want {
							t.Fatal(e)
						}
					}
					sums[k]++
				}
				if sums["item"] != 3 || sums["consume"] != 2 || sums["labor"] != 4 {
					t.Fatal(sums)
				}
				totals := list["totals"].([]any)
				if totals[0].(map[string]any)["paid_amount"] != "200.00" || totals[1].(map[string]any)["paid_amount"] != "100.00" || totals[2].(map[string]any)["amount"] != "18.04" {
					t.Fatal(totals)
				}
			})
		}
	}
}

func TestCheckoutLaborPercentPrice(t *testing.T) {
	s := open(t)
	ct := s.paymentType("手工会员卡", "80")
	c := s.paymentCard(ct, "1000", "0")
	member, discounted := s.laborLine(nil, "12.50"), s.laborLine(nil, "12.50")
	edit := s.itemInput("手工会员项目", 0)
	edit["id"] = member.ItemID
	edit["active"] = true
	edit["labor_fee_percent"] = "12.50"
	edit["level_prices"] = []any{}
	edit["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "60.04"}}
	edit["positions"] = []any{map[string]any{"position_id": s.position, "required": false, "share_percent": 70}, map[string]any{"position_id": s.position, "required": false, "share_percent": 30}}
	s.call("update_item", edit, 200)
	o := s.customerOrder(member, discounted)
	edit["labor_fee_percent"] = "99"
	s.call("update_item", edit, 200)
	if firstLine(o)["labor_fee_percent"] != "12.50" {
		t.Fatal(o)
	}
	after := s.paid(o, cardPayment(c, "140.04"))
	if firstLine(after)["price"] != "60.04" || lines(after)[1].(map[string]any)["discount"] != "20.00" {
		t.Fatal(after)
	}
	totals := map[any]string{}
	for _, v := range s.performanceList()["entries"].([]any) {
		e := v.(map[string]any)
		if e["kind"] != "labor" {
			continue
		}
		want := []string{"5.26", "2.25"}
		if e["order_item_id"] != firstLine(o)["id"] {
			want = []string{"8.75", "3.75"}
		}
		if e["amount"] != want[int(e["slot"].(float64))-1] {
			t.Fatal(e)
		}
		totals[e["order_item_id"]] = e["kind"].(string)
	}
	if len(totals) != 2 {
		t.Fatal(totals)
	}
}

func TestCountRatioSnapshot(t *testing.T) {
	s := open(t)
	line := s.laborLine(nil, nil)
	c := s.countCard(line, "count", 2, 2)
	s.performanceSetting(settings.KeyPerformanceRatioCountPaid, 23)
	s.performanceSetting(settings.KeyPerformanceRatioCountGift, 47)
	a := s.customerOrder(line)
	s.performanceSetting("card_count_order", "gift_first")
	b := s.customerOrder(line)
	s.performanceSetting(settings.KeyPerformanceRatioCountPaid, 0)
	s.performanceSetting(settings.KeyPerformanceRatioCountGift, 0)
	for _, o := range []map[string]any{a, b} {
		s.paid(o, countPayment(c, firstLine(o), "100"))
	}
	for _, v := range s.performanceList()["entries"].([]any) {
		e := v.(map[string]any)
		ratio := float64(23)
		want := []string{"16.10", "6.90"}
		if e["order_id"] == b["id"] {
			ratio = 47
			want = []string{"32.90", "14.10"}
		}
		if e["kind"] != "consume" || e["ratio"] != ratio || e["amount"] != want[int(e["slot"].(float64))-1] {
			t.Fatal(e)
		}
	}
}

func TestConsumeLaborRead(t *testing.T) {
	s := open(t)
	staff, token := s.Hire("手工员工", "097", "labor-reader", s.Role("发型师"))
	line := s.laborLine("10", nil)
	line.Staff[0].StaffID = staff
	c := s.countCard(line, "count", 3, 0)
	o := s.customerOrder(line)
	s.paid(o, countPayment(c, firstLine(o), "100"))
	empty := line
	empty.Staff = []orders.OrderItemStaffInput{}
	o = s.customerOrder(empty)
	s.paid(o, countPayment(c, firstLine(o), "100"))
	day := settings.Date(time.Now()).Format(time.DateOnly)
	for _, ch := range s.Channels() {
		for _, caller := range []string{s.Owner, token} {
			for _, kind := range []string{"item", "consume", "labor"} {
				r := ch.Call(t, s.MustCaller(caller), "list_performance", map[string]any{"date_from": day, "date_to": day, "kind": kind})
				if r.Status != 200 {
					t.Fatal(r)
				}
				totals := r.Body["totals"].([]any)
				if len(totals) != 3 {
					t.Fatal(r)
				}
				for i, k := range []string{"item", "consume", "labor"} {
					total := totals[i].(map[string]any)
					if total["kind"] != k || (k != kind || k == "item") && (total["amount"] != "0" || total["paid_amount"] != "0") {
						t.Fatal(r)
					}
				}
				n := map[string]int{"item": 0, "consume": 3, "labor": 2}[kind]
				paid := map[string]string{"item": "0", "consume": "200.00", "labor": "10.00"}[kind]
				if caller == token {
					n = 1
					paid = map[string]string{"item": "0", "consume": "70.00", "labor": "7.00"}[kind]
					if kind == "item" {
						n = 0
					}
				}
				if len(r.Body["entries"].([]any)) != n {
					t.Fatal(r)
				}
				for i, k := range []string{"item", "consume", "labor"} {
					if k == kind && totals[i].(map[string]any)["paid_amount"] != paid {
						t.Fatal(r)
					}
				}
				for _, v := range r.Body["entries"].([]any) {
					e := v.(map[string]any)
					if e["kind"] != kind || caller == token && e["staff"] == nil {
						t.Fatal(r)
					}
				}
			}
		}
	}
}

func TestConsumeLaborQueryCount(t *testing.T) {
	s := open(t)
	line := s.laborLine("10", nil)
	c := s.countCard(line, "count", 11, 0)
	counter := &paymentQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	day := settings.Date(time.Now()).Format(time.DateOnly)
	for i, ch := range app.Channels() {
		staffID := s.Res.OwnerID
		if i > 0 {
			staffID = s.employee
		}
		line.Staff = []orders.OrderItemStaffInput{{Slot: 1, StaffID: staffID}}
		counts := []int64{}
		for _, tc := range []struct{ add, want int }{{1, 1}, {4, 5}} {
			for range tc.add {
				o := s.customerOrder(line)
				s.paid(o, countPayment(c, firstLine(o), "100"))
			}
			counter.all.Store(0)
			r := ch.Call(t, s.MustCaller(s.Owner), "list_performance", map[string]any{"date_from": day, "date_to": day, "kind": "consume", "staff_id": staffID})
			if r.Status != 200 || len(r.Body["entries"].([]any)) != tc.want {
				t.Fatal(r)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal("queries grew", counts)
		}
	}
}

func TestConsumeUnitPriceJoin(t *testing.T) {
	s := open(t)
	x, y := s.laborLine(nil, nil), s.checkoutLine()
	x.Staff = x.Staff[:1]
	ct := s.call("create_card_type", map[string]any{"name": "扣次关联卡", "kind": "count", "validity_kind": "none", "items": []any{
		map[string]any{"item_id": x.ItemID, "paid_count": 1, "gift_count": 0, "unit_price": "100"},
		map[string]any{"item_id": y.ItemID, "paid_count": 1, "gift_count": 0, "unit_price": "17"},
	}}, 200)
	paying := s.paymentCard(ct, "0", "0")
	otherType := s.call("create_card_type", map[string]any{"name": "同客其他卡", "kind": "count", "validity_kind": "none", "items": []any{
		map[string]any{"item_id": x.ItemID, "paid_count": 1, "gift_count": 0, "unit_price": "45"},
	}}, 200)
	s.paymentCard(otherType, "0", "0")
	o := s.customerOrder(x)
	after := s.paid(o, countPayment(paying, firstLine(o), "100"))
	if len(after["payments"].([]any)) != 1 {
		t.Fatal(after)
	}
	list := s.performanceList()
	entries := list["entries"].([]any)
	if len(entries) != 1 {
		t.Fatal(list)
	}
	e := entries[0].(map[string]any)
	if e["kind"] != "consume" || e["order_item_id"] != firstLine(o)["id"] || e["method"] != "count_paid" || e["paid_amount"] != "100.00" || e["amount"] != "100.00" {
		t.Fatal(e)
	}
}
