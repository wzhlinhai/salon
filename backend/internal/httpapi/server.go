// Package httpapi 组装 HTTP 接口。
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// New 返回配置好路由的 Echo 实例。
func New(pool *pgxpool.Pool) *echo.Echo {
	e := echo.New()
	e.Use(middleware.Recover())
	e.GET("/healthz", healthz(pool))
	return e
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
