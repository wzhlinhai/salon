package payments

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (p *Checkout) recordCard(ctx context.Context, payment Input, rules settings.Rules) (uuid.UUID, error) {
	return p.cards[*payment.CardID].Consume(ctx, p.env, payment.Amount, rules.CardDeductionOrder(), p.day)
}
func reverseCard(ctx context.Context, env ops.Env, used map[uuid.UUID]*cards.PaymentCard, orderID, voidID uuid.UUID, day time.Time) error {
	balances, err := env.Q.VoidBalanceConsumes(ctx, dbgen.VoidBalanceConsumesParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return err
	}
	for _, consume := range balances {
		if err := used[consume.CardID].RestoreBalance(ctx, env, consume, voidID, day); err != nil {
			return err
		}
	}
	return nil
}
func (p *Checkout) recordCount(ctx context.Context, payment Input, itemID uuid.UUID, rules settings.Rules) (uuid.UUID, error) {
	return p.cards[*payment.CardID].ConsumeCount(ctx, p.env, itemID, rules.CardCountOrder(), p.day)
}
func reverseCount(ctx context.Context, env ops.Env, used map[uuid.UUID]*cards.PaymentCard, orderID, voidID uuid.UUID, day time.Time) error {
	counts, err := env.Q.VoidCountConsumes(ctx, dbgen.VoidCountConsumesParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return err
	}
	for _, consume := range counts {
		if err := used[consume.CardID].RestoreCount(ctx, env, consume, voidID, day); err != nil {
			return err
		}
	}
	return nil
}
