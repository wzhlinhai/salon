package staff_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
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

func TestOwnerQueueWaitDoesNotLockStaff(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	tx1 := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := dbgen.New(tx1).LockOwnerRole(ctx, s.Res.StoreID); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "update_staff", map[string]any{
			"staff_id": s.Res.OwnerID, "name": "老板新名", "number": "001",
		})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	tx2 := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx2.Exec(ctx, "SELECT id FROM staff WHERE id=$1 FOR KEY SHARE NOWAIT", s.Res.OwnerID); err != nil {
		t.Fatalf("owner queue wait must leave staff available for KEY SHARE: %v", err)
	}
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	waitLockResult(t, done)
}

func TestSetStaffRolesDoesNotRewriteKeptRole(t *testing.T) {
	s := open(t)
	roleID, addedID := s.Role("发型师"), s.Role("前台")
	staffID, _ := s.Hire("员工", "002", "kept-role", roleID)
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var lockedRole uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT role_id FROM staff_roles WHERE staff_id=$1 AND role_id=$2 FOR UPDATE", staffID, roleID).Scan(&lockedRole); err != nil {
		t.Fatal(err)
	}

	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "set_staff_roles", map[string]any{
			"staff_id": staffID, "role_ids": []uuid.UUID{roleID, addedID},
		})
	}()
	select {
	case r := <-done:
		if r.Status != http.StatusOK {
			t.Fatalf("set_staff_roles = %d %v, want 200", r.Status, r.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("set_staff_roles timed out while the kept staff_roles row was locked")
	}
	roles, err := dbgen.New(tx).ListStaffRoles(ctx, staffID)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 || roles[0].ID != roleID || roles[1].ID != addedID {
		t.Fatalf("staff roles = %v, want %s and %s", roles, roleID, addedID)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSetStaffRolesReturnsNotFoundWhenAddedRoleIsDeleted(t *testing.T) {
	s := open(t)
	roleID, keptID := s.Role("发型师"), s.Role("前台")
	staffID, _ := s.Hire("员工", "002", "deleted-role", keptID)
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := dbgen.New(tx).LockRole(ctx, dbgen.LockRoleParams{ID: roleID, StoreID: s.Res.StoreID}); err != nil {
		t.Fatal(err)
	}

	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "set_staff_roles", map[string]any{
			"staff_id": staffID, "role_ids": []uuid.UUID{roleID},
		})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	if result, err := tx.Exec(ctx, "DELETE FROM roles WHERE id=$1", roleID); err != nil || result.RowsAffected() != 1 {
		t.Fatalf("delete role = %v, %v, want one deleted row", result, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.Status != http.StatusNotFound {
			t.Fatalf("set_staff_roles = %d %v, want 404", r.Status, r.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("set_staff_roles timed out after role deletion committed")
	}
	check := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var roleIDs []uuid.UUID
	if err := check.QueryRow(ctx, "SELECT array_agg(role_id ORDER BY role_id) FROM staff_roles WHERE staff_id=$1", staffID).Scan(&roleIDs); err != nil {
		t.Fatal(err)
	}
	if len(roleIDs) != 1 || roleIDs[0] != keptID {
		t.Fatalf("staff roles = %v, want unchanged role %s", roleIDs, keptID)
	}
}

func TestUpdateRoleDoesNotRewriteKeptPermission(t *testing.T) {
	s := open(t)
	kept := map[string]any{"permission": "order.edit", "scope": "self", "window_days": 3}
	changed := map[string]any{"permission": "customer.view", "scope": "self"}
	role := s.call(s.Owner, "create_role", map[string]any{
		"name": "原角色", "permissions": []any{kept, changed},
	}, http.StatusOK).Body
	roleID := uuid.MustParse(role["id"].(string))
	ctx := context.Background()
	tx1 := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var lockedPermission string
	if err := tx1.QueryRow(ctx, "SELECT permission FROM role_permissions WHERE role_id=$1 AND permission=$2 FOR UPDATE", roleID, "order.edit").Scan(&lockedPermission); err != nil {
		t.Fatal(err)
	}
	before, err := dbgen.New(tx1).ListRolePermissions(ctx, []uuid.UUID{roleID})
	if err != nil {
		t.Fatal(err)
	}
	changed = map[string]any{"permission": "customer.view", "scope": "store"}
	added := map[string]any{"permission": "audit_log.view"}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "update_role", map[string]any{
			"role_id": roleID, "name": "改名角色", "permissions": []any{kept, changed, added},
		})
	}()
	select {
	case r := <-done:
		if r.Status != http.StatusOK || r.Body["name"] != "改名角色" {
			t.Fatalf("update_role = %d %v, want 200 and renamed role", r.Status, r.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update_role timed out while the kept role_permissions row was locked")
	}
	rows, err := dbgen.New(tx1).ListRolePermissions(ctx, []uuid.UUID{roleID})
	if err != nil {
		t.Fatal(err)
	}
	want := []dbgen.RolePermission{
		{TenantID: s.Res.TenantID, StoreID: s.Res.StoreID, RoleID: roleID, Permission: "audit_log.view"},
		{TenantID: s.Res.TenantID, StoreID: s.Res.StoreID, RoleID: roleID, Permission: "customer.view", Scope: new("store")},
		before[1],
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("role permissions = %v, want %v", rows, want)
	}
	if err := tx1.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRolePermissionChanges(t *testing.T) {
	s := open(t)
	kept := map[string]any{"permission": "audit_log.view"}
	initial := []any{map[string]any{"permission": "order.edit", "scope": "self", "window_days": 3.0}, kept}
	role := s.call(s.Owner, "create_role", map[string]any{"name": "原角色", "permissions": initial}, http.StatusOK).Body
	for _, tc := range []struct {
		name        string
		permissions []any
	}{
		{"只改名", initial},
		{"只改范围", []any{map[string]any{"permission": "order.edit", "scope": "store", "window_days": 3.0}, kept}},
		{"只改天数", []any{map[string]any{"permission": "order.edit", "scope": "store", "window_days": 0.0}, kept}},
		{"不限天数", []any{map[string]any{"permission": "order.edit", "scope": "store"}, kept}},
		{"恢复天数", initial},
		{"收回权限", []any{kept}},
	} {
		s.call(s.Owner, "update_role", map[string]any{"role_id": role["id"], "name": tc.name, "permissions": tc.permissions}, http.StatusOK)
		roles := s.call(s.Owner, "list_roles", map[string]any{}, http.StatusOK).Body["roles"].([]any)
		var got map[string]any
		for _, item := range roles {
			if r := item.(map[string]any); r["id"] == role["id"] {
				got = r
			}
		}

		if got["name"] != tc.name || !reflect.DeepEqual(got["permissions"], tc.permissions) {
			t.Fatalf("%s: role = %v, want name %s and permissions %v", tc.name, got, tc.name, tc.permissions)
		}
	}
}
