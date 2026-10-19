// Package httpapi 组装 HTTP 接口。
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/stores"
)

// Authenticator 从请求中识别调用者。识别不出时返回 false，业务操作会返回 401。
type Authenticator func(r *http.Request) (ops.Caller, bool)

// DenyAll 不识别任何调用者。登录（P0-03）实现之前，生产环境使用它。
func DenyAll(*http.Request) (ops.Caller, bool) { return ops.Caller{}, false }

// New 返回配置好路由的 Echo 实例，以及注册了全部业务操作的 Registry（供 AI 使用）。
func New(pool *pgxpool.Pool, auth Authenticator) (*echo.Echo, *ops.Registry) {
	e := echo.New()
	e.Use(middleware.Recover())
	e.GET("/healthz", healthz(pool))

	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if caller, ok := auth(c.Request()); ok {
				c.SetRequest(c.Request().WithContext(ops.WithCaller(c.Request().Context(), caller)))
			}
			return next(c)
		}
	})

	cfg := huma.DefaultConfig("Salon API", "0.1.0")
	// 不给响应体加 $schema 字段。
	cfg.CreateHooks = nil
	api := humaecho.New(e, cfg)

	reg := ops.NewRegistry(pool, api)
	stores.Register(reg)
	return e, reg
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
