// Package catalog 提供项目目录和级别价格规则。
package catalog

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// LevelPrice 是项目的级别价。
type LevelPrice struct {
	LevelID uuid.UUID    `json:"level_id"`
	Price   money.Amount `json:"price"`
}

// ItemPositionInput 是按列表顺序分配的项目岗位。
type ItemPositionInput struct {
	PositionID uuid.UUID `json:"position_id"`
	Required   bool      `json:"required"`
}

// ItemPosition 是带顺序的项目岗位。
type ItemPosition struct {
	Slot       int16     `json:"slot"`
	PositionID uuid.UUID `json:"position_id"`
	Required   bool      `json:"required"`
}

// Item 是本店项目及其完整价格规则。
type Item struct {
	ID              uuid.UUID      `json:"id"`
	Name            string         `json:"name"`
	Price           money.Amount   `json:"price"`
	Discountable    bool           `json:"discountable"`
	LaborFeeAmount  *money.Amount  `json:"labor_fee_amount" nullable:"true"`
	LaborFeePercent *money.Percent `json:"labor_fee_percent" nullable:"true"`
	Active          bool           `json:"active"`
	CreatedAt       time.Time      `json:"created_at"`
	LevelPrices     []LevelPrice   `json:"level_prices"`
	Positions       []ItemPosition `json:"positions"`
}

// ItemList 是本店项目列表。
type ItemList struct {
	Items []Item `json:"items"`
}

// ListItemsInput 是列出本店项目的参数。
type ListItemsInput struct{}

// CreateItemInput 是新建项目的完整价格规则。
type CreateItemInput struct {
	Name            string              `json:"name" minLength:"1" maxLength:"40"`
	Price           money.Amount        `json:"price"`
	Discountable    bool                `json:"discountable"`
	LaborFeeAmount  *money.Amount       `json:"labor_fee_amount" nullable:"true"`
	LaborFeePercent *money.Percent      `json:"labor_fee_percent" nullable:"true"`
	Positions       []ItemPositionInput `json:"positions" maxItems:"20"`
	LevelPrices     []LevelPrice        `json:"level_prices" maxItems:"20"`
}

// UpdateItemInput 是替换项目价格规则和启用状态的参数。
type UpdateItemInput struct {
	CreateItemInput
	ID     uuid.UUID `json:"id"`
	Active bool      `json:"active"`
}

// QuoteItemPriceInput 指定做项目的员工。
type QuoteItemPriceInput struct {
	ItemID  uuid.UUID `json:"item_id"`
	StaffID uuid.UUID `json:"staff_id"`
}

// ItemPrice 是项目标准价或适用的级别价。
type ItemPrice struct {
	Price   money.Amount `json:"price"`
	LevelID *uuid.UUID   `json:"level_id,omitempty"`
}

// Register 声明项目目录与价格操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListItemsInput, ItemList]{
		Name: "list_items", Summary: "列出本店项目及标准价、级别价和项目岗位", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *ListItemsInput) (*ItemList, error) {
			rows, err := env.Q.ListItems(ctx, env.Caller.StoreID)
			if err != nil {
				return nil, err
			}
			items, err := views(ctx, env.Q, env.Caller.StoreID, rows)
			return &ItemList{Items: items}, err
		},
	})
	ops.Register(r, ops.Operation[CreateItemInput, Item]{
		Name: "create_item", Summary: "新建项目，设置标准价、级别价、手工费和项目岗位", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: createItem,
	})
	ops.Register(r, ops.Operation[UpdateItemInput, Item]{
		Name: "update_item", Summary: "修改项目价格规则、项目岗位或停用项目", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: updateItem,
	})
	ops.Register(r, ops.Operation[QuoteItemPriceInput, ItemPrice]{
		Name: "quote_item_price", Summary: "查询指定员工做项目时适用的价格，优先级别价，否则标准价", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *QuoteItemPriceInput) (*ItemPrice, error) {
			return quoteItemPrice(ctx, env.Q, env.Caller.StoreID, in.ItemID, in.StaffID)
		},
	})
}

func views(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, rows []dbgen.Item) ([]Item, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	levels, err := q.ListItemLevelPrices(ctx, dbgen.ListItemLevelPricesParams{StoreID: storeID, ItemIds: ids})
	if err != nil {
		return nil, err
	}
	positions, err := q.ListItemPositions(ctx, dbgen.ListItemPositionsParams{StoreID: storeID, ItemIds: ids})
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	indices := map[uuid.UUID]int{}
	for _, r := range rows {
		indices[r.ID] = len(out)
		out = append(out, Item{ID: r.ID, Name: r.Name, Price: r.Price, Discountable: r.Discountable, LaborFeeAmount: r.LaborFeeAmount, LaborFeePercent: r.LaborFeePercent, Active: r.Active, CreatedAt: r.CreatedAt.Time, LevelPrices: []LevelPrice{}, Positions: []ItemPosition{}})
	}
	for _, l := range levels {
		if i, ok := indices[l.ItemID]; ok {
			out[i].LevelPrices = append(out[i].LevelPrices, LevelPrice{LevelID: l.LevelID, Price: l.Price})
		}
	}
	for _, p := range positions {
		if i, ok := indices[p.ItemID]; ok {
			out[i].Positions = append(out[i].Positions, ItemPosition{Slot: p.Slot, PositionID: p.PositionID, Required: p.Required})
		}
	}
	return out, nil
}

func view(ctx context.Context, env ops.Env, row dbgen.Item) (*Item, error) {
	items, err := views(ctx, env.Q, env.Caller.StoreID, []dbgen.Item{row})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	var e *pgconn.PgError
	if errors.As(err, &e) && e.Code == "23505" {
		return ops.Rule("项目名称重复")
	}
	return err
}

func validate(in *CreateItemInput) error {
	if in.LaborFeeAmount != nil && in.LaborFeePercent != nil {
		return ops.Rule("手工费只能选择固定金额或售价百分比")
	}
	seen := map[uuid.UUID]bool{}
	for _, l := range in.LevelPrices {
		if seen[l.LevelID] {
			return ops.Rule("级别价的级别不能重复")
		}
		seen[l.LevelID] = true
	}
	return nil
}

func createItem(ctx context.Context, env ops.Env, in *CreateItemInput) (*Item, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	if err := shareReferences(ctx, env, in, &Item{}); err != nil {
		return nil, err
	}
	row, err := env.Q.CreateItem(ctx, dbgen.CreateItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name, Price: in.Price, Discountable: in.Discountable, LaborFeeAmount: in.LaborFeeAmount, LaborFeePercent: in.LaborFeePercent})
	if err != nil {
		return nil, rowError(err)
	}
	if err := writeChildren(ctx, env, row.ID, &Item{}, in); err != nil {
		return nil, err
	}
	after, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "create_item", audit.ItemTarget(row.ID), nil, after); err != nil {
		return nil, err
	}
	return after, nil
}

func updateItem(ctx context.Context, env ops.Env, in *UpdateItemInput) (*Item, error) {
	row, err := env.Q.LockItem(ctx, dbgen.LockItemParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	before, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := validate(&in.CreateItemInput); err != nil {
		return nil, err
	}
	if err := shareReferences(ctx, env, &in.CreateItemInput, before); err != nil {
		return nil, err
	}
	row, err = env.Q.UpdateItem(ctx, dbgen.UpdateItemParams{ID: in.ID, Name: in.Name, Price: in.Price, Discountable: in.Discountable, LaborFeeAmount: in.LaborFeeAmount, LaborFeePercent: in.LaborFeePercent, Active: in.Active})
	if err != nil {
		return nil, rowError(err)
	}
	if err := writeChildren(ctx, env, in.ID, before, &in.CreateItemInput); err != nil {
		return nil, err
	}
	after, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "update_item", audit.ItemTarget(in.ID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

// shareReferences 先按 UUID 锁级别，再按 UUID 锁岗位，各条写入路径保持相同顺序。
func shareReferences(ctx context.Context, env ops.Env, in *CreateItemInput, before *Item) error {
	oldLevels := map[uuid.UUID]bool{}
	for _, l := range before.LevelPrices {
		oldLevels[l.LevelID] = true
	}
	levels := []uuid.UUID{}
	for _, l := range in.LevelPrices {
		if !oldLevels[l.LevelID] {
			levels = append(levels, l.LevelID)
		}
	}
	order := func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) }
	slices.SortFunc(levels, order)
	for _, id := range levels {
		l, err := env.Q.ShareLevel(ctx, dbgen.ShareLevelParams{ID: id, StoreID: env.Caller.StoreID})
		if err != nil {
			return rowError(err)
		}
		if !l.Active {
			return ops.Rule("不能新增停用级别的级别价")
		}
	}
	positions := []uuid.UUID{}
	for i, p := range in.Positions {
		if i >= len(before.Positions) || before.Positions[i].PositionID != p.PositionID {
			positions = append(positions, p.PositionID)
		}
	}
	slices.SortFunc(positions, order)
	positions = slices.Compact(positions)
	for _, id := range positions {
		p, err := env.Q.SharePosition(ctx, dbgen.SharePositionParams{ID: id, StoreID: env.Caller.StoreID})
		if err != nil {
			return rowError(err)
		}
		if !p.Active {
			return ops.Rule("不能新增停用的项目岗位")
		}
	}
	return nil
}

func writeChildren(ctx context.Context, env ops.Env, id uuid.UUID, before *Item, in *CreateItemInput) error {
	old := map[uuid.UUID]LevelPrice{}
	for _, l := range before.LevelPrices {
		old[l.LevelID] = l
	}
	next := map[uuid.UUID]bool{}
	for _, l := range in.LevelPrices {
		next[l.LevelID] = true
	}
	for _, l := range before.LevelPrices {
		if !next[l.LevelID] {
			if err := env.Q.DeleteItemLevelPrice(ctx, dbgen.DeleteItemLevelPriceParams{ItemID: id, LevelID: l.LevelID}); err != nil {
				return err
			}
		}
	}
	for _, l := range in.LevelPrices {
		previous, exists := old[l.LevelID]
		if !exists {
			if err := env.Q.AddItemLevelPrice(ctx, dbgen.AddItemLevelPriceParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, ItemID: id, LevelID: l.LevelID, Price: l.Price}); err != nil {
				return err
			}
		} else if previous.Price.String() != l.Price.String() {
			if err := env.Q.UpdateItemLevelPrice(ctx, dbgen.UpdateItemLevelPriceParams{ItemID: id, LevelID: l.LevelID, Price: l.Price}); err != nil {
				return err
			}
		}
	}
	for _, p := range before.Positions {
		if int(p.Slot) > len(in.Positions) {
			if err := env.Q.DeleteItemPosition(ctx, dbgen.DeleteItemPositionParams{ItemID: id, Slot: p.Slot}); err != nil {
				return err
			}
		}
	}
	for i, p := range in.Positions {
		slot := int16(i + 1)
		if i >= len(before.Positions) {
			if err := env.Q.AddItemPosition(ctx, dbgen.AddItemPositionParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, ItemID: id, Slot: slot, PositionID: p.PositionID, Required: p.Required}); err != nil {
				return err
			}
		} else if before.Positions[i].PositionID != p.PositionID || before.Positions[i].Required != p.Required {
			if err := env.Q.UpdateItemPosition(ctx, dbgen.UpdateItemPositionParams{ItemID: id, Slot: slot, PositionID: p.PositionID, Required: p.Required}); err != nil {
				return err
			}
		}
	}
	return nil
}

func quoteItemPrice(ctx context.Context, q *dbgen.Queries, storeID, itemID, staffID uuid.UUID) (*ItemPrice, error) {
	item, err := q.GetItem(ctx, dbgen.GetItemParams{StoreID: storeID, ID: itemID})
	if err != nil {
		return nil, rowError(err)
	}
	employee, err := q.GetStaff(ctx, staffID)
	if err != nil {
		return nil, rowError(err)
	}
	if employee.StoreID != storeID {
		return nil, ops.ErrNotFound
	}
	if !item.Active {
		return nil, ops.Rule("项目已停用")
	}
	out := &ItemPrice{Price: item.Price}
	if employee.LevelID != nil {
		levelPrice, err := q.GetItemLevelPrice(ctx, dbgen.GetItemLevelPriceParams{ItemID: itemID, LevelID: *employee.LevelID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			out.Price = levelPrice.Price
			out.LevelID = employee.LevelID
		}
	}
	return out, nil
}
