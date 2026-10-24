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
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func payment(method, amount string) orders.PaymentInput {
	var a money.Amount
	if err := a.Scan(amount); err != nil {
		panic(err)
	}
	return orders.PaymentInput{Method: method, Amount: a}
}
func checkoutOrder(o map[string]any, payments ...orders.PaymentInput) orders.CheckoutOrderInput {
	return orders.CheckoutOrderInput{OrderID: id(o["id"]), Payments: payments}
}
func checkoutInput(o ...orders.CheckoutOrderInput) orders.CheckoutOrdersInput {
	return orders.CheckoutOrdersInput{Orders: o}
}
func (s *fixture) checkoutSnapshot() string {
	s.t.Helper()
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "checkouts", "order_payments", "orders", "order_items", "order_item_positions", "customers", "audit_log", "settings_snapshots")
}
func (s *fixture) assertCheckoutRefused(in orders.CheckoutOrdersInput, status int, detail string) {
	s.t.Helper()
	before := s.checkoutSnapshot()
	r := s.HTTPWithToken(s.t, s.Owner, "checkout_orders", in)
	if r.Status != status || (detail != "" && r.Body["detail"] != detail) || before != s.checkoutSnapshot() {
		s.t.Fatal(r, "checkout refusal changed data or returned wrong error")
	}
}
func (s *fixture) checkoutLine() orders.OrderItemInput {
	s.t.Helper()
	item := s.call("create_item", s.itemInput("无岗位结账项目", 0), 200)
	return orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
}
func assertPaid(t *testing.T, before, after map[string]any, payments []orders.PaymentInput) {
	t.Helper()
	if after["id"] != before["id"] || after["customer_id"] != before["customer_id"] || after["status"] != "paid" || after["checkout_id"] == nil || after["receivable"] != before["receivable"] || after["business_date"] != before["business_date"] {
		t.Fatal(before, after)
	}
	ps := after["payments"].([]any)
	if len(ps) != len(payments) {
		t.Fatal(ps)
	}
	for i, p := range ps {
		row := p.(map[string]any)
		if _, err := uuid.Parse(row["id"].(string)); err != nil {
			t.Fatal(err)
		}
		if row["method"] != payments[i].Method || row["amount"] != payments[i].Amount.String() {
			t.Fatal(row, payments[i])
		}
	}
}

func TestCheckoutOrder(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	for _, ps := range [][]orders.PaymentInput{
		{payment("cash", "100")},
		{payment("cash", "0.10"), payment("wechat", "0.20"), payment("debt", "99.70")},
		{payment("unionpay", "30"), payment("alipay", "70")},
	} {
		before := s.call("open_order", s.opening(line), 200)
		if before["checkout_id"] != nil || len(before["payments"].([]any)) != 0 {
			t.Fatal(before)
		}
		after := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(before, ps...)), 200))[0].(map[string]any)
		assertPaid(t, before, after, ps)
		s.assertEditAudit("checkout_orders", before, after)
		for _, ch := range s.Channels() {
			caller := s.MustCaller(s.Owner)
			delete(caller.Permissions, perm.CustomerView)
			delete(caller.Permissions, perm.CustomerFullPhone)
			r := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(before["id"])})
			if r.Status != 200 || !reflect.DeepEqual(r.Body, after) {
				t.Fatal(ch.Name, r)
			}
			for _, key := range []string{"phone", "name", "customer"} {
				if _, ok := r.Body[key]; ok {
					t.Fatal(key, r)
				}
			}
		}
	}
}

func TestCheckoutPaymentOrder(t *testing.T) {
	s := open(t)
	before := s.customerOrder(s.checkoutLine())
	ps := []orders.PaymentInput{}
	for _, amount := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "55"} {
		ps = append(ps, payment("cash", amount))
	}
	s.call("checkout_orders", checkoutInput(checkoutOrder(before, ps...)), 200)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(before["id"])})
			if r.Status != 200 {
				t.Fatal(r)
			}
			assertPaid(t, before, r.Body, ps)
		})
	}
}

func TestCheckoutTogether(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	a := s.customerOrder(line)
	b := s.openAt(s.MustCaller(s.Owner), time.Now().AddDate(0, 0, -5), line)
	if a["customer_id"] == b["customer_id"] || a["business_date"] == b["business_date"] {
		t.Fatal(a, b)
	}
	in := checkoutInput(checkoutOrder(b, payment("debt", "100")), checkoutOrder(a, payment("wechat", "20"), payment("cash", "80")))
	// 只有 payment.collect 也能结账，不继承改单窗口、本人范围或顾客查看限制。
	caller := s.MustCaller(s.Owner)
	caller.Permissions = map[ops.Permission]ops.Grant{perm.PaymentCollect: {}}
	r := s.HTTP(t, &caller, "checkout_orders", in)
	if r.Status != 200 {
		t.Fatal(r)
	}
	after := lines(r.Body)
	for i, before := range []map[string]any{b, a} {
		got := after[i].(map[string]any)
		assertPaid(t, before, got, in.Orders[i].Payments)
		s.assertEditAudit("checkout_orders", before, got)
	}
	if after[0].(map[string]any)["checkout_id"] != after[1].(map[string]any)["checkout_id"] {
		t.Fatal(after)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_open_orders", orders.ListOpenOrdersInput{})
		if r.Status != 200 || len(lines(r.Body)) != 0 {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestCheckoutSumRules(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	a, b := s.customerOrder(line), s.customerOrder(line)
	for _, amount := range []string{"99.99", "100.01"} {
		s.assertCheckoutRefused(checkoutInput(checkoutOrder(a, payment("cash", amount))), 409, "支付合计须等于实收")
	}
	for _, ps := range [][]orders.PaymentInput{
		{payment("cash", "60"), payment("debt", "39.99")},
		{payment("cash", "60"), payment("debt", "40.01")},
		{payment("cash", "99999999.99"), payment("debt", "99999999.99")},
	} {
		s.assertCheckoutRefused(checkoutInput(checkoutOrder(a, ps...)), 409, "支付合计须等于实收")
	}
	s.assertCheckoutRefused(checkoutInput(checkoutOrder(a, payment("cash", "100")), checkoutOrder(b, payment("cash", "99.99"))), 409, "支付合计须等于实收")
}

func TestCheckoutRemovedLines(t *testing.T) {
	s := open(t)
	empty := s.customerOrder()
	s.assertCheckoutRefused(checkoutInput(checkoutOrder(empty, payment("cash", "1"))), 409, "单上没有项目")
	removed := s.customerOrder(s.line())
	removed = s.edit("remove_order_item", editInput(removed), 200)
	s.assertCheckoutRefused(checkoutInput(checkoutOrder(removed, payment("cash", "100"))), 409, "单上没有项目")
	// 去掉的行有空必填岗位，不应阻止另一条有效行结账。
	both := s.customerOrder(s.line(), s.checkoutLine())
	both = s.edit("remove_order_item", editInput(both), 200)
	if both["receivable"] != "100.00" {
		t.Fatal(both)
	}
	ps := []orders.PaymentInput{payment("cash", "100")}
	after := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(both, ps...)), 200))[0].(map[string]any)
	assertPaid(t, both, after, ps)
}

func TestCheckoutRequiredPositions(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.line(1, 2))
	s.assertCheckoutRefused(checkoutInput(checkoutOrder(o, payment("cash", "150"))), 409, "必填岗位还没填人")
	in := editInput(o)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}, {Slot: 2, StaffID: s.employee}, {Slot: 3, StaffID: s.employee}}
	o = s.edit("set_order_item_staff", in, 200)
	ps := []orders.PaymentInput{payment("cash", "150")}
	after := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(o, ps...)), 200))[0].(map[string]any)
	assertPaid(t, o, after, ps)
	optional := s.itemInput("可选岗位", 1)
	optional["positions"] = []any{map[string]any{"position_id": s.position, "required": false}}
	item := s.call("create_item", optional, 200)
	o = s.customerOrder(orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}})
	ps = []orders.PaymentInput{payment("cash", "100")}
	after = lines(s.call("checkout_orders", checkoutInput(checkoutOrder(o, ps...)), 200))[0].(map[string]any)
	assertPaid(t, o, after, ps)
}

func TestCheckoutValidation(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	valid := checkoutOrder(o, payment("cash", "100"))
	s.assertCheckoutRefused(checkoutInput(valid, valid), 409, "同一张单不能重复结账")
	// 重复检查必须先于查单和加锁，未知 ID 仍返回重复规则。
	unknown := orders.CheckoutOrderInput{OrderID: uuid.New(), Payments: valid.Payments}
	s.assertCheckoutRefused(checkoutInput(unknown, unknown), 409, "同一张单不能重复结账")
	s.assertCheckoutRefused(checkoutInput(checkoutOrder(o, payment("cash", "0"), payment("cash", "100"))), 409, "支付金额须大于零")
	s.assertCheckoutRefused(checkoutInput(unknown), 404, "")
	foreignCaller := s.MustCaller(s.Owner)
	foreignCaller.StoreID = s.otherStore()
	foreign := s.HTTP(t, &foreignCaller, "open_order", s.opening())
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	s.assertCheckoutRefused(checkoutInput(checkoutOrder(foreign.Body, payment("cash", "100"))), 404, "")
	for _, n := range []int{0, 11} {
		os := make([]orders.CheckoutOrderInput, n)
		for i := range os {
			os[i] = valid
		}
		s.assertCheckoutRefused(checkoutInput(os...), 422, "")
		ps := make([]orders.PaymentInput, n)
		for i := range ps {
			ps[i] = payment("cash", "10")
		}
		s.assertCheckoutRefused(checkoutInput(checkoutOrder(o, ps...)), 422, "")
	}
	bad := checkoutOrder(o, payment("unknown", "100"))
	s.assertCheckoutRefused(checkoutInput(bad), 422, "")
	a, b := s.customerOrder(s.line()), s.customerOrder(s.line())
	s.call("merge_orders", mergeInput(a, b), 200)
	s.assertCheckoutRefused(checkoutInput(checkoutOrder(b, payment("cash", "100"))), 409, "单已合并，请在合并后的单上操作")
}

func TestPaidOrderWritesRefused(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	s.call("checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "100"))), 200)
	other := s.customerOrder()
	caller := s.MustCaller(s.Owner)
	caller.StaffID = s.employee
	caller.Permissions[perm.OrderEdit] = ops.Grant{Scope: ops.ScopeSelf, WindowDays: new(0)}
	for _, tc := range []struct {
		op string
		in any
	}{
		{"add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()}},
		{"remove_order_item", editArgs("remove_order_item", editInput(o))},
		{"set_order_item_staff", editArgs("set_order_item_staff", editInput(o))},
		{"reprice_order_item", editArgs("reprice_order_item", editInput(o))},
		{"merge_orders", mergeInput(o, other)},
		{"merge_orders", mergeInput(other, o)},
		{"checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "100")))},
	} {
		before := s.checkoutSnapshot()
		r := s.HTTP(t, &caller, tc.op, tc.in)
		if r.Status != 409 || r.Body["detail"] != "单已结账" || before != s.checkoutSnapshot() {
			t.Fatal(tc.op, r)
		}
	}
}

func TestCheckoutBusinessDate(t *testing.T) {
	s := open(t)
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	o := s.customerOrder(s.checkoutLine())
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	caller := s.MustCaller(s.Owner)
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		out, err := orders.CheckoutOrdersAtForTest(context.Background(), ops.Env{Caller: caller, Q: q}, new(checkoutInput(checkoutOrder(o, payment("cash", "100")))), at)
		if err != nil {
			return err
		}
		if len(out.Items) != 1 || out.Items[0].Status != "paid" {
			t.Fatal(out)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var date string
	var actor uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT business_date::text,checked_out_by FROM checkouts").Scan(&date, &actor); err != nil {
		t.Fatal(err)
	}
	if date != day.Format(time.DateOnly) || actor != caller.StaffID {
		t.Fatal(date, actor)
	}
	dbtest.Rollback(t, tx)
}

func TestCheckoutPermissionsAndAI(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	in := checkoutInput(checkoutOrder(o, payment("cash", "100")))
	_, token := s.Hire("发型师", "003", "no-payment", s.Role("发型师"))
	before := s.checkoutSnapshot()
	caller := s.MustCaller(token)
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "checkout_orders", in)
		if r.Status != 403 || before != s.checkoutSnapshot() {
			t.Fatal(ch.Name, r)
		}
	}
	r := s.AI(t, s.MustCaller(s.Owner), "checkout_orders", in)
	if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) || before != s.checkoutSnapshot() {
		t.Fatal(r)
	}
}

func TestCheckoutWaitsForOrder(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	o := s.customerOrder(line, line)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", o["id"])
	dbtest.Exec(t, tx, "UPDATE order_items SET removed_at=clock_timestamp(),removed_by=$1 WHERE id=$2", s.Res.OwnerID, firstLine(o)["id"])
	expected := dbtest.SnapshotTx(t, tx, "checkouts", "order_payments", "orders", "order_items", "order_item_positions", "customers", "audit_log", "settings_snapshots")
	done := make(chan optest.Result, 1)
	caller := s.MustCaller(s.Owner)
	go func() {
		done <- s.HTTP(t, &caller, "checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "200"))))
	}()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	r := optest.WaitResult(t, done, 409)
	if r.Body["detail"] != "支付合计须等于实收" || expected != s.checkoutSnapshot() {
		t.Fatal(r)
	}
}

func TestConcurrentCheckouts(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", o["id"])
	caller := s.MustCaller(s.Owner)
	in := checkoutInput(checkoutOrder(o, payment("cash", "100")))
	done := make(chan optest.Result, 2)
	for range 2 {
		go func() { done <- s.HTTP(t, &caller, "checkout_orders", in) }()
	}
	waitCount(t, s, 2)
	dbtest.Commit(t, tx)
	statuses := map[int]int{}
	for range 2 {
		var r optest.Result
		select {
		case r = <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("checkout timed out")
		}
		statuses[r.Status]++
		if r.Status != 200 && (r.Status != 409 || r.Body["detail"] != "单已结账") {
			t.Fatal(r)
		}
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatal(statuses)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var checkouts, payments, audits int
	if err := tx.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM checkouts),(SELECT count(*) FROM order_payments),(SELECT count(*) FROM audit_log WHERE action='checkout_orders')").Scan(&checkouts, &payments, &audits); err != nil {
		t.Fatal(err)
	}
	if checkouts != 1 || payments != 1 || audits != 1 {
		t.Fatal(checkouts, payments, audits)
	}
	dbtest.Rollback(t, tx)
}

func TestCheckoutDatabaseProtections(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	after := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "100"))), 200))[0].(map[string]any)
	for _, table := range []string{"checkouts", "order_payments"} {
		for _, command := range []string{"UPDATE " + table + " SET id=id", "DELETE FROM " + table} {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), command)
			var pgErr interface{ SQLState() string }
			if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
				t.Fatal(command, err)
			}
			dbtest.Rollback(t, tx)
		}
	}
	unpaid := s.customerOrder()
	for _, tc := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount) VALUES ($1,$2,$3,$4,'cash',0)", []any{s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"]}},
		{"INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount) VALUES ($1,$2,$3,$4,'unknown',1)", []any{s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"]}},
		{"UPDATE orders SET status='paid' WHERE id=$1", []any{unpaid["id"]}},
		{"UPDATE orders SET checkout_id=$1 WHERE id=$2", []any{after["checkout_id"], unpaid["id"]}},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
		var pgErr interface{ SQLState() string }
		if !errors.As(err, &pgErr) || pgErr.SQLState() != "23514" {
			t.Fatal(tc.sql, err)
		}
		dbtest.Rollback(t, tx)
	}
}

func TestCheckoutMigrationDown(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(s.checkoutLine()), s.customerOrder()
	s.call("checkout_orders", checkoutInput(checkoutOrder(a, payment("cash", "100"))), 200)
	c := s.customerOrder()
	s.call("merge_orders", mergeInput(b, c), 200)
	dbtest.MigrateDownTo(t, s.Pool, 17)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM orders WHERE status='open'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM orders WHERE status='merged'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name='orders' AND column_name='checkout_id'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.tables WHERE table_name IN ('checkouts','order_payments')").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	dbtest.Exec(t, tx, "UPDATE orders SET customer_id=customer_id,status=status,merged_into=merged_into")
	dbtest.Rollback(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), "UPDATE orders SET gender=gender")
	var pgErr interface{ SQLState() string }
	if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
}
