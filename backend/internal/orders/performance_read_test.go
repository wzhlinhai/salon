package orders_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestPerformanceRead(t *testing.T) {
	s := open(t)
	mine, token := s.Hire("本人", "098", "performance", s.Role("发型师"))
	line := s.performanceLine(nil, nil)
	line.Staff[0].StaffID = mine
	s.paid(s.customerOrder(line), payment("cash", "100"))
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	day := settings.Date(time.Now()).Format(time.DateOnly)
	args := map[string]any{"date_from": day, "date_to": day}
	owner, self := s.MustCaller(s.Owner), s.MustCaller(token)
	denied := owner
	denied.Permissions = map[ops.Permission]ops.Grant{}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			all := ch.Call(t, owner, "list_performance", args)
			if all.Status != 200 || len(all.Body["entries"].([]any)) != 3 || all.Body["totals"].([]any)[0].(map[string]any)["paid_amount"] != "200.00" {
				t.Fatal(all)
			}
			own := ch.Call(t, self, "list_performance", args)
			if own.Status != 200 || len(own.Body["entries"].([]any)) != 1 || own.Body["totals"].([]any)[0].(map[string]any)["paid_amount"] != "50.00" {
				t.Fatal(own)
			}
			ownEntry := own.Body["entries"].([]any)[0].(map[string]any)
			if ownEntry["staff"].(map[string]any)["id"] != mine.String() {
				t.Fatal(ownEntry)
			}
			filtered := map[string]any{"date_from": day, "date_to": day, "staff_id": s.employee}
			r := ch.Call(t, self, "list_performance", filtered)
			if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 || r.Body["totals"].([]any)[0].(map[string]any)["paid_amount"] != "0" {
				t.Fatal(r)
			}
			r = ch.Call(t, owner, "list_performance", filtered)
			if r.Status != 200 || len(r.Body["entries"].([]any)) != 1 || r.Body["totals"].([]any)[0].(map[string]any)["paid_amount"] != "50.00" {
				t.Fatal(r)
			}
			r = ch.Call(t, denied, "list_performance", args)
			if r.Status != 403 || r.Detail() != "permission denied" {
				t.Fatal(r)
			}
			for _, tc := range []struct{ from, to, detail string }{
				{"2026-01-01", "2026-04-04", "查询范围最多 92 天"},
				{"2026-02-01", "2026-01-01", "开始营业日不能晚于结束营业日"},
			} {
				r = ch.Call(t, owner, "list_performance", map[string]any{"date_from": tc.from, "date_to": tc.to})
				if r.Status != 409 || r.Detail() != tc.detail {
					t.Fatal(r)
				}
			}
			r = ch.Call(t, owner, "list_performance", map[string]any{"date_from": "2026-01-01", "date_to": "2026-04-03"})
			if r.Status != 200 {
				t.Fatal(r)
			}
			page := ch.Call(t, owner, "list_performance", map[string]any{"date_from": day, "date_to": day, "limit": 1})
			if page.Status != 200 || len(page.Body["entries"].([]any)) != 1 || !reflect.DeepEqual(page.Body["totals"], all.Body["totals"]) {
				t.Fatal(page)
			}
			last := page.Body["entries"].([]any)[0].(map[string]any)["id"]
			next := ch.Call(t, owner, "list_performance", map[string]any{"date_from": day, "date_to": day, "before_id": last})
			if next.Status != 200 || len(next.Body["entries"].([]any)) != 2 || !reflect.DeepEqual(next.Body["totals"], all.Body["totals"]) {
				t.Fatal(next)
			}
			for _, v := range next.Body["entries"].([]any) {
				if v.(map[string]any)["id"].(string) >= last.(string) {
					t.Fatal(next)
				}
			}
		})
	}
}

func TestPerformanceStoreTenantScope(t *testing.T) {
	s := open(t)
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	otherStore := s.otherStore()
	// Valid same-tenant references deliberately seed an otherwise invisible store row.
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO performance_entries(tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,method,paid_amount,ratio,amount)
 SELECT tenant_id,$1,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,method,paid_amount,ratio,amount FROM performance_entries LIMIT 1`, otherStore)
	dbtest.Commit(t, tx)
	foreign := optest.OpenShop(t)
	foreignCaller := foreign.MustCaller(foreign.Owner)
	foreignCaller.StoreID = s.Res.StoreID
	// A caller authenticated in another tenant cannot see this tenant's rows at all.
	for _, ch := range s.Channels() {
		day := settings.Date(time.Now()).Format(time.DateOnly)
		args := map[string]any{"date_from": day, "date_to": day}
		r := ch.Call(t, s.MustCaller(s.Owner), "list_performance", args)
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 1 || r.Body["totals"].([]any)[0].(map[string]any)["paid_amount"] != "100.00" {
			t.Fatal(r)
		}
		r = ch.Call(t, foreignCaller, "list_performance", args)
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 || r.Body["totals"].([]any)[0].(map[string]any)["paid_amount"] != "0" {
			t.Fatal(r)
		}
	}
}

func TestPerformanceQueryCount(t *testing.T) {
	s := open(t)
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
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
	day := settings.Date(time.Now()).Format(time.DateOnly)
	args := map[string]any{"date_from": day, "date_to": day}
	for _, ch := range app.Channels() {
		counts := []int64{}
		for i := range 2 {
			if i == 1 {
				s.paid(s.customerOrder(s.performanceLine(nil, nil)), payment("cash", "100"))
			}
			counter.all.Store(0)
			r := ch.Call(t, caller, "list_performance", args)
			if r.Status != 200 {
				t.Fatal(r)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal("queries grew", counts)
		}
	}
}

func TestPerformanceReverseUnreversed(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	s.void(o)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	var v uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT id FROM order_voids WHERE order_id=$1", o["id"]).Scan(&v); err != nil {
		t.Fatal(err)
	}
	err := q.ReversePerformanceOrder(context.Background(), dbgen.ReversePerformanceOrderParams{StoreID: s.Res.StoreID, OrderID: id(o["id"]), VoidID: &v, Day: pgDateForPerformance(settings.Date(time.Now()))})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM performance_entries").Scan(&n); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if n != 2 {
		t.Fatal(n)
	}
}
