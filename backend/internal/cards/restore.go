package cards

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// LockRestores 按 UUID 锁住待退回的卡，撤单不检查持卡顾客查看范围或有效期。
func LockRestores(ctx context.Context, env ops.Env, ids []uuid.UUID) (map[uuid.UUID]*PaymentCard, error) {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	used := map[uuid.UUID]*PaymentCard{}
	for _, id := range slices.Compact(ordered) {
		row, err := env.Q.LockCard(ctx, dbgen.LockCardParams{StoreID: env.Caller.StoreID, ID: id})
		if err != nil {
			return nil, rowError(err)
		}
		if row.Status == "refunded" {
			return nil, ops.Rule("卡已退，不能撤单退回")
		}
		before, err := cardView(ctx, env, id)
		if err != nil {
			return nil, err
		}
		used[id] = &PaymentCard{row: row, before: before, principal: before.Principal, bonus: before.Bonus}
	}
	return used, nil
}

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
		err := env.Q.AddCardCountEntry(ctx, dbgen.AddCardCountEntryParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: c.row.ID, ItemID: consume.ItemID, Kind: "void", PaidBefore: row.PaidAfter, PaidDelta: paid, PaidAfter: row.PaidAfter + paid, GiftBefore: row.GiftAfter, GiftDelta: gift, GiftAfter: row.GiftAfter + gift, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID, ReversesID: &consume.ID, OrderVoidID: &voidID})
		if err != nil {
			return err
		}
		c.charged = true
		return nil
	}
	return ops.Rule("卡上没有这个项目")
}

// AuditRestores 为每张退回余额或次数的卡记录一次撤单日志。
func AuditRestores(ctx context.Context, env ops.Env, used map[uuid.UUID]*PaymentCard) error {
	ids := make([]uuid.UUID, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, id := range ids {
		c := used[id]
		if !c.charged {
			continue
		}
		if _, err := recordChange(ctx, env, "void_order", c.row, c.before); err != nil {
			return err
		}
	}
	return nil
}
