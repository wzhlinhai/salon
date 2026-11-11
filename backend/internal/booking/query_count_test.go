package booking_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/booking"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type queryCounter struct{ all atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func countingPool(t *testing.T, base *pgxpool.Pool, counter *queryCounter) *pgxpool.Pool {
	t.Helper()
	config := base.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestListAppointmentsQueryCount(t *testing.T) {
	s := openBooking(t)
	oneDay := settings.Date(s.Now()).AddDate(0, 0, 1)
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(1, "10:00")})
	for _, clock := range []string{"12:00", "14:00", "16:00"} {
		s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(2, clock)})
	}
	counter := &queryCounter{}
	pool := countingPool(t, s.Pool, counter)
	app := optest.New(t, pool)
	owner := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		counts := []int64{}
		for _, day := range []time.Time{oneDay, oneDay.AddDate(0, 0, 1)} {
			counter.all.Store(0)
			r := ch.Call(t, owner, "list_appointments", map[string]any{"from": day.Format(settings.DateLayout), "to": day.Format(settings.DateLayout)})
			if r.Status != 200 {
				t.Fatal(ch.Name, r)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatalf("%s: query count grew with rows: %v", ch.Name, counts)
		}
	}
}

func TestOccupiedStaffQueryCount(t *testing.T) {
	s := openBooking(t)
	s.block(s.staff, s.Now(), s.Now().Add(time.Hour), 200)
	counter := &queryCounter{}
	pool := countingPool(t, s.Pool, counter)
	count := func(ids []uuid.UUID) int64 {
		counter.all.Store(0)
		err := db.InTenant(context.Background(), pool, s.Res.TenantID, func(q *dbgen.Queries) error {
			occupied, err := booking.OccupiedStaff(context.Background(), q, s.Res.StoreID, ids, s.Now())
			if err != nil {
				return err
			}
			if !occupied[ids[0]] {
				t.Fatalf("staff %s should be occupied", ids[0])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return counter.all.Load()
	}
	one := count([]uuid.UUID{s.staff})
	many := count([]uuid.UUID{s.staff, s.other, uuid.New(), uuid.New(), uuid.New()})
	if one != many {
		t.Fatalf("occupancy query count grew with staff: one=%d many=%d", one, many)
	}
}
