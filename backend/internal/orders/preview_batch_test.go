package orders_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func TestPreviewCheckoutQueryCount(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_discount")
	ct := s.paymentType("最佳卡", "80")
	card := s.paymentCard(ct, "1000", "0")
	line := s.checkoutLine()
	one := s.customerOrder(line)
	item := s.call("create_item", s.itemInput("另一项目", 0), 200)
	other := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
	many := s.customerOrder(line, other, line, other, line)
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
	counts := map[string][]int64{}
	before := s.cardSnapshot()
	for stage := range 2 {
		for _, ch := range app.Channels() {
			for _, mode := range []string{"auto", "explicit", "lines"} {
				order := one
				in := orders.PreviewCheckoutInput{OrderID: id(order["id"]), CardID: new(id(card["id"]))}
				if mode == "auto" {
					in.AutoCard, in.CardID = true, nil
				}
				if mode == "lines" && stage == 1 {
					order = many
					in.OrderID = id(many["id"])
				}
				counter.all.Store(0)
				result := ch.Call(t, caller, "preview_checkout", in)
				n := counter.all.Load()
				key := ch.Name + "/" + mode
				counts[key] = append(counts[key], n)
				wantLines, wantDiscount, wantNet, wantReceivable := 1, "20.00", "80.00", "100.00"
				if mode == "lines" && stage == 1 {
					wantLines, wantDiscount, wantNet, wantReceivable = 5, "100.00", "400.00", "500.00"
				}
				if result.Status != 200 || result.Body["id"] != order["id"] || result.Body["pricing_card_id"] != card["id"] || result.Body["receivable"] != wantReceivable || result.Body["discount"] != wantDiscount || result.Body["net"] != wantNet || len(result.Body["items"].([]any)) != wantLines || len(result.Body["payments"].([]any)) != 0 {
					t.Fatal(key, result)
				}
				for _, raw := range result.Body["items"].([]any) {
					item := raw.(map[string]any)
					if item["price"] != "100.00" || item["discount"] != "20.00" {
						t.Fatal(key, item)
					}
				}
			}
		}
		if s.cardSnapshot() != before {
			t.Fatal("preview wrote data")
		}
		if stage == 0 {
			for i := range 4 {
				s.paymentCard(s.paymentType(fmt.Sprintf("候选%d", i), "90"), "1000", "0")
			}
			before = s.cardSnapshot()
		}
	}
	for key, pair := range counts {
		t.Log(key, pair)
		if pair[0] != pair[1] {
			t.Error(key, "SQL query count grew", pair)
		}
	}
}
