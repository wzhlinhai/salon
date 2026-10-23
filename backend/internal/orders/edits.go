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
	ops.Register(r, ops.Operation[RepriceOrderItemInput, Order]{Name: "reprice_order_item", Summary: "改价并记录原因", Kind: ops.Write, Permission: perm.OrderReprice, AI: ops.AIDraft, Handle: repriceOrderItem})
}

func removeOrderItem(ctx context.Context, env ops.Env, in *RemoveOrderItemInput) (*Order, error) {
	return changeItem(ctx, env, "remove_order_item", in.OrderID, in.OrderItemID, func(_ dbgen.Order, item dbgen.OrderItem) error {
		return env.Q.RemoveOrderItem(ctx, dbgen.RemoveOrderItemParams{ID: item.ID, RemovedBy: &env.Caller.StaffID})
	})
}

func sameStaff(a, b *uuid.UUID) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func setOrderItemStaff(ctx context.Context, env ops.Env, in *SetOrderItemStaffInput) (*Order, error) {
	return changeItem(ctx, env, "set_order_item_staff", in.OrderID, in.OrderItemID, func(order dbgen.Order, item dbgen.OrderItem) error {
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
		rules, err := settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, order.SettingsSnapshotID)
		if err != nil {
			return err
		}
		assigned, price, err := catalog.QuoteLine(ctx, env, item.ItemID, slots, lineStaff(in.Staff), &catalog.PreviousLinePrice{Source: item.PriceSource, Staff: previousStaff}, rules)
		if err != nil {
			return err
		}
		if price != nil {
			if err := env.Q.RequoteOrderItem(ctx, dbgen.RequoteOrderItemParams{ID: item.ID, Price: price.Price, PriceSource: price.Source, LevelID: price.LevelID}); err != nil {
				return err
			}
		}
		for _, p := range positions {
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
