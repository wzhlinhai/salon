package debts_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestDebtDatabaseProtections(t *testing.T) {
	s := open(t)
	d := s.historical("10")
	s.call("repay_debt", repay(d, "1"), 200)
	for _, tc := range []struct{ sql, state string }{
		{"UPDATE debts SET amount=amount", "42501"},
		{"DELETE FROM debts", "42501"},
		{"UPDATE debt_repayments SET amount=amount", "42501"},
		{"DELETE FROM debt_repayments", "42501"},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), tc.sql)
		dbtest.Rollback(t, tx)
		assertState(t, err, tc.state)
	}
	debtSQL := `INSERT INTO debts(tenant_id,store_id,customer_id,amount,business_date,recorded_by,order_id,order_payment_id) VALUES($1,$2,$3,$4,'2020-01-01',$5,$6,$7)`
	for _, tc := range []struct {
		amount         string
		order, payment any
	}{
		{"0", nil, nil}, {"-1", nil, nil}, {"1", uuid.New(), nil}, {"1", nil, uuid.New()},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), debtSQL, s.Res.TenantID, s.Res.StoreID, s.customer, tc.amount, s.Res.OwnerID, tc.order, tc.payment)
		dbtest.Rollback(t, tx)
		assertState(t, err, "23514")
	}
	for _, tc := range []struct{ amount, method string }{{"0", "cash"}, {"-1", "cash"}, {"1", "card"}, {"1", "debt"}, {"1", "count"}} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO debt_repayments(tenant_id,store_id,debt_id,amount,method,business_date,repaid_by) VALUES($1,$2,$3,$4,$5,'2020-01-01',$6)`, s.Res.TenantID, s.Res.StoreID, d["id"], tc.amount, tc.method, s.Res.OwnerID)
		dbtest.Rollback(t, tx)
		assertState(t, err, "23514")
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE debts SET customer_id=customer_id")
	dbtest.Rollback(t, tx)
}

func assertState(t *testing.T, err error, state string) {
	t.Helper()
	var pg interface{ SQLState() string }
	if !errors.As(err, &pg) || pg.SQLState() != state {
		t.Fatal(state, err)
	}
}

func TestDebtForeignKeys(t *testing.T) {
	s := open(t)
	d := s.historical("10")
	// 不存在及同租户以外的引用都由组合外键拒绝。
	otherTenant, otherStore := dbtest.NewTenant(t, s.Pool, "外租户")
	for _, tc := range []struct {
		table, column string
		value         any
	}{
		{"debts", "store_id", uuid.New()}, {"debts", "customer_id", uuid.New()}, {"debts", "recorded_by", uuid.New()},
		{"debts", "store_id", otherStore}, {"debts", "customer_id", s.Res.OwnerID},
		{"debt_repayments", "store_id", uuid.New()}, {"debt_repayments", "debt_id", uuid.New()}, {"debt_repayments", "repaid_by", uuid.New()},
		{"debt_repayments", "store_id", otherStore},
	} {
		values := map[string]any{"tenant_id": s.Res.TenantID, "store_id": s.Res.StoreID, "customer_id": s.customer, "recorded_by": s.Res.OwnerID, "debt_id": id(d["id"]), "repaid_by": s.Res.OwnerID}
		values[tc.column] = tc.value
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var err error
		if tc.table == "debts" {
			_, err = tx.Exec(context.Background(), `INSERT INTO debts(tenant_id,store_id,customer_id,amount,business_date,recorded_by) VALUES($1,$2,$3,1,'2020-01-01',$4)`, values["tenant_id"], values["store_id"], values["customer_id"], values["recorded_by"])
		} else {
			_, err = tx.Exec(context.Background(), `INSERT INTO debt_repayments(tenant_id,store_id,debt_id,amount,method,business_date,repaid_by) VALUES($1,$2,$3,1,'cash','2020-01-01',$4)`, values["tenant_id"], values["store_id"], values["debt_id"], values["repaid_by"])
		}
		dbtest.Rollback(t, tx)
		assertState(t, err, "23503")
	}
	tx := dbtest.AppTx(t, s.Pool, otherTenant)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM debts").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	dbtest.Rollback(t, tx)
}

func TestDebtMigrationDownAndBackfill(t *testing.T) {
	s := open(t)
	d := s.historical("10")
	s.call("repay_debt", repay(d, "1"), 200)
	dbtest.MigrateDownTo(t, s.Pool, 20)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var absent bool
	if err := tx.QueryRow(context.Background(), "SELECT to_regclass('debts') IS NULL AND to_regclass('debt_repayments') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatal(absent, err)
	}
	// 模拟步骤 1–4 开发库留下的挂账（未识别顾客也可存在）。
	var order, payment uuid.UUID
	dbtest.Exec(t, tx, `INSERT INTO settings_snapshots(tenant_id,store_id,rules,digest) VALUES($1,$2,'{}','test')`, s.Res.TenantID, s.Res.StoreID)
	if err := tx.QueryRow(context.Background(), `INSERT INTO orders(tenant_id,store_id,customer_id,gender,designated,business_date,settings_snapshot_id,opened_by) SELECT $1,$2,$3,'female',false,'2020-01-01',id,$4 FROM settings_snapshots LIMIT 1 RETURNING id`, s.Res.TenantID, s.Res.StoreID, s.customer, s.Res.OwnerID).Scan(&order); err != nil {
		t.Fatal(err)
	}
	var checkout uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO checkouts(tenant_id,store_id,business_date,checked_out_by) VALUES($1,$2,'2020-02-03',$3) RETURNING id`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID).Scan(&checkout); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount) VALUES($1,$2,$3,$4,'debt',12) RETURNING id`, s.Res.TenantID, s.Res.StoreID, checkout, order).Scan(&payment); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	if err := tx.QueryRow(context.Background(), `SELECT count(*)=1 AND bool_and(customer_id=$1 AND order_id=$2 AND order_payment_id=$3 AND amount=12 AND business_date='2020-02-03' AND recorded_by=$4) FROM debts`, s.customer, order, payment, s.Res.OwnerID).Scan(&valid); err != nil || !valid {
		t.Fatal(valid, err)
	}
	dbtest.Rollback(t, tx)
	for _, tc := range []struct {
		order, payment uuid.UUID
		state          string
	}{{order, payment, "23505"}, {uuid.New(), uuid.New(), "23503"}} {
		tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO debts(tenant_id,store_id,customer_id,amount,business_date,recorded_by,order_id,order_payment_id) VALUES($1,$2,$3,1,'2020-01-01',$4,$5,$6)`, s.Res.TenantID, s.Res.StoreID, s.customer, s.Res.OwnerID, tc.order, tc.payment)
		dbtest.Rollback(t, tx)
		assertState(t, err, tc.state)
	}
	// 单独验证 order 外键：新支付尚无欠款，避免唯一约束或 payment 外键先报错。
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var unusedPayment uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount) VALUES($1,$2,$3,$4,'debt',1) RETURNING id`, s.Res.TenantID, s.Res.StoreID, checkout, order).Scan(&unusedPayment); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO debts(tenant_id,store_id,customer_id,amount,business_date,recorded_by,order_id,order_payment_id) VALUES($1,$2,$3,1,'2020-01-01',$4,$5,$6)`, s.Res.TenantID, s.Res.StoreID, s.customer, s.Res.OwnerID, uuid.New(), unusedPayment)
	dbtest.Rollback(t, tx)
	assertState(t, err, "23503")
	// 单独验证 order_payment 的外键，避免 order 外键先失败掩盖它。
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(context.Background(), `INSERT INTO debts(tenant_id,store_id,customer_id,amount,business_date,recorded_by,order_id,order_payment_id) VALUES($1,$2,$3,1,'2020-01-01',$4,$5,$6)`, s.Res.TenantID, s.Res.StoreID, s.customer, s.Res.OwnerID, order, uuid.New())
	dbtest.Rollback(t, tx)
	assertState(t, err, "23503")
}
