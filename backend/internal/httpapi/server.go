// Package httpapi 组装 HTTP 接口。
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
	"github.com/wzhlinhai/salon/backend/internal/stores"
)

// Authenticator 从请求中识别调用者。识别不出时返回 false，业务操作会返回 401。
type Authenticator func(r *http.Request) (ops.Caller, bool)

// LoginFunc 校验账号密码并返回登录凭证。
type LoginFunc func(ctx context.Context, account, password string) (string, error)

// Config 是组装 HTTP 接口所需的依赖。
type Config struct {
	Authenticate Authenticator
	Login        LoginFunc
}

func init() {
	// 业务操作返回的列表都不是 nil（空列表返回 []），OpenAPI 里不标为可空，前端类型因此更准确。
	huma.DefaultArrayNullable = false
}

// New 返回配置好路由的 Echo 实例，以及注册了全部业务操作的 Registry（供 AI 使用）。
func New(pool *pgxpool.Pool, cfg Config) (*echo.Echo, *ops.Registry) {
	e := echo.New()
	e.Use(middleware.Recover())
	e.GET("/healthz", healthz(pool))

	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if caller, ok := cfg.Authenticate(c.Request()); ok {
				c.SetRequest(c.Request().WithContext(ops.WithCaller(c.Request().Context(), caller)))
			}
			return next(c)
		}
	})

	hc := huma.DefaultConfig("Salon API", "0.1.0")
	// 不给响应体加 $schema 字段。
	hc.CreateHooks = nil
	api := humaecho.New(e, hc)
	registerLogin(api, cfg.Login)

	reg := ops.NewRegistry(pool, api)
	stores.Register(reg)
	staff.Register(reg)
	customers.Register(reg)
	settings.Register(reg)
	audit.Register(reg)
	return e, reg
}

type loginInput struct {
	Body struct {
		Account  string `json:"account" minLength:"1" maxLength:"50"`
		Password string `json:"password" minLength:"1" maxLength:"128"`
	}
}

type loginOutput struct {
	Body struct {
		Token string `json:"token" doc:"登录凭证，放在 Authorization: Bearer 头里"`
	}
}

func registerLogin(api huma.API, login LoginFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/api/auth/login",
		Summary:     "员工登录",
	}, func(ctx context.Context, in *loginInput) (*loginOutput, error) {
		token, err := login(ctx, in.Body.Account, in.Body.Password)
		if errors.Is(err, auth.ErrLoginLocked) {
			return nil, huma.Error429TooManyRequests("登录失败次数过多，请 15 分钟后再试")
		}
		if errors.Is(err, auth.ErrInvalidLogin) {
			return nil, huma.Error401Unauthorized("账号或密码不正确")
		}
		if err != nil {
			return nil, err
		}
		out := &loginOutput{}
		out.Body.Token = token
		return out, nil
	})
}

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

func healthz(pool *pgxpool.Pool) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			return c.JSON(http.StatusServiceUnavailable, healthResponse{Status: "unavailable", Database: "unreachable"})
		}
		return c.JSON(http.StatusOK, healthResponse{Status: "ok", Database: "ok"})
	}
}
