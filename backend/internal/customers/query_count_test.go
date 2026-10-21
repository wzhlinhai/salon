package customers_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/optest"
)

type queryCounter struct {
	all, views, locks, expectedLocks atomic.Int64
	earlyView                        atomic.Bool
}

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	if strings.HasPrefix(data.SQL, "-- name: LockCustomer :") {
		c.locks.Add(1)
	}
	if strings.Contains(data.SQL, "-- name: ListCustomerView") {
		c.views.Add(1)
		if c.locks.Load() < c.expectedLocks.Load() {
			c.earlyView.Store(true)
		}
	}
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestCustomerViewQueryCount(t *testing.T) {
	s := open(t)
	source := s.call("create_customer_source", map[string]any{"name": "计数来源"}, 200)
	ids := []any{}
	for range 50 {
		c := s.create(map[string]any{"name": "批量计数", "source_id": source["id"]})
		ids = append(ids, c["id"])
		s.call("add_safety_label", map[string]any{"customer_id": c["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}, 200)
	}
	s.call("assign_customer_responsible", map[string]any{"customer_ids": ids, "staff_id": s.Res.OwnerID}, 200)
	counter := &queryCounter{}
	cfg := s.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
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
		for _, n := range []int{1, 50} {
			counter.all.Store(0)
			r := ch.Call(t, caller, "search_customers", map[string]any{"query": "批量计数", "include_unidentified": true, "limit": n})
			assertEqual(t, r.Status, 200)
			assertEqual(t, len(r.Body["items"].([]any)), n)
			counts = append(counts, counter.all.Load())
			for _, item := range r.Body["items"].([]any) {
				c := item.(map[string]any)
				assertEqual(t, c["source"].(map[string]any)["id"], source["id"])
				assertEqual(t, c["responsible"].(map[string]any)["id"], s.Res.OwnerID.String())
				assertEqual(t, len(c["active_safety_labels"].([]any)), 1)
			}
		}
		t.Logf("%s search N=1: %d SQL; N=50: %d SQL", ch.Name, counts[0], counts[1])
		assertEqual(t, counts[0], counts[1])
	}
	for _, n := range []int{1, 20} {
		counter.views.Store(0)
		counter.locks.Store(0)
		counter.expectedLocks.Store(int64(n))
		counter.earlyView.Store(false)
		r := app.HTTP(t, &caller, "assign_customer_responsible", map[string]any{"customer_ids": ids[:n], "staff_id": s.Res.OwnerID})
		assertEqual(t, r.Status, 200)
		assertEqual(t, len(r.Body["items"].([]any)), n)
		t.Logf("assign N=%d: %d view SELECTs", n, counter.views.Load())
		assertEqual(t, counter.views.Load(), int64(6))
		assertEqual(t, counter.locks.Load(), int64(n))
		assertEqual(t, counter.earlyView.Load(), false)
	}
}
