package orders

import (
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/debts"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/redpackets"
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
	packetIDs := []uuid.UUID{}
	packetSeen := map[uuid.UUID]bool{}
	countLines := map[uuid.UUID]bool{}
	for _, order := range in.Orders {
		unspecified := 0
		for _, payment := range order.Payments {
			if payment.Method != "red_packet" && payment.RedPacketID != nil {
				return nil, ops.Rule("只有红包支付要选红包")
			}
			if payment.Method == "red_packet" {
				if payment.RedPacketID == nil {
					return nil, ops.Rule("红包支付要选红包")
				}
				if packetSeen[*payment.RedPacketID] {
					return nil, ops.Rule("同一个红包只能用一次")
				}
				packetSeen[*payment.RedPacketID] = true
				packetIDs = append(packetIDs, *payment.RedPacketID)
			}
			if payment.Method != "count" && payment.OrderItemID != nil {
				return nil, ops.Rule("只有扣次要选项目行")
			}
			if payment.Method == "count" {
				if payment.OrderItemID == nil {
					return nil, ops.Rule("扣次要选项目行")
				}
				if payment.CardID == nil {
					return nil, ops.Rule("扣次要选卡")
				}
				if countLines[*payment.OrderItemID] {
					return nil, ops.Rule("同一项目行不能重复扣次")
				}
				countLines[*payment.OrderItemID] = true
			}
			if payment.Method != "card" && payment.Method != "count" && payment.CardID != nil {
				return nil, ops.Rule("只有卡余额支付和扣次要选卡")
			}
			if payment.Method == "card" && payment.CardID == nil {
				unspecified++
				if unspecified > 1 {
					return nil, ops.Rule("一张单只能有一笔不指定卡的卡余额支付")
				}
			}
		}
	}
	after, err := change(ctx, env, "checkout_orders", "open", ids, func(rows []dbgen.Order) error {
		orders, err := views(ctx, env, rows)
		if err != nil {
			return err
		}
		day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
		if err != nil {
			return err
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
		used, err := cards.PaymentCards(ctx, env, cardIDs, day, true, candidateIDs...)
		if err != nil {
			return err
		}
		packets, err := redpackets.LockPayments(ctx, env, packetIDs)
		if err != nil {
			return err
		}
		pricingTypes := make([]*uuid.UUID, len(rows))
		typeIDs := []uuid.UUID{}
		for i, row := range rows {
			candidates[i] = usableCandidates(candidates[i], used, row.CustomerID, day)
			for _, id := range candidates[i] {
				typeIDs = append(typeIDs, used[id].CardTypeID())
			}
			for _, payment := range in.Orders[i].Payments {
				if payment.CardID == nil {
					continue
				}
				card := used[*payment.CardID]
				if err := card.CheckHolder(row.CustomerID, rules[i]); err != nil {
					return err
				}
				if payment.Method == "card" {
					if err := card.CheckBalancePayment(); err != nil {
						return err
					}
				}
				if payment.Method == "card" && pricingTypes[i] == nil {
					pricingTypes[i] = new(card.CardTypeID())
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
		priced := make([]Order, len(orders))
		for i, order := range orders {
			// 前面各单已扣过款，选卡与计价必须使用本次调用中的当前余额。
			candidates[i] = usableCandidates(candidates[i], used, rows[i].CustomerID, day)
			for _, payment := range in.Orders[i].Payments {
				if payment.Method != "card" || payment.CardID != nil {
					continue
				}
				candidates[i], err = orderCandidates(orders[i], candidates[i], used, rules[i], types, lines, members)
				if err != nil {
					return err
				}
			}
			// 第一笔卡余额支付才决定计价卡；扣次不参与。
			for _, payment := range in.Orders[i].Payments {
				if payment.Method != "card" {
					continue
				}
				if payment.CardID == nil {
					pricingTypes[i] = new(used[candidates[i][0]].CardTypeID())
				}
				break
			}
			var percent *money.Percent
			var prices map[uuid.UUID]money.Amount
			if pricingTypes[i] != nil {
				percent = types[*pricingTypes[i]].DiscountPercent
				prices = members[*pricingTypes[i]]
			}
			priced[i] = priceCheckout(order, pricingLines(lines, prices), pricingTypes[i], percent, rules[i].MemberPriceRule())
			if err := checkCountPayments(priced[i], in.Orders[i].Payments); err != nil {
				return err
			}
			if err := checkCheckout(priced[i], in.Orders[i].Payments); err != nil {
				return err
			}
			for j, line := range priced[i].Items {
				before := orders[i].Items[j]
				if line.RemovedAt != nil || line.Price.Cmp(before.Price) == 0 && line.PriceSource == before.PriceSource && reflect.DeepEqual(line.LevelID, before.LevelID) && reflect.DeepEqual(line.CardTypeID, before.CardTypeID) && line.Discount.Cmp(before.Discount) == 0 {
					continue
				}
				if err := env.Q.CheckoutOrderItem(ctx, dbgen.CheckoutOrderItemParams{ID: line.ID, Price: line.Price, PriceSource: line.PriceSource, LevelID: line.LevelID, CardTypeID: line.CardTypeID, Discount: line.Discount}); err != nil {
					return err
				}
			}
			for _, inputPayment := range in.Orders[i].Payments {
				payments := []PaymentInput{inputPayment}
				if inputPayment.Method == "card" && inputPayment.CardID == nil {
					payments, err = selectionPayments(inputPayment, candidates[i], used)
					if err != nil {
						return err
					}
				}
				for _, payment := range payments {
					if payment.Method == "red_packet" {
						packet := packets[*payment.RedPacketID]
						if err := redpackets.CheckUsable(ctx, env, packet, rows[i].CustomerID, day); err != nil {
							return err
						}
						if payment.Amount.Cmp(packet.FaceValue) > 0 {
							return ops.Rule("红包抵扣不能超过面额")
						}
					}
					var ledgerID, countLedgerID *uuid.UUID
					if payment.Method == "count" {
						var itemID uuid.UUID
						for _, line := range priced[i].Items {
							if line.ID == *payment.OrderItemID {
								itemID = line.ItemID
								break
							}
						}
						id, err := used[*payment.CardID].ConsumeCount(ctx, env, itemID, rules[i].CardCountOrder(), day)
						if err != nil {
							return err
						}
						countLedgerID = &id
					}
					if payment.Method == "card" {
						id, err := used[*payment.CardID].Consume(ctx, env, payment.Amount, rules[i].CardDeductionOrder(), day)
						if err != nil {
							return err
						}
						ledgerID = &id
					}
					paymentID, err := env.Q.CreateOrderPayment(ctx, dbgen.CreateOrderPaymentParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CheckoutID: checkout.ID, OrderID: rows[i].ID, Method: payment.Method, Amount: payment.Amount, CardID: payment.CardID, CardLedgerID: ledgerID, OrderItemID: payment.OrderItemID, CardCountLedgerID: countLedgerID, RedPacketID: payment.RedPacketID})
					if err != nil {
						return err
					}
					if payment.Method == "red_packet" {
						if err := redpackets.MarkUsed(ctx, env, *payment.RedPacketID, paymentID); err != nil {
							return err
						}
					}
					if payment.Method == "debt" {
						if err := debts.RecordOrderDebt(ctx, env, rows[i].CustomerID, rows[i].ID, paymentID, payment.Amount, day); err != nil {
							return err
						}
					}
				}
			}
			recordedPayments, err := env.Q.ListOrderPayments(ctx, dbgen.ListOrderPaymentsParams{StoreID: env.Caller.StoreID, OrderIds: []uuid.UUID{rows[i].ID}})
			if err != nil {
				return err
			}
			consumptions := map[uuid.UUID]*performance.CountConsumption{}
			for _, p := range recordedPayments {
				if p.Method != "count" {
					continue
				}
				method := "count_paid"
				if p.Gift > 0 {
					method = "count_gift"
				}
				consumptions[*p.OrderItemID] = &performance.CountConsumption{Method: method, UnitPrice: *p.CountUnitPrice}
			}
			performanceLines := []performance.Line{}
			for _, line := range priced[i].Items {
				if line.RemovedAt != nil {
					continue
				}
				net, _ := line.Price.Apply(money.Amount{}.Sub(line.Discount))
				slots := make([]performance.Slot, 0, len(line.Positions))
				for _, p := range line.Positions {
					slots = append(slots, performance.Slot{Slot: p.Slot, PositionID: p.PositionID, StaffID: p.StaffID, SharePercent: p.SharePercent})
				}
				performanceLines = append(performanceLines, performance.Line{OrderItemID: line.ID, Net: net, Price: line.Price, LaborFeeAmount: line.LaborFeeAmount, LaborFeePercent: line.LaborFeePercent, Count: consumptions[line.ID], Slots: slots})
			}
			categories := []performance.Category{}
			for _, p := range recordedPayments {
				switch p.Method {
				case "count":
					continue
				case "card":
					categories = append(categories, performance.Category{Method: "card_principal", Amount: *p.Principal}, performance.Category{Method: "card_bonus", Amount: *p.Bonus})
				default:
					categories = append(categories, performance.Category{Method: p.Method, Amount: p.Amount})
				}
			}
			if err := performance.RecordCheckout(ctx, env, performance.CheckoutInput{CheckoutID: checkout.ID, BusinessDate: day, OrderID: rows[i].ID, SettingsSnapshotID: rows[i].SettingsSnapshotID, Rules: rules[i], Lines: performanceLines, Payments: categories}); err != nil {
				return err
			}

			if err := env.Q.MarkOrderPaid(ctx, dbgen.MarkOrderPaidParams{StoreID: env.Caller.StoreID, ID: rows[i].ID, CheckoutID: &checkout.ID}); err != nil {
				return err
			}
			rows[i].Status = "paid"
			rows[i].CheckoutID = &checkout.ID
		}
		return cards.AuditPaymentCards(ctx, env, used)
	}, now)
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
