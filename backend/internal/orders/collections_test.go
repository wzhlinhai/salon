package orders_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/collections"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestCheckoutCollections(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	at := time.Date(2027, 2, 3, 11, 0, 0, 0, settings.Location)
	s.SetNow(at)
	cash, union := payment("cash", "25"), payment("unionpay", "30")
	union.ExternalRef = new("银联小票-001")
	paid := s.paid(o, cash, union, payment("wechat", "20"), payment("alipay", "25"))
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_collections", collections.ListCollectionsInput{OrderID: id(o["id"])})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 2 {
			t.Fatal(r)
		}
		rows := r.Body["items"].([]any)
		ps := paid["payments"].([]any)
		for i, v := range rows {
			c := v.(map[string]any)
			if c["status"] != "succeeded" || c["order_id"] != o["id"] || c["order_payment_id"] != ps[i].(map[string]any)["id"] || c["channel"] != []string{"cash", "unionpay_register"}[i] || c["amount"] != []string{"25.00", "30.00"}[i] {
				t.Fatal(c)
			}
			if i == 1 && c["external_ref"] != "银联小票-001" {
				t.Fatal(c)
			}
		}
	}
	s.call("void_order", voidInput(paid), 200)
	r := s.call("list_collections", collections.ListCollectionsInput{OrderID: id(o["id"])}, 200)
	for _, v := range r["items"].([]any) {
		c := v.(map[string]any)
		if c["status"] != "cancelled" {
			t.Fatal(c)
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var n int
		if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM collection_events WHERE collection_id=$1", c["id"]).Scan(&n); err != nil || n != 2 {
			t.Fatal(n, err)
		}
		dbtest.Rollback(t, tx)
	}
}

func TestCheckoutDisabledCollection(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	for _, tc := range []struct{ method, code, name string }{{"cash", "cash", "现金"}, {"unionpay", "unionpay_register", "银联登记"}} {
		o := s.customerOrder(line)
		s.call("change_setting", map[string]any{"key": "collection_channel_" + tc.code, "value": "off", "effective_from": settings.Date(s.Now()).Format(time.DateOnly)}, 200)
		before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "collections", "collection_events", "order_payments", "orders", "checkouts", "audit_log")
		r := s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, payment("wechat", "1"), payment(tc.method, "99"))))
		if r.Status != 409 || r.Detail() != "收款通道已停用："+tc.name || before != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "collections", "collection_events", "order_payments", "orders", "checkouts", "audit_log") {
			t.Fatal(r)
		}
	}
}

func TestCollectionPaymentUnique(t *testing.T) {
	s := open(t)
	paid := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM collections c JOIN order_payments p ON (c.tenant_id,c.order_payment_id)=(p.tenant_id,p.id) WHERE p.order_id=$1`, paid["id"]).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	dbtest.Rollback(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO collections(tenant_id,store_id,order_id,order_payment_id,channel,amount,status,created_by,created_at,updated_at) SELECT tenant_id,store_id,order_id,order_payment_id,channel,amount,status,created_by,created_at,updated_at FROM collections WHERE order_id=$1`, paid["id"])
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
}

func TestCollectionReadQueryCount(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
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
	for _, ch := range app.Channels() {
		counts := []int64{}
		for i := range 2 {
			if i == 1 {
				tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
				dbtest.Exec(t, tx, `INSERT INTO collections(tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) SELECT tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at FROM collections WHERE order_id=$1`, o["id"])
				dbtest.Commit(t, tx)
			}
			counter.all.Store(0)
			r := ch.Call(t, s.MustCaller(s.Owner), "list_collections", collections.ListCollectionsInput{OrderID: id(o["id"])})
			if r.Status != 200 {
				t.Fatal(r)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, counts)
		}
	}
}
