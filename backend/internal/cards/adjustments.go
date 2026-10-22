package cards

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func adjustCard(ctx context.Context, env ops.Env, in *AdjustCardInput) (*Card, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(in.Reason) < 1 || utf8.RuneCountInString(in.Reason) > 200 {
		return nil, ops.Rule("原因须为一到二百字")
	}
	card, err := env.Q.LockCard(ctx, dbgen.LockCardParams{StoreID: env.Caller.StoreID, ID: in.CardID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := canView(ctx, env, card.CustomerID); err != nil {
		return nil, err
	}
	before, err := cardView(ctx, env, card.ID)
	if err != nil {
		return nil, err
	}
	previous, err := env.Q.LatestCardLedger(ctx, card.ID)
	if err != nil {
		return nil, err
	}
	principal, bonus := previous.PrincipalAfter, previous.BonusAfter
	if in.Principal != nil {
		principal = *in.Principal
	}
	if in.Bonus != nil {
		bonus = *in.Bonus
	}
	// 金额比较使用规范化文本，避免 decimal 内部表示影响相等判断。
	balanceChanged := principal.String() != previous.PrincipalAfter.String() || bonus.String() != previous.BonusAfter.String()
	expires := card.ExpiresOn
	if in.Expiry != nil {
		expires = pgtype.Date{}
		if in.Expiry.ExpiresOn != nil {
			date, err := time.Parse(time.DateOnly, *in.Expiry.ExpiresOn)
			if err != nil {
				return nil, ops.Rule("有效期日期无效")
			}
			// 调整单用于更正历史，有效期可以早于当前营业日。
			expires = pgtype.Date{Time: date, Valid: true}
		}
	}
	expiryChanged := expires.Valid != card.ExpiresOn.Valid || (expires.Valid && !expires.Time.Equal(card.ExpiresOn.Time))
	typeID := card.CardTypeID
	var typeBefore, typeAfter *uuid.UUID
	if in.CardTypeID != nil && *in.CardTypeID != card.CardTypeID {
		// 固定锁序：卡 → 新卡类型；开卡只锁类型，类型编辑也只锁类型。
		ct, err := env.Q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: env.Caller.StoreID, ID: *in.CardTypeID})
		if err != nil {
			return nil, rowError(err)
		}
		old, err := env.Q.GetCardType(ctx, dbgen.GetCardTypeParams{StoreID: env.Caller.StoreID, ID: card.CardTypeID})
		if err != nil {
			return nil, err
		}
		if old.Kind != ct.Kind {
			return nil, ops.Rule("卡类型必须同类")
		}
		if !ct.Active {
			return nil, ops.Rule("卡类型已停用")
		}
		typeID = ct.ID
		typeBefore, typeAfter = &card.CardTypeID, &typeID
	}
	if !balanceChanged && !expiryChanged && typeAfter == nil {
		return nil, ops.Rule("调整单没有任何改动")
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	var ledgerID *uuid.UUID
	if balanceChanged {
		row, err := env.Q.AddCardLedger(ctx, dbgen.AddCardLedgerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, Kind: "adjust", PrincipalBefore: previous.PrincipalAfter, PrincipalDelta: principal.Sub(previous.PrincipalAfter), PrincipalAfter: principal, BonusBefore: previous.BonusAfter, BonusDelta: bonus.Sub(previous.BonusAfter), BonusAfter: bonus, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID})
		if err != nil {
			return nil, err
		}
		ledgerID = &row.ID
	}
	if expiryChanged || typeAfter != nil {
		if err := env.Q.UpdateAdjustedCard(ctx, dbgen.UpdateAdjustedCardParams{ID: card.ID, ExpiresOn: expires, CardTypeID: typeID}); err != nil {
			return nil, err
		}
	}
	var expiresBefore, expiresAfter pgtype.Date
	if expiryChanged {
		expiresBefore, expiresAfter = card.ExpiresOn, expires
	}
	_, err = env.Q.AddCardAdjustment(ctx, dbgen.AddCardAdjustmentParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, Reason: in.Reason, ExpiresBefore: expiresBefore, ExpiresAfter: expiresAfter, ExpiryChanged: expiryChanged, CardTypeBefore: typeBefore, CardTypeAfter: typeAfter, LedgerID: ledgerID, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	out, err := cardView(ctx, env, card.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "adjust_card", audit.CardTarget(card.ID, card.CustomerID), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func listCardAdjustments(ctx context.Context, env ops.Env, in *ListCardAdjustmentsInput) (*CardAdjustmentList, error) {
	card, err := env.Q.GetCard(ctx, dbgen.GetCardParams{StoreID: env.Caller.StoreID, ID: in.CardID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := canView(ctx, env, card.CustomerID); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := env.Q.ListCardAdjustments(ctx, dbgen.ListCardAdjustmentsParams{CardID: card.ID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := &CardAdjustmentList{Entries: []CardAdjustment{}}
	for _, r := range rows {
		a := r.CardAdjustment
		item := CardAdjustment{ID: a.ID, Reason: a.Reason, BusinessDate: a.BusinessDate.Time.Format(time.DateOnly), At: a.At.Time, Actor: CardLedgerActor{ID: a.ActorID, Name: r.ActorName}}
		if a.ExpiryChanged {
			item.ExpiryBefore = &CardExpiry{ExpiresOn: dateView(a.ExpiresBefore)}
			item.ExpiryAfter = &CardExpiry{ExpiresOn: dateView(a.ExpiresAfter)}
		}
		if a.CardTypeBefore != nil {
			item.CardTypeBefore = &CardAdjustmentType{ID: *a.CardTypeBefore, Name: *r.TypeBeforeName}
			item.CardTypeAfter = &CardAdjustmentType{ID: *a.CardTypeAfter, Name: *r.TypeAfterName}
		}
		if a.LedgerID != nil {
			item.Balance = &CardAdjustmentBalance{PrincipalBefore: r.PrincipalBefore, PrincipalDelta: r.PrincipalDelta, PrincipalAfter: r.PrincipalAfter, BonusBefore: r.BonusBefore, BonusDelta: r.BonusDelta, BonusAfter: r.BonusAfter}
		}
		out.Entries = append(out.Entries, item)
	}
	return out, nil
}
