package cards

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// PreviewCardRefundInput 指定整张退卡的卡。
type PreviewCardRefundInput struct {
	CardID uuid.UUID `json:"card_id"`
}

// RefundCardInput 是已确认的报价金额及退款方式。
type RefundCardInput struct {
	CardID uuid.UUID    `json:"card_id"`
	Method string       `json:"method" enum:"cash,unionpay,wechat,alipay"`
	Amount money.Amount `json:"amount"`
}

// CardRefundItem 是购买次数按随卡单次金额折算的退款。
type CardRefundItem struct {
	Item          CardCountItem `json:"item"`
	PaidRemaining int32         `json:"paid_remaining"`
	UnitPrice     money.Amount  `json:"unit_price"`
	Amount        money.Amount  `json:"amount"`
}

// CardRefundQuote 是按退款当日规则算出的整卡退款报价。
type CardRefundQuote struct {
	CardID        uuid.UUID        `json:"card_id"`
	Principal     money.Amount     `json:"principal"`
	Bonus         money.Amount     `json:"bonus"`
	CountsAmount  money.Amount     `json:"counts_amount"`
	Items         []CardRefundItem `json:"items"`
	BonusPolicy   string           `json:"bonus_policy"`
	BonusDeducted money.Amount     `json:"bonus_deducted"`
	FeePercent    int              `json:"fee_percent"`
	Fee           money.Amount     `json:"fee"`
	Amount        money.Amount     `json:"amount"`
}

// CardRefund 是整卡退款记录和退卡后的卡视图。
type CardRefund struct {
	CardRefundQuote
	ID           uuid.UUID `json:"id"`
	Method       string    `json:"method"`
	BusinessDate string    `json:"business_date"`
	RefundedBy   uuid.UUID `json:"refunded_by"`
	At           time.Time `json:"at"`
	Card         Card      `json:"card"`
}

func registerRefunds(r *ops.Registry) {
	ops.Register(r, ops.Operation[PreviewCardRefundInput, CardRefundQuote]{Name: "preview_card_refund", Summary: "试算整卡退款", Kind: ops.Read, Permission: perm.CardAdjust, AI: ops.AIDirect, Handle: previewCardRefund})
	ops.Register(r, ops.Operation[RefundCardInput, CardRefund]{Name: "refund_card", Summary: "登记整卡退款", Kind: ops.Write, Permission: perm.CardAdjust, AI: ops.AIDraft, Handle: refundCard})
}

func previewCardRefund(ctx context.Context, env ops.Env, in *PreviewCardRefundInput) (*CardRefundQuote, error) {
	return previewCardRefundAt(ctx, env, in, time.Now())
}

func previewCardRefundAt(ctx context.Context, env ops.Env, in *PreviewCardRefundInput, now time.Time) (*CardRefundQuote, error) {
	row, err := env.Q.GetCard(ctx, dbgen.GetCardParams{StoreID: env.Caller.StoreID, ID: in.CardID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := customers.CheckViewable(ctx, env, row.CustomerID); err != nil {
		return nil, err
	}
	if row.Status == "refunded" {
		return nil, ops.Rule("卡已退")
	}
	card, err := cardView(ctx, env, row.ID)
	if err != nil {
		return nil, err
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
	if err != nil {
		return nil, err
	}
	return refundQuote(ctx, env, card, day)
}

func refundQuote(ctx context.Context, env ops.Env, card *Card, day time.Time) (*CardRefundQuote, error) {
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return nil, err
	}
	out := &CardRefundQuote{CardID: card.ID, Principal: card.Principal, Bonus: card.Bonus, Items: []CardRefundItem{}, BonusPolicy: rules.CardRefundBonus(), FeePercent: rules.CardRefundFeePercent()}
	var counts money.Total
	for _, item := range card.Items {
		product := money.Total{}.AddProduct(item.UnitPrice, int64(item.PaidRemaining))
		var amount money.Amount
		if err := amount.Scan(product.Format()); err != nil {
			return nil, ops.Rule("退款金额超过上限")
		}
		out.Items = append(out.Items, CardRefundItem{Item: CardCountItem{ID: item.ItemID, Name: item.ItemName}, PaidRemaining: item.PaidRemaining, UnitPrice: item.UnitPrice, Amount: amount})
		counts = counts.AddTotal(product)
	}
	if err := out.CountsAmount.Scan(counts.Format()); err != nil {
		return nil, ops.Rule("退款金额超过上限")
	}
	refundable := money.Total{}.AddProduct(out.Principal, 1).AddProduct(out.CountsAmount, 1)
	if out.BonusPolicy == "deduct_used" {
		used, err := env.Q.CardUsedBonus(ctx, card.ID)
		if err != nil {
			return nil, err
		}
		var deduction money.Total
		if err := deduction.Scan(used); err != nil {
			return nil, err
		}
		if deduction.CmpTotal(refundable) > 0 {
			deduction = refundable
		}
		if err := out.BonusDeducted.Scan(deduction.Format()); err != nil {
			return nil, ops.Rule("退款金额超过上限")
		}
	}
	gross := refundable.Deduct(out.BonusDeducted)
	if err := out.Fee.Scan(gross.PercentOf(out.FeePercent).Format()); err != nil {
		return nil, ops.Rule("退款金额超过上限")
	}
	if err := out.Amount.Scan(gross.Deduct(out.Fee).Format()); err != nil {
		return nil, ops.Rule("退款金额超过上限")
	}
	return out, nil
}

func refundCard(ctx context.Context, env ops.Env, in *RefundCardInput) (*CardRefund, error) {
	return refundCardAt(ctx, env, in, time.Now())
}

func refundCardAt(ctx context.Context, env ops.Env, in *RefundCardInput, now time.Time) (*CardRefund, error) {
	var out CardRefund
	card, err := changeOne(ctx, env, "refund_card", in.CardID, func(c cardChange, day time.Time) error {
		quote, err := refundQuote(ctx, env, c.before, day)
		if err != nil {
			return err
		}
		if quote.Amount.Cmp(in.Amount) != 0 {
			return ops.Rule("退款金额已变化，请重新确认")
		}
		row, err := env.Q.AddCardRefund(ctx, dbgen.AddCardRefundParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: in.CardID, Method: in.Method, Principal: quote.Principal, Bonus: quote.Bonus, CountsAmount: quote.CountsAmount, BonusPolicy: quote.BonusPolicy, BonusDeducted: quote.BonusDeducted, FeePercent: int32(quote.FeePercent), Fee: quote.Fee, Amount: quote.Amount, BusinessDate: pgtype.Date{Time: day, Valid: true}, RefundedBy: env.Caller.StaffID})
		if err != nil {
			return err
		}
		if _, err := writeBalance(ctx, env, in.CardID, "refund", c.before.Principal, c.before.Bonus, money.Amount{}.Sub(c.before.Principal), money.Amount{}.Sub(c.before.Bonus), day, nil, nil, &row.ID); err != nil {
			return err
		}
		for _, item := range c.before.Items {
			if err := env.Q.AddCardCountEntry(ctx, dbgen.AddCardCountEntryParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: in.CardID, ItemID: item.ItemID, Kind: "refund", PaidBefore: item.PaidRemaining, PaidDelta: -item.PaidRemaining, PaidAfter: 0, GiftBefore: item.GiftRemaining, GiftDelta: -item.GiftRemaining, GiftAfter: 0, RefundID: &row.ID, BusinessDate: row.BusinessDate, ActorID: env.Caller.StaffID}); err != nil {
				return err
			}
		}
		if err := env.Q.MarkCardRefunded(ctx, in.CardID); err != nil {
			return err
		}
		out = CardRefund{CardRefundQuote: *quote, ID: row.ID, Method: row.Method, BusinessDate: day.Format(time.DateOnly), RefundedBy: row.RefundedBy, At: row.At.Time}
		return nil
	}, now)
	if err != nil {
		return nil, err
	}
	out.Card = *card
	return &out, nil
}
