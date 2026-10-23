package orders_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func (s *fixture) customerOrder(items ...orders.OrderItemInput) map[string]any {
	s.t.Helper()
	in := s.opening(items...)
	in.CustomerID = &s.customer
	return s.call("open_order", in, 200)
}
func mergeInput(keep, merged map[string]any, rest ...map[string]any) orders.MergeOrdersInput {
	in := orders.MergeOrdersInput{OrderID: id(keep["id"]), MergedOrderIDs: []uuid.UUID{id(merged["id"])}}
	for _, o := range rest {
		in.MergedOrderIDs = append(in.MergedOrderIDs, id(o["id"]))
	}
	return in
}
func (s *fixture) assertMergeRefused(caller ops.Caller, in orders.MergeOrdersInput, status int, detail string) {
	s.t.Helper()
	before := s.snapshot()
	r := s.HTTP(s.t, &caller, "merge_orders", in)
	if r.Status != status || (detail != "" && r.Body["detail"] != detail) || before != s.snapshot() {
		s.t.Fatal(r, "refusal must leave all data unchanged")
	}
}

func TestMergeOrders(t *testing.T) {
	for _, n := range []int{2, 3} {
		t.Run(string(rune('0'+n)), func(t *testing.T) {
			s := open(t)
			keep := s.customerOrder(s.line(1))
			merged := s.customerOrder(s.line(), s.line(1))
			merged = s.edit("remove_order_item", editInput(merged), 200)
			originals := []map[string]any{keep, merged}
			in := mergeInput(keep, merged)
			if n == 3 {
				third := s.customerOrder(s.line())
				originals = append(originals, third)
				in.MergedOrderIDs = append(in.MergedOrderIDs, id(third["id"]))
			}
			after := s.call("merge_orders", in, 200)
			want := "300.00"
			if n == 3 {
				want = "400.00"
			}
			if len(lines(after)) != n+1 || after["receivable"] != want || after["status"] != "open" || after["merged_into"] != nil {
				t.Fatal(after)
			}
			for _, key := range []string{"gender", "designated", "opened_by", "opened_at", "business_date", "customer_id"} {
				if after[key] != keep[key] {
					t.Fatal(key, after)
				}
			}
			for i, before := range originals {
				got := s.call("get_order", orders.GetOrderInput{ID: id(before["id"])}, 200)
				if i > 0 && (got["status"] != "merged" || got["merged_into"] != keep["id"] || len(lines(got)) != 0 || got["receivable"] != "0.00") {
					t.Fatal(got)
				}
				s.assertEditAudit("merge_orders", before, got)
				for _, ch := range s.Channels() {
					r := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(before["id"])})
					if r.Status != 200 || !reflect.DeepEqual(r.Body, got) {
						t.Fatal(r)
					}
				}
			}
			for _, ch := range s.Channels() {
				r := ch.Call(t, s.MustCaller(s.Owner), "list_open_orders", orders.ListOpenOrdersInput{})
				if r.Status != 200 || !reflect.DeepEqual(lines(r.Body), []any{after}) {
					t.Fatal(r)
				}
			}
			moved := map[any]map[string]any{}
			for _, line := range lines(after) {
				row := line.(map[string]any)
				moved[row["id"]] = row
			}
			for _, before := range originals {
				for _, line := range lines(before) {
					row := line.(map[string]any)
					if !reflect.DeepEqual(row, moved[row["id"]]) {
						t.Fatal("line changed or lost", row, moved)
					}
				}
			}
		})
	}
}

func TestMergeRules(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(s.line()), s.customerOrder(s.line())
	caller := s.MustCaller(s.Owner)
	different := s.call("open_order", s.opening(s.line()), 200)
	old := s.openAt(caller, time.Now().AddDate(0, 0, -2), s.line())
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE orders SET customer_id=$1 WHERE id=$2", s.customer, old["id"])
	dbtest.Commit(t, tx)
	foreignCaller := caller
	foreignCaller.StoreID = s.otherStore()
	foreign := s.HTTP(t, &foreignCaller, "open_order", s.opening())
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	for _, tc := range []struct {
		in     orders.MergeOrdersInput
		status int
		detail string
	}{
		{mergeInput(a, different), 409, "不同顾客的单不能合并"},
		{mergeInput(a, old), 409, "不同营业日的单不能合并"},
		{mergeInput(a, a), 409, "保留的单不能并入自身"},
		{mergeInput(a, b, b), 409, "并入的单不能重复"},
		{mergeInput(a, foreign.Body), 404, ""},
		{orders.MergeOrdersInput{OrderID: uuid.New(), MergedOrderIDs: []uuid.UUID{id(b["id"])}}, 404, ""},
	} {
		s.assertMergeRefused(caller, tc.in, tc.status, tc.detail)
	}
	s.call("merge_orders", mergeInput(a, b), 200)
	c := s.customerOrder()
	s.assertMergeRefused(caller, mergeInput(c, b), 409, "单已合并，请在合并后的单上操作")
	s.assertMergeRefused(caller, mergeInput(b, c), 409, "单已合并，请在合并后的单上操作")
}

func TestMergedOrderWritesRefused(t *testing.T) {
	s := open(t)
	a, b, c := s.customerOrder(s.line()), s.customerOrder(s.line()), s.customerOrder()
	s.call("merge_orders", mergeInput(a, b), 200)
	caller := s.MustCaller(s.Owner)
	// 即使本人范围检查会拒绝，状态规则仍必须先返回。
	caller.StaffID = s.employee
	caller.Permissions[perm.OrderEdit] = ops.Grant{Scope: ops.ScopeSelf, WindowDays: new(0)}
	for _, tc := range []struct {
		op string
		in any
	}{
		{"add_order_item", orders.AddOrderItemInput{OrderID: id(b["id"]), Item: s.line()}},
		{"remove_order_item", editArgs("remove_order_item", editInput(b))},
		{"set_order_item_staff", editArgs("set_order_item_staff", editInput(b))},
		{"reprice_order_item", editArgs("reprice_order_item", editInput(b))},
		{"merge_orders", mergeInput(b, c)},
	} {
		t.Run(tc.op, func(t *testing.T) {
			before := s.snapshot()
			r := s.HTTP(t, &caller, tc.op, tc.in)
			if r.Status != 409 || r.Body["detail"] != "单已合并，请在合并后的单上操作" || before != s.snapshot() {
				t.Fatal(r, "unexpected write")
			}
		})
	}
}

func TestMergeEditScopeWindow(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(s.line(1)), s.customerOrder(s.line())
	caller := s.MustCaller(s.Owner)
	caller.StaffID = s.employee
	caller.Permissions[perm.OrderEdit] = ops.Grant{Scope: ops.ScopeSelf}
	s.assertMergeRefused(caller, mergeInput(a, b), 403, "")
	s.assertMergeRefused(caller, mergeInput(b, a), 403, "")
	caller = s.MustCaller(s.Owner)
	old := s.openAt(caller, time.Now().AddDate(0, 0, -2), s.line())
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE orders SET customer_id=$1 WHERE id=$2", s.customer, old["id"])
	dbtest.Commit(t, tx)
	caller.Permissions[perm.OrderEdit] = ops.Grant{Scope: ops.ScopeStore, WindowDays: new(0)}
	// 只放旧单在列表里；先检查窗口，不能先报营业日不同。
	s.assertMergeRefused(caller, mergeInput(a, old), 403, "")
	s.assertMergeRefused(caller, mergeInput(old, a), 403, "")
}

func TestMergeAfterCustomersMerge(t *testing.T) {
	s := open(t)
	a := s.customerOrder(s.line())
	b := s.call("open_order", s.opening(s.line()), 200)
	s.call("merge_customers", map[string]any{"keep_id": s.customer, "merged_id": b["customer_id"]}, 200)
	after := s.call("merge_orders", mergeInput(a, b), 200)
	if len(lines(after)) != 2 || after["receivable"] != "200.00" {
		t.Fatal(after)
	}
}

func TestMergeDatabaseConstraints(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(s.line()), s.customerOrder()
	for _, set := range []string{"status='merged'", "merged_into='" + b["id"].(string) + "'", "status='merged',merged_into=id"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "UPDATE orders SET "+set+" WHERE id=$1", a["id"])
		var pgErr interface{ SQLState() string }
		if !errors.As(err, &pgErr) || pgErr.SQLState() != "23514" {
			t.Fatal(set, err)
		}
		if err := tx.Rollback(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE order_items SET order_id=$1 WHERE order_id=$2", b["id"], a["id"])
	dbtest.Commit(t, tx)
}

func TestOrderMergeMigrationDown(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(s.line()), s.customerOrder(s.line())
	s.call("merge_orders", mergeInput(a, b), 200)
	if err := db.MigrateDown(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM orders WHERE status='open'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	dbtest.Exec(t, tx, "UPDATE orders SET gender=gender")
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name='orders' AND column_name='merged_into'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), "UPDATE order_items SET order_id=order_id")
	var pgErr interface{ SQLState() string }
	if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
}

func waitCount(t *testing.T, s *fixture, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var count int
		if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("requests did not wait for locks")
}

func TestConcurrentMerges(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(s.line()), s.customerOrder(s.line())
	if strings.Compare(a["id"].(string), b["id"].(string)) > 0 {
		a, b = b, a
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", a["id"])
	caller := s.MustCaller(s.Owner)
	done := make(chan optest.Result, 2)
	go func() { done <- s.HTTP(t, &caller, "merge_orders", mergeInput(a, b)) }()
	waitCount(t, s, 1)
	go func() { done <- s.HTTP(t, &caller, "merge_orders", mergeInput(b, a)) }()
	waitCount(t, s, 2)
	dbtest.Commit(t, tx)
	statuses := map[int]int{}
	for range 2 {
		select {
		case r := <-done:
			statuses[r.Status]++
			if r.Status != 200 && (r.Status != 409 || r.Body["detail"] != "单已合并，请在合并后的单上操作") {
				t.Fatal(r)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("merges did not finish")
		}
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatal(statuses)
	}
}

func TestMergeBlocksAdd(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(s.line()), s.customerOrder(s.line(1))
	// 阻塞较大的单，让合并先持有较小的单锁。
	if strings.Compare(a["id"].(string), b["id"].(string)) < 0 {
		a, b = b, a
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", a["id"])
	caller := s.MustCaller(s.Owner)
	mergeDone, addDone := make(chan optest.Result, 1), make(chan optest.Result, 1)
	go func() { mergeDone <- s.HTTP(t, &caller, "merge_orders", mergeInput(a, b)) }()
	waitCount(t, s, 1)
	go func() {
		addDone <- s.HTTP(t, &caller, "add_order_item", orders.AddOrderItemInput{OrderID: id(b["id"]), Item: s.line()})
	}()
	waitCount(t, s, 2)
	dbtest.Commit(t, tx)
	if r := <-mergeDone; r.Status != 200 {
		t.Fatal(r)
	}
	if r := <-addDone; r.Status != 409 || r.Body["detail"] != "单已合并，请在合并后的单上操作" {
		t.Fatal(r)
	}
	kept := s.call("get_order", orders.GetOrderInput{ID: id(a["id"])}, 200)
	merged := s.call("get_order", orders.GetOrderInput{ID: id(b["id"])}, 200)
	want := map[any]any{}
	for _, order := range []map[string]any{a, b} {
		for _, line := range lines(order) {
			want[line.(map[string]any)["id"]] = line
		}
	}
	if len(lines(kept)) != len(want) || len(lines(merged)) != 0 {
		t.Fatal("unexpected lines after refused add", kept, merged)
	}
	for _, line := range lines(kept) {
		if !reflect.DeepEqual(line, want[line.(map[string]any)["id"]]) {
			t.Fatal("line changed or added", line, want)
		}
	}
	check := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var count int
	if err := check.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='add_order_item' AND target_id=ANY($1::uuid[])", []uuid.UUID{id(a["id"]), id(b["id"])}).Scan(&count); err != nil || count != 0 {
		t.Fatal("unexpected add audit", count, err)
	}
	if err := check.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMergeWaitsForAdd(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(), s.customerOrder()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", s.item)
	caller := s.MustCaller(s.Owner)
	addDone, mergeDone := make(chan optest.Result, 1), make(chan optest.Result, 1)
	go func() {
		addDone <- s.HTTP(t, &caller, "add_order_item", orders.AddOrderItemInput{OrderID: id(b["id"]), Item: s.line()})
	}()
	waitCount(t, s, 1)
	go func() { mergeDone <- s.HTTP(t, &caller, "merge_orders", mergeInput(a, b)) }()
	waitCount(t, s, 2)
	dbtest.Commit(t, tx)
	if r := <-addDone; r.Status != 200 {
		t.Fatal(r)
	}
	if r := <-mergeDone; r.Status != 200 || len(lines(r.Body)) != 1 || r.Body["receivable"] != "100.00" {
		t.Fatal(r)
	}
}

func TestMergeLineStore(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(), s.customerOrder(s.line())
	store := s.otherStore()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var lineID uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO order_items (tenant_id,store_id,order_id,item_id,item_name,price,price_source,added_by) VALUES ($1,$2,$3,$4,'别店项目',1,'standard',$5) RETURNING id`, s.Res.TenantID, store, b["id"], s.item, s.Res.OwnerID).Scan(&lineID); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	s.call("merge_orders", mergeInput(a, b), 200)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var orderID uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT order_id FROM order_items WHERE id=$1", lineID).Scan(&orderID); err != nil || orderID != id(b["id"]) {
		t.Fatal(orderID, err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCustomerMergeOrderLockOrder(t *testing.T) {
	s := open(t)
	a, b := s.customerOrder(s.line()), s.customerOrder(s.line())
	if strings.Compare(a["id"].(string), b["id"].(string)) > 0 {
		a, b = b, a
	}
	customer := s.call("create_customer", map[string]any{"name": "保留顾客"}, 200)["customer"].(map[string]any)["id"]
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	// 两条路径都应先等较小的单；顾客改挂不能提前拿较大的单锁。
	dbtest.Exec(t, tx, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", a["id"])
	caller := s.MustCaller(s.Owner)
	customersDone, ordersDone := make(chan optest.Result, 1), make(chan optest.Result, 1)
	go func() {
		customersDone <- s.HTTP(t, &caller, "merge_customers", map[string]any{"keep_id": customer, "merged_id": s.customer})
	}()
	waitCount(t, s, 1)
	probe := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, lockErr := probe.Exec(context.Background(), "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE NOWAIT", b["id"])
	if err := probe.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() { ordersDone <- s.HTTP(t, &caller, "merge_orders", mergeInput(a, b)) }()
	waitCount(t, s, 2)
	dbtest.Commit(t, tx)
	cr, or := <-customersDone, <-ordersDone
	if lockErr != nil || cr.Status != 200 || or.Status != 200 || or.Body["customer_id"] != customer {
		t.Fatal(lockErr, cr, or)
	}
}

func TestMergeListBounds(t *testing.T) {
	s := open(t)
	keep := s.customerOrder(s.line())
	caller := s.MustCaller(s.Owner)
	for _, n := range []int{0, 11} {
		ids := make([]uuid.UUID, 0, n)
		for range n {
			ids = append(ids, id(s.customerOrder(s.line())["id"]))
		}
		s.assertMergeRefused(caller, orders.MergeOrdersInput{OrderID: id(keep["id"]), MergedOrderIDs: ids}, 422, "")
	}
}
