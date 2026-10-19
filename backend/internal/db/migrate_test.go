package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	err := pool.QueryRow(context.Background(),
		"SELECT to_regclass('public.' || $1) IS NOT NULL", name).Scan(&ok)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func version(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	v, err := db.MigrationVersion(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrationsUpDownUp(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewEmptyPool(t)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if v := version(t, pool); v != 2 {
		t.Fatalf("version after up = %d, want 2", v)
	}
	for _, table := range []string{"tenants", "stores"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("table %s missing after up", table)
		}
	}

	// 逐步回滚到 0。
	for want := int64(1); want >= 0; want-- {
		if err := db.MigrateDown(ctx, pool); err != nil {
			t.Fatal(err)
		}
		if v := version(t, pool); v != want {
			t.Fatalf("version after down = %d, want %d", v, want)
		}
	}
	for _, table := range []string{"tenants", "stores"} {
		if tableExists(t, pool, table) {
			t.Fatalf("table %s still exists after rollback", table)
		}
	}

	// 回滚后可以再次向前执行。
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if v := version(t, pool); v != 2 {
		t.Fatalf("version after re-up = %d, want 2", v)
	}
}

func TestMigrateReset(t *testing.T) {
	pool := dbtest.NewPool(t)
	if err := db.MigrateReset(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if v := version(t, pool); v != 0 {
		t.Fatalf("version after reset = %d, want 0", v)
	}
}
