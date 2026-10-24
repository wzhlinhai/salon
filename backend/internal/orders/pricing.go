package orders

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

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
			if pricing.member != nil && (memberPriceRule == "member_first" || pricing.member.Cmp(line.Price) <= 0) {
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

// pricingData 先按 UUID 共享锁计价卡类型，再共享锁全部有效项目；试算只读。
func pricingData(ctx context.Context, env ops.Env, orders []Order, typeIDs []uuid.UUID, lock bool) (map[uuid.UUID]dbgen.CardType, map[uuid.UUID]linePricing, map[uuid.UUID]map[uuid.UUID]money.Amount, error) {
	types := map[uuid.UUID]dbgen.CardType{}
	ordered := slices.Clone(typeIDs)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, id := range slices.Compact(ordered) {
		var ct dbgen.CardType
		var err error
		if lock {
			ct, err = env.Q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: env.Caller.StoreID, ID: id})
		} else {
			ct, err = env.Q.GetCardType(ctx, dbgen.GetCardTypeParams{StoreID: env.Caller.StoreID, ID: id})
		}
		if err != nil {
			return nil, nil, nil, rowError(err)
		}
		types[id] = ct
	}
	ids := []uuid.UUID{}
	for _, order := range orders {
		for _, line := range order.Items {
			if line.RemovedAt == nil {
				ids = append(ids, line.ItemID)
			}
		}
	}
	var items map[uuid.UUID]dbgen.Item
	if lock {
		var err error
		items, err = catalog.LockItems(ctx, env.Q, env.Caller.StoreID, ids...)
		if err != nil {
			return nil, nil, nil, rowError(err)
		}
	} else {
		items = map[uuid.UUID]dbgen.Item{}
		for _, id := range ids {
			item, err := env.Q.GetItem(ctx, dbgen.GetItemParams{StoreID: env.Caller.StoreID, ID: id})
			if err != nil {
				return nil, nil, nil, rowError(err)
			}
			items[id] = item
		}
	}
	lines := map[uuid.UUID]linePricing{}
	for id, item := range items {
		lines[id] = linePricing{discountable: item.Discountable}
	}
	members, err := env.Q.ListItemMemberPrices(ctx, dbgen.ListItemMemberPricesParams{StoreID: env.Caller.StoreID, ItemIds: ids})
	if err != nil {
		return nil, nil, nil, err
	}
	prices := map[uuid.UUID]map[uuid.UUID]money.Amount{}
	for _, m := range members {
		if prices[m.CardTypeID] == nil {
			prices[m.CardTypeID] = map[uuid.UUID]money.Amount{}
		}
		prices[m.CardTypeID][m.ItemID] = m.Price
	}
	return types, lines, prices, nil
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
