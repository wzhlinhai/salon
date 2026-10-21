package staff_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestResetPasswordRevokesTokens(t *testing.T) {
	s := open(t)
	id, token := s.Hire("小李", "002", "xiaoli")
	s.call(token, "get_me", map[string]any{}, http.StatusOK)
	s.call(s.Owner, "set_staff_login", map[string]any{"staff_id": id, "login_account": "xiaoli", "password": "new-password"}, http.StatusOK)
	s.call(token, "get_me", map[string]any{}, http.StatusUnauthorized)
	s.call(s.MustLogin("xiaoli", "new-password"), "get_me", map[string]any{}, http.StatusOK)
	s.call(s.Owner, "get_me", map[string]any{}, http.StatusOK)
}

func TestTokenWithoutVersionRejected(t *testing.T) {
	s := open(t)
	// 老板的初始版本为 0，缺字段不能当成版本 0。
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": s.Res.OwnerID.String(), "tid": s.Res.TenantID.String(),
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("test secret that is at least 32 bytes long"))
	if err != nil {
		t.Fatal(err)
	}
	s.call(token, "get_me", map[string]any{}, http.StatusUnauthorized)
	s.call(s.Owner, "get_me", map[string]any{}, http.StatusOK)
}

func wrongLogins(t *testing.T, s *shop, count int) {
	t.Helper()
	for range count {
		if code, _ := s.Login(t, "xiaoli", "wrong-password"); code != http.StatusUnauthorized {
			t.Fatalf("wrong password: got %d, want 401", code)
		}
	}
}

func TestSuccessfulLoginResetsFailures(t *testing.T) {
	s := open(t)
	s.Hire("小李", "002", "xiaoli")
	for range 2 {
		wrongLogins(t, s, 14)
		s.call(s.MustLogin("xiaoli", "password-xiaoli"), "get_me", map[string]any{}, http.StatusOK)
	}
}

func assertLocked(t *testing.T, s *shop, id uuid.UUID) {
	t.Helper()
	var failures int
	var remaining int64
	var lockedUntil time.Time
	if err := s.Pool.QueryRow(context.Background(),
		"SELECT failed_logins, extract(epoch FROM (locked_until - clock_timestamp()))::bigint, locked_until FROM staff WHERE id = $1", id,
	).Scan(&failures, &remaining, &lockedUntil); err != nil {
		t.Fatal(err)
	}
	if failures != 0 || remaining < 14*60 || remaining > 15*60 {
		t.Fatalf("locked state: failures=%d remaining=%d seconds, want 0 and 14–15 minutes", failures, remaining)
	}
	for _, password := range []string{"password-xiaoli", "wrong-password"} {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"account":"xiaoli","password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Handler.ServeHTTP(rec, req)
		var body struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusTooManyRequests || body.Detail != "登录失败次数过多，请 15 分钟后再试" {
			t.Fatalf("locked login: %d %s", rec.Code, rec.Body.String())
		}
	}
	var unchanged bool
	var after time.Time
	if err := s.Pool.QueryRow(context.Background(),
		"SELECT failed_logins = 0, locked_until FROM staff WHERE id = $1", id,
	).Scan(&unchanged, &after); err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Fatal("locked attempts changed failure count")
	}
	if !after.Equal(lockedUntil) {
		t.Fatalf("locked attempts changed deadline: before=%s after=%s", lockedUntil, after)
	}
}

func TestLoginLocksAfter15FailuresAndExpires(t *testing.T) {
	s := open(t)
	id, _ := s.Hire("小李", "002", "xiaoli")
	wrongLogins(t, s, 15)
	assertLocked(t, s, id)
	if _, err := s.Pool.Exec(context.Background(), "UPDATE staff SET locked_until = now() - interval '1 second' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	s.call(s.MustLogin("xiaoli", "password-xiaoli"), "get_me", map[string]any{}, http.StatusOK)
}

func TestResetPasswordUnlocksLogin(t *testing.T) {
	s := open(t)
	id, token := s.Hire("小李", "002", "xiaoli")
	wrongLogins(t, s, 15)
	assertLocked(t, s, id)
	s.call(s.Owner, "set_staff_login", map[string]any{"staff_id": id, "login_account": "xiaoli", "password": "new-password"}, http.StatusOK)
	s.call(token, "get_me", map[string]any{}, http.StatusUnauthorized)
	s.call(s.MustLogin("xiaoli", "new-password"), "get_me", map[string]any{}, http.StatusOK)
	wrongLogins(t, s, 14)
	s.MustLogin("xiaoli", "new-password")
}

func TestConcurrentFailedLoginsLockAccount(t *testing.T) {
	s := open(t)
	id, _ := s.Hire("小李", "002", "xiaoli")
	start := make(chan struct{})
	codes := make(chan int, 15)
	for range 15 {
		go func() {
			<-start
			code, _ := s.Login(t, "xiaoli", "wrong-password")
			codes <- code
		}()
	}
	close(start)
	for range 15 {
		if code := <-codes; code != http.StatusUnauthorized {
			t.Errorf("concurrent failure: got %d, want 401", code)
		}
	}
	assertLocked(t, s, id)
}

func TestResetPasswordClearsPendingFailures(t *testing.T) {
	s := open(t)
	id, _ := s.Hire("小李", "002", "xiaoli")
	wrongLogins(t, s, 14)
	s.call(s.Owner, "set_staff_login", map[string]any{"staff_id": id, "login_account": "xiaoli", "password": "new-password"}, http.StatusOK)
	wrongLogins(t, s, 14)
	s.MustLogin("xiaoli", "new-password")
}

func TestPasswordChecksReadAfterLock(t *testing.T) {
	for _, path := range []string{"login", "change_my_password"} {
		t.Run(path, func(t *testing.T) {
			s := open(t)
			id, token := s.Hire("小李", "002", "xiaoli")
			hash, err := auth.HashPassword("reset-password")
			if err != nil {
				t.Fatal(err)
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if _, err := tx.Exec(context.Background(), "UPDATE staff SET password_hash = $2, token_version = token_version + 1 WHERE id = $1", id, hash); err != nil {
				t.Fatal(err)
			}
			codes := make(chan int, 1)
			go func() {
				if path == "login" {
					code, _ := s.Login(t, "xiaoli", "password-xiaoli")
					codes <- code
				} else {
					codes <- s.HTTPWithToken(t, token, "change_my_password", map[string]any{"old_password": "password-xiaoli", "new_password": "overwritten-password"}).Status
				}
			}()
			dbtest.WaitForLockWait(t, s.Pool)
			if err := tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := http.StatusUnauthorized
			if path == "change_my_password" {
				want = http.StatusConflict
			}
			if code := <-codes; code != want {
				t.Fatalf("password check after reset: got %d, want %d", code, want)
			}
			s.MustLogin("xiaoli", "reset-password")
		})
	}
}

func TestLoginLockAllowsAuditActorForeignKey(t *testing.T) {
	s := open(t)
	id, _ := s.Hire("小李", "002", "xiaoli")
	ctx := context.Background()
	locked := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := dbgen.New(locked).LockStaffNoKey(ctx, id); err != nil {
		t.Fatal(err)
	}
	insert := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := insert.Exec(ctx, "SET LOCAL lock_timeout = '2s'"); err != nil {
		t.Fatal(err)
	}
	if _, err := insert.Exec(ctx,
		"INSERT INTO audit_log (tenant_id, store_id, action, target_id, actor_id, target_kind) VALUES ($1, $2, 'change_my_password', $3, $3, 'staff')",
		s.Res.TenantID, s.Res.StoreID, id); err != nil {
		t.Fatalf("insert audit actor while login holds lock: %v", err)
	}
	if err := insert.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLoginLockUsesDatabaseClock(t *testing.T) {
	s := open(t)
	id, _ := s.Hire("小李", "002", "xiaoli")
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	var before time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordFailedLogin(ctx, dbgen.RecordFailedLoginParams{
		ID: id, Threshold: 1, LockDuration: pgtype.Interval{Microseconds: (15 * time.Minute).Microseconds(), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	row, err := q.LockStaffNoKey(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !row.Locked || !row.Staff.LockedUntil.Valid || row.Staff.LockedUntil.Time.Before(before.Add(15*time.Minute)) {
		t.Fatalf("lock deadline must follow actual database time: before=%s row=%+v", before, row)
	}
	if _, err := tx.Exec(ctx, "UPDATE staff SET locked_until = clock_timestamp() WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	row, err = q.LockStaffNoKey(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Locked {
		t.Fatal("expired deadline is still locked when transaction start time is earlier")
	}
}
