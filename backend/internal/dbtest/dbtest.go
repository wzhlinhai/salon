// Package dbtest 为测试提供真实的 PostgreSQL。
//
// 每个测试进程启动一个全新的 PostgreSQL 容器，并按需迁移一次模板数据库；NewPool 从模板克隆数据库，
// 测试之间互不影响。行级安全的测试必须跑在真实 PostgreSQL 上（ADR-0007）。
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
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
	once         sync.Once
	adminDSN     string
	startErr     error
	dbSeq        atomic.Int64
	templateOnce sync.Once
	templateErr  error
	processTag   = fmt.Sprintf("%d_%s", os.Getpid(), randomSuffix())
	templateName = "salon_tpl_" + processTag
	janitorPipe  *os.File
)

const (
	dsnEnv               = "SALON_TEST_PG_DSN"
	janitorEnv           = "SALON_TEST_PG_JANITOR"
	environmentSignature = "启动 PostgreSQL 测试容器失败"
	templateLockKey      = int64(0x73616c6f6e313939)
)

func init() {
	if template := os.Getenv(janitorEnv); template != "" {
		runJanitor(template)
		os.Exit(0)
	}
}

func randomSuffix() string {
	var buffer [4]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer[:])
}

// setupError 在共用服务器时给所有启动错误加上环境失败标记。
func setupError(err error) error {
	if err == nil || os.Getenv(dsnEnv) == "" || strings.Contains(err.Error(), environmentSignature) {
		return err
	}
	return fmt.Errorf("%s：%w", environmentSignature, err)
}

// templateLock 在共用服务器上串行化模板迁移，避免集群级对象（角色等）互相竞争。
func templateLock(ctx context.Context, admin *pgx.Conn) error {
	if os.Getenv(dsnEnv) == "" {
		return nil
	}
	_, err := admin.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLockKey)
	return setupError(err)
}

func templateUnlock(ctx context.Context, admin *pgx.Conn) {
	if os.Getenv(dsnEnv) == "" {
		return
	}
	_, _ = admin.Exec(ctx, "SELECT pg_advisory_unlock($1)", templateLockKey)
}

func start() {
	if dsn := os.Getenv(dsnEnv); dsn != "" {
		adminDSN = dsn
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			err = conn.Ping(ctx)
			_ = conn.Close(ctx)
		}
		if err != nil {
			startErr = fmt.Errorf("启动 PostgreSQL 测试容器失败：连接 %s：%w", dsnEnv, err)
		}
		return
	}
	ctx := context.Background()
	adminDSN, startErr = runPostgres(ctx, func() (postgresContainer, error) {
		c, err := postgres.Run(ctx, Image,
			testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql": ""}),
			postgres.WithDatabase("salon"),
			postgres.WithUsername("salon"),
			postgres.WithPassword("salon"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(60*time.Second)),
		)
		if c == nil {
			return nil, err
		}
		return c, err
	})
}

type postgresContainer interface {
	ConnectionString(context.Context, ...string) (string, error)
	Terminate(context.Context, ...testcontainers.TerminateOption) error
}

var missingPort = regexp.MustCompile(`port "[^"\n]+/tcp" not found`)

// runPostgres 在端口映射缺失时清理容器并重新启动，最多尝试三次。
func runPostgres(ctx context.Context, run func() (postgresContainer, error)) (string, error) {
	for attempt := 1; ; attempt++ {
		c, err := run()
		diagnostic := "确认 Docker 已运行（WSL 下打开 Docker Desktop 并启用 WSL 集成）"
		var dsn string
		if err == nil {
			diagnostic = "读取连接地址"
			dsn, err = c.ConnectionString(ctx, "sslmode=disable")
		}
		if err == nil {
			return dsn, nil
		}
		if c != nil {
			_ = c.Terminate(ctx)
		}
		// 保留 Ryuk/reaper 启动失败的重试，另识别 PostgreSQL 的端口映射缺失。
		retry := missingPort.MatchString(err.Error()) || strings.Contains(err.Error(), "reaper")
		if !retry || attempt == 3 {
			return "", fmt.Errorf("启动 PostgreSQL 测试容器失败：%s：%w", diagnostic, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// NewEmptyPool 新建一个空数据库（未执行迁移）并返回连接池。测试结束时关闭连接池并删除数据库。
func NewEmptyPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return newPool(t, "")
}

func newPool(t testing.TB, template string) *pgxpool.Pool {
	t.Helper()
	once.Do(start)
	if startErr != nil {
		t.Fatal(startErr)
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(setupError(err))
	}
	defer func() { _ = admin.Close(ctx) }()
	name := fmt.Sprintf("salon_test_%s_%d", processTag, dbSeq.Add(1))
	create := "CREATE DATABASE " + name
	if template != "" {
		create += " TEMPLATE " + template
	}
	if _, err := admin.Exec(ctx, create); err != nil {
		t.Fatal(setupError(err))
	}

	t.Cleanup(func() {
		admin, err := pgx.Connect(ctx, adminDSN)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = admin.Close(ctx) }()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})

	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(setupError(err))
	}
	// 与 CI 一样限制为四个连接，让占用连接的测试在本地也能暴露问题。
	cfg.MaxConns = 4
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(setupError(err))
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewPool 从进程内只迁移一次的模板克隆独立数据库并返回连接池。
func NewPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	once.Do(start)
	if startErr != nil {
		t.Fatal(startErr)
	}
	templateOnce.Do(func() { templateErr = createTemplate() })
	if templateErr != nil {
		t.Fatal(templateErr)
	}
	return newPool(t, templateName)
}

func createTemplate() error {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return setupError(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if err := templateLock(ctx, admin); err != nil {
		return err
	}
	defer templateUnlock(ctx, admin)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+templateName); err != nil {
		return setupError(err)
	}
	if os.Getenv(dsnEnv) != "" {
		startJanitor(templateName)
	}
	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		return setupError(err)
	}
	cfg.ConnConfig.Database = templateName
	// 迁移期间只用一个连接，几十个测试进程共用服务器时不会耗尽连接数。
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return setupError(err)
	}
	// MigrateUp 关闭 goose 的连接包装，随后关闭连接池，确保克隆前模板没有会话。
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		return setupError(err)
	}
	return nil
}

// startJanitor 启动一个脱离当前会话的子进程，在测试进程结束后删除本进程的模板和数据库。
func startJanitor(template string) {
	if !janitorSupported() {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest: start janitor: %v\n", err)
		return
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest: start janitor: %v\n", err)
		return
	}
	command := exec.Command(executable)
	command.Env = append(os.Environ(), janitorEnv+"="+template)
	command.ExtraFiles = []*os.File{reader}
	command.SysProcAttr = janitorSysProcAttr()
	if err := command.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		fmt.Fprintf(os.Stderr, "dbtest: start janitor: %v\n", err)
		return
	}
	_ = reader.Close()
	janitorPipe = writer
	runtime.KeepAlive(janitorPipe)
	_ = command.Process.Release()
}

// runJanitor 等待父测试进程退出，然后删除它留下的模板和数据库。
func runJanitor(template string) {
	pipe := os.NewFile(3, "janitor-pipe")
	if pipe != nil {
		_, _ = io.Copy(io.Discard, pipe)
		_ = pipe.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv(dsnEnv))
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	prefix := "salon_test_" + strings.TrimPrefix(template, "salon_tpl_") + "_"
	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database")
	if err != nil {
		return
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return
		}
		if name == template || strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	rows.Close()
	for _, name := range names {
		_, _ = conn.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	}
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

// LockWaits 返回当前数据库中等待行锁或咨询锁的会话数。
func LockWaits(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var waiting int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&waiting)
	return waiting, err
}

// WaitForLockWait 等待当前数据库出现等待锁的请求，超过五秒则测试失败。
func WaitForLockWait(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		waiting, err := LockWaits(ctx, pool)
		if err != nil {
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
