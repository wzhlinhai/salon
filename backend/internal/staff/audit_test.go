package staff_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestConcurrentStaffUpdatesKeepAuditBefore(t *testing.T) {
	for _, tc := range []struct {
		action string
		args   map[string]any
	}{
		{"update_staff", map[string]any{"name": "请求改名", "number": "002"}},
		{"set_staff_login", map[string]any{"login_account": "audit-renamed", "password": "audit-new-password"}},
		{"deactivate_staff", nil},
		{"reactivate_staff", nil},
		{"set_staff_roles", nil},
	} {
		t.Run(tc.action, func(t *testing.T) {
			s := open(t)
			staffID, _ := s.Hire("原名", "002", "audit-staff")
			if tc.action == "reactivate_staff" {
				s.call(s.Owner, "deactivate_staff", map[string]any{"staff_id": staffID}, http.StatusOK)
			}
			args := map[string]any{"staff_id": staffID}
			for key, value := range tc.args {
				args[key] = value
			}
			if tc.action == "set_staff_roles" {
				args["role_ids"] = []uuid.UUID{s.Role("发型师")}
			}
			ctx := context.Background()
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			q := dbgen.New(tx)
			if _, err := q.LockStaff(ctx, staffID); err != nil {
				t.Fatal(err)
			}
			if _, err := q.UpdateStaffProfile(ctx, dbgen.UpdateStaffProfileParams{ID: staffID, Name: "事务改名", Number: "002"}); err != nil {
				t.Fatal(err)
			}

			done := make(chan optest.Result, 1)
			go func() {
				done <- s.HTTPWithToken(t, s.Owner, tc.action, args)
			}()
			dbtest.WaitForLockWait(t, s.Pool)
			select {
			case r := <-done:
				t.Fatalf("%s finished with %d while the staff row was locked; want it to wait", tc.action, r.Status)
			default:
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case r := <-done:
				if r.Status != http.StatusOK {
					t.Fatalf("%s = %d %v, want 200", tc.action, r.Status, r.Body)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("HTTP request did not finish within 10 seconds after commit")
			}
			entries := s.call(s.Owner, "list_audit_log", map[string]any{"target_id": staffID}, http.StatusOK).Body["entries"].([]any)
			if len(entries) == 0 {
				t.Fatal("missing staff audit log")
			}
			latest := entries[0].(map[string]any)
			if latest["action"] != tc.action || latest["before"].(map[string]any)["name"] != "事务改名" {
				t.Fatalf("latest audit entry = %v, want %s with before.name 事务改名", latest, tc.action)
			}
		})
	}
}

func TestConcurrentRoleUpdatesKeepAuditBefore(t *testing.T) {
	s := open(t)
	role := s.call(s.Owner, "create_role", map[string]any{"name": "原名", "permissions": []any{}}, http.StatusOK).Body
	roleID := uuid.MustParse(role["id"].(string))
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	if _, err := q.LockRole(ctx, dbgen.LockRoleParams{ID: roleID, StoreID: s.Res.StoreID}); err != nil {
		t.Fatal(err)
	}
	if n, err := q.RenameRole(ctx, dbgen.RenameRoleParams{ID: roleID, Name: "事务改名"}); err != nil || n != 1 {
		t.Fatalf("RenameRole = %d, %v, want 1, nil", n, err)
	}

	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "update_role", map[string]any{"role_id": roleID, "name": "请求改名", "permissions": []any{}})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	select {
	case r := <-done:
		t.Fatalf("update_role finished with %d while the role row was locked; want it to wait", r.Status)
	default:
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.Status != http.StatusOK {
			t.Fatalf("update_role = %d %v, want 200", r.Status, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP request did not finish within 10 seconds after commit")
	}
	entries := s.call(s.Owner, "list_audit_log", map[string]any{"target_id": roleID}, http.StatusOK).Body["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("missing role audit log")
	}
	latest := entries[0].(map[string]any)
	if latest["action"] != "update_role" || latest["before"].(map[string]any)["name"] != "事务改名" {
		t.Fatalf("latest audit entry = %v, want update_role with before.name 事务改名", latest)
	}
}

func TestConcurrentRoleAssignmentAndDeletionKeepAuditHolders(t *testing.T) {
	s := open(t)
	role := s.call(s.Owner, "create_role", map[string]any{"name": "待删除角色", "permissions": []any{}}, http.StatusOK).Body
	roleID := uuid.MustParse(role["id"].(string))
	staffID, _ := s.Hire("新持有人", "002", "audit-holder")
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	if _, err := q.LockRole(ctx, dbgen.LockRoleParams{ID: roleID, StoreID: s.Res.StoreID}); err != nil {
		t.Fatal(err)
	}
	if err := q.AddStaffRole(ctx, dbgen.AddStaffRoleParams{
		TenantID: s.Res.TenantID, StoreID: s.Res.StoreID, StaffID: staffID, RoleID: roleID,
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "delete_role", map[string]any{"role_id": roleID})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	select {
	case r := <-done:
		t.Fatalf("delete_role finished with %d while the role row was locked; want it to wait", r.Status)
	default:
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.Status != http.StatusOK {
			t.Fatalf("delete_role = %d %v, want 200", r.Status, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP request did not finish within 10 seconds after commit")
	}
	entries := s.call(s.Owner, "list_audit_log", map[string]any{"target_id": roleID}, http.StatusOK).Body["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("missing role audit log")
	}
	latest := entries[0].(map[string]any)
	if latest["action"] != "delete_role" {
		t.Fatalf("latest audit entry = %v, want delete_role", latest)
	}
	holders := latest["before"].(map[string]any)["holders"]
	want := []any{map[string]any{"id": staffID.String(), "name": "新持有人"}}
	if !reflect.DeepEqual(holders, want) {
		t.Fatalf("before.holders = %v, want %v", holders, want)
	}
}

func TestStaffAndRoleAuditLog(t *testing.T) {
	s := open(t)
	me := s.call(s.Owner, "get_me", map[string]any{}, http.StatusOK).Body
	caller, ok := s.CallerFor(t, s.Owner)
	if !ok {
		t.Fatal("owner not authenticated")
	}
	type change struct {
		action        string
		target        string
		before, after any
	}
	var expected []change
	record := func(action, target string, before, after any) {
		expected = append(expected, change{action, target, before, after})
	}
	role := s.call(s.Owner, "create_role", map[string]any{"name": "审计前", "permissions": []any{map[string]any{"permission": "staff.manage"}}}, http.StatusOK).Body
	roleID := role["id"].(string)
	record("create_role", roleID, nil, role)
	updatedRole := s.call(s.Owner, "update_role", map[string]any{"role_id": roleID, "name": "审计后", "permissions": []any{map[string]any{"permission": "audit_log.view"}}}, http.StatusOK).Body
	record("update_role", roleID, role, updatedRole)
	st := s.call(s.Owner, "create_staff", map[string]any{"name": "小李", "number": "002", "role_ids": []uuid.UUID{s.Role("发型师")}}, http.StatusOK).Body
	staffID := st["id"].(string)
	record("create_staff", staffID, nil, st)
	updated := s.call(s.Owner, "update_staff", map[string]any{"staff_id": staffID, "name": "李师傅", "number": "003"}, http.StatusOK).Body
	record("update_staff", staffID, st, updated)
	const password = "audit-secret-password"
	login := s.call(s.Owner, "set_staff_login", map[string]any{"staff_id": staffID, "login_account": "audit-staff", "password": password}, http.StatusOK).Body
	record("set_staff_login", staffID, updated, login)
	inactive := s.call(s.Owner, "deactivate_staff", map[string]any{"staff_id": staffID}, http.StatusOK).Body
	record("deactivate_staff", staffID, login, inactive)
	// 失败的操作不能产生第二条停用日志。
	s.call(s.Owner, "deactivate_staff", map[string]any{"staff_id": staffID}, http.StatusConflict)
	active := s.call(s.Owner, "reactivate_staff", map[string]any{"staff_id": staffID}, http.StatusOK).Body
	record("reactivate_staff", staffID, inactive, active)
	assigned := s.call(s.Owner, "set_staff_roles", map[string]any{"staff_id": staffID, "role_ids": []string{roleID}}, http.StatusOK).Body
	record("set_staff_roles", staffID, active, assigned)
	s.call(s.Owner, "delete_role", map[string]any{"role_id": roleID}, http.StatusOK)
	record("delete_role", roleID, map[string]any{"role": updatedRole, "holders": []any{map[string]any{"id": staffID, "name": "李师傅"}}}, nil)

	response := s.call(s.Owner, "list_audit_log", map[string]any{}, http.StatusOK).Body
	entries := response["entries"].([]any)
	if len(entries) != len(expected) {
		t.Fatalf("entries = %d, want %d: %v", len(entries), len(expected), entries)
	}
	ids := map[string]bool{}
	var previous time.Time
	for i, item := range entries {
		e := item.(map[string]any)
		want := expected[len(expected)-1-i]
		kind := "staff"
		if want.target == roleID {
			kind = "role"
		}
		if e["target_kind"] != kind {
			t.Errorf("action %s kind = %v, want %s", want.action, e["target_kind"], kind)
		}
		if e["action"] != want.action || e["target_id"] != want.target || !reflect.DeepEqual(e["before"], want.before) || !reflect.DeepEqual(e["after"], want.after) {
			t.Errorf("entry %d = %v, want %+v", i, e, want)
		}
		actor := e["actor"].(map[string]any)
		if actor["id"] != caller.StaffID.String() || actor["name"] != me["staff"].(map[string]any)["name"] {
			t.Errorf("actor = %v, want owner %s/%v", actor, caller.StaffID, me["staff"].(map[string]any)["name"])
		}
		id := e["id"].(string)
		if _, err := uuid.Parse(id); err != nil || ids[id] {
			t.Errorf("invalid or duplicate log ID %q", id)
		}
		ids[id] = true
		at, err := time.Parse(time.RFC3339Nano, e["at"].(string))
		if err != nil || at.IsZero() || (i > 0 && at.After(previous)) {
			t.Errorf("invalid log time/order: %v, %v", e["at"], err)
		}
		previous = at
	}
	var hash string
	if err := s.Pool.QueryRow(context.Background(), "SELECT password_hash FROM staff WHERE id = $1", staffID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || strings.Contains(string(raw), password) || strings.Contains(string(raw), hash) || strings.Contains(string(raw), "password") {
		t.Fatal("audit response contains a password or hash")
	}
	for _, target := range []string{roleID, staffID, uuid.NewString()} {
		filtered := s.call(s.Owner, "list_audit_log", map[string]any{"target_id": target}, http.StatusOK).Body["entries"].([]any)
		var want []any
		for _, e := range entries {
			if e.(map[string]any)["target_id"] == target {
				want = append(want, e)
			}
		}
		if len(filtered) != len(want) {
			t.Fatalf("target %s: %d entries, want %d", target, len(filtered), len(want))
		}
		for i := range filtered {
			if !reflect.DeepEqual(filtered[i], want[i]) {
				t.Errorf("filtered entry = %v, want %v", filtered[i], want[i])
			}
		}
	}
	// 日志表只追加，应用角色不能修改或删除。
	for _, query := range []string{"UPDATE audit_log SET action = 'changed'", "DELETE FROM audit_log"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), query)
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: %v, want permission denied", query, err)
		}
	}
}

func TestAuditLogRequiresPermission(t *testing.T) {
	s := open(t)
	_, token := s.Hire("小李", "002", "xiaoli", s.Role("发型师"))
	s.call(token, "list_audit_log", map[string]any{}, http.StatusForbidden)
	if result := s.ai(token, "list_audit_log", map[string]any{}); result.Status != http.StatusForbidden {
		t.Errorf("AI list_audit_log = %d, want 403", result.Status)
	}
	// 有权限的员工通过两条路径看到同一份日志。
	_, manager := s.Hire("店长", "003", "manager", s.Role("店长"))
	httpResult := s.call(manager, "list_audit_log", map[string]any{}, http.StatusOK)
	aiResult := s.ai(manager, "list_audit_log", map[string]any{})
	if aiResult.Status != http.StatusOK || !reflect.DeepEqual(httpResult.Body, aiResult.Body) {
		t.Errorf("AI audit log differs: %v / %v", httpResult, aiResult)
	}
}

func TestAuditLogIsStoreAndTenantIsolated(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	s.call(s.Owner, "create_role", map[string]any{"name": "本店角色", "permissions": []any{}}, http.StatusOK)
	before := s.call(s.Owner, "list_audit_log", map[string]any{}, http.StatusOK).Body
	otherStore := uuid.New()
	if _, err := s.Pool.Exec(ctx, "INSERT INTO stores (id, tenant_id, name) VALUES ($1, $2, $3)", otherStore, s.Res.TenantID, "同租户另一店"); err != nil {
		t.Fatal(err)
	}
	otherTenant, tenantStore := dbtest.NewTenant(t, s.Pool, "另一租户")
	for _, fixture := range []struct {
		name          string
		tenant, store uuid.UUID
	}{
		{"同租户跨门店", s.Res.TenantID, otherStore},
		{"跨租户", otherTenant, tenantStore},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			actor, target := uuid.New(), uuid.New()
			if _, err := s.Pool.Exec(ctx, "INSERT INTO staff (id, tenant_id, store_id, name, number) VALUES ($1, $2, $3, $4, $5)", actor, fixture.tenant, fixture.store, "其他门店员工", "001"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Pool.Exec(ctx, "INSERT INTO audit_log (tenant_id, store_id, action, target_id, actor_id, target_kind, after) VALUES ($1, $2, 'create_staff', $3, $4, 'staff', '{}'::jsonb)", fixture.tenant, fixture.store, target, actor); err != nil {
				t.Fatal(err)
			}
			// 外店的日志不能混入本店列表，指定它的目标也不能读到。
			all := s.HTTPWithToken(t, s.Owner, "list_audit_log", map[string]any{})
			if all.Status != http.StatusOK || !reflect.DeepEqual(all.Body, before) {
				t.Fatalf("local audit log changed: %v, want %v", all, before)
			}
			filtered := s.HTTPWithToken(t, s.Owner, "list_audit_log", map[string]any{"target_id": target})
			if filtered.Status != http.StatusOK {
				t.Fatalf("filtered audit log: %v", filtered)
			}
			if entries := filtered.Body["entries"].([]any); len(entries) != 0 {
				t.Fatalf("foreign audit log leaked: %v", entries)
			}
		})
	}
}

func TestFailedOwnerDeactivationRollsBackWithoutAuditLog(t *testing.T) {
	s := open(t)
	caller := s.MustCaller(s.Owner)
	// 留一条已有日志，确保失败操作不会改变日志列表。
	s.call(s.Owner, "create_role", map[string]any{"name": "回滚测试", "permissions": []any{}}, http.StatusOK)
	before := s.call(s.Owner, "list_audit_log", map[string]any{}, http.StatusOK).Body
	profile := s.call(s.Owner, "get_me", map[string]any{}, http.StatusOK).Body
	failed := s.call(s.Owner, "deactivate_staff", map[string]any{"staff_id": caller.StaffID}, http.StatusConflict)
	if !strings.Contains(failed.Body["detail"].(string), "至少要保留一名在职老板") {
		t.Fatalf("unexpected business error: %v", failed.Body)
	}
	var active bool
	if err := s.Pool.QueryRow(context.Background(), "SELECT active FROM staff WHERE id = $1", caller.StaffID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("failed deactivation left the owner inactive")
	}
	afterProfile := s.call(s.Owner, "get_me", map[string]any{}, http.StatusOK).Body
	if !reflect.DeepEqual(afterProfile, profile) {
		t.Fatalf("owner profile changed: %v, want %v", afterProfile, profile)
	}
	after := s.call(s.Owner, "list_audit_log", map[string]any{}, http.StatusOK).Body
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("failed operation changed audit log: %v, want %v", after, before)
	}
}
