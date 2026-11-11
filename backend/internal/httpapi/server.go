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

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/attendance"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/booking"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/collections"
	"github.com/wzhlinhai/salon/backend/internal/commission"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/dataio"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/debts"
	"github.com/wzhlinhai/salon/backend/internal/drafts"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/payroll"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"github.com/wzhlinhai/salon/backend/internal/redpackets"
	"github.com/wzhlinhai/salon/backend/internal/rotation"
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
	// Groupbuy 保存已配置的平台实现；未配置的平台拒绝操作。
	Groupbuy *groupbuy.Providers
	// Now 提供每次业务调用的时刻，nil 时使用真实时钟。
	Now          func() time.Time
	Authenticate Authenticator
	Login        LoginFunc
	// AIModel 返回 AI 对话使用的模型；返回 nil 时 /api/ai/chat 返回 503。
	AIModel func() ai.Model
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

	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	reg := ops.NewRegistry(pool, api, cfg.Now)
	model := cfg.AIModel
	if model == nil {
		model = func() ai.Model { return nil }
	}
	registerChat(api, ai.NewChat(reg, model, cfg.Now, func(ctx context.Context, caller ops.Caller, now time.Time) (time.Time, error) {
		var day time.Time
		err := db.InTenant(ctx, pool, caller.TenantID, func(q *dbgen.Queries) error {
			var err error
			day, err = settings.BusinessDate(ctx, q, caller.StoreID, now)
			return err
		})
		return day, err
	}))
	groupbuy.Register(reg, cfg.Groupbuy)
	stores.Register(reg)
	staff.Register(reg)
	catalog.Register(reg)
	orders.Register(reg)
	collections.Register(reg)
	performance.Register(reg)
	commission.Register(reg)
	payroll.Register(reg)
	attendance.Register(reg)
	rotation.Register(reg)
	booking.Register(reg)
	debts.Register(reg)
	redpackets.Register(reg)
	cards.Register(reg)
	customers.Register(reg)
	dataio.Register(reg)
	settings.Register(reg)
	audit.Register(reg)
	drafts.Register(reg)
	dataio.RegisterHTTP(e, pool, cfg.Now)
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
