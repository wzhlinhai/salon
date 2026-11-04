package attendance_test

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

func TestAttendanceQueryCount(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	shift := create(t, s, "班1", "09:00", "18:00")
	assign(t, s, s.Res.OwnerID, shift["id"], day)

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
				shift := create(t, s, fmt.Sprintf("班%d", i), "09:00", "18:00")
				assign(t, s, id, shift["id"], day)
				assign(t, s, id, nil, day.AddDate(0, 0, 2))
			}
		}
		for _, ch := range app.Channels() {
			for _, op := range []string{"list_shifts", "list_staff_shifts"} {
				counter.count.Store(0)
				args := map[string]any{}
				if op == "list_staff_shifts" {
					args["date"] = day.Format("2006-01-02")
				}
				rows := entries(t, ch.Call(t, caller, op, args))
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
