// salon 是后端服务的入口。
//
//	salon serve            启动 HTTP 服务
//	salon migrate up       执行全部未执行的迁移
//	salon migrate down     回滚最近一次迁移
//	salon migrate status   显示当前迁移版本
//	salon openapi          输出 OpenAPI 文档（JSON），供前端生成类型，不需要数据库
//	salon bootstrap -tenant 名称 -store 名称 -owner-name 姓名 -owner-number 编号 -owner-account 账号
//	                       开店：建门店、老板和默认角色。老板密码从 BOOTSTRAP_PASSWORD 读取
//
// 环境变量：DATABASE_URL（必填）；serve 还需要 JWT_SECRET（至少 32 字节），
// 可选 HTTP_ADDR（默认 :8080）、TOKEN_TTL（默认 12h）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/httpapi"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

const usage = "usage: salon serve | salon openapi | salon migrate up|down|status | salon bootstrap -tenant ... -store ... -owner-name ... -owner-number ... -owner-account ..."

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
	if args[0] == "openapi" {
		return printOpenAPI()
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
		return serve(ctx, pool)
	case args[0] == "bootstrap":
		return bootstrap(ctx, pool, args[1:])
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

func serve(ctx context.Context, pool *pgxpool.Pool) error {
	ttl := 12 * time.Hour
	if v := os.Getenv("TOKEN_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("TOKEN_TTL: %w", err)
		}
		ttl = d
	}
	authn, err := auth.NewService(pool, []byte(os.Getenv("JWT_SECRET")), ttl)
	if err != nil {
		return err
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	e, _ := httpapi.New(pool, httpapi.Config{Authenticate: authn.Authenticate, Login: authn.Login})
	sc := echo.StartConfig{Address: addr, HideBanner: true}
	return sc.Start(ctx, e)
}

func bootstrap(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	var in staff.BootstrapInput
	fs.StringVar(&in.TenantName, "tenant", "", "租户名称")
	fs.StringVar(&in.StoreName, "store", "", "门店名称")
	fs.StringVar(&in.OwnerName, "owner-name", "", "老板姓名")
	fs.StringVar(&in.OwnerNumber, "owner-number", "", "老板的员工编号")
	fs.StringVar(&in.OwnerAccount, "owner-account", "", "老板的登录账号")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in.OwnerPassword = os.Getenv("BOOTSTRAP_PASSWORD")
	if in.TenantName == "" || in.StoreName == "" || in.OwnerName == "" || in.OwnerNumber == "" || in.OwnerAccount == "" {
		return errors.New(usage)
	}
	if len(in.OwnerPassword) < 8 {
		return errors.New("BOOTSTRAP_PASSWORD must be at least 8 characters")
	}
	res, err := staff.Bootstrap(ctx, pool, in)
	if err != nil {
		return err
	}
	fmt.Printf("tenant: %s\nstore: %s\nowner: %s\n", res.TenantID, res.StoreID, res.OwnerID)
	return nil
}

func printOpenAPI() error {
	e, _ := httpapi.New(nil, httpapi.Config{
		Authenticate: func(*http.Request) (ops.Caller, bool) { return ops.Caller{}, false },
		Login:        func(context.Context, string, string) (string, error) { return "", errors.New("unavailable") },
	})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rec.Code != http.StatusOK {
		return fmt.Errorf("openapi: status %d", rec.Code)
	}
	_, err := os.Stdout.Write(rec.Body.Bytes())
	return err
}
