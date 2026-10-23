package orders

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func lockOpenOrder(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.Order, error) {
	row, err := env.Q.LockOrder(ctx, dbgen.LockOrderParams{StoreID: env.Caller.StoreID, ID: id})
	if err != nil {
		return row, rowError(err)
	}
	if row.Status != "open" {
		if row.Status == "merged" {
			return row, ops.Rule("单已合并，请在合并后的单上操作")
		}
		return row, ops.Rule("单当前状态不允许操作")
	}
	return row, nil
}

func registerMerge(r *ops.Registry) {
	ops.Register(r, ops.Operation[MergeOrdersInput, Order]{Name: "merge_orders", Summary: "合并同一顾客同一营业日的挂单", Kind: ops.Write, Permission: perm.OrderEdit, AI: ops.AIDraft, Handle: mergeOrders})
}

func mergeOrders(ctx context.Context, env ops.Env, in *MergeOrdersInput) (*Order, error) {
	ids := []uuid.UUID{in.OrderID}
	seen := map[uuid.UUID]bool{}
	for _, id := range in.MergedOrderIDs {
		if id == in.OrderID {
			return nil, ops.Rule("保留的单不能并入自身")
		}
		if seen[id] {
			return nil, ops.Rule("并入的单不能重复")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	// 与其他合并始终按相同顺序锁单，不锁顾客、项目或员工。
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	rows := make([]dbgen.Order, 0, len(ids))
	var kept dbgen.Order
	for _, id := range ids {
		row, err := lockOpenOrder(ctx, env, id)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		if id == in.OrderID {
			kept = row
		}
	}
	before, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, row := range rows {
		if err := checkEdit(ctx, env, row, now); err != nil {
			return nil, err
		}
	}
	for _, row := range rows {
		if row.CustomerID != kept.CustomerID {
			return nil, ops.Rule("不同顾客的单不能合并")
		}
		if !row.BusinessDate.Time.Equal(kept.BusinessDate.Time) {
			return nil, ops.Rule("不同营业日的单不能合并")
		}
	}
	if err := env.Q.MoveMergedOrderItems(ctx, dbgen.MoveMergedOrderItemsParams{StoreID: env.Caller.StoreID, OrderID: kept.ID, MergedOrderIds: in.MergedOrderIDs}); err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].ID == kept.ID {
			continue
		}
		if err := env.Q.MarkOrderMerged(ctx, dbgen.MarkOrderMergedParams{StoreID: env.Caller.StoreID, ID: rows[i].ID, MergedInto: &kept.ID}); err != nil {
			return nil, err
		}
		rows[i].Status = "merged"
		rows[i].MergedInto = &kept.ID
	}
	after, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	var out *Order
	for i, row := range rows {
		if row.ID == kept.ID {
			out = &after[i]
		}
		if err := audit.Record(ctx, env, "merge_orders", audit.OrderTarget(row.ID, row.CustomerID), before[i], after[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
