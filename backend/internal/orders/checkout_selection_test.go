package orders_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func assertSelectionPayments(t *testing.T, order map[string]any, cards []map[string]any, amounts []string) {
	t.Helper()
	ps := order["payments"].([]any)
	if len(ps) != len(cards) {
		t.Fatal(order)
	}
	for i, raw := range ps {
		p := raw.(map[string]any)
		if p["method"] != "card" || p["amount"] != amounts[i] || p["card"].(map[string]any)["card_id"] != cards[i]["id"] {
			t.Fatal(order)
		}
	}
}

func TestSelectionRules(t *testing.T) {
	s := open(t)
	s.paymentCard(s.paymentType("卡", nil), "1000", "0")
	line := s.checkoutLine()
	o := s.customerOrder(line)
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(o, payment("card", "100"))), 409, "卡余额支付要选卡")
	s.paymentSetting("card_selection", "by_balance")
	auto := s.customerOrder(line)
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(auto, payment("card", "50"), payment("card", "50"))), 409, "一张单只能有一笔不指定卡的卡余额支付")
	// 旧单仍使用 manual；新单的 by_balance 不受之后修改影响。
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(o, payment("card", "100"))), 409, "卡余额支付要选卡")
	s.paymentSetting("card_selection", "manual")
	s.paid(auto, payment("card", "100"))
	before := s.cardSnapshot()
	r := s.AI(t, s.MustCaller(s.Owner), "checkout_orders", checkoutInput(checkoutOrder(o, payment("card", "100"))))
	if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) || before != s.cardSnapshot() {
		t.Fatal(r)
	}
}

func TestSelectionBalance(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_balance")
	ct := s.paymentType("小余额会员卡", nil)
	a := s.paymentCard(ct, "20", "10")
	b := s.paymentCard(s.paymentType("八折卡", "80"), "80", "0")
	unused := s.paymentCard(s.paymentType("大余额", nil), "200", "0")
	expired := s.paymentCard(ct, "1", "0")
	s.call("adjust_card", map[string]any{"card_id": expired["id"], "expiry": map[string]any{"expires_on": settings.Date(time.Now()).AddDate(0, 0, -1).Format(time.DateOnly)}, "reason": "过期"}, 200)
	s.paymentCard(ct, "0", "0")
	line := s.checkoutLine()
	s.countCard(line, "count", 1, 0)
	other := s.call("open_order", s.opening(), 200)
	s.call("open_card", map[string]any{"customer_id": other["customer_id"], "card_type_id": ct["id"], "principal": "2", "bonus": "0", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}}, 200)
	input := s.itemInput("会员价一百", 0)
	input["price"] = "120"
	input["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "100"}}
	item := s.call("create_item", input, 200)
	o := s.customerOrder(orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}})
	after := s.paid(o, payment("card", "100"))
	assertSelectionPayments(t, after, []map[string]any{a, b}, []string{"30.00", "70.00"})
	if firstLine(after)["card_type_id"] != ct["id"] || after["net"] != "100.00" {
		t.Fatal(after)
	}
	for i, c := range []map[string]any{a, b} {
		l := s.consumeEntries(c)[0].(map[string]any)
		if l["kind"] != "consume" || l["order_id"] != o["id"] || l["principal_delta"] != []string{"-20.00", "-70.00"}[i] || l["bonus_delta"] != []string{"-10.00", "0.00"}[i] || l["principal_after"] != []string{"0.00", "10.00"}[i] {
			t.Fatal(l)
		}
		audits := s.call("list_audit_log", map[string]any{"target_id": c["id"]}, 200)["entries"].([]any)
		if len(audits) != 2 || audits[0].(map[string]any)["action"] != "checkout_orders" {
			t.Fatal(audits)
		}
	}
	if got := s.call("list_audit_log", map[string]any{"target_id": unused["id"]}, 200)["entries"].([]any); len(got) != 1 {
		t.Fatal(got)
	}
	short := s.customerOrder(line, line, line)
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(short, payment("card", "240"))), 409, "卡余额不足")
}

func TestSelectionDiscount(t *testing.T) {
	for _, memberWins := range []bool{true, false} {
		t.Run(map[bool]string{true: "member", false: "discount"}[memberWins], func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_selection", "by_discount")
			member := s.paymentType("会员卡", nil)
			discount := s.paymentType("折扣卡", "80")
			a := s.paymentCard(member, "30", "0")
			b := s.paymentCard(discount, "40", "100")
			input := s.itemInput("不同实收", 0)
			memberPrice, net := "70", "70.00"
			if !memberWins {
				memberPrice, net = "90", "80.00"
			}
			input["member_prices"] = []any{map[string]any{"card_type_id": member["id"], "price": memberPrice}}
			item := s.call("create_item", input, 200)
			o := s.customerOrder(orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}})
			first, second, amounts := a, b, []string{"30.00", "40.00"}
			if !memberWins {
				first, second, amounts = b, a, []string{"80.00"}
			}
			for _, ch := range s.Channels() {
				before := s.cardSnapshot()
				r := ch.Call(t, s.MustCaller(s.Owner), "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), AutoCard: true})
				if r.Status != 200 || r.Body["pricing_card_id"] != first["id"] || r.Body["net"] != net || before != s.cardSnapshot() {
					t.Fatal(ch.Name, r)
				}
			}
			after := s.paid(o, payment("card", net))
			want := []map[string]any{first, second}
			if !memberWins {
				want = want[:1]
			}
			assertSelectionPayments(t, after, want, amounts)
			if after["net"] != net || memberWins && firstLine(after)["card_type_id"] != member["id"] || !memberWins && after["discount"] != "20.00" {
				t.Fatal(after)
			}
		})
	}
}

func TestSelectionFilters(t *testing.T) {
	for _, kind := range []string{"none", "expired", "count", "zero", "other"} {
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_selection", "by_balance")
			s.paymentSetting("others_card_payment", "allowed")
			line := s.checkoutLine()
			if kind != "none" {
				var c map[string]any
				if kind == "count" {
					c = s.countCard(line, "count", 1, 0)
					// 构造带余额的次卡，确保卡种检查独立于零余额检查。
					tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
					dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'recharge',0,100,100,0,0,0,$4,$5)`, s.Res.TenantID, s.Res.StoreID, c["id"], settings.Date(time.Now()), s.Res.OwnerID)
					dbtest.Commit(t, tx)
				} else {
					balance := "100"
					if kind == "zero" {
						balance = "0"
					}
					c = s.paymentCard(s.paymentType("卡", nil), balance, "0")
				}
				if kind == "expired" {
					s.call("adjust_card", map[string]any{"card_id": c["id"], "expiry": map[string]any{"expires_on": settings.Date(time.Now()).AddDate(0, 0, -1).Format(time.DateOnly)}, "reason": "过期"}, 200)
				}
				if kind == "other" {
					other := s.call("open_order", s.opening(), 200)
					tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
					dbtest.Exec(t, tx, "UPDATE cards SET customer_id=$2 WHERE id=$1", c["id"], other["customer_id"])
					dbtest.Commit(t, tx)
				}
			}
			o := s.customerOrder(line)
			s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(o, payment("card", "100"))), 409, "这位顾客没有能用的卡")
			for _, ch := range s.Channels() {
				before := s.cardSnapshot()
				r := ch.Call(t, s.MustCaller(s.Owner), "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), AutoCard: true})
				if r.Status != 409 || ch.Name == "http" && r.Body["detail"] != "这位顾客没有能用的卡" || ch.Name == "ai" && r.Err.Error() != "这位顾客没有能用的卡" || before != s.cardSnapshot() {
					t.Fatal(ch.Name, r)
				}
			}
		})
	}
}

func TestSelectionExplicitPosition(t *testing.T) {
	for _, autoFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "auto first", false: "explicit first"}[autoFirst], func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_selection", "by_balance")
			explicit := s.paymentCard(s.paymentType("九折", "90"), "10", "0")
			ct := s.paymentType("八折", "80")
			picked := s.paymentCard(ct, "200", "0")
			line := s.checkoutLine()
			input := s.itemInput("另一计价卡会员价", 0)
			input["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "80"}}
			item := s.call("create_item", input, 200)
			line.ItemID = id(item["id"])
			o := s.customerOrder(line)
			ps := []orders.PaymentInput{cardPayment(explicit, "10"), payment("card", "80")}
			want, amounts := []map[string]any{explicit, picked}, []string{"10.00", "80.00"}
			net := "90.00"
			if autoFirst {
				ps = []orders.PaymentInput{payment("card", "70"), cardPayment(explicit, "10")}
				want, amounts, net = []map[string]any{picked, explicit}, []string{"70.00", "10.00"}, "80.00"
			}
			after := s.paid(o, ps...)
			assertSelectionPayments(t, after, want, amounts)
			if after["net"] != net || autoFirst && firstLine(after)["card_type_id"] != ct["id"] {
				t.Fatal(after)
			}
		})
	}
}

func TestSelectionTies(t *testing.T) {
	for _, rule := range []string{"by_balance", "by_discount"} {
		t.Run(rule, func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_selection", rule)
			ct := s.paymentType("同序", nil)
			cs := []map[string]any{s.paymentCard(ct, "100", "0"), s.paymentCard(ct, "100", "0"), s.paymentCard(ct, "100", "0"), s.paymentCard(ct, "100", "0"), s.paymentCard(ct, "100", "0")}
			// UUID 最后一层也固定反向输入，避免排序偶然正确。
			slices.SortFunc(cs[1:4], func(a, b map[string]any) int { return strings.Compare(a["id"].(string), b["id"].(string)) })
			cs[1], cs[2], cs[3] = cs[2], cs[3], cs[1]
			day := settings.Date(time.Now())
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			for i, c := range cs {
				var expiry any = day.AddDate(0, 0, 2)
				if i == 0 {
					expiry = day
				}
				if i == 4 {
					expiry = nil
				}
				opened := day.Add(time.Duration(i) * time.Hour)
				if i == 0 {
					opened = day.Add(5 * time.Hour)
				}
				if i == 2 {
					opened = day.Add(time.Hour)
				}
				if i == 4 {
					opened = day.Add(-time.Hour)
				}
				dbtest.Exec(t, tx, "UPDATE cards SET expires_on=$2,opened_at=$3 WHERE id=$1", c["id"], expiry, opened)
			}
			dbtest.Commit(t, tx)
			line := s.checkoutLine()
			o := s.customerOrder(line, line, line, line, line)
			// 第一次选 expiry，扣空之后再验证 opened_at 与 UUID 的排序。
			for _, ch := range s.Channels() {
				before := s.cardSnapshot()
				r := ch.Call(t, s.MustCaller(s.Owner), "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), AutoCard: true})
				if r.Status != 200 || r.Body["pricing_card_id"] != cs[0]["id"] || before != s.cardSnapshot() {
					t.Fatal(r)
				}
			}
			after := s.paid(o, payment("card", "500"))
			assertSelectionPayments(t, after, cs, []string{"100.00", "100.00", "100.00", "100.00", "100.00"})
		})
	}
}

func TestSelectionWaits(t *testing.T) {
	for _, move := range []bool{false, true} {
		t.Run(map[bool]string{false: "balance", true: "holder"}[move], func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_selection", "by_balance")
			s.paymentSetting("others_card_payment", "allowed")
			ct := s.paymentType("卡", nil)
			aBalance, bBalance := "80", "30"
			if move {
				aBalance, bBalance = "20", "200"
			}
			a, b := s.paymentCard(ct, aBalance, "0"), s.paymentCard(ct, bBalance, "0")
			o := s.customerOrder(s.checkoutLine())
			other := s.call("open_order", s.opening(), 200)
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			dbtest.Exec(t, tx, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", a["id"])
			if move {
				dbtest.Exec(t, tx, "UPDATE cards SET customer_id=$2 WHERE id=$1", a["id"], other["customer_id"])
			} else {
				dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'consume',80,-60,20,0,0,0,$4,$5)`, s.Res.TenantID, s.Res.StoreID, a["id"], settings.Date(time.Now()), s.Res.OwnerID)
			}
			in := checkoutInput(checkoutOrder(o, payment("card", "40"), payment("cash", "60")))
			if move {
				in = checkoutInput(checkoutOrder(o, payment("card", "30"), payment("cash", "70")))
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", in) }()
			dbtest.WaitForLockWait(t, s.Pool)
			optest.AssertWaiting(t, done)
			dbtest.Commit(t, tx)
			r := optest.WaitResult(t, done, 200)
			ps := lines(r.Body)[0].(map[string]any)["payments"].([]any)
			if move {
				if len(ps) != 2 || ps[0].(map[string]any)["card"].(map[string]any)["card_id"] != b["id"] || len(s.consumeEntries(a)) != 1 {
					t.Fatal(r)
				}
			} else {
				if len(ps) != 3 || ps[0].(map[string]any)["card"].(map[string]any)["card_id"] != a["id"] || ps[0].(map[string]any)["amount"] != "20.00" || ps[1].(map[string]any)["amount"] != "20.00" {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestSelectionPreview(t *testing.T) {
	s := open(t)
	ct := s.paymentType("卡", nil)
	a, b := s.paymentCard(ct, "30", "0"), s.paymentCard(ct, "200", "0")
	line := s.checkoutLine()
	manual := s.customerOrder(line)
	s.paymentSetting("card_selection", "by_balance")
	o := s.customerOrder(line)
	s.paymentSetting("card_selection", "manual")
	before := s.cardSnapshot()
	for _, ch := range s.Channels() {
		caller := s.MustCaller(s.Owner)
		for _, tc := range []struct {
			in     orders.PreviewCheckoutInput
			status int
			detail string
			want   any
		}{
			{orders.PreviewCheckoutInput{OrderID: id(o["id"]), AutoCard: true}, 200, "", a["id"]},
			{orders.PreviewCheckoutInput{OrderID: id(o["id"]), CardID: new(id(b["id"]))}, 200, "", b["id"]},
			{orders.PreviewCheckoutInput{OrderID: id(o["id"])}, 200, "", nil},
			{orders.PreviewCheckoutInput{OrderID: id(o["id"]), CardID: new(id(b["id"])), AutoCard: true}, 409, "指定了卡就不用按规则选卡", nil},
			{orders.PreviewCheckoutInput{OrderID: id(manual["id"]), AutoCard: true}, 409, "门店是手动选卡，请指定卡", nil},
		} {
			r := ch.Call(t, caller, "preview_checkout", tc.in)
			if r.Status != tc.status || before != s.cardSnapshot() {
				t.Fatal(ch.Name, r)
			}
			if tc.status == 200 {
				if r.Body["pricing_card_id"] != tc.want {
					t.Fatal(r)
				}
				delete(r.Body, "pricing_card_id")
				if !reflect.DeepEqual(r.Body, o) {
					t.Fatal(r)
				}
			} else if ch.Name == "http" && r.Body["detail"] != tc.detail || ch.Name == "ai" && r.Err.Error() != tc.detail {
				t.Fatal(r)
			}
		}
		caller.StaffID = s.employee
		caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
		r := ch.Call(t, caller, "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), AutoCard: true})
		if r.Status != 404 || before != s.cardSnapshot() {
			t.Fatal(r)
		}
	}
	after := s.paid(o, payment("card", "100"))
	assertSelectionPayments(t, after, []map[string]any{a, b}, []string{"30.00", "70.00"})
	caller := s.MustCaller(s.Owner)
	caller.StaffID = s.employee
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(o["id"])})
		if r.Status != 200 || !reflect.DeepEqual(r.Body, after) {
			t.Fatal(r)
		}
		for _, c := range []map[string]any{a, b} {
			r = ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": c["id"]})
			if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
				t.Fatal(r)
			}
		}
	}
}

func TestSelectionChained(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_balance")
	ct := s.paymentType("卡", nil)
	a, b := s.paymentCard(ct, "30", "0"), s.paymentCard(ct, "200", "0")
	line := s.checkoutLine()
	first, second := s.customerOrder(line), s.customerOrder(line)
	out := s.call("checkout_orders", checkoutInput(checkoutOrder(first, cardPayment(a, "30"), payment("cash", "70")), checkoutOrder(second, payment("card", "100"))), 200)
	assertSelectionPayments(t, lines(out)[1].(map[string]any), []map[string]any{b}, []string{"100.00"})
	if len(s.consumeEntries(a)) != 2 {
		t.Fatal(out)
	}
}

func TestSelectionCandidateTypeLocks(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_discount")
	a, b := s.paymentType("A", "80"), s.paymentType("B", "90")
	s.paymentCard(a, "10", "0")
	s.paymentCard(b, "100", "0")
	o := s.customerOrder(s.checkoutLine())
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM card_types WHERE id=$1 FOR NO KEY UPDATE", b["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, payment("card", "80"))))
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	optest.WaitResult(t, done, 200)
}

func TestSelectionScopeEmpty(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_balance")
	o := s.customerOrder(s.checkoutLine())
	caller := s.MustCaller(s.Owner)
	caller.StaffID = s.employee
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	s.countRefused(caller, checkoutInput(checkoutOrder(o, payment("card", "100"))), 404, "")
	for _, ch := range s.Channels() {
		before := s.cardSnapshot()
		r := ch.Call(t, caller, "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), AutoCard: true})
		if r.Status != 404 || before != s.cardSnapshot() {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestSelectionDiscountCurrentBalances(t *testing.T) {
	for _, together := range []bool{false, true} {
		t.Run(map[bool]string{false: "single order", true: "two orders"}[together], func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_selection", "by_discount")
			a := s.paymentCard(s.paymentType("A八折", "80"), "20", "0")
			b := s.paymentCard(s.paymentType("B九折", "90"), "200", "0")
			line := s.checkoutLine()
			first := s.customerOrder(line)
			in := checkoutInput(checkoutOrder(first, payment("card", "80")))
			if together {
				second := s.customerOrder(line)
				in.Orders = append(in.Orders, checkoutOrder(second, payment("card", "90")))
			}
			out := lines(s.call("checkout_orders", in, 200))
			firstPaid := out[0].(map[string]any)
			if len(out) != len(in.Orders) || firstPaid["net"] != "80.00" || firstPaid["discount"] != "20.00" {
				t.Fatal(out)
			}
			assertSelectionPayments(t, firstPaid, []map[string]any{a, b}, []string{"20.00", "60.00"})
			if together {
				secondPaid := out[1].(map[string]any)
				if secondPaid["net"] != "90.00" || secondPaid["discount"] != "10.00" || secondPaid["checkout_id"] != firstPaid["checkout_id"] {
					t.Fatal(out)
				}
				assertSelectionPayments(t, secondPaid, []map[string]any{b}, []string{"90.00"})
			}
			aEntries, bEntries := s.consumeEntries(a), s.consumeEntries(b)
			al := aEntries[0].(map[string]any)
			if len(aEntries) != 2 || al["principal_before"] != "20.00" || al["principal_delta"] != "-20.00" || al["principal_after"] != "0.00" || al["order_id"] != first["id"] {
				t.Fatal(aEntries)
			}
			wantB := 2
			if together {
				wantB = 3
			}
			if len(bEntries) != wantB {
				t.Fatal(bEntries)
			}
			for i, raw := range bEntries[:wantB-1] {
				l := raw.(map[string]any)
				before, delta, after, orderID := "200.00", "-60.00", "140.00", first["id"]
				if together && i == 0 {
					before, delta, after, orderID = "140.00", "-90.00", "50.00", out[1].(map[string]any)["id"]
				}
				if l["kind"] != "consume" || l["principal_before"] != before || l["principal_delta"] != delta || l["principal_after"] != after || l["bonus_delta"] != "0.00" || l["order_id"] != orderID {
					t.Fatal(bEntries)
				}
			}
		})
	}
}

func TestSelectionBalanceCurrentBalances(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_balance")
	ct := s.paymentType("余额卡", nil)
	a, b := s.paymentCard(ct, "60", "0"), s.paymentCard(ct, "200", "0")
	line := s.checkoutLine()
	first, second := s.customerOrder(line, line), s.customerOrder(line)
	out := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(first, cardPayment(b, "150"), payment("cash", "50")), checkoutOrder(second, payment("card", "100"))), 200))
	secondPaid := out[1].(map[string]any)
	if secondPaid["net"] != "100.00" || secondPaid["checkout_id"] != out[0].(map[string]any)["checkout_id"] {
		t.Fatal(out)
	}
	assertSelectionPayments(t, secondPaid, []map[string]any{b, a}, []string{"50.00", "50.00"})
	aEntries, bEntries := s.consumeEntries(a), s.consumeEntries(b)
	if len(aEntries) != 2 || len(bEntries) != 3 {
		t.Fatal(aEntries, bEntries)
	}
	for _, tc := range []struct {
		ledger               map[string]any
		before, delta, after string
		orderID              any
	}{
		{aEntries[0].(map[string]any), "60.00", "-50.00", "10.00", second["id"]},
		{bEntries[0].(map[string]any), "50.00", "-50.00", "0.00", second["id"]},
		{bEntries[1].(map[string]any), "200.00", "-150.00", "50.00", first["id"]},
	} {
		l := tc.ledger
		if l["kind"] != "consume" || l["principal_before"] != tc.before || l["principal_delta"] != tc.delta || l["principal_after"] != tc.after || l["bonus_delta"] != "0.00" || l["order_id"] != tc.orderID {
			t.Fatal(l)
		}
	}
}

func TestSelectionShortTotalRollsBack(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_balance")
	ct := s.paymentType("不足的卡", nil)
	s.paymentCard(ct, "30", "0")
	s.paymentCard(ct, "60", "0")
	line := s.checkoutLine()
	first, second := s.customerOrder(line), s.customerOrder(line)
	s.countRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(first, payment("cash", "100")), checkoutOrder(second, payment("card", "100"))), 409, "卡余额不足")
}
