package cards

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// RestoreBalance 按消费流水原扣款拆分退回本金和赠送金，接续当前余额。
func (c *PaymentCard) RestoreBalance(ctx context.Context, env ops.Env, consume dbgen.CardLedger, voidID uuid.UUID, day time.Time) error {
	ledger, err := writeBalance(ctx, env, c.row.ID, "void", c.principal, c.bonus, consume.PrincipalBefore.Sub(consume.PrincipalAfter), consume.BonusBefore.Sub(consume.BonusAfter), day, &consume.ID, &voidID, nil)
	if err != nil {
		return err
	}
	c.principal, c.bonus = ledger.PrincipalAfter, ledger.BonusAfter
	c.charged = true
	return nil
}

// RestoreCount 按消费流水原扣的购买或赠送次数退回，并关联反向单。
func (c *PaymentCard) RestoreCount(ctx context.Context, env ops.Env, consume dbgen.CardCountLedger, voidID uuid.UUID, day time.Time) error {
	rows, err := env.Q.RemainingCounts(ctx, []uuid.UUID{c.row.ID})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.CardItem.ItemID != consume.ItemID {
			continue
		}
		paid, gift := -consume.PaidDelta, -consume.GiftDelta
		_, err := writeCount(ctx, env, c.row.ID, consume.ItemID, "void", row.PaidAfter, row.GiftAfter, paid, gift, day, nil, &consume.ID, &voidID, nil)
		if err != nil {
			return err
		}
		c.charged = true
		return nil
	}
	return ops.Rule("卡上没有这个项目")
}
