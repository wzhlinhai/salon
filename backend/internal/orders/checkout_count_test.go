package orders_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) countCard(line orders.OrderItemInput, kind string, paid, gift int) map[string]any {
	s.t.Helper()
	ct := s.call("create_card_type", map[string]any{"name": "剪发卡" + uuid.NewString(), "kind": kind, "validity_kind": "none", "items": []any{map[string]any{"item_id": line.ItemID, "paid_count": paid, "gift_count": gift, "unit_price": "100"}}}, 200)
	principal := "0"
	if kind == "package" {
		principal = "1000"
	}
	return s.paymentCard(ct, principal, "0")
}
func countPayment(c map[string]any, line map[string]any, amount string) orders.PaymentInput {
	p := payment("count", amount)
	p.CardID = new(id(c["id"]))
	p.OrderItemID = new(id(line["id"]))
	return p
}
func (s *fixture) countRefused(caller ops.Caller, in orders.CheckoutOrdersInput, status int, detail string) {
	s.t.Helper()
	before := s.cardSnapshot()
	r := s.HTTP(s.t, &caller, "checkout_orders", in)
	if r.Status != status || detail != "" && r.Body["detail"] != detail || before != s.cardSnapshot() {
		s.t.Fatal(r, "wrong refusal or wrote data")
	}
}
func (s *fixture) countEntries(c map[string]any) []any {
	return s.call("list_card_counts", map[string]any{"card_id": c["id"]}, 200)["entries"].([]any)
}
func TestCountCheckout(t *testing.T) {
	for _, order := range []string{"paid_first", "gift_first"} {
		t.Run(order, func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_count_order", order)
			line := s.checkoutLine()
			c := s.countCard(line, "count", 2, 1)
			for i := 0; i < 3; i++ {
				o := s.customerOrder(line)
				after := s.paid(o, countPayment(c, firstLine(o), "100"))
				paid, gift := float64(1), float64(0)
				if order == "paid_first" && i == 2 || order == "gift_first" && i == 0 {
					paid, gift = 0, 1
				}
				pc := after["payments"].([]any)[0].(map[string]any)
				want := map[string]any{"card_id": c["id"], "order_item_id": firstLine(o)["id"], "item_id": line.ItemID.String(), "paid": paid, "gift": gift}
				if !reflect.DeepEqual(pc["count"], want) || pc["card"] != nil || after["net"] != "100.00" {
					t.Fatal(after, want)
				}
				entries := s.countEntries(c)
				l := entries[0].(map[string]any)
				if len(entries) != i+2 || l["kind"] != "consume" || l["order_id"] != o["id"] || l["paid_delta"] != -paid || l["gift_delta"] != -gift || l["business_date"] != settings.Date(time.Now()).Format(time.DateOnly) {
					t.Fatal(entries)
				}
				expectedPaid, expectedGift := float64(2), float64(1)
				if i > 0 {
					prev := entries[1].(map[string]any)
					expectedPaid, expectedGift = prev["paid_after"].(float64), prev["gift_after"].(float64)
				}
				if l["paid_before"] != expectedPaid || l["gift_before"] != expectedGift || l["paid_after"] != expectedPaid-paid || l["gift_after"] != expectedGift-gift {
					t.Fatal(l)
				}
				audits := s.call("list_audit_log", map[string]any{"target_id": c["id"]}, 200)["entries"].([]any)
				audit := audits[0].(map[string]any)
				beforeCounts := audit["before"].(map[string]any)["items"].([]any)[0].(map[string]any)
				afterCounts := audit["after"].(map[string]any)["items"].([]any)[0].(map[string]any)
				if len(audits) != i+2 || audit["action"] != "checkout_orders" || beforeCounts["paid_remaining"] != expectedPaid || beforeCounts["gift_remaining"] != expectedGift || afterCounts["paid_remaining"] != expectedPaid-paid || afterCounts["gift_remaining"] != expectedGift-gift {
					t.Fatal(audits)
				}
				s.assertEditAudit("checkout_orders", o, after)
				for _, ch := range s.Channels() {
					got := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(o["id"])})
					if got.Status != 200 || !reflect.DeepEqual(got.Body, after) {
						t.Fatal(ch.Name, got)
					}
					got = ch.Call(t, s.MustCaller(s.Owner), "list_card_counts", map[string]any{"card_id": c["id"]})
					if got.Status != 200 || !reflect.DeepEqual(got.Body["entries"], entries) {
						t.Fatal(ch.Name, got)
					}
				}
			}
			o := s.customerOrder(line)
			s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(o, countPayment(c, firstLine(o), "100"))), 409, "次数不足")
		})
	}
}
func TestCountCheckoutChained(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "package", 2, 1)
	a, b := s.customerOrder(line, line, line), s.customerOrder(line, line)
	al, bl := a["items"].([]any), b["items"].([]any)
	result := s.call("checkout_orders", checkoutInput(checkoutOrder(a, countPayment(c, al[0].(map[string]any), "100"), countPayment(c, al[1].(map[string]any), "100"), cardPayment(c, "60"), payment("cash", "40")), checkoutOrder(b, countPayment(c, bl[0].(map[string]any), "100"), payment("cash", "100"))), 200)
	entries := s.countEntries(c)
	if len(lines(result)) != 2 || len(entries) != 4 {
		t.Fatal(result, entries)
	}
	for i, want := range [][4]float64{{0, 0, 1, 0}, {1, 0, 1, 1}, {2, 1, 1, 1}} {
		l := entries[i].(map[string]any)
		if l["paid_before"] != want[0] || l["paid_after"] != want[1] || l["gift_before"] != want[2] || l["gift_after"] != want[3] {
			t.Fatal(entries)
		}
	}
	audits := s.call("list_audit_log", map[string]any{"target_id": c["id"]}, 200)["entries"].([]any)
	if len(audits) != 2 || audits[0].(map[string]any)["after"].(map[string]any)["principal"] != "940.00" {
		t.Fatal(audits)
	}
}
func TestCountCheckoutInputRules(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line)
	p := countPayment(c, firstLine(o), "100")
	for _, tc := range []struct {
		name, detail string
		ps           []orders.PaymentInput
	}{
		{"missing line", "扣次要选项目行", []orders.PaymentInput{{Method: "count", CardID: p.CardID, Amount: p.Amount}}},
		{"missing card", "扣次要选卡", []orders.PaymentInput{{Method: "count", OrderItemID: p.OrderItemID, Amount: p.Amount}}},
		{"cash line", "只有扣次要选项目行", []orders.PaymentInput{{Method: "cash", OrderItemID: p.OrderItemID, Amount: p.Amount}}},
		{"cash card", "只有卡余额支付和扣次要选卡", []orders.PaymentInput{{Method: "cash", CardID: p.CardID, Amount: p.Amount}}},
		{"duplicate line", "同一项目行不能重复扣次", []orders.PaymentInput{p, p}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.countRefused(s.MustCaller(s.Owner), checkoutInput(orders.CheckoutOrderInput{OrderID: uuid.New(), Payments: tc.ps}), 409, tc.detail)
		})
	}
	other := s.customerOrder(line)
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(o, countPayment(c, firstLine(other), "100"))), 409, "扣次的项目行不在这张单上")
	removed := s.customerOrder(line, line)
	removed = s.edit("remove_order_item", editInput(removed), 200)
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(removed, countPayment(c, firstLine(removed), "100"))), 409, "扣次的项目行不在这张单上")
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(o, countPayment(c, firstLine(o), "99"), payment("cash", "1"))), 409, "扣次金额须等于这一行的实收")
	missingItem := s.call("create_item", s.itemInput("卡外项目", 0), 200)
	missing := s.customerOrder(orders.OrderItemInput{ItemID: id(missingItem["id"]), Staff: []orders.OrderItemStaffInput{}})
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(missing, countPayment(c, firstLine(missing), "100"))), 409, "卡上没有这个项目")
}
func TestCountCheckoutPricing(t *testing.T) {
	s := open(t)
	b := s.paymentType("计价卡B", "88")
	cb := s.paymentCard(b, "1000", "0")
	input := s.itemInput("会员项目", 0)
	input["member_prices"] = []any{map[string]any{"card_type_id": b["id"], "price": "70"}}
	item := s.call("create_item", input, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
	ca := s.countCard(line, "package", 3, 0)
	s.call("update_card_type", map[string]any{"id": ca["card_type_id"], "name": "折扣次套餐A", "kind": "package", "validity_kind": "none", "discount_percent": "90", "active": true, "items": []any{map[string]any{"item_id": line.ItemID, "paid_count": 3, "gift_count": 0, "unit_price": "100"}}}, 200)
	input["id"], input["active"] = item["id"], true
	input["member_prices"] = []any{map[string]any{"card_type_id": b["id"], "price": "70"}, map[string]any{"card_type_id": ca["card_type_id"], "price": "60"}}
	s.call("update_item", input, 200)
	for _, withBalance := range []bool{false, true} {
		o := s.customerOrder(line, line)
		ps := []orders.PaymentInput{countPayment(ca, firstLine(o), "100"), countPayment(ca, o["items"].([]any)[1].(map[string]any), "100")}
		if withBalance {
			ps = []orders.PaymentInput{countPayment(ca, firstLine(o), "70"), cardPayment(cb, "70")}
		}
		after := s.paid(o, ps...)
		wantPrice, wantSource := "100.00", "standard"
		if withBalance {
			wantPrice, wantSource = "70.00", "member"
		}
		if firstLine(after)["price"] != wantPrice || firstLine(after)["price_source"] != wantSource || withBalance && firstLine(after)["card_type_id"] != b["id"] {
			t.Fatal(after)
		}
	}
	discountLine := s.checkoutLine()
	cc := s.countCard(discountLine, "count", 1, 0)
	o := s.customerOrder(discountLine, discountLine)
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(o, countPayment(cc, firstLine(o), "100"), cardPayment(cb, "76"))), 409, "扣次金额须等于这一行的实收")
	after := s.paid(o, countPayment(cc, firstLine(o), "88"), cardPayment(cb, "88"))
	if after["net"] != "176.00" || firstLine(after)["discount"] != "12.00" {
		t.Fatal(after)
	}
}
func TestCountCheckoutCardChecks(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line)
	caller := s.MustCaller(s.Owner)
	own := checkoutInput(checkoutOrder(o, countPayment(c, firstLine(o), "100")))
	other := s.call("open_order", s.opening(line), 200)
	s.countRefused(caller, checkoutInput(checkoutOrder(other, countPayment(c, firstLine(other), "100"))), 409, "不能用别人的卡")
	scoped := s.MustCaller(s.Owner)
	scoped.StaffID = s.employee
	scoped.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	s.countRefused(scoped, own, 404, "")
	for _, ch := range s.Channels() {
		before := s.cardSnapshot()
		r := ch.Call(t, scoped, "list_card_counts", map[string]any{"card_id": c["id"]})
		if r.Status != 404 || before != s.cardSnapshot() {
			t.Fatal(ch.Name, r)
		}
		r = ch.Call(t, scoped, "list_audit_log", map[string]any{"target_id": c["id"]})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
			t.Fatal(ch.Name, r)
		}
	}
	s.call("adjust_card", map[string]any{"card_id": c["id"], "expiry": map[string]any{"expires_on": settings.Date(time.Now()).AddDate(0, 0, -1).Format(time.DateOnly)}, "reason": "过期"}, 200)
	s.countRefused(caller, own, 409, "卡已过期")
	s.call("adjust_card", map[string]any{"card_id": c["id"], "expiry": map[string]any{"expires_on": settings.Date(time.Now()).Format(time.DateOnly)}, "reason": "当天有效"}, 200)
	s.paid(o, countPayment(c, firstLine(o), "100"))
	s.paymentSetting("others_card_payment", "allowed")
	allowed := s.call("open_order", s.opening(line), 200)
	s.paymentSetting("others_card_payment", "not_allowed")
	s.paid(allowed, countPayment(c, firstLine(allowed), "100"))
}
func TestCountCheckoutSnapshot(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line)
	s.paymentSetting("card_count_order", "gift_first")
	after := s.paid(o, countPayment(c, firstLine(o), "100"))
	pc := after["payments"].([]any)[0].(map[string]any)["count"].(map[string]any)
	if pc["paid"] != float64(1) || pc["gift"] != float64(0) {
		t.Fatal(after)
	}
}
func TestCountCheckoutWaits(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", c["id"])
	dbtest.Exec(t, tx, `INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id) VALUES ($1,$2,$3,$4,'consume',2,-1,1,1,0,1,$5,$6)`, s.Res.TenantID, s.Res.StoreID, c["id"], line.ItemID, settings.Date(time.Now()), s.Res.OwnerID)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, countPayment(c, firstLine(o), "100"))))
	}()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	optest.WaitResult(t, done, 200)
	l := s.countEntries(c)[0].(map[string]any)
	if l["paid_before"] != float64(1) || l["paid_after"] != float64(0) {
		t.Fatal(l)
	}
}
func TestCountCheckoutAI(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line)
	before := s.cardSnapshot()
	r := s.AI(t, s.MustCaller(s.Owner), "checkout_orders", checkoutInput(checkoutOrder(o, countPayment(c, firstLine(o), "100"))))
	if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) || before != s.cardSnapshot() {
		t.Fatal(r)
	}
}
func TestCountConsumeBusinessDate(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line)
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	caller := s.MustCaller(s.Owner)
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.CheckoutOrdersAtForTest(context.Background(), ops.Env{Caller: caller, Q: q}, new(checkoutInput(checkoutOrder(o, countPayment(c, firstLine(o), "100")))), at)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if l := s.countEntries(c)[0].(map[string]any); l["business_date"] != day.Format(time.DateOnly) {
		t.Fatal(l)
	}
}

func TestCountOnlyDoesNotLockItems(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 1, 0)
	o := s.customerOrder(line)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", line.ItemID)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, countPayment(c, firstLine(o), "100"))))
	}()
	r := optest.WaitResult(t, done, 200)
	dbtest.Rollback(t, tx)
	if firstLine(lines(r.Body)[0].(map[string]any))["price_source"] != "standard" {
		t.Fatal(r)
	}
}

func TestCountCheckoutRollbackChained(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 1, 0)
	a, b := s.customerOrder(line), s.customerOrder(line)
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(a, countPayment(c, firstLine(a), "100")), checkoutOrder(b, countPayment(c, firstLine(b), "100"))), 409, "次数不足")
}

func TestCountOrderPaymentViewQueryCount(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 11, 0)
	one, many := s.customerOrder(line), s.customerOrder(line, line, line, line, line, line, line, line, line, line)
	for _, o := range []map[string]any{one, many} {
		ps := []orders.PaymentInput{}
		for _, l := range o["items"].([]any) {
			ps = append(ps, countPayment(c, l.(map[string]any), "100"))
		}
		s.paid(o, ps...)
	}
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
	caller := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		counts := []int64{}
		for i, o := range []map[string]any{one, many} {
			counter.all.Store(0)
			counter.payments.Store(0)
			result := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(o["id"])})
			if result.Status != 200 || len(result.Body["payments"].([]any)) != []int{1, 10}[i] || counter.payments.Load() != 1 {
				t.Fatal(ch.Name, result, counter.payments.Load())
			}
			counts = append(counts, counter.all.Load())
			for _, p := range result.Body["payments"].([]any) {
				if p.(map[string]any)["count"] == nil {
					t.Fatal(result)
				}
			}
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, "SQL query count grew with count payments", counts)
		}
	}
}
