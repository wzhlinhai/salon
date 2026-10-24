package orders

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func registerPreview(r *ops.Registry) {
	ops.Register(r, ops.Operation[PreviewCheckoutInput, Order]{Name: "preview_checkout", Summary: "按计价卡试算一张挂单的价格、持卡折扣和实收", Kind: ops.Read, Permission: perm.PaymentCollect, AI: ops.AIDirect, Handle: previewCheckout})
}

func previewCheckout(ctx context.Context, env ops.Env, in *PreviewCheckoutInput) (*Order, error) {
	row, err := env.Q.GetOrder(ctx, dbgen.GetOrderParams{StoreID: env.Caller.StoreID, ID: in.OrderID})
	if err != nil {
		return nil, rowError(err)
	}
	if row.Status == "paid" {
		return nil, ops.Rule("单已结账")
	}
	if row.Status == "merged" {
		return nil, ops.Rule("单已合并，请在合并后的单上操作")
	}
	order, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	rules, err := settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, row.SettingsSnapshotID)
	if err != nil {
		return nil, err
	}
	var typeID *uuid.UUID
	typeIDs := []uuid.UUID{}
	if in.CardID != nil {
		day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
		if err != nil {
			return nil, err
		}
		used, err := cards.PaymentCards(ctx, env, []uuid.UUID{*in.CardID}, day, false)
		if err != nil {
			return nil, err
		}
		card := used[*in.CardID]
		if err := card.CheckHolder(row.CustomerID, rules); err != nil {
			return nil, err
		}
		typeID = new(card.CardTypeID())
		typeIDs = append(typeIDs, *typeID)
	}
	types, lines, members, err := pricingData(ctx, env, []Order{*order}, typeIDs, false)
	if err != nil {
		return nil, err
	}
	var percent *money.Percent
	var prices map[uuid.UUID]money.Amount
	if typeID != nil {
		percent = types[*typeID].DiscountPercent
		prices = members[*typeID]
	}
	out := priceCheckout(*order, pricingLines(lines, prices), typeID, percent, rules.MemberPriceRule())
	out.Payments = []OrderPayment{}
	return &out, nil
}
