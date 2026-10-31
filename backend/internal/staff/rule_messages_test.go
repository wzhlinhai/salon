package staff_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestDuplicateNameMessages(t *testing.T) {
	s := open(t)
	for _, tc := range []struct{ op, detail string }{
		{"create_role", "角色名称已存在"},
		{"create_level", "级别名称已存在"},
		{"create_position", "岗位名称已存在"},
	} {
		input := map[string]any{"name": "重复名称"}
		if tc.op == "create_role" {
			input["permissions"] = []any{}
		}
		s.call(s.Owner, tc.op, input, 200)
		before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "roles", "role_permissions", "levels", "positions", "audit_log")
		r := s.call(s.Owner, tc.op, input, 409)
		if r.Detail() != tc.detail {
			t.Fatal(r)
		}
		if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "roles", "role_permissions", "levels", "positions", "audit_log") != before {
			t.Fatal("rejected create changed rows")
		}
	}
}

func TestRolePermissionMessages(t *testing.T) {
	s := open(t)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "roles", "role_permissions", "audit_log")
	for _, tc := range []struct {
		permissions []any
		detail      string
	}{
		{[]any{map[string]any{"permission": "unknown"}}, "没有这项权限：unknown"},
		{[]any{map[string]any{"permission": "staff.manage"}, map[string]any{"permission": "staff.manage"}}, "权限重复：staff.manage"},
	} {
		r := s.call(s.Owner, "create_role", map[string]any{"name": "角色", "permissions": tc.permissions}, 409)
		if r.Detail() != tc.detail {
			t.Fatal(r)
		}
		if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "roles", "role_permissions", "audit_log") != before {
			t.Fatal("rejected role changed rows")
		}
	}
}

func TestAlreadyActiveMessage(t *testing.T) {
	s := open(t)
	id, _ := s.Hire("员工", "002", "staff")
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "staff", "audit_log")
	r := s.call(s.Owner, "reactivate_staff", map[string]any{"staff_id": id}, 409)
	if r.Detail() != "员工已经在职" {
		t.Fatal(r)
	}
	if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "staff", "audit_log") != before {
		t.Fatal("rejected activation changed rows")
	}
}
