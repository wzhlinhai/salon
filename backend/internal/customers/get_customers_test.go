package customers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// itemIDs 返回批量读取结果里的顾客 ID。
func itemIDs(t *testing.T, r optest.Result) []any {
	t.Helper()
	items := r.Body["items"].([]any)
	ids := make([]any, len(items))
	for i, item := range items {
		ids[i] = item.(map[string]any)["id"]
	}
	return ids
}

// foreignCustomer 在同一个租户的另一个门店建一位顾客。
func foreignCustomer(t *testing.T, s *shop) dbgen.Customer {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	store, err := q.CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "别店"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := q.CreateCustomer(context.Background(), dbgen.CreateCustomerParams{TenantID: s.Res.TenantID, StoreID: store.ID, CreatedBy: &s.Res.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	return c
}

func TestGetCustomers(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"phone": "13800131234", "name": "王女士"})
	b := s.create(map[string]any{"phone": "13900135678", "name": "李女士"})
	m := s.create(map[string]any{"phone": "13700139999"})
	s.call("merge_customers", map[string]any{"keep_id": a["id"], "merged_id": m["id"]}, 200)
	foreign := foreignCustomer(t, s)
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			ids := []any{b["id"], a["id"], m["id"], uuid.NewString(), foreign.ID.String()}
			r := ch.Call(t, owner, "get_customers", map[string]any{"ids": ids})
			assertEqual(t, r.Status, 200)
			assertEqual(t, itemIDs(t, r), []any{b["id"], a["id"], m["id"]})
			items := r.Body["items"].([]any)
			for _, item := range items {
				c := item.(map[string]any)
				single := ch.Call(t, owner, "get_customer", map[string]any{"id": c["id"]})
				assertEqual(t, single.Status, 200)
				assertEqual(t, c, single.Body)
			}
			assertEqual(t, items[2].(map[string]any)["merged_into"], a["id"])
		})
	}
}

func TestGetCustomersViewScope(t *testing.T) {
	s := open(t)
	staffID, token := s.Hire("范围员工", "002", "batch-scope", s.Role("发型师"))
	own := s.create(map[string]any{"phone": "13800131234"})
	other := s.create(map[string]any{"phone": "13900131234"})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{own["id"]}, "staff_id": staffID}, 200)
	self := s.MustCaller(token)
	self.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		r := ch.Call(t, self, "get_customers", map[string]any{"ids": []any{other["id"], own["id"]}})
		assertEqual(t, r.Status, 200)
		assertEqual(t, itemIDs(t, r), []any{own["id"]})
	}
}

func TestGetCustomersMasksPhone(t *testing.T) {
	s := open(t)
	_, token := s.Hire("前台", "002", "batch-mask", s.Role("发型师"))
	a := s.create(map[string]any{"phone": "13800131234"})
	stylist := s.MustCaller(token)
	if stylist.Has(perm.CustomerFullPhone) {
		t.Fatal("stylist unexpectedly has full phone permission")
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, stylist, "get_customers", map[string]any{"ids": []any{a["id"]}})
		assertEqual(t, r.Status, 200)
		items := r.Body["items"].([]any)
		assertEqual(t, len(items), 1)
		c := items[0].(map[string]any)
		assertEqual(t, c["phone"], "138****1234")
		assertEqual(t, c["phone_masked"], true)
	}
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		r := ch.Call(t, owner, "get_customers", map[string]any{"ids": []any{a["id"]}})
		assertEqual(t, r.Status, 200)
		c := r.Body["items"].([]any)[0].(map[string]any)
		assertEqual(t, c["phone"], "13800131234")
		assertEqual(t, c["phone_masked"], false)
	}
}

func TestGetCustomersRequiresViewPermission(t *testing.T) {
	s := open(t)
	_, token := s.Hire("无查看员工", "002", "batch-denied")
	a := s.create(map[string]any{"phone": "13800131234"})
	caller := s.MustCaller(token)
	caller.Permissions = map[ops.Permission]ops.Grant{}
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "get_customers", map[string]any{"ids": []any{a["id"]}})
		if r.Status != 403 || r.Detail() != "permission denied" {
			t.Fatalf("%s: got %d %q, want 403 permission denied", ch.Name, r.Status, r.Detail())
		}
	}
}

func TestGetCustomersValidation(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"phone": "13800131234"})
	owner := s.MustCaller(s.Owner)
	tooMany := make([]any, 0, 201)
	for range 201 {
		tooMany = append(tooMany, uuid.NewString())
	}
	for _, ch := range s.Channels() {
		for _, tc := range []struct {
			name string
			ids  []any
		}{
			{"duplicate", []any{a["id"], a["id"]}},
			{"empty", []any{}},
			{"too many", tooMany},
		} {
			r := ch.Call(t, owner, "get_customers", map[string]any{"ids": tc.ids})
			if r.Status != 422 {
				t.Fatalf("%s %s: got %d %v, want 422", ch.Name, tc.name, r.Status, r.Body)
			}
			if ch.Name == "ai" {
				if !strings.HasPrefix(r.Detail(), "invalid arguments: ") {
					t.Fatalf("%s %s: detail %q", ch.Name, tc.name, r.Detail())
				}
			} else if r.Detail() != "validation failed" {
				t.Fatalf("%s %s: detail %q", ch.Name, tc.name, r.Detail())
			}
		}
	}
}

func TestGetCustomersQueryCount(t *testing.T) {
	s := open(t)
	source := s.call("create_customer_source", map[string]any{"name": "名册来源"}, 200)
	ids := []any{}
	for range 20 {
		c := s.create(map[string]any{"name": "名册计数", "source_id": source["id"]})
		s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{c["id"]}, "staff_id": s.Res.OwnerID}, 200)
		ids = append(ids, c["id"])
	}
	counter := &queryCounter{}
	cfg := s.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		counts := []int64{}
		for _, n := range []int{1, 20} {
			counter.all.Store(0)
			r := ch.Call(t, caller, "get_customers", map[string]any{"ids": ids[:n]})
			assertEqual(t, r.Status, 200)
			assertEqual(t, len(r.Body["items"].([]any)), n)
			counts = append(counts, counter.all.Load())
		}
		t.Logf("%s get_customers N=1: %d SQL; N=20: %d SQL", ch.Name, counts[0], counts[1])
		assertEqual(t, counts[0], counts[1])
	}
}
