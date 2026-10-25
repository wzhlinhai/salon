package customers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestCheckViewableBatch(t *testing.T) {
	s := open(t)
	staffID, token := s.Hire("员工", "002", "stylist", s.Role("发型师"))
	valid := s.create(map[string]any{})
	hidden := s.create(map[string]any{})
	merged := s.create(map[string]any{})
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{valid["id"]}, "staff_id": staffID}, 200)
	s.call("merge_customers", map[string]any{"keep_id": valid["id"], "merged_id": merged["id"]}, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	store, err := q.CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "别店"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := q.CreateCustomer(context.Background(), dbgen.CreateCustomerParams{TenantID: s.Res.TenantID, StoreID: store.ID, CreatedBy: s.Res.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	owner := s.MustCaller(s.Owner)
	self := s.MustCaller(token)
	self.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, tc := range []struct {
		name   string
		caller ops.Caller
		ids    []uuid.UUID
		want   error
	}{
		{"empty", owner, nil, nil},
		{"duplicates", owner, []uuid.UUID{aID(valid), aID(hidden), aID(valid)}, nil},
		{"self", self, []uuid.UUID{aID(valid), aID(valid)}, nil},
		{"absent", owner, []uuid.UUID{aID(valid), uuid.New()}, ops.ErrNotFound},
		{"store", owner, []uuid.UUID{aID(valid), foreign.ID}, ops.ErrNotFound},
		{"scope", self, []uuid.UUID{aID(valid), aID(hidden)}, ops.ErrNotFound},
		{"merged", owner, []uuid.UUID{aID(valid), aID(merged)}, ops.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 2 {
				err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
					return customers.CheckViewable(context.Background(), ops.Env{Q: q, Caller: tc.caller}, tc.ids...)
				})
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
				for i, j := 0, len(tc.ids)-1; i < j; i, j = i+1, j-1 {
					tc.ids[i], tc.ids[j] = tc.ids[j], tc.ids[i]
				}
			}
		})
	}
}
