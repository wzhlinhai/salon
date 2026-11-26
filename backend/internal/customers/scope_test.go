package customers_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestCustomerWritesRespectViewScope(t *testing.T) {
	s := open(t)
	staffID, token := s.Hire("范围员工", "002", "scope-writer")
	other := s.create(map[string]any{"phone": "13800131234"})
	own := s.create(map[string]any{"phone": "13900131234"})
	unidentified := s.create(map[string]any{})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{own["id"]}, "staff_id": staffID}, 200)
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{other["id"]}, "staff_id": s.Res.OwnerID}, 200)
	labelResult := s.call("add_safety_label", map[string]any{"customer_id": other["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}, 200)
	label := labelResult["active_safety_labels"].([]any)[0].(map[string]any)["id"]
	for _, scoped := range []bool{true, false} {
		caller := s.MustCaller(token)
		caller.Permissions = map[ops.Permission]ops.Grant{perm.CustomerEdit: {}, perm.CustomerMerge: {}, perm.CustomerAssign: {}, perm.CustomerSafetyLabel: {}}
		if scoped {
			caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
		}
		for _, tc := range []struct {
			op           string
			args         map[string]any
			scopedStatus int
			scopedDetail string
			noViewStatus int
			noViewDetail string
		}{
			{"hand_over_customers", map[string]any{"from_staff_id": s.Res.OwnerID, "to_staff_id": staffID}, 409, "这位员工负责的顾客里有你无权查看的，不能交接", 409, "这位员工负责的顾客里有你无权查看的，不能交接"},
			{"create_customer", map[string]any{"phone": other["phone"]}, 409, "这个手机号已有档案，但不在你能查看的范围内", 403, "permission denied"},
			{"update_customer_profile", map[string]any{"id": other["id"], "name": "不能改"}, 404, "not found", 403, "permission denied"},
			{"add_customer_phone", map[string]any{"id": unidentified["id"], "phone": "13700131234"}, 404, "not found", 403, "permission denied"},
			{"change_customer_phone", map[string]any{"id": other["id"], "phone": "13700131234"}, 404, "not found", 403, "permission denied"},
			{"merge_customers", map[string]any{"keep_id": own["id"], "merged_id": other["id"]}, 404, "not found", 404, "not found"},
			{"merge_customers", map[string]any{"keep_id": other["id"], "merged_id": own["id"]}, 404, "not found", 404, "not found"},
			{"add_safety_label", map[string]any{"customer_id": other["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}, 404, "not found", 404, "not found"},
			{"revoke_safety_label", map[string]any{"customer_id": other["id"], "id": label}, 404, "not found", 404, "not found"},
			{"assign_customer_responsible", map[string]any{"customer_ids": []any{own["id"], other["id"]}, "staff_id": staffID}, 404, "not found", 404, "not found"},
		} {
			before := snapshot(t, s)
			r := s.HTTP(t, &caller, tc.op, tc.args)
			wantStatus, wantDetail := tc.noViewStatus, tc.noViewDetail
			if scoped {
				wantStatus, wantDetail = tc.scopedStatus, tc.scopedDetail
			}
			if r.Status != wantStatus || r.Detail() != wantDetail {
				t.Fatalf("%s: got %d %v, want %d %s", tc.op, r.Status, r.Body, wantStatus, wantDetail)
			}
			if tc.op == "hand_over_customers" {
				assertEqual(t, r.Body["detail"], "这位员工负责的顾客里有你无权查看的，不能交接")
			}
			raw, err := json.Marshal(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), other["id"].(string)) || strings.Contains(string(raw), "13800131234") {
				t.Fatalf("customer leaked: %s", raw)
			}
			assertEqual(t, snapshot(t, s), before)
		}
		if scoped {
			r := s.HTTP(t, &caller, "update_customer_profile", map[string]any{"id": own["id"], "name": "允许改"})
			assertEqual(t, r.Status, 200)
			r = s.HTTP(t, &caller, "create_customer", map[string]any{"phone": own["phone"]})
			assertEqual(t, r.Status, 200)
			assertEqual(t, r.Body["existed"], true)
		}
		r := s.HTTP(t, &caller, "create_customer", map[string]any{})
		if scoped {
			assertEqual(t, r.Status, 200)
			assertEqual(t, r.Body["existed"], false)
		} else if r.Status != 403 || r.Detail() != "permission denied" {
			t.Fatalf("got %d %v, want 403 permission denied", r.Status, r.Body)
		}
	}
}

func TestCreateCustomerRequiresView(t *testing.T) {
	s := open(t)
	_, token := s.Hire("建档员工", "002", "create-view")
	caller := s.MustCaller(token)
	caller.Permissions = map[ops.Permission]ops.Grant{perm.CustomerEdit: {}}
	args := map[string]any{"phone": "13800132222", "name": "看不见的顾客"}
	before := snapshot(t, s)
	r := s.HTTP(t, &caller, "create_customer", args)
	if r.Status != 403 || r.Detail() != "permission denied" {
		t.Fatalf("got %d %v, want 403 permission denied", r.Status, r.Body)
	}
	assertEqual(t, snapshot(t, s), before)
	raw, err := json.Marshal(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"13800132222", "看不见的顾客"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("create_customer leaked %q in rejection: %s", secret, raw)
		}
	}
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeStore}
	r = s.HTTP(t, &caller, "create_customer", args)
	assertEqual(t, r.Status, 200)
	assertEqual(t, r.Body["existed"], false)
}

func TestCustomerDataOpsRequireView(t *testing.T) {
	s := open(t)
	_, token := s.Hire("无查看员工", "002", "data-no-view")
	identified := s.create(map[string]any{"phone": "13800132223"})
	unidentified := s.create(map[string]any{})
	for _, tc := range []struct {
		op          string
		permissions map[ops.Permission]ops.Grant
		args        map[string]any
	}{
		{"update_customer_profile", map[ops.Permission]ops.Grant{perm.CustomerEdit: {}}, map[string]any{"id": identified["id"], "name": "看不到"}},
		{"add_customer_phone", map[ops.Permission]ops.Grant{perm.CustomerEdit: {}, perm.CustomerFullPhone: {}}, map[string]any{"id": unidentified["id"], "phone": "13700132223"}},
		{"change_customer_phone", map[ops.Permission]ops.Grant{perm.CustomerMerge: {}, perm.CustomerFullPhone: {}}, map[string]any{"id": identified["id"], "phone": "13700132223"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			caller := s.MustCaller(token)
			caller.Permissions = tc.permissions
			before := snapshot(t, s)
			r := s.HTTP(t, &caller, tc.op, tc.args)
			if r.Status != 403 || r.Detail() != "permission denied" {
				t.Fatalf("got %d %v, want 403 permission denied", r.Status, r.Body)
			}
			assertEqual(t, snapshot(t, s), before)
		})
	}
	caller := s.MustCaller(token)
	caller.Permissions = map[ops.Permission]ops.Grant{perm.CustomerEdit: {}}
	before := draftCount(t, s)
	r := s.AI(t, caller, "create_customer", map[string]any{"name": "看不到", "phone": "13900132223"})
	if r.Status != 403 || r.Detail() != "permission denied" {
		t.Fatalf("got %d %v, want 403 permission denied", r.Status, r.Body)
	}
	assertEqual(t, draftCount(t, s), before)
}

func TestPhoneDedupeRejectsInvisibleCustomer(t *testing.T) {
	s := open(t)
	staffID, token := s.Hire("范围员工", "002", "scope-phone")
	visible := s.create(map[string]any{"phone": "13800132224"})
	invisible := s.create(map[string]any{"phone": "13900132224"})
	unidentified := s.create(map[string]any{})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{visible["id"], unidentified["id"]}, "staff_id": staffID}, 200)
	caller := s.MustCaller(token)
	caller.Permissions = map[ops.Permission]ops.Grant{
		perm.CustomerView:      {Scope: ops.ScopeSelf},
		perm.CustomerEdit:      {},
		perm.CustomerFullPhone: {},
		perm.CustomerMerge:     {},
	}
	for _, tc := range []struct {
		op     string
		target map[string]any
	}{
		{"add_customer_phone", unidentified},
		{"change_customer_phone", visible},
	} {
		before := snapshot(t, s)
		r := s.HTTP(t, &caller, tc.op, map[string]any{"id": tc.target["id"], "phone": invisible["phone"]})
		if r.Status != 409 || r.Detail() != "这个手机号已有档案，但不在你能查看的范围内" {
			t.Fatalf("%s: got %d %v, want 409 invisible-customer rule", tc.op, r.Status, r.Body)
		}
		assertEqual(t, snapshot(t, s), before)
	}
	r := s.HTTP(t, &caller, "add_customer_phone", map[string]any{"id": unidentified["id"], "phone": visible["phone"]})
	if r.Status != 409 || r.Detail() != "这个手机号已属于另一位顾客，可以合并这两位顾客" {
		t.Fatalf("viewable duplicate: got %d %v, want 409 merge hint", r.Status, r.Body)
	}
}

func draftCount(t *testing.T, s *shop) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM ai_drafts").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestScopedCreateCustomerAssignsResponsible(t *testing.T) {
	s := open(t)
	staffID, token := s.Hire("范围员工", "002", "scope-creator")
	scoped := s.MustCaller(token)
	scoped.Permissions = map[ops.Permission]ops.Grant{perm.CustomerEdit: {}, perm.CustomerView: {Scope: ops.ScopeSelf}}

	identified := s.HTTP(t, &scoped, "create_customer", map[string]any{"phone": "13800132222"})
	assertEqual(t, identified.Status, 200)
	assertEqual(t, identified.Body["existed"], false)
	customer := identified.Body["customer"].(map[string]any)
	assertEqual(t, customer["responsible"].(map[string]any)["id"], staffID.String())
	got := s.HTTP(t, &scoped, "get_customer", map[string]any{"id": customer["id"]})
	assertEqual(t, got.Status, 200)
	assertEqual(t, got.Body["id"], customer["id"])

	unidentified := s.HTTP(t, &scoped, "create_customer", map[string]any{})
	assertEqual(t, unidentified.Status, 200)
	assertEqual(t, unidentified.Body["existed"], false)
	unknown := unidentified.Body["customer"].(map[string]any)
	assertEqual(t, unknown["responsible"].(map[string]any)["id"], staffID.String())
	got = s.HTTP(t, &scoped, "get_customer", map[string]any{"id": unknown["id"]})
	assertEqual(t, got.Status, 200)
	assertEqual(t, got.Body["id"], unknown["id"])

	entries := s.call("list_audit_log", map[string]any{"target_id": customer["id"]}, 200)["entries"].([]any)
	created := entries[0].(map[string]any)
	assertEqual(t, created["action"], "create_customer")
	assertEqual(t, created["after"].(map[string]any)["responsible"].(map[string]any)["id"], staffID.String())

	store := s.MustCaller(token)
	store.Permissions = map[ops.Permission]ops.Grant{perm.CustomerEdit: {}, perm.CustomerView: {}}
	r := s.HTTP(t, &store, "create_customer", map[string]any{"phone": "13900132222"})
	assertEqual(t, r.Status, 200)
	assertEqual(t, r.Body["customer"].(map[string]any)["responsible"], nil)

	otherID, _ := s.Hire("其他负责人", "003", "scope-owner")
	existing := s.create(map[string]any{"phone": "13700132222"})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{existing["id"]}, "staff_id": otherID}, 200)
	r = s.HTTP(t, &store, "create_customer", map[string]any{"phone": "13700132222"})
	assertEqual(t, r.Status, 200)
	assertEqual(t, r.Body["existed"], true)
	assertEqual(t, r.Body["customer"].(map[string]any)["responsible"].(map[string]any)["id"], otherID.String())
	r = s.HTTP(t, &scoped, "create_customer", map[string]any{"phone": "13800132222"})
	assertEqual(t, r.Status, 200)
	assertEqual(t, r.Body["existed"], true)
	assertEqual(t, r.Body["customer"].(map[string]any)["responsible"].(map[string]any)["id"], staffID.String())
}
