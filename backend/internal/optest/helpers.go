package optest

import (
	"testing"
	"time"
)

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

// AssertWaiting 检查占锁期间并发请求仍然不能返回。
func AssertWaiting(t testing.TB, done <-chan Result) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("request returned while transaction holds lock: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}
