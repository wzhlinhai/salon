package storeai

import (
	"context"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// EnsureInbox 按需建立门店收件箱（staffID 为空）或员工收件箱，并发调用也只有一行。
func EnsureInbox(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, staffID *uuid.UUID) (dbgen.AiInbox, error) {
	tenantID, err := q.StoreTenant(ctx, storeID)
	if err != nil {
		return dbgen.AiInbox{}, err
	}
	if staffID == nil {
		if err := q.InsertStoreInbox(ctx, dbgen.InsertStoreInboxParams{
			TenantID: tenantID, StoreID: storeID,
		}); err != nil {
			return dbgen.AiInbox{}, err
		}
		return q.GetStoreInbox(ctx, dbgen.GetStoreInboxParams{TenantID: tenantID, StoreID: storeID})
	}
	if err := q.InsertStaffInbox(ctx, dbgen.InsertStaffInboxParams{
		TenantID: tenantID, StoreID: storeID, StaffID: staffID,
	}); err != nil {
		return dbgen.AiInbox{}, err
	}
	return q.GetStaffInbox(ctx, dbgen.GetStaffInboxParams{TenantID: tenantID, StoreID: storeID, StaffID: staffID})
}
