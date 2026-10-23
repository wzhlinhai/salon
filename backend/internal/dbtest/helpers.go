package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
)

// Exec 在测试事务里执行 SQL，错误直接归因于调用处。
func Exec(t testing.TB, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// Commit 提交测试事务。
func Commit(t testing.TB, tx pgx.Tx) {
	t.Helper()
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Rollback 回滚测试事务。
func Rollback(t testing.TB, tx interface{ Rollback(context.Context) error }) {
	t.Helper()
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Snapshot 在租户事务中读取表快照，固定行顺序，读取后立即释放连接。
func Snapshot(t testing.TB, pool *pgxpool.Pool, tenantID uuid.UUID, tables ...string) string {
	t.Helper()
	tx := AppTx(t, pool, tenantID)
	out := SnapshotTx(t, tx, tables...)
	Rollback(t, tx)
	return out
}

// SnapshotTx 在已有事务中读取表快照；按整行 JSON 排序，支持没有 id 列的关联表。
func SnapshotTx(t testing.TB, tx pgx.Tx, tables ...string) string {
	t.Helper()
	parts := make([]string, len(tables))
	for i, table := range tables {
		parts[i] = "(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text COLLATE \"C\"),'[]'::jsonb) FROM " + pgx.Identifier{table}.Sanitize() + " r)"
	}
	var out string
	if err := tx.QueryRow(context.Background(), "SELECT jsonb_build_array("+strings.Join(parts, ",")+")::text").Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// MigrateDownTo 将测试数据库回滚到指定迁移版本，越过目标或迁移失败时终止测试。
func MigrateDownTo(t testing.TB, pool *pgxpool.Pool, version int64) {
	t.Helper()
	for {
		current, err := db.MigrationVersion(context.Background(), pool)
		if err != nil {
			t.Fatal(err)
		}
		if current < version {
			t.Fatalf("migration version %d is below target %d", current, version)
		}
		if current == version {
			return
		}
		if err := db.MigrateDown(context.Background(), pool); err != nil {
			t.Fatal(err)
		}
	}
}
