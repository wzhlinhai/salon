package audit_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestLogCursorPagination(t *testing.T) {
	s := optest.OpenShop(t)
	caller := s.MustCaller(s.Owner)
	ctx := context.Background()
	otherStore, otherCursor := uuid.New(), uuid.New()
	if _, err := s.Pool.Exec(ctx, "INSERT INTO stores (id,tenant_id,name) VALUES ($1,$2,'另一门店')", otherStore, s.Res.TenantID); err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	expected := []string{}
	visible, hidden := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{visible, hidden} {
		var responsible *uuid.UUID
		if id == visible {
			responsible = &s.Res.OwnerID
		}
		if _, err := tx.Exec(ctx, "INSERT INTO customers (id,tenant_id,store_id,created_by,responsible_staff_id) VALUES ($1,$2,$3,$4,$5)", id, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, responsible); err != nil {
			t.Fatal(err)
		}
	}
	caller.Owner = false
	caller.Permissions = map[ops.Permission]ops.Grant{perm.AuditLogView: {}, perm.CustomerView: {Scope: ops.ScopeSelf}}
	for i := range 5 {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1))
		expected = append([]string{id.String()}, expected...)
		at := time.Date(2026, 1, 1, 0, 0, i/2, 0, time.UTC)
		for _, target := range []uuid.UUID{visible, hidden} {
			logID := id
			logAt := at
			if target == hidden {
				logID = uuid.New()
				logAt = at.Add(time.Millisecond)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO audit_log (id,tenant_id,store_id,action,target_id,actor_id,at) VALUES ($1,$2,$3,'pagination',$4,$5,$6)", logID, s.Res.TenantID, s.Res.StoreID, target, s.Res.OwnerID, logAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO audit_log (id,tenant_id,store_id,action,target_id,actor_id,at) VALUES ($1,$2,$3,'other',$4,$4,'2027-01-01')", otherCursor, s.Res.TenantID, otherStore, s.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			args := map[string]any{"limit": 2}
			seen := []string{}
			var previousTime, previousID string
			for _, size := range []int{2, 2, 1} {
				r := ch.Call(t, caller, "list_audit_log", args)
				if r.Status != 200 {
					t.Fatalf("page: %+v", r)
				}
				entries := r.Body["entries"].([]any)
				if len(entries) != size {
					t.Fatalf("page size = %d, want %d", len(entries), size)
				}
				for _, item := range entries {
					entry := item.(map[string]any)
					id, at := entry["id"].(string), entry["at"].(string)
					if previousTime != "" && (at > previousTime || (at == previousTime && id >= previousID)) {
						t.Fatalf("order: %s %s after %s %s", at, id, previousTime, previousID)
					}
					previousTime, previousID = at, id
					seen = append(seen, id)
					args["before_id"] = id
					if entry["target_id"] != visible.String() {
						t.Fatal(entry)
					}
				}
			}
			if fmt.Sprint(seen) != fmt.Sprint(expected) {
				t.Fatalf("IDs = %v, want %v", seen, expected)
			}
			for _, cursor := range []string{previousID, uuid.NewString(), otherCursor.String()} {
				r := ch.Call(t, caller, "list_audit_log", map[string]any{"limit": 2, "before_id": cursor})
				if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
					t.Fatalf("empty page: %+v", r)
				}
			}
			r := ch.Call(t, caller, "list_audit_log", map[string]any{"limit": 201})
			if r.Status != 422 {
				t.Fatalf("maximum: %+v", r)
			}
		})
	}
}
