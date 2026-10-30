package orders_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

func editInput(o map[string]any) map[string]any {
	return map[string]any{"order_id": o["id"], "order_item_id": lines(o)[0].(map[string]any)["id"], "staff": []orders.OrderItemStaffInput{}, "price": "88.00", "reason": "  现场改价  "}
}

// editArgs 构造各操作自己的请求，保持 schema 的未知字段检查。
func editArgs(op string, in any) any {
	m, ok := in.(map[string]any)
	if !ok {
		return in
	}
	out := map[string]any{"order_id": m["order_id"], "order_item_id": m["order_item_id"]}
	switch op {
	case "remove_order_item":
		return out
	case "set_order_item_staff":
		out["staff"] = m["staff"]
		return out
	case "reprice_order_item":
		out["price"] = m["price"]
		out["reason"] = m["reason"]
		return out
	default:
		return in
	}
}
func (s *fixture) edit(op string, in any, want int) map[string]any {
	s.t.Helper()
	return s.call(op, editArgs(op, in), want)
}
func firstLine(o map[string]any) map[string]any { return lines(o)[0].(map[string]any) }
func (s *fixture) assertUnchanged(op string, in any, want int, detail string) {
	s.t.Helper()
	before := s.snapshot()
	r := s.edit(op, in, want)
	if r["detail"] != detail {
		s.t.Fatal(r)
	}
	if before != s.snapshot() {
		s.t.Fatal("refused edit changed data", op)
	}
}
func (s *fixture) assertEditAudit(op string, before, after map[string]any) {
	s.t.Helper()
	entries := s.call("list_audit_log", map[string]any{"target_id": before["id"]}, 200)["entries"].([]any)
	e := entries[0].(map[string]any)
	if e["action"] != op || e["target_kind"] != "order" || !reflect.DeepEqual(e["before"], before) || !reflect.DeepEqual(e["after"], after) {
		s.t.Fatal(entries)
	}
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_id=$1 AND action=$2 AND target_kind='order' AND customer_id=$3", before["id"], op, before["customer_id"]).Scan(&count); err != nil {
		s.t.Fatal(err)
	}
	if count != 1 {
		s.t.Fatal(count)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		s.t.Fatal(err)
	}
	caller := s.MustCaller(s.Owner)
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		r := ch.Call(s.t, caller, "list_audit_log", map[string]any{"target_id": before["id"]})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
			s.t.Fatal(r)
		}
	}
}

func TestRemoveOrderItem(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line(1), s.line()), 200)
	in := editInput(o)
	after := s.edit("remove_order_item", in, 200)
	if len(lines(after)) != 2 || firstLine(after)["removed_at"] == nil || firstLine(after)["removed_by"] != s.Res.OwnerID.String() || after["receivable"] != "100.00" {
		t.Fatal(after)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(o["id"])})
		if r.Status != 200 || !reflect.DeepEqual(r.Body, after) {
			t.Fatal(r)
		}
	}
	s.assertEditAudit("remove_order_item", o, after)
	s.assertUnchanged("remove_order_item", in, 409, "项目已去掉")
	s.assertUnchanged("set_order_item_staff", in, 409, "项目已去掉")
	s.assertUnchanged("reprice_order_item", in, 409, "项目已去掉")
}

func TestEditLineIsolation(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line()), 200)
	another := s.call("open_order", s.opening(s.line()), 200)
	caller := s.MustCaller(s.Owner)
	caller.StoreID = s.otherStore()
	foreign := s.HTTP(t, &caller, "open_order", s.opening())
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	for _, op := range []string{"remove_order_item", "set_order_item_staff", "reprice_order_item"} {
		for _, orderID := range []any{another["id"], foreign.Body["id"], uuid.New()} {
			in := editInput(o)
			in["order_id"] = orderID
			s.assertUnchanged(op, in, 404, "not found")
		}
	}
}

func TestSetOrderItemStaffPrices(t *testing.T) {
	s := open(t)
	another, _ := s.Hire("无级别价", "003", "plain")
	o := s.call("open_order", s.opening(s.line()), 200)
	in := editInput(o)
	for _, tc := range []struct {
		staff         []orders.OrderItemStaffInput
		price, source string
		level         any
	}{
		{[]orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}, {Slot: 2, StaffID: another}}, "150.00", "level", s.level.String()},
		{[]orders.OrderItemStaffInput{{Slot: 1, StaffID: another}}, "100.00", "standard", nil},
		{[]orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}, "150.00", "level", s.level.String()},
		{[]orders.OrderItemStaffInput{}, "100.00", "standard", nil},
	} {
		in["staff"] = tc.staff
		before := o
		o = s.edit("set_order_item_staff", in, 200)
		line := firstLine(o)
		if line["price"] != tc.price || line["price_source"] != tc.source || line["level_id"] != tc.level {
			t.Fatal(o)
		}
		if len(tc.staff) == 2 {
			s.assertEditAudit("set_order_item_staff", before, o)
		}
		if len(tc.staff) < 2 && line["positions"].([]any)[1].(map[string]any)["staff_id"] != nil {
			t.Fatal("unlisted slot was not cleared", o)
		}
	}
	update := s.itemInput("剪发", 3)
	update["id"] = s.item
	update["active"] = true
	update["price"] = "300.00"
	s.call("update_item", update, 200)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 2, StaffID: s.employee}}
	o = s.edit("set_order_item_staff", in, 200)
	if firstLine(o)["price"] != "100.00" {
		t.Fatal("slot two requoted", o)
	}
	s.edit("reprice_order_item", in, 200)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}
	after := s.edit("set_order_item_staff", in, 200)
	if firstLine(after)["price"] != "88.00" || firstLine(after)["price_source"] != "manual" || firstLine(after)["reprice_reason"] != "现场改价" {
		t.Fatal(after)
	}
}

func TestSetOrderItemStaffValidation(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line(1, 2)), 200)
	update := s.itemInput("剪发", 1)
	update["id"] = s.item
	update["active"] = true
	s.call("update_item", update, 200)
	in := editInput(o)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 2, StaffID: s.employee}}
	s.edit("set_order_item_staff", in, 200)
	short := s.call("open_order", s.opening(s.line()), 200)
	update["positions"] = s.itemInput("剪发", 4)["positions"]
	s.call("update_item", update, 200)
	shortIn := editInput(short)
	shortIn["staff"] = []orders.OrderItemStaffInput{{Slot: 2, StaffID: s.employee}}
	s.assertUnchanged("set_order_item_staff", shortIn, 409, "项目没有这个岗位")
	inactive, _ := s.Hire("离职", "003", "inactive")
	s.call("deactivate_staff", map[string]any{"staff_id": inactive}, 200)
	caller := s.MustCaller(s.Owner)
	caller.StoreID = s.otherStore()
	foreign := s.HTTP(t, &caller, "create_staff", map[string]any{"name": "外店", "number": "004", "role_ids": []uuid.UUID{}})
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	for _, tc := range []struct {
		staff      []orders.OrderItemStaffInput
		wantDetail string
	}{
		{[]orders.OrderItemStaffInput{{Slot: 4, StaffID: s.employee}}, "项目没有这个岗位"},
		{[]orders.OrderItemStaffInput{{Slot: 2, StaffID: s.employee}, {Slot: 2, StaffID: s.employee}}, "同一岗位不能重复填员工"},
		{[]orders.OrderItemStaffInput{{Slot: 2, StaffID: inactive}}, "岗位上的员工必须是本店在职员工"},
		{[]orders.OrderItemStaffInput{{Slot: 2, StaffID: id(foreign.Body["id"])}}, "岗位上的员工必须是本店在职员工"},
		{[]orders.OrderItemStaffInput{{Slot: 2, StaffID: uuid.New()}}, "岗位上的员工必须是本店在职员工"},
	} {
		in["staff"] = tc.staff
		s.assertUnchanged("set_order_item_staff", in, 409, tc.wantDetail)
	}
	update["active"] = false
	s.call("update_item", update, 200)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}
	s.assertUnchanged("set_order_item_staff", in, 409, "项目已停用")
	// 不重新取价的路径不检查目录停用状态。
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 3, StaffID: s.employee}}
	s.edit("set_order_item_staff", in, 200)
}

func TestRepriceOrderItem(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line(1)), 200)
	in := editInput(o)
	after := s.edit("reprice_order_item", in, 200)
	line := firstLine(after)
	if line["price"] != "88.00" || line["price_source"] != "manual" || line["level_id"] != nil || line["reprice_reason"] != "现场改价" || after["receivable"] != "88.00" {
		t.Fatal(after)
	}
	s.assertEditAudit("reprice_order_item", o, after)
	for _, reason := range []string{"", " \t\n ", strings.Repeat("字", 201)} {
		in["reason"] = reason
		before := s.snapshot()
		r := s.HTTPWithToken(t, s.Owner, "reprice_order_item", editArgs("reprice_order_item", in))
		if (r.Status != 409 || r.Detail() != "原因须为一到二百字") && (r.Status != 422 || r.Detail() != "validation failed") {
			t.Fatal(r)
		}
		if before != s.snapshot() {
			t.Fatal("invalid reason wrote data")
		}
	}
	in["reason"] = "原因"
	in["price"] = "-1.00"
	s.assertUnchanged("reprice_order_item", in, 422, "validation failed")
	in["price"] = "0.00"
	if o := s.edit("reprice_order_item", in, 200); firstLine(o)["price"] != "0.00" {
		t.Fatal(o)
	}
}

func (s *fixture) openAt(caller ops.Caller, at time.Time, line orders.OrderItemInput) map[string]any {
	s.t.Helper()
	var orderID uuid.UUID
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		o, err := orders.OpenOrderAtForTest(context.Background(), ops.Env{Caller: caller, Q: q}, new(s.opening(line)), at)
		if err == nil {
			orderID = o.ID
		}
		return err
	}); err != nil {
		s.t.Fatal(err)
	}
	return s.call("get_order", orders.GetOrderInput{ID: orderID}, 200)
}

func TestEditWindow(t *testing.T) {
	s := open(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes (tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES ($1,$2,'business_day_end','"02:00"',false,'"00:00"',true,'2000-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	today, err := settings.BusinessDate(context.Background(), dbgen.New(tx), s.Res.StoreID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	counter := 100
	for _, op := range []string{"remove_order_item", "set_order_item_staff"} {
		for _, tc := range []struct {
			name          string
			age           int
			window        *int
			want          int
			afterMidnight bool
		}{
			{"boundary", 3, new(3), 200, false}, {"outside", 3, new(2), 403, false}, {"unlimited", 3, nil, 200, false}, {"yesterday", 1, new(0), 403, false}, {"today", 0, new(0), 200, false}, {"after midnight", 1, new(0), 403, true},
		} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				s := *s
				s.t = t
				day := today.AddDate(0, 0, -tc.age)
				at := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, settings.Location)
				if tc.afterMidnight {
					at = time.Date(today.Year(), today.Month(), today.Day(), 0, 1, 0, 0, settings.Location)
				}
				// 默认营业日结束时间是 02:00，凌晨仍属于昨天。
				o := s.openAt(s.MustCaller(s.Owner), at, s.line())
				if o["business_date"] != day.Format(time.DateOnly) {
					t.Fatal(o, day)
				}
				role := s.call("create_role", map[string]any{"name": fmt.Sprint("窗口", counter), "permissions": []any{map[string]any{"permission": "order.edit", "scope": "store", "window_days": tc.window}}}, 200)
				counter++
				_, token := s.Hire("改单员工", fmt.Sprint(counter), fmt.Sprint("edit", counter), id(role["id"]))
				before := s.snapshot()
				r := s.HTTPWithToken(t, token, op, editArgs(op, editInput(o)))
				if r.Status != tc.want || tc.want == 403 && r.Detail() != "permission denied" {
					t.Fatal(r)
				}
				if tc.want == 403 && before != s.snapshot() {
					t.Fatal("window refusal wrote data")
				}
			})
		}
	}
}

func TestEditScope(t *testing.T) {
	s := open(t)
	role := s.call("create_role", map[string]any{"name": "本人改单", "permissions": []any{map[string]any{"permission": "order.edit", "scope": "self"}, map[string]any{"permission": "order.create"}}}, 200)
	employee, token := s.Hire("本人", "003", "selfedit", id(role["id"]))
	for _, op := range []string{"remove_order_item", "set_order_item_staff"} {
		for _, involvement := range []string{"opener", "slot", "removed slot", "none"} {
			t.Run(op+"/"+involvement, func(t *testing.T) {
				s := *s
				s.t = t
				opener := s.MustCaller(s.Owner)
				if involvement == "opener" {
					opener = s.MustCaller(token)
				}
				line := s.line()
				if involvement == "slot" || involvement == "removed slot" {
					line.Staff = []orders.OrderItemStaffInput{{Slot: 2, StaffID: employee}}
				}
				o := s.openAt(opener, time.Now(), line)
				if involvement == "removed slot" {
					s.edit("remove_order_item", editInput(o), 200)
					o = s.call("add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()}, 200)
				}
				in := editInput(o)
				if involvement == "removed slot" {
					in["order_item_id"] = lines(o)[1].(map[string]any)["id"]
				}
				before := s.snapshot()
				r := s.HTTPWithToken(t, token, op, editArgs(op, in))
				want := 200
				if involvement == "none" {
					want = 403
				}
				if r.Status != want || want == 403 && r.Detail() != "permission denied" {
					t.Fatal(r)
				}
				if want == 403 && before != s.snapshot() {
					t.Fatal("scope refusal wrote data")
				}
			})
		}
	}
	// 改价只有自己的权限，不受 order.edit 的范围和窗口限制。
	o := s.openAt(s.MustCaller(s.Owner), time.Now().AddDate(0, 0, -10), s.line())
	caller := s.MustCaller(token)
	caller.Permissions[perm.OrderEdit] = ops.Grant{Scope: ops.ScopeSelf, WindowDays: new(0)}
	caller.Permissions[perm.OrderReprice] = ops.Grant{}
	if r := s.HTTP(t, &caller, "reprice_order_item", editArgs("reprice_order_item", editInput(o))); r.Status != 200 {
		t.Fatal(r)
	}
}

func TestEditDatabaseColumns(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line()), 200)
	for table, cols := range map[string][]string{
		"order_items":          {"tenant_id", "store_id", "id", "item_id", "item_name", "added_by", "added_at"},
		"orders":               {"tenant_id", "store_id", "id", "gender", "designated", "settings_snapshot_id", "business_date", "opened_by", "opened_at"},
		"order_item_positions": {"tenant_id", "store_id", "order_item_id", "slot", "position_id", "required"},
	} {
		for _, col := range cols {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), "UPDATE "+table+" SET "+col+"="+col)
			var pgErr interface{ SQLState() string }
			if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
				t.Fatal(table, col, err)
			}
			if err := tx.Rollback(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE orders SET customer_id=customer_id,status=status,merged_into=merged_into")
	dbtest.Exec(t, tx, "UPDATE order_items SET order_id=order_id,price=price,price_source=price_source,level_id=level_id,reprice_reason=reprice_reason,removed_at=removed_at,removed_by=removed_by")
	dbtest.Exec(t, tx, "UPDATE order_item_positions SET staff_id=staff_id")
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{"price_source='manual'", "reprice_reason='原因'", "removed_at=clock_timestamp()", "removed_by='" + s.Res.OwnerID.String() + "'", "price_source='manual',reprice_reason=''", "price_source='manual',reprice_reason='   '"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "UPDATE order_items SET "+set+" WHERE id=$1", firstLine(o)["id"])
		var pgErr interface{ SQLState() string }
		if !errors.As(err, &pgErr) || pgErr.SQLState() != "23514" {
			t.Fatal(set, err)
		}
		if err := tx.Rollback(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEditItemLock(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line()), 200)
	in := editInput(o)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", s.item)
	dbtest.Exec(t, tx, "UPDATE item_level_prices SET price=222.22 WHERE item_id=$1 AND level_id=$2", s.item, s.level)
	done := make(chan optest.Result, 1)
	caller := s.MustCaller(s.Owner)
	go func() { done <- s.HTTP(t, &caller, "set_order_item_staff", editArgs("set_order_item_staff", in)) }()
	dbtest.WaitForLockWait(t, s.Pool)
	select {
	case r := <-done:
		t.Fatal("edit did not wait for item", r)
	default:
	}
	dbtest.Commit(t, tx)
	r := <-done
	if r.Status != 200 || firstLine(r.Body)["price"] != "222.22" {
		t.Fatal(r)
	}
	entries := s.call("list_audit_log", map[string]any{"target_id": o["id"]}, 200)["entries"].([]any)
	if len(entries) != 2 || firstLine(entries[0].(map[string]any)["after"].(map[string]any))["price"] != "222.22" {
		t.Fatal(entries)
	}
}

func TestEditOrderLock(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line()), 200)
	in := editInput(o)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", s.item)
	done := make(chan optest.Result, 2)
	caller := s.MustCaller(s.Owner)
	go func() { done <- s.HTTP(t, &caller, "set_order_item_staff", editArgs("set_order_item_staff", in)) }()
	dbtest.WaitForLockWait(t, s.Pool)
	go func() { done <- s.HTTP(t, &caller, "reprice_order_item", editArgs("reprice_order_item", editInput(o))) }()
	// 第二个请求必须等在单锁；无单锁时会提前改价并返回。
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
		case r := <-done:
			t.Fatal("second edit bypassed order lock", r)
		case <-deadline:
			t.Fatal("second edit did not wait")
		case <-ticker.C:
		}
	}
	dbtest.Commit(t, tx)
	for range 2 {
		if r := <-done; r.Status != 200 {
			t.Fatal(r)
		}
	}
	entries := s.call("list_audit_log", map[string]any{"target_id": o["id"]}, 200)["entries"].([]any)
	if len(entries) != 3 {
		t.Fatal(entries)
	}
	last, first := entries[0].(map[string]any), entries[1].(map[string]any)
	if last["action"] != "reprice_order_item" || first["action"] != "set_order_item_staff" || !reflect.DeepEqual(last["before"], first["after"]) || firstLine(last["before"].(map[string]any))["price"] != "150.00" {
		t.Fatal(entries)
	}
}

func TestEditAvoidsUnchangedLocks(t *testing.T) {
	s := open(t)
	for _, manual := range []bool{false, true} {
		o := s.call("open_order", s.opening(s.line(1)), 200)
		if manual {
			o = s.edit("reprice_order_item", editInput(o), 200)
		}
		in := editInput(o)
		in["staff"] = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}, {Slot: 2, StaffID: s.employee}}
		if manual {
			in["staff"] = []orders.OrderItemStaffInput{{Slot: 2, StaffID: s.employee}}
		}
		caller := s.MustCaller(s.Owner)
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", s.item)
		if !manual {
			dbtest.Exec(t, tx, "SELECT slot FROM order_item_positions WHERE order_item_id=$1 AND slot=1 FOR NO KEY UPDATE", firstLine(o)["id"])
		}
		done := make(chan optest.Result, 1)
		go func() { done <- s.HTTP(t, &caller, "set_order_item_staff", editArgs("set_order_item_staff", in)) }()
		select {
		case r := <-done:
			if r.Status != 200 || firstLine(r.Body)["price"] != firstLine(o)["price"] {
				t.Fatal(r)
			}
		case <-time.After(5 * time.Second):
			if err := tx.Rollback(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Fatal("unchanged price or slot waited for an unnecessary lock", <-done)
		}
		if err := tx.Rollback(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEditStaffLock(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line()), 200)
	in := editInput(o)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 2, StaffID: s.employee}}
	caller := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM staff WHERE id=$1 FOR NO KEY UPDATE", s.employee)
	dbtest.Exec(t, tx, "UPDATE staff SET active=false,deactivated_at=clock_timestamp() WHERE id=$1", s.employee)
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTP(t, &caller, "set_order_item_staff", editArgs("set_order_item_staff", in)) }()
	dbtest.WaitForLockWait(t, s.Pool)
	dbtest.Commit(t, tx)
	if r := <-done; r.Status != 409 || r.Detail() != "岗位上的员工必须是本店在职员工" {
		t.Fatal(r)
	}
	got := s.call("get_order", orders.GetOrderInput{ID: id(o["id"])}, 200)
	if !reflect.DeepEqual(got, o) {
		t.Fatal(got)
	}
}

func TestOrderEditsMigrationDown(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line(1)), 200)
	s.edit("reprice_order_item", editInput(o), 200)
	s.edit("remove_order_item", editInput(o), 200)
	dbtest.MigrateDownTo(t, s.Pool, 15)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var source string
	var level *uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT price_source,level_id FROM order_items WHERE id=$1", firstLine(o)["id"]).Scan(&source, &level); err != nil {
		t.Fatal(err)
	}
	if source != "standard" || level != nil {
		t.Fatal(source, level)
	}
	var columns int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name='order_items' AND column_name IN ('removed_at','removed_by','reprice_reason')").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatal(columns)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"UPDATE order_items SET price=price", "UPDATE order_item_positions SET staff_id=staff_id"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql)
		var pgErr interface{ SQLState() string }
		if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
			t.Fatal(sql, err)
		}
		if err := tx.Rollback(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
}

func TestEditWindowBusinessDate(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes (tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES ($1,$2,'business_day_end','"02:00"',false,'"00:00"',true,'2000-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	yesterday := time.Date(2026, time.May, 9, 12, 0, 0, 0, settings.Location)
	caller := s.MustCaller(s.Owner)
	o := s.openAt(caller, yesterday, s.line())
	caller.Permissions[perm.OrderEdit] = ops.Grant{Scope: ops.ScopeStore, WindowDays: new(0)}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	order, err := q.LockOrder(ctx, dbgen.LockOrderParams{StoreID: s.Res.StoreID, ID: id(o["id"])})
	if err != nil {
		t.Fatal(err)
	}
	if order.BusinessDate.Time.Format(time.DateOnly) != "2026-05-09" {
		t.Fatal(order.BusinessDate)
	}
	for _, tc := range []struct {
		name string
		hour int
		want error
	}{
		{"before end, UTC and local dates differ", 1, nil},
		{"at end", 2, ops.ErrForbidden},
		{"after end", 12, ops.ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, time.May, 10, tc.hour, 0, 0, 0, settings.Location)
			if tc.hour == 1 && now.UTC().Format(time.DateOnly) == now.In(settings.Location).Format(time.DateOnly) {
				t.Fatal("fixture must cross UTC/local dates", now)
			}
			if err := orders.CheckEditForTest(ctx, ops.Env{Caller: caller, Grant: caller.Grant(perm.OrderEdit), Q: q}, order, now); !errors.Is(err, tc.want) || tc.want != nil && (err == nil || err.Error() != "permission denied") {
				t.Fatalf("checkEdit at %s: got %v, want %v", now, err, tc.want)
			}
		})
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRequoteLockOrder(t *testing.T) {
	s := open(t)
	o := s.call("open_order", s.opening(s.line()), 200)
	in := editInput(o)
	in["staff"] = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}
	caller := s.MustCaller(s.Owner)
	ctx := context.Background()
	itemTx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, itemTx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", s.item)
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTP(t, &caller, "set_order_item_staff", editArgs("set_order_item_staff", in)) }()
	dbtest.WaitForLockWait(t, s.Pool)
	staffTx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, staffLockErr := staffTx.Exec(ctx, "SELECT id FROM staff WHERE id=$1 FOR NO KEY UPDATE NOWAIT", s.employee)
	if err := staffTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, itemTx)
	r := <-done
	if staffLockErr != nil {
		t.Fatalf("staff locked before the item lock was acquired: %v", staffLockErr)
	}
	if r.Status != 200 || firstLine(r.Body)["price"] != "150.00" || firstLine(r.Body)["positions"].([]any)[0].(map[string]any)["staff_id"] != s.employee.String() {
		t.Fatal(r)
	}
}

func TestAddUsesCreateGrant(t *testing.T) {
	s := open(t)
	o := s.openAt(s.MustCaller(s.Owner), time.Now().AddDate(0, 0, -10), s.line())
	caller := s.MustCaller(s.Owner)
	caller.StaffID = s.employee
	caller.Permissions = map[ops.Permission]ops.Grant{
		perm.OrderCreate: {},
		perm.OrderEdit:   {Scope: ops.ScopeSelf, WindowDays: new(0)},
	}
	r := s.HTTP(t, &caller, "add_order_item", orders.AddOrderItemInput{OrderID: id(o["id"]), Item: s.line()})
	if r.Status != 200 || len(lines(r.Body)) != 2 {
		t.Fatal(r)
	}
	s.assertEditAudit("add_order_item", o, r.Body)
}
