package performance

import (
	"context"
	"math/big"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type guestShare struct {
	staffID *uuid.UUID
	units   int64
}

// splitGuestVisit 接收按行序排列的有效项目行，以万分之一客次分配。
func splitGuestVisit(lines []Line, split string) []guestShare {
	ids := []uuid.UUID{}
	for _, line := range lines {
		slots := slices.Clone(line.Slots)
		slices.SortFunc(slots, func(a, b Slot) int { return int(a.Slot) - int(b.Slot) })
		for _, slot := range slots {
			if slot.StaffID == nil || slices.Contains(ids, *slot.StaffID) {
				continue
			}
			ids = append(ids, *slot.StaffID)
			if split == "first_slot" {
				return []guestShare{{staffID: slot.StaffID, units: 10000}}
			}
		}
	}
	if len(ids) == 0 {
		return []guestShare{{units: 10000}}
	}
	out := make([]guestShare, len(ids))
	share := int64(10000 / len(ids))
	for i := range ids {
		out[i] = guestShare{staffID: &ids[i], units: share}
	}
	out[0].units += 10000 - share*int64(len(ids))
	return out
}

func recordGuestVisits(ctx context.Context, env ops.Env, in CheckoutInput) error {
	for _, share := range splitGuestVisit(in.Lines, in.Rules.GuestCountSplit()) {
		if err := env.Q.AddGuestVisit(ctx, dbgen.AddGuestVisitParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
			BusinessDate: pgtype.Date{Time: in.BusinessDate, Valid: true}, OrderID: in.OrderID,
			CheckoutID: in.CheckoutID, SettingsSnapshotID: in.SettingsSnapshotID,
			StaffID: share.staffID, Share: pgtype.Numeric{Int: big.NewInt(share.units), Exp: -4, Valid: true},
			Designated: in.Designated, Gender: in.Gender,
		}); err != nil {
			return err
		}
	}
	return nil
}
