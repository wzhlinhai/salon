package orders

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
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
		positions, err := env.Q.ListOrderItemPositions(ctx, dbgen.ListOrderItemPositionsParams{StoreID: env.Caller.StoreID, ItemIds: []uuid.UUID{item.ID}})
		if err != nil {
			return err
		}
		slots := map[int16]bool{}
		previousStaff := map[int16]*uuid.UUID{}
		for _, p := range positions {
			slots[p.Slot] = true
			previousStaff[p.Slot] = p.StaffID
		}
		rules, err := settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, row.SettingsSnapshotID)
		if err != nil {
			return err
		}
		// 已结账的单不重新计价：把改前价来源当作手工价，只做岗位和员工校验。
		assigned, _, err := catalog.QuoteLine(ctx, env, item.ItemID, slots, lineStaff(in.Staff), &catalog.PreviousLinePrice{Source: "manual", Staff: previousStaff}, rules)
		if err != nil {
			return err
		}
		changed := false
		for _, p := range positions {
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
		before := staffList(positions)
		after := assignedStaffList(positions, assigned)
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
		for _, p := range positions {
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
		afterLines := orderPerformanceLines(*current)
		beforeOrder := *current
		beforeOrder.Items = append([]OrderItem(nil), current.Items...)
		for i := range beforeOrder.Items {
			if beforeOrder.Items[i].ID == item.ID {
				beforeOrder.Items[i].Positions = positionViews(positions)
			}
		}
		checkoutID := uuid.UUID{}
		if row.CheckoutID != nil {
			checkoutID = *row.CheckoutID
		}
		return performance.RecordPaidItemStaffChange(ctx, env, performance.PaidItemStaffChangeInput{
			OrderID: row.ID, OrderItemID: item.ID, StaffChangeID: change.ID, CheckoutID: checkoutID,
			SettingsSnapshotID: row.SettingsSnapshotID, BusinessDate: today, Rules: rules,
			BeforeLines: orderPerformanceLines(beforeOrder), AfterLines: afterLines,
			Designated: row.Designated, Gender: row.Gender,
		})
	}, env.Now, guardToday)
	if err != nil {
		return nil, err
	}
	return &after[0], nil
}

func staffList(positions []dbgen.OrderItemPosition) []OrderItemStaffInput {
	out := []OrderItemStaffInput{}
	for _, p := range positions {
		if p.StaffID != nil {
			out = append(out, OrderItemStaffInput{Slot: p.Slot, StaffID: *p.StaffID})
		}
	}
	return out
}

func assignedStaffList(positions []dbgen.OrderItemPosition, assigned map[int16]*uuid.UUID) []OrderItemStaffInput {
	out := []OrderItemStaffInput{}
	for _, p := range positions {
		if assigned[p.Slot] != nil {
			out = append(out, OrderItemStaffInput{Slot: p.Slot, StaffID: *assigned[p.Slot]})
		}
	}
	return out
}

func positionViews(positions []dbgen.OrderItemPosition) []OrderItemPosition {
	out := make([]OrderItemPosition, len(positions))
	for i, p := range positions {
		out[i] = OrderItemPosition{Slot: p.Slot, PositionID: p.PositionID, Required: p.Required, StaffID: p.StaffID, SharePercent: p.SharePercent}
	}
	return out
}

func orderPerformanceLines(order Order) []performance.Line {
	out := []performance.Line{}
	for _, line := range order.Items {
		if line.RemovedAt != nil {
			continue
		}
		net, _ := line.Price.Apply(money.Amount{}.Sub(line.Discount))
		slots := make([]performance.Slot, 0, len(line.Positions))
		for _, p := range line.Positions {
			slots = append(slots, performance.Slot{Slot: p.Slot, PositionID: p.PositionID, StaffID: p.StaffID, SharePercent: p.SharePercent})
		}
		categoryRatio := line.CategoryRatio
		out = append(out, performance.Line{OrderItemID: line.ID, Net: net, Price: line.Price, LaborFeeAmount: line.LaborFeeAmount, LaborFeePercent: line.LaborFeePercent, CategoryRatio: &categoryRatio, Slots: slots})
	}
	return out
}
