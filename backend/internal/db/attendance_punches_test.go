package db_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

const punchInsert = `INSERT INTO punches(tenant_id,store_id,staff_id,at,business_date,source,reason,created_by,voided_by,voided_at,void_reason) VALUES($1,$2,$3,'2026-06-12 09:00+08','2026-06-12','manual','漏打',$4,NULL,NULL,NULL)`
const approvalInsert = `INSERT INTO attendance_approvals(tenant_id,store_id,staff_id,business_date,reason,approved_by,revoked_by,revoked_at) VALUES($1,$2,$3,'2026-06-12','出差',$4,NULL,NULL)`

func TestPunchConstraints(t *testing.T) {
	s := optest.OpenShop(t)
	other, err := staff.Bootstrap(t.Context(), s.Pool, staff.BootstrapInput{TenantName: "外店", StoreName: "外店", OwnerName: "老板", OwnerNumber: "001", OwnerAccount: "foreign", OwnerPassword: optest.OwnerPassword})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, sql string }{
		{"source", strings.Replace(punchInsert, "'manual'", "'unknown'", 1)},
		{"self reason", strings.Replace(punchInsert, "'manual'", "'self'", 1)},
		{"self creator", strings.Replace(strings.Replace(strings.Replace(punchInsert, "'manual'", "'self'", 1), "'漏打'", "''", 1), "$4,NULL", "(CASE WHEN $4::uuid IS NOT NULL THEN '"+other.OwnerID.String()+"'::uuid END),NULL", 1)},
		{"manual empty", strings.Replace(punchInsert, "'漏打'", "''", 1)},
		{"manual long", strings.Replace(punchInsert, "'漏打'", "'"+strings.Repeat("理", 101)+"'", 1)},
		{"void partial actor", strings.Replace(punchInsert, "NULL,NULL,NULL", "$4,NULL,NULL", 1)},
		{"void partial at", strings.Replace(punchInsert, "NULL,NULL,NULL", "NULL,'2026-06-13',NULL", 1)},
		{"void partial reason", strings.Replace(punchInsert, "NULL,NULL,NULL", "NULL,NULL,'错'", 1)},
		{"void self", strings.Replace(strings.Replace(strings.Replace(punchInsert, "'manual'", "'self'", 1), "'漏打'", "''", 1), "NULL,NULL,NULL", "$4,'2026-06-13','错'", 1)},
		{"void empty", strings.Replace(punchInsert, "NULL,NULL,NULL", "$4,'2026-06-13',''", 1)},
		{"void long", strings.Replace(punchInsert, "NULL,NULL,NULL", "$4,'2026-06-13','"+strings.Repeat("理", 101)+"'", 1)},
		{"approval empty", strings.Replace(approvalInsert, "'出差'", "''", 1)},
		{"approval long", strings.Replace(approvalInsert, "'出差'", "'"+strings.Repeat("理", 101)+"'", 1)},
		{"revoke actor", strings.Replace(approvalInsert, "NULL,NULL", "$4,NULL", 1)},
		{"revoke at", strings.Replace(approvalInsert, "NULL,NULL", "NULL,'2026-06-13'", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(t.Context(), tc.sql, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, s.Res.OwnerID)
			attendanceError(t, err, "23514")
			dbtest.Rollback(t, tx)
		})
	}
	for _, sql := range []string{punchInsert, approvalInsert} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, sql, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, s.Res.OwnerID)
		dbtest.Commit(t, tx)
		for i := 1; i <= 3; i++ {
			args := []any{s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, s.Res.OwnerID}
			args[i] = []uuid.UUID{other.TenantID, other.StoreID, other.OwnerID, other.OwnerID}[i]
			tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(t.Context(), strings.Replace(sql, "'2026-06-12'", "'2026-06-11'", 1), args...)
			attendanceError(t, err, "23503")
			dbtest.Rollback(t, tx)
		}
		tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), sql, other.TenantID, other.StoreID, other.OwnerID, other.OwnerID)
		attendanceError(t, err, "42501")
		dbtest.Rollback(t, tx)
	}
	for _, tc := range []struct {
		sql  string
		args []any
	}{
		{strings.Replace(punchInsert, "NULL,NULL,NULL", "$4,'2026-06-13','错'", 1), []any{s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, other.OwnerID}},
		{strings.Replace(approvalInsert, "NULL,NULL", "$4,'2026-06-13'", 1), []any{s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, other.OwnerID}},
	} {
		// 创建人与作废人分开，让外租户引用只落在可空外键。
		sql := strings.Replace(tc.sql, "'漏打',$4", "'漏打','"+s.Res.OwnerID.String()+"'", 1)
		sql = strings.Replace(sql, "'出差',$4", "'出差','"+s.Res.OwnerID.String()+"'", 1)
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), sql, tc.args...)
		attendanceError(t, err, "23503")
		dbtest.Rollback(t, tx)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(t.Context(), approvalInsert, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, s.Res.OwnerID)
	attendanceError(t, err, "23505")
	dbtest.Rollback(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE attendance_approvals SET revoked_by=$1,revoked_at='2026-06-13'", s.Res.OwnerID)
	dbtest.Exec(t, tx, approvalInsert, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, s.Res.OwnerID)
	dbtest.Exec(t, tx, "UPDATE punches SET voided_by=$1,voided_at='2026-06-13',void_reason='错'", s.Res.OwnerID)
	dbtest.Rollback(t, tx)
	for _, sql := range []string{"DELETE FROM punches", "DELETE FROM attendance_approvals", "UPDATE punches SET at='2026-06-13'", "UPDATE punches SET source='self'", "UPDATE punches SET reason='改'", "UPDATE attendance_approvals SET reason='改'"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), sql)
		attendanceError(t, err, "42501")
		dbtest.Rollback(t, tx)
	}
	tx = dbtest.AppTx(t, s.Pool, other.TenantID)
	for _, table := range []string{"punches", "attendance_approvals"} {
		var n int
		if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	dbtest.Rollback(t, tx)
}
func TestPunchMigrationDown(t *testing.T) {
	s := optest.OpenShop(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for _, kind := range []string{"punch", "attendance_approval", "shift"} {
		dbtest.Exec(t, tx, `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
	}
	dbtest.Commit(t, tx)
	dbtest.MigrateDownTo(t, s.Pool, 36)
	for _, table := range []string{"punches", "attendance_approvals"} {
		var name *string
		if err := s.Pool.QueryRow(t.Context(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
			t.Fatal(name, err)
		}
	}
	var n int
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE target_kind IN ('punch','attendance_approval')").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE target_kind='shift'").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, kind := range []string{"punch", "attendance_approval"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
		attendanceError(t, err, "23514")
		dbtest.Rollback(t, tx)
	}
}
