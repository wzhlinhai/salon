package performance

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// PaidItemStaffChangeInput 是已结账单的一次改人：改前改后的有效项目行、原结账快照和冲回营业日。
type PaidItemStaffChangeInput struct {
	OrderID            uuid.UUID
	OrderItemID        uuid.UUID
	StaffChangeID      uuid.UUID
	CheckoutID         uuid.UUID
	SettingsSnapshotID uuid.UUID
	BusinessDate       time.Time
	Rules              settings.Rules
	BeforeLines        []Line
	AfterLines         []Line
	Designated         bool
	Gender             string
}

// RecordPaidItemStaffChange 冲回这一项目行当前生效的业绩和客数，并按结账快照给新员工重新记录。
func RecordPaidItemStaffChange(ctx context.Context, env ops.Env, in PaidItemStaffChangeInput) error {
	member := false
	row, err := env.Q.OrderPerformanceMember(ctx, dbgen.OrderPerformanceMemberParams{StoreID: env.Caller.StoreID, OrderID: &in.OrderID})
	if err == nil {
		member = row
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err := env.Q.ReversePaidItemPerformance(ctx, dbgen.ReversePaidItemPerformanceParams{
		StoreID: env.Caller.StoreID, OrderItemID: &in.OrderItemID, StaffChangeID: &in.StaffChangeID,
		Day: pgtype.Date{Time: in.BusinessDate, Valid: true},
	}); err != nil {
		return err
	}
	entries, err := checkoutEntries(ctx, env, in.OrderID, in.AfterLines, in.Rules)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.orderItemID != in.OrderItemID {
			continue
		}
		if err := env.Q.AddStaffChangePerformanceEntry(ctx, dbgen.AddStaffChangePerformanceEntryParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
			BusinessDate: pgtype.Date{Time: in.BusinessDate, Valid: true},
			OrderID:      &in.OrderID, OrderItemID: &e.orderItemID, CheckoutID: &in.CheckoutID, SettingsSnapshotID: &in.SettingsSnapshotID,
			Slot: e.slot, PositionID: e.positionID, StaffID: e.staffID,
			Member: &member, Kind: e.kind, Method: e.method, PaidAmount: e.paidAmount, CommissionPaidAmount: e.commissionPaidAmount,
			Ratio: e.ratio, CategoryRatio: e.categoryRatio, Amount: e.amount, CommissionAmount: e.commissionAmount,
			StaffChangeID: &in.StaffChangeID,
		}); err != nil {
			return err
		}
	}
	before := splitGuestVisit(in.BeforeLines, in.Rules.GuestCountSplit())
	after := splitGuestVisit(in.AfterLines, in.Rules.GuestCountSplit())
	if sameGuestShares(before, after) {
		return nil
	}
	if err := env.Q.ReverseStaffChangeGuestVisits(ctx, dbgen.ReverseStaffChangeGuestVisitsParams{
		StoreID: env.Caller.StoreID, OrderID: in.OrderID, StaffChangeID: &in.StaffChangeID,
		Day: pgtype.Date{Time: in.BusinessDate, Valid: true},
	}); err != nil {
		return err
	}
	for _, share := range after {
		if err := env.Q.AddStaffChangeGuestVisit(ctx, dbgen.AddStaffChangeGuestVisitParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
			BusinessDate: pgtype.Date{Time: in.BusinessDate, Valid: true},
			OrderID:      in.OrderID, CheckoutID: in.CheckoutID, SettingsSnapshotID: in.SettingsSnapshotID,
			StaffID: share.staffID, Share: guestShareNumeric(share.units), Designated: in.Designated, Gender: in.Gender,
			StaffChangeID: &in.StaffChangeID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func sameGuestShares(a, b []guestShare) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[uuid.UUID]int64{}
	for _, share := range a {
		if share.staffID != nil {
			counts[*share.staffID] += share.units
		}
	}
	for _, share := range b {
		if share.staffID == nil {
			if len(counts) != 0 {
				return false
			}
			continue
		}
		counts[*share.staffID] -= share.units
	}
	for _, units := range counts {
		if units != 0 {
			return false
		}
	}
	return true
}
