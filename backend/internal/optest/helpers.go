package optest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

// RequireDraftCreated 断言 AI 对写操作的调用创建了一条 pending 草稿，而不是执行写操作。
// unchanged 表示调用前后业务数据快照相同；草稿本身不在业务数据快照里。
func RequireDraftCreated(t testing.TB, r Result, unchanged bool) map[string]any {
	t.Helper()
	if r.Status != http.StatusOK || r.Err != nil {
		t.Fatalf("AI write should create a pending draft: status=%d body=%v err=%v", r.Status, r.Body, r.Err)
	}
	if r.Body["id"] == nil || r.Body["status"] != "pending" {
		t.Fatalf("AI write should create a pending draft: %v", r.Body)
	}
	if !unchanged {
		t.Fatal("AI write changed business data")
	}
	return r.Body
}

// Call 用凭证通过 HTTP 调用操作，want 为零时由调用方自行断言状态。
func (a *App) Call(t testing.TB, token, op string, args any, want int) Result {
	t.Helper()
	r := a.HTTPWithToken(t, token, op, args)
	if want != 0 && r.Status != want {
		t.Fatalf("%s: got %d %v (%v), want %d", op, r.Status, r.Body, r.Err, want)
	}
	return r
}

// WaitResult 等待并发请求完成并检查状态，十秒内未返回即失败。
func WaitResult(t testing.TB, done <-chan Result, want int) Result {
	t.Helper()
	select {
	case r := <-done:
		if r.Status != want {
			t.Fatalf("concurrent request: got %d %v (%v), want %d", r.Status, r.Body, r.Err, want)
		}
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent request timed out")
	}
	return Result{}
}

// AssertWaiting 检查并发请求尚未返回且当前数据库至少有 waiters 个会话等待锁，超过五秒则失败。
func AssertWaiting(t testing.TB, pool *pgxpool.Pool, done <-chan Result, waiters int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	observed := 0
	for {
		select {
		case r := <-done:
			t.Fatalf("request returned while transaction holds lock: expected at least %d lock waiters, observed %d: %+v", waiters, observed, r)
		default:
		}
		waiting, err := dbtest.LockWaits(ctx, pool)
		if err == nil {
			observed = waiting
		}
		select {
		case r := <-done:
			t.Fatalf("request returned while transaction holds lock: expected at least %d lock waiters, observed %d: %+v", waiters, observed, r)
		default:
		}
		if ctx.Err() != nil {
			t.Fatalf("database lock waits: expected at least %d, observed %d within 5 seconds", waiters, observed)
		}
		if err != nil {
			t.Fatalf("waiting for a database lock wait: %v", err)
		}
		if waiting >= waiters {
			return
		}
		select {
		case r := <-done:
			t.Fatalf("request returned while transaction holds lock: expected at least %d lock waiters, observed %d: %+v", waiters, observed, r)
		case <-ctx.Done():
			t.Fatalf("database lock waits: expected at least %d, observed %d within 5 seconds", waiters, observed)
		case <-ticker.C:
		}
	}
}
