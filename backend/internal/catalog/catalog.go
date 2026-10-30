// Package catalog 提供项目目录和级别价格规则。
package catalog

import (
	"context"
	"errors"
	"reflect"
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
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// LevelPrice 是项目的级别价。
type LevelPrice struct {
	LevelID uuid.UUID    `json:"level_id"`
	Price   money.Amount `json:"price"`
}

// MemberPrice 是项目按卡类型设置的会员价。
type MemberPrice struct {
	CardTypeID uuid.UUID    `json:"card_type_id"`
	Price      money.Amount `json:"price"`
}

// ItemPositionInput 是按列表顺序分配的项目岗位。
type ItemPositionInput struct {
	SharePercent *int16    `json:"share_percent,omitempty" nullable:"true" minimum:"0" maximum:"100" doc:"岗位比例，分配多人业绩用；都不填为均分"`
	PositionID   uuid.UUID `json:"position_id"`
	Required     bool      `json:"required"`
}

// ItemPosition 是带顺序的项目岗位。
type ItemPosition struct {
	SharePercent *int16    `json:"share_percent" nullable:"true" minimum:"0" maximum:"100" doc:"岗位比例，分配多人业绩用；都不填为均分"`
	Slot         int16     `json:"slot"`
	PositionID   uuid.UUID `json:"position_id"`
	Required     bool      `json:"required"`
}

// Item 是本店项目及其完整价格规则。
type Item struct {
	Kind            string         `json:"kind" enum:"service,goods"`
	MemberPrices    []MemberPrice  `json:"member_prices"`
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
	Kind            string              `json:"kind,omitempty" enum:"service,goods" doc:"项目的种类，不填时为服务"`
	MemberPrices    []MemberPrice       `json:"member_prices,omitempty" maxItems:"20"`
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
	CardTypeID *uuid.UUID `json:"card_type_id,omitempty"`
	ItemID     uuid.UUID  `json:"item_id"`
	StaffID    uuid.UUID  `json:"staff_id"`
}

// ItemPrice 是项目适用的标准价、级别价或会员价及其来源。
type ItemPrice struct {
	Source     string       `json:"source" enum:"standard,level,member"`
	CardTypeID *uuid.UUID   `json:"card_type_id,omitempty"`
	Price      money.Amount `json:"price"`
	LevelID    *uuid.UUID   `json:"level_id,omitempty"`
}

// Register 声明项目目录与价格操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListItemsInput, ItemList]{
		Name: "list_items", Summary: "列出本店项目及种类、标准价、级别价、会员价和项目岗位", Kind: ops.Read,
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
		Name: "create_item", Summary: "新建项目，设置种类、标准价、级别价、会员价、手工费和项目岗位", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: createItem,
	})
	ops.Register(r, ops.Operation[UpdateItemInput, Item]{
		Name: "update_item", Summary: "修改项目种类、价格规则、项目岗位或停用项目", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: updateItem,
	})
	ops.Register(r, ops.Operation[QuoteItemPriceInput, ItemPrice]{
		Name: "quote_item_price", Summary: "按门店规则查询员工及卡类型适用的项目价格", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *QuoteItemPriceInput) (*ItemPrice, error) {
			day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
			if err != nil {
				return nil, err
			}
			rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
			if err != nil {
				return nil, err
			}
			return Quote(ctx, env.Q, env.Caller.StoreID, in.ItemID, &in.StaffID, in.CardTypeID, rules)
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
	members, err := q.ListItemMemberPrices(ctx, dbgen.ListItemMemberPricesParams{StoreID: storeID, ItemIds: ids})
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	indices := map[uuid.UUID]int{}
	for _, r := range rows {
		indices[r.ID] = len(out)
		out = append(out, Item{ID: r.ID, Name: r.Name, Kind: r.Kind, Price: r.Price, Discountable: r.Discountable, LaborFeeAmount: r.LaborFeeAmount, LaborFeePercent: r.LaborFeePercent, Active: r.Active, CreatedAt: r.CreatedAt.Time, MemberPrices: []MemberPrice{}, LevelPrices: []LevelPrice{}, Positions: []ItemPosition{}})
	}
	for _, m := range members {
		if i, ok := indices[m.ItemID]; ok {
			out[i].MemberPrices = append(out[i].MemberPrices, MemberPrice{CardTypeID: m.CardTypeID, Price: m.Price})
		}
	}
	for _, l := range levels {
		if i, ok := indices[l.ItemID]; ok {
			out[i].LevelPrices = append(out[i].LevelPrices, LevelPrice{LevelID: l.LevelID, Price: l.Price})
		}
	}
	for _, p := range positions {
		if i, ok := indices[p.ItemID]; ok {
			out[i].Positions = append(out[i].Positions, ItemPosition{Slot: p.Slot, PositionID: p.PositionID, Required: p.Required, SharePercent: p.SharePercent})
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
	set, total := 0, 0
	for _, p := range in.Positions {
		if p.SharePercent != nil {
			set++
			total += int(*p.SharePercent)
		}
	}
	if set > 0 && (set != len(in.Positions) || total != 100) {
		return ops.Rule("岗位比例要么都不填，要么都填且合计 100%")
	}
	if in.Kind == "" {
		in.Kind = "service"
	}
	if in.LaborFeeAmount != nil && in.LaborFeePercent != nil {
		return ops.Rule("手工费只能选择固定金额或售价百分比")
	}
	memberSeen := map[uuid.UUID]bool{}
	for _, m := range in.MemberPrices {
		if memberSeen[m.CardTypeID] {
			return ops.Rule("会员价的卡类型不能重复")
		}
		memberSeen[m.CardTypeID] = true
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
	row, err := env.Q.CreateItem(ctx, dbgen.CreateItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name, Kind: in.Kind, Price: in.Price, Discountable: in.Discountable, LaborFeeAmount: in.LaborFeeAmount, LaborFeePercent: in.LaborFeePercent})
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
	row, err = env.Q.UpdateItem(ctx, dbgen.UpdateItemParams{ID: in.ID, Name: in.Name, Kind: in.Kind, Price: in.Price, Discountable: in.Discountable, LaborFeeAmount: in.LaborFeeAmount, LaborFeePercent: in.LaborFeePercent, Active: in.Active})
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

// shareReferences 按级别、岗位、卡类型的顺序，各组按 UUID 加共享锁。
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
	oldTypes := map[uuid.UUID]bool{}
	for _, m := range before.MemberPrices {
		oldTypes[m.CardTypeID] = true
	}
	types := []uuid.UUID{}
	for _, m := range in.MemberPrices {
		if !oldTypes[m.CardTypeID] {
			types = append(types, m.CardTypeID)
		}
	}
	slices.SortFunc(types, order)
	for _, id := range types {
		ct, err := env.Q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: env.Caller.StoreID, ID: id})
		if err != nil {
			return rowError(err)
		}
		if !ct.Active {
			return ops.Rule("不能新增停用卡类型的会员价")
		}
	}

	return nil
}

func writeChildren(ctx context.Context, env ops.Env, id uuid.UUID, before *Item, in *CreateItemInput) error {
	oldMembers := map[uuid.UUID]MemberPrice{}
	nextMembers := map[uuid.UUID]bool{}
	for _, m := range before.MemberPrices {
		oldMembers[m.CardTypeID] = m
	}
	for _, m := range in.MemberPrices {
		nextMembers[m.CardTypeID] = true
	}
	for _, m := range before.MemberPrices {
		if !nextMembers[m.CardTypeID] {
			if err := env.Q.DeleteItemMemberPrice(ctx, dbgen.DeleteItemMemberPriceParams{ItemID: id, CardTypeID: m.CardTypeID}); err != nil {
				return err
			}
		}
	}
	for _, m := range in.MemberPrices {
		previous, exists := oldMembers[m.CardTypeID]
		if !exists {
			if err := env.Q.AddItemMemberPrice(ctx, dbgen.AddItemMemberPriceParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, ItemID: id, CardTypeID: m.CardTypeID, Price: m.Price}); err != nil {
				return err
			}
		} else if previous.Price.Cmp(m.Price) != 0 {
			if err := env.Q.UpdateItemMemberPrice(ctx, dbgen.UpdateItemMemberPriceParams{ItemID: id, CardTypeID: m.CardTypeID, Price: m.Price}); err != nil {
				return err
			}
		}
	}
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
		} else if previous.Price.Cmp(l.Price) != 0 {
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
			if err := env.Q.AddItemPosition(ctx, dbgen.AddItemPositionParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, ItemID: id, Slot: slot, PositionID: p.PositionID, Required: p.Required, SharePercent: p.SharePercent}); err != nil {
				return err
			}
		} else if before.Positions[i].PositionID != p.PositionID || before.Positions[i].Required != p.Required || !reflect.DeepEqual(before.Positions[i].SharePercent, p.SharePercent) {
			if err := env.Q.UpdateItemPosition(ctx, dbgen.UpdateItemPositionParams{ItemID: id, Slot: slot, PositionID: p.PositionID, Required: p.Required, SharePercent: p.SharePercent}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Quote 先共享锁住项目和员工，再按给定规则计算标准价、级别价或会员价。
func Quote(ctx context.Context, q *dbgen.Queries, storeID, itemID uuid.UUID, staffID, cardTypeID *uuid.UUID, rules settings.Rules) (*ItemPrice, error) {
	item, err := LockReference(ctx, q, storeID, itemID)
	if err != nil {
		return nil, rowError(err)
	}
	var employee dbgen.Staff
	if staffID != nil {
		rows, lockErr := staff.LockReferences(ctx, q, storeID, *staffID)
		if lockErr != nil {
			return nil, lockErr
		}
		employee = rows[*staffID]
	}
	out := &ItemPrice{Price: item.Price, Source: "standard"}
	if staffID != nil && employee.LevelID != nil {
		levelPrice, err := q.GetItemLevelPrice(ctx, dbgen.GetItemLevelPriceParams{ItemID: itemID, LevelID: *employee.LevelID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			out.Price = levelPrice.Price
			out.LevelID = employee.LevelID
			out.Source = "level"
		}
	}
	if cardTypeID != nil {
		if _, err := q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: storeID, ID: *cardTypeID}); err != nil {
			return nil, rowError(err)
		}
		member, err := q.GetItemMemberPrice(ctx, dbgen.GetItemMemberPriceParams{ItemID: itemID, CardTypeID: *cardTypeID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil && item.Discountable && (rules.MemberPriceRule() == "member_first" || member.Price.Cmp(out.Price) <= 0) {
			out.Price = member.Price
			out.Source = "member"
			out.CardTypeID = cardTypeID
			out.LevelID = nil
		}
	}

	return out, nil
}
