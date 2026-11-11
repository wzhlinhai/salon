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
	ops.Register(r, ops.Operation[PreviewCheckoutInput, PreviewCheckout]{Name: "preview_checkout", Summary: "按计价卡试算一张挂单的价格、持卡折扣和实收", Kind: ops.Read, Permission: perm.PaymentCollect, AI: ops.AIDirect, Handle: previewCheckout})
}

func previewCheckout(ctx context.Context, env ops.Env, in *PreviewCheckoutInput) (*PreviewCheckout, error) {
	if in.AutoCard && in.CardID != nil {
		return nil, ops.Rule("指定了卡就不用按规则选卡")
	}
	row, err := env.Q.GetOrder(ctx, dbgen.GetOrderParams{StoreID: env.Caller.StoreID, ID: in.OrderID})
	if err != nil {
		return nil, rowError(err)
	}
	if row.Status == "voided" {
		return nil, ops.Rule("单已撤")
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
	if in.AutoCard && rules.CardSelection() == "manual" {
		return nil, ops.Rule("门店是手动选卡，请指定卡")
	}
	var day time.Time
	if in.CardID != nil || in.AutoCard {
		day, err = settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
		if err != nil {
			return nil, err
		}
	}
	candidates := []uuid.UUID{}
	used := map[uuid.UUID]*cards.PaymentCard{}
	if in.AutoCard {
		candidates, err = cards.SelectionCandidates(ctx, env, row.CustomerID, nil)
		if err != nil {
			return nil, err
		}
		used, err = cards.PaymentCards(ctx, env, nil, day, candidates...)
		if err != nil {
			return nil, err
		}
	}
	typeIDs := []uuid.UUID{}
	if in.CardID != nil {
		used, err = cards.PaymentCards(ctx, env, []uuid.UUID{*in.CardID}, day)
		if err != nil {
			return nil, err
		}
		typeIDs = append(typeIDs, used[*in.CardID].CardTypeID())
	}
	snapshots := make(map[uuid.UUID]cards.CardSnapshot, len(used))
	for id, card := range used {
		snapshots[id] = card.Snapshot()
	}
	candidates = usableCandidates(candidates, snapshots, row.CustomerID, day)
	for _, id := range candidates {
		typeIDs = append(typeIDs, used[id].CardTypeID())
	}
	types, lines, members, err := pricingData(ctx, env, []Order{*order}, typeIDs, false)
	if err != nil {
		return nil, err
	}
	state := checkoutState{cards: snapshots, discounts: map[uuid.UUID]*money.Percent{}, lines: lines, members: members, day: day}
	for id, cardType := range types {
		state.discounts[id] = cardType.DiscountPercent
	}
	out, pricingCardID, err := quoteOrder(state, checkoutOrder{order: *order, rules: checkoutRules{rules.CardSelection(), rules.MemberPriceRule(), rules.OthersCardPayment()}, candidates: candidates}, in.CardID, in.AutoCard)
	if err != nil {
		return nil, err
	}
	redemptions, err := env.Q.ListOrderRedemptions(ctx, dbgen.ListOrderRedemptionsParams{StoreID: env.Caller.StoreID, OrderIds: []uuid.UUID{row.ID}})
	if err != nil {
		return nil, err
	}
	groupbuyLines := make([]checkoutGroupbuy, 0, len(redemptions))
	for _, redemption := range redemptions {
		if redemption.PurchasePrice == nil || redemption.StorePrice == nil {
			continue
		}
		groupbuyLines = append(groupbuyLines, checkoutGroupbuy{RedemptionID: redemption.ID, DealName: redemption.DealName, Platform: redemption.Platform, PurchasePrice: *redemption.PurchasePrice, StorePrice: *redemption.StorePrice})
	}
	var net money.Amount
	if err := net.Scan(out.Net); err != nil {
		return nil, err
	}
	paid, _ := groupbuyPayments(groupbuyLines, net)
	out.Payments = make([]OrderPayment, 0, len(paid))
	for _, payment := range paid {
		out.Payments = append(out.Payments, OrderPayment{Method: payment.Method, Amount: payment.Amount, CouponRedemptionID: payment.CouponRedemptionID})
	}
	return &PreviewCheckout{Order: out, PricingCardID: pricingCardID}, nil
}
