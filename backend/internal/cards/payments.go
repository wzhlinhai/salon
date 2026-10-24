package cards

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// PaymentCard 是结账持有锁的卡及其在本次结账中的当前余额。
type PaymentCard struct {
	row              dbgen.Card
	before           *Card
	principal, bonus money.Amount
}

// CardTypeID 返回计价卡的卡类型。
func (c *PaymentCard) CardTypeID() uuid.UUID { return c.row.CardTypeID }

// CheckHolder 按单的规则快照检查是否允许用别人的卡。
func (c *PaymentCard) CheckHolder(customerID uuid.UUID, rules settings.Rules) error {
	if c.row.CustomerID != customerID && rules.OthersCardPayment() != "allowed" {
		return ops.Rule("不能用别人的卡")
	}
	return nil
}

// PaymentCards 按 UUID 锁卡并检查查看范围、种类和营业日有效期；试算时只读，不加锁。
func PaymentCards(ctx context.Context, env ops.Env, ids []uuid.UUID, day time.Time, lock bool) (map[uuid.UUID]*PaymentCard, error) {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	out := map[uuid.UUID]*PaymentCard{}
	for _, id := range slices.Compact(ordered) {
		var row dbgen.Card
		var err error
		if lock {
			row, err = env.Q.LockCard(ctx, dbgen.LockCardParams{StoreID: env.Caller.StoreID, ID: id})
		} else {
			row, err = env.Q.GetCard(ctx, dbgen.GetCardParams{StoreID: env.Caller.StoreID, ID: id})
		}
		if err != nil {
			return nil, rowError(err)
		}
		if err := canView(ctx, env, row.CustomerID); err != nil {
			return nil, err
		}
		before, err := cardView(ctx, env, id)
		if err != nil {
			return nil, err
		}
		if before.Kind == CountCard {
			return nil, ops.Rule("次卡不能用卡余额支付")
		}
		if row.ExpiresOn.Valid && row.ExpiresOn.Time.Before(day) {
			return nil, ops.Rule("卡已过期")
		}
		out[id] = &PaymentCard{row: row, before: before, principal: before.Principal, bonus: before.Bonus}
	}
	return out, nil
}

// Consume 按扣款顺序追加一条消费流水，接续本次结账中同卡的前一笔余额。
func (c *PaymentCard) Consume(ctx context.Context, env ops.Env, amount money.Amount, order string, day time.Time) (uuid.UUID, error) {
	first, second := c.principal, c.bonus
	if order == "bonus_first" {
		first, second = second, first
	}
	take := amount
	if take.Cmp(first) > 0 {
		take = first
	}
	rest, err := amount.Apply(money.Amount{}.Sub(take))
	if err != nil {
		return uuid.Nil, err
	}
	if rest.Cmp(second) > 0 {
		return uuid.Nil, ops.Rule("卡余额不足")
	}
	principal, bonus := take, rest
	if order == "bonus_first" {
		principal, bonus = bonus, principal
	}
	ledger, err := writeBalance(ctx, env, c.row.ID, "consume", c.principal, c.bonus, money.Amount{}.Sub(principal), money.Amount{}.Sub(bonus), day)
	if err != nil {
		return uuid.Nil, err
	}
	c.principal, c.bonus = ledger.PrincipalAfter, ledger.BonusAfter
	return ledger.ID, nil
}

// AuditPaymentCards 为本次结账用过的每张卡记录一次改前改后。
func AuditPaymentCards(ctx context.Context, env ops.Env, used map[uuid.UUID]*PaymentCard) error {
	ids := make([]uuid.UUID, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, id := range ids {
		c := used[id]
		if _, err := recordChange(ctx, env, "checkout_orders", c.row, c.before); err != nil {
			return err
		}
	}
	return nil
}
