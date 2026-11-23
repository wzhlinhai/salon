package orders

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func registerEdits(r *ops.Registry) {
	ops.Register(r, ops.Operation[RemoveOrderItemInput, Order]{Name: "remove_order_item", Summary: "去掉项目并保留记录", Kind: ops.Write, Permission: perm.OrderEdit, AI: ops.AIDraft, Handle: removeOrderItem})
	ops.Register(r, ops.Operation[SetOrderItemStaffInput, Order]{Name: "set_order_item_staff", Summary: "更换项目岗位员工", Kind: ops.Write, Permission: perm.OrderEdit, AI: ops.AIDraft, Handle: setOrderItemStaff})
	ops.Register(r, ops.Operation[SetPaidItemStaffInput, Order]{Name: "set_paid_item_staff", Summary: "已结账的单更换项目岗位员工，业绩和提成改记给新员工", Kind: ops.Write, Permission: perm.OrderEdit, AI: ops.AIDraft, Handle: setPaidItemStaff})
	ops.Register(r, ops.Operation[RepriceOrderItemInput, Order]{Name: "reprice_order_item", Summary: "改价并记录原因", Kind: ops.Write, Permission: perm.OrderReprice, AI: ops.AIDraft, Handle: repriceOrderItem})
}

func removeOrderItem(ctx context.Context, env ops.Env, in *RemoveOrderItemInput) (*Order, error) {
	return changeItem(ctx, env, "remove_order_item", in.OrderID, in.OrderItemID, func(_ dbgen.Order, item dbgen.OrderItem) error {
		return env.Q.RemoveOrderItem(ctx, dbgen.RemoveOrderItemParams{ID: item.ID, RemovedBy: &env.Caller.StaffID})
	})
}

func sameStaff(a, b *uuid.UUID) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// lineStaffing 是项目行现存的岗位、岗位员工和该单的规则快照。
type lineStaffing struct {
	positions []dbgen.OrderItemPosition
	slots     map[int16]bool
	previous  map[int16]*uuid.UUID
	rules     settings.Rules
}

// loadLineStaffing 读取项目行的岗位、各岗位当前员工和结账时留存的规则快照。
func loadLineStaffing(ctx context.Context, env ops.Env, order dbgen.Order, item dbgen.OrderItem) (*lineStaffing, error) {
	positions, err := env.Q.ListOrderItemPositions(ctx, dbgen.ListOrderItemPositionsParams{StoreID: env.Caller.StoreID, ItemIds: []uuid.UUID{item.ID}})
	if err != nil {
		return nil, err
	}
	out := &lineStaffing{positions: positions, slots: map[int16]bool{}, previous: map[int16]*uuid.UUID{}}
	for _, p := range positions {
		out.slots[p.Slot] = true
		out.previous[p.Slot] = p.StaffID
	}
	out.rules, err = settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, order.SettingsSnapshotID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func setOrderItemStaff(ctx context.Context, env ops.Env, in *SetOrderItemStaffInput) (*Order, error) {
	return changeItem(ctx, env, "set_order_item_staff", in.OrderID, in.OrderItemID, func(order dbgen.Order, item dbgen.OrderItem) error {
		staffing, err := loadLineStaffing(ctx, env, order, item)
		if err != nil {
			return err
		}
		assigned, price, err := catalog.QuoteLine(ctx, env, item.ItemID, staffing.slots, lineStaff(in.Staff), &catalog.PreviousLinePrice{Source: item.PriceSource, Staff: staffing.previous}, staffing.rules)
		if err != nil {
			return err
		}
		if price != nil {
			if err := env.Q.RequoteOrderItem(ctx, dbgen.RequoteOrderItemParams{ID: item.ID, Price: price.Price, PriceSource: price.Source, LevelID: price.LevelID}); err != nil {
				return err
			}
		}
		for _, p := range staffing.positions {
			if !sameStaff(p.StaffID, assigned[p.Slot]) {
				if err := env.Q.SetOrderItemStaff(ctx, dbgen.SetOrderItemStaffParams{OrderItemID: item.ID, Slot: p.Slot, StaffID: assigned[p.Slot]}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func repriceOrderItem(ctx context.Context, env ops.Env, in *RepriceOrderItemInput) (*Order, error) {
	return changeItem(ctx, env, "reprice_order_item", in.OrderID, in.OrderItemID, func(_ dbgen.Order, item dbgen.OrderItem) error {
		in.Reason = strings.TrimSpace(in.Reason)
		if utf8.RuneCountInString(in.Reason) < 1 || utf8.RuneCountInString(in.Reason) > 200 {
			return ops.Rule("原因须为一到二百字")
		}
		if err := env.Q.RepriceOrderItem(ctx, dbgen.RepriceOrderItemParams{ID: item.ID, Price: in.Price, RepriceReason: &in.Reason}); err != nil {
			return err
		}
		return nil
	})
}
