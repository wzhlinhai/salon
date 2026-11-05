package collections_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestCollectionMigrationDown(t *testing.T) {
	s := optest.OpenShop(t)
	initiate(t, s, "cash", order(t, s))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for _, kind := range []string{"shift", "staff_shift", "punch", "attendance_approval", "payroll_month"} {
		dbtest.Exec(t, tx, `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
	}
	dbtest.Commit(t, tx)
	dbtest.MigrateDownTo(t, s.Pool, 35)
	for _, table := range []string{"collections", "collection_events"} {
		var name *string
		if err := s.Pool.QueryRow(context.Background(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
			t.Fatal(table, name, err)
		}
	}
	var n int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind IN ('collection','shift','staff_shift','punch','attendance_approval')").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind='payroll_month'").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, kind := range []string{"collection", "shift", "staff_shift", "punch", "attendance_approval"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "audit_log_target_kind_check" {
			t.Fatal(kind, err)
		}
		dbtest.Rollback(t, tx)
	}
}
