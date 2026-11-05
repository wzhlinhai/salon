package groupbuy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestCouponMigrationDown(t *testing.T) {
	s := optest.OpenShop(t)
	kinds := []string{
		"customer", "staff", "role", "customer_source", "item", "card_type", "card",
		"card_upgrade_rule", "order", "red_packet", "debt", "commission_scheme",
		"base_salary", "payroll_adjustment", "payroll_month", "shift", "staff_shift",
		"punch", "attendance_approval", "collection", "coupon_redemption",
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	customer := uuid.New()
	dbtest.Exec(t, tx, "INSERT INTO customers (id,tenant_id,store_id,created_by) VALUES ($1,$2,$3,$4)", customer, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	for _, kind := range kinds {
		target := uuid.New()
		var customerID *uuid.UUID
		switch kind {
		case "customer":
			target = customer
			customerID = &customer
		case "card", "order", "red_packet", "debt":
			customerID = &customer
		}
		dbtest.Exec(t, tx, `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id,customer_id) VALUES($1,$2,$3,'test',$4,$5,$6)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, kind, target, customerID)
	}
	dbtest.Commit(t, tx)

	dbtest.MigrateDownTo(t, s.Pool, 39)

	for _, kind := range kinds {
		want := 1
		if kind == "coupon_redemption" {
			want = 0
		}
		var n int
		if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind=$1", kind).Scan(&n); err != nil || n != want {
			t.Fatal(kind, n, err)
		}
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test','coupon_redemption',$4)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, uuid.New())
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "audit_log_target_kind_check" {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
}
