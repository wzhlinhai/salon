package customers_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestAuditLogMasksNestedPhones(t *testing.T) {
	s := open(t)
	from, _ := s.Hire("原负责人", "002", "audit-from")
	to, _ := s.Hire("接手人", "003", "audit-to")
	a := s.create(map[string]any{"phone": "13800131234"})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{a["id"]}, "staff_id": from}, 200)
	s.call("hand_over_customers", map[string]any{"from_staff_id": from, "to_staff_id": to}, 200)
	_, token := s.Hire("日志查看员工", "004", "auditor")
	reader := s.MustCaller(token)
	reader.Permissions = map[ops.Permission]ops.Grant{perm.AuditLogView: {}, perm.CustomerView: {Scope: ops.ScopeStore}}
	full := reader
	full.Permissions = map[ops.Permission]ops.Grant{perm.AuditLogView: {}, perm.CustomerFullPhone: {}, perm.CustomerView: {Scope: ops.ScopeStore}}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	env := ops.Env{Caller: s.MustCaller(s.Owner), Q: dbgen.New(tx)}
	nested := map[string]any{"objects": []any{map[string]any{"deep": map[string]any{"phone": "13800131234"}}}, "phone": "123", "note": "不含号码", "number": json.Number("9007199254740993")}
	if err := audit.Record(context.Background(), env, "nested_fixture", aID(a), nested, nested); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			for _, c := range []ops.Caller{reader, full} {
				r := ch.Call(t, c, "list_audit_log", map[string]any{})
				assertEqual(t, r.Status, 200)
				bytes, err := json.Marshal(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				contains := strings.Contains(string(bytes), "13800131234")
				assertEqual(t, contains, c.Has(perm.CustomerFullPhone))
				if !c.Has(perm.CustomerFullPhone) && !strings.Contains(string(bytes), "138****1234") {
					t.Fatal("missing masked phone")
				}
				foundHandover, foundNested := false, false
				for _, entry := range r.Body["entries"].([]any) {
					e := entry.(map[string]any)
					if e["action"] == "hand_over_customers" && e["target_id"] == from.String() {
						foundHandover = true
						assertEqual(t, e["before"], map[string]any{"customer_count": float64(1)})
						assertEqual(t, e["after"], map[string]any{"customer_count": float64(0), "to_staff_id": to.String()})
					}
					if e["action"] == "nested_fixture" {
						foundNested = true
						for _, key := range []string{"before", "after"} {
							value := e[key].(map[string]any)
							assertEqual(t, value["phone"], "123")
							phone := value["objects"].([]any)[0].(map[string]any)["deep"].(map[string]any)["phone"]
							want := "138****1234"
							if c.Has(perm.CustomerFullPhone) {
								want = "13800131234"
							}
							assertEqual(t, phone, want)
						}
					}
				}
				assertEqual(t, foundHandover, true)
				assertEqual(t, foundNested, true)
			}
		})
	}
	var stored string
	if err := s.Pool.QueryRow(context.Background(), "SELECT before::text FROM audit_log WHERE action='nested_fixture'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "13800131234") || !strings.Contains(stored, "9007199254740993") {
		t.Fatal(stored)
	}
}
