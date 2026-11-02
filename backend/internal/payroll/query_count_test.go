package payroll_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/optest"
)

type queryCounter struct{ count atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPayrollQueryCount(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	set(t, s, s.Res.OwnerID, m, "base", "1")
	add(t, s, s.Res.OwnerID, m, "加项", "1")
	counter := &queryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	counts := map[string]int64{}
	for _, n := range []int{1, 6} {
		if n > 1 {
			for i := 2; i <= n; i++ {
				id, _ := s.Hire(fmt.Sprintf("员工%d", i), fmt.Sprintf("%03d", i), fmt.Sprintf("worker%d", i))
				set(t, s, id, m, "guarantee", "2")
				add(t, s, id, m, "减项", "-1")
			}
		}
		for _, ch := range app.Channels() {
			for _, op := range []string{"list_base_salaries", "list_payroll_adjustments"} {
				counter.count.Store(0)
				rows := entries(t, ch.Call(t, caller, op, map[string]any{"month": month(m)}))
				count := counter.count.Load()
				if len(rows) != n {
					t.Fatal(op, len(rows), n)
				}
				key := ch.Name + op
				if n == 1 {
					counts[key] = count
				} else if count != counts[key] {
					t.Fatalf("%s queries grew: %d -> %d", key, counts[key], count)
				}
			}
		}
	}
}
