package staff_test

import (
	"context"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

// waitLockResult 检查操作在有限等待内成功返回。
func waitLockResult(t *testing.T, done <-chan optest.Result) {
	t.Helper()
	select {
	case r := <-done:
		if r.Status != 200 {
			t.Fatalf("HTTP request: %d %v %v, want 200", r.Status, r.Body, r.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request timed out while KEY SHARE lock was held")
	}
}

// assertWaiting 检查操作在占锁事务释放前仍在等待。
func assertWaiting(t *testing.T, done <-chan optest.Result) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("request returned while transaction holds lock: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestStaffOperationLockLevels(t *testing.T) {
	for _, action := range []string{"deactivate_staff", "reactivate_staff", "set_staff_roles", "update_staff"} {
		t.Run(action, func(t *testing.T) {
			s := open(t)
			id, _ := s.Hire("员工", "002", "key-share-staff")
			args := map[string]any{"staff_id": id}
			switch action {
			case "reactivate_staff":
				s.call(s.Owner, "deactivate_staff", args, 200)
			case "set_staff_roles":
				args["role_ids"] = []any{s.Role("发型师")}
			case "update_staff":
				// 编号保持不变，确保等待来自显式锁，而不是 UPDATE 修改唯一列。
				args["name"], args["number"] = "新名", "002"
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if _, err := tx.Exec(context.Background(), "SELECT id FROM staff WHERE id=$1 FOR KEY SHARE", id); err != nil {
				t.Fatal(err)
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, action, args) }()
			if action == "update_staff" {
				dbtest.WaitForLockWait(t, s.Pool)
				assertWaiting(t, done)
				if err := tx.Rollback(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			waitLockResult(t, done)
		})
	}
}

func TestOwnerQueueLockAllowsKeyShare(t *testing.T) {
	s := open(t)
	id, _ := s.Hire("员工", "002", "owner-key-share")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SELECT id FROM roles WHERE id=$1 FOR KEY SHARE", s.Role("老板")); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "deactivate_staff", map[string]any{"staff_id": id})
	}()
	waitLockResult(t, done)
}
