package orders_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestVoidTenantIsolation(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	s.void(s.paid(s.customerOrder(line), payment("cash", "100")))
	otherOrder := s.paid(s.customerOrder(line), payment("cash", "100"))
	otherTenant, _ := dbtest.NewTenant(t, s.Pool, "撤单隔离租户")
	tx := dbtest.AppTx(t, s.Pool, otherTenant)
	var count int
	err := tx.QueryRow(context.Background(), "SELECT count(*) FROM order_voids").Scan(&count)
	dbtest.Rollback(t, tx)
	if err != nil || count != 0 {
		t.Fatal("another tenant can read order voids", count, err)
	}
	tx = dbtest.AppTx(t, s.Pool, otherTenant)
	_, err = tx.Exec(context.Background(), `INSERT INTO order_voids(tenant_id,store_id,order_id,reason,business_date,voided_by) VALUES($1,$2,$3,'跨租户写入','2020-01-01',$4)`, s.Res.TenantID, s.Res.StoreID, otherOrder["id"], s.Res.OwnerID)
	dbtest.Rollback(t, tx)
	voidState(t, err, "42501")
}
