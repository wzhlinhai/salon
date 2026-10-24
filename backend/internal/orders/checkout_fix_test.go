package orders_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func TestCashCheckoutDoesNotLockItems(t *testing.T) {
	for _, withCard := range []bool{false, true} {
		name := "cash only"
		if withCard {
			name = "mixed call"
		}
		t.Run(name, func(t *testing.T) {
			s := open(t)
			line := s.checkoutLine()
			cashOrder := s.customerOrder(line)
			in := checkoutInput(checkoutOrder(cashOrder, payment("cash", "100")))
			if withCard {
				c := s.paymentCard(s.paymentType("卡", nil), "100", "0")
				item := s.call("create_item", s.itemInput("卡计价项目", 0), 200)
				cardOrder := s.customerOrder(orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}})
				in.Orders = append(in.Orders, checkoutOrder(cardOrder, cardPayment(c, "100")))
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", line.ItemID)
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", in) }()
			var result optest.Result
			waited := false
			select {
			case result = <-done:
			case <-time.After(2 * time.Second):
				waited = true
			}
			dbtest.Rollback(t, tx)
			if waited {
				optest.WaitResult(t, done, 200)
				t.Fatal("checkout waited for an item belonging to an order without a pricing card")
			}
			if result.Status != 200 {
				t.Fatal(result)
			}
			cash := lines(result.Body)[0].(map[string]any)
			if cash["discount"] != "0.00" || cash["net"] != "100.00" || firstLine(cash)["price_source"] != "standard" {
				t.Fatal(cash)
			}
		})
	}
}

func TestPreviewSnapshotRules(t *testing.T) {
	for _, key := range []string{"member_price_rule", "others_card_payment"} {
		t.Run(key, func(t *testing.T) {
			s := open(t)
			ct := s.paymentType("卡", nil)
			c := s.paymentCard(ct, "1000", "0")
			input := s.itemInput("会员项目", 0)
			input["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "120"}}
			item := s.call("create_item", input, 200)
			line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
			s.paymentSetting("member_price_rule", "member_first")
			s.paymentSetting("others_card_payment", "allowed")
			o := s.customerOrder(line)
			if key == "others_card_payment" {
				o = s.call("open_order", s.opening(line), 200)
			}
			if o["receivable"] != "100.00" {
				t.Fatal(o)
			}
			if key == "others_card_payment" && o["customer_id"] == c["customer_id"] {
				t.Fatal("expected another customer's order", o, c)
			}
			next := "lowest"
			if key == "others_card_payment" {
				next = "not_allowed"
			}
			s.paymentSetting(key, next)
			before := s.cardSnapshot()
			for _, ch := range s.Channels() {
				result := ch.Call(t, s.MustCaller(s.Owner), "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), CardID: new(id(c["id"]))})
				if result.Status != 200 || result.Body["receivable"] != "120.00" || result.Body["discount"] != "0.00" || result.Body["net"] != "120.00" || firstLine(result.Body)["price_source"] != "member" || firstLine(result.Body)["card_type_id"] != ct["id"] || len(result.Body["payments"].([]any)) != 0 || s.cardSnapshot() != before {
					t.Fatal(ch.Name, result)
				}
			}
		})
	}
}

type paymentQueryCounter struct{ all, payments atomic.Int64 }

func (c *paymentQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	if strings.HasPrefix(data.SQL, "-- name: ListOrderPayments :") {
		c.payments.Add(1)
	}
	return ctx
}
func (*paymentQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestOrderPaymentViewQueryCount(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("卡", nil), "1000", "0")
	line := s.checkoutLine()
	one, many := s.customerOrder(line), s.customerOrder(line)
	s.paid(one, cardPayment(c, "100"))
	ps := make([]orders.PaymentInput, 10)
	for i := range ps {
		ps[i] = cardPayment(c, "10")
	}
	s.paid(many, ps...)
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
	for _, ch := range app.Channels() {
		counts := []int64{}
		for i, o := range []map[string]any{one, many} {
			counter.all.Store(0)
			counter.payments.Store(0)
			result := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(o["id"])})
			if result.Status != 200 || len(result.Body["payments"].([]any)) != []int{1, 10}[i] || counter.payments.Load() != 1 {
				t.Fatal(ch.Name, result, counter.payments.Load())
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, "SQL query count grew with card payments", counts)
		}
	}
}
