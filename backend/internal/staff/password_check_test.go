package staff_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestChangeMyPasswordSharedLockout(t *testing.T) {
	s := open(t)
	employee, token := s.Hire("员工", "002", "shared")
	c := s.MustCaller(token)
	in := map[string]any{"old_password": "wrong", "new_password": "changed-password"}
	r := s.HTTP(t, &c, "change_my_password", in)
	if r.Status != 409 || r.Body["detail"] != "原密码不正确" {
		t.Fatal(r)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	err := tx.QueryRow(context.Background(), "SELECT failed_logins FROM staff WHERE id=$1", employee).Scan(&n)
	dbtest.Rollback(t, tx)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for range 14 {
		if code, _ := s.Login(t, "shared", "wrong"); code != 401 {
			t.Fatal(code)
		}
	}
	in["old_password"] = "password-shared"
	r = s.HTTP(t, &c, "change_my_password", in)
	if r.Status != 409 || r.Body["detail"] != "密码错误次数过多，请 15 分钟后再试" {
		t.Fatal(r)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE staff SET locked_until=NULL WHERE id=$1", employee)
	dbtest.Commit(t, tx)
	s.MustLogin("shared", "password-shared")
	if code, _ := s.Login(t, "shared", "changed-password"); code != 401 {
		t.Fatal("locked account changed password", code)
	}
}
