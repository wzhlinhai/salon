package db_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

// versions 是迁移文件编号的升序列表。编号可能因并行分支预留而暂时不连续。
var versions = func() []int64 {
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		panic(fmt.Sprintf("no migrations found: %v", err))
	}
	out := make([]int64, 0, len(files))
	for _, f := range files {
		n, err := strconv.ParseInt(strings.SplitN(filepath.Base(f), "_", 2)[0], 10, 64)
		if err != nil {
			panic(fmt.Sprintf("migration %s: %v", f, err))
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}()

// latest 是最新的迁移版本：migrations 目录里最大的文件编号。
var latest = versions[len(versions)-1]

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

	// 逐步回滚到 0；每步回到文件列表中的前一个版本，允许预留编号造成空档。
	for i := len(versions) - 1; i >= 1; i-- {
		if err := db.MigrateDown(ctx, pool); err != nil {
			t.Fatal(err)
		}
		if v := version(t, pool); v != versions[i-1] {
			t.Fatalf("version after down = %d, want %d", v, versions[i-1])
		}
	}
	if err := db.MigrateDown(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if v := version(t, pool); v != 0 {
		t.Fatalf("version after final down = %d, want 0", v)
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
