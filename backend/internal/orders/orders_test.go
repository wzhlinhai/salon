package orders_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type fixture struct {
	*optest.Shop
	t        *testing.T
	item     uuid.UUID
	position uuid.UUID
	level    uuid.UUID
	employee uuid.UUID
	customer uuid.UUID
}

func open(t *testing.T) *fixture {
	t.Helper()
	s := &fixture{Shop: optest.OpenShop(t), t: t}
	s.customer = id(s.call("create_customer", map[string]any{"name": "顾客", "phone": "13800000001"}, 200)["customer"].(map[string]any)["id"])
	s.position = id(s.call("create_position", map[string]any{"name": "大工"}, 200)["id"])
	s.level = id(s.call("create_level", map[string]any{"name": "首席"}, 200)["id"])
	s.employee, _ = s.Hire("首席员工", "002", "stylist")
	s.call("update_staff", map[string]any{"staff_id": s.employee, "name": "首席员工", "number": "002", "level_id": s.level, "position_id": nil}, 200)
	s.item = id(s.call("create_item", s.itemInput("剪发", 3), 200)["id"])
	return s
}
func id(v any) uuid.UUID { return uuid.MustParse(v.(string)) }
func (s *fixture) call(op string, in any, want int) map[string]any {
	s.t.Helper()
	return s.Call(s.t, s.Owner, op, in, want).Body
}
func (s *fixture) itemInput(name string, slots int) map[string]any {
	positions := []any{}
	for range slots {
		positions = append(positions, map[string]any{"position_id": s.position, "required": true})
	}
	return map[string]any{"name": name, "price": "100.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": positions, "level_prices": []any{map[string]any{"level_id": s.level, "price": "150.00"}}}
}
func (s *fixture) line(slots ...int16) orders.OrderItemInput {
	staff := []orders.OrderItemStaffInput{}
	for _, slot := range slots {
		staff = append(staff, orders.OrderItemStaffInput{Slot: slot, StaffID: s.employee})
	}
	return orders.OrderItemInput{ItemID: s.item, Staff: staff}
}
func (s *fixture) opening(items ...orders.OrderItemInput) orders.OpenOrderInput {
	if items == nil {
		items = []orders.OrderItemInput{}
	}
	return orders.OpenOrderInput{Gender: "female", Designated: true, Items: items}
}
func lines(o map[string]any) []any { return o["items"].([]any) }

func (s *fixture) snapshot() string {
	s.t.Helper()
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "orders", "order_items", "order_item_positions", "customers", "audit_log", "settings_snapshots")
}
func snapshotTx(t *testing.T, tx pgx.Tx) string {
	t.Helper()
	return dbtest.SnapshotTx(t, tx, "orders", "order_items", "order_item_positions", "customers", "audit_log", "settings_snapshots")
}

func (s *fixture) otherStore() uuid.UUID {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "别店"})
	if err != nil {
		s.t.Fatal(err)
	}
	dbtest.Commit(s.t, tx)
	return store.ID
}

func TestOpenOrder(t *testing.T) {
	s := open(t)
	assistant, _ := s.Hire("助理", "007", "assistant")
	junior, _ := s.Hire("小工", "008", "junior")
	line := s.line(1, 2, 3)
	line.Staff[1].StaffID = assistant
	line.Staff[2].StaffID = junior
	in := s.opening(line)
	in.CustomerID = &s.customer
	o := s.call("open_order", in, 200)
	if o["customer_id"] != s.customer.String() || o["gender"] != "female" || o["designated"] != true || o["status"] != "open" || o["opened_by"] != s.Res.OwnerID.String() || o["opened_at"] == nil || o["receivable"] != "150.00" {
		t.Fatal(o)
	}
	p := lines(o)[0].(map[string]any)["positions"].([]any)
	if len(p) != 3 {
		t.Fatal(p)
	}
	for i, v := range p {
		row := v.(map[string]any)
		if row["slot"] != float64(i+1) || row["position_id"] != s.position.String() || row["required"] != true || row["staff_id"] != []uuid.UUID{s.employee, assistant, junior}[i].String() {
			t.Fatal(row)
		}
	}
	for _, key := range []string{"phone", "name", "customer", "tenant_id", "store_id", "settings_snapshot_id"} {
		if _, ok := o[key]; ok {
			t.Fatal("unexpected output", key)
		}
	}
	caller := s.MustCaller(s.Owner)
	delete(caller.Permissions, perm.CustomerEdit)
	r := s.HTTP(t, &caller, "open_order", s.opening())
	if r.Status != 200 {
		t.Fatal(r)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var name, phone *string
	var gender string
	var createdBy uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT name,phone,gender,created_by FROM customers WHERE id=$1", r.Body["customer_id"]).Scan(&name, &phone, &gender, &createdBy); err != nil {
		t.Fatal(err)
	}
	if name != nil || phone != nil || gender != "female" || createdBy != caller.StaffID {
		t.Fatal(name, phone, gender, createdBy)
	}
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_id=$1 AND action='create_customer' AND target_kind='customer' AND customer_id=target_id AND before IS NULL AND after->>'gender'='female'", r.Body["customer_id"]).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("missing customer creation audit", count)
	}
}

func TestCustomerProtection(t *testing.T) {
	s := open(t)
	store := s.otherStore()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	other, err := dbgen.New(tx).CreateCustomer(context.Background(), dbgen.CreateCustomerParams{TenantID: s.Res.TenantID, StoreID: store, CreatedBy: s.Res.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	for _, tc := range []struct {
		name     string
		customer uuid.UUID
		self     bool
		want     int
	}{{"other store", other.ID, false, 404}, {"outside scope", s.customer, true, 404}} {
		t.Run(tc.name, func(t *testing.T) {
			s := *s
			s.t = t
			caller := s.MustCaller(s.Owner)
			if tc.self {
				role := s.call("create_role", map[string]any{"name": "本人顾客", "permissions": []any{map[string]any{"permission": "customer.view", "scope": "self"}, map[string]any{"permission": "order.create"}}}, 200)
				_, token := s.Hire("本人范围", "003", "self", id(role["id"]))
				caller = s.MustCaller(token)
			}
			before := s.snapshot()
			in := s.opening()
			in.CustomerID = &tc.customer
			r := s.HTTP(t, &caller, "open_order", in)
			if r.Status != tc.want || before != s.snapshot() {
				t.Fatal(r, "unexpected write")
			}
		})
	}
	s.call("merge_customers", map[string]any{"keep_id": other.ID, "merged_id": s.customer}, 200)
	before := s.snapshot()
	in := s.opening()
	in.CustomerID = &s.customer
	s.call("open_order", in, 409)
	if before != s.snapshot() {
		t.Fatal("merged customer wrote rows")
	}
}

func TestPrices(t *testing.T) {
	s := open(t)
	noSlots := id(s.call("create_item", s.itemInput("无岗位", 0), 200)["id"])
	noLevel := id(s.call("create_level", map[string]any{"name": "设计师"}, 200)["id"])
	another, _ := s.Hire("设计师员工", "004", "designer")
	s.call("update_staff", map[string]any{"staff_id": another, "name": "设计师员工", "number": "004", "level_id": noLevel, "position_id": nil}, 200)
	for _, tc := range []struct {
		name          string
		line          orders.OrderItemInput
		price, source string
		level         any
	}{{"without positions", orders.OrderItemInput{ItemID: noSlots, Staff: []orders.OrderItemStaffInput{}}, "100.00", "standard", nil}, {"slot one", s.line(1), "150.00", "level", s.level.String()}, {"empty slot one", s.line(), "100.00", "standard", nil}, {"only slot two", s.line(2), "100.00", "standard", nil}, {"no level price", orders.OrderItemInput{ItemID: s.item, Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: another}}}, "100.00", "standard", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			s := *s
			s.t = t
			o := s.call("open_order", s.opening(tc.line), 200)
			line := lines(o)[0].(map[string]any)
			if line["price"] != tc.price || line["price_source"] != tc.source || line["level_id"] != tc.level || o["receivable"] != tc.price {
				t.Fatal(o)
			}
		})
	}
	o := s.call("open_order", s.opening(s.line(1, 2, 3), s.line(2)), 200)
	if o["receivable"] != "250.00" || len(lines(o)) != 2 {
		t.Fatal(o)
	}
}

func TestValidationRollback(t *testing.T) {
	s := open(t)
	disabledIn := s.itemInput("停用项目", 1)
	disabled := s.call("create_item", disabledIn, 200)
	disabledIn["id"] = disabled["id"]
	disabledIn["active"] = false
	s.call("update_item", disabledIn, 200)
	for _, tc := range []struct {
		name string
		line orders.OrderItemInput
		want int
	}{{"unknown item", orders.OrderItemInput{ItemID: uuid.New(), Staff: []orders.OrderItemStaffInput{}}, 404}, {"inactive item", orders.OrderItemInput{ItemID: id(disabled["id"]), Staff: []orders.OrderItemStaffInput{}}, 409}, {"unknown slot", s.line(4), 409}, {"duplicate slot", s.line(1, 1), 409}, {"unknown staff", orders.OrderItemInput{ItemID: s.item, Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: uuid.New()}}}, 409}} {
		t.Run(tc.name, func(t *testing.T) {
			s := *s
			s.t = t
			before := s.snapshot()
			s.call("open_order", s.opening(s.line(), tc.line), tc.want)
			if before != s.snapshot() {
				t.Fatal("failed opening left rows including auto customer")
			}
		})
	}
}

func TestReadOrders(t *testing.T) {
	s := open(t)
	first := s.call("open_order", s.opening(s.line()), 200)
	second := s.call("open_order", s.opening(), 200)
	store := s.otherStore()
	caller := s.MustCaller(s.Owner)
	caller.StoreID = store
	foreign := s.HTTP(t, &caller, "open_order", s.opening())
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			caller := s.MustCaller(s.Owner)
			delete(caller.Permissions, perm.CustomerView)
			delete(caller.Permissions, perm.CustomerFullPhone)
			r := ch.Call(t, caller, "list_open_orders", orders.ListOpenOrdersInput{})
			if r.Status != 200 || len(lines(r.Body)) != 2 || !reflect.DeepEqual(lines(r.Body), []any{first, second}) {
				t.Fatal(r)
			}
			r = ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(first["id"])})
			if r.Status != 200 || !reflect.DeepEqual(r.Body, first) {
				t.Fatal(r)
			}
			for _, oid := range []uuid.UUID{id(foreign.Body["id"]), uuid.New()} {
				if r := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: oid}); r.Status != 404 {
					t.Fatal(r)
				}
			}
		})
	}
	before := s.snapshot()
	for _, oid := range []uuid.UUID{id(foreign.Body["id"]), uuid.New()} {
		s.call("add_order_item", orders.AddOrderItemInput{OrderID: oid, Item: s.line()}, 404)
	}
	if before != s.snapshot() {
		t.Fatal("missing order wrote rows")
	}
}

func TestAddItemAuditAndMerge(t *testing.T) {
	s := open(t)
	in := s.opening()
	in.CustomerID = &s.customer
	before := s.call("open_order", in, 200)
	after := s.call("add_order_item", orders.AddOrderItemInput{OrderID: id(before["id"]), Item: s.line(1)}, 200)
	if len(lines(after)) != 1 || after["receivable"] != "150.00" {
		t.Fatal(after)
	}
	entries := s.call("list_audit_log", map[string]any{"target_id": before["id"]}, 200)["entries"].([]any)
	if len(entries) != 2 {
		t.Fatal(entries)
	}
	for i, v := range entries {
		e := v.(map[string]any)
		if e["target_kind"] != "order" || e["target_id"] != before["id"] {
			t.Fatal(e)
		}
		if i == 0 && (e["action"] != "add_order_item" || !reflect.DeepEqual(e["before"], before) || !reflect.DeepEqual(e["after"], after)) {
			t.Fatal(e)
		}
		if i == 1 && (e["action"] != "open_order" || e["before"] != nil || !reflect.DeepEqual(e["after"], before)) {
			t.Fatal(e)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_id=$1 AND customer_id=$2", before["id"], s.customer).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal(count)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	caller := s.MustCaller(s.Owner)
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": before["id"]})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
	keep := s.call("create_customer", map[string]any{"name": "保留顾客"}, 200)["customer"].(map[string]any)["id"]
	s.call("merge_customers", map[string]any{"keep_id": keep, "merged_id": s.customer}, 200)
	got := s.call("get_order", orders.GetOrderInput{ID: id(before["id"])}, 200)
	if got["customer_id"] != keep {
		t.Fatal(got)
	}
	// 加项目不读顾客：已合并后沿当前单的顾客引用继续操作。
	s.call("add_order_item", orders.AddOrderItemInput{OrderID: id(before["id"]), Item: s.line()}, 200)
}

func TestSnapshotAndBusinessDate(t *testing.T) {
	s := open(t)
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	now := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	caller := s.MustCaller(s.Owner)
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		o, err := orders.OpenOrderAtForTest(context.Background(), ops.Env{Caller: caller, Q: q}, new(s.opening()), now)
		if err != nil {
			return err
		}
		row, err := q.GetOrder(context.Background(), dbgen.GetOrderParams{StoreID: s.Res.StoreID, ID: o.ID})
		if err != nil {
			return err
		}
		snapshot, err := settings.TakeSnapshot(context.Background(), q, s.Res.TenantID, s.Res.StoreID, now)
		if err != nil {
			return err
		}
		businessDate, err := settings.BusinessDate(context.Background(), q, s.Res.StoreID, now)
		if err != nil {
			return err
		}
		if row.SettingsSnapshotID != snapshot.ID || o.BusinessDate != day.Format(time.DateOnly) || o.BusinessDate != businessDate.Format(time.DateOnly) {
			t.Fatal(row, o, snapshot, businessDate)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPermissions(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(), 200)
	caller := s.MustCaller(s.Owner)
	delete(caller.Permissions, perm.OrderCreate)
	delete(caller.Permissions, perm.OrderEdit)
	for _, tc := range []struct {
		op string
		in any
	}{{"merge_orders", orders.MergeOrdersInput{OrderID: id(o["id"]), MergedOrderIDs: []uuid.UUID{uuid.New()}}}, {"open_order", s.opening()}, {"add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()}}, {"list_open_orders", orders.ListOpenOrdersInput{}}, {"get_order", orders.GetOrderInput{ID: id(o["id"])}}} {
		t.Run(tc.op, func(t *testing.T) {
			before := s.snapshot()
			r := s.HTTP(t, &caller, tc.op, tc.in)
			if r.Status != 403 {
				t.Fatal(r)
			}
			if before != s.snapshot() {
				t.Fatal("forbidden op changed data")
			}
			if tc.op == "get_order" || tc.op == "list_open_orders" {
				for _, ch := range s.Channels() {
					if r := ch.Call(t, caller, tc.op, tc.in); r.Status != 403 {
						t.Fatal(r)
					}
				}
			}
		})
	}
	editable := s.call("open_order", s.opening(s.line()), 200)
	for _, permission := range []ops.Permission{perm.OrderEdit, perm.OrderReprice} {
		role := s.call("create_role", map[string]any{"name": string(permission), "permissions": []any{map[string]any{"permission": permission}}}, 200)
		_, token := s.Hire(string(permission), string(permission), string(permission), id(role["id"]))
		for _, op := range []string{"remove_order_item", "set_order_item_staff", "reprice_order_item"} {
			want := 403
			if (permission == perm.OrderReprice) == (op == "reprice_order_item") {
				want = 200
			}
			before := s.snapshot()
			r := s.HTTPWithToken(t, token, op, editArgs(op, editInput(editable)))
			if r.Status != want {
				t.Fatal(permission, op, r)
			}
			if want == 403 && before != s.snapshot() {
				t.Fatal("forbidden edit changed data")
			}
			if want == 200 {
				editable = s.call("open_order", s.opening(s.line()), 200)
			}
		}
	}
	caller = s.MustCaller(s.Owner)
	delete(caller.Permissions, perm.OrderEdit)
	delete(caller.Permissions, perm.OrderReprice)
	for _, op := range []string{"remove_order_item", "set_order_item_staff", "reprice_order_item"} {
		before := s.snapshot()
		if r := s.HTTP(t, &caller, op, editArgs(op, editInput(editable))); r.Status != 403 {
			t.Fatal(op, r)
		}
		if before != s.snapshot() {
			t.Fatal("forbidden edit wrote data")
		}
	}
}
func TestAIWritesRefused(t *testing.T) {
	s := open(t)
	o, merged := s.customerOrder(s.line()), s.customerOrder(s.line())
	before := s.snapshot()
	for _, tc := range []struct {
		op string
		in any
	}{{"merge_orders", mergeInput(o, merged)}, {"open_order", s.opening(s.line())}, {"add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()}}, {"remove_order_item", editInput(o)}, {"set_order_item_staff", editInput(o)}, {"reprice_order_item", editInput(o)}} {
		r := s.AI(t, s.MustCaller(s.Owner), tc.op, editArgs(tc.op, tc.in))
		if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) || r.Err.Error() != "operation requires a draft confirmed by a person" || before != s.snapshot() {
			t.Fatal(tc.op, r, "expected AI draft refusal and unchanged data")
		}
	}
}

func TestItemLock(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", s.item)
	dbtest.Exec(t, tx, "UPDATE items SET price=222.22 WHERE id=$1", s.item)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	select {
	case r := <-done:
		t.Fatal("add did not wait for item", r)
	default:
	}
	dbtest.Commit(t, tx)
	r := <-done
	if r.Status != 200 || lines(r.Body)[0].(map[string]any)["price"] != "222.22" {
		t.Fatal(r)
	}
}

func TestOrderLock(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", s.item)
	done := make(chan optest.Result, 2)
	caller := s.MustCaller(s.Owner)
	go func() {
		done <- s.HTTP(t, &caller, "add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	go func() {
		done <- s.HTTP(t, &caller, "add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()})
	}()
	// 两个请求分别等待项目和单锁；移除单锁后两者都会在读完空 before 后等待项目。
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("two requests did not wait")
		case <-ticker.C:
		}
	}
	dbtest.Commit(t, tx)
	for range 2 {
		if r := <-done; r.Status != 200 {
			t.Fatal(r)
		}
	}
	after := s.call("get_order", orders.GetOrderInput{ID: id(o["id"])}, 200)
	if len(lines(after)) != 2 || after["receivable"] != "200.00" {
		t.Fatal(after)
	}
	entries := s.call("list_audit_log", map[string]any{"target_id": o["id"]}, 200)["entries"].([]any)
	if len(entries) != 3 {
		t.Fatal(entries)
	}
	last, first := entries[0].(map[string]any), entries[1].(map[string]any)
	if len(lines(last["before"].(map[string]any))) != 1 || !reflect.DeepEqual(last["before"], first["after"]) || len(lines(first["before"].(map[string]any))) != 0 {
		t.Fatal(entries)
	}
}

func TestDatabaseProtections(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line(1)), 200)
	for _, table := range []string{"orders", "order_items", "order_item_positions"} {
		t.Run("DELETE"+table, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), "DELETE FROM "+table)
			var pgErr interface{ SQLState() string }
			if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
				t.Fatal(err)
			}
			if err := tx.Rollback(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		source string
		level  any
	}{{"level", nil}, {"standard", s.level}, {"member", nil}} {
		t.Run(fmt.Sprint(tc.source, tc.level), func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), `INSERT INTO order_items (tenant_id,store_id,order_id,item_id,item_name,price,price_source,level_id,added_by) VALUES ($1,$2,$3,$4,'剪发',1,$5,$6,$7)`, s.Res.TenantID, s.Res.StoreID, o["id"], s.item, tc.source, tc.level, s.Res.OwnerID)
			if err == nil {
				t.Fatal("invalid price source accepted")
			}
			var pgErr interface{ SQLState() string }
			if !errors.As(err, &pgErr) || pgErr.SQLState() != "23514" {
				t.Fatal(err)
			}
			_ = tx.Rollback(context.Background())
		})
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO audit_log (tenant_id,store_id,action,target_id,target_kind,actor_id) VALUES ($1,$2,'open_order',$3,'order',$4)`, s.Res.TenantID, s.Res.StoreID, o["id"], s.Res.OwnerID)
	if err == nil {
		t.Fatal("order audit without customer accepted")
	}
	var pgErr interface{ SQLState() string }
	if !errors.As(err, &pgErr) || pgErr.SQLState() != "23514" {
		t.Fatal(err)
	}
}

func TestCopiedPositions(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line(1)), 200)
	original := lines(o)[0].(map[string]any)
	positions := original["positions"].([]any)
	for _, value := range positions[1:] {
		row := value.(map[string]any)
		if row["staff_id"] != nil || row["required"] != true {
			t.Fatal(row)
		}
	}
	// 修改目录不改变已复制的岗位、员工和行价格。
	update := s.itemInput("剪发", 1)
	update["id"] = s.item
	update["active"] = true
	update["price"] = "300.00"
	update["positions"] = []any{map[string]any{"position_id": s.position, "required": false}}
	s.call("update_item", update, 200)
	got := s.call("get_order", orders.GetOrderInput{ID: id(o["id"])}, 200)
	if !reflect.DeepEqual(lines(got)[0], original) || got["receivable"] != "150.00" {
		t.Fatal(got)
	}
}

func TestAddItemDoesNotLockOtherStoreOrder(t *testing.T) {
	s := open(t)
	caller := s.MustCaller(s.Owner)
	caller.StoreID = s.otherStore()
	foreign := s.HTTP(t, &caller, "open_order", s.opening())
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	before := s.snapshot()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", foreign.Body["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "add_order_item", orders.AddOrderItemInput{OrderID: id(foreign.Body["id"]), Item: s.line()})
	}()
	select {
	case r := <-done:
		if r.Status != 404 {
			t.Fatal(r)
		}
		if err := tx.Rollback(context.Background()); err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		if err := tx.Rollback(context.Background()); err != nil {
			t.Fatal(err)
		}
		r := <-done
		t.Fatalf("add_order_item waited for another store's order lock: %v", r)
	}
	if before != s.snapshot() {
		t.Fatal("foreign order request changed rows")
	}
}

func TestItemNameSnapshot(t *testing.T) {
	s := open(t)
	opened := s.call("open_order", s.opening(s.line()), 200)
	update := s.itemInput("造型", 3)
	update["id"] = s.item
	update["active"] = true
	s.call("update_item", update, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(opened["id"])})
		if r.Status != 200 || !reflect.DeepEqual(r.Body, opened) || lines(r.Body)[0].(map[string]any)["item_name"] != "剪发" {
			t.Fatal(ch.Name, r)
		}
	}
	after := s.call("add_order_item", orders.AddOrderItemInput{OrderID: id(opened["id"]), Item: s.line()}, 200)
	if lines(after)[0].(map[string]any)["item_name"] != "剪发" || lines(after)[1].(map[string]any)["item_name"] != "造型" {
		t.Fatal(after)
	}
	entries := s.call("list_audit_log", map[string]any{"target_id": opened["id"]}, 200)["entries"].([]any)
	if len(entries) != 2 {
		t.Fatal(entries)
	}
	previous, current := entries[1].(map[string]any), entries[0].(map[string]any)
	if !reflect.DeepEqual(previous["after"], opened) || !reflect.DeepEqual(current["before"], previous["after"]) || !reflect.DeepEqual(current["after"], after) {
		t.Fatal(entries)
	}
}

func TestCustomerShareLock(t *testing.T) {
	s := open(t)
	keep := s.call("create_customer", map[string]any{"name": "合并后顾客"}, 200)["customer"].(map[string]any)["id"]
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM customers WHERE id=$1 FOR NO KEY UPDATE", s.customer)
	dbtest.Exec(t, tx, "UPDATE customers SET merged_into=$2 WHERE id=$1", s.customer, keep)
	expected := snapshotTx(t, tx)
	in := s.opening(s.line())
	in.CustomerID = &s.customer
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "open_order", in) }()
	dbtest.WaitForLockWait(t, s.Pool)
	select {
	case r := <-done:
		t.Fatal("open_order did not wait for customer", r)
	default:
	}
	dbtest.Commit(t, tx)
	r := <-done
	if r.Status != 409 || r.Body["detail"] != "该顾客已合并到另一位顾客" {
		t.Fatal(r)
	}
	if expected != s.snapshot() {
		t.Fatal("opening a merged customer wrote rows")
	}
}

func TestStaffShareLock(t *testing.T) {
	s := open(t)
	opened := s.call("open_order", s.opening(), 200)
	before := s.snapshot()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM staff WHERE id=$1 FOR NO KEY UPDATE", s.employee)
	dbtest.Exec(t, tx, "UPDATE staff SET active=false,deactivated_at=clock_timestamp() WHERE id=$1", s.employee)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "add_order_item", orders.AddOrderItemInput{OrderID: id(opened["id"]), Item: s.line(2)})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	select {
	case r := <-done:
		t.Fatal("add_order_item did not wait for slot two staff", r)
	default:
	}
	dbtest.Commit(t, tx)
	r := <-done
	if r.Status != 409 || r.Body["detail"] != "岗位上的员工必须是本店在职员工" {
		t.Fatal(r)
	}
	if before != s.snapshot() {
		t.Fatal("deactivated staff request wrote rows")
	}
}

func TestItemNameConstraints(t *testing.T) {
	s := open(t)
	opened := s.call("open_order", s.opening(), 200)
	for _, tc := range []struct {
		name any
		code string
	}{{nil, "23502"}, {"", "23514"}} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO order_items (tenant_id,store_id,order_id,item_id,item_name,price,price_source,added_by) VALUES ($1,$2,$3,$4,$5,1,'standard',$6)`, s.Res.TenantID, s.Res.StoreID, opened["id"], s.item, tc.name, s.Res.OwnerID)
		var pgErr interface{ SQLState() string }
		if !errors.As(err, &pgErr) || pgErr.SQLState() != tc.code {
			t.Fatal(tc, err)
		}
		if err := tx.Rollback(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNonPricingStaffValidation(t *testing.T) {
	s := open(t)
	inactive, _ := s.Hire("离职员工", "005", "inactive")
	s.call("deactivate_staff", map[string]any{"staff_id": inactive}, 200)
	store := s.otherStore()
	caller := s.MustCaller(s.Owner)
	caller.StoreID = store
	other := s.HTTP(t, &caller, "create_staff", map[string]any{"name": "别店员工", "number": "006", "role_ids": []uuid.UUID{}})
	if other.Status != 200 {
		t.Fatal(other)
	}

	for _, tc := range []struct {
		name    string
		staffID uuid.UUID
	}{{"inactive", inactive}, {"other store", id(other.Body["id"])}} {
		t.Run(tc.name, func(t *testing.T) {
			s := *s
			s.t = t
			before := s.snapshot()
			line := orders.OrderItemInput{ItemID: s.item, Staff: []orders.OrderItemStaffInput{{Slot: 2, StaffID: tc.staffID}}}
			r := s.HTTPWithToken(t, s.Owner, "open_order", s.opening(s.line(), line))
			if r.Status != 409 || r.Body["detail"] != "岗位上的员工必须是本店在职员工" {
				t.Fatal(r)
			}
			if before != s.snapshot() {
				t.Fatal("invalid slot two staff left rows including auto customer")
			}
		})
	}
}
