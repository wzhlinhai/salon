package groupbuy

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func registerLink(r *ops.Registry) {
	ops.Register(r, ops.Operation[LinkCouponOrderInput, Redemption]{Name: "link_coupon_order", Summary: "把已核销的团购券关联到挂单，记录平台购买价和门店标价", Kind: ops.Write, Permission: perm.GroupBuyRedeem, AI: ops.AIDraft, Handle: linkCouponOrder})
	ops.Register(r, ops.Operation[UnlinkCouponOrderInput, Redemption]{Name: "unlink_coupon_order", Summary: "取消团购券与挂单的关联", Kind: ops.Write, Permission: perm.GroupBuyRedeem, AI: ops.AIDraft, Handle: unlinkCouponOrder})
}

func requireOrderCreate(caller ops.Caller) error {
	if !caller.Has(perm.OrderCreate) {
		return ops.ErrForbidden
	}
	return nil
}

func linkCouponOrder(ctx context.Context, env ops.Env, in *LinkCouponOrderInput) (*Redemption, error) {
	if err := requireOrderCreate(env.Caller); err != nil {
		return nil, err
	}
	var before, after *Redemption
	_, err := orders.ChangeOpenOrder(ctx, env, "link_coupon_order", in.OrderID, func(*dbgen.Order) error {
		row, err := env.Q.LockRedemptionByID(ctx, dbgen.LockRedemptionByIDParams{StoreID: env.Caller.StoreID, ID: in.RedemptionID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ops.ErrNotFound
			}
			return err
		}
		if row.Status != "redeemed" {
			return ops.Rule("只有已核销的团购券可以关联订单")
		}
		if row.OrderID != nil {
			return ops.Rule("团购券已关联其他单")
		}
		deal, item, err := dealForLink(ctx, env, in.DealID, row.Platform)
		if err != nil {
			return err
		}
		before = view(row)
		linked, err := env.Q.LinkRedemption(ctx, dbgen.LinkRedemptionParams{StoreID: env.Caller.StoreID, ID: row.ID, OrderID: &in.OrderID, DealID: &deal.ID, PurchasePrice: &deal.PurchasePrice, StorePrice: &item.Price, LinkedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}})
		if err != nil {
			return err
		}
		after = view(linked)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "link_coupon_order", audit.CouponRedemptionTarget(after.ID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

func unlinkCouponOrder(ctx context.Context, env ops.Env, in *UnlinkCouponOrderInput) (*Redemption, error) {
	if err := requireOrderCreate(env.Caller); err != nil {
		return nil, err
	}
	row, err := env.Q.GetRedemptionByID(ctx, dbgen.GetRedemptionByIDParams{StoreID: env.Caller.StoreID, ID: in.RedemptionID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.ErrNotFound
		}
		return nil, err
	}
	if row.Status != "redeemed" {
		return nil, ops.Rule("只有已核销的团购券可以取消关联")
	}
	if row.OrderID == nil {
		return nil, ops.Rule("团购券没有关联订单")
	}
	orderID := *row.OrderID
	var before, after *Redemption
	_, err = orders.ChangeOpenOrder(ctx, env, "unlink_coupon_order", orderID, func(*dbgen.Order) error {
		current, err := env.Q.LockRedemptionByID(ctx, dbgen.LockRedemptionByIDParams{StoreID: env.Caller.StoreID, ID: in.RedemptionID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ops.ErrNotFound
			}
			return err
		}
		if current.OrderID == nil {
			return ops.Rule("团购券没有关联订单")
		}
		if *current.OrderID != orderID {
			return ops.Rule("团购券已关联其他单")
		}
		before = view(current)
		released, err := env.Q.UnlinkRedemption(ctx, dbgen.UnlinkRedemptionParams{StoreID: env.Caller.StoreID, ID: in.RedemptionID})
		if err != nil {
			return err
		}
		after = view(released)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "unlink_coupon_order", audit.CouponRedemptionTarget(after.ID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

// linkedOrderRule 拒绝撤销仍关联在挂单或已结账单上的团购券。
func linkedOrderRule(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, row dbgen.CouponRedemption) error {
	if row.OrderID == nil {
		return nil
	}
	order, err := q.GetOrder(ctx, dbgen.GetOrderParams{StoreID: storeID, ID: *row.OrderID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if order.Status == "open" || order.Status == "paid" {
		return ops.Rule("团购券已关联订单，请先取消关联或撤单")
	}
	return nil
}
