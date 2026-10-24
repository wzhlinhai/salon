package orders_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func TestOrderItemKindSnapshot(t *testing.T) {
	s := open(t)
	in := s.itemInput("洗发水", 0)
	in["kind"] = "goods"
	item := s.call("create_item", in, 200)
	o := s.customerOrder(orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}})
	if lines(o)[0].(map[string]any)["item_kind"] != "goods" {
		t.Fatal(o)
	}
	in["id"], in["active"], in["kind"] = item["id"], true, "service"
	s.call("update_item", in, 200)
	kept := s.customerOrder()
	merged := s.call("merge_orders", orders.MergeOrdersInput{OrderID: id(kept["id"]), MergedOrderIDs: []uuid.UUID{id(o["id"])}}, 200)
	if lines(merged)[0].(map[string]any)["item_kind"] != "goods" {
		t.Fatal(merged)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(kept["id"])})
		if r.Status != 200 || lines(r.Body)[0].(map[string]any)["item_kind"] != "goods" {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestServiceAndGoodsCashCheckout(t *testing.T) {
	s := open(t)
	service := s.checkoutLine()
	in := s.itemInput("洗发水", 0)
	in["kind"], in["price"] = "goods", "35.50"
	item := s.call("create_item", in, 200)
	o := s.customerOrder(service, orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}})
	ps := []orders.PaymentInput{payment("cash", "135.50")}
	paid := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(o, ps...)), 200))[0].(map[string]any)
	assertPaid(t, o, paid, ps)
	if paid["net"] != "135.50" || len(lines(paid)) != 2 {
		t.Fatal(paid)
	}
	for i, kind := range []string{"service", "goods"} {
		line := lines(paid)[i].(map[string]any)
		if line["item_kind"] != kind || line["price"] != []string{"100.00", "35.50"}[i] || line["discount"] != "0.00" {
			t.Fatal(line)
		}
	}
}

func TestItemKindMigration(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	dbtest.MigrateDownTo(t, s.Pool, 22)
	var n int
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name IN ('items','order_items') AND column_name IN ('kind','item_kind')`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var kind, snapshot string
	if err := tx.QueryRow(context.Background(), `SELECT i.kind,oi.item_kind FROM items i JOIN order_items oi ON oi.item_id=i.id WHERE oi.order_id=$1`, o["id"]).Scan(&kind, &snapshot); err != nil || kind != "service" || snapshot != "service" {
		t.Fatal(kind, snapshot, err)
	}
	dbtest.Rollback(t, tx)
	for _, tc := range []struct{ sql, state string }{
		{`UPDATE items SET kind='other'`, "23514"},
		{`UPDATE items SET kind=NULL`, "23502"},
		{`UPDATE order_items SET item_kind='goods'`, "42501"},
		{`INSERT INTO order_items(tenant_id,store_id,order_id,item_id,item_name,price,price_source,added_by) SELECT tenant_id,store_id,order_id,item_id,item_name,price,price_source,added_by FROM order_items`, "23502"},
		{`INSERT INTO order_items(tenant_id,store_id,order_id,item_id,item_name,price,price_source,added_by,item_kind) SELECT tenant_id,store_id,order_id,item_id,item_name,price,price_source,added_by,'other' FROM order_items`, "23514"},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), tc.sql)
		dbtest.Rollback(t, tx)
		var pg interface{ SQLState() string }
		if !errors.As(err, &pg) || pg.SQLState() != tc.state {
			t.Fatal(tc.sql, err)
		}
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var defaultKind string
	if err := tx.QueryRow(context.Background(), `INSERT INTO items(tenant_id,store_id,name,price) VALUES($1,$2,'默认服务',1) RETURNING kind`, s.Res.TenantID, s.Res.StoreID).Scan(&defaultKind); err != nil || defaultKind != "service" {
		t.Fatal(defaultKind, err)
	}
	dbtest.Rollback(t, tx)
}
