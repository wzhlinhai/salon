package db_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestPayrollAutoLockConstraints(t *testing.T) {
	s := optest.OpenShop(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO commission_settlements(tenant_id,store_id,id,month,settled_by) VALUES($1,$2,$3,'2099-01-01',NULL)`, s.Res.TenantID, s.Res.StoreID, uuid.New())
	dbtest.Commit(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO audit_log(tenant_id,store_id,action,target_kind,target_id) VALUES($1,$2,'auto_lock','payroll_month',$3)`, s.Res.TenantID, s.Res.StoreID, uuid.New())
	dbtest.Commit(t, tx)
	customer := uuid.MustParse(s.Call(t, s.Owner, "create_customer", map[string]any{}, 200).Body["customer"].(map[string]any)["id"].(string))
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(t.Context(), `INSERT INTO audit_log(tenant_id,store_id,action,target_kind,target_id,actor_id,actor_customer_id) VALUES($1,$2,'both','payroll_month',$3,$4,$5)`, s.Res.TenantID, s.Res.StoreID, uuid.New(), s.Res.OwnerID, customer)
	dbtest.Rollback(t, tx)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "audit_log_actor_check" {
		t.Fatalf("both actors = %v", err)
	}
}

func TestPayrollAutoLockMigrationDown(t *testing.T) {
	s := optest.OpenShop(t)
	dbtest.MigrateDownTo(t, s.Pool, 61)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(t.Context(), `INSERT INTO commission_settlements(tenant_id,store_id,id,month,settled_by) VALUES($1,$2,$3,'2099-01-01',NULL)`, s.Res.TenantID, s.Res.StoreID, uuid.New())
	dbtest.Rollback(t, tx)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23502" || pe.ColumnName != "settled_by" {
		t.Fatalf("null settled_by = %v", err)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(t.Context(), `INSERT INTO audit_log(tenant_id,store_id,action,target_kind,target_id) VALUES($1,$2,'auto_lock','payroll_month',$3)`, s.Res.TenantID, s.Res.StoreID, uuid.New())
	dbtest.Rollback(t, tx)
	if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "audit_log_actor_check" {
		t.Fatalf("system audit after down = %v", err)
	}
	if err := db.MigrateUp(t.Context(), s.Pool); err != nil {
		t.Fatal(err)
	}
}
