package customers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type shop struct {
	*optest.Shop
	t *testing.T
}

func open(t *testing.T) *shop { return &shop{optest.OpenShop(t), t} }

func (s *shop) call(op string, args any, status int) map[string]any {
	s.t.Helper()
	r := s.HTTPWithToken(s.t, s.Owner, op, args)
	if r.Status != status {
		s.t.Fatalf("%s = %d %v, want %d", op, r.Status, r.Body, status)
	}
	return r.Body
}

func (s *shop) create(args map[string]any) map[string]any {
	s.t.Helper()
	return s.call("create_customer", args, 200)["customer"].(map[string]any)
}

func (s *shop) get(id any) map[string]any {
	return s.call("get_customer", map[string]any{"id": id}, 200)
}

// aID 返回测试顾客的 UUID。
func aID(c map[string]any) uuid.UUID { return uuid.MustParse(c["id"].(string)) }

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCreateCustomers(t *testing.T) {
	s := open(t)
	first := s.call("create_customer", map[string]any{"phone": "138-0013 1234"}, 200)
	second := s.call("create_customer", map[string]any{"phone": "13800131234"}, 200)
	assertEqual(t, first["existed"], false)
	assertEqual(t, second["existed"], true)
	assertEqual(t, first["customer"], second["customer"])
	assertEqual(t, first["customer"].(map[string]any)["phone"], "13800131234")
	a := s.create(map[string]any{})
	b := s.create(map[string]any{})
	if a["id"] == b["id"] {
		t.Fatal("unidentified customers must differ")
	}
	assertEqual(t, a["unidentified"], true)
	for _, phone := range []string{"138", "23800131234", "1380013123a", ""} {
		s.call("create_customer", map[string]any{"phone": phone}, 409)
	}
	var count, logs int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customers").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='create_customer'").Scan(&logs); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, count, 3)
	assertEqual(t, logs, 3)
}

func TestReadCustomers(t *testing.T) {
	s := open(t)
	staffID, token := s.Hire("发型师", "002", "stylist", s.Role("发型师"))
	a := s.create(map[string]any{"phone": "13800131234", "name": "王女士"})
	b := s.create(map[string]any{"phone": "13900135678", "name": "李女士"})
	u := s.create(map[string]any{})
	m := s.create(map[string]any{"phone": "13700139999"})
	s.call("merge_customers", map[string]any{"keep_id": a["id"], "merged_id": m["id"]}, 200)
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{a["id"]}, "staff_id": staffID}, 200)
	owner := s.MustCaller(s.Owner)
	stylist := s.MustCaller(token)
	self := stylist
	self.Permissions = map[ops.Permission]ops.Grant{perm.CustomerView: {Scope: ops.ScopeSelf}}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			for _, q := range []string{"13800131234", "1234", "王女"} {
				r := ch.Call(t, owner, "search_customers", map[string]any{"query": q})
				assertEqual(t, r.Status, 200)
				items := r.Body["items"].([]any)
				assertEqual(t, len(items), 1)
				assertEqual(t, items[0].(map[string]any)["id"], a["id"])
			}
			for _, tc := range []struct {
				in map[string]any
				n  int
			}{
				{map[string]any{}, 2}, {map[string]any{"include_unidentified": true}, 3},
				{map[string]any{"query": "9999"}, 0}, {map[string]any{"responsible_staff_id": staffID}, 1},
				{map[string]any{"limit": 1, "offset": 1}, 1},
			} {
				r := ch.Call(t, owner, "search_customers", tc.in)
				assertEqual(t, r.Status, 200)
				assertEqual(t, len(r.Body["items"].([]any)), tc.n)
			}
			for _, c := range []ops.Caller{owner, stylist, self} {
				r := ch.Call(t, c, "get_customer", map[string]any{"id": a["id"]})
				assertEqual(t, r.Status, 200)
				wantPhone := "138****1234"
				if c.Has(perm.CustomerFullPhone) {
					wantPhone = "13800131234"
				}
				assertEqual(t, r.Body["phone"], wantPhone)
				assertEqual(t, r.Body["phone_masked"], !c.Has(perm.CustomerFullPhone))
				r = ch.Call(t, c, "search_customers", map[string]any{"query": "13800131234"})
				assertEqual(t, r.Status, 200)
				item := r.Body["items"].([]any)[0].(map[string]any)
				assertEqual(t, item["phone"], wantPhone)
				assertEqual(t, item["phone_masked"], !c.Has(perm.CustomerFullPhone))
			}
			for _, id := range []any{b["id"], u["id"]} {
				r := ch.Call(t, self, "get_customer", map[string]any{"id": id})
				assertEqual(t, r.Status, 404)
			}
			r := ch.Call(t, self, "search_customers", map[string]any{"include_unidentified": true})
			assertEqual(t, r.Status, 200)
			assertEqual(t, len(r.Body["items"].([]any)), 1)
			r = ch.Call(t, owner, "get_customer", map[string]any{"id": m["id"]})
			assertEqual(t, r.Status, 200)
			assertEqual(t, r.Body["merged_into"], a["id"])
			r = ch.Call(t, owner, "list_customer_sources", map[string]any{})
			assertEqual(t, r.Status, 200)
			assertEqual(t, len(r.Body["items"].([]any)), 7)
		})
	}
}

func TestCustomerSearchPagination(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"name": "第一页旧", "phone": "13800131234"})
	b := s.create(map[string]any{"name": "第二位", "phone": "13900131234"})
	c := s.create(map[string]any{"name": "最新", "phone": "13700131234"})
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for i, customer := range []map[string]any{a, b, c} {
		if _, err := tx.Exec(context.Background(), "UPDATE customers SET created_at=$2 WHERE id=$1", aID(customer), time.Date(2020, 1, i+1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		first := ch.Call(t, s.MustCaller(s.Owner), "search_customers", map[string]any{"limit": 2})
		assertEqual(t, first.Status, 200)
		firstIDs := []any{}
		for _, v := range first.Body["items"].([]any) {
			firstIDs = append(firstIDs, v.(map[string]any)["id"])
		}
		assertEqual(t, firstIDs, []any{c["id"], b["id"]})
		next := ch.Call(t, s.MustCaller(s.Owner), "search_customers", map[string]any{"limit": 2, "offset": 2})
		assertEqual(t, next.Status, 200)
		items := next.Body["items"].([]any)
		assertEqual(t, len(items), 1)
		assertEqual(t, items[0].(map[string]any)["id"], a["id"])
	}
}

func TestCustomerPhones(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{})
	b := s.create(map[string]any{"phone": "13800131234"})
	s.call("add_customer_phone", map[string]any{"id": a["id"], "phone": "139-0013 5678"}, 200)
	assertEqual(t, s.get(a["id"])["phone"], "13900135678")
	s.call("add_customer_phone", map[string]any{"id": a["id"], "phone": "13600135678"}, 409)
	u := s.create(map[string]any{})
	for _, op := range []string{"add_customer_phone", "change_customer_phone"} {
		id := u["id"]
		if op == "change_customer_phone" {
			id = a["id"]
		}
		r := s.call(op, map[string]any{"id": id, "phone": b["phone"]}, 409)
		if !strings.Contains(r["detail"].(string), "可以合并") {
			t.Fatal(r)
		}
	}
	s.call("change_customer_phone", map[string]any{"id": a["id"], "phone": "13600135678"}, 200)
	s.call("change_customer_phone", map[string]any{"id": u["id"], "phone": "13500135678"}, 409)
}

func TestMergeCustomersAndSafetyLabels(t *testing.T) {
	s := open(t)
	source := s.call("create_customer_source", map[string]any{"name": "测试来源"}, 200)
	staffID, _ := s.Hire("负责人", "002", "responsible")
	keep := s.create(map[string]any{})
	merged := s.create(map[string]any{"name": "王女士", "phone": "13800131234", "gender": "female", "source_id": source["id"], "note": "注意"})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{merged["id"]}, "staff_id": staffID}, 200)
	s.call("add_safety_label", map[string]any{"customer_id": merged["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01", "note": "染膏"}, 200)
	before := s.get(merged["id"])
	s.call("merge_customers", map[string]any{"keep_id": keep["id"], "merged_id": merged["id"]}, 200)
	after := s.get(keep["id"])
	other := s.get(merged["id"])
	for _, field := range []string{"name", "phone", "gender", "source", "responsible", "note", "active_safety_labels"} {
		assertEqual(t, after[field], before[field])
	}
	assertEqual(t, other["merged_into"], keep["id"])
	assertEqual(t, other["phone"], nil)
	for _, pair := range [][2]any{{keep["id"], merged["id"]}, {merged["id"], keep["id"]}, {keep["id"], keep["id"]}} {
		s.call("merge_customers", map[string]any{"keep_id": pair[0], "merged_id": pair[1]}, 409)
	}
	r := s.call("update_customer_profile", map[string]any{"id": merged["id"], "name": "不能改"}, 409)
	if !strings.Contains(r["detail"].(string), "已合并") {
		t.Fatal(r)
	}
	for _, id := range []any{keep["id"], merged["id"]} {
		entries := s.call("list_audit_log", map[string]any{"target_id": id}, 200)["entries"].([]any)
		assertEqual(t, entries[0].(map[string]any)["action"], "merge_customers")
	}
	a := s.create(map[string]any{"name": "保留", "phone": "13900131234", "gender": "male", "source_id": source["id"], "note": "保留"})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{a["id"]}, "staff_id": s.Res.OwnerID}, 200)
	b := s.create(map[string]any{"name": "并入", "phone": "13700131234", "gender": "female", "note": "并入"})
	prior := s.get(a["id"])
	s.call("merge_customers", map[string]any{"keep_id": a["id"], "merged_id": b["id"]}, 200)
	assertEqual(t, s.get(a["id"]), prior)
}

func TestSafetyLabelRules(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{})
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	today, err := settings.BusinessDate(context.Background(), dbgen.New(tx), s.Res.StoreID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"customer_id": a["id"], "kind": "scalp_sensitive", "source": "staff_observed", "observed_on": today.AddDate(0, 0, 1).Format(time.DateOnly)}
	s.call("add_safety_label", args, 409)
	args["observed_on"] = today.Format(time.DateOnly)
	s.call("add_safety_label", args, 200)
	args["observed_on"] = "2000-01-01"
	s.call("add_safety_label", args, 200)
	labels := s.get(a["id"])["active_safety_labels"].([]any)
	assertEqual(t, len(labels), 2)
	assertEqual(t, labels[0].(map[string]any)["observed_on"], today.Format(time.DateOnly))
	revoke := map[string]any{"customer_id": a["id"], "id": labels[0].(map[string]any)["id"]}
	s.call("revoke_safety_label", revoke, 200)
	s.call("revoke_safety_label", revoke, 409)
	assertEqual(t, len(s.get(a["id"])["active_safety_labels"].([]any)), 1)
}

func TestCustomerHandover(t *testing.T) {
	s := open(t)
	from, _ := s.Hire("离职员工", "002", "from")
	to, _ := s.Hire("接手员工", "003", "handover-to")
	a := s.create(map[string]any{"phone": "13800131234"})
	b := s.create(map[string]any{})
	m := s.create(map[string]any{})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{a["id"], b["id"], m["id"]}, "staff_id": from}, 200)
	s.call("merge_customers", map[string]any{"keep_id": a["id"], "merged_id": m["id"]}, 200)
	s.call("deactivate_staff", map[string]any{"staff_id": from}, 200)
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{a["id"]}, "staff_id": from}, 409)
	s.call("hand_over_customers", map[string]any{"from_staff_id": to, "to_staff_id": from}, 409)
	s.call("hand_over_customers", map[string]any{"from_staff_id": to, "to_staff_id": to}, 409)
	result := s.call("hand_over_customers", map[string]any{"from_staff_id": from, "to_staff_id": to}, 200)
	assertEqual(t, result["count"], float64(2))
	for _, id := range []any{a["id"], b["id"]} {
		assertEqual(t, s.get(id)["responsible"].(map[string]any)["id"], to.String())
		for _, ch := range s.Channels() {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_audit_log", map[string]any{"target_id": id})
			assertEqual(t, r.Status, 200)
			entries := r.Body["entries"].([]any)
			latest := entries[0].(map[string]any)
			assertEqual(t, latest["action"], "hand_over_customers")
			assertEqual(t, latest["target_id"], id)
			before := latest["before"].(map[string]any)
			after := latest["after"].(map[string]any)
			assertEqual(t, before["id"], id)
			assertEqual(t, before["responsible"].(map[string]any)["id"], from.String())
			assertEqual(t, after, s.get(id))
			assertEqual(t, after["responsible"].(map[string]any)["id"], to.String())
		}
	}
	assertEqual(t, s.get(m["id"])["responsible"].(map[string]any)["id"], from.String())
	logs := s.call("list_audit_log", map[string]any{"target_id": from}, 200)["entries"].([]any)
	latest := logs[0].(map[string]any)
	assertEqual(t, latest["action"], "hand_over_customers")
	assertEqual(t, latest["before"], map[string]any{"customer_count": float64(2)})
	assertEqual(t, latest["after"], map[string]any{"customer_count": float64(0), "to_staff_id": to.String()})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{a["id"]}}, 200)
	assertEqual(t, s.get(a["id"])["responsible"], nil)
}

func TestCustomerSources(t *testing.T) {
	s := open(t)
	source := s.call("create_customer_source", map[string]any{"name": "活动"}, 200)
	a := s.create(map[string]any{"source_id": source["id"]})
	s.call("create_customer_source", map[string]any{"name": "活动"}, 409)
	s.call("update_customer_source", map[string]any{"id": source["id"], "name": "活动", "active": false}, 200)
	s.call("create_customer", map[string]any{"source_id": source["id"]}, 409)
	s.call("update_customer_profile", map[string]any{"id": a["id"], "source_id": source["id"], "name": "保留已选"}, 200)
	b := s.create(map[string]any{})
	s.call("update_customer_profile", map[string]any{"id": b["id"], "source_id": source["id"]}, 409)
	s.call("create_customer", map[string]any{"source_id": uuid.New()}, 409)
}

func snapshot(t *testing.T, s *shop) string {
	t.Helper()
	var state string
	err := s.Pool.QueryRow(context.Background(), `SELECT jsonb_build_array(
 (SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY id),'[]') FROM customers c),
 (SELECT coalesce(jsonb_agg(to_jsonb(l) ORDER BY id),'[]') FROM customer_safety_labels l),
 (SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY id),'[]') FROM customer_sources s),
 (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY id),'[]') FROM audit_log a))::text`).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestCustomerWritePermissionsAndAI(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{})
	b := s.create(map[string]any{"phone": "13800131234"})
	source := s.call("create_customer_source", map[string]any{"name": "权限"}, 200)
	labeled := s.call("add_safety_label", map[string]any{"customer_id": a["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}, 200)
	label := labeled["active_safety_labels"].([]any)[0].(map[string]any)["id"]
	owner := s.MustCaller(s.Owner)
	for _, tc := range []struct {
		op   string
		p    ops.Permission
		args map[string]any
	}{
		{"create_customer", perm.CustomerEdit, map[string]any{"phone": "13900131234"}},
		{"update_customer_profile", perm.CustomerEdit, map[string]any{"id": a["id"], "name": "修改"}},
		{"add_customer_phone", perm.CustomerEdit, map[string]any{"id": a["id"], "phone": "13900131234"}},
		{"change_customer_phone", perm.CustomerMerge, map[string]any{"id": b["id"], "phone": "13900131234"}},
		{"merge_customers", perm.CustomerMerge, map[string]any{"keep_id": a["id"], "merged_id": b["id"]}},
		{"add_safety_label", perm.CustomerSafetyLabel, map[string]any{"customer_id": a["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}},
		{"revoke_safety_label", perm.CustomerSafetyLabel, map[string]any{"customer_id": a["id"], "id": label}},
		{"assign_customer_responsible", perm.CustomerAssign, map[string]any{"customer_ids": []any{a["id"]}, "staff_id": s.Res.OwnerID}},
		{"hand_over_customers", perm.CustomerAssign, map[string]any{"from_staff_id": uuid.New(), "to_staff_id": s.Res.OwnerID}},
		{"create_customer_source", perm.SettingsManage, map[string]any{"name": "拒绝新建"}},
		{"update_customer_source", perm.SettingsManage, map[string]any{"id": source["id"], "name": "拒绝修改", "active": false}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			before := snapshot(t, s)
			c := owner
			c.Owner = false
			c.Permissions = perm.All()
			delete(c.Permissions, tc.p)
			r := s.HTTP(t, &c, tc.op, tc.args)
			assertEqual(t, r.Status, 403)
			assertEqual(t, snapshot(t, s), before)
			r = s.AI(t, owner, tc.op, tc.args)
			if r.Status == 200 || !strings.Contains(fmt.Sprint(r.Err), "draft") {
				t.Fatalf("AI write = %+v", r)
			}
			assertEqual(t, snapshot(t, s), before)
		})
	}
}

func TestCustomerUpdateLocksBeforeAudit(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"name": "原名"})
	id := uuid.MustParse(a["id"].(string))
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	if _, err := q.LockCustomer(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE customers SET name='事务改名' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "update_customer_profile", map[string]any{"id": id, "name": "请求改名"})
	}()
	select {
	case r := <-done:
		t.Fatalf("returned while locked: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		assertEqual(t, r.Status, 200)
	case <-time.After(10 * time.Second):
		t.Fatal("request did not finish")
	}
	logs := s.call("list_audit_log", map[string]any{"target_id": id}, 200)["entries"].([]any)
	assertEqual(t, logs[0].(map[string]any)["before"].(map[string]any)["name"], "事务改名")
}

func TestCustomersCannotBeDeleted(t *testing.T) {
	s := open(t)
	for _, table := range []string{"customers", "customer_safety_labels"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "DELETE FROM "+table)
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("DELETE %s: %v", table, err)
		}
	}
}

func TestCustomerReadTenantIsolation(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"phone": "13800131234"})
	otherTenant, _ := dbtest.NewTenant(t, s.Pool, "其他租户")
	other := s.MustCaller(s.Owner)
	other.TenantID = otherTenant
	store := uuid.New()
	if _, err := s.Pool.Exec(context.Background(), "INSERT INTO stores (id,tenant_id,name) VALUES ($1,$2,'另一门店')", store, s.Res.TenantID); err != nil {
		t.Fatal(err)
	}
	same := s.MustCaller(s.Owner)
	same.StoreID = store
	for _, ch := range s.Channels() {
		r := ch.Call(t, other, "get_customer", map[string]any{"id": a["id"]})
		assertEqual(t, r.Status, 404)
		r = ch.Call(t, other, "search_customers", map[string]any{})
		assertEqual(t, r.Status, 200)
		assertEqual(t, len(r.Body["items"].([]any)), 0)
		r = ch.Call(t, same, "get_customer", map[string]any{"id": a["id"]})
		assertEqual(t, r.Status, 200)
		r = ch.Call(t, same, "search_customers", map[string]any{})
		assertEqual(t, r.Status, 200)
		assertEqual(t, len(r.Body["items"].([]any)), 1)
	}
}

func TestAuditStoresFullPhoneForMaskedCaller(t *testing.T) {
	s := open(t)
	_, token := s.Hire("前台记录", "002", "masked", s.Role("发型师"))
	r := s.HTTPWithToken(t, token, "create_customer", map[string]any{"phone": "13800131234"})
	assertEqual(t, r.Status, 200)
	c := r.Body["customer"].(map[string]any)
	assertEqual(t, c["phone"], "138****1234")
	r = s.HTTPWithToken(t, token, "update_customer_profile", map[string]any{"id": c["id"], "name": "修改姓名"})
	assertEqual(t, r.Status, 200)
	assertEqual(t, r.Body["phone"], "138****1234")
	assertEqual(t, r.Body["phone_masked"], true)
	logs := s.call("list_audit_log", map[string]any{"target_id": c["id"]}, 200)["entries"].([]any)
	raw, err := json.Marshal(logs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "13800131234") || strings.Contains(string(raw), "138****1234") {
		t.Fatal(string(raw))
	}
}

func TestCustomerUniquePhoneIndex(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"phone": "13800131234"})
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO customers (tenant_id,store_id,phone,created_by) VALUES ($1,$2,'13800131234',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	if err == nil || !strings.Contains(err.Error(), "customers_unique_phone") {
		t.Fatalf("duplicate insert = %v", err)
	}
	b := s.create(map[string]any{"phone": "13900131234"})
	s.call("merge_customers", map[string]any{"keep_id": a["id"], "merged_id": b["id"]}, 200)
	c := s.create(map[string]any{"phone": "13900131234"})
	if c["id"] == b["id"] {
		t.Fatal("merged customer's old phone must be reusable")
	}
}

func TestMergedCustomerRejectsEveryWrite(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{})
	b := s.create(map[string]any{})
	labeled := s.call("add_safety_label", map[string]any{"customer_id": b["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}, 200)
	label := labeled["active_safety_labels"].([]any)[0].(map[string]any)["id"]
	s.call("merge_customers", map[string]any{"keep_id": a["id"], "merged_id": b["id"]}, 200)
	for _, tc := range []struct {
		op   string
		args map[string]any
	}{
		{"update_customer_profile", map[string]any{"id": b["id"], "name": "修改"}},
		{"add_customer_phone", map[string]any{"id": b["id"], "phone": "13800131234"}},
		{"change_customer_phone", map[string]any{"id": b["id"], "phone": "13800131234"}},
		{"add_safety_label", map[string]any{"customer_id": b["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}},
		{"revoke_safety_label", map[string]any{"customer_id": b["id"], "id": label}},
		{"assign_customer_responsible", map[string]any{"customer_ids": []any{b["id"]}, "staff_id": s.Res.OwnerID}},
	} {
		before := snapshot(t, s)
		r := s.call(tc.op, tc.args, 409)
		if !strings.Contains(r["detail"].(string), "该顾客已合并到另一位顾客") {
			t.Fatal(r)
		}
		assertEqual(t, snapshot(t, s), before)
	}
}

func TestCustomerDefaultRolePermissions(t *testing.T) {
	s := open(t)
	for i, name := range []string{"店长", "发型师", "助理", "前台"} {
		_, token := s.Hire(name, fmt.Sprint(i+2), "role-"+fmt.Sprint(i), s.Role(name))
		c := s.MustCaller(token)
		assertEqual(t, c.Has(perm.CustomerEdit), name != "助理")
		assertEqual(t, c.Has(perm.CustomerSafetyLabel), true)
		assertEqual(t, c.Has(perm.CustomerMerge), name == "店长")
		assertEqual(t, c.Has(perm.CustomerAssign), name == "店长")
		for _, p := range []ops.Permission{perm.CustomerEdit, perm.CustomerSafetyLabel, perm.CustomerMerge, perm.CustomerAssign} {
			if g, ok := c.Permissions[p]; ok && (g.Scope != "" || g.WindowDays != nil) {
				t.Fatalf("%s %s: unexpected limits %+v", name, p, g)
			}
		}
	}
}
