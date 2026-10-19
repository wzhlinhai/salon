// Package optest 让测试通过 HTTP 和 AI 两条路径调用同一个业务操作。
package optest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/httpapi"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// App 是一个完整的后端实例，调用者用测试令牌识别。
type App struct {
	Handler  http.Handler
	Registry *ops.Registry

	mu      sync.Mutex
	callers map[string]ops.Caller
}

// New 用 pool 组装后端。
func New(pool *pgxpool.Pool) *App {
	a := &App{callers: map[string]ops.Caller{}}
	a.Handler, a.Registry = httpapi.New(pool, a.authenticate)
	return a
}

func (a *App) authenticate(r *http.Request) (ops.Caller, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ops.Caller{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.callers[token]
	return c, ok
}

func (a *App) token(c ops.Caller) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	tok := uuid.NewString()
	a.callers[tok] = c
	return tok
}

// Result 是一次调用的结果。HTTP 和 AI 两条路径的结果统一成同一种形式，便于比较。
type Result struct {
	// Status：HTTP 调用为响应状态码；AI 调用按错误映射为同义的状态码。
	Status int
	Body   map[string]any
	Err    error
}

// HTTP 以 caller 身份通过 HTTP 调用操作。caller 为 nil 时不带登录信息。
func (a *App) HTTP(t testing.TB, caller *ops.Caller, op string, args any) Result {
	t.Helper()
	body, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, ops.PathPrefix+op, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if caller != nil {
		req.Header.Set("Authorization", "Bearer "+a.token(*caller))
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, req)
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode %s response %q: %v", op, rec.Body.String(), err)
		}
	}
	return Result{Status: rec.Code, Body: m}
}

// AI 以 caller 身份通过 AI 工具调用操作。
func (a *App) AI(t testing.TB, caller ops.Caller, op string, args any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.Registry.CallTool(context.Background(), caller, op, raw)
	if err != nil {
		return Result{Status: statusOf(err), Err: err}
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return Result{Status: http.StatusOK, Body: m}
}

func statusOf(err error) int {
	var te *ops.ToolError
	switch {
	case errors.As(err, &te):
		return http.StatusUnprocessableEntity
	case errors.Is(err, ops.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ops.ErrNotFound), errors.Is(err, ops.ErrUnknownOperation):
		return http.StatusNotFound
	case errors.Is(err, ops.ErrDraftRequired):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// Channel 是一条调用路径，供表格测试对两条路径跑同一组用例。
type Channel struct {
	Name string
	Call func(t testing.TB, caller ops.Caller, op string, args any) Result
}

// Channels 返回 HTTP 和 AI 两条调用路径。
func (a *App) Channels() []Channel {
	return []Channel{
		{"http", func(t testing.TB, c ops.Caller, op string, args any) Result { return a.HTTP(t, &c, op, args) }},
		{"ai", a.AI},
	}
}
