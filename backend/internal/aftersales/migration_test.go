package aftersales_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func assertAuditKind(t *testing.T, f *fixture, kind string, rejected bool) {
	t.Helper()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, f.Res.TenantID, f.Res.StoreID, f.Res.OwnerID, kind, uuid.New())
	var pe *pgconn.PgError
	if rejected {
		if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "audit_log_target_kind_check" {
			t.Fatal(kind, err)
		}
	} else if err != nil {
		t.Fatal(kind, err)
	}
	dbtest.Rollback(t, tx)
}

func TestAftersaleMigrationDown(t *testing.T) {
	t.Run("refuses with aftersale data", func(t *testing.T) {
		f := open(t)
		f.now("2026-10-01", 12)
		f.commissionScheme()
		ct := f.cardType("退卡本金")
		card := f.card(ct, "100.00", "0.00", payment("cash", "100"))
		order := f.order(1)
		f.checkout(order, cardPayment("card", "100", id(card["id"]), nil))
		f.now("2026-10-02", 12)
		refund := f.create(id(order["id"]), "partial_refund", "退本金", map[string]any{"method": "card_principal", "amount": "40", "card_id": card["id"]})
		f.approve(id(refund["id"]), map[string]any{"deduct_performance": true, "deduct_commission": true})

		// 先回滚本分支新增的 00051、00050，再确认 00049 在已有售后数据时拒绝回滚。
		if err := db.MigrateDown(context.Background(), f.Pool); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateDown(context.Background(), f.Pool); err != nil {
			t.Fatal(err)
		}
		err := db.MigrateDown(context.Background(), f.Pool)
		if err == nil || !strings.Contains(err.Error(), "无法回滚 00049") {
			t.Fatal(err)
		}
		if version, err := db.MigrationVersion(context.Background(), f.Pool); err != nil || version != 49 {
			t.Fatal(version, err)
		}
		var tables, ledgers, performances int
		if err := f.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM aftersales),(SELECT count(*) FROM card_ledger WHERE kind='aftersale_refund'),(SELECT count(*) FROM performance_entries WHERE aftersale_id IS NOT NULL)`).Scan(&tables, &ledgers, &performances); err != nil || tables != 1 || ledgers != 1 || performances == 0 {
			t.Fatal(tables, ledgers, performances, err)
		}
	})

	t.Run("empty database", func(t *testing.T) {
		f := open(t)
		ct := f.cardType("退卡本金")
		f.card(ct, "1.00", "0.00", payment("cash", "1"))
		dbtest.MigrateDownTo(t, f.Pool, 48)
		for _, table := range []string{"aftersales", "aftersale_events", "aftersale_refunds"} {
			var name *string
			if err := f.Pool.QueryRow(context.Background(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
				t.Fatal(table, name, err)
			}
		}
		var n int
		if err := f.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind='aftersale'").Scan(&n); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		if err := f.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name='performance_entries' AND column_name='aftersale_id'").Scan(&n); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		assertAuditKind(t, f, "aftersale", true)
		assertAuditKind(t, f, "closing", false)

		tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
		var cardID uuid.UUID
		if err := tx.QueryRow(context.Background(), "SELECT id FROM cards LIMIT 1").Scan(&cardID); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(context.Background(), `INSERT INTO card_ledger(tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES($1,$2,$3,'aftersale_refund',0,1,1,0,0,0,current_date,$4)`, f.Res.TenantID, f.Res.StoreID, cardID, f.Res.OwnerID)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23514" {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
	})
}
