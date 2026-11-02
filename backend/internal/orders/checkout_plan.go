package orders

import (
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type checkoutRules struct{ cardSelection, memberPriceRule, othersCardPayment string }
type checkoutState struct {
	cards     map[uuid.UUID]cards.CardSnapshot
	discounts map[uuid.UUID]*money.Percent
	lines     map[uuid.UUID]linePricing
	members   map[uuid.UUID]map[uuid.UUID]money.Amount
	day       time.Time
}
type checkoutOrder struct {
	order      Order
	rules      checkoutRules
	candidates []uuid.UUID
	payments   []PaymentInput
}
type checkoutLine struct {
	ID                  uuid.UUID
	Price               money.Amount
	PriceSource         string
	LevelID, CardTypeID *uuid.UUID
	Discount            money.Amount
}
type checkoutPlan struct {
	pricingTypeID *uuid.UUID
	order         Order
	changedLines  []checkoutLine
	payments      []PaymentInput
}

// checkExplicitCards 按单和支付的输入顺序检查显式卡，不依赖计价数据。
func checkExplicitCards(snapshots map[uuid.UUID]cards.CardSnapshot, orders []checkoutOrder) error {
	for _, input := range orders {
		for _, payment := range input.payments {
			if payment.CardID == nil {
				continue
			}
			card := snapshots[*payment.CardID]
			if err := card.CheckHolder(input.order.CustomerID, input.rules.othersCardPayment); err != nil {
				return err
			}
			if payment.Method == "card" {
				if err := card.CheckBalancePayment(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// planCheckout 在内存中接续各单卡余额，整体成功后执行方才写入。
func planCheckout(state checkoutState, orders []checkoutOrder) ([]checkoutPlan, error) {
	if err := checkExplicitCards(state.cards, orders); err != nil {
		return nil, err
	}
	state.cards = cloneCards(state.cards)
	plans := make([]checkoutPlan, 0, len(orders))
	for _, input := range orders {
		// 前面各单的计划已扣过余额，按当前余额重新过滤、排序和选计价卡。
		candidates := usableCandidates(input.candidates, state.cards, input.order.CustomerID, state.day)
		for _, payment := range input.payments {
			if payment.Method == "card" && payment.CardID == nil {
				var err error
				candidates, err = orderCandidates(input.order, candidates, state.cards, input.rules, state.discounts, state.lines, state.members)
				if err != nil {
					return nil, err
				}
			}
		}
		// 第一笔卡余额支付才决定计价卡；扣次不参与。
		var pricingCardID *uuid.UUID
		for _, payment := range input.payments {
			if payment.Method != "card" {
				continue
			}
			pricingCardID = payment.CardID
			if pricingCardID == nil {
				pricingCardID = new(candidates[0])
			}
			break
		}
		priced, typeID := priceOrder(state, input.order, input.rules, pricingCardID)
		if err := checkCountPayments(priced, input.payments); err != nil {
			return nil, err
		}
		if err := checkCheckout(priced, input.payments); err != nil {
			return nil, err
		}
		plan := checkoutPlan{pricingTypeID: typeID, order: priced, changedLines: changedCheckoutLines(input.order, priced), payments: []PaymentInput{}}
		for _, payment := range input.payments {
			payments := []PaymentInput{payment}
			if payment.Method == "card" && payment.CardID == nil {
				var err error
				payments, err = selectionPayments(payment, candidates, state.cards)
				if err != nil {
					return nil, err
				}
			}
			for _, expanded := range payments {
				if expanded.Method == "card" {
					card := state.cards[*expanded.CardID]
					if card.Balance.Cmp(expanded.Amount) < 0 {
						return nil, ops.Rule("卡余额不足")
					}
					card.Balance = card.Balance.Deduct(expanded.Amount)
					state.cards[*expanded.CardID] = card
				}
				plan.payments = append(plan.payments, expanded)
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func cloneCards(input map[uuid.UUID]cards.CardSnapshot) map[uuid.UUID]cards.CardSnapshot {
	out := make(map[uuid.UUID]cards.CardSnapshot, len(input))
	for id, card := range input {
		out[id] = card
	}
	return out
}

// quoteOrder 共用选卡和计价，不检查支付及必填岗位。
func quoteOrder(state checkoutState, input checkoutOrder, explicit *uuid.UUID, auto bool) (Order, *uuid.UUID, error) {
	pricingCardID := explicit
	if explicit != nil {
		card := state.cards[*explicit]
		if err := card.CheckHolder(input.order.CustomerID, input.rules.othersCardPayment); err != nil {
			return Order{}, nil, err
		}
		if err := card.CheckBalancePayment(); err != nil {
			return Order{}, nil, err
		}
	}
	if auto {
		candidates := usableCandidates(input.candidates, state.cards, input.order.CustomerID, state.day)
		candidates, err := orderCandidates(input.order, candidates, state.cards, input.rules, state.discounts, state.lines, state.members)
		if err != nil {
			return Order{}, nil, err
		}
		pricingCardID = new(candidates[0])
	}
	priced, _ := priceOrder(state, input.order, input.rules, pricingCardID)
	priced.Payments = []OrderPayment{}
	return priced, pricingCardID, nil
}

func priceOrder(state checkoutState, order Order, rules checkoutRules, cardID *uuid.UUID) (Order, *uuid.UUID) {
	var typeID *uuid.UUID
	var percent *money.Percent
	var members map[uuid.UUID]money.Amount
	if cardID != nil {
		typeID = new(state.cards[*cardID].CardTypeID)
		percent = state.discounts[*typeID]
		members = state.members[*typeID]
	}
	return priceCheckout(order, pricingLines(state.lines, members), typeID, percent, rules.memberPriceRule), typeID
}

func changedCheckoutLines(before, priced Order) []checkoutLine {
	out := []checkoutLine{}
	for j, line := range priced.Items {
		previous := before.Items[j]
		if line.RemovedAt != nil || line.Price.Cmp(previous.Price) == 0 && line.PriceSource == previous.PriceSource && reflect.DeepEqual(line.LevelID, previous.LevelID) && reflect.DeepEqual(line.CardTypeID, previous.CardTypeID) && line.Discount.Cmp(previous.Discount) == 0 {
			continue
		}
		out = append(out, checkoutLine{ID: line.ID, Price: line.Price, PriceSource: line.PriceSource, LevelID: line.LevelID, CardTypeID: line.CardTypeID, Discount: line.Discount})
	}
	return out
}

type linePricing struct {
	discountable bool
	member       *money.Amount
}

// priceCheckout 只使用行上已有价格和计价卡规则，不重取标准价或级别价。
func priceCheckout(order Order, lines map[uuid.UUID]linePricing, cardTypeID *uuid.UUID, percent *money.Percent, memberPriceRule string) Order {
	order.Items = slices.Clone(order.Items)
	receivable, discount, net := money.Total{}, money.Total{}, money.Total{}
	for i := range order.Items {
		line := &order.Items[i]
		if line.RemovedAt != nil {
			continue
		}
		line.Discount = money.Amount{}
		if cardTypeID != nil && line.PriceSource != "manual" {
			pricing := lines[line.ItemID]
			if catalog.UseMemberPrice(pricing.discountable, pricing.member, line.Price, memberPriceRule) {
				line.Price = *pricing.member
				line.PriceSource = "member"
				line.CardTypeID = cardTypeID
				line.LevelID = nil
			} else if pricing.discountable && percent != nil {
				line.Discount = line.Price.Discount(*percent)
			}
		}
		receivable = receivable.AddProduct(line.Price, 1)
		discount = discount.AddProduct(line.Discount, 1)
		amount, _ := line.Price.Apply(money.Amount{}.Sub(line.Discount))
		net = net.AddProduct(amount, 1)
	}
	order.Receivable, order.Discount, order.Net = receivable.Format(), discount.Format(), net.Format()
	return order
}

func pricingLines(lines map[uuid.UUID]linePricing, members map[uuid.UUID]money.Amount) map[uuid.UUID]linePricing {
	out := make(map[uuid.UUID]linePricing, len(lines))
	for id, line := range lines {
		if price, ok := members[id]; ok {
			line.member = &price
		}
		out[id] = line
	}
	return out
}

// usableCandidates 在卡锁之后检查候选，不把列表时的归属和余额当作当前状态。
func usableCandidates(ids []uuid.UUID, used map[uuid.UUID]cards.CardSnapshot, customerID uuid.UUID, day time.Time) []uuid.UUID {
	out := []uuid.UUID{}
	for _, id := range ids {
		if used[id].CheckSelection(customerID, day) == nil {
			out = append(out, id)
		}
	}
	return out
}

// orderCandidates 与试算共用同一套选卡和同序规则；类型、项目在调用前一起锁定。
func orderCandidates(order Order, ids []uuid.UUID, used map[uuid.UUID]cards.CardSnapshot, rules checkoutRules, types map[uuid.UUID]*money.Percent, lines map[uuid.UUID]linePricing, members map[uuid.UUID]map[uuid.UUID]money.Amount) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, ops.Rule("这位顾客没有能用的卡")
	}
	nets := map[uuid.UUID]money.Total{}
	if rules.cardSelection == "by_discount" {
		for _, id := range ids {
			typeID := used[id].CardTypeID
			priced := priceCheckout(order, pricingLines(lines, members[typeID]), &typeID, types[typeID], rules.memberPriceRule)
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
		if rules.cardSelection == "by_discount" {
			n = nets[a].CmpTotal(nets[b])
		} else {
			n = used[a].Balance.CmpTotal(used[b].Balance)
		}
		if n != 0 {
			return n
		}
		return used[a].CompareSelectionTie(used[b])
	})
	return ordered, nil
}

// selectionPayments 在原支付位置展开未指定卡支付，余额接续本次调用中已扣的流水。
func selectionPayments(payment PaymentInput, candidates []uuid.UUID, used map[uuid.UUID]cards.CardSnapshot) ([]PaymentInput, error) {
	remaining := payment.Amount
	out := []PaymentInput{}
	for _, id := range candidates {
		if remaining.Cmp(money.Amount{}) == 0 {
			break
		}
		balance := used[id].Balance
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

func checkCheckout(order Order, payments []PaymentInput) error {
	active := 0
	net := money.Total{}
	missing := false
	for _, item := range order.Items {
		if item.RemovedAt != nil {
			continue
		}
		active++
		amount, _ := item.Price.Apply(money.Amount{}.Sub(item.Discount))
		net = net.AddProduct(amount, 1)
		for _, p := range item.Positions {
			if p.Required && p.StaffID == nil {
				missing = true
			}
		}
	}
	if active == 0 {
		return ops.Rule("单上没有项目")
	}
	if missing {
		return ops.Rule("必填岗位还没填人")
	}
	total := money.Total{}
	for _, payment := range payments {
		if payment.Amount.Cmp(money.Amount{}) <= 0 {
			return ops.Rule("支付金额须大于零")
		}
		total = total.AddProduct(payment.Amount, 1)
	}
	if total.CmpTotal(net) != 0 {
		return ops.Rule("支付合计须等于实收")
	}
	return nil
}

func checkCountPayments(order Order, payments []PaymentInput) error {
	for _, payment := range payments {
		if payment.Method != "count" {
			continue
		}
		found := false
		for _, line := range order.Items {
			if line.ID != *payment.OrderItemID || line.RemovedAt != nil {
				continue
			}
			found = true
			net, _ := line.Price.Apply(money.Amount{}.Sub(line.Discount))
			if payment.Amount.Cmp(net) != 0 {
				return ops.Rule("扣次金额须等于这一行的实收")
			}
			break
		}
		if !found {
			return ops.Rule("扣次的项目行不在这张单上")
		}
	}
	return nil
}
