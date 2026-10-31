package db_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"testing"
)

func TestCommissionConstraints(t *testing.T) {
	s := optest.OpenShop(t)
	var tenantUnique bool
	if err := s.Pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='commission_scheme_versions'::regclass AND contype='u' AND pg_get_constraintdef(oid)='UNIQUE (tenant_id, id)')`).Scan(&tenantUnique); err != nil || !tenantUnique {
		t.Fatal("missing tenant/id unique", err)
	}
	foreignTenant, foreignStore := dbtest.NewTenant(t, s.Pool, "外租户")
	tx := dbtest.AppTx(t, s.Pool, foreignTenant)
	var foreignStaff uuid.UUID
	if err := tx.QueryRow(t.Context(), "INSERT INTO staff(tenant_id,store_id,name,number) VALUES($1,$2,'外人','001') RETURNING id", foreignTenant, foreignStore).Scan(&foreignStaff); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	insert := `INSERT INTO commission_scheme_versions(tenant_id,store_id,version,effective_from,scheme,saved_by) VALUES($1,$2,$3,'2099-01-01','{}',$4)`
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, insert, s.Res.TenantID, s.Res.StoreID, 1, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	for _, tc := range []struct {
		name, code           string
		tenant, store, actor uuid.UUID
		version              int
	}{
		{"positive version", "23514", s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, 0},
		{"unique version", "23505", s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, 1},
		{"store tenant FK", "23503", s.Res.TenantID, foreignStore, s.Res.OwnerID, 2},
		{"staff tenant FK", "23503", s.Res.TenantID, s.Res.StoreID, foreignStaff, 2},
		{"tenant RLS", "42501", foreignTenant, foreignStore, foreignStaff, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(t.Context(), insert, tc.tenant, tc.store, tc.version, tc.actor)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != tc.code {
				t.Fatalf("got %v want %s", err, tc.code)
			}
			dbtest.Rollback(t, tx)
		})
	}
	tx = dbtest.AppTx(t, s.Pool, foreignTenant)
	var n int
	if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM commission_scheme_versions").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	dbtest.Rollback(t, tx)
	for _, sql := range []string{"UPDATE commission_scheme_versions SET version=2", "DELETE FROM commission_scheme_versions"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), sql)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Fatalf("%s: %v", sql, err)
		}
		dbtest.Rollback(t, tx)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'save_commission_scheme','commission_scheme',$4)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, uuid.New())
	dbtest.Commit(t, tx)
	dbtest.MigrateDownTo(t, s.Pool, 31)
	var table *string
	if err := s.Pool.QueryRow(context.Background(), "SELECT to_regclass('commission_scheme_versions')::text").Scan(&table); err != nil || table != nil {
		t.Fatal(table, err)
	}
	var count int
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE target_kind='commission_scheme'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(t.Context(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test','commission_scheme',$4)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, uuid.New())
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.ConstraintName != "audit_log_target_kind_check" {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
}
