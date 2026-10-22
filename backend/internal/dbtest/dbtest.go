// Package dbtest 为测试提供真实的 PostgreSQL。
//
// 每个测试进程启动一个全新的 PostgreSQL 容器；每次调用 NewPool 在其中新建一个空数据库，
// 测试之间互不影响。行级安全的测试必须跑在真实 PostgreSQL 上（ADR-0007）。
package dbtest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// Image 是测试使用的 PostgreSQL 镜像。
const Image = "postgres:18-alpine"

var (
	once     sync.Once
	adminDSN string
	startErr error
	dbSeq    atomic.Int64
)

func start() {
	ctx := context.Background()
	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("salon"),
		postgres.WithUsername("salon"),
		postgres.WithPassword("salon"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		startErr = fmt.Errorf("启动 PostgreSQL 测试容器失败：确认 Docker 已运行（WSL 下打开 Docker Desktop 并启用 WSL 集成）：%w", err)
		return
	}
	adminDSN, startErr = c.ConnectionString(ctx, "sslmode=disable")
}

// NewEmptyPool 新建一个空数据库（未执行迁移）并返回连接池。测试结束时连接池关闭。
func NewEmptyPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	once.Do(start)
	if startErr != nil {
		t.Fatal(startErr)
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	name := fmt.Sprintf("test_%d", dbSeq.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}

	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	// 与 CI 一样限制为四个连接，让占用连接的测试在本地也能暴露问题。
	cfg.MaxConns = 4
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewPool 新建一个已执行全部迁移的数据库并返回连接池。
func NewPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := NewEmptyPool(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// NewTenant 在 pool 中建一个租户和它的一家门店，返回二者的 ID。
func NewTenant(t testing.TB, pool *pgxpool.Pool, name string) (tenantID, storeID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tenantID = uuid.New()
	err := db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		if _, err := q.CreateTenant(ctx, dbgen.CreateTenantParams{ID: tenantID, Name: name}); err != nil {
			return err
		}
		s, err := q.CreateStore(ctx, dbgen.CreateStoreParams{TenantID: tenantID, Name: name + "门店"})
		storeID = s.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenantID, storeID
}

// AppTx 开一个与 db.InTenant 相同的事务（salon_app 角色、租户 tenantID 的上下文），由测试决定何时提交。
// 用于 sqlc 查询之外的写法，或在事务进行中做断言（例如持锁时另一个请求要等待）。测试结束时未提交的事务回滚。
func AppTx(t testing.TB, pool *pgxpool.Pool, tenantID uuid.UUID) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE salon_app"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
		t.Fatal(err)
	}
	return tx
}

// WaitForLockWait 等待当前数据库出现等待锁的请求，超过五秒则测试失败。
func WaitForLockWait(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&waiting); err != nil {
			t.Fatalf("waiting for a database lock wait: %v", err)
		}
		if waiting > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("no database lock wait observed within 5 seconds")
		case <-ticker.C:
		}
	}
}
