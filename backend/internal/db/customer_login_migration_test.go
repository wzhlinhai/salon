package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestCustomerLoginMigrationDown(t *testing.T) {
	t.Run("refuses with customer login data", func(t *testing.T) {
		s := optest.OpenShop(t)
		customer := s.HTTPWithToken(t, s.Owner, "create_customer", map[string]any{"phone": "13800130040"})
		customerID := uuid.MustParse(customer.Body["customer"].(map[string]any)["id"].(string))
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, `INSERT INTO customer_logins (tenant_id,store_id,openid,customer_id) VALUES ($1,$2,'down-guard',$3)`, s.Res.TenantID, s.Res.StoreID, customerID)
		dbtest.Commit(t, tx)
		err := db.MigrateDown(context.Background(), s.Pool)
		if err == nil || !strings.Contains(err.Error(), "无法回滚 00052") {
			t.Fatalf("down error = %v", err)
		}
		if version, err := db.MigrationVersion(context.Background(), s.Pool); err != nil || version != 52 {
			t.Fatalf("version = %d, err = %v", version, err)
		}
	})

	t.Run("succeeds without data", func(t *testing.T) {
		s := optest.OpenShop(t)
		if err := db.MigrateDown(context.Background(), s.Pool); err != nil {
			t.Fatal(err)
		}
		var table *string
		if err := s.Pool.QueryRow(context.Background(), "SELECT to_regclass('customer_logins')::text").Scan(&table); err != nil || table != nil {
			t.Fatalf("customer_logins = %v, err = %v", table, err)
		}
		if version, err := db.MigrationVersion(context.Background(), s.Pool); err != nil || version != 51 {
			t.Fatalf("version = %d, err = %v", version, err)
		}
	})
}
