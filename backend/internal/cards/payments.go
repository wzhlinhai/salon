package cards

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// PaymentCard 是结账持有锁的卡及其在本次结账中的当前余额。
type PaymentCard struct {
	row              dbgen.Card
	charged          bool
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

// PaymentCards 按 UUID 一起锁显式卡和候选卡并检查查看范围；显式卡检查有效期，候选卡留待筛选，试算只读。
func PaymentCards(ctx context.Context, env ops.Env, ids []uuid.UUID, day time.Time, lock bool, candidates ...uuid.UUID) (map[uuid.UUID]*PaymentCard, error) {
	ordered := append(slices.Clone(ids), candidates...)
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
		if !slices.Contains(candidates, id) || slices.Contains(ids, id) {
			if err := checkExpiry(row, day); err != nil {
				return nil, err
			}
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
	c.charged = true
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
		if !c.charged {
			continue
		}
		if _, err := recordChange(ctx, env, "checkout_orders", c.row, c.before); err != nil {
			return err
		}
	}
	return nil
}

// CheckBalancePayment 检查卡是否可以用于卡余额支付或作为计价卡。
func (c *PaymentCard) CheckBalancePayment() error {
	if c.before.Kind == CountCard {
		return ops.Rule("次卡不能用卡余额支付")
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
		return env.Q.ConsumeCardCount(ctx, dbgen.ConsumeCardCountParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: c.row.ID, ItemID: itemID, PaidBefore: paid, PaidDelta: pd, PaidAfter: paid + pd, GiftBefore: gift, GiftDelta: gd, GiftAfter: gift + gd, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID})
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

// CheckSelection 检查锁后候选卡仍属于顾客且可用于卡余额支付。
func (c *PaymentCard) CheckSelection(customerID uuid.UUID, day time.Time) error {
	if c.row.CustomerID != customerID {
		return ops.Rule("不能用别人的卡")
	}
	if err := checkExpiry(c.row, day); err != nil {
		return err
	}
	if err := c.CheckBalancePayment(); err != nil {
		return err
	}
	if c.Balance().Cmp(money.Amount{}) <= 0 {
		return ops.Rule("卡余额不足")
	}
	return nil
}

// CompareSelectionTie 按到期日（不限最后）、开卡时间、UUID 比较选卡的同序卡。
func (c *PaymentCard) CompareSelectionTie(other *PaymentCard) int {
	if c.row.ExpiresOn.Valid != other.row.ExpiresOn.Valid {
		if c.row.ExpiresOn.Valid {
			return -1
		}
		return 1
	}
	if c.row.ExpiresOn.Valid {
		if n := c.row.ExpiresOn.Time.Compare(other.row.ExpiresOn.Time); n != 0 {
			return n
		}
	}
	if n := c.row.OpenedAt.Time.Compare(other.row.OpenedAt.Time); n != 0 {
		return n
	}
	return cmp.Compare(c.row.ID.String(), other.row.ID.String())
}

// SelectionCandidates 只列出本店顾客的候选卡 ID，使用前须在锁后重新检查。
func SelectionCandidates(ctx context.Context, env ops.Env, customerID uuid.UUID, excluded []uuid.UUID) ([]uuid.UUID, error) {
	if err := canView(ctx, env, customerID); err != nil {
		return nil, err
	}
	rows, err := env.Q.ListCustomerCards(ctx, dbgen.ListCustomerCardsParams{StoreID: env.Caller.StoreID, CustomerID: customerID})
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{}
	for _, row := range rows {
		if !slices.Contains(excluded, row.Card.ID) {
			ids = append(ids, row.Card.ID)
		}
	}
	return ids, nil
}
