package orders

import (
	"context"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

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
	return change(ctx, env, "merge_orders", ids, func(rows []dbgen.Order) error {
		kept := rows[0]
		for _, row := range rows {
			if row.CustomerID != kept.CustomerID {
				return ops.Rule("不同顾客的单不能合并")
			}
			if !row.BusinessDate.Time.Equal(kept.BusinessDate.Time) {
				return ops.Rule("不同营业日的单不能合并")
			}
		}
		if err := env.Q.MoveMergedOrderItems(ctx, dbgen.MoveMergedOrderItemsParams{StoreID: env.Caller.StoreID, OrderID: kept.ID, MergedOrderIds: in.MergedOrderIDs}); err != nil {
			return err
		}
		for i := range rows {
			if rows[i].ID == kept.ID {
				continue
			}
			if err := env.Q.MarkOrderMerged(ctx, dbgen.MarkOrderMergedParams{StoreID: env.Caller.StoreID, ID: rows[i].ID, MergedInto: &kept.ID}); err != nil {
				return err
			}
			rows[i].Status = "merged"
			rows[i].MergedInto = &kept.ID
		}
		return nil
	})
}
