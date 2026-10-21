package customers_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestAuditLogCustomerViewScope(t *testing.T) {
	s := open(t)
	staffID, token := s.Hire("负责人", "002", "audit-scope")
	a := s.create(map[string]any{"name": "顾客甲"})
	b := s.create(map[string]any{"name": "顾客乙"})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{a["id"]}, "staff_id": staffID}, 200)
	for _, ch := range s.Channels() {
		for _, tc := range []struct {
			name  string
			view  bool
			scope ops.Scope
			wantA bool
			wantB bool
		}{
			{name: "no_customer_view"},
			{name: "self", view: true, scope: ops.ScopeSelf, wantA: true},
			{name: "store", view: true, scope: ops.ScopeStore, wantA: true, wantB: true},
		} {
			t.Run(ch.Name+"/"+tc.name, func(t *testing.T) {
				reader := s.MustCaller(token)
				reader.Permissions = map[ops.Permission]ops.Grant{perm.AuditLogView: {}}
				if tc.view {
					reader.Permissions[perm.CustomerView] = ops.Grant{Scope: tc.scope}
				}
				for _, target := range []any{nil, a["id"], b["id"]} {
					in := map[string]any{}
					if target != nil {
						in["target_id"] = target
					}
					r := ch.Call(t, reader, "list_audit_log", in)
					assertEqual(t, r.Status, 200)
					foundA, foundB, foundStaff := false, false, false
					for _, entry := range r.Body["entries"].([]any) {
						e := entry.(map[string]any)
						foundA = foundA || e["target_id"] == a["id"]
						foundB = foundB || e["target_id"] == b["id"]
						foundStaff = foundStaff || (e["target_id"] == staffID.String() && e["action"] == "create_staff")
					}
					assertEqual(t, foundA, tc.wantA && (target == nil || target == a["id"]))
					assertEqual(t, foundB, tc.wantB && (target == nil || target == b["id"]))
					assertEqual(t, foundStaff, target == nil)
				}
			})
		}
	}
}
