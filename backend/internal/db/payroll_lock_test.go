package db_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func TestPayrollLockConstraints(t *testing.T) {
	s := optest.OpenShop(t)
	other, err := staff.Bootstrap(t.Context(), s.Pool, staff.BootstrapInput{TenantName: "外店", StoreName: "外店", OwnerName: "老板", OwnerNumber: "001", OwnerAccount: "other", OwnerPassword: optest.OwnerPassword})
	if err != nil {
		t.Fatal(err)
	}
	settlement := uuid.New()
	foreignSettlement := uuid.New()
	insertSettlement := `INSERT INTO commission_settlements(tenant_id,store_id,id,month,settled_by) VALUES($1,$2,$3,'2099-01-01',$4)`
	for _, tc := range []struct{ tenant, store, id, actor uuid.UUID }{{s.Res.TenantID, s.Res.StoreID, settlement, s.Res.OwnerID}, {other.TenantID, other.StoreID, foreignSettlement, other.OwnerID}} {
		tx := dbtest.AppTx(t, s.Pool, tc.tenant)
		dbtest.Exec(t, tx, insertSettlement, tc.tenant, tc.store, tc.id, tc.actor)
		dbtest.Commit(t, tx)
	}
	payroll := `INSERT INTO payroll_lines(tenant_id,store_id,settlement_id,staff_id,mode,applied_amount,base_pay,item_commission,consume_commission,labor_commission,sale_commission,commission_total,guarantee_top_up,adjustments_total,total) VALUES($1,$2,$3,$4,'base',10,10,1,2,3,4,10,0,-1,19)`
	guarantee := strings.Replace(payroll, "'base',10,10,1,2,3,4,10,0,-1,19", "'guarantee',20,0,1,2,3,4,10,10,-1,19", 1)
	commissions := `INSERT INTO settled_commissions(tenant_id,store_id,settlement_id,month,id,business_date,source,staff_id,amount,reversal,explanation,unit_created_at) VALUES($1,$2,$3,'2099-01-01',uuidv7(),'2099-01-02','item',$4,-1,true,'{}',now())`
	check := func(sql, code string, tenant, store, settlement, employee uuid.UUID) {
		t.Helper()
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), sql, tenant, store, settlement, employee)
		dbtest.Rollback(t, tx)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != code {
			t.Fatalf("%s: got %v want %s", sql, err, code)
		}
	}
	for _, tc := range []struct{ name, sql string }{
		{"mode", strings.Replace(payroll, "'base'", "'bad'", 1)},
		{"applied nonnegative", strings.ReplaceAll(strings.Replace(payroll, "'base',10,10", "'base',-1,-1", 1), "-1,19", "-1,8")},
		{"commission sum", strings.Replace(payroll, "3,4,10,0,-1,19", "3,4,11,0,-1,20", 1)},
		{"total sum", strings.Replace(payroll, "-1,19", "-1,20", 1)},
		{"base pay", strings.ReplaceAll(strings.Replace(payroll, "'base',10,10", "'base',10,9", 1), "-1,19", "-1,18")},
		{"base topup", strings.Replace(payroll, "10,0,-1,19", "10,1,-1,20", 1)},
		{"guarantee base", strings.ReplaceAll(strings.Replace(guarantee, "'guarantee',20,0", "'guarantee',20,1", 1), "-1,19", "-1,20")},
		{"guarantee topup", strings.Replace(guarantee, "10,10,-1,19", "10,11,-1,20", 1)},
		{"commission month", strings.Replace(commissions, "2099-01-02", "2099-02-02", 1)},
		{"commission source", strings.Replace(commissions, "'item'", "'bad'", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) { check(tc.sql, "23514", s.Res.TenantID, s.Res.StoreID, settlement, s.Res.OwnerID) })
	}
	// 有效保底与底薪记录、负提成记录均可写入。
	for _, sql := range []string{payroll, guarantee, commissions} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, sql, s.Res.TenantID, s.Res.StoreID, settlement, s.Res.OwnerID)
		dbtest.Rollback(t, tx)
	}
	check(strings.Replace(insertSettlement, "2099-01-01", "2099-01-02", 1), "23514", s.Res.TenantID, s.Res.StoreID, uuid.New(), s.Res.OwnerID)
	check(insertSettlement, "23505", s.Res.TenantID, s.Res.StoreID, uuid.New(), s.Res.OwnerID)
	for _, sql := range []string{payroll, commissions} {
		check(sql, "23503", s.Res.TenantID, other.StoreID, settlement, s.Res.OwnerID)
		check(sql, "23503", s.Res.TenantID, s.Res.StoreID, foreignSettlement, s.Res.OwnerID)
		check(sql, "23503", s.Res.TenantID, s.Res.StoreID, settlement, other.OwnerID)
		check(sql, "42501", other.TenantID, other.StoreID, foreignSettlement, other.OwnerID)
	}
	check(strings.Replace(insertSettlement, "2099-01-01", "2099-02-01", 1), "23503", s.Res.TenantID, other.StoreID, uuid.New(), s.Res.OwnerID)
	check(strings.Replace(insertSettlement, "2099-01-01", "2099-02-01", 1), "23503", s.Res.TenantID, s.Res.StoreID, uuid.New(), other.OwnerID)
	check(insertSettlement, "42501", other.TenantID, other.StoreID, uuid.New(), other.OwnerID)
	// 组合外键不仅关联结算 ID，也保证结算月份相同。
	check(strings.ReplaceAll(commissions, "2099-01", "2099-02"), "23503", s.Res.TenantID, s.Res.StoreID, settlement, s.Res.OwnerID)
	for _, sql := range []string{payroll, commissions} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, sql, s.Res.TenantID, s.Res.StoreID, settlement, s.Res.OwnerID)
		dbtest.Commit(t, tx)
	}
	check(payroll, "23505", s.Res.TenantID, s.Res.StoreID, settlement, s.Res.OwnerID)
	// 同笔提成 ID 不能在另一结算重复存储。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(t.Context(), `INSERT INTO settled_commissions SELECT * FROM settled_commissions`)
	dbtest.Rollback(t, tx)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		t.Fatal(err)
	}
	for _, table := range []string{"commission_settlements", "settled_commissions", "payroll_lines"} {
		tx := dbtest.AppTx(t, s.Pool, other.TenantID)
		var n int
		err := tx.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", s.Res.TenantID).Scan(&n)
		dbtest.Rollback(t, tx)
		if err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
		for _, sql := range []string{"UPDATE " + table + " SET tenant_id=tenant_id", "DELETE FROM " + table} {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(t.Context(), sql)
			dbtest.Rollback(t, tx)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "42501" {
				t.Fatal(table, sql, err)
			}
		}
	}
}

func TestPayrollLockMigrationDown(t *testing.T) {
	s := optest.OpenShop(t)
	insert := `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'lock_test',$4,$5)`
	for _, kind := range []string{"base_salary", "payroll_adjustment", "commission_scheme", "payroll_month"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, insert, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, uuid.New())
		dbtest.Commit(t, tx)
	}
	dbtest.MigrateDownTo(t, s.Pool, 34)
	for _, table := range []string{"commission_settlements", "settled_commissions", "payroll_lines"} {
		var name *string
		if err := s.Pool.QueryRow(t.Context(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
			t.Fatal(table, name, err)
		}
	}
	var n int
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE action='lock_test'").Scan(&n); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(t.Context(), insert, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, "payroll_month", uuid.New())
	dbtest.Rollback(t, tx)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.ConstraintName != "audit_log_target_kind_check" {
		t.Fatal(fmt.Sprint(err))
	}
}

func assertPayrollSettlementStore(t *testing.T, insert string) {
	t.Helper()
	s := optest.OpenShop(t)
	otherStore, ownSettlement, otherSettlement := uuid.New(), uuid.New(), uuid.New()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO stores(id,tenant_id,name) VALUES($1,$2,'同租户分店')", otherStore, s.Res.TenantID)
	for _, row := range []struct{ store, settlement uuid.UUID }{{s.Res.StoreID, ownSettlement}, {otherStore, otherSettlement}} {
		dbtest.Exec(t, tx, `INSERT INTO commission_settlements(tenant_id,store_id,id,month,settled_by) VALUES($1,$2,$3,'2099-01-01',$4)`, s.Res.TenantID, row.store, row.settlement, s.Res.OwnerID)
	}
	dbtest.Commit(t, tx)
	for _, settlement := range []uuid.UUID{ownSettlement, otherSettlement} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(t.Context(), insert, s.Res.TenantID, s.Res.StoreID, settlement, s.Res.OwnerID)
		dbtest.Rollback(t, tx)
		if settlement == ownSettlement {
			if err != nil {
				t.Fatal("same store settlement rejected", err)
			}
			continue
		}
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23503" {
			t.Fatalf("another store's settlement: got %v, want FK violation", err)
		}
	}
}

func TestSettledCommissionsSettlementStore(t *testing.T) {
	assertPayrollSettlementStore(t, `INSERT INTO settled_commissions(tenant_id,store_id,settlement_id,month,id,business_date,source,staff_id,amount,reversal,explanation,unit_created_at) VALUES($1,$2,$3,'2099-01-01',uuidv7(),'2099-01-02','item',$4,1,false,'{}',now())`)
}

func TestPayrollLinesSettlementStore(t *testing.T) {
	assertPayrollSettlementStore(t, `INSERT INTO payroll_lines(tenant_id,store_id,settlement_id,staff_id,mode,applied_amount,base_pay,item_commission,consume_commission,labor_commission,sale_commission,commission_total,guarantee_top_up,adjustments_total,total) VALUES($1,$2,$3,$4,'base',0,0,0,0,0,0,0,0,0,0)`)
}
