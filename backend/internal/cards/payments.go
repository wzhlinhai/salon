package cards

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// PaymentCard 是结账或撤单持有锁的卡及其当前余额。
type PaymentCard struct {
	row              dbgen.Card
	charged          bool
	before           *Card
	principal, bonus money.Amount
}

// CardTypeID 返回计价卡的卡类型。
func (c *PaymentCard) CardTypeID() uuid.UUID { return c.row.CardTypeID }

// PaymentCards 只读试算卡及候选卡，检查查看范围和显式卡状态。
func PaymentCards(ctx context.Context, env ops.Env, ids []uuid.UUID, day time.Time, candidates ...uuid.UUID) (map[uuid.UUID]*PaymentCard, error) {
	return readPaymentCards(ctx, env, ids, day, candidates)
}

// LockPayments 锁住支付用卡，撤单只检查已退状态，结账检查查看范围及显式卡有效期。
func LockPayments(ctx context.Context, env ops.Env, ids, candidates []uuid.UUID, day time.Time, reverse bool) (map[uuid.UUID]*PaymentCard, error) {
	ordered := append(slices.Clone(ids), candidates...)
	out, err := lockCards(ctx, env, ordered)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, id := range slices.Compact(ordered) {
		row := out[id].row
		if reverse {
			if row.Status == "refunded" {
				return nil, ops.Rule("卡已退，不能撤单退回")
			}
			continue
		}
		if err := customers.CheckViewable(ctx, env, row.CustomerID); err != nil {
			return nil, err
		}
		if !slices.Contains(candidates, id) || slices.Contains(ids, id) {
			if row.Status == "refunded" {
				return nil, ops.Rule("卡已退")
			}
			if err := checkExpiry(row, day); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// readPaymentCards 按批读取试算卡及顾客、余额、积分和次数，不加业务行锁。
func readPaymentCards(ctx context.Context, env ops.Env, ids []uuid.UUID, day time.Time, candidates []uuid.UUID) (map[uuid.UUID]*PaymentCard, error) {
	all := append(slices.Clone(ids), candidates...)
	rows, err := env.Q.CardViews(ctx, dbgen.CardViewsParams{StoreID: env.Caller.StoreID, Ids: all})
	if err != nil {
		return nil, err
	}
	holders := make([]uuid.UUID, len(rows))
	views := make([]Card, len(rows))
	for i, r := range rows {
		holders[i] = r.Card.CustomerID
		views[i] = view(r.Card, r.CardTypeName, r.PrincipalAfter, r.BonusAfter)
		views[i].Kind = r.Kind
	}
	if err := customers.CheckViewable(ctx, env, holders...); err != nil {
		return nil, err
	}
	if err := fillCounts(ctx, env, views); err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]*PaymentCard, len(rows))
	for i, r := range rows {
		before := &views[i]
		out[r.Card.ID] = &PaymentCard{row: r.Card, before: before, principal: before.Principal, bonus: before.Bonus}
	}
	for _, id := range all {
		_, exists := out[id]
		if !exists {
			return nil, ops.ErrNotFound
		}
		if !slices.Contains(candidates, id) || slices.Contains(ids, id) {
			if out[id].row.Status == "refunded" {
				return nil, ops.Rule("卡已退")
			}
			if err := checkExpiry(out[id].row, day); err != nil {
				return nil, err
			}
		}
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
	ledger, err := writeBalance(ctx, env, c.row.ID, "consume", c.principal, c.bonus, money.Amount{}.Sub(principal), money.Amount{}.Sub(bonus), day, nil, nil, nil)
	if err != nil {
		return uuid.Nil, err
	}
	c.principal, c.bonus = ledger.PrincipalAfter, ledger.BonusAfter
	c.charged = true
	return ledger.ID, nil
}

// Audit 为本次支付或冲回用过的每张卡记录一次改前改后。
func Audit(ctx context.Context, env ops.Env, action string, used map[uuid.UUID]*PaymentCard) error {
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
		if _, err := recordChange(ctx, env, action, c.row, c.before); err != nil {
			return err
		}
	}
	return nil
}

// ConsumeCount 在已持卡锁下读取当前次数并按快照顺序扣一次，接续同卡同项目的前笔流水。
func (c *PaymentCard) ConsumeCount(ctx context.Context, env ops.Env, itemID uuid.UUID, order string, day time.Time) (uuid.UUID, error) {
	rows, err := env.Q.RemainingCounts(ctx, []uuid.UUID{c.row.ID})
	if err != nil {
		return uuid.Nil, err
	}
	for _, row := range rows {
		if row.CardItem.ItemID != itemID {
			continue
		}
		paid, gift := row.PaidAfter, row.GiftAfter
		if paid == 0 && gift == 0 {
			return uuid.Nil, ops.Rule("次数不足")
		}
		var pd, gd int32
		if order == "paid_first" && paid > 0 || order == "gift_first" && gift == 0 {
			pd = -1
		} else {
			gd = -1
		}
		c.charged = true
		return writeCount(ctx, env, c.row.ID, itemID, "consume", paid, gift, pd, gd, day, nil, nil, nil, nil)
	}
	return uuid.Nil, ops.Rule("卡上没有这个项目")
}

func checkExpiry(row dbgen.Card, day time.Time) error {
	if row.ExpiresOn.Valid && row.ExpiresOn.Time.Before(day) {
		return ops.Rule("卡已过期")
	}
	return nil
}

// Balance 返回本次结账中卡的当前本金与赠送金合计。
func (c *PaymentCard) Balance() money.Total {
	return money.Total{}.AddProduct(c.principal, 1).AddProduct(c.bonus, 1)
}

// SelectionCandidates 只列出本店顾客的候选卡 ID，使用前须在锁后重新检查。
func SelectionCandidates(ctx context.Context, env ops.Env, customerID uuid.UUID, excluded []uuid.UUID) ([]uuid.UUID, error) {
	if err := customers.CheckViewable(ctx, env, customerID); err != nil {
		return nil, err
	}
	rows, err := env.Q.ListCustomerCards(ctx, dbgen.ListCustomerCardsParams{StoreID: env.Caller.StoreID, CustomerID: customerID})
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{}
	for _, row := range rows {
		if row.Card.Status != "refunded" && !slices.Contains(excluded, row.Card.ID) {
			ids = append(ids, row.Card.ID)
		}
	}
	return ids, nil
}
