// Package miniprogram 实现顾客微信登录和顾客本人可调用的只读业务操作。
package miniprogram

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/privacy"
	"github.com/wzhlinhai/salon/backend/internal/wechat"
)

var (
	// ErrDisabled 表示没有配置微信登录实现。
	ErrDisabled = errors.New("小程序登录未启用")
	// ErrStoreNotFound 表示门店不存在；与权限错误一样不透露更多信息。
	ErrStoreNotFound = errors.New("门店不存在")
	// ErrWechatLoginFailed 表示微信登录 code 或手机号 code 无效。
	ErrWechatLoginFailed = errors.New("微信登录失败")
)

// LoginCustomer 是登录返回的顾客本人资料。
type LoginCustomer struct {
	ID          uuid.UUID `json:"id"`
	Name        *string   `json:"name" nullable:"true"`
	Phone       *string   `json:"phone" nullable:"true" doc:"按小程序规则打码"`
	PhoneMasked bool      `json:"phone_masked"`
}

// LoginResult 是顾客登录结果；需要手机号时没有凭证。
type LoginResult struct {
	Token     string         `json:"token,omitempty"`
	Customer  *LoginCustomer `json:"customer,omitempty"`
	NeedPhone bool           `json:"need_phone"`
}

// LoginService 处理顾客微信登录、手机号关联和登录绑定。
type LoginService struct {
	pool     *pgxpool.Pool
	tokens   *auth.Service
	provider wechat.Provider
}

// NewLoginService 返回顾客登录服务；provider 为空时登录返回 ErrDisabled。
func NewLoginService(pool *pgxpool.Pool, tokens *auth.Service, provider wechat.Provider) *LoginService {
	return &LoginService{pool: pool, tokens: tokens, provider: provider}
}

// Login 用微信 code 登录；没有绑定时用手机号 code 查找或新建顾客档案。
func (s *LoginService) Login(ctx context.Context, storeID uuid.UUID, code, phoneCode string) (LoginResult, error) {
	if s.provider == nil {
		return LoginResult{}, ErrDisabled
	}
	tenantID, err := s.tenantForStore(ctx, storeID)
	if err != nil {
		return LoginResult{}, err
	}
	session, err := s.provider.Session(ctx, code)
	if err != nil {
		return LoginResult{}, ErrWechatLoginFailed
	}
	if result, found, err := s.existingLogin(ctx, tenantID, storeID, session); err != nil || found {
		return result, err
	}
	if phoneCode == "" {
		return LoginResult{NeedPhone: true}, nil
	}
	phone, err := s.provider.PhoneNumber(ctx, phoneCode)
	if err != nil {
		return LoginResult{}, ErrWechatLoginFailed
	}
	return s.bind(ctx, tenantID, storeID, session, phone)
}

func (s *LoginService) tenantForStore(ctx context.Context, storeID uuid.UUID) (uuid.UUID, error) {
	var tenantID uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE salon_app"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT tenant_id FROM customer_login_store($1)", storeID).Scan(&tenantID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrStoreNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("find store: %w", err)
	}
	return tenantID, nil
}

func (s *LoginService) existingLogin(ctx context.Context, tenantID, storeID uuid.UUID, session wechat.Session) (LoginResult, bool, error) {
	var result LoginResult
	found := false
	err := db.InTenant(ctx, s.pool, tenantID, func(q *dbgen.Queries) error {
		login, err := q.GetCustomerLoginByOpenID(ctx, dbgen.GetCustomerLoginByOpenIDParams{StoreID: storeID, Openid: session.OpenID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		result, err = s.resultForLogin(ctx, q, login, session)
		return err
	})
	return result, found, err
}

func (s *LoginService) bind(ctx context.Context, tenantID, storeID uuid.UUID, session wechat.Session, phone string) (LoginResult, error) {
	var result LoginResult
	err := db.InTenant(ctx, s.pool, tenantID, func(q *dbgen.Queries) error {
		if err := q.LockCustomerLogin(ctx, dbgen.LockCustomerLoginParams{StoreID: storeID.String(), Openid: session.OpenID}); err != nil {
			return err
		}
		if login, err := q.GetCustomerLoginByOpenID(ctx, dbgen.GetCustomerLoginByOpenIDParams{StoreID: storeID, Openid: session.OpenID}); err == nil {
			var err error
			result, err = s.resultForLogin(ctx, q, login, session)
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		env := ops.Env{Caller: ops.Caller{TenantID: tenantID, StoreID: storeID}, Q: q}
		customer, _, err := customers.FindOrCreateForCustomerLogin(ctx, env, phone)
		if err != nil {
			return err
		}
		if err := ensureLoginStore(customer, storeID); err != nil {
			return err
		}
		var unionID *string
		if session.UnionID != "" {
			unionID = &session.UnionID
		}
		login, err := q.CreateCustomerLogin(ctx, dbgen.CreateCustomerLoginParams{
			TenantID: tenantID, StoreID: storeID, Openid: session.OpenID, Unionid: unionID, CustomerID: customer.ID, LastLoginAt: timestamptz(s.tokens.Now()),
		})
		if err != nil {
			return err
		}
		env.Caller.CustomerID = &customer.ID
		if err := audit.Record(ctx, env, "bind_wechat_login", audit.CustomerTarget(customer.ID), nil, map[string]any{"via": "miniprogram"}); err != nil {
			return err
		}
		token, err := s.tokens.IssueCustomerToken(login.TenantID, login.ID, login.TokenVersion)
		if err != nil {
			return err
		}
		result = LoginResult{Token: token, Customer: loginCustomer(customer)}
		return nil
	})
	return result, err
}

func (s *LoginService) resultForLogin(ctx context.Context, q *dbgen.Queries, login dbgen.CustomerLogin, session wechat.Session) (LoginResult, error) {
	customer, err := customers.SurvivingCustomer(ctx, q, login.CustomerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, ErrWechatLoginFailed
	}
	if err != nil {
		return LoginResult{}, err
	}
	if customer.StoreID != login.StoreID {
		return LoginResult{}, ErrWechatLoginFailed
	}
	if customer.ID != login.CustomerID {
		if err := q.MoveCustomerLoginCustomer(ctx, dbgen.MoveCustomerLoginCustomerParams{StoreID: login.StoreID, ID: login.ID, CustomerID: customer.ID}); err != nil {
			return LoginResult{}, err
		}
	}
	var unionID *string
	if session.UnionID != "" {
		unionID = &session.UnionID
	}
	if err := q.TouchCustomerLogin(ctx, dbgen.TouchCustomerLoginParams{StoreID: login.StoreID, ID: login.ID, Unionid: unionID, LastLoginAt: timestamptz(s.tokens.Now())}); err != nil {
		return LoginResult{}, err
	}
	token, err := s.tokens.IssueCustomerToken(login.TenantID, login.ID, login.TokenVersion)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, Customer: loginCustomer(customer)}, nil
}

func loginCustomer(row dbgen.Customer) *LoginCustomer {
	out := &LoginCustomer{ID: row.ID, Name: row.Name, Phone: row.Phone}
	if row.Phone != nil {
		masked := privacy.MaskPhone(*row.Phone)
		out.Phone = &masked
		out.PhoneMasked = true
	}
	return out
}

func ensureLoginStore(customer dbgen.Customer, storeID uuid.UUID) error {
	if customer.StoreID != storeID {
		return ops.Rule("该顾客属于其他门店，不能登录")
	}
	return nil
}

func timestamptz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
