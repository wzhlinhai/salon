package orders

import (
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// usableCandidates 在卡锁之后检查候选，不把列表时的归属和余额当作当前状态。
func usableCandidates(ids []uuid.UUID, used map[uuid.UUID]*cards.PaymentCard, customerID uuid.UUID, day time.Time) []uuid.UUID {
	out := []uuid.UUID{}
	for _, id := range ids {
		if used[id].CheckSelection(customerID, day) == nil {
			out = append(out, id)
		}
	}
	return out
}

// orderCandidates 与试算共用同一套选卡和同序规则；类型、项目在调用前一起锁定。
func orderCandidates(order Order, ids []uuid.UUID, used map[uuid.UUID]*cards.PaymentCard, rules settings.Rules, types map[uuid.UUID]dbgen.CardType, lines map[uuid.UUID]linePricing, members map[uuid.UUID]map[uuid.UUID]money.Amount) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, ops.Rule("这位顾客没有能用的卡")
	}
	nets := map[uuid.UUID]money.Total{}
	if rules.CardSelection() == "by_discount" {
		for _, id := range ids {
			typeID := used[id].CardTypeID()
			priced := priceCheckout(order, pricingLines(lines, members[typeID]), &typeID, types[typeID].DiscountPercent, rules.MemberPriceRule())
			total := money.Total{}
			for _, line := range priced.Items {
				if line.RemovedAt == nil {
					net, _ := line.Price.Apply(money.Amount{}.Sub(line.Discount))
					total = total.AddProduct(net, 1)
				}
			}
			nets[id] = total
		}
	}
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int {
		var n int
		if rules.CardSelection() == "by_discount" {
			n = nets[a].CmpTotal(nets[b])
		} else {
			n = used[a].Balance().CmpTotal(used[b].Balance())
		}
		if n != 0 {
			return n
		}
		return used[a].CompareSelectionTie(used[b])
	})
	return ordered, nil
}

// selectionPayments 在原支付位置展开未指定卡支付，余额接续本次调用中已扣的流水。
func selectionPayments(payment PaymentInput, candidates []uuid.UUID, used map[uuid.UUID]*cards.PaymentCard) ([]PaymentInput, error) {
	remaining := payment.Amount
	out := []PaymentInput{}
	for _, id := range candidates {
		if remaining.Cmp(money.Amount{}) == 0 {
			break
		}
		balance := used[id].Balance()
		if balance.Cmp(money.Amount{}) == 0 {
			continue
		}
		take := balance.Limit(remaining)
		out = append(out, PaymentInput{Method: "card", CardID: new(id), Amount: take})
		remaining, _ = remaining.Apply(money.Amount{}.Sub(take))
	}
	if remaining.Cmp(money.Amount{}) > 0 {
		return nil, ops.Rule("卡余额不足")
	}
	return out, nil
}
