package orders_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestCountPaymentDatabaseProtections(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line, line)
	after := s.paid(o, countPayment(c, firstLine(o), "100"), payment("cash", "100"))
	ledger := s.countEntries(c)[0].(map[string]any)["id"]
	second := o["items"].([]any)[1].(map[string]any)["id"]
	for _, tc := range []struct{ name, values string }{
		{"positive paid", "2,1,3,2,-2,0"},
		{"positive gift", "2,-2,0,2,1,3"},
		{"minus two", "2,-2,0,1,0,1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), `INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id) VALUES ($1,$2,$3,$4,'consume',`+tc.values+`,current_date,$5)`, s.Res.TenantID, s.Res.StoreID, c["id"], line.ItemID, s.Res.OwnerID)
			dbtest.Rollback(t, tx)
			var pg interface{ SQLState() string }
			if !errors.As(err, &pg) || pg.SQLState() != "23514" {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		name, method       string
		card, item, ledger any
		code               string
	}{
		{"missing item", "count", c["id"], nil, ledger, "23514"},
		{"missing ledger", "count", c["id"], second, nil, "23514"},
		{"missing card", "count", nil, second, ledger, "23514"},
		{"cash item", "cash", nil, second, nil, "23514"},
		{"cash count ledger", "cash", nil, nil, ledger, "23514"},
		{"duplicate count ledger", "count", c["id"], second, ledger, "23505"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id,order_item_id,card_count_ledger_id) VALUES ($1,$2,$3,$4,$5,100,$6,$7,$8)`, s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"], tc.method, tc.card, tc.item, tc.ledger)
			dbtest.Rollback(t, tx)
			var pg interface{ SQLState() string }
			if !errors.As(err, &pg) || pg.SQLState() != tc.code {
				t.Fatal(err)
			}
		})
	}
	// 使用另一条合法流水，确保此处守住的是一行一次的部分唯一索引。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var newLedger string
	if err := tx.QueryRow(context.Background(), `INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id) VALUES ($1,$2,$3,$4,'consume',1,-1,0,1,0,1,current_date,$5) RETURNING id`, s.Res.TenantID, s.Res.StoreID, c["id"], line.ItemID, s.Res.OwnerID).Scan(&newLedger); err != nil {
		t.Fatal(err)
	}
	_, err := tx.Exec(context.Background(), `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id,order_item_id,card_count_ledger_id) VALUES ($1,$2,$3,$4,'count',100,$5,$6,$7)`, s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"], c["id"], firstLine(o)["id"], newLedger)
	dbtest.Rollback(t, tx)
	var pg interface{ SQLState() string }
	if !errors.As(err, &pg) || pg.SQLState() != "23505" {
		t.Fatal(err)
	}
}
func TestCountPaymentMigrationDown(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line)
	s.paid(o, countPayment(c, firstLine(o), "100"))
	dbtest.MigrateDownTo(t, s.Pool, 19)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	if err := tx.QueryRow(context.Background(), `SELECT (SELECT count(*)=0 FROM order_payments WHERE method='count') AND (SELECT count(*)=0 FROM card_count_ledger WHERE kind='consume') AND (SELECT count(*)=0 FROM information_schema.columns WHERE table_name='order_payments' AND column_name IN ('order_item_id','card_count_ledger_id'))`).Scan(&valid); err != nil || !valid {
		t.Fatal(valid, err)
	}
	dbtest.Rollback(t, tx)
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
}

func TestCountPaymentForeignKeys(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.countCard(line, "count", 2, 1)
	o := s.customerOrder(line)
	after := s.paid(o, payment("cash", "100"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ledgerID uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id) VALUES ($1,$2,$3,$4,'consume',2,-1,1,1,0,1,current_date,$5) RETURNING id`, s.Res.TenantID, s.Res.StoreID, c["id"], line.ItemID, s.Res.OwnerID).Scan(&ledgerID); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	for _, tc := range []struct {
		name             string
		itemID, ledgerID uuid.UUID
	}{
		{"nonexistent order item", uuid.New(), ledgerID},
		{"nonexistent count ledger", id(firstLine(o)["id"]), uuid.New()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id,order_item_id,card_count_ledger_id) VALUES ($1,$2,$3,$4,'count',100,$5,$6,$7)`, s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"], c["id"], tc.itemID, tc.ledgerID)
			dbtest.Rollback(t, tx)
			var pg interface{ SQLState() string }
			if !errors.As(err, &pg) || pg.SQLState() != "23503" {
				t.Fatal(err)
			}
		})
	}
}
