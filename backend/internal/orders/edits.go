package orders

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/audit"
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

func checkEdit(ctx context.Context, env ops.Env, order dbgen.Order, now time.Time) error {
	g := env.Caller.Grant(perm.OrderEdit)
	if g.WindowDays != nil {
		today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
		if err != nil {
			return err
		}
		if order.BusinessDate.Time.Before(today.AddDate(0, 0, -*g.WindowDays)) {
			return ops.ErrForbidden
		}
	}
	_, self := env.Caller.ScopeFilter(perm.OrderEdit)
	if self != nil {
		involved, err := env.Q.OrderInvolvesStaff(ctx, dbgen.OrderInvolvesStaffParams{StoreID: env.Caller.StoreID, ID: order.ID, StaffID: *self})
		if err != nil {
			return err
		}
		if !involved {
			return ops.ErrForbidden
		}
	}
	return nil
}

// editView 在单锁内读取改前和项目行；所有编辑都沿用同一条读取路径。
func editView(ctx context.Context, env ops.Env, orderID, itemID uuid.UUID, guarded bool) (dbgen.Order, *Order, dbgen.OrderItem, error) {
	order, err := lockOpenOrder(ctx, env, orderID)
	if err != nil {
		return order, nil, dbgen.OrderItem{}, rowError(err)
	}
	if guarded {
		if err := checkEdit(ctx, env, order, time.Now()); err != nil {
			return order, nil, dbgen.OrderItem{}, err
		}
	}
	before, err := view(ctx, env, order)
	if err != nil {
		return order, nil, dbgen.OrderItem{}, err
	}
	item, err := env.Q.GetOrderItem(ctx, dbgen.GetOrderItemParams{StoreID: env.Caller.StoreID, OrderID: order.ID, ID: itemID})
	return order, before, item, rowError(err)
}

func removeOrderItem(ctx context.Context, env ops.Env, in *RemoveOrderItemInput) (*Order, error) {
	order, before, item, err := editView(ctx, env, in.OrderID, in.OrderItemID, true)
	if err != nil {
		return nil, err
	}
	if item.RemovedAt.Valid {
		return nil, ops.Rule("项目已去掉")
	}
	if err := env.Q.RemoveOrderItem(ctx, dbgen.RemoveOrderItemParams{ID: item.ID, RemovedBy: &env.Caller.StaffID}); err != nil {
		return nil, err
	}
	after, err := view(ctx, env, order)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "remove_order_item", audit.OrderTarget(order.ID, order.CustomerID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

func sameStaff(a, b *uuid.UUID) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func setOrderItemStaff(ctx context.Context, env ops.Env, in *SetOrderItemStaffInput) (*Order, error) {
	order, before, item, err := editView(ctx, env, in.OrderID, in.OrderItemID, true)
	if err != nil {
		return nil, err
	}
	if item.RemovedAt.Valid {
		return nil, ops.Rule("项目已去掉")
	}
	positions, err := env.Q.ListOrderItemPositions(ctx, dbgen.ListOrderItemPositionsParams{StoreID: env.Caller.StoreID, ItemIds: []uuid.UUID{item.ID}})
	if err != nil {
		return nil, err
	}
	slots := map[int16]bool{}
	var oldFirst, newFirst *uuid.UUID
	for _, p := range positions {
		slots[p.Slot] = true
		if p.Slot == 1 {
			oldFirst = p.StaffID
		}
	}
	for _, s := range in.Staff {
		if s.Slot == 1 {
			newFirst = new(s.StaffID)
		}
	}
	requote := item.PriceSource != "manual" && !sameStaff(oldFirst, newFirst)
	// 需要重取价格时先锁项目，再由共享校验按 UUID 锁员工。
	if requote {
		if _, err := env.Q.ShareOrderItem(ctx, dbgen.ShareOrderItemParams{StoreID: env.Caller.StoreID, ID: item.ItemID}); err != nil {
			return nil, rowError(err)
		}
	}
	assigned, err := validateStaff(ctx, env, slots, in.Staff)
	if err != nil {
		return nil, err
	}
	if requote {
		rules, err := settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, order.SettingsSnapshotID)
		if err != nil {
			return nil, err
		}
		price, err := catalog.Quote(ctx, env.Q, env.Caller.StoreID, item.ItemID, assigned[1], nil, rules)
		if err != nil {
			return nil, err
		}
		if err := env.Q.RequoteOrderItem(ctx, dbgen.RequoteOrderItemParams{ID: item.ID, Price: price.Price, PriceSource: price.Source, LevelID: price.LevelID}); err != nil {
			return nil, err
		}
	}
	for _, p := range positions {
		if !sameStaff(p.StaffID, assigned[p.Slot]) {
			if err := env.Q.SetOrderItemStaff(ctx, dbgen.SetOrderItemStaffParams{OrderItemID: item.ID, Slot: p.Slot, StaffID: assigned[p.Slot]}); err != nil {
				return nil, err
			}
		}
	}
	after, err := view(ctx, env, order)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "set_order_item_staff", audit.OrderTarget(order.ID, order.CustomerID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

func repriceOrderItem(ctx context.Context, env ops.Env, in *RepriceOrderItemInput) (*Order, error) {
	order, before, item, err := editView(ctx, env, in.OrderID, in.OrderItemID, false)
	if err != nil {
		return nil, err
	}
	if item.RemovedAt.Valid {
		return nil, ops.Rule("项目已去掉")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(in.Reason) < 1 || utf8.RuneCountInString(in.Reason) > 200 {
		return nil, ops.Rule("原因须为一到二百字")
	}
	if err := env.Q.RepriceOrderItem(ctx, dbgen.RepriceOrderItemParams{ID: item.ID, Price: in.Price, RepriceReason: &in.Reason}); err != nil {
		return nil, err
	}
	after, err := view(ctx, env, order)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "reprice_order_item", audit.OrderTarget(order.ID, order.CustomerID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}
