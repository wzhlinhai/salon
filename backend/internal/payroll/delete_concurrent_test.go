package payroll_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestDeletePayrollAdjustmentDisappeared(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	adjustment := add(t, s, s.Res.OwnerID, m, "并发删除", "1")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if err := dbgen.New(tx).LockPayroll(t.Context(), s.Res.StoreID); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "delete_payroll_adjustment", map[string]any{"id": adjustment["id"]})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	dbtest.Exec(t, tx, "DELETE FROM payroll_adjustments WHERE store_id=$1 AND id=$2", s.Res.StoreID, adjustment["id"])
	dbtest.Commit(t, tx)
	reject(t, optest.WaitResult(t, done, 404), 404, "加减项不存在")
	rows := entries(t, s.Call(t, s.Owner, "list_audit_log", map[string]any{"target_id": adjustment["id"]}, 200))
	if len(rows) != 1 || rows[0].(map[string]any)["action"] != "add_payroll_adjustment" {
		t.Fatal(rows)
	}
}
