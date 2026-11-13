package orders

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/privacy"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Receipt 是一张结账单的小票数据，包含门店抬头、可打印内容和卡余额。
type Receipt struct {
	CheckoutID       uuid.UUID      `json:"checkout_id"`
	StoreName        string         `json:"store_name"`
	PaperWidth       string         `json:"paper_width" enum:"58mm,80mm"`
	Header           string         `json:"header"`
	Footer           string         `json:"footer"`
	CustomerName     *string        `json:"customer_name" nullable:"true" doc:"已脱敏的顾客姓名；未识别顾客为空"`
	BusinessDate     string         `json:"business_date" format:"date"`
	Total            string         `json:"total"`
	ShowCustomerName bool           `json:"show_customer_name"`
	ShowStaff        bool           `json:"show_staff"`
	ShowPayments     bool           `json:"show_payments"`
	ShowCardBalance  bool           `json:"show_card_balance"`
	Backfill         bool           `json:"backfill"`
	CheckedOutAt     time.Time      `json:"checked_out_at"`
	Orders           []ReceiptOrder `json:"orders"`
	Cards            []ReceiptCard  `json:"cards"`
}

// ReceiptOrder 是一张单在小票上的项目、支付和小计。
type ReceiptOrder struct {
	ID           uuid.UUID        `json:"id"`
	BusinessDate string           `json:"business_date" format:"date"`
	CustomerName *string          `json:"customer_name" nullable:"true"`
	Backfill     bool             `json:"backfill"`
	Receivable   string           `json:"receivable"`
	Net          string           `json:"net"`
	Items        []ReceiptItem    `json:"items"`
	Payments     []ReceiptPayment `json:"payments"`
}

// ReceiptItem 是小票上的一个项目行。
type ReceiptItem struct {
	ItemName   string       `json:"item_name"`
	Price      money.Amount `json:"price"`
	Net        money.Amount `json:"net"`
	StaffNames []string     `json:"staff_names"`
}

// ReceiptPayment 是小票上的一笔支付；团购支付同时带上门店标价。
type ReceiptPayment struct {
	StorePrice *money.Amount `json:"store_price" nullable:"true"`
	Method     string        `json:"method"`
	Amount     money.Amount  `json:"amount"`
}

// ReceiptCard 是本次结账用到的卡在结账后的余额和剩余次数。
type ReceiptCard struct {
	CardID    uuid.UUID          `json:"card_id"`
	Principal money.Amount       `json:"principal"`
	Bonus     money.Amount       `json:"bonus"`
	Counts    []ReceiptCardCount `json:"counts"`
}

// ReceiptCardCount 是一次结账后某个卡项目的剩余购买和赠送次数。
type ReceiptCardCount struct {
	ItemID uuid.UUID `json:"item_id"`
	Name   string    `json:"name"`
	Paid   int32     `json:"paid"`
	Gift   int32     `json:"gift"`
}

// GetReceiptInput 指定要打印小票的结账单。
type GetReceiptInput struct {
	CheckoutID uuid.UUID `json:"checkout_id"`
}

func registerReceipt(r *ops.Registry) {
	ops.Register(r, ops.Operation[GetReceiptInput, Receipt]{Name: "get_receipt", Summary: "查看结账单的小票数据", Kind: ops.Read, Permission: perm.PaymentCollect, AI: ops.AIDirect, Handle: getReceipt})
}

func getReceipt(ctx context.Context, env ops.Env, in *GetReceiptInput) (*Receipt, error) {
	checkout, err := env.Q.GetCheckout(ctx, dbgen.GetCheckoutParams{StoreID: env.Caller.StoreID, ID: in.CheckoutID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListCheckoutOrders(ctx, dbgen.ListCheckoutOrdersParams{StoreID: env.Caller.StoreID, CheckoutID: &checkout.ID})
	if err != nil {
		return nil, err
	}
	orders, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	customerIDs := make([]uuid.UUID, len(orders))
	staffIDs := []uuid.UUID{}
	seenStaff := map[uuid.UUID]bool{}
	for i, order := range orders {
		customerIDs[i] = order.CustomerID
		for _, item := range order.Items {
			for _, position := range item.Positions {
				if position.StaffID != nil && !seenStaff[*position.StaffID] {
					seenStaff[*position.StaffID] = true
					staffIDs = append(staffIDs, *position.StaffID)
				}
			}
		}
	}
	customerRows, err := env.Q.GetCustomers(ctx, dbgen.GetCustomersParams{StoreID: env.Caller.StoreID, Ids: customerIDs})
	if err != nil {
		return nil, err
	}
	visible := make(map[uuid.UUID]bool, len(customerRows))
	names := make(map[uuid.UUID]string, len(customerRows))
	for _, customer := range customerRows {
		if customers.CanView(env.Caller, customer) {
			visible[customer.ID] = true
			if customer.Name != nil {
				names[customer.ID] = *customer.Name
			}
		}
	}
	staff, err := env.Q.StaffNames(ctx, dbgen.StaffNamesParams{StoreID: env.Caller.StoreID, Ids: staffIDs})
	if err != nil {
		return nil, err
	}
	staffNames := make(map[uuid.UUID]string, len(staff))
	for _, row := range staff {
		staffNames[row.ID] = row.Name
	}
	cardBalances, err := env.Q.ListCheckoutCardBalances(ctx, dbgen.ListCheckoutCardBalancesParams{StoreID: env.Caller.StoreID, CheckoutID: checkout.ID})
	if err != nil {
		return nil, err
	}
	countBalances, err := env.Q.ListCheckoutCountBalances(ctx, dbgen.ListCheckoutCountBalancesParams{StoreID: env.Caller.StoreID, CheckoutID: checkout.ID})
	if err != nil {
		return nil, err
	}
	store, err := env.Q.GetStore(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, rowError(err)
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, today)
	if err != nil {
		return nil, err
	}
	out := &Receipt{
		CheckoutID: checkout.ID, StoreName: store.Name,
		PaperWidth: rules.ReceiptPaperWidth(), Header: rules.ReceiptHeader(), Footer: rules.ReceiptFooter(),
		ShowCustomerName: rules.ReceiptShowCustomerName(), ShowStaff: rules.ReceiptShowStaff(),
		ShowPayments: rules.ReceiptShowPayments(), ShowCardBalance: rules.ReceiptShowCardBalance(),
		BusinessDate: checkout.BusinessDate.Time.Format(settings.DateLayout), CheckedOutAt: checkout.At.Time,
		Orders: make([]ReceiptOrder, 0, len(orders)), Cards: receiptCards(cardBalances, countBalances, visible),
	}
	var total money.Total
	for _, order := range orders {
		out.Backfill = out.Backfill || order.Backfill
		item := ReceiptOrder{ID: order.ID, BusinessDate: order.BusinessDate, Backfill: order.Backfill, Receivable: order.Receivable, Net: order.Net, Items: make([]ReceiptItem, 0), Payments: make([]ReceiptPayment, 0)}
		if name, ok := names[order.CustomerID]; ok && name != "" {
			masked := privacy.MaskName(name)
			item.CustomerName = &masked
			if out.CustomerName == nil {
				out.CustomerName = &masked
			}
		}
		storePrices := map[uuid.UUID]money.Amount{}
		for _, groupbuy := range order.Groupbuy {
			storePrices[groupbuy.RedemptionID] = groupbuy.StorePrice
		}
		for _, line := range order.Items {
			if line.RemovedAt != nil {
				continue
			}
			net, err := line.Price.Apply(money.Amount{}.Sub(line.Discount))
			if err != nil {
				return nil, err
			}
			lineStaff := []string{}
			seen := map[uuid.UUID]bool{}
			for _, position := range line.Positions {
				if position.StaffID == nil || seen[*position.StaffID] {
					continue
				}
				seen[*position.StaffID] = true
				if name, ok := staffNames[*position.StaffID]; ok {
					lineStaff = append(lineStaff, name)
				}
			}
			item.Items = append(item.Items, ReceiptItem{ItemName: line.ItemName, Price: line.Price, Net: net, StaffNames: lineStaff})
		}
		for _, payment := range order.Payments {
			line := ReceiptPayment{Method: payment.Method, Amount: payment.Amount}
			if payment.CouponRedemptionID != nil {
				if price, ok := storePrices[*payment.CouponRedemptionID]; ok {
					line.StorePrice = &price
				}
			}
			item.Payments = append(item.Payments, line)
		}
		var value money.Amount
		if err := value.Scan(item.Net); err == nil {
			total = total.AddProduct(value, 1)
		}
		out.Orders = append(out.Orders, item)
	}
	out.Total = total.Format()
	return out, nil
}

func receiptCards(balances []dbgen.ListCheckoutCardBalancesRow, counts []dbgen.ListCheckoutCountBalancesRow, visible map[uuid.UUID]bool) []ReceiptCard {
	byCard := map[uuid.UUID]*ReceiptCard{}
	order := []uuid.UUID{}
	for _, row := range balances {
		if row.CardID == nil || !visible[row.CustomerID] {
			continue
		}
		if _, ok := byCard[*row.CardID]; ok {
			continue
		}
		card := &ReceiptCard{CardID: *row.CardID, Principal: row.PrincipalAfter, Bonus: row.BonusAfter, Counts: []ReceiptCardCount{}}
		byCard[*row.CardID] = card
		order = append(order, *row.CardID)
	}
	seenCount := map[uuid.UUID]map[uuid.UUID]bool{}
	for _, row := range counts {
		if row.CardID == nil || !visible[row.CustomerID] {
			continue
		}
		id := *row.CardID
		card, ok := byCard[id]
		if !ok {
			card = &ReceiptCard{CardID: id, Counts: []ReceiptCardCount{}}
			byCard[id] = card
			order = append(order, id)
		}
		if seenCount[id] == nil {
			seenCount[id] = map[uuid.UUID]bool{}
		}
		if seenCount[id][row.ItemID] {
			continue
		}
		seenCount[id][row.ItemID] = true
		card.Counts = append(card.Counts, ReceiptCardCount{ItemID: row.ItemID, Name: row.ItemName, Paid: row.PaidAfter, Gift: row.GiftAfter})
	}
	out := make([]ReceiptCard, 0, len(order))
	for _, id := range order {
		out = append(out, *byCard[id])
	}
	return out
}
