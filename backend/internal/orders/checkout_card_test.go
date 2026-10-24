package orders_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) cardSnapshot() string {
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "checkouts", "order_payments", "orders", "order_items", "order_item_positions", "cards", "card_ledger", "card_points_ledger", "audit_log", "customers", "settings_snapshots")
}
func (s *fixture) paymentType(name string, discount any) map[string]any {
	return s.call("create_card_type", map[string]any{"name": name, "kind": "stored_value", "validity_kind": "none", "discount_percent": discount}, 200)
}
func (s *fixture) paymentCard(ct map[string]any, principal, bonus string) map[string]any {
	return s.call("open_card", map[string]any{"customer_id": s.customer, "card_type_id": ct["id"], "principal": principal, "bonus": bonus, "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}}, 200)
}
func cardPayment(c map[string]any, amount string) orders.PaymentInput {
	p := payment("card", amount)
	p.CardID = new(id(c["id"]))
	return p
}
func (s *fixture) paymentSetting(key, value string) {
	s.call("change_setting", map[string]any{"key": key, "value": value, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
}
func (s *fixture) paid(o map[string]any, ps ...orders.PaymentInput) map[string]any {
	return lines(s.call("checkout_orders", checkoutInput(checkoutOrder(o, ps...)), 200))[0].(map[string]any)
}
func (s *fixture) cardRefused(caller ops.Caller, o map[string]any, p orders.PaymentInput, status int, detail string) {
	s.t.Helper()
	before := s.cardSnapshot()
	r := s.HTTP(s.t, &caller, "checkout_orders", checkoutInput(checkoutOrder(o, p)))
	if r.Status != status || detail != "" && r.Body["detail"] != detail || before != s.cardSnapshot() {
		s.t.Fatal(r, "wrong refusal or wrote data")
	}
	for _, ch := range s.Channels() {
		r = ch.Call(s.t, caller, "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), CardID: p.CardID})
		if r.Status != status || detail != "" && (ch.Name == "http" && r.Body["detail"] != detail || ch.Name == "ai" && (r.Err == nil || r.Err.Error() != detail)) || before != s.cardSnapshot() {
			s.t.Fatal(ch.Name, r)
		}
	}
}
func (s *fixture) consumeEntries(c map[string]any) []any {
	return s.call("list_card_ledger", map[string]any{"card_id": c["id"]}, 200)["entries"].([]any)
}
func TestCardCheckoutBalances(t *testing.T) {
	for _, order := range []string{"principal_first", "bonus_first"} {
		t.Run(order, func(t *testing.T) {
			s := open(t)
			s.call("change_setting", map[string]any{"key": "points_per_recharge_yuan", "value": 1, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
			s.paymentSetting("card_deduction_order", order)
			c := s.paymentCard(s.paymentType("余额卡", nil), "80", "50")
			o := s.customerOrder(s.checkoutLine())
			after := s.paid(o, cardPayment(c, "100"))
			wantP, wantB, afterP, afterB := "80.00", "20.00", "0.00", "30.00"
			if order == "bonus_first" {
				wantP, wantB, afterP, afterB = "50.00", "50.00", "30.00", "0.00"
			}
			pc := after["payments"].([]any)[0].(map[string]any)["card"].(map[string]any)
			if pc["card_id"] != c["id"] || pc["principal"] != wantP || pc["bonus"] != wantB {
				t.Fatal(after)
			}
			entries := s.consumeEntries(c)
			l := entries[0].(map[string]any)
			if len(entries) != 2 || l["kind"] != "consume" || l["principal_delta"] != "-"+wantP || l["bonus_delta"] != "-"+wantB || l["principal_after"] != afterP || l["bonus_after"] != afterB || l["order_id"] != o["id"] || l["business_date"] != settings.Date(time.Now()).Format(time.DateOnly) {
				t.Fatal(entries)
			}
			audits := s.call("list_audit_log", map[string]any{"target_id": c["id"]}, 200)["entries"].([]any)
			a := audits[0].(map[string]any)
			if len(audits) != 2 || a["action"] != "checkout_orders" || a["before"].(map[string]any)["principal"] != "80.00" || a["after"].(map[string]any)["principal"] != afterP || a["after"].(map[string]any)["bonus"] != afterB {
				t.Fatal(audits)
			}
			s.assertEditAudit("checkout_orders", o, after)
			cards := s.call("list_customer_cards", map[string]any{"customer_id": s.customer}, 200)["items"].([]any)
			if cards[0].(map[string]any)["principal"] != afterP || cards[0].(map[string]any)["bonus"] != afterB || cards[0].(map[string]any)["points"] != c["points"] {
				t.Fatal(cards)
			}
		})
	}
}
func TestCardCheckoutChained(t *testing.T) {
	s := open(t)
	s.paymentSetting("others_card_payment", "allowed")
	c := s.paymentCard(s.paymentType("余额卡", nil), "80", "150")
	line := s.checkoutLine()
	a, b := s.customerOrder(line), s.call("open_order", s.opening(line), 200)
	after := s.call("checkout_orders", checkoutInput(checkoutOrder(a, cardPayment(c, "30"), cardPayment(c, "70")), checkoutOrder(b, cardPayment(c, "60"), payment("cash", "40"))), 200)
	if len(lines(after)) != 2 {
		t.Fatal(after)
	}
	entries := s.consumeEntries(c)
	if len(entries) != 4 {
		t.Fatal(entries)
	}
	for i, want := range []struct{ pb, pd, pa, bb, bd, ba string }{
		{"0.00", "0.00", "0.00", "130.00", "-60.00", "70.00"},
		{"50.00", "-50.00", "0.00", "150.00", "-20.00", "130.00"},
		{"80.00", "-30.00", "50.00", "150.00", "0.00", "150.00"},
	} {
		l := entries[i].(map[string]any)
		if l["principal_before"] != want.pb || l["principal_delta"] != want.pd || l["principal_after"] != want.pa || l["bonus_before"] != want.bb || l["bonus_delta"] != want.bd || l["bonus_after"] != want.ba {
			t.Fatal(entries)
		}
	}
	audits := s.call("list_audit_log", map[string]any{"target_id": c["id"]}, 200)["entries"].([]any)
	if len(audits) != 2 {
		t.Fatal(audits)
	}
}
func TestCardCheckoutInsufficient(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("余额卡", nil), "40", "50")
	o := s.customerOrder(s.checkoutLine())
	before := s.cardSnapshot()
	r := s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, cardPayment(c, "100"))))
	if r.Status != 409 || r.Body["detail"] != "卡余额不足" || before != s.cardSnapshot() {
		t.Fatal(r)
	}
}
func TestCardCheckoutPricing(t *testing.T) {
	s := open(t)
	a, b := s.paymentType("A", "88"), s.paymentType("B", "90")
	ca, cb := s.paymentCard(a, "1000", "0"), s.paymentCard(b, "1000", "0")
	in := s.itemInput("会员项目", 0)
	in["member_prices"] = []any{map[string]any{"card_type_id": a["id"], "price": "70"}, map[string]any{"card_type_id": b["id"], "price": "80"}}
	item := s.call("create_item", in, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
	for _, tc := range []struct {
		first, second map[string]any
		price         string
		ct            any
	}{{ca, cb, "70.00", a["id"]}, {cb, ca, "80.00", b["id"]}} {
		o := s.customerOrder(line)
		ps := []orders.PaymentInput{cardPayment(tc.first, "10"), cardPayment(tc.second, map[string]string{"70.00": "60", "80.00": "70"}[tc.price])}
		after := s.paid(o, ps...)
		l := firstLine(after)
		if l["price"] != tc.price || l["price_source"] != "member" || l["card_type_id"] != tc.ct || l["level_id"] != nil || l["discount"] != "0.00" || after["net"] != tc.price {
			t.Fatal(after)
		}
		s.assertEditAudit("checkout_orders", o, after)
	}
	o := s.customerOrder(line)
	after := s.paid(o, payment("cash", "100"))
	if firstLine(after)["price_source"] != "standard" || after["net"] != "100.00" {
		t.Fatal(after)
	}
}
func TestCardCheckoutDiscount(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("折扣卡", "88"), "1000", "0")
	line := s.checkoutLine()
	o := s.customerOrder(line)
	before := s.cardSnapshot()
	r := s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, cardPayment(c, "100"))))
	if r.Status != 409 || r.Body["detail"] != "支付合计须等于实收" || before != s.cardSnapshot() {
		t.Fatal(r)
	}
	after := s.paid(o, cardPayment(c, "88"))
	if after["receivable"] != "100.00" || after["discount"] != "12.00" || after["net"] != "88.00" || firstLine(after)["discount"] != "12.00" {
		t.Fatal(after)
	}
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(o["id"])})
		if got.Status != 200 || !reflect.DeepEqual(got.Body, after) {
			t.Fatal(ch.Name, got)
		}
	}
	manual := s.customerOrder(line)
	manual = s.edit("reprice_order_item", map[string]any{"order_id": manual["id"], "order_item_id": firstLine(manual)["id"], "price": "95", "reason": "改价"}, 200)
	manual = s.paid(manual, cardPayment(c, "95"))
	if firstLine(manual)["price"] != "95.00" || firstLine(manual)["price_source"] != "manual" || manual["discount"] != "0.00" {
		t.Fatal(manual)
	}
	in := s.itemInput("不打折", 0)
	in["discountable"] = false
	item := s.call("create_item", in, 200)
	after = s.paid(s.customerOrder(orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}), cardPayment(c, "100"))
	if after["discount"] != "0.00" {
		t.Fatal(after)
	}
}
func TestCheckoutSnapshotRules(t *testing.T) {
	for _, key := range []string{"member_price_rule", "others_card_payment", "card_deduction_order"} {
		t.Run(key, func(t *testing.T) {
			s := open(t)
			ct := s.paymentType("卡", nil)
			c := s.paymentCard(ct, "80", "100")
			in := s.itemInput("会员项目", 0)
			in["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "120"}}
			item := s.call("create_item", in, 200)
			line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
			if key == "others_card_payment" {
				s.paymentSetting(key, "allowed")
			}
			o := s.customerOrder(line)
			if key == "others_card_payment" {
				o = s.call("open_order", s.opening(line), 200)
			}
			changes := map[string]string{"member_price_rule": "lowest", "others_card_payment": "not_allowed", "card_deduction_order": "bonus_first"}
			s.paymentSetting(key, changes[key])
			after := s.paid(o, cardPayment(c, "120"))
			pc := after["payments"].([]any)[0].(map[string]any)["card"].(map[string]any)
			if firstLine(after)["price_source"] != "member" || after["net"] != "120.00" || pc["principal"] != "80.00" || pc["bonus"] != "40.00" {
				t.Fatal(after)
			}
		})
	}
}
func TestCardCheckoutChecks(t *testing.T) {
	s := open(t)
	ct := s.paymentType("余额", nil)
	c := s.paymentCard(ct, "1000", "0")
	line := s.checkoutLine()
	o := s.customerOrder(line)
	caller := s.MustCaller(s.Owner)
	unknown := map[string]any{"id": uuid.NewString()}
	s.cardRefused(caller, o, cardPayment(unknown, "100"), 404, "")
	s.cardRefused(caller, s.call("open_order", s.opening(line), 200), cardPayment(c, "100"), 409, "不能用别人的卡")
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	caller.StaffID = s.employee
	s.cardRefused(caller, o, cardPayment(c, "100"), 404, "")
	caller = s.MustCaller(s.Owner)
	caller.StoreID = s.otherStore()
	foreign := s.HTTP(t, &caller, "open_order", s.opening())
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	s.cardRefused(caller, foreign.Body, cardPayment(c, "100"), 404, "")
	caller = s.MustCaller(s.Owner)
	for _, delta := range []int{-1, 0} {
		date := settings.Date(time.Now()).AddDate(0, 0, delta).Format(time.DateOnly)
		s.call("adjust_card", map[string]any{"card_id": c["id"], "expiry": map[string]any{"expires_on": date}, "reason": "边界"}, 200)
		if delta < 0 {
			s.cardRefused(caller, o, cardPayment(c, "100"), 409, "卡已过期")
		} else {
			s.paid(o, cardPayment(c, "100"))
		}
	}
	count := s.call("create_card_type", map[string]any{"name": "次卡", "kind": "count", "validity_kind": "none", "items": []any{map[string]any{"item_id": line.ItemID, "paid_count": 1, "gift_count": 0, "unit_price": "100"}}}, 200)
	cc := s.paymentCard(count, "0", "0")
	s.cardRefused(caller, s.customerOrder(line), cardPayment(cc, "100"), 409, "次卡不能用卡余额支付")
	for _, tc := range []struct {
		p      orders.PaymentInput
		detail string
	}{{payment("card", "100"), "卡余额支付要选卡"}, {func() orders.PaymentInput { p := cardPayment(c, "100"); p.Method = "cash"; return p }(), "只有卡余额支付要选卡"}} {
		before := s.cardSnapshot()
		r := s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(orders.CheckoutOrderInput{OrderID: uuid.New(), Payments: []orders.PaymentInput{tc.p}}))
		if r.Status != 409 || r.Body["detail"] != tc.detail || before != s.cardSnapshot() {
			t.Fatal(r)
		}
	}
}
func TestPreviewCheckout(t *testing.T) {
	s := open(t)
	ct := s.paymentType("折扣", "88")
	c := s.paymentCard(ct, "1000", "0")
	in := s.itemInput("会员价项目", 0)
	in["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "70"}}
	item := s.call("create_item", in, 200)
	line := s.checkoutLine()
	o := s.customerOrder(line, orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}})
	before := s.cardSnapshot()
	for _, ch := range s.Channels() {
		caller := s.MustCaller(s.Owner)
		got := ch.Call(t, caller, "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), CardID: new(id(c["id"]))})
		if got.Status != 200 || got.Body["receivable"] != "170.00" || got.Body["discount"] != "12.00" || got.Body["net"] != "158.00" || len(got.Body["payments"].([]any)) != 0 || before != s.cardSnapshot() {
			t.Fatal(ch.Name, got)
		}
		got = ch.Call(t, caller, "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"])})
		if got.Status != 200 || !reflect.DeepEqual(got.Body, o) || before != s.cardSnapshot() {
			t.Fatal(ch.Name, got)
		}
		delete(caller.Permissions, perm.PaymentCollect)
		got = ch.Call(t, caller, "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"])})
		if got.Status != 403 || before != s.cardSnapshot() {
			t.Fatal(ch.Name, got)
		}
		got = ch.Call(t, s.MustCaller(s.Owner), "preview_checkout", orders.PreviewCheckoutInput{OrderID: uuid.New()})
		if got.Status != 404 || before != s.cardSnapshot() {
			t.Fatal(ch.Name, got)
		}
	}
	s.paid(o, cardPayment(c, "158"))
	s.cardRefused(s.MustCaller(s.Owner), o, cardPayment(c, "158"), 409, "单已结账")
	a, b := s.customerOrder(), s.customerOrder()
	s.call("merge_orders", mergeInput(a, b), 200)
	s.cardRefused(s.MustCaller(s.Owner), b, cardPayment(c, "1"), 409, "单已合并，请在合并后的单上操作")
}
func TestCardCheckoutWaits(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("余额", nil), "80", "50")
	o := s.customerOrder(s.checkoutLine())
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", c["id"])
	dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'recharge',80,20,100,50,0,50,$4,$5)`, s.Res.TenantID, s.Res.StoreID, c["id"], settings.Date(time.Now()), s.Res.OwnerID)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, cardPayment(c, "100"))))
	}()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	r := optest.WaitResult(t, done, 200)
	pc := lines(r.Body)[0].(map[string]any)["payments"].([]any)[0].(map[string]any)["card"].(map[string]any)
	if pc["principal"] != "100.00" || pc["bonus"] != "0.00" {
		t.Fatal(r)
	}
}
func TestCardCheckoutPricingLocks(t *testing.T) {
	for _, table := range []string{"items", "card_types"} {
		t.Run(table, func(t *testing.T) {
			s := open(t)
			ct := s.paymentType("折扣", "88")
			c := s.paymentCard(ct, "1000", "0")
			line := s.checkoutLine()
			o := s.customerOrder(line)
			target := any(line.ItemID)
			if table == "card_types" {
				target = ct["id"]
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			dbtest.Exec(t, tx, "SELECT id FROM "+table+" WHERE id=$1 FOR NO KEY UPDATE", target)
			done := make(chan optest.Result, 1)
			go func() {
				done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, cardPayment(c, "88"))))
			}()
			optest.AssertWaiting(t, done)
			dbtest.Commit(t, tx)
			optest.WaitResult(t, done, 200)
		})
	}
}
func TestConsumeBusinessDate(t *testing.T) {
	s := open(t)
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	c := s.paymentCard(s.paymentType("余额", nil), "100", "0")
	o := s.customerOrder(s.checkoutLine())
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	caller := s.MustCaller(s.Owner)
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.CheckoutOrdersAtForTest(context.Background(), ops.Env{Caller: caller, Q: q}, new(checkoutInput(checkoutOrder(o, cardPayment(c, "100")))), at)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	entries := s.consumeEntries(c)
	if entries[0].(map[string]any)["business_date"] != day.Format(time.DateOnly) {
		t.Fatal(entries)
	}
}
func TestCardCheckoutAIRefused(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("余额", nil), "100", "0")
	o := s.customerOrder(s.checkoutLine())
	before := s.cardSnapshot()
	r := s.AI(t, s.MustCaller(s.Owner), "checkout_orders", checkoutInput(checkoutOrder(o, cardPayment(c, "100"))))
	if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) || before != s.cardSnapshot() {
		t.Fatal(r)
	}
}

func TestCardCheckoutRecordedPriceAndLowest(t *testing.T) {
	for _, rule := range []string{"member_first", "lowest"} {
		t.Run(rule, func(t *testing.T) {
			s := open(t)
			s.paymentSetting("member_price_rule", rule)
			ct := s.paymentType("卡", "88")
			c := s.paymentCard(ct, "1000", "0")
			in := s.itemInput("级别会员项目", 1)
			in["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "180"}}
			item := s.call("create_item", in, 200)
			line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}}
			o := s.customerOrder(line)
			// 已录入的 150 级别价不随目录变成 300，最低价比较必须使用行上的 150。
			in["id"], in["active"], in["price"] = item["id"], true, "400"
			in["level_prices"] = []any{map[string]any{"level_id": s.level, "price": "300"}}
			s.call("update_item", in, 200)
			amount, source, discount := "180", "member", "0.00"
			if rule == "lowest" {
				amount, source, discount = "132", "level", "18.00"
			}
			after := s.paid(o, cardPayment(c, amount))
			if firstLine(after)["price_source"] != source || after["discount"] != discount {
				t.Fatal(after)
			}
			manual := s.customerOrder(line)
			manual = s.edit("reprice_order_item", map[string]any{"order_id": manual["id"], "order_item_id": firstLine(manual)["id"], "price": "95", "reason": "改价"}, 200)
			after = s.paid(manual, cardPayment(c, "95"))
			if firstLine(after)["price_source"] != "manual" || after["net"] != "95.00" || after["discount"] != "0.00" {
				t.Fatal(after)
			}
		})
	}
}
func TestCardCheckoutRoundingAndRemoved(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("九折", "90"), "1000", "0")
	in := s.itemInput("舍入", 0)
	in["price"] = "0.25"
	item := s.call("create_item", in, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
	o := s.customerOrder(line, line)
	o = s.edit("remove_order_item", editInput(o), 200)
	removed := firstLine(o)
	after := s.paid(o, cardPayment(c, "0.23"))
	if after["receivable"] != "0.25" || after["discount"] != "0.02" || after["net"] != "0.23" || !reflect.DeepEqual(firstLine(after), removed) {
		t.Fatal(after)
	}
}

func TestCheckoutUnchangedLines(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	xmin := func() string {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var version string
		if err := tx.QueryRow(context.Background(), "SELECT xmin::text FROM order_items WHERE id=$1", firstLine(o)["id"]).Scan(&version); err != nil {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
		return version
	}
	before := xmin()
	s.paid(o, payment("cash", "100"))
	if xmin() != before {
		t.Fatal("cash checkout rewrote an unchanged line")
	}
}

func TestCheckoutCardsLockUUIDOrder(t *testing.T) {
	s := open(t)
	ct := s.paymentType("卡", nil)
	a, b := s.paymentCard(ct, "100", "0"), s.paymentCard(ct, "100", "0")
	low, high := a, b
	if low["id"].(string) > high["id"].(string) {
		low, high = high, low
	}
	o := s.customerOrder(s.checkoutLine())
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", low["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, cardPayment(high, "50"), cardPayment(low, "50"))))
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	probe := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := probe.Exec(context.Background(), "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE NOWAIT", high["id"])
	dbtest.Rollback(t, probe)
	dbtest.Commit(t, held)
	optest.WaitResult(t, done, 200)
	if err != nil {
		t.Fatal("checkout locked higher card UUID first", err)
	}
}

func TestCardCheckoutOutputPermissions(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("卡", nil), "100", "0")
	o := s.customerOrder(s.checkoutLine())
	after := s.paid(o, cardPayment(c, "100"))
	caller := s.MustCaller(s.Owner)
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	caller.StaffID = s.employee
	for _, ch := range s.Channels() {
		got := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(o["id"])})
		if got.Status != 200 || !reflect.DeepEqual(got.Body, after) {
			t.Fatal(ch.Name, got)
		}
		for _, key := range []string{"phone", "name", "customer"} {
			if _, ok := got.Body[key]; ok {
				t.Fatal(key, got)
			}
		}
		for _, target := range []any{c["id"], o["id"]} {
			got = ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": target})
			if got.Status != 200 || len(got.Body["entries"].([]any)) != 0 {
				t.Fatal(ch.Name, got)
			}
		}
		got = ch.Call(t, caller, "list_card_ledger", map[string]any{"card_id": c["id"]})
		if got.Status != 404 {
			t.Fatal(ch.Name, got)
		}
	}
}
