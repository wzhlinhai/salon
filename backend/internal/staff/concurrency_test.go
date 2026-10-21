package staff_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

// pausedQuery 在指定 SQL 执行前暂停一次，让测试确定另一操作读到的是哪一版数据。
type pausedQuery struct {
	prefix string
	once   sync.Once
	paused chan struct{}
	resume chan struct{}
}

func (p *pausedQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, p.prefix) {
		p.once.Do(func() { close(p.paused); <-p.resume })
	}
	return ctx
}

func (*pausedQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func pauseApp(t *testing.T, s *shop, prefix string) (*optest.App, <-chan struct{}, func()) {
	t.Helper()
	tracer := &pausedQuery{prefix: prefix, paused: make(chan struct{}), resume: make(chan struct{})}
	cfg := s.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	release := sync.OnceFunc(func() { close(tracer.resume) })
	t.Cleanup(release)
	return optest.New(t, pool), tracer.paused, release
}

func awaitPaused(t *testing.T, paused <-chan struct{}) {
	t.Helper()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("query did not reach the pause point")
	}
}

func staffResult(t *testing.T, done <-chan optest.Result, want int) optest.Result {
	t.Helper()
	select {
	case r := <-done:
		if r.Status != want {
			t.Fatalf("operation = %d %v (%v), want %d", r.Status, r.Body, r.Err, want)
		}
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not finish")
	}
	return optest.Result{}
}

func TestRoleEscalationSerializesStaffAuthorization(t *testing.T) {
	for _, action := range []string{"set_staff_roles", "create_staff", "set_staff_login"} {
		t.Run(action, func(t *testing.T) {
			s := open(t)
			_, manager := s.Hire("店长", "002", "manager", s.Role("店长"))
			role := s.call(s.Owner, "create_role", map[string]any{"name": "空角色", "permissions": []any{}}, 200).Body
			roleID := uuid.MustParse(role["id"].(string))
			victim, _ := s.Hire("员工", "003", "victim", roleID)
			app, paused, release := pauseApp(t, s, "-- name: RenameRole :")
			owner := s.MustCaller(s.Owner)
			updated := make(chan optest.Result, 1)
			go func() {
				updated <- app.HTTP(t, &owner, "update_role", map[string]any{
					"role_id": roleID, "name": "角色管理", "permissions": []any{map[string]any{"permission": "role.manage"}},
				})
			}()
			awaitPaused(t, paused)
			args := map[string]any{"staff_id": victim, "role_ids": []uuid.UUID{roleID, s.Role("前台")}}
			if action == "create_staff" {
				args = map[string]any{"name": "越权新员工", "number": "004", "role_ids": []uuid.UUID{roleID}}
			}
			if action == "set_staff_login" {
				args = map[string]any{"staff_id": victim, "login_account": "taken-over", "password": "taken-over-password"}
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, manager, action, args) }()
			dbtest.WaitForLockWait(t, s.Pool)
			assertWaiting(t, done)
			release()
			staffResult(t, updated, http.StatusOK)
			staffResult(t, done, http.StatusForbidden)
			var count int
			if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM staff WHERE number='004' OR login_account='taken-over'").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("rejected operation changed staff")
			}
			roles := s.call(s.Owner, "list_staff", map[string]any{}, 200).Body["staff"].([]any)
			for _, row := range roles {
				st := row.(map[string]any)
				if st["id"] == victim.String() && len(st["roles"].([]any)) != 1 {
					t.Fatal("rejected assignment changed staff roles")
				}
			}
		})
	}
}

func TestInactiveLevelAndPositionWaitBeforeAssignment(t *testing.T) {
	for _, kind := range []string{"level", "position"} {
		for _, action := range []string{"create_staff", "update_staff"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				s := open(t)
				option := s.call(s.Owner, "create_"+kind, map[string]any{"name": "待停用"}, 200).Body
				id := uuid.MustParse(option["id"].(string))
				args := map[string]any{"name": "员工", "number": "002", kind + "_id": id}
				if action == "create_staff" {
					args["role_ids"] = []uuid.UUID{}
				} else {
					staffID, _ := s.Hire("原名", "002", "employee")
					args["staff_id"] = staffID
				}
				ctx := context.Background()
				tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
				q := dbgen.New(tx)
				if kind == "level" {
					if _, err := q.UpdateLevel(ctx, dbgen.UpdateLevelParams{ID: id, Name: "待停用", Active: false}); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := q.UpdatePosition(ctx, dbgen.UpdatePositionParams{ID: id, Name: "待停用", Active: false}); err != nil {
						t.Fatal(err)
					}
				}
				done := make(chan optest.Result, 1)
				go func() { done <- s.HTTPWithToken(t, s.Owner, action, args) }()
				dbtest.WaitForLockWait(t, s.Pool)
				assertWaiting(t, done)
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				staffResult(t, done, http.StatusConflict)
				rows := s.call(s.Owner, "list_staff", map[string]any{}, 200).Body["staff"].([]any)
				for _, row := range rows {
					st := row.(map[string]any)
					if st["number"] == "002" && (action == "create_staff" || st[kind] != nil || st["name"] != "原名") {
						t.Fatalf("rejected assignment changed staff: %v", st)
					}
				}
			})
		}
	}
}

func TestListRolesUsesOneRoleCollection(t *testing.T) {
	for _, channel := range []string{"http", "ai"} {
		t.Run(channel, func(t *testing.T) {
			s := open(t)
			app, paused, release := pauseApp(t, s, "-- name: ListRolePermissions :")
			caller := s.MustCaller(s.Owner)
			var call optest.Channel
			for _, ch := range app.Channels() {
				if ch.Name == channel {
					call = ch
				}
			}
			done := make(chan optest.Result, 1)
			go func() {
				// AI 没有 HTTP Recover；把 panic 交回测试断言，不让它终止整个测试进程。
				defer func() {
					if value := recover(); value != nil {
						done <- optest.Result{Err: fmt.Errorf("list_roles panicked: %v", value)}
					}
				}()
				done <- call.Call(t, caller, "list_roles", map[string]any{})
			}()
			awaitPaused(t, paused)
			s.call(s.Owner, "create_role", map[string]any{"name": "并发新增", "permissions": []any{}}, 200)
			release()
			result := staffResult(t, done, http.StatusOK)
			roles := result.Body["roles"].([]any)
			if len(roles) != 5 {
				t.Fatalf("roles = %v, want original five roles", roles)
			}
			for i, name := range []string{"老板", "店长", "发型师", "助理", "前台"} {
				if roles[i].(map[string]any)["name"] != name {
					t.Fatalf("role order: %v", roles)
				}
			}
		})
	}
}
