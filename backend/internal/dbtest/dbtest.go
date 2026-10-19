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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/wzhlinhai/salon/backend/internal/db"
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
		startErr = fmt.Errorf("start postgres container: %w", err)
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
	defer admin.Close(ctx)
	name := fmt.Sprintf("test_%d", dbSeq.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}

	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
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
