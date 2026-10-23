package orders

import (
	"context"
	"errors"
	"slices"
	"strings"
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
		row, err := env.Q.ShareOrderCustomer(ctx, dbgen.ShareOrderCustomerParams{StoreID: env.Caller.StoreID, ID: *in.CustomerID})
		if err != nil {
			return nil, rowError(err)
		}
		if !customers.CanView(env.Caller, row) {
			return nil, ops.ErrNotFound
		}
		if row.MergedInto != nil {
			return nil, ops.Rule("该顾客已合并到另一位顾客")
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
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CreateOrder(ctx, dbgen.CreateOrderParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: customerID, Gender: in.Gender, Designated: in.Designated, SettingsSnapshotID: snapshot, BusinessDate: pgtype.Date{Time: day, Valid: true}, OpenedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	rules, err := settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, snapshot)
	if err != nil {
		return nil, err
	}
	for _, item := range in.Items {
		if err := addItem(ctx, env, row.ID, item, rules); err != nil {
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
	row, err := lockOpenOrder(ctx, env, in.OrderID)
	if err != nil {
		return nil, rowError(err)
	}
	before, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	rules, err := settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, row.SettingsSnapshotID)
	if err != nil {
		return nil, err
	}
	if err := addItem(ctx, env, row.ID, in.Item, rules); err != nil {
		return nil, err
	}
	after, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "add_order_item", audit.OrderTarget(row.ID, row.CustomerID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

func addItem(ctx context.Context, env ops.Env, orderID uuid.UUID, in OrderItemInput, rules settings.Rules) error {
	item, err := env.Q.ShareOrderItem(ctx, dbgen.ShareOrderItemParams{StoreID: env.Caller.StoreID, ID: in.ItemID})
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
	assigned, err := validateStaff(ctx, env, slots, in.Staff)
	if err != nil {
		return err
	}
	price, err := catalog.Quote(ctx, env.Q, env.Caller.StoreID, item.ID, assigned[1], nil, rules)
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
	for _, i := range items {
		p := byItem[i.ID]
		if p == nil {
			p = []OrderItemPosition{}
		}
		byOrder[i.OrderID] = append(byOrder[i.OrderID], OrderItem{ID: i.ID, ItemID: i.ItemID, ItemName: i.ItemName, Price: i.Price, PriceSource: i.PriceSource, LevelID: i.LevelID, AddedAt: i.AddedAt.Time, RemovedAt: removedTime(i.RemovedAt), RemovedBy: i.RemovedBy, RepriceReason: i.RepriceReason, Positions: p})
		if !i.RemovedAt.Valid {
			totals[i.OrderID] = totals[i.OrderID].AddProduct(i.Price, 1)
		}
	}
	out := make([]Order, 0, len(rows))
	for _, r := range rows {
		items := byOrder[r.ID]
		if items == nil {
			items = []OrderItem{}
		}
		out = append(out, Order{ID: r.ID, CustomerID: r.CustomerID, Gender: r.Gender, Designated: r.Designated, Status: r.Status, MergedInto: r.MergedInto, BusinessDate: r.BusinessDate.Time.Format(time.DateOnly), OpenedBy: r.OpenedBy, OpenedAt: r.OpenedAt.Time, Items: items, Receivable: totals[r.ID].Format()})
	}
	return out, nil
}

func validateStaff(ctx context.Context, env ops.Env, slots map[int16]bool, staffInput []OrderItemStaffInput) (map[int16]*uuid.UUID, error) {
	assigned := map[int16]*uuid.UUID{}
	ids := []uuid.UUID{}
	for _, s := range staffInput {
		if !slots[s.Slot] {
			return nil, ops.Rule("项目没有这个岗位")
		}
		if assigned[s.Slot] != nil {
			return nil, ops.Rule("同一岗位不能重复填员工")
		}
		assigned[s.Slot] = new(s.StaffID)
		ids = append(ids, s.StaffID)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, id := range slices.Compact(ids) {
		staff, err := env.Q.ShareOrderStaff(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!staff.Active || staff.StoreID != env.Caller.StoreID)) {
			return nil, ops.Rule("岗位上的员工必须是本店在职员工")
		}
		if err != nil {
			return nil, err
		}
	}
	return assigned, nil
}

func removedTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}
