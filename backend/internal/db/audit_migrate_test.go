package db_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func TestAuditTargetBackfill(t *testing.T) {
	for _, unmatched := range []bool{false, true} {
		name := "existing kinds"
		if unmatched {
			name = "unmatched fails"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			pool := dbtest.NewEmptyPool(t)
			sqlDB := stdlib.OpenDBFromPool(pool)
			defer func() { _ = sqlDB.Close() }()
			provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("migrations"), goose.WithDisableGlobalRegistry(true))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.UpTo(ctx, 8); err != nil {
				t.Fatal(err)
			}
			// 历史迁移测试须在 00008 建店，OpenShop 会先迁移到最新版本。
			shop, err := staff.Bootstrap(ctx, pool, staff.BootstrapInput{TenantName: "历史租户", StoreName: "历史门店", OwnerName: "老板", OwnerNumber: "001", OwnerAccount: "boss", OwnerPassword: "owner-password"})
			if err != nil {
				t.Fatal(err)
			}
			customer, source, item, deletedRole := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			for _, fixture := range []struct {
				query string
				args  []any
			}{
				{"INSERT INTO customers (id,tenant_id,store_id,created_by) VALUES ($1,$2,$3,$4)", []any{customer, shop.TenantID, shop.StoreID, shop.OwnerID}},
				{"INSERT INTO customer_sources (id,tenant_id,store_id,name) VALUES ($1,$2,$3,'历史来源')", []any{source, shop.TenantID, shop.StoreID}},
				{"INSERT INTO items (id,tenant_id,store_id,name,price) VALUES ($1,$2,$3,'历史项目',10)", []any{item, shop.TenantID, shop.StoreID}},
				{"INSERT INTO roles (id,tenant_id,store_id,name) VALUES ($1,$2,$3,'已删除角色')", []any{deletedRole, shop.TenantID, shop.StoreID}},
				{"DELETE FROM roles WHERE id=$1", []any{deletedRole}},
			} {
				if _, err := pool.Exec(ctx, fixture.query, fixture.args...); err != nil {
					t.Fatal(err)
				}
			}
			fixtures := []struct {
				kind, action string
				target       uuid.UUID
			}{
				{"customer", "create_customer", customer}, {"staff", "create_staff", shop.OwnerID},
				{"customer_source", "create_customer_source", source}, {"item", "create_item", item},
				{"role", "create_role", deletedRole}, {"role", "update_role", deletedRole}, {"role", "delete_role", deletedRole},
			}
			if unmatched {
				fixtures = append(fixtures, struct {
					kind, action string
					target       uuid.UUID
				}{"", "unknown", uuid.New()})
			}
			for _, f := range fixtures {
				if _, err := pool.Exec(ctx, "INSERT INTO audit_log (tenant_id,store_id,action,target_id,actor_id) VALUES ($1,$2,$3,$4,$5)", shop.TenantID, shop.StoreID, f.action, f.target, shop.OwnerID); err != nil {
					t.Fatal(err)
				}
			}
			err = db.MigrateUp(ctx, pool)
			if unmatched {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23502" || pgErr.ColumnName != "target_kind" {
					t.Fatalf("migration = %v, want target_kind NOT NULL failure", err)
				}
				if got := version(t, pool); got != 8 {
					t.Fatalf("failed migration version = %d, want 8", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_log").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != len(fixtures) {
				t.Fatalf("rows = %d, want %d", count, len(fixtures))
			}
			for _, f := range fixtures {
				var kind string
				var customerID *uuid.UUID
				if err := pool.QueryRow(ctx, "SELECT target_kind,customer_id FROM audit_log WHERE action=$1", f.action).Scan(&kind, &customerID); err != nil {
					t.Fatal(err)
				}
				if kind != f.kind {
					t.Errorf("%s kind = %s, want %s", f.action, kind, f.kind)
				}
				if f.kind == "customer" {
					if customerID == nil || *customerID != customer {
						t.Fatalf("customer_id = %v, want %s", customerID, customer)
					}
				} else if customerID != nil {
					t.Fatalf("%s customer_id = %v, want NULL", f.kind, customerID)
				}
			}
		})
	}
}

func TestAuditPacketDebtMigration(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewEmptyPool(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("migrations"), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	shop, err := staff.Bootstrap(ctx, pool, staff.BootstrapInput{TenantName: "历史租户", StoreName: "门店", OwnerName: "老板", OwnerNumber: "001", OwnerAccount: "boss", OwnerPassword: "owner-password"})
	if err != nil {
		t.Fatal(err)
	}
	customer := uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO customers (id,tenant_id,store_id,created_by) VALUES ($1,$2,$3,$4)", customer, shop.TenantID, shop.StoreID, shop.OwnerID); err != nil {
		t.Fatal(err)
	}
	actions := []string{"issue_red_packet", "revoke_red_packet", "register_historical_debt", "repay_debt", "void_order"}
	objects := make(map[string]uuid.UUID)
	for i, action := range actions {
		object := uuid.New()
		objects[action] = object
		before, after := any(nil), any(`{"id":"`+object.String()+`"}`)
		if i%2 == 1 {
			before, after = after, nil
		}
		if _, err := pool.Exec(ctx, `INSERT INTO audit_log (tenant_id,store_id,actor_id,action,target_kind,target_id,customer_id,before,after) VALUES ($1,$2,$3,$4,'customer',$5,$5,$6,$7)`, shop.TenantID, shop.StoreID, shop.OwnerID, action, customer, before, after); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(down bool) {
		t.Helper()
		for _, action := range actions {
			kind, target := "red_packet", objects[action]
			if action == "register_historical_debt" || action == "repay_debt" {
				kind = "debt"
			}
			if down {
				kind, target = "customer", customer
			}
			var gotKind string
			var gotTarget, gotCustomer uuid.UUID
			if err := pool.QueryRow(ctx, "SELECT target_kind,target_id,customer_id FROM audit_log WHERE action=$1", action).Scan(&gotKind, &gotTarget, &gotCustomer); err != nil {
				t.Fatal(err)
			}
			if gotKind != kind || gotTarget != target || gotCustomer != customer {
				t.Fatal(action, gotKind, gotTarget, gotCustomer)
			}
		}
	}
	check(false)
	for _, tc := range []struct{ kind, constraint string }{
		{"unknown", "audit_log_target_kind_check"},
		{"red_packet", "audit_log_customer_presence_check"},
		{"debt", "audit_log_customer_presence_check"},
	} {
		_, err := pool.Exec(ctx, `INSERT INTO audit_log (tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES ($1,$2,$3,'test',$4,$5)`, shop.TenantID, shop.StoreID, shop.OwnerID, tc.kind, uuid.New())
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != tc.constraint {
			t.Fatal(tc.kind, err)
		}
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	check(true)
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	check(false)
}
