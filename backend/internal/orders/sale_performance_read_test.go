package orders_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestSalePerformanceRead(t *testing.T) {
	s := open(t)
	mine, token := s.Hire("本人", "098", "sale-reader", s.Role("发型师"))
	ct := s.paymentType("销售卡", nil)
	s.call("open_card", map[string]any{"customer_id": s.customer, "card_type_id": ct["id"], "principal": "100", "bonus": "20", "sales_staff_ids": []uuid.UUID{mine, s.Res.OwnerID}}, 200)
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	packet := s.call("issue_red_packet", map[string]any{"customer_id": s.customer, "kind": "sold", "face_value": "100", "price": "88", "expires_on": "2999-01-01", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}}, 200)
	day := settings.Date(time.Now()).Format(time.DateOnly)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			for _, tc := range []struct {
				token  string
				filter any
				want   int
				total  string
			}{{s.Owner, nil, 3, "188.00"}, {token, nil, 1, "50.00"}, {token, s.Res.OwnerID, 0, "0"}, {s.Owner, mine, 1, "50.00"}} {
				in := map[string]any{"date_from": day, "date_to": day, "kind": "sale"}
				if tc.filter != nil {
					in["staff_id"] = tc.filter
				}
				r := ch.Call(t, s.MustCaller(tc.token), "list_performance", in)
				if r.Status != 200 || len(r.Body["entries"].([]any)) != tc.want {
					t.Fatal(r)
				}
				totals := r.Body["totals"].([]any)
				if len(totals) != 4 {
					t.Fatal(r)
				}
				for i, k := range []string{"item", "consume", "labor", "sale"} {
					total := totals[i].(map[string]any)
					want := "0"
					if k == "sale" {
						want = tc.total
					}
					if total["kind"] != k || total["amount"] != want || total["paid_amount"] != want {
						t.Fatal(total)
					}
				}
				for _, raw := range r.Body["entries"].([]any) {
					e := raw.(map[string]any)
					if e["kind"] != "sale" || e["ratio"] != float64(100) || e["amount"] != e["paid_amount"] || e["staff"] == nil || e["reversal"] != false {
						t.Fatal(e)
					}
					for _, key := range []string{"order_id", "order_item_id", "item_name", "checkout_id", "settings_snapshot_id", "slot", "position_id", "reverses_id"} {
						v, exists := e[key]
						if !exists || v != nil {
							t.Fatal(key, e)
						}
					}
					if e["method"] == "red_packet_sold" {
						if e["red_packet_id"] != packet["id"] || e["card_ledger_id"] != nil || e["purchase"] != "repeat" {
							t.Fatal(e)
						}
					} else if e["method"] != "card_open" || e["card_ledger_id"] == nil || e["red_packet_id"] != nil || e["purchase"] != "first" {
						t.Fatal(e)
					}
					if tc.token == token && e["staff"].(map[string]any)["id"] != mine.String() {
						t.Fatal(e)
					}
				}
			}
		})
	}
}

func TestSalePerformanceQueryCount(t *testing.T) {
	s := open(t)
	ct := s.paymentType("销售查询", nil)
	s.paymentCard(ct, "100", "0")
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
	for _, ch := range app.Channels() {
		counts := []int64{}
		for _, n := range []int{0, 4} {
			for range n {
				s.paymentCard(ct, "100", "0")
			}
			counter.all.Store(0)
			r := ch.Call(t, caller, "list_performance", map[string]any{"date_from": day, "date_to": day, "kind": "sale"})
			if r.Status != 200 || len(r.Body["entries"].([]any)) < n+1 {
				t.Fatal(r)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal("queries grew", counts)
		}
	}
}
