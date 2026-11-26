package audit_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestRecordRequiresActor(t *testing.T) {
	err := audit.Record(context.Background(), ops.Env{Caller: ops.Caller{TenantID: uuid.New(), StoreID: uuid.New()}}, "system_probe", audit.StaffTarget(uuid.New()), nil, nil)
	if err == nil || err.Error() != "audit: caller has no actor" {
		t.Fatalf("err = %v, want audit: caller has no actor", err)
	}
}
