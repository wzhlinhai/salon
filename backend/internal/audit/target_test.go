package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestAuditTargetConstraints(t *testing.T) {
	s := optest.OpenShop(t)
	r := s.HTTPWithToken(t, s.Owner, "create_customer", map[string]any{})
	if r.Status != 200 {
		t.Fatal(r)
	}
	customerID := uuid.MustParse(r.Body["customer"].(map[string]any)["id"].(string))
	for _, tc := range []struct {
		name, kind, constraint string
		target                 uuid.UUID
		customer               *uuid.UUID
	}{
		{"missing customer", "customer", "audit_log_customer_presence_check", customerID, nil},
		{"different customer", "customer", "audit_log_customer_target_check", uuid.New(), &customerID},
		{"unexpected customer", "staff", "audit_log_customer_presence_check", customerID, &customerID},
		{"unknown kind", "unknown", "audit_log_target_kind_check", uuid.New(), nil},
		{"missing reference", "customer", "audit_log_customer_fk", uuid.Nil, &uuid.Nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), "INSERT INTO audit_log (tenant_id,store_id,action,target_kind,target_id,customer_id,actor_id) VALUES ($1,$2,'constraint',$3,$4,$5,$6)", s.Res.TenantID, s.Res.StoreID, tc.kind, tc.target, tc.customer, s.Res.OwnerID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.ConstraintName != tc.constraint {
				t.Fatalf("insert = %v, want constraint %s", err, tc.constraint)
			}
		})
	}
}
