// Package optest 让测试通过 HTTP 和 AI 两条路径调用同一个业务操作。
package optest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/httpapi"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// App 是一个完整的后端实例。调用者可以用真实的账号密码登录，也可以由测试直接指定。
type App struct {
	// Groupbuy 是测试可替换的平台注册表。
	Groupbuy *groupbuy.Providers
	Handler  http.Handler
	Registry *ops.Registry
	Auth     *auth.Service

	mu      sync.Mutex
	callers map[string]ops.Caller
	instant *time.Time
	aiModel ai.Model
}

// New 用 pool 组装后端。
func New(t testing.TB, pool *pgxpool.Pool) *App {
	t.Helper()
	svc, err := auth.NewService(pool, []byte("test secret that is at least 32 bytes long"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Groupbuy: &groupbuy.Providers{}, Auth: svc, callers: map[string]ops.Caller{}}
	a.Handler, a.Registry = httpapi.New(pool, httpapi.Config{Groupbuy: a.Groupbuy, Authenticate: a.authenticate, Login: svc.Login, Now: a.Now, AIModel: a.currentAIModel})
	return a
}

// SetAIModel 替换 /api/ai/chat 使用的模型；测试用脚本化假模型，不访问网络。
func (a *App) SetAIModel(model ai.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.aiModel = model
}

// AIModel 返回当前 AI 对话使用的模型。
func (a *App) AIModel() ai.Model {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.aiModel
}

func (a *App) currentAIModel() ai.Model { return a.AIModel() }

// authenticate 先认测试直接指定的调用者，再走真实的登录凭证。
func (a *App) authenticate(r *http.Request) (ops.Caller, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ops.Caller{}, false
	}
	a.mu.Lock()
	c, ok := a.callers[token]
	a.mu.Unlock()
	if ok {
		return c, true
	}
	return a.Auth.Authenticate(r)
}

// Login 通过 HTTP 登录，返回状态码和登录凭证。
func (a *App) Login(t testing.TB, account, password string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"account": account, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, req)
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Token
}

// HTTPWithToken 用登录凭证通过 HTTP 调用操作。
func (a *App) HTTPWithToken(t testing.TB, token, op string, args any) Result {
	t.Helper()
	return a.do(t, token, op, args)
}

// CallerFor 用登录凭证识别调用者，与 HTTP 请求的识别方式相同。AI 调用用它取得发起人本人的 Caller。
func (a *App) CallerFor(t testing.TB, token string) (ops.Caller, bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return a.Auth.Authenticate(req)
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

// Detail 返回拒绝的提示文字：HTTP 取响应的 detail，AI 取错误文字。
func (r Result) Detail() string {
	if detail, ok := r.Body["detail"].(string); ok {
		return detail
	}
	if r.Err != nil {
		return r.Err.Error()
	}
	return ""
}

// HTTP 以 caller 身份通过 HTTP 调用操作。caller 为 nil 时不带登录信息。
func (a *App) HTTP(t testing.TB, caller *ops.Caller, op string, args any) Result {
	t.Helper()
	token := ""
	if caller != nil {
		token = a.token(*caller)
	}
	return a.do(t, token, op, args)
}

func (a *App) do(t testing.TB, token, op string, args any) Result {
	t.Helper()
	body, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, ops.PathPrefix+op, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
		return Result{Status: ops.StatusOf(err), Err: err}
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return Result{Status: http.StatusOK, Body: m}
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

// SetNow 把业务操作的时刻固定为 t，零值恢复真实时间。
func (a *App) SetNow(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t.IsZero() {
		a.instant = nil
	} else {
		a.instant = &t
	}
}

// Advance 将当前时刻前移 d，未固定时从真实时刻开始并固定。
func (a *App) Advance(d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.current().Add(d)
	a.instant = &t
}

// Now 返回业务操作此刻会使用的时刻。
func (a *App) Now() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current()
}

// current 在持有 mu 时读取时刻。
func (a *App) current() time.Time {
	if a.instant != nil {
		return *a.instant
	}
	return time.Now()
}
