package orders

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func setPaidItemStaff(ctx context.Context, env ops.Env, in *SetPaidItemStaffInput) (*Order, error) {
	var today time.Time
	guardToday := func(ctx context.Context, env ops.Env, _ []dbgen.Order) (time.Time, error) {
		day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
		today = day
		return day, err
	}
	after, err := changeWithDay(ctx, env, "set_paid_item_staff", "paid", []uuid.UUID{in.OrderID}, func(rows []dbgen.Order) error {
		row := &rows[0]
		blocked, err := env.Q.HasBlockingAftersale(ctx, dbgen.HasBlockingAftersaleParams{StoreID: env.Caller.StoreID, OrderID: row.ID})
		if err != nil {
			return err
		}
		if blocked {
			return ops.Rule("这张单有售后单，不能再改做的人")
		}
		item, err := env.Q.GetOrderItem(ctx, dbgen.GetOrderItemParams{StoreID: env.Caller.StoreID, OrderID: row.ID, ID: in.OrderItemID})
		if err != nil {
			return rowError(err)
		}
		if item.RemovedAt.Valid {
			return ops.Rule("项目已去掉")
		}
		staffing, err := loadLineStaffing(ctx, env, *row, item)
		if err != nil {
			return err
		}
		// 已结账的单不重新计价：把改前价来源当作手工价，只做岗位和员工校验。
		assigned, _, err := catalog.QuoteLine(ctx, env, item.ItemID, staffing.slots, lineStaff(in.Staff), &catalog.PreviousLinePrice{Source: "manual", Staff: staffing.previous}, staffing.rules)
		if err != nil {
			return err
		}
		changed := false
		for _, p := range staffing.positions {
			if p.Required && assigned[p.Slot] == nil {
				return ops.Rule("必填岗位还没填人")
			}
			if !sameStaff(p.StaffID, assigned[p.Slot]) {
				changed = true
			}
		}
		if !changed {
			return ops.Rule("做的人没有变化")
		}
		before := staffSlots(staffing.positions, staffing.previous)
		after := staffSlots(staffing.positions, assigned)
		beforeJSON, err := json.Marshal(before)
		if err != nil {
			return err
		}
		afterJSON, err := json.Marshal(after)
		if err != nil {
			return err
		}
		change, err := env.Q.CreateOrderStaffChange(ctx, dbgen.CreateOrderStaffChangeParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, OrderID: row.ID, OrderItemID: item.ID,
			BusinessDate: pgtype.Date{Time: today, Valid: true}, BeforeStaff: beforeJSON, AfterStaff: afterJSON, ChangedBy: env.Caller.StaffID,
		})
		if err != nil {
			return err
		}
		for _, p := range staffing.positions {
			if !sameStaff(p.StaffID, assigned[p.Slot]) {
				if err := env.Q.SetOrderItemStaff(ctx, dbgen.SetOrderItemStaffParams{OrderItemID: item.ID, Slot: p.Slot, StaffID: assigned[p.Slot]}); err != nil {
					return err
				}
			}
		}
		current, err := view(ctx, env, *row)
		if err != nil {
			return err
		}
		afterLines := performanceLines(*current)
		beforeOrder := *current
		beforeOrder.Items = append([]OrderItem(nil), current.Items...)
		for i := range beforeOrder.Items {
			if beforeOrder.Items[i].ID == item.ID {
				beforeOrder.Items[i].Positions = positionViews(staffing.positions)
			}
		}
		checkoutID := uuid.UUID{}
		if row.CheckoutID != nil {
			checkoutID = *row.CheckoutID
		}
		return performance.RecordPaidItemStaffChange(ctx, env, performance.PaidItemStaffChangeInput{
			OrderID: row.ID, OrderItemID: item.ID, StaffChangeID: change.ID, CheckoutID: checkoutID,
			SettingsSnapshotID: row.SettingsSnapshotID, BusinessDate: today, Rules: staffing.rules,
			BeforeLines: performanceLines(beforeOrder), AfterLines: afterLines,
			Designated: row.Designated, Gender: row.Gender,
		})
	}, env.Now, guardToday)
	if err != nil {
		return nil, err
	}
	return &after[0], nil
}

// staffSlots 按项目行的全部岗位输出员工，空岗位的 StaffID 为 null。
func staffSlots(positions []dbgen.OrderItemPosition, assigned map[int16]*uuid.UUID) []OrderStaffChangeSlot {
	out := make([]OrderStaffChangeSlot, 0, len(positions))
	for _, p := range positions {
		out = append(out, OrderStaffChangeSlot{Slot: p.Slot, StaffID: assigned[p.Slot]})
	}
	return out
}
