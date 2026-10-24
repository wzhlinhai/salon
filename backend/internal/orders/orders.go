package orders

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Register 声明开单、项目行修改和挂单读操作。
func Register(r *ops.Registry) {
	registerEdits(r)
	registerMerge(r)
	registerCheckout(r)
	registerPreview(r)
	ops.Register(r, ops.Operation[OpenOrderInput, Order]{Name: "open_order", Summary: "开单并保存规则快照", Kind: ops.Write, Permission: perm.OrderCreate, AI: ops.AIDraft, Handle: openOrder})
	ops.Register(r, ops.Operation[AddOrderItemInput, Order]{Name: "add_order_item", Summary: "向单添加项目行", Kind: ops.Write, Permission: perm.OrderCreate, AI: ops.AIDraft, Handle: addOrderItem})
	ops.Register(r, ops.Operation[ListOpenOrdersInput, OrderList]{Name: "list_open_orders", Summary: "列出本店挂单", Kind: ops.Read, Permission: perm.OrderCreate, AI: ops.AIDirect, Handle: listOpenOrders})
	ops.Register(r, ops.Operation[GetOrderInput, Order]{Name: "get_order", Summary: "查看本店单据", Kind: ops.Read, Permission: perm.OrderCreate, AI: ops.AIDirect, Handle: getOrder})
}

func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	return err
}

func openOrder(ctx context.Context, env ops.Env, in *OpenOrderInput) (*Order, error) {
	return openOrderAt(ctx, env, in, time.Time{})
}

func openOrderAt(ctx context.Context, env ops.Env, in *OpenOrderInput, now time.Time) (*Order, error) {
	var customerID uuid.UUID
	if in.CustomerID != nil {
		row, err := customers.LockReference(ctx, env, *in.CustomerID)
		if err != nil {
			return nil, rowError(err)
		}
		customerID = row.ID
	} else {
		id, err := customers.CreateUnidentified(ctx, env, in.Gender)
		if err != nil {
			return nil, err
		}
		customerID = id
	}
	if now.IsZero() {
		now = time.Now()
	}
	snapshot, err := settings.TakeSnapshot(ctx, env.Q, env.Caller.TenantID, env.Caller.StoreID, now)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CreateOrder(ctx, dbgen.CreateOrderParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: customerID, Gender: in.Gender, Designated: in.Designated, SettingsSnapshotID: snapshot.ID, BusinessDate: pgtype.Date{Time: snapshot.BusinessDate, Valid: true}, OpenedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	for _, item := range in.Items {
		if err := addItem(ctx, env, row.ID, item, snapshot.Rules); err != nil {
			return nil, err
		}
	}
	out, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "open_order", audit.OrderTarget(row.ID, customerID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func addOrderItem(ctx context.Context, env ops.Env, in *AddOrderItemInput) (*Order, error) {
	return changeOne(ctx, env, "add_order_item", in.OrderID, func(row *dbgen.Order) error {
		rules, err := settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, row.SettingsSnapshotID)
		if err != nil {
			return err
		}
		return addItem(ctx, env, row.ID, in.Item, rules)
	})
}

func addItem(ctx context.Context, env ops.Env, orderID uuid.UUID, in OrderItemInput, rules settings.Rules) error {
	item, err := catalog.LockReference(ctx, env.Q, env.Caller.StoreID, in.ItemID)
	if err != nil {
		return rowError(err)
	}
	positions, err := env.Q.ListItemPositions(ctx, dbgen.ListItemPositionsParams{StoreID: env.Caller.StoreID, ItemIds: []uuid.UUID{item.ID}})
	if err != nil {
		return err
	}
	slots := map[int16]bool{}
	for _, p := range positions {
		slots[p.Slot] = true
	}
	assigned, price, err := catalog.QuoteLine(ctx, env, item.ID, slots, lineStaff(in.Staff), nil, rules)
	if err != nil {
		return err
	}
	row, err := env.Q.CreateOrderItem(ctx, dbgen.CreateOrderItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, OrderID: orderID, ItemID: item.ID, ItemName: item.Name, Price: price.Price, PriceSource: price.Source, LevelID: price.LevelID, AddedBy: env.Caller.StaffID})
	if err != nil {
		return err
	}
	for _, p := range positions {
		if err := env.Q.AddOrderItemPosition(ctx, dbgen.AddOrderItemPositionParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, OrderItemID: row.ID, Slot: p.Slot, PositionID: p.PositionID, Required: p.Required, StaffID: assigned[p.Slot]}); err != nil {
			return err
		}
	}
	return nil
}

func getOrder(ctx context.Context, env ops.Env, in *GetOrderInput) (*Order, error) {
	row, err := env.Q.GetOrder(ctx, dbgen.GetOrderParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	return view(ctx, env, row)
}

func listOpenOrders(ctx context.Context, env ops.Env, _ *ListOpenOrdersInput) (*OrderList, error) {
	rows, err := env.Q.ListOpenOrders(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out, err := views(ctx, env, rows)
	return &OrderList{Items: out}, err
}

func view(ctx context.Context, env ops.Env, row dbgen.Order) (*Order, error) {
	out, err := views(ctx, env, []dbgen.Order{row})
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

func views(ctx context.Context, env ops.Env, rows []dbgen.Order) ([]Order, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	items, err := env.Q.ListOrderItems(ctx, dbgen.ListOrderItemsParams{StoreID: env.Caller.StoreID, OrderIds: ids})
	if err != nil {
		return nil, err
	}
	payments, err := env.Q.ListOrderPayments(ctx, dbgen.ListOrderPaymentsParams{StoreID: env.Caller.StoreID, OrderIds: ids})
	if err != nil {
		return nil, err
	}
	byPaymentOrder := map[uuid.UUID][]OrderPayment{}
	for _, p := range payments {
		payment := OrderPayment{DebtID: p.DebtID, ID: p.ID, Method: p.Method, Amount: p.Amount}
		if p.Method == "card" {
			payment.Card = &OrderPaymentCard{CardID: *p.CardID, Principal: *p.Principal, Bonus: *p.Bonus}
		}
		if p.Method == "count" {
			payment.Count = &OrderPaymentCount{CardID: *p.CardID, OrderItemID: *p.OrderItemID, ItemID: *p.CountItemID, Paid: p.Paid, Gift: p.Gift}
		}
		byPaymentOrder[p.OrderID] = append(byPaymentOrder[p.OrderID], payment)
	}
	itemIDs := make([]uuid.UUID, len(items))
	for i, row := range items {
		itemIDs[i] = row.ID
	}
	positions, err := env.Q.ListOrderItemPositions(ctx, dbgen.ListOrderItemPositionsParams{StoreID: env.Caller.StoreID, ItemIds: itemIDs})
	if err != nil {
		return nil, err
	}
	byItem := map[uuid.UUID][]OrderItemPosition{}
	for _, p := range positions {
		byItem[p.OrderItemID] = append(byItem[p.OrderItemID], OrderItemPosition{Slot: p.Slot, PositionID: p.PositionID, Required: p.Required, StaffID: p.StaffID})
	}
	byOrder := map[uuid.UUID][]OrderItem{}
	totals := map[uuid.UUID]money.Total{}
	discounts := map[uuid.UUID]money.Total{}
	nets := map[uuid.UUID]money.Total{}
	for _, i := range items {
		p := byItem[i.ID]
		if p == nil {
			p = []OrderItemPosition{}
		}
		byOrder[i.OrderID] = append(byOrder[i.OrderID], OrderItem{ID: i.ID, ItemID: i.ItemID, ItemName: i.ItemName, Price: i.Price, PriceSource: i.PriceSource, CardTypeID: i.CardTypeID, Discount: i.Discount, LevelID: i.LevelID, AddedAt: i.AddedAt.Time, RemovedAt: removedTime(i.RemovedAt), RemovedBy: i.RemovedBy, RepriceReason: i.RepriceReason, Positions: p})
		if !i.RemovedAt.Valid {
			totals[i.OrderID] = totals[i.OrderID].AddProduct(i.Price, 1)
			discounts[i.OrderID] = discounts[i.OrderID].AddProduct(i.Discount, 1)
			net, err := i.Price.Apply(money.Amount{}.Sub(i.Discount))
			if err != nil {
				return nil, err
			}
			nets[i.OrderID] = nets[i.OrderID].AddProduct(net, 1)
		}
	}
	out := make([]Order, 0, len(rows))
	for _, r := range rows {
		items := byOrder[r.ID]
		if items == nil {
			items = []OrderItem{}
		}
		ps := byPaymentOrder[r.ID]
		if ps == nil {
			ps = []OrderPayment{}
		}
		out = append(out, Order{CheckoutID: r.CheckoutID, Payments: ps, ID: r.ID, CustomerID: r.CustomerID, Gender: r.Gender, Designated: r.Designated, Status: r.Status, MergedInto: r.MergedInto, BusinessDate: r.BusinessDate.Time.Format(time.DateOnly), OpenedBy: r.OpenedBy, OpenedAt: r.OpenedAt.Time, Items: items, Receivable: totals[r.ID].Format(), Discount: discounts[r.ID].Format(), Net: nets[r.ID].Format()})
	}
	return out, nil
}

func lineStaff(in []OrderItemStaffInput) []catalog.LineStaff {
	out := make([]catalog.LineStaff, len(in))
	for i, s := range in {
		out[i] = catalog.LineStaff{Slot: s.Slot, StaffID: s.StaffID}
	}
	return out
}

func removedTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}
