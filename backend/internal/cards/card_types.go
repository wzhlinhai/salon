package cards

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
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
	out := typeView(row)
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
	before, err := env.Q.LockCardType(ctx, dbgen.LockCardTypeParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	row, err := env.Q.UpdateCardType(ctx, dbgen.UpdateCardTypeParams{ID: in.ID, Name: in.Name, Kind: in.Kind, ValidityKind: in.ValidityKind, ValidityDate: date, ValidityDays: in.ValidityDays, Active: in.Active})
	if err != nil {
		return nil, rowError(err)
	}
	out := typeView(row)
	if err := audit.Record(ctx, env, "update_card_type", audit.CardTypeTarget(row.ID), typeView(before), out); err != nil {
		return nil, err
	}
	return &out, nil
}
