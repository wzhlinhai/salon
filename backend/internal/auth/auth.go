// Package auth 负责员工登录：密码哈希、登录凭证，以及每次请求时读取当前权限（ADR-0009）。
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// ErrInvalidLogin：账号不存在、密码错误或员工已停用。三种情况不区分，避免泄露账号是否存在。
var ErrInvalidLogin = errors.New("invalid account or password")

// HashPassword 用 argon2id 哈希密码。
func HashPassword(password string) (string, error) {
	return argon2id.CreateHash(password, argon2id.DefaultParams)
}

// dummyHash 用于账号不存在时也做一次哈希比较，使响应时间与账号存在时一致。
var dummyHash, _ = HashPassword("dummy password for timing")

// Service 签发和校验登录凭证。
type Service struct {
	pool   *pgxpool.Pool
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewService 返回 Service。secret 至少 32 字节。
func NewService(pool *pgxpool.Pool, secret []byte, ttl time.Duration) (*Service, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must be at least 32 bytes")
	}
	return &Service{pool: pool, secret: secret, ttl: ttl, now: time.Now}, nil
}

type claims struct {
	TenantID uuid.UUID `json:"tid"`
	jwt.RegisteredClaims
}

// Login 校验账号密码，返回登录凭证。凭证里只有员工和租户，不含权限。
func (s *Service) Login(ctx context.Context, account, password string) (string, error) {
	var staffID, tenantID uuid.UUID
	var hash *string
	var active bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE salon_app"); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			"SELECT staff_id, tenant_id, password_hash, active FROM auth_find_login($1)", account,
		).Scan(&staffID, &tenantID, &hash, &active)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = argon2id.ComparePasswordAndHash(password, dummyHash)
		return "", ErrInvalidLogin
	}
	if err != nil {
		return "", fmt.Errorf("find login: %w", err)
	}
	if hash == nil {
		_, _ = argon2id.ComparePasswordAndHash(password, dummyHash)
		return "", ErrInvalidLogin
	}
	ok, err := argon2id.ComparePasswordAndHash(password, *hash)
	if err != nil {
		return "", fmt.Errorf("compare password: %w", err)
	}
	if !ok || !active {
		return "", ErrInvalidLogin
	}
	now := s.now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		TenantID: tenantID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   staffID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	})
	return tok.SignedString(s.secret)
}

// VerifyPassword 报告 password 是否是员工 staffID 的密码。q 须处于该员工租户的事务中。
func VerifyPassword(ctx context.Context, q *dbgen.Queries, staffID uuid.UUID, password string) (bool, error) {
	st, err := q.GetStaff(ctx, staffID)
	if err != nil {
		return false, err
	}
	if st.PasswordHash == nil {
		return false, nil
	}
	return argon2id.ComparePasswordAndHash(password, *st.PasswordHash)
}

func (s *Service) parse(token string) (staffID, tenantID uuid.UUID, err error) {
	var c claims
	_, err = jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return s.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	staffID, err = uuid.Parse(c.Subject)
	if err != nil || c.TenantID == uuid.Nil {
		return uuid.Nil, uuid.Nil, errors.New("invalid token subject")
	}
	return staffID, c.TenantID, nil
}

// Authenticate 从请求的 Bearer 凭证识别员工，并从数据库读取其当前权限。
// 员工已停用、凭证无效或过期时返回 false。
func (s *Service) Authenticate(r *http.Request) (ops.Caller, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ops.Caller{}, false
	}
	staffID, tenantID, err := s.parse(token)
	if err != nil {
		return ops.Caller{}, false
	}
	var caller ops.Caller
	err = db.InTenant(r.Context(), s.pool, tenantID, func(q *dbgen.Queries) error {
		var err error
		caller, ok, err = LoadCaller(r.Context(), q, staffID)
		return err
	})
	if err != nil || !ok {
		return ops.Caller{}, false
	}
	return caller, true
}

// LoadCaller 读取员工当前的角色，算出有效权限：各角色权限取并集，同一项权限的限制取最宽（ADR-0009）。
// 员工不存在或已停用时返回 false。q 须处于该员工租户的事务中。
func LoadCaller(ctx context.Context, q *dbgen.Queries, staffID uuid.UUID) (ops.Caller, bool, error) {
	st, err := q.GetStaff(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.Caller{}, false, nil
	}
	if err != nil {
		return ops.Caller{}, false, err
	}
	if !st.Active {
		return ops.Caller{}, false, nil
	}
	caller := ops.Caller{TenantID: st.TenantID, StoreID: st.StoreID, StaffID: st.ID}
	roles, err := q.ListStaffRoles(ctx, staffID)
	if err != nil {
		return ops.Caller{}, false, err
	}
	ids := make([]uuid.UUID, 0, len(roles))
	for _, r := range roles {
		if r.IsOwner {
			caller.Owner = true
		}
		ids = append(ids, r.ID)
	}
	if caller.Owner {
		caller.Permissions = perm.All()
		return caller, true, nil
	}
	rows, err := q.ListRolePermissions(ctx, ids)
	if err != nil {
		return ops.Caller{}, false, err
	}
	caller.Permissions = map[ops.Permission]ops.Grant{}
	for _, row := range rows {
		p := ops.Permission(row.Permission)
		if _, known := perm.Lookup(p); !known {
			continue
		}
		g := perm.Normalize(p, GrantFromRow(row))
		if prev, ok := caller.Permissions[p]; ok {
			g = prev.Widen(g)
		}
		caller.Permissions[p] = g
	}
	return caller, true, nil
}

// GrantFromRow 把一行角色权限转成 Grant。
func GrantFromRow(row dbgen.RolePermission) ops.Grant {
	g := ops.Grant{WindowDays: nil}
	if row.Scope != nil {
		g.Scope = ops.Scope(*row.Scope)
	}
	if row.WindowDays != nil {
		n := int(*row.WindowDays)
		g.WindowDays = &n
	}
	return g
}
