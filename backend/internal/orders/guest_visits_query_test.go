package orders_test

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"testing"
	"time"
)

func TestGuestVisitQueryCount(t *testing.T) {
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
			r := ch.Call(t, caller, "list_guest_visits", args)
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
