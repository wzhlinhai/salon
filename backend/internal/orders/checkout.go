package orders

import (
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func registerCheckout(r *ops.Registry) {
	ops.Register(r, ops.Operation[CheckoutOrdersInput, OrderList]{Name: "checkout_orders", Summary: "一张或几张挂单一起结账并登记支付", Kind: ops.Write, Permission: perm.PaymentCollect, AI: ops.AIDraft, Handle: checkoutOrders})
}

func checkoutOrders(ctx context.Context, env ops.Env, in *CheckoutOrdersInput) (*OrderList, error) {
	return checkoutOrdersAt(ctx, env, in, time.Now())
}

func checkoutOrdersAt(ctx context.Context, env ops.Env, in *CheckoutOrdersInput, now time.Time) (*OrderList, error) {
	ids := make([]uuid.UUID, len(in.Orders))
	seen := map[uuid.UUID]bool{}
	for i, order := range in.Orders {
		if seen[order.OrderID] {
			return nil, ops.Rule("同一张单不能重复结账")
		}
		seen[order.OrderID] = true
		ids[i] = order.OrderID
	}
	for _, order := range in.Orders {
		for _, payment := range order.Payments {
			if payment.Method != "card" && payment.CardID != nil {
				return nil, ops.Rule("只有卡余额支付要选卡")
			}
			if payment.Method == "card" && payment.CardID == nil {
				return nil, ops.Rule("卡余额支付要选卡")
			}
		}
	}
	after, err := change(ctx, env, "checkout_orders", ids, func(rows []dbgen.Order) error {
		orders, err := views(ctx, env, rows)
		if err != nil {
			return err
		}
		day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
		if err != nil {
			return err
		}
		rules := make([]settings.Rules, len(rows))
		cardIDs := []uuid.UUID{}
		for i, row := range rows {
			rules[i], err = settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, row.SettingsSnapshotID)
			if err != nil {
				return err
			}
			for _, payment := range in.Orders[i].Payments {
				if payment.CardID != nil {
					cardIDs = append(cardIDs, *payment.CardID)
				}
			}
		}
		used, err := cards.PaymentCards(ctx, env, cardIDs, day, true)
		if err != nil {
			return err
		}
		pricingTypes := make([]*uuid.UUID, len(rows))
		typeIDs := []uuid.UUID{}
		for i, row := range rows {
			for _, payment := range in.Orders[i].Payments {
				if payment.CardID == nil {
					continue
				}
				card := used[*payment.CardID]
				if err := card.CheckHolder(row.CustomerID, rules[i]); err != nil {
					return err
				}
				if pricingTypes[i] == nil {
					pricingTypes[i] = new(card.CardTypeID())
					typeIDs = append(typeIDs, *pricingTypes[i])
				}
			}
		}
		pricingOrders := []Order{}
		for i, order := range orders {
			if pricingTypes[i] != nil {
				pricingOrders = append(pricingOrders, order)
			}
		}
		var types map[uuid.UUID]dbgen.CardType
		var lines map[uuid.UUID]linePricing
		var members map[uuid.UUID]map[uuid.UUID]money.Amount
		if len(pricingOrders) > 0 {
			types, lines, members, err = pricingData(ctx, env, pricingOrders, typeIDs, true)
			if err != nil {
				return err
			}
		}
		priced := make([]Order, len(orders))
		for i, order := range orders {
			var percent *money.Percent
			var prices map[uuid.UUID]money.Amount
			if pricingTypes[i] != nil {
				percent = types[*pricingTypes[i]].DiscountPercent
				prices = members[*pricingTypes[i]]
			}
			priced[i] = priceCheckout(order, pricingLines(lines, prices), pricingTypes[i], percent, rules[i].MemberPriceRule())
			if err := checkCheckout(priced[i], in.Orders[i].Payments); err != nil {
				return err
			}
		}
		checkout, err := env.Q.CreateCheckout(ctx, dbgen.CreateCheckoutParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, BusinessDate: pgtype.Date{Time: day, Valid: true}, CheckedOutBy: env.Caller.StaffID})
		if err != nil {
			return err
		}
		for i := range rows {
			for j, line := range priced[i].Items {
				before := orders[i].Items[j]
				if line.RemovedAt != nil || line.Price.Cmp(before.Price) == 0 && line.PriceSource == before.PriceSource && reflect.DeepEqual(line.LevelID, before.LevelID) && reflect.DeepEqual(line.CardTypeID, before.CardTypeID) && line.Discount.Cmp(before.Discount) == 0 {
					continue
				}
				if err := env.Q.CheckoutOrderItem(ctx, dbgen.CheckoutOrderItemParams{ID: line.ID, Price: line.Price, PriceSource: line.PriceSource, LevelID: line.LevelID, CardTypeID: line.CardTypeID, Discount: line.Discount}); err != nil {
					return err
				}
			}
			for _, payment := range in.Orders[i].Payments {
				var ledgerID *uuid.UUID
				if payment.CardID != nil {
					id, err := used[*payment.CardID].Consume(ctx, env, payment.Amount, rules[i].CardDeductionOrder(), day)
					if err != nil {
						return err
					}
					ledgerID = &id
				}
				if err := env.Q.CreateOrderPayment(ctx, dbgen.CreateOrderPaymentParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CheckoutID: checkout.ID, OrderID: rows[i].ID, Method: payment.Method, Amount: payment.Amount, CardID: payment.CardID, CardLedgerID: ledgerID}); err != nil {
					return err
				}
			}
			if err := env.Q.MarkOrderPaid(ctx, dbgen.MarkOrderPaidParams{StoreID: env.Caller.StoreID, ID: rows[i].ID, CheckoutID: &checkout.ID}); err != nil {
				return err
			}
			rows[i].Status = "paid"
			rows[i].CheckoutID = &checkout.ID
		}
		return cards.AuditPaymentCards(ctx, env, used)
	})
	if err != nil {
		return nil, err
	}
	return &OrderList{Items: after}, nil
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
