package dbtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"

	"github.com/wzhlinhai/salon/backend/internal/db"
)

func TestNewPoolsAreIndependent(t *testing.T) {
	first, second := NewPool(t), NewPool(t)
	NewTenant(t, first, "独立数据库")
	var count int
	if err := second.QueryRow(context.Background(), "SELECT count(*) FROM tenants").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("second database has %d tenants, want 0", count)
	}
}

func TestNewPoolIsFullyMigrated(t *testing.T) {
	pool := NewPool(t)
	migrations, err := goose.CollectMigrations("../db/migrations", 0, goose.MaxVersion)
	if err != nil {
		t.Fatal(err)
	}
	version, err := db.MigrationVersion(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if latest := migrations[len(migrations)-1].Version; version != latest {
		t.Fatalf("migration version = %d, want %d", version, latest)
	}
}

func TestNewPoolDoesNotChangeTemplate(t *testing.T) {
	first := NewPool(t)
	NewTenant(t, first, "不写入模板")
	second := NewPool(t)
	var count int
	if err := second.QueryRow(context.Background(), "SELECT count(*) FROM tenants").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("fresh clone has %d tenants, want 0", count)
	}
}

func TestSetupErrorMarksSharedServerFailures(t *testing.T) {
	t.Setenv(dsnEnv, "postgres://example")
	if err := setupError(errors.New("boom")); !strings.Contains(err.Error(), environmentSignature) {
		t.Fatalf("shared error %q lacks environment signature", err)
	}
	t.Setenv(dsnEnv, "")
	if err := setupError(errors.New("boom")); err.Error() != "boom" {
		t.Fatalf("container-mode error = %q, want boom", err)
	}
}

func TestConcurrentProcessesShareOneServer(t *testing.T) {
	if !janitorSupported() {
		t.Skip("janitor is not supported on this platform")
	}
	NewPool(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	outputs := make([][]byte, 2)
	failures := make([]error, len(outputs))
	var wait sync.WaitGroup
	for index := range outputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			command := exec.Command(executable, "-test.run=^TestSharedServerHelper$")
			command.Env = append(os.Environ(), "GO_WANT_SHARED_SERVER_HELPER=1", dsnEnv+"="+adminDSN)
			outputs[index], failures[index] = command.CombinedOutput()
		}(index)
	}
	wait.Wait()
	var tags []string
	for index, output := range outputs {
		if failures[index] != nil {
			t.Fatalf("child %d: %v\n%s", index, failures[index], output)
		}
		if !bytes.Contains(output, []byte("PASS")) {
			t.Fatalf("child %d did not pass:\n%s", index, output)
		}
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "TAG=") {
				tags = append(tags, strings.TrimSpace(strings.TrimPrefix(line, "TAG=")))
			}
		}
	}
	if len(tags) != len(outputs) {
		t.Fatalf("child tags = %v, want %d", tags, len(outputs))
	}
	for _, tag := range tags {
		waitForNoDatabases(t, "salon_tpl_"+tag, "salon_test_"+tag+"_")
	}
}

func TestSharedServerHelper(t *testing.T) {
	if os.Getenv("GO_WANT_SHARED_SERVER_HELPER") != "1" {
		return
	}
	fmt.Printf("TAG=%s\n", processTag)
	first, second := NewPool(t), NewPool(t)
	NewTenant(t, first, "并发进程")
	var count int
	if err := second.QueryRow(context.Background(), "SELECT count(*) FROM tenants").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("second database has %d tenants, want 0", count)
	}
}

func TestSharedServerJanitorAfterSIGKILL(t *testing.T) {
	if !janitorSupported() {
		t.Skip("janitor is not supported on this platform")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatal(startErr)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := t.TempDir() + "/ready"
	command := exec.Command(executable, "-test.run=^TestSharedServerSleeper$")
	command.Env = append(os.Environ(), "GO_WANT_SHARED_SERVER_SLEEP=1",
		"SHARED_SERVER_MARKER="+marker, dsnEnv+"="+adminDSN)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if data, err := os.ReadFile(marker); err == nil {
			tag := strings.TrimSpace(string(data))
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			waitForNoDatabases(t, "salon_tpl_"+tag, "salon_test_"+tag+"_")
			return
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal("sleeper child never became ready")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSharedServerSleeper(t *testing.T) {
	if os.Getenv("GO_WANT_SHARED_SERVER_SLEEP") != "1" {
		return
	}
	NewPool(t)
	if err := os.WriteFile(os.Getenv("SHARED_SERVER_MARKER"), []byte(processTag), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
}

func waitForNoDatabases(t *testing.T, patterns ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	deadline := time.Now().Add(time.Minute)
	for {
		rows, err := conn.Query(ctx, "SELECT datname FROM pg_database")
		if err != nil {
			t.Fatal(err)
		}
		var leftovers []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for _, pattern := range patterns {
				if strings.HasPrefix(name, pattern) {
					leftovers = append(leftovers, name)
				}
			}
		}
		rows.Close()
		if len(leftovers) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("janitor left databases %v", leftovers)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type fakePostgres struct {
	err        error
	terminated int
}

func (c *fakePostgres) ConnectionString(context.Context, ...string) (string, error) {
	return "dsn", c.err
}

func (c *fakePostgres) Terminate(context.Context, ...testcontainers.TerminateOption) error {
	c.terminated++
	return nil
}

func TestRunPostgres(t *testing.T) {
	portErr := errors.New(`port "5432/tcp" not found`)
	otherErr := errors.New("pull postgres: unavailable")
	for _, tc := range []struct {
		name          string
		runErr        error
		connectionErr error
		failures      int
		noContainer   bool
		nextErr       error
		calls         int
		want          error
	}{
		{name: "success", calls: 1},
		{name: "reaper recovers", runErr: errors.New("create container: reaper: new reaper: No such container"), failures: 1, calls: 2},
		{name: "nil container", runErr: portErr, noContainer: true, failures: 1, calls: 2},
		{name: "retry returns other error", runErr: portErr, nextErr: otherErr, failures: 2, calls: 2, want: otherErr},
		{name: "Run missing port", runErr: portErr, failures: 1, calls: 2},
		{name: "Ryuk missing port", runErr: errors.New("reaper: mapped port: invalid port"), failures: 1, calls: 2},
		{name: "connection missing port", connectionErr: portErr, failures: 1, calls: 2},
		{name: "three connection failures", connectionErr: portErr, failures: 3, calls: 3, want: portErr},
		{name: "three failures", runErr: portErr, failures: 3, calls: 3, want: portErr},
		{name: "other Run error", runErr: otherErr, failures: 1, calls: 1, want: otherErr},
		{name: "other connection error", connectionErr: otherErr, failures: 1, calls: 1, want: otherErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var containers []*fakePostgres
			got, err := runPostgres(context.Background(), func() (postgresContainer, error) {
				calls++
				c := &fakePostgres{}
				containers = append(containers, c)
				if calls <= tc.failures {
					c.err = tc.connectionErr
					if tc.noContainer {
						return nil, tc.runErr
					}
					if calls > 1 && tc.nextErr != nil {
						return c, tc.nextErr
					}
					return c, tc.runErr
				}
				return c, nil
			})
			if calls != tc.calls || !errors.Is(err, tc.want) {
				t.Fatalf("calls=%d err=%v; want calls=%d err=%v", calls, err, tc.calls, tc.want)
			}
			if tc.want != nil {
				diagnostic := "确认 Docker 已运行（WSL 下打开 Docker Desktop 并启用 WSL 集成）"
				if tc.connectionErr != nil {
					diagnostic = "读取连接地址"
				}
				want := "启动 PostgreSQL 测试容器失败：" + diagnostic + "：" + tc.want.Error()
				if err.Error() != want {
					t.Fatalf("message=%q, want %q", err.Error(), want)
				}
			}
			if err == nil && got != "dsn" {
				t.Fatalf("dsn=%q", got)
			}
			for i, c := range containers {
				want := 0
				if i < tc.failures && !tc.noContainer {
					want = 1
				}
				if c.terminated != want {
					t.Fatalf("container %d terminated=%d, want %d", i, c.terminated, want)
				}
			}
		})
	}
}
