package groupbuy

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func registerOpenGroupbuyOrder(r *ops.Registry) {
	ops.Register(r, ops.Operation[OpenGroupbuyOrderInput, OpenGroupbuyOrderOutput]{Name: "open_groupbuy_order", Summary: "按已核销且未关联的团购券开单并关联", Kind: ops.Write, Permission: perm.OrderCreate, AlsoRequires: []ops.Permission{perm.GroupBuyRedeem}, AI: ops.AIDraft, Handle: openGroupbuyOrder})
	r.SetDraftPreview("open_groupbuy_order", previewOpenGroupbuyOrder)
}

func previewOpenGroupbuyOrder(ctx context.Context, env ops.Env, params json.RawMessage) (any, error) {
	var in OpenGroupbuyOrderInput
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	deal, err := env.Q.GetGroupbuyDeal(ctx, dbgen.GetGroupbuyDealParams{StoreID: env.Caller.StoreID, ID: in.DealID})
	if err != nil {
		return nil, dealError(err)
	}
	if err := checkDealActive(deal); err != nil {
		return nil, err
	}
	if err := checkItemsCoverDeal(in.Items, deal.ItemID); err != nil {
		return nil, err
	}
	redemption, err := env.Q.GetRedemptionByID(ctx, dbgen.GetRedemptionByIDParams{StoreID: env.Caller.StoreID, ID: in.RedemptionID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.ErrNotFound
		}
		return nil, err
	}
	if err := checkRedemptionLinkable(redemption); err != nil {
		return nil, err
	}
	return orders.PreviewOpenOrder(ctx, env, orders.OpenOrderInput{CustomerID: in.CustomerID, Gender: in.Gender, Designated: in.Designated, Items: in.Items})
}

func openGroupbuyOrder(ctx context.Context, env ops.Env, in *OpenGroupbuyOrderInput) (*OpenGroupbuyOrderOutput, error) {
	// 券 → 团购商品的锁序与 link_coupon_order 一致；单据是本次事务新开的。
	locked, err := env.Q.LockRedemptionByID(ctx, dbgen.LockRedemptionByIDParams{StoreID: env.Caller.StoreID, ID: in.RedemptionID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.ErrNotFound
		}
		return nil, err
	}
	if err := checkRedemptionLinkable(locked); err != nil {
		return nil, err
	}
	deal, err := env.Q.LockGroupbuyDeal(ctx, dbgen.LockGroupbuyDealParams{StoreID: env.Caller.StoreID, ID: in.DealID})
	if err != nil {
		return nil, dealError(err)
	}
	if err := checkDealActive(deal); err != nil {
		return nil, err
	}
	if err := checkItemsCoverDeal(in.Items, deal.ItemID); err != nil {
		return nil, err
	}
	order, err := orders.OpenOrder(ctx, env, &orders.OpenOrderInput{CustomerID: in.CustomerID, Gender: in.Gender, Designated: in.Designated, Items: in.Items})
	if err != nil {
		return nil, err
	}
	redemption, err := linkCouponOrder(ctx, env, &LinkCouponOrderInput{RedemptionID: in.RedemptionID, DealID: in.DealID, OrderID: order.ID})
	if err != nil {
		return nil, err
	}
	linked, err := orders.GetOrder(ctx, env, &orders.GetOrderInput{ID: order.ID})
	if err != nil {
		return nil, err
	}
	return &OpenGroupbuyOrderOutput{Order: *linked, Redemption: *redemption}, nil
}

func checkDealActive(deal dbgen.GroupbuyDeal) error {
	if !deal.Active {
		return ops.Rule("团购商品已停用")
	}
	return nil
}

func checkItemsCoverDeal(items []orders.OrderItemInput, dealItemID uuid.UUID) error {
	for _, item := range items {
		if item.ItemID == dealItemID {
			return nil
		}
	}
	return ops.Rule("开单项目必须包含团购商品对应的项目")
}

func checkRedemptionLinkable(row dbgen.CouponRedemption) error {
	if row.Status != "redeemed" {
		return ops.Rule("只有已核销的团购券可以关联订单")
	}
	if row.OrderID != nil {
		return ops.Rule("团购券已关联其他单")
	}
	return nil
}
