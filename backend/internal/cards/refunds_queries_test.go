package cards_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

type refundQueryCounter struct{ count atomic.Int64 }

func (c *refundQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}
func (*refundQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestRefundPreviewQueryCount(t *testing.T) {
	s := open(t)
	a, b, c := s.item("剪"), s.item("烫"), s.item("染")
	one := s.call("create_card_type", countType("一项", "package", countItem(a)), 200)
	many := s.call("create_card_type", countType("三项", "package", countItem(a), countItem(b), countItem(c)), 200)
	counter := &refundQueryCounter{}
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
	counts := map[string]int64{}
	for i, ct := range []map[string]any{one, many} {
		in := s.opening()
		in["card_type_id"] = ct["id"]
		card := s.call("open_card", in, 200)
		for _, ch := range app.Channels() {
			counter.count.Store(0)
			r := ch.Call(t, caller, "preview_card_refund", map[string]any{"card_id": card["id"]})
			n := counter.count.Load()
			want := 1
			if i == 1 {
				want = 3
			}
			if r.Status != 200 || len(r.Body["items"].([]any)) != want {
				t.Fatal(r)
			}
			if i == 0 {
				counts[ch.Name] = n
			} else if counts[ch.Name] != n {
				t.Fatal(ch.Name, "queries grew", counts[ch.Name], n)
			}
		}
	}
}
