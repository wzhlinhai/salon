package miniprogram

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/booking"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Register 声明顾客本人可调用的只读操作。它们只认 CustomerSelf，不向 AI 开放。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[struct{}, MyProfile]{
		Name: "my_profile", Summary: "查看本人的档案与门店", Kind: ops.Read,
		Permission: ops.CustomerSelf, AI: ops.AINone, Handle: myProfile,
	})
	ops.Register(r, ops.Operation[struct{}, MyCards]{
		Name: "my_cards", Summary: "查看本人在本店的卡、余额和剩余次数", Kind: ops.Read,
		Permission: ops.CustomerSelf, AI: ops.AINone, Handle: myCards,
	})
	ops.Register(r, ops.Operation[MyCardLedgerInput, MyCardLedger]{
		Name: "my_card_ledger", Summary: "查看本人一张卡的余额变动", Kind: ops.Read,
		Permission: ops.CustomerSelf, AI: ops.AINone, Handle: myCardLedger,
	})
	ops.Register(r, ops.Operation[MyOrdersInput, MyOrders]{
		Name: "my_orders", Summary: "查看本人的消费明细", Kind: ops.Read,
		Permission: ops.CustomerSelf, AI: ops.AINone, Handle: myOrders,
	})
	ops.Register(r, ops.Operation[struct{}, MyAppointments]{
		Name: "my_appointments", Summary: "查看本人的预约", Kind: ops.Read,
		Permission: ops.CustomerSelf, AI: ops.AINone, Handle: myAppointments,
	})
}

func selfCustomer(ctx context.Context, env ops.Env) (dbgen.Customer, error) {
	if env.Caller.CustomerID == nil {
		return dbgen.Customer{}, ops.ErrUnauthenticated
	}
	row, err := customers.SurvivingCustomer(ctx, env.Q, *env.Caller.CustomerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Customer{}, ops.ErrNotFound
	}
	if err != nil {
		return dbgen.Customer{}, err
	}
	if row.StoreID != env.Caller.StoreID {
		return dbgen.Customer{}, ops.ErrNotFound
	}
	return row, nil
}

func myProfile(ctx context.Context, env ops.Env, _ *struct{}) (*MyProfile, error) {
	customer, err := selfCustomer(ctx, env)
	if err != nil {
		return nil, err
	}
	store, err := env.Q.GetStore(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	phone := customers.MaskPhone(customer.Phone)
	return &MyProfile{Name: customer.Name, Phone: phone, PhoneMasked: phone != nil, StoreName: store.Name}, nil
}

func myCards(ctx context.Context, env ops.Env, _ *struct{}) (*MyCards, error) {
	customer, err := selfCustomer(ctx, env)
	if err != nil {
		return nil, err
	}
	list, err := cards.ListForCustomer(ctx, env, customer.ID)
	if err != nil {
		return nil, err
	}
	out := &MyCards{Items: make([]MyCard, 0, len(list.Items))}
	for _, c := range list.Items {
		balance, err := c.Principal.Add(c.Bonus)
		if err != nil {
			return nil, err
		}
		items := make([]MyCardItem, 0, len(c.Items))
		for _, i := range c.Items {
			items = append(items, MyCardItem{ItemID: i.ItemID, ItemName: i.ItemName, UnitPrice: i.UnitPrice, PaidRemaining: i.PaidRemaining, GiftRemaining: i.GiftRemaining})
		}
		out.Items = append(out.Items, MyCard{Kind: c.Kind, Status: c.Status, ID: c.ID, CardTypeName: c.CardTypeName, ExpiresOn: c.ExpiresOn, Principal: c.Principal, Bonus: c.Bonus, Balance: balance, Points: c.Points, OpenedAt: c.OpenedAt, Items: items})
	}
	return out, nil
}

func myCardLedger(ctx context.Context, env ops.Env, in *MyCardLedgerInput) (*MyCardLedger, error) {
	customer, err := selfCustomer(ctx, env)
	if err != nil {
		return nil, err
	}
	card, err := env.Q.GetCard(ctx, dbgen.GetCardParams{StoreID: env.Caller.StoreID, ID: in.CardID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && card.CustomerID != customer.ID) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := env.Q.ListCardLedger(ctx, dbgen.ListCardLedgerParams{CardID: card.ID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := &MyCardLedger{Entries: make([]MyCardLedgerEntry, 0, len(rows))}
	for _, row := range rows {
		l := row.CardLedger
		out.Entries = append(out.Entries, MyCardLedgerEntry{
			OrderID: row.OrderID, ID: l.ID, Kind: l.Kind, BusinessDate: l.BusinessDate.Time.Format(time.DateOnly),
			PrincipalDelta: l.PrincipalDelta, PrincipalAfter: l.PrincipalAfter, BonusDelta: l.BonusDelta, BonusAfter: l.BonusAfter,
		})
	}
	return out, nil
}

func myOrders(ctx context.Context, env ops.Env, in *MyOrdersInput) (*MyOrders, error) {
	customer, err := selfCustomer(ctx, env)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	orders, err := env.Q.ListCustomerOrders(ctx, dbgen.ListCustomerOrdersParams{StoreID: env.Caller.StoreID, CustomerID: customer.ID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(orders))
	for i, order := range orders {
		ids[i] = order.ID
	}
	items, err := env.Q.ListOrderItems(ctx, dbgen.ListOrderItemsParams{StoreID: env.Caller.StoreID, OrderIds: ids})
	if err != nil {
		return nil, err
	}
	itemIDs := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		if !item.RemovedAt.Valid {
			itemIDs = append(itemIDs, item.ID)
		}
	}
	positions, err := env.Q.ListOrderItemPositions(ctx, dbgen.ListOrderItemPositionsParams{StoreID: env.Caller.StoreID, ItemIds: itemIDs})
	if err != nil {
		return nil, err
	}
	staffIDs := make([]uuid.UUID, 0, len(positions))
	for _, p := range positions {
		if p.StaffID != nil {
			staffIDs = append(staffIDs, *p.StaffID)
		}
	}
	names, err := env.Q.StaffNames(ctx, dbgen.StaffNamesParams{StoreID: env.Caller.StoreID, Ids: staffIDs})
	if err != nil {
		return nil, err
	}
	staffNames := make(map[uuid.UUID]string, len(names))
	for _, n := range names {
		staffNames[n.ID] = n.Name
	}
	positionsByItem := map[uuid.UUID][]uuid.UUID{}
	for _, p := range positions {
		if p.StaffID != nil {
			positionsByItem[p.OrderItemID] = append(positionsByItem[p.OrderItemID], *p.StaffID)
		}
	}
	payments, err := env.Q.ListOrderPayments(ctx, dbgen.ListOrderPaymentsParams{StoreID: env.Caller.StoreID, OrderIds: ids})
	if err != nil {
		return nil, err
	}
	itemsByOrder := map[uuid.UUID][]MyOrderItem{}
	for _, item := range items {
		if item.RemovedAt.Valid {
			continue
		}
		line := MyOrderItem{ItemName: item.ItemName, Price: item.Price, StaffNames: []string{}}
		seen := map[uuid.UUID]bool{}
		for _, staffID := range positionsByItem[item.ID] {
			if seen[staffID] {
				continue
			}
			seen[staffID] = true
			if name, ok := staffNames[staffID]; ok {
				line.StaffNames = append(line.StaffNames, name)
			}
		}
		itemsByOrder[item.OrderID] = append(itemsByOrder[item.OrderID], line)
	}
	paymentsByOrder := map[uuid.UUID][]MyOrderPayment{}
	paidByOrder := map[uuid.UUID]money.Total{}
	for _, p := range payments {
		paymentsByOrder[p.OrderID] = append(paymentsByOrder[p.OrderID], MyOrderPayment{CardName: p.CardName, Method: p.Method, Amount: p.Amount})
		paidByOrder[p.OrderID] = paidByOrder[p.OrderID].AddProduct(p.Amount, 1)
	}
	out := &MyOrders{Entries: make([]MyOrder, 0, len(orders))}
	for _, order := range orders {
		var paid money.Amount
		if err := paid.Scan(paidByOrder[order.ID].Format()); err != nil {
			return nil, err
		}
		orderItems := itemsByOrder[order.ID]
		if orderItems == nil {
			orderItems = []MyOrderItem{}
		}
		orderPayments := paymentsByOrder[order.ID]
		if orderPayments == nil {
			orderPayments = []MyOrderPayment{}
		}
		out.Entries = append(out.Entries, MyOrder{ID: order.ID, BusinessDate: order.BusinessDate.Time.Format(time.DateOnly), Status: order.Status, Voided: order.Status == "voided", PaidAmount: paid, Items: orderItems, Payments: orderPayments})
	}
	return out, nil
}

func myAppointments(ctx context.Context, env ops.Env, _ *struct{}) (*MyAppointments, error) {
	customer, err := selfCustomer(ctx, env)
	if err != nil {
		return nil, err
	}
	list, err := booking.ListForCustomer(ctx, env, customer.ID, env.Now, 100)
	if err != nil {
		return nil, err
	}
	out := &MyAppointments{Entries: make([]MyAppointment, 0, len(list))}
	for _, a := range list {
		out.Entries = append(out.Entries, MyAppointment{ID: a.ID, Status: a.Status, StartsAt: a.StartsAt, EndsAt: a.EndsAt, Staff: staff.Ref{ID: a.Staff.ID, Name: a.Staff.Name}, Items: a.Items})
	}
	return out, nil
}
