// salon 是后端服务的入口。
//
//	salon serve            启动 HTTP 服务
//	salon migrate up       执行全部未执行的迁移
//	salon migrate down     回滚最近一次迁移
//	salon migrate status   显示当前迁移版本
//
// 环境变量：DATABASE_URL（必填），HTTP_ADDR（默认 :8080）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/httpapi"
)

const usage = "usage: salon serve | salon migrate up|down|status"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("salon", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	switch {
	case args[0] == "serve":
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		sc := echo.StartConfig{Address: addr, HideBanner: true}
		return sc.Start(ctx, httpapi.New(pool))
	case args[0] == "migrate" && len(args) == 2:
		switch args[1] {
		case "up":
			return db.MigrateUp(ctx, pool)
		case "down":
			return db.MigrateDown(ctx, pool)
		case "status":
			v, err := db.MigrationVersion(ctx, pool)
			if err != nil {
				return err
			}
			fmt.Println("version:", v)
			return nil
		}
	}
	return errors.New(usage)
}
