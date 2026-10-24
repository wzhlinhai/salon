package redpackets_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

type queryCounter struct{ all, sales, payments atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	if strings.HasPrefix(data.SQL, "-- name: ListRedPacketSales :") {
		c.sales.Add(1)
	}
	if strings.HasPrefix(data.SQL, "-- name: ListRedPacketPayments :") {
		c.payments.Add(1)
	}
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestRedPacketListQueryCount(t *testing.T) {
	s := open(t)
	other := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)
	customers := []uuid.UUID{s.customer, id(other["id"])}
	for i, customer := range customers {
		packets := []uuid.UUID{}
		for range []int{1, 10}[i] {
			in := s.input("sold")
			in["customer_id"] = customer
			packets = append(packets, id(s.call("issue_red_packet", in, 200)["id"]))
		}
		order := s.call("open_order", map[string]any{"customer_id": customer, "gender": "female", "designated": false, "items": []any{}}, 200)
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var checkout uuid.UUID
		if err := tx.QueryRow(context.Background(), `INSERT INTO checkouts(tenant_id,store_id,business_date,checked_out_by) VALUES($1,$2,'2020-01-01',$3) RETURNING id`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID).Scan(&checkout); err != nil {
			t.Fatal(err)
		}
		for _, packet := range packets {
			var payment uuid.UUID
			if err := tx.QueryRow(context.Background(), `INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount,red_packet_id) VALUES($1,$2,$3,$4,'red_packet',1,$5) RETURNING id`, s.Res.TenantID, s.Res.StoreID, checkout, order["id"], packet).Scan(&payment); err != nil {
				t.Fatal(err)
			}
			dbtest.Exec(t, tx, "UPDATE red_packets SET order_payment_id=$2 WHERE id=$1", packet, payment)
		}
		dbtest.Commit(t, tx)
	}
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
		for i, customer := range customers {
			counter.all.Store(0)
			counter.sales.Store(0)
			counter.payments.Store(0)
			r := ch.Call(t, caller, "list_customer_red_packets", map[string]any{"customer_id": customer})
			if r.Status != 200 || len(r.Body["items"].([]any)) != []int{1, 10}[i] || counter.sales.Load() != 1 || counter.payments.Load() != 1 {
				t.Fatal(ch.Name, r, counter.sales.Load(), counter.payments.Load())
			}
			counts = append(counts, counter.all.Load())
			for _, raw := range r.Body["items"].([]any) {
				p := raw.(map[string]any)
				if p["status"] != "used" || p["order_id"] == nil || p["used_amount"] != "1.00" || len(p["sales_staff"].([]any)) != 1 {
					t.Fatal(p)
				}
			}
		}
		t.Logf("%s list N=1: %d SQL; N=10: %d SQL", ch.Name, counts[0], counts[1])
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, "红包列表查询数随条数增长", counts)
		}
	}
}
