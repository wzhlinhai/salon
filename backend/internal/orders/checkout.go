package orders

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/payments"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/rotation"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func registerCheckout(r *ops.Registry) {
	ops.Register(r, ops.Operation[CheckoutOrdersInput, OrderList]{Name: "checkout_orders", Summary: "一张或几张挂单一起结账并登记支付", Kind: ops.Write, Permission: perm.PaymentCollect, AI: ops.AIDraft, Handle: checkoutOrders})
}

func checkoutOrders(ctx context.Context, env ops.Env, in *CheckoutOrdersInput) (*OrderList, error) {
	now := env.Now
	ids := make([]uuid.UUID, len(in.Orders))
	seen := map[uuid.UUID]bool{}
	for i, order := range in.Orders {
		if seen[order.OrderID] {
			return nil, ops.Rule("同一张单不能重复结账")
		}
		seen[order.OrderID] = true
		ids[i] = order.OrderID
	}
	inputs := make([][]payments.Input, len(in.Orders))
	packetIDs := []uuid.UUID{}
	for i, order := range in.Orders {
		inputs[i] = paymentInputs(order.Payments)
		for _, payment := range order.Payments {
			if payment.RedPacketID != nil {
				packetIDs = append(packetIDs, *payment.RedPacketID)
			}
		}
	}
	if err := payments.Validate(inputs); err != nil {
		return nil, err
	}
	after, err := change(ctx, env, "checkout_orders", "open", ids, func(rows []dbgen.Order) error {
		orders, err := views(ctx, env, rows)
		if err != nil {
			return err
		}
		today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
		if err != nil {
			return err
		}
		day := today
		backfill := false
		for i, row := range rows {
			orderDay := today
			if row.Backfill {
				orderDay = row.BusinessDate.Time
				backfill = true
			}
			if i == 0 {
				day = orderDay
			} else if !orderDay.Equal(day) {
				return ops.Rule("一次结账只能是同一个营业日")
			}
		}
		if backfill {
			if err := payrollmonth.Guard(ctx, env.Q, env.Caller.StoreID, payrollmonth.Of(day), "补录"); err != nil {
				return err
			}
		}
		for i, row := range rows {
			for _, payment := range in.Orders[i].Payments {
				if payment.Method != "debt" {
					continue
				}
				// 结账先锁单，合并先锁顾客再改挂单；这里锁顾客会倒置锁序。
				// 补手机号只会让顾客变为已识别，无锁读取不会放过未识别挂账。
				customer, err := env.Q.GetCustomer(ctx, row.CustomerID)
				if err != nil {
					return err
				}
				if customer.Phone == nil {
					return ops.Rule("未识别顾客不能挂账，请先补手机号")
				}
			}
		}
		rules := make([]settings.Rules, len(rows))
		cardIDs := []uuid.UUID{}
		candidates := make([][]uuid.UUID, len(rows))
		candidateIDs := []uuid.UUID{}
		for i, row := range rows {
			rules[i], err = settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, row.SettingsSnapshotID)
			if err != nil {
				return err
			}
			explicit := []uuid.UUID{}
			unspecified := false
			for _, payment := range in.Orders[i].Payments {
				if payment.CardID != nil {
					explicit = append(explicit, *payment.CardID)
				}
				if payment.Method == "card" && payment.CardID == nil {
					unspecified = true
				}
			}
			cardIDs = append(cardIDs, explicit...)
			if unspecified {
				if rules[i].CardSelection() == "manual" {
					return ops.Rule("卡余额支付要选卡")
				}
				candidates[i], err = cards.SelectionCandidates(ctx, env, row.CustomerID, explicit)
				if err != nil {
					return err
				}
				candidateIDs = append(candidateIDs, candidates[i]...)
			}
		}
		pay, err := payments.Lock(ctx, env, cardIDs, candidateIDs, packetIDs, day)
		if err != nil {
			return err
		}
		redemptions, err := env.Q.LockOrderRedemptions(ctx, dbgen.LockOrderRedemptionsParams{StoreID: env.Caller.StoreID, OrderIds: ids})
		if err != nil {
			return err
		}
		groupbuy := map[uuid.UUID][]checkoutGroupbuy{}
		for _, row := range redemptions {
			if row.OrderID == nil || row.PurchasePrice == nil || row.StorePrice == nil {
				continue
			}
			groupbuy[*row.OrderID] = append(groupbuy[*row.OrderID], checkoutGroupbuy{RedemptionID: row.ID, DealName: row.DealName, Platform: row.Platform, PurchasePrice: *row.PurchasePrice, StorePrice: *row.StorePrice})
		}
		snapshots := pay.Snapshots()
		inputs := make([]checkoutOrder, len(orders))
		for i, order := range orders {
			inputs[i] = checkoutOrder{order: order, rules: checkoutRules{rules[i].CardSelection(), rules[i].MemberPriceRule(), rules[i].OthersCardPayment()}, candidates: candidates[i], groupbuy: groupbuy[order.ID], payments: in.Orders[i].Payments}
		}
		if err := checkExplicitCards(snapshots, inputs); err != nil {
			return err
		}
		// 保留原加载范围：首笔显式卡余额支付的类型，以及首次过滤通过的候选类型。
		pricingTypes := make([]*uuid.UUID, len(rows))
		typeIDs := []uuid.UUID{}
		for i, row := range rows {
			candidates[i] = usableCandidates(candidates[i], snapshots, row.CustomerID, day)
			for _, id := range candidates[i] {
				typeIDs = append(typeIDs, snapshots[id].CardTypeID)
			}
			for _, payment := range in.Orders[i].Payments {
				if payment.CardID == nil {
					continue
				}
				card := snapshots[*payment.CardID]
				if payment.Method == "card" && pricingTypes[i] == nil {
					pricingTypes[i] = new(card.CardTypeID)
					typeIDs = append(typeIDs, *pricingTypes[i])
				}
			}
		}
		pricingOrders := []Order{}
		for i, order := range orders {
			if pricingTypes[i] != nil || len(candidates[i]) > 0 {
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
		checkout, err := env.Q.CreateCheckout(ctx, dbgen.CreateCheckoutParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, BusinessDate: pgtype.Date{Time: day, Valid: true}, CheckedOutBy: env.Caller.StaffID})
		if err != nil {
			return err
		}
		state := checkoutState{cards: snapshots, discounts: map[uuid.UUID]*money.Percent{}, lines: lines, members: members, day: day}
		for id, cardType := range types {
			state.discounts[id] = cardType.DiscountPercent
		}
		plans, err := planCheckout(state, inputs)
		if err != nil {
			return err
		}
		for i, plan := range plans {
			for _, line := range plan.changedLines {
				if err := env.Q.CheckoutOrderItem(ctx, dbgen.CheckoutOrderItemParams{ID: line.ID, Price: line.Price, PriceSource: line.PriceSource, LevelID: line.LevelID, CardTypeID: line.CardTypeID, Discount: line.Discount}); err != nil {
					return err
				}
			}
			items := make(map[uuid.UUID]uuid.UUID, len(plan.order.Items))
			for _, line := range plan.order.Items {
				items[line.ID] = line.ItemID
			}
			if err := pay.Record(ctx, checkout.ID, rows[i], items, rules[i], paymentInputs(plan.payments)); err != nil {
				return err
			}
			performanceLines := []performance.Line{}
			for _, line := range plan.order.Items {
				if line.RemovedAt != nil {
					continue
				}
				net, _ := line.Price.Apply(money.Amount{}.Sub(line.Discount))
				slots := make([]performance.Slot, 0, len(line.Positions))
				for _, p := range line.Positions {
					slots = append(slots, performance.Slot{Slot: p.Slot, PositionID: p.PositionID, StaffID: p.StaffID, SharePercent: p.SharePercent})
				}
				performanceLines = append(performanceLines, performance.Line{OrderItemID: line.ID, Net: net, Price: line.Price, LaborFeeAmount: line.LaborFeeAmount, LaborFeePercent: line.LaborFeePercent, Slots: slots})
			}
			member, err := cards.HasValidCard(ctx, env.Q, env.Caller.StoreID, rows[i].CustomerID, day)
			if err != nil {
				return err
			}
			if err := performance.RecordCheckout(ctx, env, performance.CheckoutInput{Member: member, CheckoutID: checkout.ID, BusinessDate: day, OrderID: rows[i].ID, SettingsSnapshotID: rows[i].SettingsSnapshotID, Rules: rules[i], Lines: performanceLines, Designated: rows[i].Designated, Gender: rows[i].Gender}); err != nil {
				return err
			}

			if err := env.Q.MarkOrderPaid(ctx, dbgen.MarkOrderPaidParams{StoreID: env.Caller.StoreID, ID: rows[i].ID, CheckoutID: &checkout.ID}); err != nil {
				return err
			}
			// 补录单不推进今天的轮牌：它不是今天实际发生的服务。
			if !rows[i].Backfill {
				if err := rotation.AdvanceAfterCheckout(ctx, env, rows[i].ID); err != nil {
					return err
				}
			}
			rows[i].Status = "paid"
			rows[i].CheckoutID = &checkout.ID
		}
		return pay.Finish(ctx)
	}, now)
	if err != nil {
		return nil, err
	}
	return &OrderList{Items: after}, nil
}

func paymentInputs(in []PaymentInput) []payments.Input {
	out := make([]payments.Input, len(in))
	for i, p := range in {
		out[i] = payments.Input(p)
	}
	return out
}
