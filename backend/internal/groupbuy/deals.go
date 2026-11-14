package groupbuy

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func registerDeals(r *ops.Registry) {
	ops.Register(r, ops.Operation[CreateDealInput, Deal]{Name: "create_groupbuy_deal", Summary: "登记团购平台商品、平台购买价和门店项目", Kind: ops.Write, Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: createDeal})
	ops.Register(r, ops.Operation[UpdateDealInput, Deal]{Name: "update_groupbuy_deal", Summary: "修改团购商品名称、平台购买价、项目或停用", Kind: ops.Write, Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: updateDeal})
	ops.Register(r, ops.Operation[ListDealsInput, DealList]{Name: "list_groupbuy_deals", Summary: "列出本店团购商品及对应项目和平台购买价", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect, Handle: listDeals})
}

func dealError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ops.Rule("团购商品名称已存在")
	}
	return err
}

func dealView(row dbgen.GroupbuyDeal, itemName, createdByName string) *Deal {
	return &Deal{ID: row.ID, Platform: row.Platform, Name: row.Name, PurchasePrice: row.PurchasePrice, ItemID: row.ItemID, ItemName: itemName, Active: row.Active, CreatedBy: row.CreatedBy, CreatedByName: createdByName, CreatedAt: row.CreatedAt.Time}
}

func loadDealView(ctx context.Context, env ops.Env, row dbgen.GroupbuyDeal) (*Deal, error) {
	item, err := env.Q.GetItem(ctx, dbgen.GetItemParams{StoreID: env.Caller.StoreID, ID: row.ItemID})
	if err != nil {
		return nil, dealError(err)
	}
	staff, err := env.Q.GetStaff(ctx, row.CreatedBy)
	if err != nil {
		return nil, dealError(err)
	}
	return dealView(row, item.Name, staff.Name), nil
}

func createDeal(ctx context.Context, env ops.Env, in *CreateDealInput) (*Deal, error) {
	platform := strings.TrimSpace(in.Platform)
	name := strings.TrimSpace(in.Name)
	if platform == "" || name == "" {
		return nil, ops.Rule("团购平台和商品名称不能为空")
	}
	if _, err := env.Q.GetItem(ctx, dbgen.GetItemParams{StoreID: env.Caller.StoreID, ID: in.ItemID}); err != nil {
		return nil, dealError(err)
	}
	row, err := env.Q.CreateGroupbuyDeal(ctx, dbgen.CreateGroupbuyDealParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Platform: platform, Name: name, PurchasePrice: in.PurchasePrice, ItemID: in.ItemID, CreatedBy: env.Caller.StaffID})
	if err != nil {
		return nil, dealError(err)
	}
	out, err := loadDealView(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "create_groupbuy_deal", audit.GroupbuyDealTarget(row.ID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func updateDeal(ctx context.Context, env ops.Env, in *UpdateDealInput) (*Deal, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ops.Rule("团购商品名称不能为空")
	}
	current, err := env.Q.LockGroupbuyDeal(ctx, dbgen.LockGroupbuyDealParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, dealError(err)
	}
	before, err := loadDealView(ctx, env, current)
	if err != nil {
		return nil, err
	}
	if _, err := env.Q.GetItem(ctx, dbgen.GetItemParams{StoreID: env.Caller.StoreID, ID: in.ItemID}); err != nil {
		return nil, dealError(err)
	}
	row, err := env.Q.UpdateGroupbuyDeal(ctx, dbgen.UpdateGroupbuyDealParams{StoreID: env.Caller.StoreID, ID: in.ID, Name: name, PurchasePrice: in.PurchasePrice, ItemID: in.ItemID, Active: in.Active})
	if err != nil {
		return nil, dealError(err)
	}
	out, err := loadDealView(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "update_groupbuy_deal", audit.GroupbuyDealTarget(row.ID), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func listDeals(ctx context.Context, env ops.Env, _ *ListDealsInput) (*DealList, error) {
	rows, err := env.Q.ListGroupbuyDeals(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := &DealList{Items: make([]Deal, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, Deal{ID: row.ID, Platform: row.Platform, Name: row.Name, PurchasePrice: row.PurchasePrice, ItemID: row.ItemID, ItemName: row.ItemName, Active: row.Active, CreatedBy: row.CreatedBy, CreatedByName: row.CreatedByName, CreatedAt: row.CreatedAt.Time})
	}
	return out, nil
}

// dealForLink 锁定并检查团购商品可用，返回商品及门店项目标准价。
func dealForLink(ctx context.Context, env ops.Env, dealID uuid.UUID, platform string) (dbgen.GroupbuyDeal, dbgen.Item, error) {
	deal, err := env.Q.LockGroupbuyDeal(ctx, dbgen.LockGroupbuyDealParams{StoreID: env.Caller.StoreID, ID: dealID})
	if err != nil {
		return dbgen.GroupbuyDeal{}, dbgen.Item{}, dealError(err)
	}
	if err := checkDealActive(deal); err != nil {
		return dbgen.GroupbuyDeal{}, dbgen.Item{}, err
	}
	if deal.Platform != platform {
		return dbgen.GroupbuyDeal{}, dbgen.Item{}, ops.Rule("团购平台不一致")
	}
	item, err := env.Q.GetItem(ctx, dbgen.GetItemParams{StoreID: env.Caller.StoreID, ID: deal.ItemID})
	if err != nil {
		return dbgen.GroupbuyDeal{}, dbgen.Item{}, dealError(err)
	}
	return deal, item, nil
}
