package orders_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

type previewDayCounter struct{ queries atomic.Int64 }

func (c *previewDayCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: BusinessDayEndHistory :") {
		c.queries.Add(1)
	}
	return ctx
}
func (*previewDayCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPreviewCheckoutBusinessDateQueries(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_balance")
	card := s.paymentCard(s.paymentType("余额卡", nil), "100", "0")
	order := s.customerOrder(s.checkoutLine())
	counter := &previewDayCounter{}
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
	for _, ch := range app.Channels() {
		for _, mode := range []string{"none", "explicit", "auto"} {
			input := orders.PreviewCheckoutInput{OrderID: id(order["id"])}
			want := int64(0)
			if mode == "explicit" {
				input.CardID = new(id(card["id"]))
				want = 1
			}
			if mode == "auto" {
				input.AutoCard = true
				want = 1
			}
			counter.queries.Store(0)
			result := ch.Call(t, s.MustCaller(s.Owner), "preview_checkout", input)
			if result.Status != 200 || counter.queries.Load() != want {
				t.Fatal(ch.Name, mode, result, counter.queries.Load())
			}
		}
	}
}
