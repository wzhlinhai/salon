package optest

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// OwnerPassword 是 OpenShop 建的老板账号（boss）的密码。
const OwnerPassword = "owner-password"

// Shop 是一家用 staff.Bootstrap 开好的店：本店租户、门店、老板角色、四个默认角色和老板王老板（编号 001）。
type Shop struct {
	*App
	t    testing.TB
	Pool *pgxpool.Pool
	Res  staff.BootstrapResult
	// Owner 是老板的登录凭证。
	Owner string
}

// OpenShop 在一个全新的数据库里开一家店。
func OpenShop(t testing.TB) *Shop {
	t.Helper()
	pool := dbtest.NewPool(t)
	res, err := staff.Bootstrap(context.Background(), pool, staff.BootstrapInput{
		TenantName: "本店", StoreName: "本店", OwnerName: "王老板", OwnerNumber: "001",
		OwnerAccount: "boss", OwnerPassword: OwnerPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Shop{App: New(t, pool), t: t, Pool: pool, Res: res}
	s.Owner = s.MustLogin("boss", OwnerPassword)
	return s
}

// MustLogin 登录并返回登录凭证，登录失败时测试失败。
func (s *Shop) MustLogin(account, password string) string {
	s.t.Helper()
	code, token := s.Login(s.t, account, password)
	if code != http.StatusOK {
		s.t.Fatalf("login %s: %d", account, code)
	}
	return token
}

// Role 返回默认角色或老板角色的 ID。
func (s *Shop) Role(name string) uuid.UUID { return s.Res.Roles[name] }

// Hire 由老板建一名有登录账号的员工（密码为 "password-" + 账号），返回员工 ID 和登录凭证。
func (s *Shop) Hire(name, number, account string, roles ...uuid.UUID) (uuid.UUID, string) {
	s.t.Helper()
	if roles == nil {
		roles = []uuid.UUID{}
	}
	r := s.HTTPWithToken(s.t, s.Owner, "create_staff", map[string]any{"name": name, "number": number, "role_ids": roles})
	if r.Status != http.StatusOK {
		s.t.Fatalf("create_staff: %d %v", r.Status, r.Body)
	}
	id := uuid.MustParse(r.Body["id"].(string))
	password := "password-" + account
	r = s.HTTPWithToken(s.t, s.Owner, "set_staff_login", map[string]any{"staff_id": id, "login_account": account, "password": password})
	if r.Status != http.StatusOK {
		s.t.Fatalf("set_staff_login: %d %v", r.Status, r.Body)
	}
	return id, s.MustLogin(account, password)
}

// MustCaller 返回登录凭证对应的员工本人的调用者，HTTP 和 AI 两条路径都可以用它。
func (s *Shop) MustCaller(token string) ops.Caller {
	s.t.Helper()
	c, ok := s.CallerFor(s.t, token)
	if !ok {
		s.t.Fatal("token does not authenticate")
	}
	return c
}
