package db_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

// latest 是最新的迁移版本：migrations 目录里最大的文件编号。
var latest = func() int64 {
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		panic(fmt.Sprintf("no migrations found: %v", err))
	}
	var v int64
	for _, f := range files {
		n, err := strconv.ParseInt(strings.SplitN(filepath.Base(f), "_", 2)[0], 10, 64)
		if err != nil {
			panic(fmt.Sprintf("migration %s: %v", f, err))
		}
		v = max(v, n)
	}
	return v
}()

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
	if v := version(t, pool); v != latest {
		t.Fatalf("version after up = %d, want %d", v, latest)
	}
	for _, table := range []string{"tenants", "stores", "staff", "roles"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("table %s missing after up", table)
		}
	}

	// 逐步回滚到 0。
	for want := latest - 1; want >= 0; want-- {
		if err := db.MigrateDown(ctx, pool); err != nil {
			t.Fatal(err)
		}
		if v := version(t, pool); v != want {
			t.Fatalf("version after down = %d, want %d", v, want)
		}
	}
	for _, table := range []string{"tenants", "stores", "staff", "roles"} {
		if tableExists(t, pool, table) {
			t.Fatalf("table %s still exists after rollback", table)
		}
	}

	// 回滚后可以再次向前执行。
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if v := version(t, pool); v != latest {
		t.Fatalf("version after re-up = %d, want %d", v, latest)
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
