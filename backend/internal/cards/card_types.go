package cards

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func dateView(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	return new(d.Time.Format(time.DateOnly))
}
func typeView(r dbgen.CardType) CardType {
	return CardType{ID: r.ID, Name: r.Name, Kind: r.Kind, ValidityKind: r.ValidityKind, ValidityDate: dateView(r.ValidityDate), ValidityDays: r.ValidityDays, Active: r.Active, CreatedAt: r.CreatedAt.Time}
}
func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "card_types_store_id_name_key" {
		return ops.Rule("卡类型名称已存在")
	}
	return err
}
func validateType(in *CreateCardTypeInput) (pgtype.Date, error) {
	var date pgtype.Date
	if in.Kind != StoredValue && in.Kind != CountCard && in.Kind != Package {
		return date, ops.Rule("卡种类无效")
	}
	if len(in.Items) > 20 || (in.Kind == StoredValue && len(in.Items) != 0) || (in.Kind != StoredValue && len(in.Items) == 0) {
		return date, ops.Rule("储值卡不能含项目，次卡和套餐须含一到二十个项目")
	}
	seen := map[uuid.UUID]bool{}
	for _, x := range in.Items {
		if seen[x.ItemID] || x.PaidCount <= 0 || x.GiftCount < 0 {
			return date, ops.Rule("项目不能重复，购买次数须大于零，赠送次数不能为负")
		}
		seen[x.ItemID] = true
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return date, ops.Rule("请填写卡类型名称")
	}
	switch in.ValidityKind {
	case "none":
		if in.ValidityDate != nil || in.ValidityDays != nil {
			return date, ops.Rule("不限有效期不能填写日期或天数")
		}
	case "fixed_date":
		if in.ValidityDate == nil || in.ValidityDays != nil {
			return date, ops.Rule("固定日期必须填写日期，不能填写天数")
		}
		d, err := time.Parse(time.DateOnly, *in.ValidityDate)
		if err != nil {
			return date, ops.Rule("有效期日期无效")
		}
		date = pgtype.Date{Time: d, Valid: true}
	case "days_after_open":
		if in.ValidityDate != nil || in.ValidityDays == nil || *in.ValidityDays <= 0 {
			return date, ops.Rule("开卡后天数必须大于零，不能填写固定日期")
		}
	default:
		return date, ops.Rule("有效期规则无效")
	}
	return date, nil
}
func createCardType(ctx context.Context, env ops.Env, in *CreateCardTypeInput) (*CardType, error) {
	date, err := validateType(in)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CreateCardType(ctx, dbgen.CreateCardTypeParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name, Kind: in.Kind, ValidityKind: in.ValidityKind, ValidityDate: date, ValidityDays: in.ValidityDays})
	if err != nil {
		return nil, rowError(err)
	}
	if err := changeTypeItems(ctx, env, row.ID, nil, in.Items); err != nil {
		return nil, err
	}
	views := []CardType{typeView(row)}
	if err := typeItems(ctx, env, views); err != nil {
		return nil, err
	}
	out := views[0]
	if err := audit.Record(ctx, env, "create_card_type", audit.CardTypeTarget(row.ID), nil, out); err != nil {
		return nil, err
	}
	return &out, nil
}
func updateCardType(ctx context.Context, env ops.Env, in *UpdateCardTypeInput) (*CardType, error) {
	date, err := validateType(&in.CreateCardTypeInput)
	if err != nil {
		return nil, err
	}
	// 会员价写入按项目 → 卡类型加锁；类型编辑也先共享锁住传入项目，避免反向等待。
	// 此处只锁行，停用检查仍由 changeTypeItems 仅对新增或改动的引用执行。
	itemIDs := make([]uuid.UUID, len(in.Items))
	for i, item := range in.Items {
		itemIDs[i] = item.ItemID
	}
	if _, err := catalog.LockItems(ctx, env.Q, env.Caller.StoreID, itemIDs...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.Rule("项目必须是本店启用的项目")
		}
		return nil, err
	}
	before, err := env.Q.LockCardType(ctx, dbgen.LockCardTypeParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	beforeViews := []CardType{typeView(before)}
	if err := typeItems(ctx, env, beforeViews); err != nil {
		return nil, err
	}
	if before.Kind != in.Kind {
		exists, err := env.Q.CardTypeHasCards(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, ops.Rule("已有卡的卡类型不能改变种类")
		}
		rules, err := env.Q.CardTypeHasUpgradeRules(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if rules {
			return nil, ops.Rule("升级规则引用的卡类型不能改变种类")
		}
	}
	if err := changeTypeItems(ctx, env, in.ID, beforeViews[0].Items, in.Items); err != nil {
		return nil, err
	}
	row, err := env.Q.UpdateCardType(ctx, dbgen.UpdateCardTypeParams{ID: in.ID, Name: in.Name, Kind: in.Kind, ValidityKind: in.ValidityKind, ValidityDate: date, ValidityDays: in.ValidityDays, Active: in.Active})
	if err != nil {
		return nil, rowError(err)
	}
	views := []CardType{typeView(row)}
	if err := typeItems(ctx, env, views); err != nil {
		return nil, err
	}
	out := views[0]
	if err := audit.Record(ctx, env, "update_card_type", audit.CardTypeTarget(row.ID), beforeViews[0], out); err != nil {
		return nil, err
	}
	return &out, nil
}
