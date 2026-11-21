package orders_test

import (
	"context"
	"reflect"
	"sort"
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
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func orderedBodies(t *testing.T, ch optest.Channel, caller ops.Caller, ids ...uuid.UUID) []any {
	t.Helper()
	out := make([]any, len(ids))
	for i, orderID := range ids {
		r := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: orderID})
		if r.Status != 200 {
			t.Fatal(r)
		}
		out[i] = r.Body
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].(map[string]any), out[j].(map[string]any)
		lt, _ := time.Parse(time.RFC3339Nano, left["opened_at"].(string))
		rt, _ := time.Parse(time.RFC3339Nano, right["opened_at"].(string))
		if lt.Equal(rt) {
			return left["id"].(string) > right["id"].(string)
		}
		return lt.After(rt)
	})
	return out
}

func assertPage(t *testing.T, ch optest.Channel, r optest.Result, total int, want []any) {
	t.Helper()
	if r.Status != 200 {
		t.Fatal(ch.Name, r)
	}
	if r.Body["total"] != float64(total) {
		t.Fatal(ch.Name, "total", r.Body["total"], "want", total)
	}
	items, ok := r.Body["items"].([]any)
	if !ok || len(items) != len(want) {
		t.Fatal(ch.Name, r.Body["items"], want)
	}
	for i := range want {
		if !reflect.DeepEqual(items[i], want[i]) {
			t.Fatal(ch.Name, "item", i, items[i], "want", want[i])
		}
	}
}

func TestListOrdersFiltersAndViews(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, settings.Location)
	s.SetNow(now)
	day := settings.Date(now)
	yesterday := day.AddDate(0, 0, -1)
	line := s.checkoutLine()
	paid := s.paid(s.customerOrder(line), payment("cash", "100"))
	voided := s.void(s.paid(s.customerOrder(line), payment("cash", "100")))
	s.customerOrder(line)
	keep, merged := s.customerOrder(line), s.customerOrder(line)
	s.call("merge_orders", mergeInput(keep, merged), 200)
	s.SetNow(yesterday.Add(12 * time.Hour))
	past := s.paid(s.customerOrder(line), payment("cash", "100"))
	s.SetNow(now)
	otherCustomer := id(s.call("create_customer", map[string]any{"name": "另一位顾客", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"])
	otherInput := s.opening(line)
	otherInput.CustomerID = &otherCustomer
	otherPaid := s.paid(s.call("open_order", otherInput, 200), payment("cash", "100"))
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			allWant := orderedBodies(t, ch, owner, id(paid["id"]), id(voided["id"]), id(otherPaid["id"]))
			omitted := ch.Call(t, owner, "list_orders", orders.ListOrdersInput{})
			assertPage(t, ch, omitted, 3, allWant)
			explicitAll := ch.Call(t, owner, "list_orders", orders.ListOrdersInput{Status: "all"})
			assertPage(t, ch, explicitAll, 3, allWant)
			if !reflect.DeepEqual(omitted.Body, explicitAll.Body) {
				t.Fatal(ch.Name, "status=all differs from omitted status")
			}
			paidWant := orderedBodies(t, ch, owner, id(paid["id"]), id(otherPaid["id"]))
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{Status: "paid"}), 2, paidWant)
			voidWant := orderedBodies(t, ch, owner, id(voided["id"]))
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{Status: "voided"}), 1, voidWant)
			pastWant := orderedBodies(t, ch, owner, id(past["id"]))
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{BusinessDate: yesterday.Format(time.DateOnly)}), 1, pastWant)
			mineWant := orderedBodies(t, ch, owner, id(paid["id"]), id(voided["id"]))
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{CustomerID: &s.customer}), 2, mineWant)
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{Limit: 1}), 3, allWant[:1])
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{Limit: 1, Offset: 1}), 3, allWant[1:2])
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{Limit: 1, Offset: 10}), 3, []any{})
		})
	}
}

func TestListOrdersBusinessDateDefault(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	s.SetNow(time.Date(2026, 3, 9, 12, 0, 0, 0, settings.Location))
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": "2026-03-10"}, 200)
	s.SetNow(time.Date(2026, 3, 10, 12, 0, 0, 0, settings.Location))
	before := s.paid(s.customerOrder(line), payment("cash", "100"))
	s.SetNow(time.Date(2026, 3, 11, 3, 0, 0, 0, settings.Location))
	after := s.paid(s.customerOrder(line), payment("cash", "100"))
	s.SetNow(time.Date(2026, 3, 11, 1, 30, 0, 0, settings.Location))
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{}), 1, orderedBodies(t, ch, owner, id(before["id"])))
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{BusinessDate: "2026-03-11"}), 1, orderedBodies(t, ch, owner, id(after["id"])))
		})
	}
}

func TestListOrdersStoreIsolation(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 3, 10, 12, 0, 0, 0, settings.Location))
	paid := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	otherStore := s.otherStore()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO orders (tenant_id,store_id,customer_id,gender,designated,status,settings_snapshot_id,business_date,opened_by,opened_at,merged_into,checkout_id,backfill)
 SELECT tenant_id,$1,customer_id,gender,designated,status,settings_snapshot_id,business_date,opened_by,opened_at,merged_into,checkout_id,backfill FROM orders WHERE id=$2`, otherStore, id(paid["id"]))
	dbtest.Commit(t, tx)
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			assertPage(t, ch, ch.Call(t, owner, "list_orders", orders.ListOrdersInput{}), 1, orderedBodies(t, ch, owner, id(paid["id"])))
		})
	}
}

func TestListOrdersPermission(t *testing.T) {
	s := open(t)
	owner := s.MustCaller(s.Owner)
	delete(owner.Permissions, perm.OrderCreate)
	for _, ch := range s.Channels() {
		r := ch.Call(t, owner, "list_orders", orders.ListOrdersInput{})
		if r.Status != 403 || r.Detail() != "permission denied" {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestListOrdersQueryCount(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 3, 10, 12, 0, 0, 0, settings.Location))
	day := settings.Date(s.Now()).Format(time.DateOnly)
	line := s.checkoutLine()
	s.paid(s.customerOrder(line), payment("cash", "100"))
	for range 4 {
		s.paid(s.customerOrder(line), payment("cash", "100"))
	}
	s.void(s.paid(s.customerOrder(line), payment("cash", "100")))
	counter := &paymentQueryCounter{}
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
		for i, limit := range []int32{1, 100} {
			counter.all.Store(0)
			r := ch.Call(t, caller, "list_orders", orders.ListOrdersInput{BusinessDate: day, Limit: limit})
			if r.Status != 200 || len(r.Body["items"].([]any)) != []int{1, 6}[i] {
				t.Fatal(ch.Name, r)
			}
			if counter.all.Load() == 0 {
				t.Fatal(ch.Name, "query counter observed no SQL")
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, "query count grew with rows", counts)
		}
	}
}

func TestListOrdersSchema(t *testing.T) {
	s := open(t)
	owner := s.MustCaller(s.Owner)
	for _, tc := range []struct {
		in    orders.ListOrdersInput
		field string
	}{{orders.ListOrdersInput{Status: "open"}, "status"}, {orders.ListOrdersInput{Limit: 101}, "limit"}} {
		for _, ch := range s.Channels() {
			assertSchemaField(t, ch, ch.Call(t, owner, "list_orders", tc.in), tc.field)
		}
	}
}

func assertSchemaField(t *testing.T, ch optest.Channel, r optest.Result, field string) {
	t.Helper()
	if r.Status != 422 {
		t.Fatal(ch.Name, r)
	}
	if ch.Name == "http" {
		for _, raw := range r.Body["errors"].([]any) {
			if location, _ := raw.(map[string]any)["location"].(string); location == "body."+field {
				return
			}
		}
		t.Fatal(ch.Name, "errors do not point to", field, r)
	}
	if !strings.Contains(r.Detail(), field+":") {
		t.Fatal(ch.Name, "detail does not point to", field, r)
	}
}

func TestListOrdersTieBreakByID(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 3, 10, 12, 0, 0, 0, settings.Location))
	line := s.checkoutLine()
	first := s.paid(s.customerOrder(line), payment("cash", "100"))
	second := s.paid(s.customerOrder(line), payment("cash", "100"))
	openedAt := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	if _, err := s.Pool.Exec(context.Background(), `UPDATE orders SET opened_at=$1 WHERE id=$2 OR id=$3`, openedAt, id(first["id"]), id(second["id"])); err != nil {
		t.Fatal(err)
	}
	ids := []string{id(first["id"]).String(), id(second["id"]).String()}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, owner, "list_orders", orders.ListOrdersInput{Status: "paid"})
			if r.Status != 200 {
				t.Fatal(r)
			}
			items := r.Body["items"].([]any)
			if len(items) != 2 || items[0].(map[string]any)["id"] != ids[0] || items[1].(map[string]any)["id"] != ids[1] {
				t.Fatal(ch.Name, items, ids)
			}
		})
	}
}

func TestListOrdersDefaultLimit(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 3, 10, 12, 0, 0, 0, settings.Location))
	paid := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO orders (tenant_id,store_id,customer_id,gender,designated,status,settings_snapshot_id,business_date,opened_by,opened_at,backfill,checkout_id)
 SELECT tenant_id,store_id,customer_id,gender,designated,status,settings_snapshot_id,business_date,opened_by,opened_at,backfill,checkout_id FROM orders CROSS JOIN generate_series(1,50) WHERE id=$1`, id(paid["id"]))
	dbtest.Commit(t, tx)
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		r := ch.Call(t, owner, "list_orders", orders.ListOrdersInput{BusinessDate: "2026-03-10"})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 50 || r.Body["total"] != float64(51) {
			t.Fatal(ch.Name, r)
		}
	}
}
