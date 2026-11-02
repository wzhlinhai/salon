package db_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
	"strings"
	"testing"
)

func TestPayrollConstraints(t *testing.T) {
	s := optest.OpenShop(t)
	other, err := staff.Bootstrap(t.Context(), s.Pool, staff.BootstrapInput{TenantName: "外店", StoreName: "外店", OwnerName: "外店老板", OwnerNumber: "001", OwnerAccount: "foreign", OwnerPassword: optest.OwnerPassword})
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, foreignStore, foreignStaff := other.TenantID, other.StoreID, other.OwnerID

	salary := `INSERT INTO staff_base_salaries(tenant_id,store_id,staff_id,effective_month,mode,amount,saved_by) VALUES($1,$2,$3,'2099-01-01','base',1,$4)`
	adjustment := `INSERT INTO payroll_adjustments(tenant_id,store_id,staff_id,month,name,amount,note,created_by) VALUES($1,$2,$3,'2099-01-01','项目',1,'备注',$4)`
	for _, table := range []string{"staff_base_salaries", "payroll_adjustments"} {
		var unique bool
		if err := s.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=$1::regclass AND contype='u' AND pg_get_constraintdef(oid)='UNIQUE (tenant_id, id)')`, table).Scan(&unique); err != nil || !unique {
			t.Fatal(table, "missing tenant/id unique", err)
		}
	}
	for _, tc := range []struct{ name, sql string }{
		{"salary month first", strings.Replace(salary, "2099-01-01", "2099-01-02", 1)},
		{"salary mode", strings.Replace(salary, "'base'", "'other'", 1)},
		{"salary nonnegative", strings.Replace(salary, "'base',1", "'base',-1", 1)},
		{"adjustment month first", strings.Replace(adjustment, "2099-01-01", "2099-01-02", 1)},
		{"adjustment name required", strings.Replace(adjustment, "'项目'", "''", 1)},
		{"adjustment name length", strings.Replace(adjustment, "'项目'", "'"+strings.Repeat("名", 21)+"'", 1)},
		{"adjustment nonzero", strings.Replace(adjustment, "'项目',1", "'项目',0", 1)},
		{"adjustment note length", strings.Replace(adjustment, "'备注'", "'"+strings.Repeat("注", 101)+"'", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(t.Context(), tc.sql, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, s.Res.OwnerID)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "23514" {
				t.Fatalf("got %v want CHECK violation", err)
			}
			dbtest.Rollback(t, tx)
		})
	}
	for _, sql := range []string{salary, adjustment} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, sql, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, s.Res.OwnerID)
		dbtest.Commit(t, tx)
		for _, tc := range []struct {
			name, code                     string
			tenant, store, employee, actor uuid.UUID
		}{
			{"store FK", "23503", s.Res.TenantID, foreignStore, s.Res.OwnerID, s.Res.OwnerID},
			{"staff FK", "23503", s.Res.TenantID, s.Res.StoreID, foreignStaff, s.Res.OwnerID},
			{"actor FK", "23503", s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, foreignStaff},
			{"RLS write", "42501", foreignTenant, foreignStore, foreignStaff, foreignStaff},
		} {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(t.Context(), sql, tc.tenant, tc.store, tc.employee, tc.actor)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != tc.code {
				t.Fatalf("%s: got %v want %s", tc.name, err, tc.code)
			}
			dbtest.Rollback(t, tx)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, foreignTenant)
	for _, table := range []string{"staff_base_salaries", "payroll_adjustments"} {
		var n int
		if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	dbtest.Rollback(t, tx)
	for _, sql := range []string{"UPDATE staff_base_salaries SET amount=2", "DELETE FROM staff_base_salaries", "UPDATE payroll_adjustments SET amount=2"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), sql)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Fatalf("%s: got %v want permission denied", sql, err)
		}
		dbtest.Rollback(t, tx)
	}
	// DELETE 权限与租户过滤都实际执行。
	tx = dbtest.AppTx(t, s.Pool, foreignTenant)
	tag, err := tx.Exec(t.Context(), "DELETE FROM payroll_adjustments")
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatal(tag, err)
	}
	dbtest.Rollback(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	tag, err = tx.Exec(t.Context(), "DELETE FROM payroll_adjustments")
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal(tag, err)
	}
	dbtest.Rollback(t, tx)
}

func TestPayrollMigrationDown(t *testing.T) {
	s := optest.OpenShop(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for _, kind := range []string{"base_salary", "payroll_adjustment", "commission_scheme"} {
		dbtest.Exec(t, tx, `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
	}
	dbtest.Commit(t, tx)
	dbtest.MigrateDownTo(t, s.Pool, 33)
	for _, name := range []string{"staff_base_salaries", "payroll_adjustments"} {
		var table *string
		if err := s.Pool.QueryRow(t.Context(), "SELECT to_regclass($1)::text", name).Scan(&table); err != nil || table != nil {
			t.Fatal(table, err)
		}
	}
	var n int
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE target_kind IN ('base_salary','payroll_adjustment')").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE target_kind='commission_scheme'").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, kind := range []string{"base_salary", "payroll_adjustment"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.ConstraintName != "audit_log_target_kind_check" {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
	}
}

func TestPayrollRequiredColumns(t *testing.T) {
	s := optest.OpenShop(t)
	for table, columns := range map[string][]string{
		"staff_base_salaries": {"tenant_id", "store_id", "id", "staff_id", "effective_month", "mode", "amount", "saved_by", "saved_at"},
		"payroll_adjustments": {"tenant_id", "store_id", "id", "staff_id", "month", "name", "amount", "note", "created_by", "created_at"},
	} {
		for _, column := range columns {
			var required bool
			if err := s.Pool.QueryRow(t.Context(), "SELECT attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attname=$2", table, column).Scan(&required); err != nil || !required {
				t.Fatal(table, column, "must be NOT NULL", err)
			}
		}
		var primary bool
		if err := s.Pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=$1::regclass AND contype='p' AND pg_get_constraintdef(oid)='PRIMARY KEY (id)')", table).Scan(&primary); err != nil || !primary {
			t.Fatal(table, "missing primary key", err)
		}
	}
}
