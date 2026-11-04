package db_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func attendanceError(t *testing.T, err error, code string) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != code {
		t.Fatalf("got %v want SQLSTATE %s", err, code)
	}
}

func TestAttendanceConstraints(t *testing.T) {
	s := optest.OpenShop(t)
	other, err := staff.Bootstrap(t.Context(), s.Pool, staff.BootstrapInput{TenantName: "外店", StoreName: "外店", OwnerName: "老板", OwnerNumber: "001", OwnerAccount: "foreign", OwnerPassword: optest.OwnerPassword})
	if err != nil {
		t.Fatal(err)
	}
	shiftSQL := `INSERT INTO shifts(tenant_id,store_id,name,start_time,end_time,created_by) VALUES($1,$2,'班次','09:00','18:00',$3) RETURNING id`
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var shift uuid.UUID
	if err := tx.QueryRow(t.Context(), shiftSQL, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID).Scan(&shift); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	tx = dbtest.AppTx(t, s.Pool, other.TenantID)
	var foreignShift uuid.UUID
	if err := tx.QueryRow(t.Context(), shiftSQL, other.TenantID, other.StoreID, other.OwnerID).Scan(&foreignShift); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	for _, tc := range []struct{ name, sql string }{
		{"name empty", strings.Replace(shiftSQL, "'班次'", "''", 1)},
		{"name length", strings.Replace(shiftSQL, "'班次'", "'"+strings.Repeat("名", 21)+"'", 1)},
		{"equal times", strings.Replace(shiftSQL, "'09:00'", "'18:00'", 1)},
		{"reverse times", strings.Replace(shiftSQL, "'09:00'", "'19:00'", 1)},
		{"start seconds", strings.Replace(shiftSQL, "'09:00'", "'09:00:01'", 1)},
		{"end seconds", strings.Replace(shiftSQL, "'18:00'", "'18:00:00.1'", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(t.Context(), tc.sql, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
			attendanceError(t, err, "23514")
			dbtest.Rollback(t, tx)
		})
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(t.Context(), shiftSQL, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	attendanceError(t, err, "23505")
	dbtest.Rollback(t, tx)
	assignment := `INSERT INTO staff_shifts(tenant_id,store_id,staff_id,shift_id,effective_from,saved_by) VALUES($1,$2,$3,$4,'2026-06-12',$5)`
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, assignment, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, shift, s.Res.OwnerID)
	dbtest.Exec(t, tx, assignment, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, nil, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	for _, tc := range []struct {
		name, sql, code string
		args            []any
	}{
		{"shift store FK", strings.Replace(shiftSQL, "'班次'", "'新班次'", 1), "23503", []any{s.Res.TenantID, other.StoreID, s.Res.OwnerID}},
		{"shift creator FK", strings.Replace(shiftSQL, "'班次'", "'新班次'", 1), "23503", []any{s.Res.TenantID, s.Res.StoreID, other.OwnerID}},
		{"shift write RLS", strings.TrimSuffix(strings.Replace(shiftSQL, "'班次'", "'写入隔离'", 1), " RETURNING id"), "42501", []any{other.TenantID, other.StoreID, other.OwnerID}},
		{"assignment store FK", assignment, "23503", []any{s.Res.TenantID, other.StoreID, s.Res.OwnerID, shift, s.Res.OwnerID}},
		{"assignment staff FK", assignment, "23503", []any{s.Res.TenantID, s.Res.StoreID, other.OwnerID, shift, s.Res.OwnerID}},
		{"assignment actor FK", assignment, "23503", []any{s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, shift, other.OwnerID}},
		{"assignment shift FK", assignment, "23503", []any{s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, foreignShift, s.Res.OwnerID}},
		{"assignment write RLS", assignment, "42501", []any{other.TenantID, other.StoreID, other.OwnerID, foreignShift, other.OwnerID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(t.Context(), tc.sql, tc.args...)
			attendanceError(t, err, tc.code)
			dbtest.Rollback(t, tx)
		})
	}
	tx = dbtest.AppTx(t, s.Pool, other.TenantID)
	for table, want := range map[string]int{"shifts": 1, "staff_shifts": 0} {
		var n int
		if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != want {
			t.Fatal(table, n, err)
		}
	}
	tag, err := tx.Exec(t.Context(), "UPDATE shifts SET active=false WHERE id=$1", shift)
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatal(tag, err)
	}
	dbtest.Rollback(t, tx)
	for _, sql := range []string{
		"UPDATE shifts SET start_time='08:00'", "UPDATE shifts SET end_time='19:00'", "UPDATE shifts SET name='新名'", "DELETE FROM shifts", "UPDATE staff_shifts SET effective_from='2026-06-13'", "DELETE FROM staff_shifts",
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), sql)
		attendanceError(t, err, "42501")
		dbtest.Rollback(t, tx)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	tag, err = tx.Exec(t.Context(), "UPDATE shifts SET active=false WHERE id=$1", shift)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal(tag, err)
	}
	dbtest.Rollback(t, tx)
	for _, table := range []string{"shifts", "staff_shifts"} {
		var unique bool
		if err := s.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=$1::regclass AND contype='u' AND pg_get_constraintdef(oid)='UNIQUE (tenant_id, id)')`, table).Scan(&unique); err != nil || !unique {
			t.Fatal(table, "tenant unique", err)
		}
	}
}

func TestAttendanceRequiredColumns(t *testing.T) {
	s := optest.OpenShop(t)
	for table, columns := range map[string][]string{
		"shifts":       {"tenant_id", "store_id", "id", "name", "start_time", "end_time", "active", "created_by", "created_at"},
		"staff_shifts": {"tenant_id", "store_id", "id", "staff_id", "effective_from", "saved_by", "saved_at"},
	} {
		for _, column := range columns {
			var required bool
			if err := s.Pool.QueryRow(t.Context(), "SELECT attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attname=$2", table, column).Scan(&required); err != nil || !required {
				t.Fatal(table, column, "must be NOT NULL", err)
			}
		}
		var primary bool
		if err := s.Pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=$1::regclass AND contype='p' AND pg_get_constraintdef(oid)='PRIMARY KEY (id)')", table).Scan(&primary); err != nil || !primary {
			t.Fatal(table, "primary key", err)
		}
	}
}

func TestAttendanceMigrationDown(t *testing.T) {
	s := optest.OpenShop(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for _, kind := range []string{"shift", "staff_shift", "payroll_month"} {
		dbtest.Exec(t, tx, `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
	}
	dbtest.Commit(t, tx)
	dbtest.MigrateDownTo(t, s.Pool, 35)
	for _, table := range []string{"shifts", "staff_shifts"} {
		var name *string
		if err := s.Pool.QueryRow(t.Context(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
			t.Fatal(name, err)
		}
	}
	var n int
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE target_kind IN ('shift','staff_shift')").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE target_kind='payroll_month'").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, kind := range []string{"shift", "staff_shift"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
		attendanceError(t, err, "23514")
		dbtest.Rollback(t, tx)
	}
}
