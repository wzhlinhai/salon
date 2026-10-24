package orders_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestCardPaymentDatabaseProtections(t *testing.T) {
	s := open(t)
	ct := s.paymentType("余额", nil)
	c := s.paymentCard(ct, "200", "0")
	o := s.customerOrder(s.checkoutLine())
	after := s.paid(o, cardPayment(c, "100"))
	ledger := s.consumeEntries(c)[0].(map[string]any)["id"]
	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		{"consume positive principal", `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'consume',100,1,101,100,-2,98,current_date,$4)`, []any{s.Res.TenantID, s.Res.StoreID, c["id"], s.Res.OwnerID}},
		{"consume positive bonus", `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'consume',100,-2,98,100,1,101,current_date,$4)`, []any{s.Res.TenantID, s.Res.StoreID, c["id"], s.Res.OwnerID}},
		{"consume zero", `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'consume',100,0,100,100,0,100,current_date,$4)`, []any{s.Res.TenantID, s.Res.StoreID, c["id"], s.Res.OwnerID}},
		{"card missing both", `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount) VALUES ($1,$2,$3,$4,'card',1)`, []any{s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"]}},
		{"card missing ledger", `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id) VALUES ($1,$2,$3,$4,'card',1,$5)`, []any{s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"], c["id"]}},
		{"cash with card", `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id) VALUES ($1,$2,$3,$4,'cash',1,$5)`, []any{s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"], c["id"]}},
		{"cash with ledger", `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_ledger_id) VALUES ($1,$2,$3,$4,'cash',1,$5)`, []any{s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"], ledger}},
		{"member missing type", `UPDATE order_items SET price_source='member',level_id=NULL WHERE id=$1`, []any{firstLine(after)["id"]}},
		{"standard with type", `UPDATE order_items SET card_type_id=$2 WHERE id=$1`, []any{firstLine(after)["id"], ct["id"]}},
		{"discount above price", `UPDATE order_items SET discount=price+0.01 WHERE id=$1`, []any{firstLine(after)["id"]}},
		{"negative discount", `UPDATE order_items SET discount=-0.01 WHERE id=$1`, []any{firstLine(after)["id"]}},
		{"zero percent", `UPDATE card_types SET discount_percent=0 WHERE id=$1`, []any{ct["id"]}},
		{"hundred percent", `UPDATE card_types SET discount_percent=100 WHERE id=$1`, []any{ct["id"]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
			var pg interface{ SQLState() string }
			if !errors.As(err, &pg) || pg.SQLState() != "23514" {
				t.Fatal(err)
			}
			dbtest.Rollback(t, tx)
		})
	}
}
func TestCardPaymentMigrationDown(t *testing.T) {
	s := open(t)
	ct := s.paymentType("折扣", "88")
	c := s.paymentCard(ct, "1000", "0")
	in := s.itemInput("会员项目", 0)
	in["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "70"}}
	item := s.call("create_item", in, 200)
	line := s.checkoutLine()
	line.ItemID = id(item["id"])
	s.paid(s.customerOrder(line), cardPayment(c, "70"))
	dbtest.MigrateDownTo(t, s.Pool, 18)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	if err := tx.QueryRow(context.Background(), `SELECT (SELECT count(*)=0 FROM card_ledger WHERE kind='consume') AND (SELECT count(*)=0 FROM order_payments WHERE method='card') AND (SELECT bool_and(price_source='standard') FROM order_items) AND (SELECT count(*)=0 FROM information_schema.columns WHERE (table_name='card_types' AND column_name='discount_percent') OR (table_name='order_items' AND column_name IN ('discount','card_type_id')) OR (table_name='order_payments' AND column_name IN ('card_id','card_ledger_id')))`).Scan(&valid); err != nil || !valid {
		t.Fatal(valid, err)
	}
	dbtest.Rollback(t, tx)
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
}

func TestCardPaymentLedgerUnique(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("卡", nil), "100", "0")
	o := s.customerOrder(s.checkoutLine())
	after := s.paid(o, cardPayment(c, "100"))
	ledger := s.consumeEntries(c)[0].(map[string]any)["id"]
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id,card_ledger_id) VALUES ($1,$2,$3,$4,'card',1,$5,$6)`, s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"], c["id"], ledger)
	var pg interface{ SQLState() string }
	if !errors.As(err, &pg) || pg.SQLState() != "23505" {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
}

func TestCardPaymentForeignKeys(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("外键测试卡", nil), "200", "0")
	o := s.customerOrder(s.checkoutLine())
	after := s.paid(o, payment("cash", "100"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ledgerID uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'consume',200,-1,199,0,0,0,current_date,$4) RETURNING id`, s.Res.TenantID, s.Res.StoreID, c["id"], s.Res.OwnerID).Scan(&ledgerID); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	for _, tc := range []struct {
		name             string
		cardID, ledgerID uuid.UUID
	}{
		{"nonexistent card", uuid.New(), ledgerID},
		{"nonexistent card ledger", id(c["id"]), uuid.New()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), `INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id,card_ledger_id) VALUES ($1,$2,$3,$4,'card',1,$5,$6)`, s.Res.TenantID, s.Res.StoreID, after["checkout_id"], o["id"], tc.cardID, tc.ledgerID)
			dbtest.Rollback(t, tx)
			var pg interface{ SQLState() string }
			if !errors.As(err, &pg) || pg.SQLState() != "23503" {
				t.Fatal(err)
			}
		})
	}
}
