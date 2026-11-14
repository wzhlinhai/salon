package reports_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/optest"
)

type reportQueryCounter struct{ count atomic.Int64 }

func (c *reportQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}

func (*reportQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestReportQueryCounts(t *testing.T) {
	f := open(t)
	source := id(f.call("create_customer_source", map[string]any{"name": "计数来源"}, 200)["id"])
	customer := f.customer("顾客甲", "13800000061", &source)
	f.now("2026-11-02", 12)
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	f.openCard(customer, f.stored, "10.00", "0", payment("cash", "10"))

	counter := &reportQueryCounter{}
	config := f.Pool.Config().Copy()
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
	caller := f.MustCaller(f.Owner)
	cases := []struct {
		name string
		args map[string]any
		rows string
	}{
		{"get_business_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02", "granularity": "day"}, ""},
		{"get_staff_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, "rows"},
		{"get_item_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, "rows"},
		{"get_card_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, "rows"},
		{"get_customer_source_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, "rows"},
	}
	baseline := map[string]map[string]int64{}
	rowCounts := map[string]int{}
	for _, tc := range cases {
		baseline[tc.name] = map[string]int64{}
		for _, ch := range app.Channels() {
			counter.count.Store(0)
			r := ch.Call(t, caller, tc.name, tc.args)
			if r.Status != 200 {
				t.Fatal(ch.Name, tc.name, r)
			}
			baseline[tc.name][ch.Name] = counter.count.Load()
			if tc.rows != "" {
				rowCounts[tc.name] = len(r.Body[tc.rows].([]any))
			}
		}
	}

	otherSource := id(f.call("create_customer_source", map[string]any{"name": "计数来源二"}, 200)["id"])
	other := f.customer("顾客乙", "13800000062", &otherSource)
	f.checkout(f.order(other, []uuid.UUID{f.staffB}, true, "male", f.goods), payment("cash", "50"))
	f.openCard(other, f.count, "0", "0", payment("cash", "300"))
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, false, "female", f.item), payment("cash", "100"))

	for _, tc := range cases {
		for _, ch := range app.Channels() {
			counter.count.Store(0)
			r := ch.Call(t, caller, tc.name, tc.args)
			if r.Status != 200 {
				t.Fatal(ch.Name, tc.name, r)
			}
			if got := counter.count.Load(); got != baseline[tc.name][ch.Name] {
				t.Fatalf("%s %s queries grew: %d -> %d", ch.Name, tc.name, baseline[tc.name][ch.Name], got)
			}
			if tc.rows != "" && len(r.Body[tc.rows].([]any)) <= rowCounts[tc.name] {
				t.Fatalf("%s %s rows did not grow: %d", ch.Name, tc.name, len(r.Body[tc.rows].([]any)))
			}
		}
	}

	for _, day := range []string{"2026-11-03", "2026-11-04"} {
		f.now(day, 12)
		f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	}
	for _, ch := range app.Channels() {
		counter.count.Store(0)
		r := ch.Call(t, caller, "get_business_report", map[string]any{"from": "2026-11-02", "to": "2026-11-04", "granularity": "day"})
		if r.Status != 200 || len(r.Body["rows"].([]any)) != 3 {
			t.Fatal(ch.Name, r)
		}
		if got := counter.count.Load(); got != baseline["get_business_report"][ch.Name] {
			t.Fatalf("%s business report queries grow with days: %d -> %d", ch.Name, baseline["get_business_report"][ch.Name], got)
		}
	}
}
