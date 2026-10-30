package orders_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

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

func voidInput(o map[string]any) orders.VoidOrderInput {
	return orders.VoidOrderInput{OrderID: id(o["id"]), Reason: "  录入错误  "}
}
func (s *fixture) voidSnapshot() string {
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "orders", "order_items", "order_item_positions", "order_payments", "checkouts", "order_voids", "debts", "debt_repayments", "cards", "card_ledger", "card_count_ledger", "card_points_ledger", "red_packets", "audit_log")
}
func (s *fixture) voidRefused(c ops.Caller, in orders.VoidOrderInput, status int, detail string) {
	s.t.Helper()
	before := s.voidSnapshot()
	r := s.HTTP(s.t, &c, "void_order", in)
	if r.Status != status || detail != "" && r.Body["detail"] != detail || before != s.voidSnapshot() {
		s.t.Fatal(r)
	}
}
func (s *fixture) void(o map[string]any) map[string]any {
	s.t.Helper()
	return s.call("void_order", voidInput(o), 200)
}
func TestVoidCashAndJointCheckout(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	a, b := s.customerOrder(line), s.customerOrder(line)
	paid := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(a, payment("cash", "100")), checkoutOrder(b, payment("wechat", "100"))), 200))
	a, b = paid[0].(map[string]any), paid[1].(map[string]any)
	original := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "order_items", "order_item_positions", "order_payments", "checkouts")
	originalOrder := s.orderColumns(a)
	after := s.void(a)
	v := after["void"].(map[string]any)
	if after["status"] != "voided" || v["reason"] != "录入错误" || v["voided_by"] != s.Res.OwnerID.String() || v["business_date"] != settings.Date(time.Now()).Format(time.DateOnly) || after["checkout_id"] != a["checkout_id"] || !reflect.DeepEqual(after["payments"], a["payments"]) || original != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "order_items", "order_item_positions", "order_payments", "checkouts") {
		t.Fatal(after)
	}
	if s.orderColumns(a) != originalOrder {
		t.Fatal("void changed original order columns")
	}
	s.assertEditAudit("void_order", a, after)
	caller := s.MustCaller(s.Owner)
	delete(caller.Permissions, perm.CustomerFullPhone)
	delete(caller.Permissions, perm.CustomerView)
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(a["id"])})
		if r.Status != 200 || !reflect.DeepEqual(r.Body, after) {
			t.Fatal(ch.Name, r)
		}
		r = ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(b["id"])})
		if r.Status != 200 || !reflect.DeepEqual(r.Body, b) {
			t.Fatal(ch.Name, r)
		}
	}
}
func TestVoidRulesAndWrites(t *testing.T) {
	s := open(t)
	c := s.MustCaller(s.Owner)
	o := s.customerOrder(s.checkoutLine())
	s.voidRefused(c, voidInput(o), 409, "只能撤已结账的单")
	for _, reason := range []string{"", " \t\n", strings.Repeat("撤", 201)} {
		in := voidInput(o)
		in.Reason = reason
		status := 409
		if len([]rune(reason)) > 200 {
			status = 422
		}
		detail := ""
		if status == 409 {
			detail = "请填写不超过200字的撤单原因"
		}
		s.voidRefused(c, in, status, detail)
	}
	a, b := s.customerOrder(), s.customerOrder()
	s.call("merge_orders", mergeInput(a, b), 200)
	s.voidRefused(c, voidInput(b), 409, "单已合并，请在合并后的单上操作")
	s.paid(o, payment("cash", "100"))
	s.void(o)
	s.voidRefused(c, voidInput(o), 409, "单已撤")
	for _, tc := range []struct {
		op string
		in any
	}{
		{"add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()}},
		{"remove_order_item", editArgs("remove_order_item", editInput(o))},
		{"set_order_item_staff", editArgs("set_order_item_staff", editInput(o))},
		{"reprice_order_item", editArgs("reprice_order_item", editInput(o))},
		{"merge_orders", mergeInput(o, a)}, {"merge_orders", mergeInput(a, o)},
		{"checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "100")))},
		{"preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"])}},
	} {
		before := s.voidSnapshot()
		r := s.HTTP(t, &c, tc.op, tc.in)
		if r.Status != 409 || r.Body["detail"] != "单已撤" || before != s.voidSnapshot() {
			t.Fatal(tc.op, r)
		}
	}
	c.StoreID = s.otherStore()
	s.voidRefused(c, voidInput(o), 404, "")
}
func TestVoidPermissionAndAI(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	c := s.MustCaller(s.Owner)
	delete(c.Permissions, perm.OrderVoid)
	for _, ch := range s.Channels() {
		before := s.voidSnapshot()
		r := ch.Call(t, c, "void_order", voidInput(o))
		if r.Status != 403 || r.Detail() != "permission denied" || before != s.voidSnapshot() {
			t.Fatal(ch.Name, r)
		}
	}
	before := s.voidSnapshot()
	r := s.AI(t, s.MustCaller(s.Owner), "void_order", voidInput(o))
	if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) || before != s.voidSnapshot() {
		t.Fatal(r)
	}
}
func TestVoidBalance(t *testing.T) {
	for _, mode := range []string{"split", "same", "two", "other", "expired", "member"} {
		t.Run(mode, func(t *testing.T) {
			s := open(t)
			ct := s.paymentType("卡", nil)
			c := s.paymentCard(ct, "80", "100")
			line := s.checkoutLine()
			if mode == "member" {
				in := s.itemInput("会员项目", 0)
				in["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "100"}}
				item := s.call("create_item", in, 200)
				line = orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
			}
			if mode == "other" {
				s.paymentSetting("others_card_payment", "allowed")
			}
			o := s.customerOrder(line)
			if mode == "other" {
				o = s.call("open_order", s.opening(line), 200)
			}
			ps := []orders.PaymentInput{cardPayment(c, "100")}
			cs := []map[string]any{c}
			if mode == "same" {
				ps = []orders.PaymentInput{cardPayment(c, "60"), cardPayment(c, "40")}
			}
			if mode == "two" {
				d := s.paymentCard(ct, "30", "90")
				cs = append(cs, d)
				ps = []orders.PaymentInput{cardPayment(c, "50"), cardPayment(d, "50")}
			}
			paid := s.paid(o, ps...)
			if mode == "expired" {
				s.call("adjust_card", map[string]any{"card_id": c["id"], "expiry": map[string]any{"expires_on": settings.Date(time.Now()).AddDate(0, 0, -1).Format(time.DateOnly)}, "reason": "过期"}, 200)
			}
			original := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "order_items", "order_item_positions", "order_payments")
			consumes := map[any][]any{}
			for _, card := range cs {
				consumes[card["id"]] = s.consumeEntries(card)
			}
			caller := s.MustCaller(s.Owner)
			delete(caller.Permissions, perm.CustomerFullPhone)
			delete(caller.Permissions, perm.CustomerView)
			r := s.HTTP(t, &caller, "void_order", voidInput(o))
			if r.Status != 200 {
				t.Fatal(r)
			}
			after := r.Body
			if original != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "order_items", "order_item_positions", "order_payments") || !reflect.DeepEqual(paid["items"], after["items"]) {
				t.Fatal("original changed")
			}
			v := after["void"].(map[string]any)
			for _, card := range cs {
				entries := s.consumeEntries(card)
				count := len(entries) - len(consumes[card["id"]])
				if count < 1 {
					t.Fatal(entries)
				}
				originalEntries := consumes[card["id"]]
				if !reflect.DeepEqual(entries[count:], originalEntries) {
					t.Fatal("consume changed", entries, originalEntries)
				}
				for i := 0; i < count; i++ {
					l := entries[i].(map[string]any)
					old := originalEntries[i].(map[string]any)
					if l["kind"] != "void" || l["reverses_id"] != old["id"] || l["order_void_id"] != v["id"] || l["business_date"] != v["business_date"] || l["principal_delta"] != strings.TrimPrefix(old["principal_delta"].(string), "-") || l["bonus_delta"] != strings.TrimPrefix(old["bonus_delta"].(string), "-") {
						t.Fatal(l, old)
					}
				}
				got := s.call("list_customer_cards", map[string]any{"customer_id": s.customer}, 200)["items"].([]any)
				found := false
				for _, raw := range got {
					row := raw.(map[string]any)
					if row["id"] == card["id"] {
						found = true
						if row["principal"] != card["principal"] || row["bonus"] != card["bonus"] {
							t.Fatal(row, card)
						}
					}
				}
				if !found {
					t.Fatal(got)
				}
				audits := s.call("list_audit_log", map[string]any{"target_id": card["id"]}, 200)["entries"].([]any)
				log := audits[0].(map[string]any)
				if log["action"] != "void_order" || log["after"].(map[string]any)["principal"] != card["principal"] {
					t.Fatal(log)
				}
			}
			s.assertEditAudit("void_order", paid, after)
		})
	}
}
func TestVoidCounts(t *testing.T) {
	for _, order := range []string{"paid_first", "gift_first"} {
		t.Run(order, func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_count_order", order)
			line := s.checkoutLine()
			c := s.countCard(line, "count", 2, 1)
			o := s.customerOrder(line)
			paid := s.paid(o, countPayment(c, firstLine(o), "100"))
			old := s.countEntries(c)[0].(map[string]any)
			after := s.void(paid)
			logs := s.call("list_audit_log", map[string]any{"target_id": c["id"]}, 200)["entries"].([]any)
			if len(logs) != 3 || logs[0].(map[string]any)["action"] != "void_order" || logs[0].(map[string]any)["after"].(map[string]any)["items"].([]any)[0].(map[string]any)["paid_remaining"] != float64(2) {
				t.Fatal(logs)
			}
			entries := s.countEntries(c)
			l := entries[0].(map[string]any)
			if l["kind"] != "void" || l["reverses_id"] != old["id"] || l["order_void_id"] != after["void"].(map[string]any)["id"] || l["paid_after"] != float64(2) || l["gift_after"] != float64(1) || l["paid_delta"].(float64) != -old["paid_delta"].(float64) || l["gift_delta"].(float64) != -old["gift_delta"].(float64) || !reflect.DeepEqual(entries[1], old) {
				t.Fatal(entries)
			}
		})
	}
}
func TestVoidRedPacketReuse(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	p := s.redPacket(s.customer, "100")
	o := s.paid(s.customerOrder(line), packetPayment(p, "100"))
	s.void(o)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_red_packets", map[string]any{"customer_id": s.customer})
		if r.Status != 200 || r.Body["items"].([]any)[0].(map[string]any)["status"] != "available" {
			t.Fatal(ch.Name, r)
		}
	}
	audits := s.call("list_audit_log", map[string]any{"target_id": s.customer}, 200)["entries"].([]any)
	log := audits[0].(map[string]any)
	if log["action"] != "void_order" || log["before"].(map[string]any)["status"] != "used" || log["after"].(map[string]any)["status"] != "available" {
		t.Fatal(log)
	}
	s.paid(s.customerOrder(line), packetPayment(p, "100"))
}
func TestVoidDebt(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	a := s.paid(s.customerOrder(line), payment("debt", "100"))
	b := s.paid(s.customerOrder(line), payment("debt", "100"))
	da := a["payments"].([]any)[0].(map[string]any)["debt_id"]
	db := b["payments"].([]any)[0].(map[string]any)["debt_id"]
	s.call("repay_debt", map[string]any{"debt_id": db, "amount": "1", "method": "cash"}, 200)
	s.voidRefused(s.MustCaller(s.Owner), voidInput(b), 409, "这张单的挂账已有还款，不能撤单")
	s.void(a)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_debts", map[string]any{"customer_id": s.customer})
		if r.Status != 200 {
			t.Fatal(r)
		}
		totals := r.Body["totals"].(map[string]any)
		if len(r.Body["items"].([]any)) != 2 || totals["amount"] != "100.00" || totals["repaid"] != "1.00" || totals["outstanding"] != "99.00" {
			t.Fatal(ch.Name, r)
		}
		found := false
		for _, raw := range r.Body["items"].([]any) {
			d := raw.(map[string]any)
			if d["id"] == da {
				found = true
				if d["status"] != "voided" || d["outstanding"] != "0.00" || d["amount"] != "100.00" {
					t.Fatal(d)
				}
			}
		}
		if !found {
			t.Fatal(r)
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "debt_summary", map[string]any{})
		if r.Status != 200 || r.Body["amount"] != "100.00" || r.Body["repaid"] != "1.00" || r.Body["outstanding"] != "99.00" {
			t.Fatal(ch.Name, r)
		}
	}
	before := s.voidSnapshot()
	r := s.HTTPWithToken(t, s.Owner, "repay_debt", map[string]any{"debt_id": da, "amount": "1", "method": "cash"})
	if r.Status != 409 || r.Body["detail"] != "这笔欠款已撤单" || before != s.voidSnapshot() {
		t.Fatal(r)
	}
}
func TestVoidWindow(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	today := settings.Date(time.Now())
	for _, window := range []int{0, 7} {
		for _, age := range []int{window, window + 1} {
			o := s.openAt(s.MustCaller(s.Owner), time.Now().AddDate(0, 0, -age), line)
			s.paid(o, payment("cash", "100"))
			c := s.MustCaller(s.Owner)
			c.Permissions[perm.OrderVoid] = ops.Grant{WindowDays: new(window)}
			if age == window {
				r := s.HTTP(t, &c, "void_order", voidInput(o))
				if r.Status != 200 {
					t.Fatal(r)
				}
			} else {
				s.voidRefused(c, voidInput(o), 403, "permission denied")
				s.void(o)
			}
		}
	}
	// 营业日跨自然日的 01:30；窗口和反向单都使用营业日。
	day := today.AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	for _, age := range []int{0, 1} {
		openedAt := at
		if age == 1 {
			openedAt = time.Date(day.Year(), day.Month(), day.Day()-1, 12, 0, 0, 0, settings.Location)
		}
		o := s.openAt(s.MustCaller(s.Owner), openedAt, line)
		s.paid(o, payment("cash", "100"))
		c := s.MustCaller(s.Owner)
		grant := ops.Grant{WindowDays: new(0)}
		before := s.voidSnapshot()
		err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
			out, err := orders.VoidOrderAtForTest(context.Background(), ops.Env{Caller: c, Q: q, Grant: grant}, new(voidInput(o)), at)
			if err == nil && out.Void.BusinessDate != day.Format(time.DateOnly) {
				t.Fatal(out)
			}
			return err
		})
		if age == 0 && err != nil || age == 1 && (!errors.Is(err, ops.ErrForbidden) || err.Error() != "permission denied" || before != s.voidSnapshot()) {
			t.Fatal(err)
		}
	}
}
func TestVoidViewQueryCount(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	one, many := s.customerOrder(line), s.customerOrder(line, line, line)
	s.void(s.paid(one, payment("cash", "100")))
	s.void(s.paid(many, payment("cash", "100"), payment("wechat", "100"), payment("alipay", "100")))
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
	for _, ch := range app.Channels() {
		counts := []int64{}
		for _, o := range []map[string]any{one, many} {
			counter.all.Store(0)
			r := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(o["id"])})
			if r.Status != 200 || r.Body["void"] == nil {
				t.Fatal(r)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, counts)
		}
	}
}

func TestVoidOutputPermissionsAndAudit(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.paymentCard(s.paymentType("审计卡", nil), "100", "0")
	p := s.redPacket(s.customer, "100")
	o := s.paid(s.customerOrder(line), cardPayment(c, "30"), cardPayment(c, "20"), packetPayment(p, "50"))
	caller := s.MustCaller(s.Owner)
	delete(caller.Permissions, perm.CustomerFullPhone)
	r := s.HTTP(t, &caller, "void_order", voidInput(o))
	if r.Status != 200 {
		t.Fatal(r)
	}
	for _, ch := range s.Channels() {
		r = ch.Call(t, caller, "list_audit_log", map[string]any{"limit": 200})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		data, err := json.Marshal(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "13800000001") || !strings.Contains(string(data), "138****0001") {
			t.Fatal(string(data))
		}
		counts := map[string]int{}
		for _, raw := range r.Body["entries"].([]any) {
			log := raw.(map[string]any)
			if log["action"] == "void_order" {
				counts[log["target_kind"].(string)]++
			}
		}
		if !reflect.DeepEqual(counts, map[string]int{"order": 1, "card": 1, "customer": 1}) {
			t.Fatal(counts)
		}
		hidden := caller
		hidden.Permissions = maps.Clone(caller.Permissions)
		hidden.StaffID = s.employee
		hidden.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
		for _, target := range []any{o["id"], c["id"], s.customer} {
			r = ch.Call(t, hidden, "list_audit_log", map[string]any{"target_id": target})
			if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
				t.Fatal(ch.Name, r)
			}
		}
		r = ch.Call(t, hidden, "list_card_ledger", map[string]any{"card_id": c["id"]})
		if r.Status != 404 {
			t.Fatal(r)
		}
		r = ch.Call(t, hidden, "list_customer_debts", map[string]any{"customer_id": s.customer})
		if r.Status != 404 {
			t.Fatal(r)
		}
	}
}
func TestVoidRestoreBusinessDate(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.paymentCard(s.paymentType("营业日卡", nil), "80", "100")
	cc := s.countCard(line, "count", 1, 1)
	p := s.redPacket(s.customer, "100")
	o := s.customerOrder(line, line)
	o = s.paid(o, cardPayment(c, "100"), countPayment(cc, firstLine(o), "100"))
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	out, err := orders.VoidOrderAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: dbgen.New(tx)}, new(voidInput(o)), at)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	for _, l := range []map[string]any{s.consumeEntries(c)[0].(map[string]any), s.countEntries(cc)[0].(map[string]any)} {
		if l["business_date"] != day.Format(time.DateOnly) || l["order_void_id"] != out.Void.ID.String() {
			t.Fatal(l, out)
		}
	}
	packetOrder := s.paid(s.customerOrder(line), packetPayment(p, "100"))
	s.void(packetOrder)
}

func TestVoidJointCard(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.paymentCard(s.paymentType("一起结账卡", nil), "80", "100")
	a, b := s.customerOrder(line), s.customerOrder(line)
	paid := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(a, cardPayment(c, "100")), checkoutOrder(b, cardPayment(c, "50"), payment("cash", "50"))), 200))
	a, b = paid[0].(map[string]any), paid[1].(map[string]any)
	s.void(a)
	got := s.call("get_order", orders.GetOrderInput{ID: id(b["id"])}, 200)
	l := s.consumeEntries(c)[0].(map[string]any)
	if !reflect.DeepEqual(got, b) || l["principal_after"] != "80.00" || l["bonus_after"] != "50.00" {
		t.Fatal(got, b, l)
	}
}
func TestVoidViewsBatch(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	rows := []dbgen.Order{}
	for range 3 {
		o := s.void(s.paid(s.customerOrder(line), payment("cash", "100")))
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		row, err := dbgen.New(tx).GetOrder(context.Background(), dbgen.GetOrderParams{StoreID: s.Res.StoreID, ID: id(o["id"])})
		dbtest.Rollback(t, tx)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	counter := &paymentQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tx := dbtest.AppTx(t, pool, s.Res.TenantID)
	q := dbgen.New(tx)
	counts := []int64{}
	for _, size := range []int{1, 3} {
		counter.all.Store(0)
		out, err := orders.ViewsForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, rows[:size])
		counts = append(counts, counter.all.Load())
		if err != nil || len(out) != size {
			t.Fatal(out, err)
		}
		for i, o := range out {
			if o.Void == nil || o.Status != "voided" || o.ID != rows[i].ID {
				t.Fatal(o)
			}
		}
	}
	dbtest.Rollback(t, tx)
	if counts[0] != counts[1] {
		t.Fatal(counts)
	}
}

func (s *fixture) orderColumns(o map[string]any) string {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	var columns string
	err := tx.QueryRow(context.Background(), "SELECT (to_jsonb(o)-'status')::text FROM orders o WHERE id=$1", o["id"]).Scan(&columns)
	dbtest.Rollback(s.t, tx)
	if err != nil {
		s.t.Fatal(err)
	}
	return columns
}
