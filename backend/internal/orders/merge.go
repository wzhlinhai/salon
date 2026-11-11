package orders

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
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
	after, err := change(ctx, env, "merge_orders", "open", ids, func(rows []dbgen.Order) error {
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
		redemptions, err := env.Q.LockOrderRedemptions(ctx, dbgen.LockOrderRedemptionsParams{StoreID: env.Caller.StoreID, OrderIds: in.MergedOrderIDs})
		if err != nil {
			return err
		}
		if err := env.Q.MoveOrderRedemptions(ctx, dbgen.MoveOrderRedemptionsParams{StoreID: env.Caller.StoreID, OrderID: &kept.ID, MergedOrderIds: in.MergedOrderIDs}); err != nil {
			return err
		}
		for _, row := range redemptions {
			before := mergeRedemptionView(row)
			row.OrderID = &kept.ID
			after := mergeRedemptionView(row)
			if err := audit.Record(ctx, env, "merge_orders", audit.CouponRedemptionTarget(row.ID), before, after); err != nil {
				return err
			}
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
	}, env.Now)
	if err != nil {
		return nil, err
	}
	return &after[0], nil
}

type mergeRedemptionAudit struct {
	ID             uuid.UUID     `json:"id"`
	Platform       string        `json:"platform"`
	CouponCode     string        `json:"coupon_code"`
	Status         string        `json:"status"`
	OrderID        *uuid.UUID    `json:"order_id" nullable:"true"`
	DealID         *uuid.UUID    `json:"deal_id" nullable:"true"`
	PurchasePrice  *money.Amount `json:"purchase_price" nullable:"true"`
	StorePrice     *money.Amount `json:"store_price" nullable:"true"`
	LinkedAt       *time.Time    `json:"linked_at" nullable:"true"`
	OrderPaymentID *uuid.UUID    `json:"order_payment_id" nullable:"true"`
}

func mergeRedemptionView(row dbgen.LockOrderRedemptionsRow) mergeRedemptionAudit {
	out := mergeRedemptionAudit{ID: row.ID, Platform: row.Platform, CouponCode: row.CouponCode, Status: row.Status, OrderID: row.OrderID, DealID: row.DealID, PurchasePrice: row.PurchasePrice, StorePrice: row.StorePrice, OrderPaymentID: row.OrderPaymentID}
	if row.LinkedAt.Valid {
		out.LinkedAt = &row.LinkedAt.Time
	}
	return out
}
