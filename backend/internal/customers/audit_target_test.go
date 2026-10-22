package customers_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestAuditLogStaffTargetDoesNotConcernCustomer(t *testing.T) {
	s := open(t)
	_, token := s.Hire("日志读者", "002", "target-reader")
	customer := s.create(map[string]any{})
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	id := uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO audit_log (id,tenant_id,store_id,action,target_kind,target_id,actor_id) VALUES ($1,$2,$3,'staff_fixture','staff',$4,$5)", id, s.Res.TenantID, s.Res.StoreID, aID(customer), s.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	reader := s.MustCaller(token)
	reader.Permissions = map[ops.Permission]ops.Grant{perm.AuditLogView: {}, perm.CustomerView: {Scope: ops.ScopeSelf}}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, reader, "list_audit_log", map[string]any{"target_id": customer["id"]})
			assertEqual(t, r.Status, 200)
			entries := r.Body["entries"].([]any)
			assertEqual(t, len(entries), 1)
			entry := entries[0].(map[string]any)
			assertEqual(t, entry["id"], id.String())
			assertEqual(t, entry["target_kind"], "staff")
		})
	}
}

func TestCustomerAuditTargetKinds(t *testing.T) {
	s := open(t)
	source := s.call("create_customer_source", map[string]any{"name": "日志来源"}, 200)
	s.call("update_customer_source", map[string]any{"id": source["id"], "name": "新日志来源", "active": true}, 200)
	a := s.create(map[string]any{})
	b := s.create(map[string]any{})
	s.call("update_customer_profile", map[string]any{"id": a["id"], "name": "新姓名"}, 200)
	s.call("add_customer_phone", map[string]any{"id": a["id"], "phone": "13800131234"}, 200)
	s.call("change_customer_phone", map[string]any{"id": a["id"], "phone": "13900131234"}, 200)
	labeled := s.call("add_safety_label", map[string]any{"customer_id": a["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}, 200)
	label := labeled["active_safety_labels"].([]any)[0].(map[string]any)["id"]
	s.call("revoke_safety_label", map[string]any{"customer_id": a["id"], "id": label}, 200)
	s.call("merge_customers", map[string]any{"keep_id": a["id"], "merged_id": b["id"]}, 200)
	from, _ := s.Hire("交接员工", "002", "kind-from")
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{a["id"]}, "staff_id": from}, 200)
	s.call("hand_over_customers", map[string]any{"from_staff_id": from, "to_staff_id": s.Res.OwnerID}, 200)
	expected := map[string]int{
		"create_customer": 2, "update_customer_profile": 1, "add_customer_phone": 1, "change_customer_phone": 1,
		"add_safety_label": 1, "revoke_safety_label": 1, "merge_customers": 2, "assign_customer_responsible": 1,
		"hand_over_customers": 1, "create_customer_source": 1, "update_customer_source": 1,
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for action, count := range expected {
		kind := "customer"
		if action == "create_customer_source" || action == "update_customer_source" {
			kind = "customer_source"
		}
		var got int
		if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action=$1 AND target_kind=$2 AND (customer_id=target_id OR ($2 <> 'customer' AND customer_id IS NULL))", action, kind).Scan(&got); err != nil {
			t.Fatal(err)
		}
		assertEqual(t, got, count)
	}
	var summaries int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='hand_over_customers' AND target_kind='staff' AND target_id=$1 AND customer_id IS NULL", from).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, summaries, 1)
}

func TestCustomerMergePreservesAuditCustomerReferences(t *testing.T) {
	s := open(t)
	keep := s.create(map[string]any{})
	merged := s.create(map[string]any{})
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var logID uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT id FROM audit_log WHERE action='create_customer' AND customer_id=$1", aID(merged)).Scan(&logID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.call("merge_customers", map[string]any{"keep_id": keep["id"], "merged_id": merged["id"]}, 200)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var targetID, customerID uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT target_id,customer_id FROM audit_log WHERE id=$1", logID).Scan(&targetID, &customerID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, targetID, aID(merged))
	assertEqual(t, customerID, aID(merged))
}
