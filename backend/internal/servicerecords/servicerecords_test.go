package servicerecords_test

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type shop struct {
	*optest.Shop
	t        *testing.T
	customer uuid.UUID
	position uuid.UUID
	employee uuid.UUID
	service  uuid.UUID
	goods    uuid.UUID
	category uuid.UUID
}

func open(t *testing.T) *shop {
	t.Helper()
	s := &shop{Shop: optest.OpenShop(t), t: t}
	s.position = id(s.call("create_position", map[string]any{"name": "大工"}, 200)["id"])
	s.employee, _ = s.Hire("发型师", "002", "stylist")
	s.category = id(s.call("create_item_category", map[string]any{"name": "剪发类"}, 200)["id"])
	s.service = id(s.call("create_item", map[string]any{
		"name": "剪发", "price": "100.00", "discountable": true, "category_id": s.category,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "level_prices": []any{},
		"positions": []any{map[string]any{"position_id": s.position, "required": true}},
	}, 200)["id"])
	s.goods = id(s.call("create_item", map[string]any{
		"name": "洗发水", "kind": "goods", "price": "50.00", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "level_prices": []any{}, "positions": []any{},
	}, 200)["id"])
	s.customer = id(s.call("create_customer", map[string]any{"name": "服务记录顾客", "phone": "13800000001"}, 200)["customer"].(map[string]any)["id"])
	return s
}

func id(v any) uuid.UUID { return uuid.MustParse(v.(string)) }

func (s *shop) call(op string, in any, want int) map[string]any {
	s.t.Helper()
	return s.Call(s.t, s.Owner, op, in, want).Body
}

func (s *shop) order(item uuid.UUID, customer uuid.UUID) map[string]any {
	s.t.Helper()
	staff := []any{}
	if item == s.service {
		staff = append(staff, map[string]any{"slot": 1, "staff_id": s.employee})
	}
	return s.call("open_order", map[string]any{
		"customer_id": customer, "gender": "female", "designated": true,
		"items": []any{map[string]any{"item_id": item, "staff": staff}},
	}, 200)
}

func (s *shop) addService(o map[string]any) map[string]any {
	s.t.Helper()
	return s.call("add_order_item", map[string]any{
		"order_id": o["id"],
		"item":     map[string]any{"item_id": s.service, "staff": []any{map[string]any{"slot": 1, "staff_id": s.employee}}},
	}, 200)
}

func (s *shop) paid(o map[string]any) map[string]any {
	s.t.Helper()
	result := s.call("checkout_orders", map[string]any{
		"orders": []any{map[string]any{
			"order_id": o["id"],
			"payments": []any{map[string]any{"method": "cash", "amount": o["receivable"]}},
		}},
	}, 200)
	return result["items"].([]any)[0].(map[string]any)
}

func line(o map[string]any) map[string]any { return o["items"].([]any)[0].(map[string]any) }

func (s *shop) record(orderID, itemID uuid.UUID, result, formula, feedback *string) optest.Result {
	s.t.Helper()
	return s.HTTPWithToken(s.t, s.Owner, "record_service", map[string]any{
		"order_id": orderID, "order_item_id": itemID, "result": result, "formula": formula, "feedback": feedback,
	})
}

func str(v string) *string { return &v }

func TestRecordService(t *testing.T) {
	s := open(t)
	o := s.paid(s.order(s.service, s.customer))
	item := line(o)
	r := s.record(id(o["id"]), id(item["id"]), str("剪短打薄"), str("6/45 + 9%"), str("satisfied"))
	if r.Status != 200 || r.Body["order_id"] != o["id"] || r.Body["order_item_id"] != item["id"] || r.Body["result"] != "剪短打薄" || r.Body["formula"] != "6/45 + 9%" || r.Body["feedback"] != "satisfied" || r.Body["recorded_by"] != s.Res.OwnerID.String() || r.Body["updated_at"] == nil {
		t.Fatal(r)
	}
	updated := s.record(id(o["id"]), id(item["id"]), str("只修发尾"), nil, nil)
	if updated.Status != 200 || updated.Body["result"] != "只修发尾" || updated.Body["formula"] != nil || updated.Body["feedback"] != nil {
		t.Fatal(updated)
	}

	entries := s.call("list_audit_log", map[string]any{"target_id": o["id"]}, 200)["entries"].([]any)
	latest := entries[0].(map[string]any)
	if latest["action"] != "record_service" {
		t.Fatal(latest)
	}
	before := latest["before"].(map[string]any)
	after := latest["after"].(map[string]any)
	if before["result"] != "剪短打薄" || before["formula"] != "6/45 + 9%" || after["result"] != "只修发尾" || after["formula"] != nil {
		t.Fatal(latest)
	}
	first := entries[1].(map[string]any)
	if first["action"] != "record_service" || first["before"] != nil {
		t.Fatal(first)
	}
}

func TestRecordServiceRules(t *testing.T) {
	s := open(t)
	paid := s.paid(s.order(s.service, s.customer))
	openOrder := s.order(s.service, s.customer)
	goodsOrder := s.paid(s.order(s.goods, s.customer))
	other := s.order(s.service, s.customer)
	otherLine := line(other)
	two := s.order(s.service, s.customer)
	second := s.addService(two)
	removed := second["items"].([]any)[1].(map[string]any)
	s.call("remove_order_item", map[string]any{"order_id": two["id"], "order_item_id": removed["id"]}, 200)
	voided := s.paid(s.order(s.service, s.customer))
	s.call("void_order", map[string]any{"order_id": voided["id"], "reason": "客人取消"}, 200)
	mergeA := s.order(s.service, s.customer)
	mergeB := s.order(s.service, s.customer)
	mergedLine := line(mergeB)
	s.call("merge_orders", map[string]any{"order_id": mergeA["id"], "merged_order_ids": []any{mergeB["id"]}}, 200)
	otherOrder, otherItem := s.otherStoreOrder(paid)

	for _, tc := range []struct {
		name   string
		order  uuid.UUID
		item   uuid.UUID
		status int
		detail string
	}{
		{"order missing", uuid.New(), id(otherLine["id"]), 404, "not found"},
		{"order other store", otherOrder, otherItem, 404, "not found"},
		{"item other order", id(paid["id"]), id(otherLine["id"]), 404, "not found"},
		{"removed item", id(two["id"]), id(removed["id"]), 409, "项目已去掉"},
		{"goods", id(goodsOrder["id"]), id(line(goodsOrder)["id"]), 409, "卖品不记服务"},
		{"voided", id(voided["id"]), id(line(voided)["id"]), 409, "这张单已撤单或已合并，不能记服务"},
		{"merged", id(mergeB["id"]), id(mergedLine["id"]), 409, "这张单已撤单或已合并，不能记服务"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.record(tc.order, tc.item, str("结果"), nil, nil)
			if r.Status != tc.status || r.Detail() != tc.detail {
				t.Fatal(r)
			}
		})
	}

	if r := s.record(id(openOrder["id"]), id(line(openOrder)["id"]), str("还没结账也能记"), nil, nil); r.Status != 200 {
		t.Fatal(r)
	}
	if r := s.HTTPWithToken(t, s.Owner, "record_service", map[string]any{"order_id": paid["id"], "order_item_id": line(paid)["id"]}); r.Status != 409 || r.Detail() != "至少填写做成什么样、配方或顾客反馈中的一项" {
		t.Fatal(r)
	}
	if r := s.HTTPWithToken(t, s.Owner, "record_service", map[string]any{"order_id": paid["id"], "order_item_id": line(paid)["id"], "result": "  ", "formula": "\t"}); r.Status != 409 || r.Detail() != "至少填写做成什么样、配方或顾客反馈中的一项" {
		t.Fatal(r)
	}
	if r := s.record(id(paid["id"]), id(line(paid)["id"]), str(" 剪短 "), nil, nil); r.Status != 200 || r.Body["result"] != " 剪短 " {
		t.Fatal(r)
	}
	if r := s.HTTPWithToken(t, s.Owner, "record_service", map[string]any{"order_id": paid["id"], "order_item_id": line(paid)["id"], "feedback": "bad"}); r.Status != 422 || r.Detail() == "" {
		t.Fatal(r)
	}
	for field, value := range map[string]string{
		"result":  strings.Repeat("剪", 501),
		"formula": strings.Repeat("染", 501),
	} {
		t.Run(field+" too long", func(t *testing.T) {
			r := s.HTTPWithToken(t, s.Owner, "record_service", map[string]any{"order_id": paid["id"], "order_item_id": line(paid)["id"], field: value})
			if r.Status != 422 || r.Detail() == "" {
				t.Fatal(r)
			}
		})
	}
	for field, value := range map[string]string{
		"result":  strings.Repeat("剪", 500),
		"formula": strings.Repeat("染", 500),
	} {
		t.Run(field+" at limit", func(t *testing.T) {
			r := s.HTTPWithToken(t, s.Owner, "record_service", map[string]any{"order_id": paid["id"], "order_item_id": line(paid)["id"], field: value})
			if r.Status != 200 || r.Body[field] != value {
				t.Fatal(r)
			}
		})
	}
}

func TestRecordServiceScope(t *testing.T) {
	s := open(t)
	o := s.paid(s.order(s.service, s.customer))
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{s.customer}, "staff_id": s.employee}, 200)
	caller := s.MustCaller(s.Owner)
	caller.Permissions = map[ops.Permission]ops.Grant{perm.CustomerView: {Scope: ops.ScopeSelf}}
	r := s.HTTP(t, &caller, "record_service", map[string]any{"order_id": o["id"], "order_item_id": line(o)["id"], "result": "看不见"})
	if r.Status != 404 || r.Detail() != "not found" {
		t.Fatal(r)
	}
	for _, ch := range s.Channels() {
		got := ch.Call(t, caller, "list_service_history", map[string]any{"customer_id": s.customer})
		if got.Status != 404 || got.Detail() != "not found" {
			t.Fatal(ch.Name, got)
		}
	}
}

func TestRecordServiceAfterSealedDay(t *testing.T) {
	s := open(t)
	o := s.paid(s.order(s.service, s.customer))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO store_sealings (tenant_id, store_id, sealed_through) VALUES ($1, $2, $3)", s.Res.TenantID, s.Res.StoreID, mustDate(t, o["business_date"].(string)))
	dbtest.Commit(t, tx)
	if r := s.record(id(o["id"]), id(line(o)["id"]), str("封账后补记"), nil, nil); r.Status != 200 {
		t.Fatal(r)
	}
}

func TestRecordServiceWaitsForOrderLock(t *testing.T) {
	s := open(t)
	o := s.paid(s.order(s.service, s.customer))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", o["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "record_service", map[string]any{"order_id": o["id"], "order_item_id": line(o)["id"], "result": "等锁"})
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Rollback(t, tx)
	optest.WaitResult(t, done, 200)
}

func TestRecordServiceRejectsChangedCustomer(t *testing.T) {
	s := open(t)
	o := s.paid(s.order(s.service, s.customer))
	changed := id(s.call("create_customer", map[string]any{"name": "换到的顾客", "phone": "13800000089"}, 200)["customer"].(map[string]any)["id"])
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE orders SET customer_id=$2 WHERE id=$1", o["id"], changed)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "record_service", map[string]any{"order_id": o["id"], "order_item_id": line(o)["id"], "result": "换顾客后记"})
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Commit(t, tx)
	r := optest.WaitResult(t, done, 404)
	if r.Detail() != "not found" {
		t.Fatal(r)
	}
}

func TestRecordServiceAIDraft(t *testing.T) {
	s := open(t)
	o := s.paid(s.order(s.service, s.customer))
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "service_records", "audit_log")
	r := s.AI(t, s.MustCaller(s.Owner), "record_service", map[string]any{"order_id": o["id"], "order_item_id": line(o)["id"], "result": "AI 草稿"})
	optest.RequireDraftCreated(t, r, dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "service_records", "audit_log") == before)
}

func TestListServiceHistory(t *testing.T) {
	s := open(t)
	o := s.paid(s.order(s.service, s.customer))
	s.record(id(o["id"]), id(line(o)["id"]), str("层次剪"), nil, str("neutral"))
	s.record(id(o["id"]), id(line(o)["id"]), str("层次剪"), str("6/45"), str("satisfied"))
	s.order(s.service, s.customer)
	voided := s.paid(s.order(s.service, s.customer))
	s.call("void_order", map[string]any{"order_id": voided["id"], "reason": "测试"}, 200)
	s.paid(s.order(s.goods, s.customer))
	removed := s.order(s.service, s.customer)
	second := s.addService(removed)
	removedLine := second["items"].([]any)[1].(map[string]any)
	s.call("remove_order_item", map[string]any{"order_id": removed["id"], "order_item_id": removedLine["id"]}, 200)
	s.paid(removed)

	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_service_history", map[string]any{"customer_id": s.customer})
		if r.Status != 200 || r.Body["total"].(float64) != 2 {
			t.Fatal(ch.Name, r)
		}
		items := r.Body["items"].([]any)
		if len(items) != 2 {
			t.Fatal(ch.Name, r)
		}
		var item map[string]any
		for _, entry := range items {
			if entry.(map[string]any)["order_id"] == o["id"] {
				item = entry.(map[string]any)
			}
			if entry.(map[string]any)["order_id"] == voided["id"] {
				t.Fatal(ch.Name, "voided order listed", entry)
			}
		}
		if item == nil || item["order_item_id"] != line(o)["id"] || item["business_date"] != o["business_date"] || item["category"] == nil {
			t.Fatal(ch.Name, item)
		}
		ref := item["item"].(map[string]any)
		if ref["id"] != s.service.String() || ref["name"] != "剪发" {
			t.Fatal(ch.Name, ref)
		}
		staff := item["staff"].([]any)
		if len(staff) != 1 || staff[0].(map[string]any)["position"] != "大工" || staff[0].(map[string]any)["id"] != s.employee.String() || staff[0].(map[string]any)["name"] != "发型师" {
			t.Fatal(ch.Name, staff)
		}
		record := item["record"].(map[string]any)
		if record["result"] != "层次剪" || record["formula"] != "6/45" || record["feedback"] != "satisfied" {
			t.Fatal(ch.Name, record)
		}
	}

	t.Run("排序", func(t *testing.T) {
		s := open(t)
		s.SetNow(time.Date(2026, 11, 21, 12, 0, 0, 0, settings.Location))
		early := s.paid(s.order(s.service, s.customer))
		late := s.paid(s.order(s.service, s.customer))
		openBackfill := func(day string) map[string]any {
			return s.call("open_order", map[string]any{
				"customer_id": s.customer, "gender": "female", "designated": true, "paper_date": day,
				"items": []any{map[string]any{"item_id": s.service, "staff": []any{map[string]any{"slot": 1, "staff_id": s.employee}}}},
			}, 200)
		}
		two := openBackfill("2026-11-20")
		two = s.addService(two)
		first := two["items"].([]any)[0].(map[string]any)
		second := two["items"].([]any)[1].(map[string]any)
		older := s.paid(openBackfill("2026-11-19"))
		twoPaid := s.paid(two)
		for _, tc := range []struct {
			id  any
			at  time.Time
			sql string
		}{
			{late["checkout_id"], time.Date(2026, 11, 21, 11, 0, 0, 0, settings.Location), "UPDATE checkouts SET at=$2 WHERE id=$1"},
			{early["checkout_id"], time.Date(2026, 11, 21, 10, 0, 0, 0, settings.Location), "UPDATE checkouts SET at=$2 WHERE id=$1"},
			{twoPaid["checkout_id"], time.Date(2026, 11, 20, 9, 0, 0, 0, settings.Location), "UPDATE checkouts SET at=$2 WHERE id=$1"},
			{older["checkout_id"], time.Date(2026, 11, 19, 12, 0, 0, 0, settings.Location), "UPDATE checkouts SET at=$2 WHERE id=$1"},
			{first["id"], time.Date(2026, 11, 20, 10, 0, 0, 0, settings.Location), "UPDATE order_items SET added_at=$2 WHERE id=$1"},
			{second["id"], time.Date(2026, 11, 20, 10, 5, 0, 0, settings.Location), "UPDATE order_items SET added_at=$2 WHERE id=$1"},
		} {
			if _, err := s.Pool.Exec(context.Background(), tc.sql, tc.id, tc.at); err != nil {
				t.Fatal(err)
			}
		}
		want := []any{line(late)["id"], line(early)["id"], first["id"], second["id"], line(older)["id"]}
		for _, ch := range s.Channels() {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_service_history", map[string]any{"customer_id": s.customer, "limit": 100})
			if r.Status != 200 || r.Body["total"].(float64) != float64(len(want)) {
				t.Fatal(ch.Name, r)
			}
			got := []any{}
			for _, raw := range r.Body["items"].([]any) {
				got = append(got, raw.(map[string]any)["order_item_id"])
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s history order = %v, want %v", ch.Name, got, want)
			}
		}
	})
}

func TestListServiceHistoryMergeAndNotFound(t *testing.T) {
	s := open(t)
	keep := s.customer
	merged := id(s.call("create_customer", map[string]any{"name": "被合并顾客", "phone": "13900000002"}, 200)["customer"].(map[string]any)["id"])
	o := s.paid(s.order(s.service, merged))
	s.record(id(o["id"]), id(line(o)["id"]), str("合并前的服务"), nil, nil)
	s.call("merge_customers", map[string]any{"keep_id": keep, "merged_id": merged}, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_service_history", map[string]any{"customer_id": keep})
		if r.Status != 200 || r.Body["total"].(float64) != 1 || len(r.Body["items"].([]any)) != 1 {
			t.Fatal(ch.Name, r)
		}
		item := r.Body["items"].([]any)[0].(map[string]any)
		if item["order_id"] != o["id"] || item["record"].(map[string]any)["result"] != "合并前的服务" {
			t.Fatal(ch.Name, item)
		}
	}
	for _, customer := range []uuid.UUID{uuid.New(), s.otherStoreCustomer()} {
		for _, ch := range s.Channels() {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_service_history", map[string]any{"customer_id": customer})
			if r.Status != 404 || r.Detail() != "not found" {
				t.Fatal(ch.Name, r)
			}
		}
	}
}

func TestListServiceHistoryStoreFilter(t *testing.T) {
	s := open(t)
	o := s.paid(s.order(s.service, s.customer))
	s.otherStorePaidOrder(o)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_service_history", map[string]any{"customer_id": s.customer})
		if r.Status != 200 || r.Body["total"].(float64) != 1 || len(r.Body["items"].([]any)) != 1 || r.Body["items"].([]any)[0].(map[string]any)["order_id"] != o["id"] {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestListServiceHistoryPagination(t *testing.T) {
	s := open(t)
	for i := 0; i < 3; i++ {
		o := s.paid(s.order(s.service, s.customer))
		s.record(id(o["id"]), id(line(o)["id"]), str("第"+string(rune('一'+i))+"次"), nil, nil)
	}
	for _, ch := range s.Channels() {
		first := ch.Call(t, s.MustCaller(s.Owner), "list_service_history", map[string]any{"customer_id": s.customer, "limit": 1})
		if first.Status != 200 || first.Body["total"].(float64) != 3 || len(first.Body["items"].([]any)) != 1 {
			t.Fatal(ch.Name, first)
		}
		next := ch.Call(t, s.MustCaller(s.Owner), "list_service_history", map[string]any{"customer_id": s.customer, "limit": 1, "offset": 1})
		if next.Status != 200 || len(next.Body["items"].([]any)) != 1 || next.Body["items"].([]any)[0].(map[string]any)["order_item_id"] == first.Body["items"].([]any)[0].(map[string]any)["order_item_id"] {
			t.Fatal(ch.Name, next)
		}
	}
}

type historyQueryCounter struct{ all atomic.Int64 }

func (c *historyQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}
func (*historyQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestListServiceHistoryQueryCount(t *testing.T) {
	s := open(t)
	one := s.paid(s.order(s.service, s.customer))
	s.record(id(one["id"]), id(line(one)["id"]), str("一行"), nil, nil)
	manyCustomer := id(s.call("create_customer", map[string]any{"name": "多行顾客", "phone": "13700000003"}, 200)["customer"].(map[string]any)["id"])
	for i := 0; i < 3; i++ {
		o := s.order(s.service, manyCustomer)
		o = s.addService(o)
		o = s.paid(o)
		s.record(id(o["id"]), id(line(o)["id"]), str("多行"), nil, nil)
	}
	counter := &historyQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
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
		for _, customer := range []uuid.UUID{s.customer, manyCustomer} {
			counter.all.Store(0)
			r := ch.Call(t, caller, "list_service_history", map[string]any{"customer_id": customer})
			if r.Status != 200 || len(r.Body["items"].([]any)) == 0 {
				t.Fatal(ch.Name, r)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, "query count grew with service history rows", counts)
		}
	}
}

func (s *shop) otherStoreCustomer() uuid.UUID {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	store, err := q.CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "别店"})
	if err != nil {
		s.t.Fatal(err)
	}
	row, err := q.CreateCustomer(context.Background(), dbgen.CreateCustomerParams{TenantID: s.Res.TenantID, StoreID: store.ID, Name: str("别店顾客"), CreatedBy: &s.Res.OwnerID})
	if err != nil {
		s.t.Fatal(err)
	}
	dbtest.Commit(s.t, tx)
	return row.ID
}

func (s *shop) otherStoreOrder(template map[string]any) (uuid.UUID, uuid.UUID) {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	store, err := q.CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "别店订单"})
	if err != nil {
		s.t.Fatal(err)
	}
	var snapshot uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT settings_snapshot_id FROM orders WHERE id=$1", template["id"]).Scan(&snapshot); err != nil {
		s.t.Fatal(err)
	}
	row, err := q.CreateOrder(context.Background(), dbgen.CreateOrderParams{
		TenantID: s.Res.TenantID, StoreID: store.ID, CustomerID: s.customer, Gender: "female", Designated: true,
		SettingsSnapshotID: snapshot, BusinessDate: mustDate(s.t, template["business_date"].(string)), OpenedBy: s.Res.OwnerID,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	item, err := q.CreateOrderItem(context.Background(), dbgen.CreateOrderItemParams{
		TenantID: s.Res.TenantID, StoreID: store.ID, OrderID: row.ID, ItemID: s.service, ItemName: "剪发",
		Price: mustAmount(s.t, "100.00"), PriceSource: "standard", AddedBy: s.Res.OwnerID, ItemKind: "service",
	})
	if err != nil {
		s.t.Fatal(err)
	}
	dbtest.Commit(s.t, tx)
	return row.ID, item.ID
}

func (s *shop) otherStorePaidOrder(template map[string]any) {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	store, err := q.CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "别店已结"})
	if err != nil {
		s.t.Fatal(err)
	}
	var snapshot uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT settings_snapshot_id FROM orders WHERE id=$1", template["id"]).Scan(&snapshot); err != nil {
		s.t.Fatal(err)
	}
	row, err := q.CreateOrder(context.Background(), dbgen.CreateOrderParams{
		TenantID: s.Res.TenantID, StoreID: store.ID, CustomerID: s.customer, Gender: "female", Designated: true,
		SettingsSnapshotID: snapshot, BusinessDate: mustDate(s.t, template["business_date"].(string)), OpenedBy: s.Res.OwnerID,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := q.CreateOrderItem(context.Background(), dbgen.CreateOrderItemParams{
		TenantID: s.Res.TenantID, StoreID: store.ID, OrderID: row.ID, ItemID: s.service, ItemName: "剪发",
		Price: mustAmount(s.t, "100.00"), PriceSource: "standard", AddedBy: s.Res.OwnerID, ItemKind: "service",
	}); err != nil {
		s.t.Fatal(err)
	}
	checkout, err := q.CreateCheckout(context.Background(), dbgen.CreateCheckoutParams{
		TenantID: s.Res.TenantID, StoreID: store.ID, BusinessDate: mustDate(s.t, template["business_date"].(string)), CheckedOutBy: s.Res.OwnerID,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	dbtest.Exec(s.t, tx, "UPDATE orders SET status='paid', checkout_id=$2 WHERE id=$1", row.ID, checkout.ID)
	dbtest.Commit(s.t, tx)
}

func mustDate(t *testing.T, value string) pgtype.Date {
	t.Helper()
	day, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatal(err)
	}
	return pgtype.Date{Time: day, Valid: true}
}

func mustAmount(t *testing.T, value string) money.Amount {
	t.Helper()
	var out money.Amount
	if err := out.Scan(value); err != nil {
		t.Fatal(err)
	}
	return out
}
